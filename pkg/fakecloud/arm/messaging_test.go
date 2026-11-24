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

// An Event Hubs namespace has the same rule set and pairing rules (a Service
// Bus namespace is no partner), and an application group names its clients
// as SASKeyName= or AADAppID= with policies of unique names.
func TestEventHubsNamespaceChildren(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/hubs"
	ns := rg + "/providers/Microsoft.EventHub/namespaces/telemetry"
	west := rg + "/providers/Microsoft.EventHub/namespaces/telemetry-west"
	bus := rg + "/providers/Microsoft.ServiceBus/namespaces/orders"
	for _, id := range []string{rg, ns, west, bus} {
		a.AddResource(id)
	}
	put := func(what, id, props string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, id, `{"properties":`+props+`}`)
		expect(t, what, code, resp, want)
	}
	put("an IPv6 mask", ns+"/networkRuleSets/default", `{"ipRules":[{"ipMask":"2001:db8::1"}]}`, http.StatusBadRequest)
	put("a rule set", ns+"/networkRuleSets/default", `{"defaultAction":"Deny","ipRules":[{"ipMask":"203.0.113.0/24","action":"Allow"}]}`, http.StatusCreated)
	code, resp := do(t, a, http.MethodDelete, ns+"/networkRuleSets/default", "")
	expect(t, "deleting the rule set", code, resp, http.StatusMethodNotAllowed)
	put("a Service Bus partner", ns+"/disasterRecoveryConfigs/dr", `{"partnerNamespace":"`+bus+`"}`, http.StatusBadRequest)
	put("an alias", ns+"/disasterRecoveryConfigs/dr", `{"partnerNamespace":"`+west+`"}`, http.StatusCreated)

	group := ns + "/applicationGroups/ingest"
	put("a client identifier of neither form", group, `{"clientAppGroupIdentifier":"ingest","isEnabled":true}`, http.StatusBadRequest)
	put("an AAD identifier that is no GUID", group, `{"clientAppGroupIdentifier":"AADAppID=app"}`, http.StatusBadRequest)
	throttle := `{"name":"in","type":"ThrottlingPolicy","metricId":"IncomingMessages","rateLimitThreshold":100}`
	put("two policies of one name", group, `{"clientAppGroupIdentifier":"SASKeyName=ingest","policies":[`+throttle+`,`+throttle+`]}`, http.StatusBadRequest)
	put("an application group", group, `{"clientAppGroupIdentifier":"SASKeyName=ingest","isEnabled":true,"policies":[`+throttle+`]}`, http.StatusCreated)
	put("an application group of an app", ns+"/applicationGroups/app", `{"clientAppGroupIdentifier":"AADAppID=00000000-0000-0000-0000-0000000000a1"}`, http.StatusCreated)
	put("a connection no private endpoint made", ns+"/privateEndpointConnections/pe", `{"privateLinkServiceConnectionState":{"status":"Approved"}}`, http.StatusNotFound)
}
