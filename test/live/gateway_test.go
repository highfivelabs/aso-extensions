// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

//go:build live

package live

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	network "github.com/highfivelabs/aso-extensions/apis/arm/network/v20260301"
)

// Gateways take half an hour or more to make and to delete.
const gatewayTimeout = 75 * time.Minute

func ours(kind, name string) *genruntime.ResourceReference {
	return &genruntime.ResourceReference{Group: "network.azure.hfl.dev", Kind: kind, Name: name}
}

func secret(t *testing.T, ns, name string, data map[string]string) {
	t.Helper()
	if err := k8s.Create(context.Background(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}, StringData: data}); err != nil {
		t.Fatal(err)
	}
}

// vnet makes a virtual network fixture with the given subnets (name: prefix).
func vnet(t *testing.T, name, prefix string, subnets map[string]string) string {
	t.Helper()
	var sn []any
	for n, p := range subnets {
		sn = append(sn, map[string]any{"name": n, "properties": map[string]any{"addressPrefix": p}})
	}
	id := rgID() + "/providers/Microsoft.Network/virtualNetworks/" + name
	fixture(t, id, "2024-05-01", map[string]any{
		"location":   location,
		"properties": map[string]any{"addressSpace": map[string]any{"addressPrefixes": []string{prefix}}, "subnets": sn},
	})
	return id
}

