// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// What a circuit, a peering, a Global Reach connection and a port must give;
// the keys Azure makes; children a parent's PUT keeps or deletes; and
// authorization keys redeemed across subscriptions.
func TestExpressRoute(t *testing.T) {
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
	names := func(r map[string]any, list string) string {
		props, _ := r["properties"].(map[string]any)
		items, _ := props[list].([]any)
		var out []string
		for _, i := range items {
			out = append(out, i.(map[string]any)["name"].(string))
		}
		return strings.Join(out, ",")
	}
	rg := "/subscriptions/" + sub1 + "/resourceGroups/net"
	put("resource group", rg, `{"location":"westus"}`, http.StatusCreated)
	nw := rg + "/providers/Microsoft.Network/"
	sku := `"sku":{"name":"Standard_MeteredData","tier":"Standard","family":"MeteredData"}`
	provider := `"serviceProviderProperties":{"serviceProviderName":"Equinix","peeringLocation":"Silicon Valley","bandwidthInMbps":50}`

	c := nw + "expressRouteCircuits/er"
	put("circuit without a sku", c, `{"location":"westus","properties":{`+provider+`}}`, http.StatusBadRequest)
	put("circuit without a provider or port", c, `{"location":"westus",`+sku+`,"properties":{}}`, http.StatusBadRequest)
	r := put("circuit", c, `{"location":"westus",`+sku+`,"properties":{`+provider+`,"serviceKey":"mine"}}`, http.StatusCreated)
	serviceKey := a.AuthorizationKey(c)
	if serviceKey == "" || serviceKey == "mine" {
		t.Fatalf("service key %q: Azure makes it", serviceKey)
	}
	if names(r, "peerings") != "" || names(r, "authorizations") != "" {
		t.Fatalf("a new circuit has children: %v", r)
	}

	p := c + "/peerings/AzurePrivatePeering"
	peering := `{"properties":{"peeringType":"AzurePrivatePeering","vlanId":100,"peerASN":65010,"primaryPeerAddressPrefix":"192.168.10.0/30","secondaryPeerAddressPrefix":"192.168.10.4/30","sharedKey":"md5-key"}}`
	put("peering named for another type", c+"/peerings/MicrosoftPeering", peering, http.StatusBadRequest)
	put("peering without a VLAN", p, `{"properties":{"peeringType":"AzurePrivatePeering","peerASN":65010,"primaryPeerAddressPrefix":"192.168.10.0/30","secondaryPeerAddressPrefix":"192.168.10.4/30"}}`, http.StatusBadRequest)
	put("peering", p, peering, http.StatusCreated)
	auth := c + "/authorizations/to-team-b"
	put("authorization", auth, `{"properties":{"authorizationKey":"mine"}}`, http.StatusCreated)
	key := a.AuthorizationKey(auth)
	if key == "" || key == "mine" {
		t.Fatalf("authorization key %q: Azure makes it", key)
	}
	put("authorization again", auth, `{"properties":{}}`, http.StatusOK)
	if a.AuthorizationKey(auth) != key {
		t.Fatal("the authorization key changed on a second PUT")
	}

	circuit := get(c)
	if names(circuit, "peerings") != "AzurePrivatePeering" || names(circuit, "authorizations") != "to-team-b" {
		t.Fatalf("the circuit lists %q and %q", names(circuit, "peerings"), names(circuit, "authorizations"))
	}
	// A PUT with the lists Azure returned keeps the children; the service key
	// stays.
	body, _ := json.Marshal(circuit)
	put("circuit with its children", c, string(body), http.StatusOK)
	if !a.HasResource(p) || !a.HasResource(auth) || a.AuthorizationKey(c) != serviceKey {
		t.Fatal("a PUT with the children's lists lost a child or the service key")
	}

	// Global Reach to a circuit in another subscription.
	rg2 := "/subscriptions/" + sub2 + "/resourceGroups/net"
	put("resource group 2", rg2, `{"location":"westus"}`, http.StatusCreated)
	c2 := rg2 + "/providers/Microsoft.Network/expressRouteCircuits/er2"
	put("circuit 2", c2, `{"location":"eastus",`+sku+`,"properties":{`+provider+`}}`, http.StatusCreated)
	p2 := c2 + "/peerings/AzurePrivatePeering"
	put("peering 2", p2, peering, http.StatusCreated)
	gr := p2 + "/connections/to-er"
	reach := func(own, peer, extra string) string {
		return `{"properties":{"expressRouteCircuitPeering":{"id":"` + own + `"},"peerExpressRouteCircuitPeering":{"id":"` + peer + `"},"addressPrefix":"10.99.0.0/29"` + extra + `}}`
	}
	put("Global Reach naming another peering", gr, reach(p, p, ""), http.StatusBadRequest)
	put("Global Reach to a missing peer", gr, reach(p2, c+"/peerings/MicrosoftPeering", ""), http.StatusNotFound)
	put("Global Reach across subscriptions without a key", gr, reach(p2, p, ""), http.StatusBadRequest)
	put("Global Reach with a wrong key", gr, reach(p2, p, `,"authorizationKey":"nope"`), http.StatusBadRequest)
	put("Global Reach", gr, reach(p2, p, `,"authorizationKey":"`+key+`"`), http.StatusCreated)
	if a := get(auth); a["properties"].(map[string]any)["authorizationUseStatus"] != "InUse" {
		t.Fatalf("the redeemed authorization is not in use: %v", a)
	}
	if names(get(p2), "connections") != "to-er" || names(get(c2)["properties"].(map[string]any)["peerings"].([]any)[0].(map[string]any), "connections") != "to-er" {
		t.Fatal("the peering (or the circuit's peering) does not list its Global Reach connection")
	}
	// The key is in use by that connection: another may not redeem it.
	p3 := c2 + "/peerings/AzurePrivatePeering/connections/again"
	put("Global Reach with a key in use", p3, reach(p2, p, `,"authorizationKey":"`+key+`"`), http.StatusBadRequest)

	// A peering PUT without its connections, and a circuit PUT without its
	// peerings, delete them: what ASO's network kinds copy the lists in for.
	put("peering 2 without its connections", p2, peering, http.StatusOK)
	if a.HasResource(gr) {
		t.Fatal("a peering PUT without its connections kept them")
	}
	put("circuit without its children", c, `{"location":"westus",`+sku+`,"properties":{`+provider+`}}`, http.StatusOK)
	if a.HasResource(p) || a.HasResource(auth) {
		t.Fatal("a circuit PUT without its peerings and authorizations kept them")
	}

	// ExpressRoute Direct: a port, its authorization, a circuit on it from
	// another subscription.
	port := nw + "expressRoutePorts/direct"
	put("port without a location", port, `{"location":"westus","properties":{"bandwidthInGbps":10}}`, http.StatusBadRequest)
	put("port", port, `{"location":"westus","properties":{"peeringLocation":"Equinix-Silicon-Valley","bandwidthInGbps":10,"encapsulation":"Dot1Q"}}`, http.StatusCreated)
	portAuth := port + "/authorizations/to-team-b"
	put("port authorization", portAuth, `{"properties":{}}`, http.StatusCreated)
	portKey := a.AuthorizationKey(portAuth)
	if portKey == "" {
		t.Fatal("no port authorization key")
	}
	onPort := rg2 + "/providers/Microsoft.Network/expressRouteCircuits/direct"
	direct := func(extra string) string {
		return `{"location":"westus",` + sku + `,"properties":{"expressRoutePort":{"id":"` + port + `"},"bandwidthInGbps":5` + extra + `}}`
	}
	put("circuit on a missing port", onPort, `{"location":"westus",`+sku+`,"properties":{"expressRoutePort":{"id":"`+port+`-nope"},"bandwidthInGbps":5}}`, http.StatusNotFound)
	put("circuit on a port without bandwidth", onPort, `{"location":"westus",`+sku+`,"properties":{"expressRoutePort":{"id":"`+port+`"}}}`, http.StatusBadRequest)
	put("circuit on another subscription's port without a key", onPort, direct(""), http.StatusBadRequest)
	put("circuit on another subscription's port", onPort, direct(`,"authorizationKey":"`+portKey+`"`), http.StatusCreated)
	if got := get(portAuth)["properties"].(map[string]any); got["authorizationUseStatus"] != "InUse" || !strings.EqualFold(got["circuitResourceUri"].(string), onPort) {
		t.Fatalf("the port authorization is not in use by the circuit: %v", got)
	}

	// An ExpressRoute gateway connection in another subscription redeems a
	// circuit authorization too (vpn.go).
	put("authorization for a gateway", c+"/authorizations/gw", `{"properties":{}}`, http.StatusCreated)
	vng := rg2 + "/providers/Microsoft.Network/virtualNetworkGateways/er-gw"
	a.AddResource(vng)
	conn := rg2 + "/providers/Microsoft.Network/connections/to-er"
	put("ExpressRoute connection across subscriptions without a key", conn, connection("ExpressRoute", vng, "peer", c, ""), http.StatusBadRequest)
	put("ExpressRoute connection across subscriptions", conn, connection("ExpressRoute", vng, "peer", c, `,"authorizationKey":"`+a.AuthorizationKey(c+"/authorizations/gw")+`"`), http.StatusCreated)

	if code, _ := do(t, a, http.MethodDelete, c, ""); code != http.StatusOK || a.HasResource(c+"/authorizations/gw") {
		t.Fatalf("delete the circuit and its children: %d", code)
	}
}
