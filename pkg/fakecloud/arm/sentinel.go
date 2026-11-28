// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Microsoft Sentinel settings and the workspace manager
// (Microsoft.SecurityInsights, API 2025-10-01-preview), as far as the fake
// models them. Every rule is one the spec's descriptions state:
//
//   - a setting is named after its kind (Anomalies, EntityAnalytics, EyesOn
//     or Ueba; 400 otherwise);
//   - a workspace manager member names its target workspace by
//     targetWorkspaceResourceId, which must exist (404, as for any missing
//     reference);
//   - a workspace manager group's memberResourceNames are members of the
//     same workspace manager (404 for one that is not there);
//   - a workspace manager assignment's targetResourceName is a group of the
//     same workspace manager (404), and each item's resourceId must exist
//     (404).
//
// Not modelled: a setting taking effect (anomaly rules, UEBA data sources),
// the workspace manager configuration's mode gating the others, an
// assignment's jobs publishing content to members (no lastJob* fields are
// filled), members in other tenants, and the error codes (the fake's own).
var (
	sentinelSetting = regexp.MustCompile(`(?i)/providers/microsoft\.securityinsights/settings/([^/]+)$`)
	managerMember   = regexp.MustCompile(`(?i)/providers/microsoft\.securityinsights/workspacemanagermembers/[^/]+$`)
	managerGroup    = regexp.MustCompile(`(?i)^(.*/providers/microsoft\.securityinsights)/workspacemanagergroups/[^/]+$`)
	managerAssign   = regexp.MustCompile(`(?i)^(.*/providers/microsoft\.securityinsights)/workspacemanagerassignments/[^/]+$`)
	settingKinds    = map[string]bool{"anomalies": true, "entityanalytics": true, "eyeson": true, "ueba": true}
)

// checkSentinel refuses a Sentinel setting or workspace manager resource the
// rules above do not allow; code is "" when it is allowed or id is none of
// them.
func (a *ARM) checkSentinel(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	if m := sentinelSetting.FindStringSubmatch(id); m != nil {
		kind, _ := body["kind"].(string)
		if !settingKinds[strings.ToLower(m[1])] || !strings.EqualFold(kind, m[1]) {
			return http.StatusBadRequest, "InvalidSetting", "A Sentinel setting is named after its kind (Anomalies, EntityAnalytics, EyesOn or Ueba): " + m[1] + " with kind " + kind + "."
		}
		return 0, "", ""
	}
	if managerMember.MatchString(id) {
		target, _ := props["targetWorkspaceResourceId"].(string)
		if !a.exists(target) {
			return http.StatusNotFound, "ResourceNotFound", "The target workspace " + target + " was not found."
		}
		return 0, "", ""
	}
	if m := managerGroup.FindStringSubmatch(id); m != nil {
		names, _ := props["memberResourceNames"].([]any)
		for _, n := range names {
			name, _ := n.(string)
			if !a.exists(m[1] + "/workspaceManagerMembers/" + name) {
				return http.StatusNotFound, "MemberNotFound", "The workspace manager member " + name + " was not found."
			}
		}
		return 0, "", ""
	}
	if m := managerAssign.FindStringSubmatch(id); m != nil {
		group, _ := props["targetResourceName"].(string)
		if !a.exists(m[1] + "/workspaceManagerGroups/" + group) {
			return http.StatusNotFound, "GroupNotFound", "The workspace manager group " + group + " was not found."
		}
		items, _ := props["items"].([]any)
		for _, i := range items {
			item, _ := i.(map[string]any)
			if resourceID, _ := item["resourceId"].(string); !a.exists(resourceID) {
				return http.StatusNotFound, "ResourceNotFound", "The assigned resource " + resourceID + " was not found."
			}
		}
	}
	return 0, "", ""
}
