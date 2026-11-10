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

// What a connectivity configuration, a security admin or security user rule
// collection and their rules must give.
func TestNetworkManagerConfigurations(t *testing.T) {
	a := New(func(string) bool { return true })
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	sub := "/subscriptions/" + sub1
	rg := sub + "/resourceGroups/net"
	put("resource group", rg, `{"location":"westus"}`, http.StatusCreated)
	nw := rg + "/providers/Microsoft.Network/"
	nm := nw + "networkManagers/avnm"
	put("manager", nm, `{"location":"westus","properties":{"networkManagerScopes":{"subscriptions":["`+sub+`"]}}}`, http.StatusCreated)
	put("other manager", nw+"networkManagers/other", `{"location":"westus","properties":{"networkManagerScopes":{"subscriptions":["`+sub+`"]}}}`, http.StatusCreated)
	group := nm + "/networkGroups/spokes"
	put("network group", group, `{"properties":{"memberType":"VirtualNetwork"}}`, http.StatusCreated)
	put("other manager's group", nw+"networkManagers/other/networkGroups/spokes", `{"properties":{}}`, http.StatusCreated)
	hub := nw + "virtualNetworks/hub"
	a.AddResource(hub)

	applies := func(g string) string { return `"appliesToGroups":[{"networkGroupId":"` + g + `","groupConnectivity":"None"}]` }
	cc := nm + "/connectivityConfigurations/hub-spoke"
	put("connectivity of an unknown topology", cc, `{"properties":{"connectivityTopology":"Star",`+applies(group)+`}}`, http.StatusBadRequest)
	put("hub and spoke without hubs", cc, `{"properties":{"connectivityTopology":"HubAndSpoke",`+applies(group)+`}}`, http.StatusBadRequest)
	put("hub and spoke to a missing hub", cc, `{"properties":{"connectivityTopology":"HubAndSpoke","hubs":[{"resourceId":"`+hub+`-nope"}],`+applies(group)+`}}`, http.StatusNotFound)
	put("connectivity without groups", cc, `{"properties":{"connectivityTopology":"Mesh"}}`, http.StatusBadRequest)
	put("connectivity to another manager's group", cc, `{"properties":{"connectivityTopology":"Mesh",`+applies(nw+"networkManagers/other/networkGroups/spokes")+`}}`, http.StatusBadRequest)
	put("connectivity to a missing group", cc, `{"properties":{"connectivityTopology":"Mesh",`+applies(group+"-nope")+`}}`, http.StatusNotFound)
	put("hub and spoke", cc, `{"properties":{"connectivityTopology":"HubAndSpoke","hubs":[{"resourceId":"`+hub+`","resourceType":"Microsoft.Network/virtualNetworks"}],`+applies(group)+`}}`, http.StatusCreated)

	admin := nm + "/securityAdminConfigurations/baseline"
	put("security admin configuration", admin, `{"properties":{}}`, http.StatusCreated)
	coll := admin + "/ruleCollections/deny"
	put("collection without groups", coll, `{"properties":{}}`, http.StatusBadRequest)
	put("collection", coll, `{"properties":{"appliesToGroups":[{"networkGroupId":"`+group+`"}]}}`, http.StatusCreated)
	rule := coll + "/rules/rdp"
	custom := func(extra string) string {
		return `{"kind":"Custom","properties":{"protocol":"Tcp","direction":"Inbound"` + extra + `}}`
	}
	put("rule of an unknown kind", rule, `{"kind":"Other","properties":{}}`, http.StatusBadRequest)
	put("custom rule without a direction", rule, `{"kind":"Custom","properties":{"protocol":"Tcp","access":"Deny","priority":10}}`, http.StatusBadRequest)
	put("custom rule of an unknown access", rule, custom(`,"access":"Block","priority":10`), http.StatusBadRequest)
	put("custom rule out of priority", rule, custom(`,"access":"Deny","priority":5000`), http.StatusBadRequest)
	put("custom rule", rule, custom(`,"access":"Deny","priority":10,"destinationPortRanges":["3389"]`), http.StatusCreated)
	put("default rule without a flag", coll+"/rules/default", `{"kind":"Default","properties":{}}`, http.StatusBadRequest)
	put("default rule", coll+"/rules/default", `{"kind":"Default","properties":{"flag":"AllowVnetInbound"}}`, http.StatusCreated)

	user := nm + "/securityUserConfigurations/team"
	put("security user configuration", user, `{"properties":{}}`, http.StatusCreated)
	put("user collection", user+"/ruleCollections/app", `{"properties":{"appliesToGroups":[{"networkGroupId":"`+group+`"}]}}`, http.StatusCreated)
	put("user rule without a protocol", user+"/ruleCollections/app/rules/https", `{"properties":{"direction":"Inbound"}}`, http.StatusBadRequest)
	put("user rule", user+"/ruleCollections/app/rules/https", `{"properties":{"protocol":"Tcp","direction":"Inbound","destinationPortRanges":["443"]}}`, http.StatusCreated)
}
