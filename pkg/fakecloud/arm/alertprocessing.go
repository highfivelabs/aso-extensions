// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Alert processing rules (Microsoft.AlertsManagement/actionRules,
// 2021-08-08; docs/plan.md f58), as far as the fake models them. Each rule
// is one the spec or Azure Monitor's documentation of alert processing
// rules states:
//
//   - a rule is a global resource (location Global; 400);
//   - it applies to at least one scope (a subscription, resource group or
//     resource) that exists (400, 404);
//   - it has exactly one action: AddActionGroups, naming one to five action
//     groups that exist (400, 404), or RemoveAllActionGroups (400);
//   - a schedule's effectiveUntil is after its effectiveFrom (400); a
//     Weekly recurrence names its days of the week and a Monthly one its
//     days of the month, 1 to 31 (400).
//
// Not modelled: alerts being processed (no alert fires here), the time zone
// names, and the error codes (the fake's own).
var alertProcessingRule = regexp.MustCompile(`(?i)/providers/microsoft\.alertsmanagement/actionrules/[^/]+$`)

// checkAlertProcessingRule refuses a rule the rules above do not allow;
// code is "" when it is allowed or id is not one.
func (a *ARM) checkAlertProcessingRule(id string, body map[string]any) (status int, code, message string) {
	if !alertProcessingRule.MatchString(id) {
		return 0, "", ""
	}
	bad := func(msg string) (int, string, string) {
		return http.StatusBadRequest, "InvalidAlertProcessingRule", msg
	}
	if loc, _ := body["location"].(string); !strings.EqualFold(loc, "global") {
		return bad("An alert processing rule's location is Global, not " + loc + ".")
	}
	props, _ := body["properties"].(map[string]any)
	scopes, _ := props["scopes"].([]any)
	if len(scopes) == 0 {
		return bad("An alert processing rule applies to at least one scope.")
	}
	for _, s := range scopes {
		if id, _ := s.(string); !a.exists(id) {
			return http.StatusNotFound, "ResourceNotFound", "The scope " + id + " was not found."
		}
	}
	actions, _ := props["actions"].([]any)
	if len(actions) != 1 {
		return bad("An alert processing rule has exactly one action.")
	}
	action, _ := actions[0].(map[string]any)
	switch t, _ := action["actionType"].(string); t {
	case "AddActionGroups":
		groups, _ := action["actionGroupIds"].([]any)
		if len(groups) == 0 || len(groups) > 5 {
			return bad("AddActionGroups names one to five action groups.")
		}
		for _, g := range groups {
			if id, _ := g.(string); !a.exists(id) {
				return http.StatusNotFound, "ResourceNotFound", "The action group " + id + " was not found."
			}
		}
	case "RemoveAllActionGroups":
	default:
		return bad("An action is AddActionGroups or RemoveAllActionGroups.")
	}
	schedule, _ := props["schedule"].(map[string]any)
	from, _ := schedule["effectiveFrom"].(string)
	until, _ := schedule["effectiveUntil"].(string)
	if from != "" && until != "" && until <= from {
		return bad("A schedule's effectiveUntil is after its effectiveFrom.")
	}
	recurrences, _ := schedule["recurrences"].([]any)
	for _, r := range recurrences {
		rm, _ := r.(map[string]any)
		switch t, _ := rm["recurrenceType"].(string); t {
		case "Weekly":
			if days, _ := rm["daysOfWeek"].([]any); len(days) == 0 {
				return bad("A Weekly recurrence names its days of the week.")
			}
		case "Monthly":
			days, _ := rm["daysOfMonth"].([]any)
			if len(days) == 0 {
				return bad("A Monthly recurrence names its days of the month.")
			}
			for _, d := range days {
				if n, _ := d.(float64); n < 1 || n > 31 {
					return bad("A day of the month is 1 to 31.")
				}
			}
		}
	}
	return 0, "", ""
}
