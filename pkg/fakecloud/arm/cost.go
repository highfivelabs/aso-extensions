// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Budgets (Microsoft.Consumption, API 2026-06-01) and cost exports
// (Microsoft.CostManagement, API 2026-06-01), as far as the fake models them.
// Every rule is one the spec's descriptions state:
//
//   - both are kept at a Cost Management scope: a subscription, a resource
//     group, a management group, or a billing account or anything under one
//     (/providers/Microsoft.Billing/billingAccounts/...); not at a resource
//     (400);
//   - a budget has at most five notifications; a threshold is between 0 and
//     1000; contact groups (action groups, by ARM ID) are taken only at a
//     subscription or a resource group, and each must exist (404, as for any
//     missing reference); a notification needs a contact email, or at a
//     subscription or resource group a contact email or group; the time
//     period starts on the first of a month, on or after 2017-06-01, and ends
//     after it starts; a filter's "and" has at least two items (400 each);
//   - an export delivers to a storage account by resourceId, which must exist
//     (404), or by storageAccount with a sasToken (400 when neither); a
//     Custom timeframe needs a timePeriod, and a period (the data's or the
//     schedule's recurrence) ends after it starts; a managed identity needs
//     a location (400 each);
//   - an export's sasToken is never returned: what is kept, and read back,
//     has it obfuscated, as Azure's has; a system-assigned identity is given
//     a principal and tenant ID.
//
// Not modelled: spend (a budget's currentSpend and forecastSpend are never
// filled), notifications being sent, an export running or delivering
// anything (no run history, no next run time), the rule that a budget's
// future start is at most twelve months ahead and an export's recurrence
// starts in the future (both depend on the day the test runs), the
// subscription's registration with Microsoft.CostManagementExports, the
// time grains and export types an agreement type allows, and the error
// codes (this fake's own).
var (
	budgetType   = regexp.MustCompile(`(?i)/providers/microsoft\.consumption/budgets/[^/]+$`)
	exportType   = regexp.MustCompile(`(?i)/providers/microsoft\.costmanagement/exports/[^/]+$`)
	billingScope = regexp.MustCompile(`(?i)^/providers/microsoft\.billing/billingaccounts/[^/]+(/.*)?$`)
)

// obfuscated is what the fake keeps and returns in place of a SAS token.
const obfuscated = "********"

// checkCost refuses a budget or an export the rules above do not allow, and
// otherwise obfuscates an export's SAS token and fills its identity in body;
// code is "" when it is allowed or id is neither.
func (a *ARM) checkCost(id string, body map[string]any) (status int, code, message string) {
	budget := budgetType.MatchString(id)
	if !budget && !exportType.MatchString(id) {
		return 0, "", ""
	}
	scope := parentOf(id)
	if !costScope(scope) {
		return http.StatusBadRequest, "InvalidScope", "A budget or an export is kept at a subscription, a resource group, a management group or a billing scope, not at " + scope + "."
	}
	props, _ := body["properties"].(map[string]any)
	if budget {
		return a.checkBudget(scope, props)
	}
	return a.checkExport(id, body, props)
}

func costScope(scope string) bool {
	return subscriptionScope.MatchString(scope) || resourceGroupScope.MatchString(scope) ||
		managementGroup.MatchString(scope) || billingScope.MatchString(scope)
}

