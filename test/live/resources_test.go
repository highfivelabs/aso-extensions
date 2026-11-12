// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

//go:build live

package live

import (
	"fmt"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	authorization "github.com/highfivelabs/aso-extensions/apis/arm/authorization/v20200501"
	resources "github.com/highfivelabs/aso-extensions/apis/arm/resources/v20220201"
)

const (
	templateSpecAPI = "2022-02-01"
	rmplAPI         = "2020-05-01"
)

// A template spec and a version of it holding an empty ARM template (free:
// a template spec is stored, never run).
func TestLiveTemplateSpec(t *testing.T) {
	t.Parallel()
	ns := namespace(t, "tspec")
	spec := &resources.TemplateSpec{}
	spec.Namespace, spec.Name = ns, "spec"
	spec.Spec = resources.TemplateSpec_Spec{
		AzureName: azName("spec"), Owner: owner("rg"), Location: ptr(location),
		Description: ptr("made by an aso-extensions live test"), DisplayName: ptr("live test"),
		Tags: map[string]string{"hfl-live-run": runID},
	}
	raw := func(s string) apiextensionsv1.JSON { return apiextensionsv1.JSON{Raw: []byte(s)} }
	version := &resources.TemplateSpecsVersion{}
	version.Namespace, version.Name = ns, "v1"
	version.Spec = resources.TemplateSpecsVersion_Spec{
		AzureName: "1.0.0", Owner: owner("spec"), Location: ptr(location),
		Description: ptr("the first version"),
		MainTemplate: map[string]apiextensionsv1.JSON{
			"$schema":        raw(`"https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#"`),
			"contentVersion": raw(`"1.0.0.0"`),
			"resources":      raw(`[]`),
		},
	}
	create(t, timeout, spec, version)
	specID := rgID() + "/providers/Microsoft.Resources/templateSpecs/" + spec.Spec.AzureName
	versionID := specID + "/versions/1.0.0"
	expectResource(t, specID, templateSpecAPI, func(r map[string]any) error {
		if p := properties(r); p["displayName"] != "live test" || p["description"] != "made by an aso-extensions live test" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, versionID, templateSpecAPI, func(r map[string]any) error {
		p := properties(r)
		main, _ := p["mainTemplate"].(map[string]any)
		if p["description"] != "the first version" || main["contentVersion"] != "1.0.0.0" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	update(t, spec, func(s *resources.TemplateSpec) { s.Spec.Description = ptr("changed by an aso-extensions live test") })
	eventually(t, timeout, "TemplateSpec Ready after the change", ready(spec))
	expectResource(t, specID, templateSpecAPI, func(r map[string]any) error {
		if properties(r)["description"] != "changed by an aso-extensions live test" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, version, func(v *resources.TemplateSpecsVersion) { v.Spec.Description = ptr("the first version, changed") })
	eventually(t, timeout, "TemplateSpecsVersion Ready after the change", ready(version))
	expectResource(t, versionID, templateSpecAPI, func(r map[string]any) error {
		if properties(r)["description"] != "the first version, changed" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	remove(t, timeout, version)
	expectGone(t, versionID, templateSpecAPI)
	remove(t, timeout, spec)
	expectGone(t, specID, templateSpecAPI)
}

// A Resource Manager private link (free: it is the target a private
// endpoint for Azure Resource Manager points at; none is made here). Its
// write is a Microsoft.Authorization action, which Contributor excludes
// (refused AuthorizationFailed, live run of 2026-11-12).
func TestLiveResourceManagementPrivateLink(t *testing.T) {
	t.Parallel()
	requireAction(t, "Microsoft.Authorization/resourceManagementPrivateLinks/write")
	ns := namespace(t, "rmpl")
	link := &authorization.ResourceManagementPrivateLink{}
	link.Namespace, link.Name = ns, "link"
	link.Spec = authorization.ResourceManagementPrivateLink_Spec{AzureName: azName("rmpl"), Owner: owner("rg"), Location: ptr(location)}
	create(t, timeout, link)
	id := rgID() + "/providers/Microsoft.Authorization/resourceManagementPrivateLinks/" + link.Spec.AzureName
	expectResource(t, id, rmplAPI, func(r map[string]any) error {
		if r["location"] != location {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, link)
	expectGone(t, id, rmplAPI)
}
