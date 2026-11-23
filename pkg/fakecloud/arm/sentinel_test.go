// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// Sentinel settings by kind; workspace manager members, groups and
// assignments naming what exists.
func TestSentinelSettingsAndWorkspaceManager(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/soc"
	a.AddResource(rg)
	central := rg + "/providers/Microsoft.OperationalInsights/workspaces/central"
	member := rg + "/providers/Microsoft.OperationalInsights/workspaces/member"
	a.AddResource(central)
	a.AddResource(member)
	si := central + "/providers/Microsoft.SecurityInsights/"
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	put("a setting by another name", si+"settings/Mine", `{"kind":"Ueba","properties":{}}`, http.StatusBadRequest)
	put("a setting of another kind", si+"settings/Ueba", `{"kind":"EyesOn","properties":{}}`, http.StatusBadRequest)
	put("UEBA", si+"settings/Ueba", `{"kind":"Ueba","properties":{"dataSources":["AuditLogs"]}}`, http.StatusCreated)

	put("a member of a missing workspace", si+"workspaceManagerMembers/m", `{"properties":{"targetWorkspaceResourceId":"`+rg+`/providers/Microsoft.OperationalInsights/workspaces/gone","targetWorkspaceTenantId":"t"}}`, http.StatusNotFound)
	put("a member", si+"workspaceManagerMembers/m", `{"properties":{"targetWorkspaceResourceId":"`+member+`","targetWorkspaceTenantId":"t"}}`, http.StatusCreated)
	put("a group of a missing member", si+"workspaceManagerGroups/g", `{"properties":{"displayName":"g","memberResourceNames":["x"]}}`, http.StatusNotFound)
	put("a group", si+"workspaceManagerGroups/g", `{"properties":{"displayName":"g","memberResourceNames":["m"]}}`, http.StatusCreated)
	rule := si + "alertRules/r"
	put("an assignment to a missing group", si+"workspaceManagerAssignments/a", `{"properties":{"targetResourceName":"x","items":[]}}`, http.StatusNotFound)
	put("an assignment of a missing rule", si+"workspaceManagerAssignments/a", `{"properties":{"targetResourceName":"g","items":[{"resourceId":"`+rule+`"}]}}`, http.StatusNotFound)
	a.AddResource(rule)
	put("an assignment", si+"workspaceManagerAssignments/a", `{"properties":{"targetResourceName":"g","items":[{"resourceId":"`+rule+`"}]}}`, http.StatusCreated)
}
