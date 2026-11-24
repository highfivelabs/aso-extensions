// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net"
	"net/http"
	"regexp"
	"strings"
)

// A Service Bus namespace's network rule set and geo-disaster recovery alias
// (Microsoft.ServiceBus/namespaces/networkRuleSets/default and
// disasterRecoveryConfigs, 2026-01-01; docs/plan.md f61), as far as the fake
// models them. Each rule is one the spec or Azure's documentation of the
// namespace's network security and geo-disaster recovery states:
//
//   - a namespace has one network rule set, named default (404 for any
//     other name, as the spec's path has no name parameter), which has no
//     DELETE (405): it goes with its namespace;
//   - an IP rule's ipMask is an IPv4 address or an IPv4 CIDR range (400);
//   - a virtual network rule names a subnet (400) that exists (404);
//   - a disaster recovery alias names a partner namespace (400) that exists
//     (404) and is another namespace of the same service (400); a namespace
//     is in one pairing at most, as primary or secondary (409).
//
// The connections of a namespace's private endpoints are privateendpoints.go's.
// Not modelled: the tiers that take IP and virtual network rules, the
// partner's tier and region, the secondary's copy of the alias, replication,
// breaking the pairing and failover (POSTs, not faked; Azure wants the pairing
// broken before the alias is deleted), and the error codes (the fake's own).
var (
	messagingRuleSet = regexp.MustCompile(`(?i)/providers/microsoft\.servicebus/namespaces/[^/]+/networkrulesets/([^/]+)$`)
	messagingAlias   = regexp.MustCompile(`(?i)^(.*/providers/microsoft\.servicebus/namespaces/[^/]+)/disasterrecoveryconfigs/[^/]+$`)
	messagingNS      = regexp.MustCompile(`(?i)/providers/(microsoft\.servicebus)/namespaces/[^/]+$`)
)

// checkMessaging refuses a network rule set or a disaster recovery alias the
// rules above do not allow; code is "" when it is allowed or id is neither.
func (a *ARM) checkMessaging(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	if m := messagingRuleSet.FindStringSubmatch(id); m != nil {
		if !strings.EqualFold(m[1], "default") {
			return http.StatusNotFound, "ResourceNotFound", "A namespace has one network rule set, default; " + m[1] + " is not it."
		}
		bad := func(msg string) (int, string, string) {
			return http.StatusBadRequest, "InvalidNetworkRuleSet", msg
		}
		ipRules, _ := props["ipRules"].([]any)
		for _, r := range ipRules {
			rm, _ := r.(map[string]any)
			if mask, _ := rm["ipMask"].(string); !ipv4OrRange(mask) {
				return bad("An IP rule's ipMask is an IPv4 address or CIDR range, not " + mask + ".")
			}
		}
		vnetRules, _ := props["virtualNetworkRules"].([]any)
		for _, r := range vnetRules {
			rm, _ := r.(map[string]any)
			subnet := refID(rm, "subnet")
			if subnet == "" {
				return bad("A virtual network rule names a subnet.")
			}
			if !a.exists(subnet) {
				return http.StatusNotFound, "ResourceNotFound", "The subnet " + subnet + " was not found."
			}
		}
		return 0, "", ""
	}
	if m := messagingAlias.FindStringSubmatch(id); m != nil {
		partner, _ := props["partnerNamespace"].(string)
		if partner == "" {
			return http.StatusBadRequest, "InvalidDisasterRecoveryConfig", "A disaster recovery alias names its partner namespace."
		}
		if !a.exists(partner) {
			return http.StatusNotFound, "ResourceNotFound", "The partner namespace " + partner + " was not found."
		}
		pm, nm := messagingNS.FindStringSubmatch(partner), messagingNS.FindStringSubmatch(m[1])
		if pm == nil || nm == nil || !strings.EqualFold(pm[1], nm[1]) || strings.EqualFold(partner, m[1]) {
			return http.StatusBadRequest, "InvalidDisasterRecoveryConfig", "The partner of " + m[1] + " is another namespace of the same service, not " + partner + "."
		}
		for k, r := range a.resources {
			if strings.EqualFold(k, id) || !messagingAlias.MatchString(k) {
				continue
			}
			p, _ := (*r)["properties"].(map[string]any)
			other, _ := p["partnerNamespace"].(string)
			owner := messagingAlias.FindStringSubmatch(k)[1]
			if strings.EqualFold(owner, m[1]) || strings.EqualFold(other, partner) || strings.EqualFold(owner, partner) || strings.EqualFold(other, m[1]) {
				return http.StatusConflict, "Conflict", "The namespace " + partner + " or " + m[1] + " is already paired by " + (*r)["id"].(string) + "."
			}
		}
	}
	return 0, "", ""
}

// ipv4OrRange is whether s is an IPv4 address or an IPv4 CIDR range.
func ipv4OrRange(s string) bool {
	if _, n, err := net.ParseCIDR(s); err == nil {
		return n.IP.To4() != nil
	}
	return ipv4(s)
}
