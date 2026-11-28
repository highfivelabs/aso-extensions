// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Container Apps sandbox groups and Azure SRE Agent agents
// (Microsoft.App, 2026-07-01; docs/plan.md f49), as far as the fake models
// them. Each rule is one the spec states:
//
//   - a sandbox group's environment link "cannot then be changed or
//     removed ... omitting or changing it returns 409 Conflict";
//   - the environment it links, and the subnet a sandbox group's network
//     connection names, exist (404, as for any missing reference);
//   - an agent's sandbox egress mode AzureVNet "requires a subnet, and a
//     configured subnet requires AzureVNet" (400).
//
// Not modelled: sandboxes running, a Dapr component's resiliency policy
// being applied, the agent investigating anything, its connectors reaching
// their data sources, and the error codes (the fake's own).
var (
	appSandboxGroup = regexp.MustCompile(`(?i)/providers/microsoft\.app/sandboxgroups/[^/]+$`)
	appSandboxVnet  = regexp.MustCompile(`(?i)/providers/microsoft\.app/sandboxgroups/[^/]+/vnetconnections/[^/]+$`)
	appSREAgent     = regexp.MustCompile(`(?i)/providers/microsoft\.app/agents/[^/]+$`)
)

// checkAppExtras refuses what the rules above do not allow; code is "" when
// it is allowed or id is none of these.
func (a *ARM) checkAppExtras(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	switch {
	case appSandboxGroup.MatchString(id):
		env, _ := props["environmentId"].(string)
		if res, ok := a.resources[strings.ToLower(id)]; ok {
			old, _ := (*res)["properties"].(map[string]any)
			if was, _ := old["environmentId"].(string); was != "" && !strings.EqualFold(was, env) {
				return http.StatusConflict, "Conflict", "A sandbox group's environment link cannot be changed or removed (" + was + ")."
			}
		}
		if env != "" && !a.exists(env) {
			return http.StatusNotFound, "ResourceNotFound", "The environment " + env + " was not found."
		}
	case appSandboxVnet.MatchString(id):
		subnet, _ := props["subnetId"].(string)
		if subnet == "" {
			return http.StatusBadRequest, "BadRequest", "A sandbox group's network connection names a subnet."
		}
		if !a.exists(subnet) {
			return http.StatusNotFound, "ResourceNotFound", "The subnet " + subnet + " was not found."
		}
	case appSREAgent.MatchString(id):
		vnet, _ := props["vnetConfiguration"].(map[string]any)
		subnet, _ := vnet["subnetResourceId"].(string)
		sandbox, _ := props["sandboxConfiguration"].(map[string]any)
		egress, _ := sandbox["egress"].(map[string]any)
		mode, _ := egress["mode"].(string)
		if strings.EqualFold(mode, "AzureVNet") != (subnet != "") {
			return http.StatusBadRequest, "InvalidVnetConfiguration", "An agent's subnet and the AzureVNet sandbox egress mode are configured together."
		}
	}
	return 0, "", ""
}
