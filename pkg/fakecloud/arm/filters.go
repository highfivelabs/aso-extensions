// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net"
	"net/http"
	"regexp"
	"strings"
)

// IP groups (Microsoft.Network/ipGroups), route filters
// (Microsoft.Network/routeFilters) and their rules (routeFilterRules), and
// service endpoint policies (Microsoft.Network/serviceEndpointPolicies) and
// their definitions (serviceEndpointPolicyDefinitions), API 2026-03-01, as
// far as the fake models them (foundation row 12):
//
//   - an IP group's ipAddresses are IPv4 addresses, CIDR prefixes or ranges
//     (a.b.c.d-e.f.g.h); anything else is 400 InvalidAddressPrefixFormat;
//   - a route filter rule allows (access Allow; Azure takes no Deny rule),
//     is of type Community and names at least one BGP community value
//     (12076:NNNN or 12076:NNNNN, Microsoft's ASN), each 400 otherwise; a route filter
//     has at most one rule (400 for a second);
//   - a service endpoint policy definition is for the service
//     Microsoft.Storage and names at least one subscription, resource
//     group or storage account ID in serviceResources (400 otherwise);
//   - a route filter holds its rules, and a policy its definitions, as
//     child resources and as a list in its own properties, the way a
//     circuit holds its peerings: a GET renders the list from the children,
//     and a PUT of the parent makes the children what the list says, so a
//     PUT without the list deletes them.
//
// Not modelled: a route filter being attached to a circuit's Microsoft
// peering (routeFilter on the peering is kept as sent) and the routes it
// lets through; which firewalls and firewall policies use an IP group (the
// read-only firewalls and firewallPolicies), and the limits on IP groups per
// firewall and addresses per group; a subnet's serviceEndpointPolicies
// (ASO's VirtualNetworksSubnet) and whether its service endpoint is on;
// service aliases and contextual policies; the error codes other than
// InvalidAddressPrefixFormat (this fake's own).
var (
	ipGroup           = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/ipgroups/[^/]+$`)
	routeFilter       = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/routefilters/[^/]+$`)
	routeFilterRuleID = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/routefilters/[^/]+/routefilterrules/[^/]+$`)
	sePolicy          = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/serviceendpointpolicies/[^/]+$`)
	sePolicyDef       = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/serviceendpointpolicies/[^/]+/serviceendpointpolicydefinitions/[^/]+$`)
	bgpCommunity      = regexp.MustCompile(`^12076:\d{4,5}$`)
	seResource        = regexp.MustCompile(`(?i)^/subscriptions/[^/]+(/resourcegroups/[^/]+(/providers/microsoft\.storage/storageaccounts/[^/]+)?)?$`)
)

func init() {
	erChildLists[routeFilter] = []string{"rules"}
	erChildLists[sePolicy] = []string{"serviceEndpointPolicyDefinitions"}
	childSegment[routeFilter] = map[string]string{"rules": "routeFilterRules"}
}

// checkFilters refuses an IP group, a route filter rule or a service
// endpoint policy definition the rules above do not allow, whether it comes
// as its own resource or in its parent's list. code is "" when it is
// allowed or id is none of these.
func (a *ARM) checkFilters(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	switch {
	case ipGroup.MatchString(id):
		addresses, _ := props["ipAddresses"].([]any)
		for _, v := range addresses {
			s, _ := v.(string)
			if !ipGroupAddress(s) {
				return http.StatusBadRequest, "InvalidAddressPrefixFormat", "The IP group address " + s + " is not an IPv4 address, prefix or range."
			}
		}
	case routeFilter.MatchString(id):
		rules, _ := props["rules"].([]any)
		if len(rules) > 1 {
			return http.StatusBadRequest, "RouteFilterRuleLimitExceeded", "A route filter has at most one rule."
		}
		for _, r := range rules {
			rule, _ := r.(map[string]any)
			rp, _ := rule["properties"].(map[string]any)
			if code, msg := routeFilterRuleRefusal(rp); code != "" {
				return http.StatusBadRequest, code, msg
			}
		}
	case routeFilterRuleID.MatchString(id):
		if code, msg := routeFilterRuleRefusal(props); code != "" {
			return http.StatusBadRequest, code, msg
		}
		filter := strings.ToLower(parentOf(id))
		prefix := filter + "/routefilterrules/"
		for k := range a.resources {
			if strings.HasPrefix(k, prefix) && !strings.Contains(k[len(prefix):], "/") && k != strings.ToLower(id) {
				return http.StatusBadRequest, "RouteFilterRuleLimitExceeded", "A route filter has at most one rule; " + parentOf(id) + " has one."
			}
		}
	case sePolicy.MatchString(id):
		defs, _ := props["serviceEndpointPolicyDefinitions"].([]any)
		for _, d := range defs {
			def, _ := d.(map[string]any)
			dp, _ := def["properties"].(map[string]any)
			if code, msg := sePolicyDefinitionRefusal(dp); code != "" {
				return http.StatusBadRequest, code, msg
			}
		}
	case sePolicyDef.MatchString(id):
		if code, msg := sePolicyDefinitionRefusal(props); code != "" {
			return http.StatusBadRequest, code, msg
		}
	}
	return 0, "", ""
}

func routeFilterRuleRefusal(props map[string]any) (code, message string) {
	if props["access"] != "Allow" {
		return "RouteFilterRuleAccessNotAllowed", "A route filter rule's access is Allow."
	}
	if props["routeFilterRuleType"] != "Community" {
		return "RouteFilterRuleTypeInvalid", "A route filter rule's routeFilterRuleType is Community."
	}
	communities, _ := props["communities"].([]any)
	if len(communities) == 0 {
		return "RouteFilterRuleCommunitiesMissing", "A route filter rule names at least one BGP community."
	}
	for _, c := range communities {
		if s, _ := c.(string); !bgpCommunity.MatchString(s) {
			return "InvalidBgpCommunity", "The BGP community " + s + " is not one of Microsoft's (12076:NNNN or 12076:NNNNN)."
		}
	}
	return "", ""
}

func sePolicyDefinitionRefusal(props map[string]any) (code, message string) {
	if props["service"] != "Microsoft.Storage" {
		return "ServiceEndpointPolicyDefinitionServiceInvalid", "A service endpoint policy definition's service is Microsoft.Storage."
	}
	resources, _ := props["serviceResources"].([]any)
	if len(resources) == 0 {
		return "ServiceEndpointPolicyDefinitionResourcesMissing", "A service endpoint policy definition names at least one service resource."
	}
	for _, r := range resources {
		if s, _ := r.(string); !seResource.MatchString(s) {
			return "ServiceEndpointPolicyDefinitionResourceInvalid", "The service resource " + s + " is not a subscription, resource group or storage account ID."
		}
	}
	return "", ""
}

// ipGroupAddress is whether s is an IPv4 address, prefix or range.
func ipGroupAddress(s string) bool {
	if from, to, ok := strings.Cut(s, "-"); ok {
		return ipv4(from) && ipv4(to)
	}
	if _, n, err := net.ParseCIDR(s); err == nil {
		return n.IP.To4() != nil
	}
	return ipv4(s)
}

func ipv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil
}
