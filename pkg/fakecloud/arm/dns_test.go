// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"testing"
)

// A zone's SOA record set changed but never deleted alone; DNS security
// rules' priorities, actions and domain lists; a virtual network linked to
// one DNS security policy only.
func TestDNS(t *testing.T) {
	a := New(func(string) bool { return true })
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	del := func(what, path string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodDelete, path, "")
		expect(t, what, code, resp, want)
	}
	rg := "/subscriptions/" + sub1 + "/resourceGroups/dns"
	put("resource group", rg, `{"location":"westus"}`, http.StatusCreated)
	nw := rg + "/providers/Microsoft.Network/"

	for _, zone := range []string{nw + "dnsZones/example.com", nw + "privateDnsZones/internal.example"} {
		a.AddResource(zone)
		soa := zone + "/SOA/@"
		a.AddResource(soa)
		put("a SOA not named @", zone+"/SOA/other", `{"properties":{"soaRecord":{"email":"dns.example.com"}}}`, http.StatusBadRequest)
		put("the SOA changed", soa, `{"properties":{"ttl":3600,"soaRecord":{"email":"hostmaster.example.com","refreshTime":3600}}}`, http.StatusOK)
		del("the SOA alone", soa, http.StatusBadRequest)
		if !a.HasResource(soa) {
			t.Fatalf("%s was deleted alone", soa)
		}
		del("the zone", zone, http.StatusOK)
		if a.HasResource(soa) {
			t.Fatalf("%s outlived its zone", soa)
		}
	}

	vnet := nw + "virtualNetworks/spoke"
	a.AddResource(vnet)
	list := nw + "dnsResolverDomainLists/blocked"
	policy := nw + "dnsResolverPolicies/egress"
	put("domain list", list, `{"location":"westus","properties":{"domains":["malware.example."]}}`, http.StatusCreated)
	put("policy", policy, `{"location":"westus"}`, http.StatusCreated)
	rule := policy + "/dnsSecurityRules/block"
	ruleBody := func(priority, action, listID string) string {
		return `{"location":"westus","properties":{"priority":` + priority + `,"action":{"actionType":"` + action + `"},"dnsResolverDomainLists":[{"id":"` + listID + `"}]}}`
	}
	put("a priority under 100", rule, ruleBody("50", "Block", list), http.StatusBadRequest)
	put("an unknown action", rule, ruleBody("100", "Drop", list), http.StatusBadRequest)
	put("no domain lists", rule, `{"location":"westus","properties":{"priority":100,"action":{"actionType":"Block"}}}`, http.StatusBadRequest)
	put("a missing domain list", rule, ruleBody("100", "Block", list+"-nope"), http.StatusNotFound)
	put("rule", rule, ruleBody("100", "Block", list), http.StatusCreated)

	link := policy + "/virtualNetworkLinks/spoke"
	put("a link without a network", link, `{"location":"westus","properties":{}}`, http.StatusBadRequest)
	put("a link to a missing network", link, `{"location":"westus","properties":{"virtualNetwork":{"id":"`+vnet+`-nope"}}}`, http.StatusNotFound)
	put("link", link, `{"location":"westus","properties":{"virtualNetwork":{"id":"`+vnet+`"}}}`, http.StatusCreated)
	put("the link again", link, `{"location":"westus","tags":{"a":"b"},"properties":{"virtualNetwork":{"id":"`+vnet+`"}}}`, http.StatusOK)
	other := nw + "dnsResolverPolicies/other"
	put("another policy", other, `{"location":"westus"}`, http.StatusCreated)
	put("the network linked to a second policy", other+"/virtualNetworkLinks/spoke", `{"location":"westus","properties":{"virtualNetwork":{"id":"`+vnet+`"}}}`, http.StatusBadRequest)
	del("the link", link, http.StatusOK)
	put("the network linked to the other policy", other+"/virtualNetworkLinks/spoke", `{"location":"westus","properties":{"virtualNetwork":{"id":"`+vnet+`"}}}`, http.StatusCreated)
}
