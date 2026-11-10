// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"strings"
	"testing"
)

func connection(kind, vng1, end, endID, extra string) string {
	body := `{"location":"westus","properties":{"connectionType":"` + kind + `"`
	if vng1 != "" {
		body += `,"virtualNetworkGateway1":{"id":"` + vng1 + `"}`
	}
	if end != "" {
		body += `,"` + end + `":{"id":"` + endID + `"}`
	}
	return body + extra + `}}`
}

// What a connection must name, the shared key kept but not returned, and the
// address a local network gateway needs.
func TestVPN(t *testing.T) {
	a := New(func(string) bool { return true })
	put := func(what, path, body string, want int) string {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
		return resp
	}
	rg := "/subscriptions/" + sub1 + "/resourceGroups/net"
	put("resource group", rg, `{"location":"westus"}`, http.StatusCreated)
	nw := rg + "/providers/Microsoft.Network/"
	vng := nw + "virtualNetworkGateways/hub"
	other := nw + "virtualNetworkGateways/spoke"
	a.AddResource(vng)
	a.AddResource(other)
	circuitPeering := rg + "/providers/Microsoft.Network/expressRouteCircuits/c/peerings/AzurePrivatePeering"

	lng := nw + "localNetworkGateways/onprem"
	put("local gateway without an address", lng, `{"location":"westus","properties":{"localNetworkAddressSpace":{"addressPrefixes":["10.20.0.0/16"]}}}`, http.StatusBadRequest)
	put("local gateway by IP", lng, `{"location":"westus","properties":{"gatewayIpAddress":"203.0.113.10"}}`, http.StatusCreated)
	put("local gateway by FQDN", nw+"localNetworkGateways/branch", `{"location":"westus","properties":{"fqdn":"vpn.example.com"}}`, http.StatusCreated)

	c := nw + "connections/to-onprem"
	put("no connection type", c, `{"location":"westus","properties":{"virtualNetworkGateway1":{"id":"`+vng+`"}}}`, http.StatusBadRequest)
	put("no first gateway", c, connection("IPsec", "", "localNetworkGateway2", lng, ""), http.StatusBadRequest)
	put("IPsec without a local gateway", c, connection("IPsec", vng, "", "", ""), http.StatusBadRequest)
	put("a missing first gateway", c, connection("IPsec", vng+"-nope", "localNetworkGateway2", lng, ""), http.StatusNotFound)
	put("a missing local gateway", c, connection("IPsec", vng, "localNetworkGateway2", lng+"-nope", ""), http.StatusNotFound)
	resp := put("IPsec", c, connection("IPsec", vng, "localNetworkGateway2", lng, `,"sharedKey":"psk-1"`), http.StatusCreated)
	if strings.Contains(resp, "psk-1") || strings.Contains(resp, "sharedKey") {
		t.Fatalf("the shared key is returned: %s", resp)
	}
	if _, got := do(t, a, http.MethodGet, c, ""); strings.Contains(got, "psk-1") {
		t.Fatalf("the shared key is returned by a GET: %s", got)
	}
	if k, ok := a.SharedKey(c); !ok || k != "psk-1" {
		t.Fatalf("shared key %q %v", k, ok)
	}
	put("IPsec, changed without the key", c, connection("IPsec", vng, "localNetworkGateway2", lng, `,"routingWeight":10`), http.StatusOK)
	if k, _ := a.SharedKey(c); k != "psk-1" {
		t.Fatalf("the shared key changed to %q on a PUT without one", k)
	}

	put("Vnet2Vnet without a second gateway", nw+"connections/v2v", connection("Vnet2Vnet", vng, "localNetworkGateway2", lng, ""), http.StatusBadRequest)
	put("Vnet2Vnet", nw+"connections/v2v", connection("Vnet2Vnet", vng, "virtualNetworkGateway2", other, ""), http.StatusCreated)
	put("ExpressRoute to a missing peering", nw+"connections/er", connection("ExpressRoute", vng, "peer", circuitPeering, ""), http.StatusNotFound)
	a.AddResource(circuitPeering)
	put("ExpressRoute", nw+"connections/er", connection("ExpressRoute", vng, "peer", circuitPeering, ""), http.StatusCreated)

	if code, _ := do(t, a, http.MethodDelete, c, ""); code != http.StatusOK {
		t.Fatalf("delete: %d", code)
	}
	if _, ok := a.SharedKey(c); ok {
		t.Fatal("a deleted connection still has a shared key")
	}
}
