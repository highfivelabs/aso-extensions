// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// An alert processing rule is global, applies to scopes that exist, has
// one action naming action groups that exist, and a schedule in order with
// the days its recurrences need.
func TestAlertProcessingRules(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/ops"
	ag := rg + "/providers/Microsoft.Insights/actionGroups/oncall"
	for _, id := range []string{rg, ag} {
		a.AddResource(id)
	}
	rule := rg + "/providers/Microsoft.AlertsManagement/actionRules/route"
	put := func(what, props string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, rule, `{"location":"Global","properties":`+props+`}`)
		expect(t, what, code, resp, want)
	}
	add := `{"actionType":"AddActionGroups","actionGroupIds":["` + ag + `"]}`
	code, resp := do(t, a, http.MethodPut, rule, `{"location":"westus","properties":{"scopes":["`+rg+`"],"actions":[`+add+`]}}`)
	expect(t, "a rule in a region", code, resp, http.StatusBadRequest)
	put("a rule with no scope", `{"scopes":[],"actions":[`+add+`]}`, http.StatusBadRequest)
	put("a missing scope", `{"scopes":["`+rg+`x"],"actions":[`+add+`]}`, http.StatusNotFound)
	put("two actions", `{"scopes":["`+rg+`"],"actions":[`+add+`,{"actionType":"RemoveAllActionGroups"}]}`, http.StatusBadRequest)
	put("a missing action group", `{"scopes":["`+rg+`"],"actions":[{"actionType":"AddActionGroups","actionGroupIds":["`+ag+`x"]}]}`, http.StatusNotFound)
	put("a schedule ending before it starts", `{"scopes":["`+rg+`"],"actions":[`+add+`],"schedule":{"effectiveFrom":"2026-12-02T00:00:00","effectiveUntil":"2026-12-01T00:00:00"}}`, http.StatusBadRequest)
	put("a weekly recurrence with no days", `{"scopes":["`+rg+`"],"actions":[`+add+`],"schedule":{"recurrences":[{"recurrenceType":"Weekly"}]}}`, http.StatusBadRequest)
	put("day 32 of a month", `{"scopes":["`+rg+`"],"actions":[`+add+`],"schedule":{"recurrences":[{"recurrenceType":"Monthly","daysOfMonth":[32]}]}}`, http.StatusBadRequest)
	put("a rule", `{"scopes":["`+rg+`"],"actions":[`+add+`],"conditions":[{"field":"Severity","operator":"Equals","values":["Sev0"]}]}`, http.StatusCreated)
	put("a suppression on weekends", `{"scopes":["`+rg+`"],"actions":[{"actionType":"RemoveAllActionGroups"}],"schedule":{"timeZone":"UTC","recurrences":[{"recurrenceType":"Weekly","daysOfWeek":["Saturday","Sunday"]}]}}`, http.StatusOK)
}
