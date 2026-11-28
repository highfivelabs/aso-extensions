// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	asonetwork "github.com/Azure/azure-service-operator/v2/api/network/v20250301/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"

	network "github.com/highfivelabs/aso-extensions/apis/arm/network/v20260301"
)

// Virtual network TAP (docs/plan.md f60): a TAP in a resource group mirroring
// to a collector NIC's IP configuration (by ARM ID), and a tap configuration
// on ASO's NetworkInterface (a new external owner, by name) naming the TAP by
// its object. A TAP with no destination and a tap configuration naming a
// missing TAP are refused (the fake's rules, vnettap.go); a change of port is
// PUT again, and the TAP read back lists the tap configuration; deleted, both
// go.
func TestVirtualNetworkTap(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "vnet-tap")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	nicID := rgID + "/providers/Microsoft.Network/networkInterfaces/web"
	collectorID := rgID + "/providers/Microsoft.Network/networkInterfaces/collector/ipConfigurations/ipconfig1"
	for _, id := range []string{rgID, nicID, collectorID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")

	// ASO's NIC, as ASO leaves it once provisioned.
	nic := &asonetwork.NetworkInterface{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "web",
		Annotations: map[string]string{genruntime.ResourceIDAnnotation: nicID}}}
	nic.Spec.AzureName = "web"
	nic.Spec.Location = ptr("westus")
	nic.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	if err := k8s.Create(ctx, nic); err != nil {
		t.Fatal(err)
	}
	nic.Status.Conditions = []conditions.Condition{asoReady(nic.Generation)}
	if err := k8s.Status().Update(ctx, nic); err != nil {
		t.Fatal(err)
	}

	tap := &network.VirtualNetworkTap{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "mirror"},
		Spec: network.VirtualNetworkTap_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
			DestinationNetworkInterfaceIPConfiguration: &network.NetworkInterfaceIPConfiguration_VirtualNetworkTap_SubResourceEmbedded{
				Reference: &genruntime.ResourceReference{ARMID: collectorID},
			},
			DestinationPort: ptr(4789),
		},
	}
	nowhere := &network.VirtualNetworkTap{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "nowhere"},
		Spec: network.VirtualNetworkTap_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "rg"}, Location: ptr("westus"),
		},
	}
	for _, o := range []client.Object{tap, nowhere} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "the TAP Ready", ready(tap))
	eventually(t, timeout, "a TAP with no destination refused", notReadyWith(nowhere, "has one destination"))

	// The TAP exists: the NIC's tap configuration names it by its object.
	conf := &network.NetworkInterfacesTapConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "web-mirror"},
		Spec: network.NetworkInterfacesTapConfiguration_Spec{
			AzureName: "mirror",
			Owner:     &genruntime.KnownResourceReference{Name: "web"},
			VirtualNetworkTap: &network.VirtualNetworkTapSpec{Reference: &genruntime.ResourceReference{
				Group: "network.azure.hfl.dev", Kind: "VirtualNetworkTap", Name: "mirror",
			}},
		},
	}
	missing := &network.NetworkInterfacesTapConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "web-gone"},
		Spec: network.NetworkInterfacesTapConfiguration_Spec{
			AzureName: "gone",
			Owner:     &genruntime.KnownResourceReference{Name: "web"},
			VirtualNetworkTap: &network.VirtualNetworkTapSpec{Reference: &genruntime.ResourceReference{
				ARMID: rgID + "/providers/Microsoft.Network/virtualNetworkTaps/gone",
			}},
		},
	}
	for _, o := range []client.Object{conf, missing} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "the tap configuration Ready", ready(conf))
	eventually(t, timeout, "a missing TAP refused", notReadyWith(missing, "was not found"))

	tapID := rgID + "/providers/Microsoft.Network/virtualNetworkTaps/mirror"
	confID := nicID + "/tapConfigurations/mirror"
	expectResource(t, confID, func(r map[string]any) error {
		if v, _ := properties(r)["virtualNetworkTap"].(map[string]any); v["id"] != tapID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, tapID, func(r map[string]any) error {
		p := properties(r)
		dest, _ := p["destinationNetworkInterfaceIPConfiguration"].(map[string]any)
		if dest["id"] != collectorID || p["destinationPort"] != float64(4789) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again: another VXLAN port.
	if err := change(t, tap, func(tap *network.VirtualNetworkTap) {
		tap.Spec.DestinationPort = ptr(4790)
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, tapID, func(r map[string]any) error {
		if p := properties(r); p["destinationPort"] != float64(4790) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	// The TAP read back after that PUT lists the tap configuration, which
	// Azure fills in (read-only).
	eventually(t, timeout, "the TAP's status listing the tap configuration", func() error {
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(tap), tap); err != nil {
			return err
		}
		var ids []string
		for _, c := range tap.Status.NetworkInterfaceTapConfigurations {
			ids = append(ids, deref(c.Id))
		}
		if !reflect.DeepEqual(ids, []string{confID}) || deref(tap.Status.DestinationPort) != 4790 {
			return fmt.Errorf("status: %v, port %v", ids, tap.Status.DestinationPort)
		}
		return nil
	})

	// The tap configurations first, then the TAPs, as Azure's documentation
	// removes them.
	for _, group := range [][]client.Object{{conf, missing}, {tap, nowhere}} {
		for _, o := range group {
			if err := k8s.Delete(ctx, o); err != nil {
				t.Fatal(err)
			}
		}
		for _, o := range group {
			eventually(t, timeout, o.GetName()+" gone", gone(o))
		}
	}
	for _, id := range []string{tapID, confID} {
		if armFake.HasResource(id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
}
