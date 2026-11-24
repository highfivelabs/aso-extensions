// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// A smart detection rule is one Application Insights defines, and it cannot
// be deleted.
func TestSmartDetection(t *testing.T) {
	a := New(func(string) bool { return true })
	component := "/subscriptions/" + sub1 + "/resourceGroups/ai/providers/Microsoft.Insights/components/app"
	a.AddResource("/subscriptions/" + sub1 + "/resourceGroups/ai")
	a.AddResource(component)
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	rules := component + "/ProactiveDetectionConfigs/"
	put("a rule Azure does not define", rules+"cpuspike", `{"enabled":true}`, http.StatusBadRequest)
	put("a rule", rules+"slowpageloadtime", `{"name":"slowpageloadtime","enabled":false,"customEmails":["ops@example.com"]}`, http.StatusCreated)
	put("an extension rule", rules+"extension_billingdatavolumedailyspikeextension", `{"enabled":true,"sendEmailsToSubscriptionOwners":false}`, http.StatusCreated)
	code, resp := do(t, a, http.MethodDelete, rules+"slowpageloadtime", "")
	expect(t, "deleting a rule", code, resp, http.StatusMethodNotAllowed)
}
