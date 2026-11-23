// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// Governance rule keys, priorities and condition sets; summary rules' bins.
func TestGovernanceRulesAndSummaryRules(t *testing.T) {
	a := New(func(string) bool { return true })
	sub := "/subscriptions/" + sub1
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	rules := sub + "/providers/Microsoft.Security/governanceRules/"
	const one, two = "0b9a1c3e-8a52-4f0a-9d6e-1b2c3d4e5f60", "0b9a1c3e-8a52-4f0a-9d6e-1b2c3d4e5f61"
	rule := func(priority string) string {
		return `{"properties":{"displayName":"r","rulePriority":` + priority + `,"ruleType":"Integrated","sourceResourceType":"Assessments","conditionSets":[{"conditions":[]}],"ownerSource":{"type":"Manually","value":"owner@example.com"}}}`
	}
	put("a word for a key", rules+"critical", rule("100"), http.StatusBadRequest)
	put("priority out of range", rules+one, rule("1001"), http.StatusBadRequest)
	put("no condition sets", rules+one, `{"properties":{"rulePriority":1,"conditionSets":[]}}`, http.StatusBadRequest)
	put("a rule", rules+one, rule("100"), http.StatusCreated)
	put("the same rule again", rules+one, rule("100"), http.StatusOK)
	put("another at the same priority", rules+two, rule("100"), http.StatusBadRequest)
	put("another at another priority", rules+two, rule("200"), http.StatusCreated)

	rg := sub + "/resourceGroups/logs"
	a.AddResource(rg)
	workspace := rg + "/providers/Microsoft.OperationalInsights/workspaces/law"
	a.AddResource(workspace)
	summary := workspace + "/summaryLogs/"
	put("no query", summary+"s", `{"properties":{"ruleDefinition":{"binSize":60}}}`, http.StatusBadRequest)
	put("a bin size Azure lacks", summary+"s", `{"properties":{"ruleDefinition":{"query":"AppRequests","binSize":45}}}`, http.StatusBadRequest)
	put("a summary rule", summary+"s", `{"properties":{"ruleDefinition":{"query":"AppRequests | summarize count() by bin(TimeGenerated, 1h)","binSize":60,"destinationTable":"Hourly_CL"}}}`, http.StatusCreated)
}
