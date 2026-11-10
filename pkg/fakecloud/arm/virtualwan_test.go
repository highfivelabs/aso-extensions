// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"encoding/json"
	"net/http"
	"testing"
)

// What a virtual WAN, a hub and a hub's children must give, and a hub's
// route tables kept or deleted by its PUT.
func TestVirtualWAN(t *testing.T) {
	a := New(func(string) bool { return true })
	put := func(what, path, body string, want int) map[string]any {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
		out := map[string]any{}
		_ = json.Unmarshal([]byte(resp), &out)
		return out
	}
	get := func(path string) map[string]any {
		t.Helper()
		code, resp := do(t, a, http.MethodGet, path, "")
		expect(t, "GET "+path, code, resp, http.StatusOK)
		out := map[string]any{}
		_ = json.Unmarshal([]byte(resp), &out)
		return out
	}
	rg := "/subscriptions/" + sub1 + "/resourceGroups/net"
	put("resource group", rg, `{"location":"westus"}`, http.StatusCreated)
	nw := rg + "/providers/Microsoft.Network/"

	wan := nw + "virtualWans/wan"
	put("WAN of an unknown type", wan, `{"location":"westus","properties":{"type":"Premium"}}`, http.StatusBadRequest)
	if r := put("WAN", wan, `{"location":"westus","properties":{}}`, http.StatusCreated); r["properties"].(map[string]any)["type"] != "Standard" {
		t.Fatalf("a WAN's type is Standard when left out: %v", r)
	}

	hub := nw + "virtualHubs/hub"
	hubBody := func(wanID, prefix, extra string) string {
		return `{"location":"westus","properties":{"virtualWan":{"id":"` + wanID + `"},"addressPrefix":"` + prefix + `"` + extra + `}}`
	}
	put("hub without a WAN", hub, `{"location":"westus","properties":{"addressPrefix":"10.0.0.0/24"}}`, http.StatusBadRequest)
	put("hub in a missing WAN", hub, hubBody(wan+"-nope", "10.0.0.0/24", ""), http.StatusNotFound)
	put("hub without an address prefix", hub, hubBody(wan, "", ""), http.StatusBadRequest)
	put("hub", hub, hubBody(wan, "10.0.0.0/24", ""), http.StatusCreated)

	// Virtual network connections.
	vnet := nw + "virtualNetworks/spoke"
	conn := hub + "/hubVirtualNetworkConnections/spoke"
	connBody := func(vnetID, extra string) string {
		return `{"properties":{"remoteVirtualNetwork":{"id":"` + vnetID + `"}` + extra + `}}`
	}
	put("connection without a network", conn, `{"properties":{}}`, http.StatusBadRequest)
	put("connection to a missing network", conn, connBody(vnet, ""), http.StatusNotFound)
	a.AddResource(vnet)
	put("connection", conn, connBody(vnet, ""), http.StatusCreated)
	put("second connection of the network", hub+"/hubVirtualNetworkConnections/again", connBody(vnet, ""), http.StatusBadRequest)

	// Hub route tables and a connection associated with one.
	table := hub + "/hubRouteTables/spokes"
	route := func(next string) string {
		return `{"properties":{"labels":["spokes"],"routes":[{"name":"to-spoke","destinationType":"CIDR","destinations":["10.1.0.0/16"],"nextHopType":"ResourceId","nextHop":"` + next + `"}]}}`
	}
	put("route without a next hop", table, `{"properties":{"routes":[{"name":"r","destinationType":"CIDR","destinations":["10.1.0.0/16"],"nextHopType":"ResourceId"}]}}`, http.StatusBadRequest)
	put("route to a missing next hop", table, route(conn+"-nope"), http.StatusNotFound)
	put("hub route table", table, route(conn), http.StatusCreated)
	otherHub := nw + "virtualHubs/other"
	put("other hub", otherHub, hubBody(wan, "10.0.1.0/24", ""), http.StatusCreated)
	put("connection associated with another hub's table", conn, connBody(vnet, `,"routingConfiguration":{"associatedRouteTable":{"id":"`+otherHub+`/hubRouteTables/x"}}`), http.StatusBadRequest)
	put("connection associated with a missing table", conn, connBody(vnet, `,"routingConfiguration":{"associatedRouteTable":{"id":"`+hub+`/hubRouteTables/x"}}`), http.StatusNotFound)
	put("connection associated with its hub's table", conn, connBody(vnet, `,"routingConfiguration":{"associatedRouteTable":{"id":"`+table+`"}}`), http.StatusOK)

	// Routing intent: one per hub, to Internet or PrivateTraffic.
	fw := nw + "azureFirewalls/fw"
	a.AddResource(fw)
	intent := func(destination string) string {
		return `{"properties":{"routingPolicies":[{"name":"private","destinations":["` + destination + `"],"nextHop":"` + fw + `"}]}}`
	}
	put("routing intent to an unknown destination", hub+"/routingIntent/intent", intent("Everywhere"), http.StatusBadRequest)
	put("routing intent", hub+"/routingIntent/intent", intent("PrivateTraffic"), http.StatusCreated)
	put("second routing intent", hub+"/routingIntent/again", intent("Internet"), http.StatusBadRequest)

	// BGP connections and IP configurations.
	bgp := hub + "/bgpConnections/nva"
	put("BGP connection without a peer", bgp, `{"properties":{"peerAsn":65010}}`, http.StatusBadRequest)
	put("BGP connection over another hub's connection", bgp, `{"properties":{"peerAsn":65010,"peerIp":"10.1.0.4","hubVirtualNetworkConnection":{"id":"`+otherHub+`/hubVirtualNetworkConnections/spoke"}}}`, http.StatusBadRequest)
	put("BGP connection", bgp, `{"properties":{"peerAsn":65010,"peerIp":"10.1.0.4","hubVirtualNetworkConnection":{"id":"`+conn+`"}}}`, http.StatusCreated)
	subnet := vnet + "/subnets/RouteServerSubnet"
	put("IP configuration without a subnet", hub+"/ipConfigurations/ipconfig1", `{"properties":{}}`, http.StatusBadRequest)
	put("IP configuration in a missing subnet", hub+"/ipConfigurations/ipconfig1", `{"properties":{"subnet":{"id":"`+subnet+`"}}}`, http.StatusNotFound)
	a.AddResource(subnet)
	put("IP configuration", hub+"/ipConfigurations/ipconfig1", `{"properties":{"subnet":{"id":"`+subnet+`"}}}`, http.StatusCreated)

	// The hub's routeTables: listed on the hub, kept by a PUT with its
	// list, deleted by a PUT without it.
	rt := hub + "/routeTables/v2"
	put("route table", rt, `{"name":"v2","properties":{"attachedConnections":["All_Vnets"]}}`, http.StatusCreated)
	listed := func() int {
		items, _ := get(hub)["properties"].(map[string]any)["virtualHubRouteTableV2s"].([]any)
		return len(items)
	}
	if listed() != 1 {
		t.Fatalf("the hub lists %d route tables", listed())
	}
	body, _ := json.Marshal(get(hub))
	put("hub with its route tables", hub, string(body), http.StatusOK)
	if !a.HasResource(rt) {
		t.Fatal("a hub PUT with its route tables lost one")
	}
	put("hub without its route tables", hub, hubBody(wan, "10.0.0.0/24", ""), http.StatusOK)
	if a.HasResource(rt) {
		t.Fatal("a hub PUT without its route tables kept them")
	}
	if !a.HasResource(conn) || !a.HasResource(table) {
		t.Fatal("a hub PUT removed children it does not list")
	}

	if code, _ := do(t, a, http.MethodDelete, hub, ""); code != http.StatusOK || a.HasResource(conn) || a.HasResource(bgp) {
		t.Fatalf("delete the hub and its children: %d", code)
	}
}
