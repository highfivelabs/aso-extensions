// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"testing"

	asoweb "github.com/Azure/azure-service-operator/v2/api/web/v20260715/storage"

	webv1 "github.com/highfivelabs/aso-extensions/apis/arm/web/v20260715"
)

// An App Service app's sidecar containers (docs/plan.md f40): a Linux app
// in sitecontainers mode on a Basic B1 plan (about $0.02 an hour; Standard
// S1 had no quota in eastus2, 2026-11-12), its main container from a public
// Microsoft image, a sidecar beside it, the main container's start-up
// command changed, both deleted. Functions, premier add-ons, private access
// and the slot children are not run live: a function needs a function app
// and a deployment, an add-on a Marketplace purchase, private access an App
// Service Environment, and a slot a Standard plan.
func TestLiveSiteContainers(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.Web")
	ns := namespace(t, "sidecars")
	var cands []candidate
	for _, region := range regions() {
		cands = append(cands, candidate{
			id: rgID() + "/providers/Microsoft.Web/serverfarms/" + azName(fmt.Sprintf("scplan%d", len(cands))),
			body: map[string]any{
				"location": region, "kind": "linux", "sku": map[string]any{"name": "B1", "tier": "Basic"},
				"properties": map[string]any{"reserved": true},
			},
		})
	}
	i, _ := fixtureFirst(t, timeout, "2023-12-01", cands)
	planID, webRegion := cands[i].id, fmt.Sprint(cands[i].body["location"])
	siteName := azName("sidecars")
	siteID := rgID() + "/providers/Microsoft.Web/sites/" + siteName
	fixture(t, siteID, webAPI, map[string]any{
		"location": webRegion, "kind": "app,linux",
		"properties": map[string]any{"serverFarmId": planID, "httpsOnly": true, "siteConfig": map[string]any{"linuxFxVersion": "sitecontainers"}},
	})
	site := &asoweb.Site{}
	site.Namespace, site.Name = ns, "site"
	site.Spec.AzureName, site.Spec.Owner = siteName, owner("rg")
	standIn(t, site, siteID, nil)

	primary := &webv1.SitesSitecontainer{}
	primary.Namespace, primary.Name = ns, "main"
	primary.Spec = webv1.SitesSitecontainer_Spec{
		AzureName: "main", Owner: owner("site"), Image: ptr("mcr.microsoft.com/azuredocs/aci-helloworld:latest"),
		IsMain: ptr(true), TargetPort: ptr("80"), AuthType: ptr(webv1.AuthType("Anonymous")),
	}
	sidecar := &webv1.SitesSitecontainer{}
	sidecar.Namespace, sidecar.Name = ns, "sidecar"
	sidecar.Spec = webv1.SitesSitecontainer_Spec{
		AzureName: "sidecar", Owner: owner("site"), Image: ptr("mcr.microsoft.com/azurelinux/base/nginx:1"),
		IsMain: ptr(false), TargetPort: ptr("8080"), AuthType: ptr(webv1.AuthType("Anonymous")),
		EnvironmentVariables: []webv1.EnvironmentVariable{{Name: ptr("ROLE"), Value: ptr("SIDECAR_ROLE")}},
	}
	create(t, timeout, primary, sidecar)
	mainID, sidecarID := siteID+"/sitecontainers/main", siteID+"/sitecontainers/sidecar"
	expectResource(t, mainID, webAPI, func(r map[string]any) error {
		if p := properties(r); p["isMain"] != true || p["image"] != "mcr.microsoft.com/azuredocs/aci-helloworld:latest" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, sidecarID, webAPI, func(r map[string]any) error {
		if p := properties(r); p["isMain"] != false {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	update(t, primary, func(c *webv1.SitesSitecontainer) {
		c.Spec.StartUpCommand = ptr("node /usr/src/app/index.js")
	})
	eventually(t, timeout, "SitesSitecontainer Ready after the change", ready(primary))
	expectResource(t, mainID, webAPI, func(r map[string]any) error {
		if properties(r)["startUpCommand"] != "node /usr/src/app/index.js" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	remove(t, timeout, sidecar)
	expectGone(t, sidecarID, webAPI)
	remove(t, timeout, primary)
	expectGone(t, mainID, webAPI)
}
