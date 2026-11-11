// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"strings"
	"testing"
)

// IP group addresses, a route filter's one Allow rule of Microsoft's BGP
// communities, a service endpoint policy definition's service and
// resources, and the filter and policy keeping their children only while
// their PUT lists them.
func TestFilters(t *testing.T) {
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

	ig := nw + "ipGroups/branches"
	put("IP group with a host name", ig, `{"location":"westus","properties":{"ipAddresses":["branch.example.com"]}}`, http.StatusBadRequest)
	put("IP group with an IPv6 prefix", ig, `{"location":"westus","properties":{"ipAddresses":["2001:db8::/32"]}}`, http.StatusBadRequest)
	put("IP group with a bad range", ig, `{"location":"westus","properties":{"ipAddresses":["10.0.0.1-nowhere"]}}`, http.StatusBadRequest)
	put("IP group", ig, `{"location":"westus","properties":{"ipAddresses":["10.10.0.0/16","192.0.2.7","10.20.0.1-10.20.0.9"]}}`, http.StatusCreated)

	rf := nw + "routeFilters/m365"
	rule := rf + "/routeFilterRules/allow-exchange"
	put("route filter", rf, `{"location":"westus"}`, http.StatusCreated)
	put("a Deny rule", rule, `{"location":"westus","properties":{"access":"Deny","routeFilterRuleType":"Community","communities":["12076:5010"]}}`, http.StatusBadRequest)
	put("a rule with no communities", rule, `{"location":"westus","properties":{"access":"Allow","routeFilterRuleType":"Community"}}`, http.StatusBadRequest)
	put("a rule with another ASN's community", rule, `{"location":"westus","properties":{"access":"Allow","routeFilterRuleType":"Community","communities":["65000:100"]}}`, http.StatusBadRequest)
	put("rule", rule, `{"location":"westus","properties":{"access":"Allow","routeFilterRuleType":"Community","communities":["12076:5010"]}}`, http.StatusCreated)
	put("the rule changed", rule, `{"location":"westus","properties":{"access":"Allow","routeFilterRuleType":"Community","communities":["12076:5010","12076:5040"]}}`, http.StatusOK)
	put("a second rule", rf+"/routeFilterRules/another", `{"location":"westus","properties":{"access":"Allow","routeFilterRuleType":"Community","communities":["12076:5020"]}}`, http.StatusBadRequest)
	_, got := do(t, a, http.MethodGet, rf, "")
	if !strings.Contains(got, `"rules":[`) || !strings.Contains(got, "allow-exchange") {
		t.Fatalf("the filter's GET does not list its rule: %s", got)
	}
	put("the filter with its rule", rf, `{"location":"westus","tags":{"a":"b"},"properties":{"rules":[{"name":"allow-exchange","properties":{"access":"Allow","routeFilterRuleType":"Community","communities":["12076:5010"]}}]}}`, http.StatusOK)
	if !a.HasResource(rule) {
		t.Fatal("the filter's PUT with its rule deleted the rule")
	}
	put("the filter with two rules", rf, `{"location":"westus","properties":{"rules":[{"name":"a","properties":{"access":"Allow","routeFilterRuleType":"Community","communities":["12076:5010"]}},{"name":"b","properties":{"access":"Allow","routeFilterRuleType":"Community","communities":["12076:5020"]}}]}}`, http.StatusBadRequest)
	put("the filter without its rule", rf, `{"location":"westus"}`, http.StatusOK)
	if a.HasResource(rule) {
		t.Fatal("the filter's PUT without its rule kept the rule")
	}

	sep := nw + "serviceEndpointPolicies/storage-only"
	def := sep + "/serviceEndpointPolicyDefinitions/ours"
	account := rg + "/providers/Microsoft.Storage/storageAccounts/ours"
	put("policy", sep, `{"location":"westus"}`, http.StatusCreated)
	put("a definition for another service", def, `{"properties":{"service":"Microsoft.Sql","serviceResources":["`+account+`"]}}`, http.StatusBadRequest)
	put("a definition with no resources", def, `{"properties":{"service":"Microsoft.Storage"}}`, http.StatusBadRequest)
	put("a definition naming a vault", def, `{"properties":{"service":"Microsoft.Storage","serviceResources":["`+rg+`/providers/Microsoft.KeyVault/vaults/v"]}}`, http.StatusBadRequest)
	put("definition", def, `{"properties":{"service":"Microsoft.Storage","serviceResources":["`+account+`","`+rg+`"]}}`, http.StatusCreated)
	put("the policy with its definition", sep, `{"location":"westus","properties":{"serviceEndpointPolicyDefinitions":[{"name":"ours","properties":{"service":"Microsoft.Storage","serviceResources":["`+account+`"]}}]}}`, http.StatusOK)
	if !a.HasResource(def) {
		t.Fatal("the policy's PUT with its definition deleted the definition")
	}
	put("the policy with a bad definition", sep, `{"location":"westus","properties":{"serviceEndpointPolicyDefinitions":[{"name":"x","properties":{"service":"Microsoft.Web","serviceResources":["`+account+`"]}}]}}`, http.StatusBadRequest)
	put("the policy without its definition", sep, `{"location":"westus"}`, http.StatusOK)
	if a.HasResource(def) {
		t.Fatal("the policy's PUT without its definition kept the definition")
	}
}
