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

// ours is a reference to one of our network kinds by object.
func ours(kind, name string) *genruntime.ResourceReference {
	return &genruntime.ResourceReference{Group: "network.azure.hfl.dev", Kind: kind, Name: name}
}

// A virtual WAN and a hub in it (by object), owned by ASO's ResourceGroup;
// the hub's children: a connection to a virtual network (by ARM ID), a hub
// route table whose route's next hop is that connection (by object) and
// which the connection is then associated with, a route table, routing
// intent, a BGP connection over the hub connection and an IP configuration;
// an ExpressRoute gateway in our hub by object (row 9). The hub changed and
// PUT again, keeping its route tables (its extension); a second routing
// intent refused on its Ready condition. Deleted, children first.
func TestVirtualWAN(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "vwan")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	nw := rgID + "/providers/Microsoft.Network/"
	vnetID := nw + "virtualNetworks/spoke"
	subnetID := vnetID + "/subnets/RouteServerSubnet"
	fwID := nw + "azureFirewalls/fw"
	for _, id := range []string{vnetID, subnetID, fwID} {
		armFake.AddResource(id)
	}
	owner := func(name string) *genruntime.KnownResourceReference {
		return &genruntime.KnownResourceReference{Name: name}
	}

	wan := &network.VirtualWan{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "wan"},
		Spec: network.VirtualWan_Spec{
			Owner: owner("rg"), Location: ptr("westus"), Type: ptr("Standard"),
			AllowBranchToBranchTraffic: ptr(true),
		},
	}
	hub := &network.VirtualHub{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "hub"},
		Spec: network.VirtualHub_Spec{
			Owner: owner("rg"), Location: ptr("westus"), AddressPrefix: ptr("10.100.0.0/23"),
			VirtualWan: &network.SubResource{Reference: ours("VirtualWan", "wan")},
			Sku:        ptr("Standard"),
		},
	}
	conn := &network.VirtualHubsHubVirtualNetworkConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "spoke"},
		Spec: network.VirtualHubsHubVirtualNetworkConnection_Spec{
			Owner:                  owner("hub"),
			RemoteVirtualNetwork:   &network.SubResource{Reference: &genruntime.ResourceReference{ARMID: vnetID}},
			EnableInternetSecurity: ptr(true),
		},
	}
	for _, o := range []client.Object{wan, hub, conn} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "VirtualWan Ready", ready(wan))
	eventually(t, timeout, "VirtualHub Ready", ready(hub))
	eventually(t, timeout, "VirtualHubsHubVirtualNetworkConnection Ready", ready(conn))
	wanID := nw + "virtualWans/wan"
	hubID := nw + "virtualHubs/hub"
	connID := hubID + "/hubVirtualNetworkConnections/spoke"
	expectResource(t, hubID, func(r map[string]any) error {
		p := properties(r)
		w, _ := p["virtualWan"].(map[string]any)
		if r["type"] != "Microsoft.Network/virtualHubs" || w["id"] != wanID || p["addressPrefix"] != "10.100.0.0/23" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	table := &network.VirtualHubsHubRouteTable{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "spokes"},
		Spec: network.VirtualHubsHubRouteTable_Spec{
			Owner:  owner("hub"),
			Labels: []string{"spokes"},
			Routes: []network.HubRoute{{
				Name: ptr("to-spoke"), DestinationType: ptr("CIDR"), Destinations: []string{"10.1.0.0/16"},
				NextHopType: ptr("ResourceId"), NextHopReference: ours("VirtualHubsHubVirtualNetworkConnection", "spoke"),
			}},
		},
	}
	routeTable := &network.VirtualHubsRouteTable{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "v2"},
		Spec: network.VirtualHubsRouteTable_Spec{
			Owner:               owner("hub"),
			AttachedConnections: []string{"All_Vnets"},
			Routes: []network.VirtualHubRouteV2{{
				DestinationType: ptr("CIDR"), Destinations: []string{"10.2.0.0/16"},
				NextHopType: ptr("IPAddress"), NextHops: []string{"10.1.0.4"},
			}},
		},
	}
	intent := &network.VirtualHubsRoutingIntent{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "intent"},
		Spec: network.VirtualHubsRoutingIntent_Spec{
			Owner: owner("hub"),
			RoutingPolicies: []network.RoutingPolicy{{
				Name: ptr("private"), Destinations: []string{"PrivateTraffic"},
				NextHopReference: &genruntime.ResourceReference{ARMID: fwID},
			}},
		},
	}
	bgp := &network.VirtualHubsBgpConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "nva"},
		Spec: network.VirtualHubsBgpConnection_Spec{
			Owner: owner("hub"), PeerAsn: ptr(uint32(65010)), PeerIp: ptr("10.1.0.4"),
			HubVirtualNetworkConnection: &network.SubResource{Reference: ours("VirtualHubsHubVirtualNetworkConnection", "spoke")},
		},
	}
	ipConfig := &network.VirtualHubsIpConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "ipconfig1"},
		Spec: network.VirtualHubsIpConfiguration_Spec{
			Owner:  owner("hub"),
			Subnet: &network.Subnet_VirtualHubs_IpConfiguration_SubResourceEmbedded{Reference: &genruntime.ResourceReference{ARMID: subnetID}},
		},
	}
	gw := &network.ExpressRouteGateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "er"},
		Spec: network.ExpressRouteGateway_Spec{
			Owner: owner("rg"), Location: ptr("westus"),
			VirtualHub: &network.VirtualHubId{Reference: ours("VirtualHub", "hub")},
		},
	}
	children := []client.Object{table, routeTable, intent, bgp, ipConfig, gw}
	for _, o := range children {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range children {
		eventually(t, timeout, fmt.Sprintf("%T Ready", o), ready(o.(conditioned)))
	}
	tableID := hubID + "/hubRouteTables/spokes"
	routeTableID := hubID + "/routeTables/v2"
	bgpID := hubID + "/bgpConnections/nva"
	gwID := nw + "expressRouteGateways/er"
	expectResource(t, tableID, func(r map[string]any) error {
		routes, _ := properties(r)["routes"].([]any)
		if len(routes) != 1 || !strings.EqualFold(fmt.Sprint(routes[0].(map[string]any)["nextHop"]), connID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, bgpID, func(r map[string]any) error {
		c, _ := properties(r)["hubVirtualNetworkConnection"].(map[string]any)
		if !strings.EqualFold(fmt.Sprint(c["id"]), connID) || properties(r)["peerAsn"] != float64(65010) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, gwID, func(r map[string]any) error {
		h, _ := properties(r)["virtualHub"].(map[string]any)
		if !strings.EqualFold(fmt.Sprint(h["id"]), hubID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// The connection associated with the hub route table, by object.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(conn), conn); err != nil {
		t.Fatal(err)
	}
	conn.Spec.RoutingConfiguration = &network.RoutingConfiguration{
		AssociatedRouteTable: &network.SubResource{Reference: ours("VirtualHubsHubRouteTable", "spokes")},
	}
	if err := k8s.Update(ctx, conn); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "VirtualHubsHubVirtualNetworkConnection Ready again", ready(conn))
	expectResource(t, connID, func(r map[string]any) error {
		routing, _ := properties(r)["routingConfiguration"].(map[string]any)
		table, _ := routing["associatedRouteTable"].(map[string]any)
		if !strings.EqualFold(fmt.Sprint(table["id"]), tableID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// The hub changed: PUT again with its route tables.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(hub), hub); err != nil {
		t.Fatal(err)
	}
	hub.Spec.Tags = map[string]string{"team": "network"}
	if err := k8s.Update(ctx, hub); err != nil {
		t.Fatal(err)
	}
	expectResource(t, hubID, func(r map[string]any) error {
		tags, _ := r["tags"].(map[string]any)
		if tags["team"] != "network" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if !armFake.HasResource(routeTableID) {
		t.Fatal("the hub's PUT removed its route table")
	}

	// A hub has one routing intent.
	second := &network.VirtualHubsRoutingIntent{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "again"},
		Spec: network.VirtualHubsRoutingIntent_Spec{
			Owner: owner("hub"),
			RoutingPolicies: []network.RoutingPolicy{{
				Name: ptr("internet"), Destinations: []string{"Internet"},
				NextHopReference: &genruntime.ResourceReference{ARMID: fwID},
			}},
		},
	}
	if err := k8s.Create(ctx, second); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the second routing intent on the Ready condition", notReadyWith(second, "already has the routing intent"))

	for _, o := range []client.Object{second, gw, ipConfig, bgp, intent, routeTable, conn, table, hub, wan} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T %s gone", o, o.GetName()), gone(o))
	}
	for _, id := range []string{gwID, bgpID, routeTableID, connID, tableID, hubID, wanID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
}
