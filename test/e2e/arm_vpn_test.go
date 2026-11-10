// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	network "github.com/highfivelabs/aso-extensions/apis/arm/network/v20260301"
)

// A site-to-site VPN: the on-premises side as our LocalNetworkGateway (owned
// by ASO's ResourceGroup), and an IPsec connection from ASO's
// VirtualNetworkGateway (by ARM ID) to it (by object), with its shared key
// read from a Secret and a custom IPsec policy; then a Vnet2Vnet connection
// between two gateways. Changed, and deleted connection first.
func TestVPNConnection(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "vpn")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	hubID := rgID + "/providers/Microsoft.Network/virtualNetworkGateways/hub"
	spokeID := rgID + "/providers/Microsoft.Network/virtualNetworkGateways/spoke"
	armFake.AddResource(hubID)
	armFake.AddResource(spokeID)
	secret(t, ns, "psk", map[string]string{"key": "s2s-shared-key-1"})

	onprem := &network.LocalNetworkGateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "onprem"},
		Spec: network.LocalNetworkGateway_Spec{
			Owner:                    &genruntime.KnownResourceReference{Name: "rg"},
			Location:                 ptr("westus"),
			GatewayIpAddress:         ptr("203.0.113.10"),
			LocalNetworkAddressSpace: &network.AddressSpace{AddressPrefixes: []string{"10.20.0.0/16"}},
			BgpSettings:              &network.BgpSettings{Asn: ptr(uint32(65010)), BgpPeeringAddress: ptr("10.20.255.254")},
		},
	}
	if err := k8s.Create(ctx, onprem); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "LocalNetworkGateway Ready", ready(onprem))
	onpremID := rgID + "/providers/Microsoft.Network/localNetworkGateways/onprem"
	expectResource(t, onpremID, func(r map[string]any) error {
		p := properties(r)
		space, _ := p["localNetworkAddressSpace"].(map[string]any)
		bgp, _ := p["bgpSettings"].(map[string]any)
		if r["type"] != "Microsoft.Network/localNetworkGateways" || p["gatewayIpAddress"] != "203.0.113.10" ||
			!reflect.DeepEqual(space["addressPrefixes"], []any{"10.20.0.0/16"}) || bgp["asn"] != float64(65010) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	s2s := &network.VirtualNetworkGatewayConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "to-onprem"},
		Spec: network.VirtualNetworkGatewayConnection_Spec{
			Owner:                  &genruntime.KnownResourceReference{Name: "rg"},
			Location:               ptr("westus"),
			ConnectionType:         ptr(network.VirtualNetworkGatewayConnectionType_IPsec),
			ConnectionProtocol:     ptr(network.VirtualNetworkGatewayConnectionProtocol("IKEv2")),
			VirtualNetworkGateway1: &network.VirtualNetworkGatewaySpec{Reference: &genruntime.ResourceReference{ARMID: hubID}},
			LocalNetworkGateway2: &network.LocalNetworkGatewaySpec{Reference: &genruntime.ResourceReference{
				Group: "network.azure.hfl.dev", Kind: "LocalNetworkGateway", Name: "onprem"}},
			SharedKey: &genruntime.SecretReference{Name: "psk", Key: "key"},
			EnableBgp: ptr(true),
			IpsecPolicies: []network.IpsecPolicy{{
				SaLifeTimeSeconds:   ptr(27000),
				SaDataSizeKilobytes: ptr(102400000),
				IpsecEncryption:     ptr(network.IpsecEncryption_AES256),
				IpsecIntegrity:      ptr(network.IpsecIntegrity_SHA256),
				IkeEncryption:       ptr(network.IkeEncryption_AES256),
				IkeIntegrity:        ptr(network.IkeIntegrity_SHA256),
				DhGroup:             ptr(network.DhGroup_DHGroup14),
				PfsGroup:            ptr(network.PfsGroup_PFS2048),
			}},
		},
	}
	if err := k8s.Create(ctx, s2s); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "IPsec VirtualNetworkGatewayConnection Ready", ready(s2s))
	s2sID := rgID + "/providers/Microsoft.Network/connections/to-onprem"
	expectResource(t, s2sID, func(r map[string]any) error {
		p := properties(r)
		g1, _ := p["virtualNetworkGateway1"].(map[string]any)
		l2, _ := p["localNetworkGateway2"].(map[string]any)
		policies, _ := p["ipsecPolicies"].([]any)
		if r["type"] != "Microsoft.Network/connections" || p["connectionType"] != "IPsec" || p["enableBgp"] != true ||
			g1["id"] != hubID || l2["id"] != onpremID || len(policies) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		if _, ok := p["sharedKey"]; ok {
			return fmt.Errorf("the shared key is kept on the resource: %v", r)
		}
		return nil
	})
	if k, ok := armFake.SharedKey(s2sID); !ok || k != "s2s-shared-key-1" {
		t.Fatalf("the connection's shared key in Resource Manager: %q %v", k, ok)
	}

	// A routing weight and the on-premises prefixes changed: PUT again.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(s2s), s2s); err != nil {
		t.Fatal(err)
	}
	s2s.Spec.RoutingWeight = ptr(10)
	if err := k8s.Update(ctx, s2s); err != nil {
		t.Fatal(err)
	}
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(onprem), onprem); err != nil {
		t.Fatal(err)
	}
	onprem.Spec.LocalNetworkAddressSpace.AddressPrefixes = []string{"10.20.0.0/16", "10.21.0.0/16"}
	if err := k8s.Update(ctx, onprem); err != nil {
		t.Fatal(err)
	}
	expectResource(t, s2sID, func(r map[string]any) error {
		if properties(r)["routingWeight"] != float64(10) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, onpremID, func(r map[string]any) error {
		space, _ := properties(r)["localNetworkAddressSpace"].(map[string]any)
		if !reflect.DeepEqual(space["addressPrefixes"], []any{"10.20.0.0/16", "10.21.0.0/16"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	v2v := &network.VirtualNetworkGatewayConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "hub-to-spoke"},
		Spec: network.VirtualNetworkGatewayConnection_Spec{
			Owner:                  &genruntime.KnownResourceReference{Name: "rg"},
			Location:               ptr("westus"),
			ConnectionType:         ptr(network.VirtualNetworkGatewayConnectionType_Vnet2Vnet),
			VirtualNetworkGateway1: &network.VirtualNetworkGatewaySpec{Reference: &genruntime.ResourceReference{ARMID: hubID}},
			VirtualNetworkGateway2: &network.VirtualNetworkGatewaySpec{Reference: &genruntime.ResourceReference{ARMID: spokeID}},
			SharedKey:              &genruntime.SecretReference{Name: "psk", Key: "key"},
		},
	}
	if err := k8s.Create(ctx, v2v); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "Vnet2Vnet VirtualNetworkGatewayConnection Ready", ready(v2v))
	v2vID := rgID + "/providers/Microsoft.Network/connections/hub-to-spoke"
	expectResource(t, v2vID, func(r map[string]any) error {
		g2, _ := properties(r)["virtualNetworkGateway2"].(map[string]any)
		if properties(r)["connectionType"] != "Vnet2Vnet" || g2["id"] != spokeID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range []client.Object{v2v, s2s, onprem} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T gone", o), gone(o))
	}
	for _, id := range []string{v2vID, s2sID, onpremID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
	if !armFake.HasResource(hubID) || !armFake.HasResource(spokeID) {
		t.Fatal("a virtual network gateway is gone from Resource Manager")
	}
}

// Refused by Resource Manager, and the refusal reaches the Ready condition:
// an IPsec connection to a gateway that does not exist, and a local network
// gateway with neither an address nor an FQDN. Refused by the CRD: a
// connection type that does not exist.
func TestVPNRefusals(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "vpn-refused")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	onpremID := rgID + "/providers/Microsoft.Network/localNetworkGateways/elsewhere"
	armFake.AddResource(onpremID)

	missing := &network.VirtualNetworkGatewayConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "nowhere"},
		Spec: network.VirtualNetworkGatewayConnection_Spec{
			Owner:          &genruntime.KnownResourceReference{Name: "rg"},
			Location:       ptr("westus"),
			ConnectionType: ptr(network.VirtualNetworkGatewayConnectionType_IPsec),
			VirtualNetworkGateway1: &network.VirtualNetworkGatewaySpec{Reference: &genruntime.ResourceReference{
				ARMID: rgID + "/providers/Microsoft.Network/virtualNetworkGateways/not-there"}},
			LocalNetworkGateway2: &network.LocalNetworkGatewaySpec{Reference: &genruntime.ResourceReference{ARMID: onpremID}},
		},
	}
	noAddress := &network.LocalNetworkGateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "no-address"},
		Spec: network.LocalNetworkGateway_Spec{
			Owner:                    &genruntime.KnownResourceReference{Name: "rg"},
			Location:                 ptr("westus"),
			LocalNetworkAddressSpace: &network.AddressSpace{AddressPrefixes: []string{"10.30.0.0/16"}},
		},
	}
	for _, o := range []client.Object{missing, noAddress} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "the missing gateway on the Ready condition", notReadyWith(missing, "virtualNetworkGateway1"))
	eventually(t, timeout, "the missing address on the Ready condition", notReadyWith(noAddress, "gatewayIpAddress or its fqdn"))

	badType := &network.VirtualNetworkGatewayConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "bad"},
		Spec: network.VirtualNetworkGatewayConnection_Spec{
			Owner:          &genruntime.KnownResourceReference{Name: "rg"},
			ConnectionType: ptr(network.VirtualNetworkGatewayConnectionType("Carrier pigeon")),
		},
	}
	if err := k8s.Create(ctx, badType); err == nil || !contains(err.Error(), "Unsupported value") {
		t.Fatalf("want a refusal of the connection type, got %v", err)
	}
}
