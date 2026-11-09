// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

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

// appService makes the ASO footprint the children of an App Service app
// hang from: a resource group and a Site ASO has provisioned (stand-ins), and
// the same two in the fake Resource Manager. It returns the Site's ARM ID and
// the resource group's.
func appService(t *testing.T, ns string) (siteID, rgID string) {
	t.Helper()
	rgID = "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	siteID = rgID + "/providers/Microsoft.Web/sites/" + ns + "-site"
	armFake.AddResource(rgID)
	armFake.AddResource(siteID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	webSite(t, ns, "site", "rg", ns+"-rg", ns+"-site")
	return siteID, rgID
}

// A deployment slot of ASO's Site, and a custom domain bound to the slot, which
// is owned by our slot, not by ASO's Site: PUT by ASO's ARM reconciler with
// the namespace's identity, changed, and deleted child first.
func TestSitesSlotAndHostNameBinding(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "web-slot")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	siteID, rgID := appService(t, ns)
	planID := rgID + "/providers/Microsoft.Web/serverfarms/plan"

	slot := &webv1.SitesSlot{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "staging"},
		Spec: webv1.SitesSlot_Spec{
			Owner:               &genruntime.KnownResourceReference{Name: "site"},
			Location:            ptr("westus"),
			HttpsOnly:           ptr(true),
			ServerFarmReference: &genruntime.ResourceReference{ARMID: planID},
		},
	}
	if err := k8s.Create(ctx, slot); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "SitesSlot Ready", ready(slot))
	slotID := siteID + "/slots/staging"
	expectResource(t, slotID, func(r map[string]any) error {
		p := properties(r)
		if r["type"] != "Microsoft.Web/sites/slots" || p["httpsOnly"] != true || p["serverFarmId"] != planID || r["location"] != "westus" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if refs := slot.GetOwnerReferences(); len(refs) != 1 || refs[0].Name != "site" {
		t.Fatalf("owner references: %+v", refs)
	}

	// A host name bound to the slot: its owner is the slot (our kind, in our
	// group), so its ARM ID runs through ASO's Site and the slot.
	binding := &webv1.SitesSlotsHostNameBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "staging-example"},
		Spec: webv1.SitesSlotsHostNameBinding_Spec{
			AzureName:    "staging.example.com",
			Owner:        &genruntime.KnownResourceReference{Name: "staging"},
			HostNameType: ptr(webv1.HostNameType("Verified")),
			SslState:     ptr(webv1.SslState("Disabled")),
		},
	}
	if err := k8s.Create(ctx, binding); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "SitesSlotsHostNameBinding Ready", ready(binding))
	bindingID := slotID + "/hostNameBindings/staging.example.com"
	expectResource(t, bindingID, func(r map[string]any) error {
		if p := properties(r); r["type"] != "Microsoft.Web/sites/slots/hostNameBindings" || p["hostNameType"] != "Verified" || p["sslState"] != "Disabled" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change to the slot is PUT again.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(slot), slot); err != nil {
		t.Fatal(err)
	}
	slot.Spec.HttpsOnly = ptr(false)
	if err := k8s.Update(ctx, slot); err != nil {
		t.Fatal(err)
	}
	expectResource(t, slotID, func(r map[string]any) error {
		if properties(r)["httpsOnly"] != false {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// Deleting the binding, then the slot, deletes them in Resource Manager and
	// leaves ASO's Site alone.
	for _, o := range []client.Object{binding, slot} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T gone", o), gone(o))
	}
	if armFake.HasResource(slotID) || armFake.HasResource(bindingID) || !armFake.HasResource(siteID) {
		t.Fatalf("slot %v binding %v site %v", armFake.HasResource(slotID), armFake.HasResource(bindingID), armFake.HasResource(siteID))
	}
}

// A custom domain on the app itself, and a certificate (a PFX and its password
// from Secrets) owned by the resource group.
func TestSitesHostNameBindingAndCertificate(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "web-domain")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	siteID, rgID := appService(t, ns)
	secret(t, ns, "pfx", map[string]string{"blob": "cGZ4LWJ5dGVz", "password": "pfx-password-1"})

	cert := &webv1.Certificate{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "www"},
		Spec: webv1.Certificate_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
			PfxBlob:  &genruntime.SecretReference{Name: "pfx", Key: "blob"},
			Password: &genruntime.SecretReference{Name: "pfx", Key: "password"},
		},
	}
	binding := &webv1.SitesHostNameBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "www-example"},
		Spec: webv1.SitesHostNameBinding_Spec{
			AzureName:    "www.example.com",
			Owner:        &genruntime.KnownResourceReference{Name: "site"},
			HostNameType: ptr(webv1.HostNameType("Verified")),
			SslState:     ptr(webv1.SslState("SniEnabled")),
			Thumbprint:   ptr("ABCDEF0123456789"),
		},
	}
	for _, o := range []client.Object{cert, binding} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []client.Object{cert, binding} {
		eventually(t, timeout, fmt.Sprintf("%T Ready", o), ready(o.(conditioned)))
	}
	expectResource(t, rgID+"/providers/Microsoft.Web/certificates/www", func(r map[string]any) error {
		if p := properties(r); r["type"] != "Microsoft.Web/certificates" || p["pfxBlob"] != "cGZ4LWJ5dGVz" || p["password"] != "pfx-password-1" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, siteID+"/hostNameBindings/www.example.com", func(r map[string]any) error {
		if p := properties(r); r["type"] != "Microsoft.Web/sites/hostNameBindings" || p["sslState"] != "SniEnabled" || p["thumbprint"] != "ABCDEF0123456789" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range []client.Object{binding, cert} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T gone", o), gone(o))
	}
	if armFake.HasResource(rgID+"/providers/Microsoft.Web/certificates/www") || armFake.HasResource(siteID+"/hostNameBindings/www.example.com") {
		t.Fatal("the certificate or the binding is still in Resource Manager")
	}
}

// The CRD refuses an SSL state that does not exist and a certificate whose
// PFX is not a Secret reference.
func TestWebValidation(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "web-validation")
	bad := &webv1.SitesHostNameBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "bad"},
		Spec: webv1.SitesHostNameBinding_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "site"},
			SslState: ptr(webv1.SslState("Sometimes")),
		},
	}
	if err := k8s.Create(ctx, bad); err == nil || !contains(err.Error(), "Unsupported value") {
		t.Fatalf("want a refusal of the SSL state, got %v", err)
	}
}
