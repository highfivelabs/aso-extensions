// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

// A virtual network TAP has one destination that exists and a VXLAN port; a
// NIC's tap configuration names a TAP that exists; the TAP lists the tap
// configurations that name it.
func TestVirtualNetworkTap(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/net"
	collector := rg + "/providers/Microsoft.Network/networkInterfaces/collector/ipConfigurations/ipconfig1"
	frontend := rg + "/providers/Microsoft.Network/loadBalancers/ilb/frontendIPConfigurations/fe"
	nic := rg + "/providers/Microsoft.Network/networkInterfaces/web"
	for _, id := range []string{rg, collector, frontend, nic} {
		a.AddResource(id)
	}
	tap := rg + "/providers/Microsoft.Network/virtualNetworkTaps/mirror"
	putTap := func(what, props string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, tap, `{"location":"westus","properties":`+props+`}`)
		expect(t, what, code, resp, want)
	}
	putTap("a TAP with no destination", `{}`, http.StatusBadRequest)
	putTap("a TAP with two destinations", `{"destinationNetworkInterfaceIPConfiguration":{"id":"`+collector+`"},"destinationLoadBalancerFrontEndIPConfiguration":{"id":"`+frontend+`"}}`, http.StatusBadRequest)
	putTap("a missing collector", `{"destinationNetworkInterfaceIPConfiguration":{"id":"`+collector+`x"}}`, http.StatusNotFound)
	putTap("port 0", `{"destinationNetworkInterfaceIPConfiguration":{"id":"`+collector+`"},"destinationPort":0}`, http.StatusBadRequest)
	putTap("port 65536", `{"destinationNetworkInterfaceIPConfiguration":{"id":"`+collector+`"},"destinationPort":65536}`, http.StatusBadRequest)
	putTap("a TAP to a collector NIC", `{"destinationNetworkInterfaceIPConfiguration":{"id":"`+collector+`"},"destinationPort":4789}`, http.StatusCreated)
	putTap("the TAP to a load balancer", `{"destinationLoadBalancerFrontEndIPConfiguration":{"id":"`+frontend+`"}}`, http.StatusOK)

	conf := nic + "/tapConfigurations/mirror"
	putConf := func(what, props string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, conf, `{"properties":`+props+`}`)
		expect(t, what, code, resp, want)
	}
	putConf("a tap configuration naming no TAP", `{}`, http.StatusBadRequest)
	putConf("a missing TAP", `{"virtualNetworkTap":{"id":"`+tap+`x"}}`, http.StatusNotFound)
	putConf("a tap configuration", `{"virtualNetworkTap":{"id":"`+tap+`"}}`, http.StatusCreated)

	listed := func() any {
		t.Helper()
		code, resp := do(t, a, http.MethodGet, tap, "")
		expect(t, "the TAP", code, resp, http.StatusOK)
		var r map[string]any
		if err := json.Unmarshal([]byte(resp), &r); err != nil {
			t.Fatal(err)
		}
		return r["properties"].(map[string]any)["networkInterfaceTapConfigurations"]
	}
	if got, want := listed(), []any{map[string]any{"id": conf}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the TAP lists %v, want %v", got, want)
	}
	code, resp := do(t, a, http.MethodDelete, conf, "")
	expect(t, "deleting the tap configuration", code, resp, http.StatusOK)
	if got := listed(); !reflect.DeepEqual(got, []any{}) {
		t.Fatalf("the TAP still lists %v", got)
	}
}
