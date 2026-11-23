// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net"
	"net/http"
	"regexp"
	"strings"
)

// Network Watcher connection monitors and application security group address
// prefix sets (Microsoft.Network, API 2026-03-01), as far as the fake models
// them. Every rule is one the spec states:
//
//   - a connection monitor (the v2 shape) has at least one test group, and
//     each test group's sources and destinations name endpoints of the same
//     monitor and its test configurations name test configurations of the
//     same monitor (400); an endpoint that names a resource by resourceId
//     needs it to exist (404, as for any missing reference); a v1 monitor
//     (source and destination, no test groups) needs its source's resource
//     to exist (404);
//   - an address prefix set has at least one address prefix (minItems 1),
//     each in CIDR notation, IPv4 or IPv6 (400).
//
// Not modelled: a monitor running (no checks, no results in the workspace,
// no monitoringStatus beyond what was sent), the Network Watcher agent on a
// virtual machine, endpoint filters and scopes resolved, the network watcher
// being in the monitor's region, address prefix sets taking effect in
// security rules, and the error codes (the fake's own).
var (
	connectionMonitorType = regexp.MustCompile(`(?i)/providers/microsoft\.network/networkwatchers/[^/]+/connectionmonitors/[^/]+$`)
	addressPrefixSetType  = regexp.MustCompile(`(?i)/providers/microsoft\.network/applicationsecuritygroups/[^/]+/addressprefixsets/[^/]+$`)
)

// checkNetworkWatcher refuses a connection monitor or an address prefix set
// the rules above do not allow; code is "" when it is allowed or id is
// neither.
func (a *ARM) checkNetworkWatcher(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	switch {
	case connectionMonitorType.MatchString(id):
		return a.checkConnectionMonitor(props)
	case addressPrefixSetType.MatchString(id):
		prefixes, _ := props["addressPrefixes"].([]any)
		if len(prefixes) == 0 {
			return http.StatusBadRequest, "AddressPrefixesRequired", "An address prefix set has at least one address prefix."
		}
		for _, p := range prefixes {
			s, _ := p.(string)
			if _, _, err := net.ParseCIDR(s); err != nil {
				return http.StatusBadRequest, "InvalidAddressPrefix", "An address prefix is in CIDR notation, not " + s + "."
			}
		}
	}
	return 0, "", ""
}

func (a *ARM) checkConnectionMonitor(props map[string]any) (status int, code, message string) {
	groups, _ := props["testGroups"].([]any)
	if len(groups) == 0 {
		source, _ := props["source"].(map[string]any)
		resourceID, _ := source["resourceId"].(string)
		if resourceID == "" {
			return http.StatusBadRequest, "TestGroupsRequired", "A connection monitor has at least one test group (or, as a v1 monitor, a source)."
		}
		if !a.exists(resourceID) {
			return http.StatusNotFound, "ResourceNotFound", "The source resource " + resourceID + " was not found."
		}
		return 0, "", ""
	}
	endpoints := map[string]bool{}
	list, _ := props["endpoints"].([]any)
	for _, e := range list {
		endpoint, _ := e.(map[string]any)
		name, _ := endpoint["name"].(string)
		endpoints[strings.ToLower(name)] = true
		if resourceID, _ := endpoint["resourceId"].(string); resourceID != "" && !a.exists(resourceID) {
			return http.StatusNotFound, "ResourceNotFound", "The endpoint " + name + "'s resource " + resourceID + " was not found."
		}
	}
	configurations := map[string]bool{}
	list, _ = props["testConfigurations"].([]any)
	for _, c := range list {
		configuration, _ := c.(map[string]any)
		name, _ := configuration["name"].(string)
		configurations[strings.ToLower(name)] = true
	}
	for _, g := range groups {
		group, _ := g.(map[string]any)
		name, _ := group["name"].(string)
		for field, known := range map[string]map[string]bool{"sources": endpoints, "destinations": endpoints, "testConfigurations": configurations} {
			names, _ := group[field].([]any)
			if len(names) == 0 {
				return http.StatusBadRequest, "InvalidTestGroup", "The test group " + name + " names no " + field + "."
			}
			for _, n := range names {
				if s, _ := n.(string); !known[strings.ToLower(s)] {
					return http.StatusBadRequest, "InvalidTestGroup", "The test group " + name + "'s " + field + " name " + s + ", which the monitor does not define."
				}
			}
		}
	}
	return 0, "", ""
}
