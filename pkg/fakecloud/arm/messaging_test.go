// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// A Service Bus namespace has one network rule set, default, whose IP rules
// are IPv4 addresses or ranges and whose virtual network rules name subnets
// that exist; a disaster recovery alias pairs it with another namespace of
// the service, once.
func TestServiceBusNamespaceChildren(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/msg"
	ns := rg + "/providers/Microsoft.ServiceBus/namespaces/orders"
	west := rg + "/providers/Microsoft.ServiceBus/namespaces/orders-west"
	third := rg + "/providers/Microsoft.ServiceBus/namespaces/orders-third"
	hubs := rg + "/providers/Microsoft.EventHub/namespaces/orders-hub"
	subnet := rg + "/providers/Microsoft.Network/virtualNetworks/vnet/subnets/apps"
	for _, id := range []string{rg, ns, west, third, hubs, subnet} {
		a.AddResource(id)
	}
	put := func(what, id, props string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, id, `{"properties":`+props+`}`)
		expect(t, what, code, resp, want)
	}
	rules := ns + "/networkRuleSets/default"
	put("a rule set not named default", ns+"/networkRuleSets/other", `{"defaultAction":"Deny"}`, http.StatusNotFound)
	put("an IPv6 mask", rules, `{"defaultAction":"Deny","ipRules":[{"ipMask":"2001:db8::/32","action":"Allow"}]}`, http.StatusBadRequest)
	put("a mask that is no address", rules, `{"defaultAction":"Deny","ipRules":[{"ipMask":"office","action":"Allow"}]}`, http.StatusBadRequest)
	put("a virtual network rule with no subnet", rules, `{"defaultAction":"Deny","virtualNetworkRules":[{}]}`, http.StatusBadRequest)
	put("a missing subnet", rules, `{"defaultAction":"Deny","virtualNetworkRules":[{"subnet":{"id":"`+subnet+`x"}}]}`, http.StatusNotFound)
	put("a rule set", rules, `{"defaultAction":"Deny","publicNetworkAccess":"Enabled","ipRules":[{"ipMask":"203.0.113.0/24","action":"Allow"},{"ipMask":"198.51.100.7","action":"Allow"}],"virtualNetworkRules":[{"subnet":{"id":"`+subnet+`"}}]}`, http.StatusCreated)

	code, resp := do(t, a, http.MethodDelete, rules, "")
	expect(t, "deleting the rule set", code, resp, http.StatusMethodNotAllowed)

	alias := ns + "/disasterRecoveryConfigs/orders-dr"
	put("an alias with no partner", alias, `{}`, http.StatusBadRequest)
	put("a missing partner", alias, `{"partnerNamespace":"`+west+`x"}`, http.StatusNotFound)
	put("an Event Hubs partner", alias, `{"partnerNamespace":"`+hubs+`"}`, http.StatusBadRequest)
	put("the namespace as its own partner", alias, `{"partnerNamespace":"`+ns+`"}`, http.StatusBadRequest)
	put("an alias", alias, `{"partnerNamespace":"`+west+`"}`, http.StatusCreated)
	put("the alias again", alias, `{"partnerNamespace":"`+west+`"}`, http.StatusOK)
	put("a second alias on the namespace", ns+"/disasterRecoveryConfigs/again", `{"partnerNamespace":"`+third+`"}`, http.StatusConflict)
	put("an alias on the paired secondary", west+"/disasterRecoveryConfigs/back", `{"partnerNamespace":"`+third+`"}`, http.StatusConflict)
	put("an alias naming a paired namespace", third+"/disasterRecoveryConfigs/third-dr", `{"partnerNamespace":"`+west+`"}`, http.StatusConflict)

	put("a connection no private endpoint made", ns+"/privateEndpointConnections/pe", `{"privateLinkServiceConnectionState":{"status":"Approved"}}`, http.StatusNotFound)
}
