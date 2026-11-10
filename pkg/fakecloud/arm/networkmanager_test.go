// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"encoding/json"
	"net/http"
	"testing"
)

// What a network manager, a network manager connection at a subscription and
// at a management group, a scope connection, a network group and its static
// members must give.
func TestNetworkManager(t *testing.T) {
	a := New(func(string) bool { return true })
	put := func(what, path, body string, want int) map[string]any {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
		out := map[string]any{}
		_ = json.Unmarshal([]byte(resp), &out)
		return out
	}
	sub := "/subscriptions/" + sub1
	rg := sub + "/resourceGroups/net"
	put("resource group", rg, `{"location":"westus"}`, http.StatusCreated)
	mg := "/providers/Microsoft.Management/managementGroups/platform"
	put("management group", mg, `{"properties":{}}`, http.StatusCreated)
	nw := rg + "/providers/Microsoft.Network/"

	nm := nw + "networkManagers/avnm"
	put("manager without scopes", nm, `{"location":"westus","properties":{}}`, http.StatusBadRequest)
	put("manager with empty scopes", nm, `{"location":"westus","properties":{"networkManagerScopes":{"subscriptions":[]}}}`, http.StatusBadRequest)
	put("manager over a resource group", nm, `{"location":"westus","properties":{"networkManagerScopes":{"subscriptions":["`+rg+`"]}}}`, http.StatusBadRequest)
	put("manager over a subscription as a group", nm, `{"location":"westus","properties":{"networkManagerScopes":{"managementGroups":["`+sub+`"]}}}`, http.StatusBadRequest)
	put("manager", nm, `{"location":"westus","properties":{"networkManagerScopes":{"subscriptions":["`+sub+`"],"managementGroups":["`+mg+`"]},"networkManagerScopeAccesses":["Connectivity","SecurityAdmin"]}}`, http.StatusCreated)

	for _, scope := range []string{sub, mg} {
		conn := scope + "/providers/Microsoft.Network/networkManagerConnections/avnm"
		put("connection without a manager", conn, `{"properties":{}}`, http.StatusBadRequest)
		put("connection to a resource group", conn, `{"properties":{"networkManagerId":"`+rg+`"}}`, http.StatusBadRequest)
		put("connection to a missing manager", conn, `{"properties":{"networkManagerId":"`+nm+`-nope"}}`, http.StatusNotFound)
		put("connection at "+scope, conn, `{"properties":{"networkManagerId":"`+nm+`","description":"joined"}}`, http.StatusCreated)
	}
	put("connection in a missing management group", mg+"-nope/providers/Microsoft.Network/networkManagerConnections/avnm",
		`{"properties":{"networkManagerId":"`+nm+`"}}`, http.StatusNotFound)

	sc := nm + "/scopeConnections/partner"
	put("scope connection without a tenant", sc, `{"properties":{"resourceId":"/subscriptions/00000000-0000-0000-0000-0000000000f9"}}`, http.StatusBadRequest)
	put("scope connection to a resource group", sc, `{"properties":{"tenantId":"t","resourceId":"`+rg+`"}}`, http.StatusBadRequest)
	r := put("scope connection", sc, `{"properties":{"tenantId":"t","resourceId":"/subscriptions/00000000-0000-0000-0000-0000000000f9"}}`, http.StatusCreated)
	if r["properties"].(map[string]any)["connectionState"] != "Pending" {
		t.Fatalf("a scope connection is Pending: %v", r)
	}

	vnet := nw + "virtualNetworks/spoke"
	a.AddResource(vnet)
	a.AddResource(vnet + "/subnets/app")
	group := nm + "/networkGroups/spokes"
	put("network group", group, `{"properties":{"memberType":"VirtualNetwork"}}`, http.StatusCreated)
	member := group + "/staticMembers/spoke"
	put("member without a resource", member, `{"properties":{}}`, http.StatusBadRequest)
	put("missing member", member, `{"properties":{"resourceId":"`+vnet+`-nope"}}`, http.StatusNotFound)
	put("subnet in a group of networks", member, `{"properties":{"resourceId":"`+vnet+`/subnets/app"}}`, http.StatusBadRequest)
	put("member", member, `{"properties":{"resourceId":"`+vnet+`"}}`, http.StatusCreated)

	subnets := nm + "/networkGroups/subnets"
	put("subnet group", subnets, `{"properties":{"memberType":"Subnet"}}`, http.StatusCreated)
	put("network in a group of subnets", subnets+"/staticMembers/spoke", `{"properties":{"resourceId":"`+vnet+`"}}`, http.StatusBadRequest)
	put("subnet member", subnets+"/staticMembers/app", `{"properties":{"resourceId":"`+vnet+`/subnets/app"}}`, http.StatusCreated)

	if code, resp := do(t, a, http.MethodDelete, nm, ""); code != http.StatusOK {
		t.Fatalf("delete manager: %d %s", code, resp)
	}
	if a.HasResource(member) || a.HasResource(sc) {
		t.Fatal("a network manager's children outlive it")
	}
}
