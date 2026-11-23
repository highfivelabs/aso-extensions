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

// The App Service leftovers of docs/plan.md f53, on ASO's Site stand-in and
// our SitesSlot: an app's certificate from a PFX and its password (from a
// Secret, never in the status) and a slot's from a Key Vault secret; a
// slot's source control; the gateway of the app's gateway-required
// virtual network integration (no DELETE: detached); a Standard static web
// app's basic authentication (its password from a Secret; no DELETE:
// detached); an AI gateway; and a Kubernetes environment on a custom
// location. A certificate with no source, a gateway not named primary,
// basic authentication on a Free static web app and a Kubernetes
// environment on a missing custom location are refused (the fake's rules,
// webextras.go); a change is PUT again; deleted, the rest goes.
func TestAppServiceLeftovers(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "web-left")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	siteID, rgID := appService(t, ns)
	planID := rgID + "/providers/Microsoft.Web/serverfarms/plan"
	vnetID := rgID + "/providers/Microsoft.Network/virtualNetworks/app-net"
	vaultID := rgID + "/providers/Microsoft.KeyVault/vaults/kv"
	locationID := rgID + "/providers/Microsoft.ExtendedLocation/customLocations/arc"
	for _, id := range []string{planID, vnetID, vaultID, locationID} {
		armFake.AddResource(id)
	}
	secret(t, ns, "web", map[string]string{"pfx": "MIIKPfxBlobBase64", "pfx-password": "Pfx-Passw0rd", "basic": "Basic-Passw0rd"})
	site := &genruntime.KnownResourceReference{Name: "site"}
	onRG := &genruntime.KnownResourceReference{Name: "rg"}
	detach := map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}

	slot := &webv1.SitesSlot{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "staging"},
		Spec:       webv1.SitesSlot_Spec{Owner: site, Location: ptr("westus"), ServerFarmReference: &genruntime.ResourceReference{ARMID: planID}},
	}
	vnetConn := &webv1.SitesVirtualNetworkConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "app-net"},
		Spec:       webv1.SitesVirtualNetworkConnection_Spec{Owner: site, VnetResourceReference: &genruntime.ResourceReference{ARMID: vnetID}},
	}
	standard := &webv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "docs"},
		Spec:       webv1.StaticSite_Spec{Owner: onRG, Location: ptr("westus2"), Sku: &webv1.SkuDescription{Name: ptr("Standard"), Tier: ptr("Standard")}},
	}
	free := &webv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "free"},
		Spec:       webv1.StaticSite_Spec{Owner: onRG, Location: ptr("westus2"), Sku: &webv1.SkuDescription{Name: ptr("Free"), Tier: ptr("Free")}},
	}
	parents := []conditioned{slot, vnetConn, standard, free}
	for _, o := range parents {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range parents {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}

	cert := &webv1.SitesCertificate{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "tls"},
		Spec: webv1.SitesCertificate_Spec{
			Owner: site, Location: ptr("westus"), HostNames: []string{"shop.example.com"},
			PfxBlob:  &genruntime.SecretReference{Name: "web", Key: "pfx"},
			Password: &genruntime.SecretReference{Name: "web", Key: "pfx-password"},
		},
	}
	slotCert := &webv1.SitesSlotsCertificate{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "staging-tls"},
		Spec: webv1.SitesSlotsCertificate_Spec{
			AzureName: "tls", Owner: &genruntime.KnownResourceReference{Name: "staging"}, Location: ptr("westus"),
			KeyVaultReference: &genruntime.ResourceReference{ARMID: vaultID}, KeyVaultSecretName: ptr("staging-tls"),
		},
	}
	source := &webv1.SitesSlotsSourcecontrol{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "staging-source"},
		Spec: webv1.SitesSlotsSourcecontrol_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "staging"}, RepoUrl: ptr("https://github.com/contoso/shop"),
			Branch: ptr("release"), IsManualIntegration: ptr(true),
		},
	}
	gateway := &webv1.SitesVirtualNetworkConnectionsGateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "primary", Annotations: detach},
		Spec: webv1.SitesVirtualNetworkConnectionsGateway_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "app-net"}, VnetName: ptr("app-net"),
			VpnPackageUri: ptr("https://contoso.blob.core.windows.net/vpn/package.zip"),
		},
	}
	basic := &webv1.StaticSitesBasicAuth{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "docs-auth", Annotations: detach},
		Spec: webv1.StaticSitesBasicAuth_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "docs"}, ApplicableEnvironmentsMode: ptr("StagingEnvironments"),
			Password: &genruntime.SecretReference{Name: "web", Key: "basic"},
		},
	}
	aiGateway := &webv1.AiGateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "ai"},
		Spec:       webv1.AiGateway_Spec{Owner: onRG, Location: ptr("westus"), Tags: map[string]string{"team": "shop"}},
	}
	kube := &webv1.KubeEnvironment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "arc"},
		Spec: webv1.KubeEnvironment_Spec{
			Owner: onRG, Location: ptr("westus"), Kind: ptr("kubernetes"),
			ExtendedLocation: &webv1.ExtendedLocation{Name: ptr(locationID)}, StaticIp: ptr("10.0.0.10"),
		},
	}
	objs := []conditioned{cert, slotCert, source, gateway, basic, aiGateway, kube}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}

	slotID := siteID + "/slots/staging"
	staticID := rgID + "/providers/Microsoft.Web/staticSites/docs"
	expectResource(t, siteID+"/certificates/tls", func(r map[string]any) error {
		if p := properties(r); p["pfxBlob"] != "MIIKPfxBlobBase64" || p["password"] != "Pfx-Passw0rd" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, slotID+"/certificates/tls", func(r map[string]any) error {
		if p := properties(r); p["keyVaultId"] != vaultID || p["keyVaultSecretName"] != "staging-tls" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, slotID+"/sourcecontrols/web", func(r map[string]any) error {
		if p := properties(r); p["repoUrl"] != "https://github.com/contoso/shop" || p["branch"] != "release" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, siteID+"/virtualNetworkConnections/app-net/gateways/primary", func(r map[string]any) error {
		if properties(r)["vnetName"] != "app-net" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, staticID+"/basicAuth/default", func(r map[string]any) error {
		if p := properties(r); p["password"] != "Basic-Passw0rd" || p["applicableEnvironmentsMode"] != "StagingEnvironments" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, rgID+"/providers/Microsoft.Web/kubeEnvironments/arc", func(r map[string]any) error {
		ext, _ := r["extendedLocation"].(map[string]any)
		if ext["name"] != locationID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	for _, o := range []conditioned{cert, basic} {
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(o), o); err != nil {
			t.Fatal(err)
		}
	}
	for what, status := range map[string]any{"certificate": cert.Status, "basic authentication": basic.Status} {
		raw := fmt.Sprintf("%+v", status)
		for _, s := range []string{"MIIKPfxBlobBase64", "Pfx-Passw0rd", "Basic-Passw0rd"} {
			if contains(raw, s) {
				t.Fatalf("a secret is in the %s's status: %s", what, raw)
			}
		}
	}

	// A change is PUT again.
	if err := change(t, source, func(s *webv1.SitesSlotsSourcecontrol) { s.Spec.Branch = ptr("main") }); err != nil {
		t.Fatal(err)
	}
	expectResource(t, slotID+"/sourcecontrols/web", func(r map[string]any) error {
		if properties(r)["branch"] != "main" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// Refused: a certificate with no source, a gateway not named primary,
	// basic authentication on a Free static web app, a Kubernetes environment
	// on a missing custom location.
	bare := &webv1.SitesCertificate{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "bare"},
		Spec:       webv1.SitesCertificate_Spec{Owner: site, Location: ptr("westus"), HostNames: []string{"bare.example.com"}},
	}
	backup := &webv1.SitesVirtualNetworkConnectionsGateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "backup", Annotations: detach},
		Spec: webv1.SitesVirtualNetworkConnectionsGateway_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "app-net"}, VnetName: ptr("app-net"),
			VpnPackageUri: ptr("https://contoso.blob.core.windows.net/vpn/package.zip"),
		},
	}
	freeAuth := &webv1.StaticSitesBasicAuth{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "free-auth", Annotations: detach},
		Spec: webv1.StaticSitesBasicAuth_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "free"}, ApplicableEnvironmentsMode: ptr("AllEnvironments"),
			Password: &genruntime.SecretReference{Name: "web", Key: "basic"},
		},
	}
	lost := &webv1.KubeEnvironment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "lost"},
		Spec: webv1.KubeEnvironment_Spec{
			Owner: onRG, Location: ptr("westus"),
			ExtendedLocation: &webv1.ExtendedLocation{Name: ptr(rgID + "/providers/Microsoft.ExtendedLocation/customLocations/none")},
		},
	}
	refusals := map[conditioned]string{bare: "A certificate comes from", backup: "only supported gateway name is primary", freeAuth: "needs the Standard plan", lost: "was not found"}
	for o := range refusals {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for o, text := range refusals {
		eventually(t, timeout, o.GetName()+" refused", notReadyWith(o, text))
	}

	for _, o := range []client.Object{bare, backup, freeAuth, lost, kube, aiGateway, basic, gateway, source, slotCert, cert} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, id := range []string{siteID + "/certificates/tls", slotID + "/certificates/tls", slotID + "/sourcecontrols/web", rgID + "/providers/Microsoft.Web/kubeEnvironments/arc", rgID + "/providers/Microsoft.Web/aigateways/ai"} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in the fake", id)
		}
	}
	for _, id := range []string{siteID + "/virtualNetworkConnections/app-net/gateways/primary", staticID + "/basicAuth/default"} {
		if !armFake.HasResource(id) {
			t.Fatalf("%s was deleted; it has no DELETE and was detached", id)
		}
	}
}