func (a *ARM) checkBudget(scope string, props map[string]any) (status int, code, message string) {
	bad := func(code, message string) (int, string, string) { return http.StatusBadRequest, code, message }
	notifications, _ := props["notifications"].(map[string]any)
	if len(notifications) > 5 {
		return bad("TooManyNotifications", fmt.Sprintf("A budget can have up to five notifications, not %d.", len(notifications)))
	}
	subOrRG := subscriptionScope.MatchString(scope) || resourceGroupScope.MatchString(scope)
	for name, n := range notifications {
		n, _ := n.(map[string]any)
		if t, ok := n["threshold"].(float64); ok && (t < 0 || t > 1000) {
			return bad("InvalidThreshold", fmt.Sprintf("Notification %s: the threshold is a percentage between 0 and 1000, not %v.", name, t))
		}
		emails, _ := n["contactEmails"].([]any)
		groups, _ := n["contactGroups"].([]any)
		if len(groups) > 0 && !subOrRG {
			return bad("ContactGroupsNotSupported", "Notification "+name+": contact groups are supported only at a subscription or a resource group, not at "+scope+".")
		}
		for _, g := range groups {
			g, _ := g.(string)
			if !a.exists(g) {
				return http.StatusNotFound, "ActionGroupNotFound", "Notification " + name + ": the action group '" + g + "' was not found."
			}
		}
		if len(emails) == 0 && (!subOrRG || len(groups) == 0) {
			if subOrRG {
				return bad("MissingContact", "Notification "+name+": give at least one contact email or contact group.")
			}
			return bad("MissingContact", "Notification "+name+": give at least one contact email (contact groups are not supported at "+scope+").")
		}
	}
	period, _ := props["timePeriod"].(map[string]any)
	start, startOK := date(period["startDate"])
	if startOK {
		if start.Day() != 1 {
			return bad("InvalidTimePeriod", "The start date must be the first of a month, not "+start.Format(time.DateOnly)+".")
		}
		if start.Before(time.Date(2017, 6, 1, 0, 0, 0, 0, time.UTC)) {
			return bad("InvalidTimePeriod", "The start date must be on or after 2017-06-01.")
		}
		if end, ok := date(period["endDate"]); ok && !end.After(start) {
			return bad("InvalidTimePeriod", "The end date must be after the start date.")
		}
	}
	filter, _ := props["filter"].(map[string]any)
	if and, ok := filter["and"].([]any); ok && len(and) < 2 {
		return bad("InvalidFilter", fmt.Sprintf("A filter's \"and\" must have at least two items, not %d.", len(and)))
	}
	return 0, "", ""
}

func (a *ARM) checkExport(id string, body, props map[string]any) (status int, code, message string) {
	bad := func(code, message string) (int, string, string) { return http.StatusBadRequest, code, message }
	delivery, _ := props["deliveryInfo"].(map[string]any)
	dest, _ := delivery["destination"].(map[string]any)
	resourceID, _ := dest["resourceId"].(string)
	account, _ := dest["storageAccount"].(string)
	sas, _ := dest["sasToken"].(string)
	switch {
	case resourceID != "":
		if !a.exists(resourceID) {
			return http.StatusNotFound, "StorageAccountNotFound", "The storage account '" + resourceID + "' was not found."
		}
	case account == "" || sas == "":
		return bad("InvalidDestination", "Give the destination's resourceId, or its storageAccount with a sasToken.")
	}
	definition, _ := props["definition"].(map[string]any)
	period, hasPeriod := definition["timePeriod"].(map[string]any)
	if definition["timeframe"] == "Custom" && !hasPeriod {
		return bad("InvalidTimePeriod", "A Custom timeframe needs a timePeriod.")
	}
	if msg := checkPeriod("timePeriod", period); msg != "" {
		return bad("InvalidTimePeriod", msg)
	}
	schedule, _ := props["schedule"].(map[string]any)
	if msg := checkPeriod("recurrencePeriod", asMap(schedule["recurrencePeriod"])); msg != "" {
		return bad("InvalidRecurrencePeriod", msg)
	}
	identity, _ := body["identity"].(map[string]any)
	if identity["type"] == "SystemAssigned" {
		if location, _ := body["location"].(string); location == "" {
			return bad("MissingLocation", "An export with a managed identity needs a location.")
		}
		sum := sha256.Sum256([]byte(strings.ToLower(id)))
		identity["principalId"] = formatGUID(sum[:16])
		identity["tenantId"] = "00000000-0000-0000-0000-00000000a5c0"
	}
	if sas != "" {
		dest["sasToken"] = obfuscated
	}
	return 0, "", ""
}

// checkPeriod is a message when a period's "to" is not after its "from".
func checkPeriod(name string, period map[string]any) string {
	from, ok := date(period["from"])
	to, ok2 := date(period["to"])
	if ok && ok2 && !to.After(from) {
		return name + ": the end must be after the start."
	}
	return ""
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// date parses an ISO 8601 date-time (or a date), as the specs' are.
func date(v any) (time.Time, bool) {
	s, _ := v.(string)
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", time.DateOnly} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func formatGUID(b []byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
