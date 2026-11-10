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
// members (staticMembers), and network manager connections
// (networkManagerConnections) at a subscription or a management group, API
// 2026-03-01, as far as the fake models them:
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
//     Subnet (400).
//
// Not modelled: a scope managed by two network managers for the same
// feature, the other tenant's side of a scope connection and a network
// manager connection's state, dynamic membership through Azure Policy, the
// region of a static member, and the error codes (this fake's own).
var (
	nmManager    = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/networkmanagers/[^/]+$`)
	nmConnection = regexp.MustCompile(`(?i)^(/subscriptions/[^/]+|/providers/microsoft\.management/managementgroups/[^/]+)/providers/microsoft\.network/networkmanagerconnections/[^/]+$`)
	nmScopeConn  = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/networkmanagers/[^/]+/scopeconnections/[^/]+$`)
	nmStatic     = regexp.MustCompile(`(?i)^((/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/networkmanagers/[^/]+)/networkgroups/[^/]+)/staticmembers/[^/]+$`)
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
	}
	return 0, "", ""
}