// A virtual hub (about $0.25 an hour) in a Virtual WAN with its children: a
// connection to a virtual network, a hub route table routing to it, a BGP
// connection to an address in it; then a site-to-site VPN gateway (about
// $0.36 an hour) with a NAT rule and a connection to a VPN site, and a
// point-to-site gateway (about $0.36 an hour) with a server configuration.
// Not here: routing intent (needs an Azure Firewall or NVA in the hub, over
// $1 an hour) and the ExpressRoute gateway (skipped by cost).
func TestLiveVirtualHub(t *testing.T) {
	t.Parallel()
	ns := namespace(t, "hub")
	secret(t, ns, "vpn-keys", map[string]string{"link": "live-psk-0123456789", "radius": "radius-secret-1"})
	spokeID := vnet(t, azName("spoke"), "10.1.0.0/16", map[string]string{"default": "10.1.0.0/24"})

	wan := &network.VirtualWan{}
	wan.Namespace, wan.Name = ns, "wan"
	wan.Spec = network.VirtualWan_Spec{AzureName: azName("hubwan"), Owner: owner("rg"), Location: ptr(location), Type: ptr("Standard")}
	hub := &network.VirtualHub{}
	hub.Namespace, hub.Name = ns, "hub"
	hub.Spec = network.VirtualHub_Spec{
		AzureName: azName("hub"), Owner: owner("rg"), Location: ptr(location), AddressPrefix: ptr("10.100.0.0/23"),
		VirtualWan: &network.SubResource{Reference: ours("VirtualWan", "wan")}, Sku: ptr("Standard"),
	}
	create(t, gatewayTimeout, wan, hub)
	hubID := rgID() + "/providers/Microsoft.Network/virtualHubs/" + hub.Spec.AzureName
	expectResource(t, hubID, networkAPI, func(r map[string]any) error {
		if p := properties(r); p["addressPrefix"] != "10.100.0.0/23" || p["sku"] != "Standard" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	conn := &network.VirtualHubsHubVirtualNetworkConnection{}
	conn.Namespace, conn.Name = ns, "spoke"
	conn.Spec = network.VirtualHubsHubVirtualNetworkConnection_Spec{
		Owner:                owner("hub"),
		RemoteVirtualNetwork: &network.SubResource{Reference: &genruntime.ResourceReference{ARMID: spokeID}},
	}
	create(t, gatewayTimeout, conn)
	expectResource(t, hubID+"/hubVirtualNetworkConnections/spoke", networkAPI, func(r map[string]any) error {
		rv, _ := properties(r)["remoteVirtualNetwork"].(map[string]any)
		if !strings.EqualFold(fmt.Sprint(rv["id"]), spokeID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	t.Run("HubRouteTable", func(t *testing.T) {
		table := &network.VirtualHubsHubRouteTable{}
		table.Namespace, table.Name = ns, "spokes"
		table.Spec = network.VirtualHubsHubRouteTable_Spec{
			Owner: owner("hub"), Labels: []string{"spokes"},
			Routes: []network.HubRoute{{
				Name: ptr("to-spoke"), DestinationType: ptr("CIDR"), Destinations: []string{"10.2.0.0/16"},
				NextHopType: ptr("ResourceId"), NextHopReference: ours("VirtualHubsHubVirtualNetworkConnection", "spoke"),
			}},
		}
		create(t, gatewayTimeout, table)
		id := hubID + "/hubRouteTables/spokes"
		expectResource(t, id, networkAPI, func(r map[string]any) error {
			if routes, _ := properties(r)["routes"].([]any); len(routes) != 1 {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})
		remove(t, gatewayTimeout, table)
		expectGone(t, id, networkAPI)
	})

	t.Run("BgpConnection", func(t *testing.T) {
		bgp := &network.VirtualHubsBgpConnection{}
		bgp.Namespace, bgp.Name = ns, "nva"
		bgp.Spec = network.VirtualHubsBgpConnection_Spec{
			Owner: owner("hub"), PeerAsn: ptr(uint32(65010)), PeerIp: ptr("10.1.0.4"),
			HubVirtualNetworkConnection: &network.SubResource{Reference: ours("VirtualHubsHubVirtualNetworkConnection", "spoke")},
		}
		create(t, gatewayTimeout, bgp)
		id := hubID + "/bgpConnections/nva"
		expectResource(t, id, networkAPI, func(r map[string]any) error {
			if p := properties(r); p["peerIp"] != "10.1.0.4" || p["peerAsn"] != float64(65010) {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})
		remove(t, gatewayTimeout, bgp)
		expectGone(t, id, networkAPI)
	})

	site := &network.VpnSite{}
	site.Namespace, site.Name = ns, "branch"
	site.Spec = network.VpnSite_Spec{
		AzureName: azName("hubsite"), Owner: owner("rg"), Location: ptr(location),
		VirtualWan:       &network.SubResource{Reference: ours("VirtualWan", "wan")},
		DeviceProperties: &network.DeviceProperties{DeviceVendor: ptr("Contoso")},
		VpnSiteLinks: []network.VpnSiteLink{{
			Name: ptr("isp1"), IpAddress: ptr("203.0.113.10"),
			LinkProperties: &network.VpnLinkProviderProperties{LinkProviderName: ptr("ISP1"), LinkSpeedInMbps: ptr(100)},
		}},
	}
	s2s := &network.VpnGateway{}
	s2s.Namespace, s2s.Name = ns, "s2s"
	s2s.Spec = network.VpnGateway_Spec{
		AzureName: azName("s2s"), Owner: owner("rg"), Location: ptr(location), VpnGatewayScaleUnit: ptr(1),
		VirtualHub: &network.SubResource{Reference: ours("VirtualHub", "hub")},
	}
	cfg := &network.VpnServerConfiguration{}
	cfg.Namespace, cfg.Name = ns, "users"
	cfg.Spec = network.VpnServerConfiguration_Spec{
		AzureName: azName("hubusers"), Owner: owner("rg"), Location: ptr(location),
		VpnProtocols:           []network.VpnGatewayTunnelingProtocol{network.VpnGatewayTunnelingProtocol_OpenVPN},
		VpnAuthenticationTypes: []network.VpnAuthenticationType{network.VpnAuthenticationType_Radius},
		RadiusServerAddress:    ptr("10.100.0.10"),
		RadiusServerSecret:     &genruntime.SecretReference{Name: "vpn-keys", Key: "radius"},
	}
	p2s := &network.P2sVpnGateway{}
	p2s.Namespace, p2s.Name = ns, "p2s"
	p2s.Spec = network.P2sVpnGateway_Spec{
		AzureName: azName("p2s"), Owner: owner("rg"), Location: ptr(location), VpnGatewayScaleUnit: ptr(1),
		VirtualHub:             &network.SubResource{Reference: ours("VirtualHub", "hub")},
		VpnServerConfiguration: &network.SubResource{Reference: ours("VpnServerConfiguration", "users")},
		P2SConnectionConfigurations: []network.P2SConnectionConfiguration{{
			Name:                 ptr("clients"),
			VpnClientAddressPool: &network.AddressSpace{AddressPrefixes: []string{"172.16.0.0/24"}},
		}},
	}
	createAll(t, gatewayTimeout, site, s2s, cfg, p2s)
	s2sID := rgID() + "/providers/Microsoft.Network/vpnGateways/" + s2s.Spec.AzureName
	siteID := rgID() + "/providers/Microsoft.Network/vpnSites/" + site.Spec.AzureName
	expectResource(t, s2sID, networkAPI, func(r map[string]any) error {
		h, _ := properties(r)["virtualHub"].(map[string]any)
		if !strings.EqualFold(fmt.Sprint(h["id"]), hubID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, rgID()+"/providers/Microsoft.Network/p2sVpnGateways/"+p2s.Spec.AzureName, networkAPI, func(r map[string]any) error {
		if c, _ := properties(r)["p2SConnectionConfigurations"].([]any); len(c) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	rule := &network.VpnGatewaysNatRule{}
	rule.Namespace, rule.Name = ns, "egress"
	rule.Spec = network.VpnGatewaysNatRule_Spec{
		Owner: owner("s2s"),
		Type:  ptr(network.VpnNatRuleType_Static), Mode: ptr(network.VpnNatRuleMode_EgressSnat),
		InternalMappings: []network.VpnNatRuleMapping{{AddressSpace: ptr("10.1.0.0/24")}},
		ExternalMappings: []network.VpnNatRuleMapping{{AddressSpace: ptr("100.64.0.0/24")}},
	}
	vpnConn := &network.VpnGatewaysVpnConnection{}
	vpnConn.Namespace, vpnConn.Name = ns, "branch"
	vpnConn.Spec = network.VpnGatewaysVpnConnection_Spec{
		Owner:         owner("s2s"),
		RemoteVpnSite: &network.SubResource{Reference: ours("VpnSite", "branch")},
		VpnLinkConnections: []network.VpnSiteLinkConnection{{
			Name:           ptr("isp1"),
			VpnSiteLink:    &network.SubResource{Reference: &genruntime.ResourceReference{ARMID: siteID + "/vpnSiteLinks/isp1"}},
			SharedKey:      &genruntime.SecretReference{Name: "vpn-keys", Key: "link"},
			EgressNatRules: []network.SubResource{{Reference: ours("VpnGatewaysNatRule", "egress")}},
		}},
	}
	create(t, gatewayTimeout, rule, vpnConn)
	expectResource(t, s2sID+"/natRules/egress", networkAPI, func(r map[string]any) error {
		if p := properties(r); p["mode"] != "EgressSnat" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, s2sID+"/vpnConnections/branch", networkAPI, func(r map[string]any) error {
		rs, _ := properties(r)["remoteVpnSite"].(map[string]any)
		if !strings.EqualFold(fmt.Sprint(rs["id"]), siteID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	// Cleanup (the reverse of creation) deletes the connection, the rule,
	// the gateways, the site, the hub's connection, the hub and the WAN.
}

// A site-to-site connection from a virtual network gateway (a fixture,
// VpnGw1AZ, about $0.19 an hour plus a public IP) to a local network gateway
// of ours.
func TestLiveVirtualNetworkGatewayConnection(t *testing.T) {
	t.Parallel()
	ns := namespace(t, "vngc")
	secret(t, ns, "psk", map[string]string{"key": "live-psk-0123456789"})
	vnetID := vnet(t, azName("gwnet"), "10.50.0.0/16", map[string]string{"GatewaySubnet": "10.50.255.0/27"})
	pipID := rgID() + "/providers/Microsoft.Network/publicIPAddresses/" + azName("gwip")
	fixture(t, pipID, "2024-05-01", map[string]any{
		"location": location, "sku": map[string]any{"name": "Standard"}, "zones": []string{"1", "2", "3"},
		"properties": map[string]any{"publicIPAllocationMethod": "Static"},
	})
	gwID := rgID() + "/providers/Microsoft.Network/virtualNetworkGateways/" + azName("vng")
	if _, _, err := azure.do(context.Background(), "PUT", gwID, "2024-05-01", map[string]any{
		"location": location,
		"properties": map[string]any{
			"gatewayType": "Vpn", "vpnType": "RouteBased", "vpnGatewayGeneration": "Generation1",
			"sku": map[string]any{"name": "VpnGw1AZ", "tier": "VpnGw1AZ"},
			"ipConfigurations": []any{map[string]any{"name": "default", "properties": map[string]any{
				"subnet":          map[string]any{"id": vnetID + "/subnets/GatewaySubnet"},
				"publicIPAddress": map[string]any{"id": pipID},
			}}},
		},
	}); err != nil {
		t.Fatalf("virtual network gateway: %v", err)
	}
	t.Cleanup(func() { deleteARM(t, gwID, "2024-05-01") })

	onprem := &network.LocalNetworkGateway{}
	onprem.Namespace, onprem.Name = ns, "onprem"
	onprem.Spec = network.LocalNetworkGateway_Spec{
		AzureName: azName("vngc-onprem"), Owner: owner("rg"), Location: ptr(location),
		GatewayIpAddress:         ptr("203.0.113.20"),
		LocalNetworkAddressSpace: &network.AddressSpace{AddressPrefixes: []string{"10.60.0.0/16"}},
	}
	create(t, timeout, onprem)
	eventually(t, gatewayTimeout, "virtual network gateway Succeeded", func() error {
		_, r, err := azure.get(context.Background(), gwID, "2024-05-01")
		if err != nil {
			return err
		}
		if s := state(r); s != "Succeeded" {
			return fmt.Errorf("provisioningState %s", s)
		}
		return nil
	})

	s2s := &network.VirtualNetworkGatewayConnection{}
	s2s.Namespace, s2s.Name = ns, "to-onprem"
	s2s.Spec = network.VirtualNetworkGatewayConnection_Spec{
		AzureName: azName("to-onprem"), Owner: owner("rg"), Location: ptr(location),
		ConnectionType:         ptr(network.VirtualNetworkGatewayConnectionType_IPsec),
		ConnectionProtocol:     ptr(network.VirtualNetworkGatewayConnectionProtocol("IKEv2")),
		VirtualNetworkGateway1: &network.VirtualNetworkGatewaySpec{Reference: &genruntime.ResourceReference{ARMID: gwID}},
		LocalNetworkGateway2:   &network.LocalNetworkGatewaySpec{Reference: ours("LocalNetworkGateway", "onprem")},
		SharedKey:              &genruntime.SecretReference{Name: "psk", Key: "key"},
	}
	create(t, gatewayTimeout, s2s)
	id := rgID() + "/providers/Microsoft.Network/connections/" + s2s.Spec.AzureName
	expectResource(t, id, networkAPI, func(r map[string]any) error {
		p := properties(r)
		g1, _ := p["virtualNetworkGateway1"].(map[string]any)
		if p["connectionType"] != "IPsec" || !strings.EqualFold(fmt.Sprint(g1["id"]), gwID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, s2s, func(c *network.VirtualNetworkGatewayConnection) { c.Spec.RoutingWeight = ptr(5) })
	eventually(t, gatewayTimeout, "connection Ready after the change", ready(s2s))
	expectResource(t, id, networkAPI, func(r map[string]any) error {
		if properties(r)["routingWeight"] != float64(5) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, gatewayTimeout, s2s)
	expectGone(t, id, networkAPI)
}
