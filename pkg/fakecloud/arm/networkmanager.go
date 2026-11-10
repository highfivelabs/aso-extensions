// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Azure Virtual Network Manager: network managers
// (Microsoft.Network/networkManagers), their scope connections
// (scopeConnections), network groups (networkGroups) and a group's static
// members (staticMembers), connectivity configurations
// (connectivityConfigurations), security admin and security user
// configurations (securityAdminConfigurations, securityUserConfigurations)
// with their rule collections (ruleCollections) and rules (rules), and
// network manager connections (networkManagerConnections) at a subscription
// or a management group, API 2026-03-01, as far as the fake models them:
//
//   - a network manager gives networkManagerScopes with at least one
//     subscription or management group (400), each one's ARM ID
//     (/subscriptions/{id}, /providers/Microsoft.Management/managementGroups/{id};
//     400 otherwise);
//   - a network manager connection names a network manager
//     (networkManagerId, 400 when missing or not a network manager's ID) that
//     exists (404);
//   - a scope connection gives a tenantId and the resourceId of a
//     subscription or management group (400); it is Pending
//     (connectionState) until the other tenant accepts it, which the fake
//     never does;
//   - a static member names a resource that exists (resourceId, 400 when
//     missing, 404 when not there), a virtual network when its group's
//     memberType is VirtualNetwork or left out and a subnet when it is
//     Subnet (400);
//   - a connectivity configuration's connectivityTopology is HubAndSpoke or
//     Mesh (400); a HubAndSpoke one gives hubs (400), each a resourceId that
//     exists (404);
//   - a connectivity configuration and a rule collection give
//     appliesToGroups (400), each networkGroupId a network group of the same
//     network manager (400) that exists (404);
//   - an admin rule's kind is Custom or Default (400); a Custom rule gives
//     protocol, direction and access (Allow, Deny or AlwaysAllow) and a
//     priority from 1 to 4096 (400); a Default rule gives its flag (400);
//   - a security user rule gives protocol and direction (400).
//
// Not modelled: a scope managed by two network managers for the same
// feature, the other tenant's side of a scope connection and a network
// manager connection's state, dynamic membership through Azure Policy, the
// region of a static member, configurations reaching the networks (nothing
// is deployed until committed), the rules a Default rule's flag names,
// address prefixes checked as CIDRs or service tags, two rules of one
// collection with one priority, and the error codes (this fake's own).
var (
	nmManager    = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/networkmanagers/[^/]+$`)
	nmConnection = regexp.MustCompile(`(?i)^(/subscriptions/[^/]+|/providers/microsoft\.management/managementgroups/[^/]+)/providers/microsoft\.network/networkmanagerconnections/[^/]+$`)
	nmScopeConn  = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/networkmanagers/[^/]+/scopeconnections/[^/]+$`)
	nmStatic     = regexp.MustCompile(`(?i)^((/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/networkmanagers/[^/]+)/networkgroups/[^/]+)/staticmembers/[^/]+$`)
	nmConfig     = regexp.MustCompile(`(?i)^(/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/networkmanagers/[^/]+)/connectivityconfigurations/[^/]+$`)
	nmCollection = regexp.MustCompile(`(?i)^(/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/networkmanagers/[^/]+)/(securityadminconfigurations|securityuserconfigurations)/[^/]+/rulecollections/[^/]+$`)
	nmRule       = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/networkmanagers/[^/]+/(securityadminconfigurations|securityuserconfigurations)/[^/]+/rulecollections/[^/]+/rules/[^/]+$`)
	nmVNet       = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/virtualnetworks/[^/]+$`)
	nmSubnet     = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/virtualnetworks/[^/]+/subnets/[^/]+$`)
)

// checkNetworkManager refuses a Virtual Network Manager resource the rules
// above do not allow; code is "" when it is allowed or id is no such
// resource.
func (a *ARM) checkNetworkManager(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		body["properties"] = props
	}
	switch {
	case nmManager.MatchString(id):
		scopes, _ := props["networkManagerScopes"].(map[string]any)
		subs, _ := scopes["subscriptions"].([]any)
		groups, _ := scopes["managementGroups"].([]any)
		if len(subs)+len(groups) == 0 {
			return http.StatusBadRequest, "NetworkManagerScopesMissing", "A network manager needs networkManagerScopes with a subscription or a management group."
		}
		for _, s := range subs {
			if v, _ := s.(string); !subscriptionScope.MatchString(v) {
				return http.StatusBadRequest, "InvalidNetworkManagerScope", "The scope " + v + " is not a subscription ID."
			}
		}
		for _, g := range groups {
			if v, _ := g.(string); !managementGroup.MatchString(v) {
				return http.StatusBadRequest, "InvalidNetworkManagerScope", "The scope " + v + " is not a management group ID."
			}
		}
	case nmConnection.MatchString(id):
		manager, _ := props["networkManagerId"].(string)
		if !nmManager.MatchString(manager) {
			return http.StatusBadRequest, "InvalidNetworkManagerId", "A network manager connection needs the networkManagerId of a network manager, not " + manager + "."
		}
		if !a.exists(manager) {
			return http.StatusNotFound, "ResourceNotFound", "The network manager " + manager + " was not found."
		}
	case nmScopeConn.MatchString(id):
		if tenant, _ := props["tenantId"].(string); tenant == "" {
			return http.StatusBadRequest, "ScopeConnectionTenantMissing", "A scope connection needs its tenantId."
		}
		if r, _ := props["resourceId"].(string); !subscriptionScope.MatchString(r) && !managementGroup.MatchString(r) {
			return http.StatusBadRequest, "InvalidScopeConnectionResource", "A scope connection needs the resourceId of a subscription or a management group, not " + r + "."
		}
		props["connectionState"] = "Pending"
	case nmStatic.MatchString(id):
		member, _ := props["resourceId"].(string)
		if member == "" {
			return http.StatusBadRequest, "StaticMemberResourceMissing", "A static member needs its resourceId."
		}
		if !a.exists(member) {
			return http.StatusNotFound, "ResourceNotFound", "The resource " + member + " was not found."
		}
		group := nmStatic.FindStringSubmatch(id)[1]
		memberType := ""
		if r, ok := a.resources[strings.ToLower(group)]; ok {
			gp, _ := (*r)["properties"].(map[string]any)
			memberType, _ = gp["memberType"].(string)
		}
		if strings.EqualFold(memberType, "Subnet") {
			if !nmSubnet.MatchString(member) {
				return http.StatusBadRequest, "InvalidStaticMember", "The network group's members are subnets; " + member + " is not one."
			}
		} else if !nmVNet.MatchString(member) {
			return http.StatusBadRequest, "InvalidStaticMember", "The network group's members are virtual networks; " + member + " is not one."
		}
	case nmConfig.MatchString(id):
		topology, _ := props["connectivityTopology"].(string)
		switch topology {
		case "HubAndSpoke":
			hubs, _ := props["hubs"].([]any)
			if len(hubs) == 0 {
				return http.StatusBadRequest, "ConnectivityConfigurationHubMissing", "A HubAndSpoke connectivity configuration needs its hubs."
			}
			for _, item := range hubs {
				h, _ := item.(map[string]any)
				hub, _ := h["resourceId"].(string)
				if hub == "" {
					return http.StatusBadRequest, "ConnectivityConfigurationHubMissing", "A hub needs its resourceId."
				}
				if !a.exists(hub) {
					return http.StatusNotFound, "ResourceNotFound", "The hub " + hub + " was not found."
				}
			}
		case "Mesh":
		default:
			return http.StatusBadRequest, "InvalidConnectivityTopology", "A connectivity configuration's connectivityTopology is HubAndSpoke or Mesh, not " + topology + "."
		}
		return a.checkAppliesTo(nmConfig.FindStringSubmatch(id)[1], props, "appliesToGroups")
	case nmCollection.MatchString(id):
		return a.checkAppliesTo(nmCollection.FindStringSubmatch(id)[1], props, "appliesToGroups")
	case nmRule.MatchString(id):
		if strings.EqualFold(nmRule.FindStringSubmatch(id)[1], "securityUserConfigurations") {
			return checkRuleBasics(props, "A security user rule")
		}
		switch kind, _ := body["kind"].(string); kind {
		case "Custom":
			if status, code, msg := checkRuleBasics(props, "A custom admin rule"); code != "" {
				return status, code, msg
			}
			switch access, _ := props["access"].(string); access {
			case "Allow", "Deny", "AlwaysAllow":
			default:
				return http.StatusBadRequest, "InvalidAdminRule", "A custom admin rule's access is Allow, Deny or AlwaysAllow, not " + access + "."
			}
			if p, _ := props["priority"].(float64); p < 1 || p > 4096 {
				return http.StatusBadRequest, "InvalidAdminRule", "A custom admin rule's priority is from 1 to 4096."
			}
		case "Default":
			if flag, _ := props["flag"].(string); flag == "" {
				return http.StatusBadRequest, "InvalidAdminRule", "A default admin rule needs its flag."
			}
		default:
			return http.StatusBadRequest, "InvalidAdminRule", "An admin rule's kind is Custom or Default, not " + kind + "."
		}
	}
	return 0, "", ""
}

// checkAppliesTo refuses a configuration or rule collection whose list
// (appliesToGroups) is empty or names a network group that is not one of
// manager's or is not there.
func (a *ARM) checkAppliesTo(manager string, props map[string]any, list string) (status int, code, message string) {
	groups, _ := props[list].([]any)
	if len(groups) == 0 {
		return http.StatusBadRequest, "AppliesToGroupsMissing", "A configuration or rule collection needs its " + list + "."
	}
	prefix := strings.ToLower(manager + "/networkGroups/")
	for _, item := range groups {
		g, _ := item.(map[string]any)
		group, _ := g["networkGroupId"].(string)
		if !strings.HasPrefix(strings.ToLower(group), prefix) || strings.Contains(group[len(prefix):], "/") {
			return http.StatusBadRequest, "InvalidNetworkGroup", "The networkGroupId " + group + " is not a network group of " + manager + "."
		}
		if !a.exists(group) {
			return http.StatusNotFound, "ResourceNotFound", "The network group " + group + " was not found."
		}
	}
	return 0, "", ""
}

// checkRuleBasics refuses a rule without a protocol or direction.
func checkRuleBasics(props map[string]any, what string) (status int, code, message string) {
	protocol, _ := props["protocol"].(string)
	direction, _ := props["direction"].(string)
	if protocol == "" || (direction != "Inbound" && direction != "Outbound") {
		return http.StatusBadRequest, "InvalidSecurityRule", what + " needs its protocol and a direction of Inbound or Outbound."
	}
	return 0, "", ""
}
