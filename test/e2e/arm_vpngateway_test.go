// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	network "github.com/highfivelabs/aso-extensions/apis/arm/network/v20260301"
)

// The VPN half of Virtual WAN (foundation row 10): in a WAN and a hub (by
// object), a VPN site with one link (its key from a Secret), a site-to-site
// VPN gateway in the hub (by object), a NAT rule and a connection to the site
// owned by the gateway (the link connection's shared key from a Secret, its
// site link by ARM ID, the NAT rule by object), a VPN server configuration
// (RADIUS, its secret from a Secret) and a point-to-site gateway in the hub
// using it (by object). The gateway changed and PUT again, keeping its
// connection and NAT rule (its extension); a second VPN gateway in the hub
// refused on its Ready condition. Deleted, children first.
func TestVPNGateway(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "vpngw")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	nw := rgID + "/providers/Microsoft.Network/"
	owner := func(name string) *genruntime.KnownResourceReference {
		return &genruntime.KnownResourceReference{Name: name}
	}
	secret(t, ns, "vpn-keys", map[string]string{"site": "site-key-1", "link": "psk-link-1", "radius": "radius-1"})

	wan := &network.VirtualWan{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "wan"},
		Spec:       network.VirtualWan_Spec{Owner: owner("rg"), Location: ptr("westus"), Type: ptr("Standard")},
	}
	hub := &network.VirtualHub{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "hub"},
		Spec: network.VirtualHub_Spec{
			Owner: owner("rg"), Location: ptr("westus"), AddressPrefix: ptr("10.100.0.0/23"),
			VirtualWan: &network.SubResource{Reference: ours("VirtualWan", "wan")},
		},
	}
	site := &network.VpnSite{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "branch"},
		Spec: network.VpnSite_Spec{
			Owner: owner("rg"), Location: ptr("westus"),
			VirtualWan:       &network.SubResource{Reference: ours("VirtualWan", "wan")},
			SiteKey:          &genruntime.SecretReference{Name: "vpn-keys", Key: "site"},
			DeviceProperties: &network.DeviceProperties{DeviceVendor: ptr("Contoso")},
			VpnSiteLinks: []network.VpnSiteLink{{
				Name: ptr("isp1"), IpAddress: ptr("203.0.113.10"),
				LinkProperties: &network.VpnLinkProviderProperties{LinkProviderName: ptr("ISP1"), LinkSpeedInMbps: ptr(100)},
			}},
		},
	}
	gw := &network.VpnGateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "s2s"},
		Spec: network.VpnGateway_Spec{
			Owner: owner("rg"), Location: ptr("westus"), VpnGatewayScaleUnit: ptr(1),
			VirtualHub: &network.SubResource{Reference: ours("VirtualHub", "hub")},
		},
	}
	for _, o := range []client.Object{wan, hub, site, gw} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []client.Object{wan, hub, site, gw} {
		eventually(t, timeout, fmt.Sprintf("%T Ready", o), ready(o.(conditioned)))
	}
	hubID := nw + "virtualHubs/hub"
	siteID := nw + "vpnSites/branch"
	gwID := nw + "vpnGateways/s2s"
	expectResource(t, gwID, func(r map[string]any) error {
		h, _ := properties(r)["virtualHub"].(map[string]any)
		if r["type"] != "Microsoft.Network/vpnGateways" || !strings.EqualFold(fmt.Sprint(h["id"]), hubID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, siteID, func(r map[string]any) error {
		p := properties(r)
		links, _ := p["vpnSiteLinks"].([]any)
		if p["siteKey"] != "site-key-1" || len(links) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	rule := &network.VpnGatewaysNatRule{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "egress"},
		Spec: network.VpnGatewaysNatRule_Spec{
			Owner: owner("s2s"),
			Type:  ptr(network.VpnNatRuleType_Static), Mode: ptr(network.VpnNatRuleMode_EgressSnat),
			InternalMappings: []network.VpnNatRuleMapping{{AddressSpace: ptr("10.100.0.0/24")}},
			ExternalMappings: []network.VpnNatRuleMapping{{AddressSpace: ptr("100.64.0.0/24")}},
		},
	}
	conn := &network.VpnGatewaysVpnConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "branch"},
		Spec: network.VpnGatewaysVpnConnection_Spec{
			Owner:         owner("s2s"),
			RemoteVpnSite: &network.SubResource{Reference: ours("VpnSite", "branch")},
			VpnLinkConnections: []network.VpnSiteLinkConnection{{
				Name:           ptr("isp1"),
				VpnSiteLink:    &network.SubResource{Reference: &genruntime.ResourceReference{ARMID: siteID + "/vpnSiteLinks/isp1"}},
				SharedKey:      &genruntime.SecretReference{Name: "vpn-keys", Key: "link"},
				EgressNatRules: []network.SubResource{{Reference: ours("VpnGatewaysNatRule", "egress")}},
				RoutingWeight:  ptr(10),
			}},
		},
	}
	cfg := &network.VpnServerConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "users"},
		Spec: network.VpnServerConfiguration_Spec{
			Owner: owner("rg"), Location: ptr("westus"),
			VpnProtocols:           []network.VpnGatewayTunnelingProtocol{network.VpnGatewayTunnelingProtocol_OpenVPN},
			VpnAuthenticationTypes: []network.VpnAuthenticationType{network.VpnAuthenticationType_Radius},
			RadiusServerAddress:    ptr("10.100.0.10"),
			RadiusServerSecret:     &genruntime.SecretReference{Name: "vpn-keys", Key: "radius"},
		},
	}
	p2s := &network.P2sVpnGateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "users"},
		Spec: network.P2sVpnGateway_Spec{
			Owner: owner("rg"), Location: ptr("westus"), VpnGatewayScaleUnit: ptr(1),
			VirtualHub:             &network.SubResource{Reference: ours("VirtualHub", "hub")},
			VpnServerConfiguration: &network.SubResource{Reference: ours("VpnServerConfiguration", "users")},
			P2SConnectionConfigurations: []network.P2SConnectionConfiguration{{
				Name:                 ptr("clients"),
				VpnClientAddressPool: &network.AddressSpace{AddressPrefixes: []string{"172.16.0.0/24"}},
			}},
		},
	}
	children := []client.Object{rule, conn, cfg, p2s}
	for _, o := range children {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range children {
		eventually(t, timeout, fmt.Sprintf("%T Ready", o), ready(o.(conditioned)))
	}
	ruleID := gwID + "/natRules/egress"
	connID := gwID + "/vpnConnections/branch"
	cfgID := nw + "vpnServerConfigurations/users"
	p2sID := nw + "p2svpnGateways/users"
	expectResource(t, connID, func(r map[string]any) error {
		links, _ := properties(r)["vpnLinkConnections"].([]any)
		if len(links) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		lp, _ := links[0].(map[string]any)["properties"].(map[string]any)
		egress, _ := lp["egressNatRules"].([]any)
		if len(egress) != 1 || !strings.EqualFold(fmt.Sprint(egress[0].(map[string]any)["id"]), ruleID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if k, _ := armFake.SharedKey(connID + "/vpnLinkConnections/isp1"); k != "psk-link-1" {
		t.Fatalf("the link connection's shared key in Azure is %q", k)
	}
	if k, _ := armFake.SharedKey(cfgID + "/radiusServerSecret"); k != "radius-1" {
		t.Fatalf("the RADIUS secret in Azure is %q", k)
	}
	expectResource(t, p2sID, func(r map[string]any) error {
		c, _ := properties(r)["vpnServerConfiguration"].(map[string]any)
		if !strings.EqualFold(fmt.Sprint(c["id"]), cfgID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// The gateway changed: PUT again with its connection and NAT rule.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(gw), gw); err != nil {
		t.Fatal(err)
	}
	gw.Spec.Tags = map[string]string{"team": "network"}
	if err := k8s.Update(ctx, gw); err != nil {
		t.Fatal(err)
	}
	expectResource(t, gwID, func(r map[string]any) error {
		tags, _ := r["tags"].(map[string]any)
		if tags["team"] != "network" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if !armFake.HasResource(connID) || !armFake.HasResource(ruleID) {
		t.Fatal("the gateway's PUT removed its connection or NAT rule")
	}

	// A hub has one site-to-site VPN gateway.
	second := &network.VpnGateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "again"},
		Spec: network.VpnGateway_Spec{
			Owner: owner("rg"), Location: ptr("westus"),
			VirtualHub: &network.SubResource{Reference: ours("VirtualHub", "hub")},
		},
	}
	if err := k8s.Create(ctx, second); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the second VPN gateway on the Ready condition", notReadyWith(second, "already has the VPN gateway"))

	for _, o := range []client.Object{second, p2s, cfg, conn, rule, gw, site, hub, wan} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T %s gone", o, o.GetName()), gone(o))
	}
	for _, id := range []string{p2sID, cfgID, connID, ruleID, gwID, siteID, hubID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
}
