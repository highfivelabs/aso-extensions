// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// A DNS zone's SOA record set (Microsoft.Network/dnsZones/SOA, API
// 2018-05-01, and privateDnsZones/SOA, 2024-06-01) and DNS security
// policies (Microsoft.Network/dnsResolverPolicies with their
// dnsSecurityRules and virtualNetworkLinks, and dnsResolverDomainLists, API
// 2025-05-01), as far as the fake models them (foundation row 12):
//
//   - a zone has one SOA record set, named @ (400 for another name); a PUT
//     changes it; it cannot be deleted alone (400), only with its zone;
//   - a DNS security rule has a priority from 100 to 65000, an action
//     (actionType Allow, Alert or Block) and at least one domain list, each
//     of which exists (400, or 404 for a list that is not there);
//   - a policy's virtual network link names a virtual network that exists
//     (400 when missing, 404 when not there), and a virtual network is
//     linked to at most one DNS security policy (400 for a second).
//
// A zone makes its SOA record set itself (computed.go). Not modelled: the
// SOA's host (Azure keeps its own name
// server whatever is sent), serial numbers moving, a domain list's domains
// being well formed and its bulk upload (domainsUrl), queries being
// answered, allowed, alerted or blocked, the limits on rules, lists and
// domains, and the error codes (this fake's own).
var (
	soaRecordSet    = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/(dnszones|privatednszones)/[^/]+/soa/([^/]+)$`)
	dnsSecurityRule = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/dnsresolverpolicies/[^/]+/dnssecurityrules/[^/]+$`)
	dnsPolicyLink   = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/dnsresolverpolicies/[^/]+/virtualnetworklinks/[^/]+$`)
	dnsDomainList   = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/dnsresolverdomainlists/[^/]+$`)
)

// checkDNS refuses a SOA record set, a DNS security rule or a policy's
// virtual network link the rules above do not allow. code is "" when it is
// allowed or id is none of these.
func (a *ARM) checkDNS(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	switch {
	case soaRecordSet.MatchString(id):
		if m := soaRecordSet.FindStringSubmatch(id); m[2] != "@" {
			return http.StatusBadRequest, "BadRequest", "A zone's SOA record set is named @."
		}
	case dnsSecurityRule.MatchString(id):
		p, ok := props["priority"].(float64)
		if !ok || p < 100 || p > 65000 {
			return http.StatusBadRequest, "InvalidDnsSecurityRulePriority", "A DNS security rule's priority is from 100 to 65000."
		}
		action, _ := props["action"].(map[string]any)
		switch action["actionType"] {
		case "Allow", "Alert", "Block":
		default:
			return http.StatusBadRequest, "InvalidDnsSecurityRuleAction", "A DNS security rule's action is Allow, Alert or Block."
		}
		lists, _ := props["dnsResolverDomainLists"].([]any)
		if len(lists) == 0 {
			return http.StatusBadRequest, "DnsResolverDomainListsMissing", "A DNS security rule names at least one domain list."
		}
		for _, l := range lists {
			ref, _ := l.(map[string]any)
			listID, _ := ref["id"].(string)
			if !dnsDomainList.MatchString(listID) {
				return http.StatusBadRequest, "InvalidDnsResolverDomainList", "The domain list " + listID + " is not a DNS resolver domain list ID."
			}
			if !a.exists(listID) {
				return http.StatusNotFound, "ResourceNotFound", "The domain list " + listID + " was not found."
			}
		}
	case dnsPolicyLink.MatchString(id):
		vnet := refID(props, "virtualNetwork")
		if vnet == "" {
			return http.StatusBadRequest, "VirtualNetworkMissing", "A DNS security policy's virtual network link names a virtual network."
		}
		if !a.exists(vnet) {
			return http.StatusNotFound, "ResourceNotFound", "The virtual network " + vnet + " was not found."
		}
		policy := strings.ToLower(parentOf(id))
		for k, r := range a.resources {
			if !dnsPolicyLink.MatchString(k) || strings.HasPrefix(k, policy+"/") {
				continue
			}
			rp, _ := (*r)["properties"].(map[string]any)
			if strings.EqualFold(refID(rp, "virtualNetwork"), vnet) {
				return http.StatusBadRequest, "VirtualNetworkAlreadyLinked", "The virtual network " + vnet + " is linked to another DNS security policy, " + parentOf(k) + "."
			}
		}
	}
	return 0, "", ""
}

// soaDeleteRefused is whether a DELETE of id is refused: a zone's SOA
// record set goes only with its zone.
func soaDeleteRefused(id string) bool {
	return soaRecordSet.MatchString(id)
}
