// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	webv1 "github.com/highfivelabs/aso-extensions/apis/arm/web/v20260715"
)

// The children of docs/plan.md f40, on ASO's Site and on our slot: sidecar
// containers (a registry password from a Secret, never in the status; one
// main container per app or slot, a second refused), a function, private
// access naming a network by ARM ID, and a slot's own config sections and
// publishing policies (fixed names, no DELETE: deleting the object leaves
// them with the slot). A change is PUT again; the rest is deleted.
func TestWebSiteAndSlotChildren(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "web-children")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	siteID, rgID := appService(t, ns)
	planID := rgID + "/providers/Microsoft.Web/serverfarms/plan"
	vnetID := rgID + "/providers/Microsoft.Network/virtualNetworks/app-net"
	armFake.AddResource(vnetID)
	secret(t, ns, "registry", map[string]string{"password": "registry-password-1"})
	site := &genruntime.KnownResourceReference{Name: "site"}

	slot := &webv1.SitesSlot{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "staging"},
		Spec: webv1.SitesSlot_Spec{
			Owner: site, Location: ptr("westus"),
			ServerFarmReference: &genruntime.ResourceReference{ARMID: planID},
		},
	}
	if err := k8s.Create(ctx, slot); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "SitesSlot Ready", ready(slot))
	slotID := siteID + "/slots/staging"
	onSlot := &genruntime.KnownResourceReference{Name: "staging"}

	mainContainer := &webv1.SitesSitecontainer{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "main"},
		Spec: webv1.SitesSitecontainer_Spec{
			Owner: site, Image: ptr("contoso.azurecr.io/shop:1.0"), IsMain: ptr(true), TargetPort: ptr("8080"),
			AuthType: ptr(webv1.AuthType("UserCredentials")), UserName: ptr("contoso"),
			PasswordSecret:       &genruntime.SecretReference{Name: "registry", Key: "password"},
			EnvironmentVariables: []webv1.EnvironmentVariable{{Name: ptr("DB"), Value: ptr("DB_CONNECTION")}},
		},
	}
	sidecar := &webv1.SitesSitecontainer{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "otel"},
		Spec: webv1.SitesSitecontainer_Spec{
			Owner: site, Image: ptr("otel/opentelemetry-collector:0.110.0"), IsMain: ptr(false),
			AuthType: ptr(webv1.AuthType("Anonymous")),
		},
	}
	function := &webv1.SitesFunction{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "hello"},
		Spec: webv1.SitesFunction_Spec{
			Owner: site, Language: ptr("node"), IsDisabled: ptr(false),
			Files: map[string]string{"index.js": "module.exports = async () => ({ body: 'hello' });"},
		},
	}
	access := &webv1.SitesPrivateAccess{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "access"},
		Spec: webv1.SitesPrivateAccess_Spec{
			Owner: site, Enabled: ptr(true),
			VirtualNetworks: []webv1.PrivateAccessVirtualNetwork{{
				Name: ptr("app-net"), ResourceReference: &genruntime.ResourceReference{ARMID: vnetID},
				Subnets: []webv1.PrivateAccessSubnet{{Name: ptr("apps")}},
			}},
		},
	}
	slotContainer := &webv1.SitesSlotsSitecontainer{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "staging-main"},
		Spec: webv1.SitesSlotsSitecontainer_Spec{
			AzureName: "main", Owner: onSlot, Image: ptr("contoso.azurecr.io/shop:1.1"), IsMain: ptr(true), TargetPort: ptr("8080"),
		},
	}
	slotLogs := &webv1.SitesSlotsConfigLogs{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "staging-logs"},
		Spec: webv1.SitesSlotsConfigLogs_Spec{
			Owner:                 onSlot,
			DetailedErrorMessages: &webv1.EnabledConfig{Enabled: ptr(true)},
		},
	}
	slotFtp := &webv1.SitesSlotsBasicPublishingCredentialsPolicyFtp{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "staging-ftp"},
		Spec:       webv1.SitesSlotsBasicPublishingCredentialsPolicyFtp_Spec{Owner: onSlot, Allow: ptr(false)},
	}
	objs := []conditioned{mainContainer, sidecar, function, access, slotContainer, slotLogs, slotFtp}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}

	expectResource(t, siteID+"/sitecontainers/main", func(r map[string]any) error {
		p := properties(r)
		env, _ := p["environmentVariables"].([]any)
		if r["type"] != "Microsoft.Web/sites/sitecontainers" || p["isMain"] != true || p["passwordSecret"] != "registry-password-1" ||
			p["image"] != "contoso.azurecr.io/shop:1.0" || len(env) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, siteID+"/sitecontainers/otel", func(r map[string]any) error {
		if properties(r)["isMain"] != false {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, siteID+"/functions/hello", func(r map[string]any) error {
		files, _ := properties(r)["files"].(map[string]any)
		if r["type"] != "Microsoft.Web/sites/functions" || files["index.js"] == nil {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, siteID+"/privateAccess/virtualNetworks", func(r map[string]any) error {
		networks, _ := properties(r)["virtualNetworks"].([]any)
		if len(networks) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		if n, _ := networks[0].(map[string]any); n["resourceId"] != vnetID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, slotID+"/sitecontainers/main", func(r map[string]any) error {
		if r["type"] != "Microsoft.Web/sites/slots/sitecontainers" || properties(r)["image"] != "contoso.azurecr.io/shop:1.1" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, slotID+"/config/logs", func(r map[string]any) error {
		if r["type"] != "Microsoft.Web/sites/slots/config" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, slotID+"/basicPublishingCredentialsPolicies/ftp", func(r map[string]any) error {
		if r["type"] != "Microsoft.Web/sites/slots/basicPublishingCredentialsPolicies" || properties(r)["allow"] != false {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// The status never holds the registry password.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(mainContainer), mainContainer); err != nil {
		t.Fatal(err)
	}
	if raw := fmt.Sprintf("%+v", mainContainer.Status); contains(raw, "registry-password-1") {
		t.Fatalf("the password is in the container's status: %s", raw)
	}

	// A new image is PUT again.
	mainContainer.Spec.Image = ptr("contoso.azurecr.io/shop:1.1")
	if err := k8s.Update(ctx, mainContainer); err != nil {
		t.Fatal(err)
	}
	expectResource(t, siteID+"/sitecontainers/main", func(r map[string]any) error {
		if properties(r)["image"] != "contoso.azurecr.io/shop:1.1" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A second main container on the site is refused.
	second := &webv1.SitesSitecontainer{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "second"},
		Spec:       webv1.SitesSitecontainer_Spec{Owner: site, Image: ptr("httpd:2.4"), IsMain: ptr(true)},
	}
	if err := k8s.Create(ctx, second); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "a second main container refused", notReadyWith(second, "already has a main container"))

	// Deleting: the containers and the function go; private access and the
	// slot's sections and policy have no DELETE, so their extension leaves
	// them with their app (the slot's go when the slot does).
	for _, o := range []client.Object{second, sidecar, mainContainer, function, access, slotContainer, slotLogs, slotFtp, slot} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, id := range []string{siteID + "/sitecontainers/main", siteID + "/sitecontainers/otel", siteID + "/functions/hello", slotID, slotID + "/config/logs"} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in the fake", id)
		}
	}
	if !armFake.HasResource(siteID + "/privateAccess/virtualNetworks") {
		t.Fatal("private access is gone from the fake; it has no DELETE and stays with the app")
	}
}
