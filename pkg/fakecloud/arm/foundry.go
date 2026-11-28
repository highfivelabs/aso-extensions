// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Microsoft Foundry's newer account children (Microsoft.CognitiveServices,
// 2026-09-01; docs/plan.md f44), as far as the fake models them. Each rule
// is one the spec states:
//
//   - an account's managed network is named default ("Only 'default' is
//     supported"; 400);
//   - its kind can go from V1 to V2 and not back ("cannot switch back to V1
//     once V2 is enabled"; 400);
//   - a private endpoint outbound rule names the resource it reaches
//     (destination.serviceResourceId), which must exist (404, as for any
//     missing reference).
//
// Not modelled: the managed network being provisioned (its firewall, the
// private endpoints the rules make), agent applications and deployments
// running, RAI topics being trained from their sample blob, tool labels
// being applied, and the error codes (the fake's own).
var (
	foundryManagedNetwork = regexp.MustCompile(`(?i)/providers/microsoft\.cognitiveservices/accounts/[^/]+/managednetworks/([^/]+)$`)
	foundryOutboundRule   = regexp.MustCompile(`(?i)/providers/microsoft\.cognitiveservices/accounts/[^/]+/managednetworks/[^/]+/outboundrules/[^/]+$`)
)

// checkFoundry refuses what the rules above do not allow; code is "" when it
// is allowed or id is none of these.
func (a *ARM) checkFoundry(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	switch {
	case foundryManagedNetwork.MatchString(id):
		if name := foundryManagedNetwork.FindStringSubmatch(id)[1]; !strings.EqualFold(name, "default") {
			return http.StatusBadRequest, "InvalidManagedNetworkName", "An account's managed network is named default, not " + name + "."
		}
		network, _ := props["managedNetwork"].(map[string]any)
		kind, _ := network["managedNetworkKind"].(string)
		if res, ok := a.resources[strings.ToLower(id)]; ok && strings.EqualFold(kind, "V1") {
			old, _ := (*res)["properties"].(map[string]any)
			oldNetwork, _ := old["managedNetwork"].(map[string]any)
			if was, _ := oldNetwork["managedNetworkKind"].(string); strings.EqualFold(was, "V2") {
				return http.StatusBadRequest, "ManagedNetworkKindDowngrade", "A managed network cannot switch back to V1 once V2 is enabled."
			}
		}
	case foundryOutboundRule.MatchString(id):
		if t, _ := props["type"].(string); !strings.EqualFold(t, "PrivateEndpoint") {
			return 0, "", ""
		}
		dest, _ := props["destination"].(map[string]any)
		if target, _ := dest["serviceResourceId"].(string); !a.exists(target) {
			return http.StatusNotFound, "ResourceNotFound", "The outbound rule's serviceResourceId " + target + " was not found."
		}
	}
	return 0, "", ""
}
