// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Maintenance configuration assignments
// (Microsoft.Maintenance/configurationAssignments, API 2023-04-01) at any
// scope (a resource, a resource group or a subscription), as far as the fake
// models them:
//
//   - an assignment names a maintenance configuration that exists
//     (maintenanceConfigurationId, 400 when missing, 404 when not there);
//   - on a resource, its resourceId, when given, is that resource (400
//     otherwise); Azure sets it when it is left out;
//   - at a subscription or a resource group (dynamic scope) it gives a filter
//     and names an InGuestPatch configuration (400), the only scope Azure's
//     dynamic scoping serves.
//
// Not modelled: whether the resource type can be maintained by the
// configuration's scope (Host, OSImage, Extension), the filter selecting
// machines, and the error codes (this fake's own).
var (
	maintenanceAssignment = regexp.MustCompile(`(?i)^(.*)/providers/microsoft\.maintenance/configurationassignments/[^/]+$`)
	dynamicScope          = regexp.MustCompile(`(?i)^/subscriptions/[^/]+(/resourcegroups/[^/]+)?$`)
)

// checkMaintenance refuses a configuration assignment the rules above do not
// allow; code is "" when it is allowed or id is no assignment.
func (a *ARM) checkMaintenance(id string, body map[string]any) (status int, code, message string) {
	m := maintenanceAssignment.FindStringSubmatch(id)
	if m == nil {
		return 0, "", ""
	}
	scope := m[1]
	props, _ := body["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		body["properties"] = props
	}
	config, _ := props["maintenanceConfigurationId"].(string)
	if config == "" {
		return http.StatusBadRequest, "MaintenanceConfigurationIdMissing", "A configuration assignment needs its maintenanceConfigurationId."
	}
	r, ok := a.resources[strings.ToLower(config)]
	if !ok {
		return http.StatusNotFound, "ResourceNotFound", "The maintenance configuration " + config + " was not found."
	}
	if dynamicScope.MatchString(scope) {
		if _, ok := props["filter"].(map[string]any); !ok {
			return http.StatusBadRequest, "ConfigurationAssignmentFilterMissing", "A configuration assignment at a subscription or resource group needs a filter."
		}
		cp, _ := (*r)["properties"].(map[string]any)
		if s, _ := cp["maintenanceScope"].(string); !strings.EqualFold(s, "InGuestPatch") {
			return http.StatusBadRequest, "ConfigurationAssignmentScopeInvalid", "A configuration assignment at a subscription or resource group names an InGuestPatch configuration."
		}
		return 0, "", ""
	}
	if rid, _ := props["resourceId"].(string); rid != "" && !strings.EqualFold(rid, scope) {
		return http.StatusBadRequest, "ConfigurationAssignmentResourceInvalid", "A configuration assignment's resourceId is the resource it is on, " + scope + "."
	}
	props["resourceId"] = scope
	return 0, "", ""
}
