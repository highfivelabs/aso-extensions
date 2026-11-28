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

// An App Service Environment v3 (docs/plan.md f41) in a subnet named by ARM
// ID, its networking configuration (a fixed name with no DELETE: deleting
// the object leaves it with the environment), its custom DNS suffix (one
// without its certificate refused), and the approval of a private endpoint
// connection a private endpoint made. A change is PUT again; deleting the
// environment takes its configurations with it.
func TestAppServiceEnvironment(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "web-ase")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	subnetID := rgID + "/providers/Microsoft.Network/virtualNetworks/net/subnets/ase"
	armFake.AddResource(rgID + "/providers/Microsoft.Network/virtualNetworks/net")
	armFake.AddResource(subnetID)

	ase := &webv1.HostingEnvironment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "ase"},
		Spec: webv1.HostingEnvironment_Spec{
			AzureName: ns + "-ase", Owner: &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"), Kind: ptr("ASEv3"),
			VirtualNetwork:            &webv1.VirtualNetworkProfile{Reference: &genruntime.ResourceReference{ARMID: subnetID}},
			InternalLoadBalancingMode: ptr(webv1.LoadBalancingMode("Web, Publishing")),
			ZoneRedundant:             ptr(false),
			ClusterSettings:           []webv1.NameValuePair{{Name: ptr("DisableTls1.0"), Value: ptr("1")}},
		},
	}
	if err := k8s.Create(ctx, ase); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "HostingEnvironment Ready", ready(ase))
	aseID := rgID + "/providers/Microsoft.Web/hostingEnvironments/" + ns + "-ase"
	expectResource(t, aseID, func(r map[string]any) error {
		p := properties(r)
		network, _ := p["virtualNetwork"].(map[string]any)
		if r["type"] != "Microsoft.Web/hostingEnvironments" || r["kind"] != "ASEv3" || network["id"] != subnetID ||
			p["internalLoadBalancingMode"] != "Web, Publishing" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	onASE := &genruntime.KnownResourceReference{Name: "ase"}
	networking := &webv1.HostingEnvironmentsConfigurationNetworking{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "networking"},
		Spec: webv1.HostingEnvironmentsConfigurationNetworking_Spec{
			Owner: onASE, FtpEnabled: ptr(false), RemoteDebugEnabled: ptr(false), AllowNewPrivateEndpointConnections: ptr(true),
		},
	}
	dns := &webv1.HostingEnvironmentsConfigurationCustomDnsSuffix{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "dns"},
		Spec: webv1.HostingEnvironmentsConfigurationCustomDnsSuffix_Spec{
			Owner: onASE, DnsSuffix: ptr("apps.contoso.internal"),
			CertificateUrl: ptr("https://contoso.vault.azure.net/secrets/apps-wildcard"),
		},
	}
	connID := aseID + "/privateEndpointConnections/pe-1"
	armFake.AddResource(connID) // a private endpoint made it
	conn := &webv1.HostingEnvironmentsPrivateEndpointConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "pe-1"},
		Spec: webv1.HostingEnvironmentsPrivateEndpointConnection_Spec{
			Owner: onASE,
			PrivateLinkServiceConnectionState: &webv1.PrivateLinkConnectionState{
				Status: ptr("Approved"), Description: ptr("approved by the platform team"),
			},
		},
	}
	objs := []conditioned{networking, dns, conn}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	expectResource(t, aseID+"/configurations/networking", func(r map[string]any) error {
		if p := properties(r); r["type"] != "Microsoft.Web/hostingEnvironments/configurations" || p["ftpEnabled"] != false || p["allowNewPrivateEndpointConnections"] != true {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, aseID+"/configurations/customdnssuffix", func(r map[string]any) error {
		if p := properties(r); p["dnsSuffix"] != "apps.contoso.internal" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, connID, func(r map[string]any) error {
		state, _ := properties(r)["privateLinkServiceConnectionState"].(map[string]any)
		if state["status"] != "Approved" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again.
	if err := change(t, networking, func(networking *webv1.HostingEnvironmentsConfigurationNetworking) {
		networking.Spec.FtpEnabled = ptr(true)
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, aseID+"/configurations/networking", func(r map[string]any) error {
		if properties(r)["ftpEnabled"] != true {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A custom DNS suffix without its certificate is refused.
	if err := change(t, dns, func(dns *webv1.HostingEnvironmentsConfigurationCustomDnsSuffix) {
		dns.Spec.CertificateUrl = nil
	}); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "a suffix without its certificate refused", notReadyWith(dns, "needs its certificate"))

	// The networking configuration stays when its object goes; the rest is
	// deleted, and the environment takes its configurations with it.
	if err := k8s.Delete(ctx, networking); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "networking gone", gone(networking))
	if !armFake.HasResource(aseID + "/configurations/networking") {
		t.Fatal("the networking configuration was deleted; it has no DELETE and stays with the environment")
	}
	for _, o := range []client.Object{conn, dns, ase} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, id := range []string{aseID, aseID + "/configurations/networking", connID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in the fake", id)
		}
	}
	if !armFake.HasResource(subnetID) {
		t.Fatal("the subnet was deleted with the environment")
	}
}
