// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Access reviews and the subscription policy: flat PUT bodies read back
// under properties, GUID names, singletons that cannot be deleted.
func TestAccessReviewsAndSubscriptionPolicy(t *testing.T) {
	a := New(func(string) bool { return true })
	sub := "/subscriptions/" + sub1
	const id = "6c1e2b8a-2b9f-4a43-9d36-5c3a0d1f6e10"
	call := func(what, method, path, body string, want int) string {
		t.Helper()
		code, resp := do(t, a, method, path, body)
		expect(t, what, code, resp, want)
		return resp
	}
	properties := func(resp string) map[string]any {
		t.Helper()
		var got struct {
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal([]byte(resp), &got); err != nil {
			t.Fatal(err)
		}
		return got.Properties
	}

	// Schedule and history definitions: GUID names, kept under properties.
	schedule := sub + "/providers/Microsoft.Authorization/accessReviewScheduleDefinitions/"
	call("a schedule definition named by a word", http.MethodPut, schedule+"quarterly", `{"displayName":"q"}`, http.StatusBadRequest)
	resp := call("a schedule definition", http.MethodPut, schedule+id, `{"displayName":"quarterly","descriptionForReviewers":"r"}`, http.StatusCreated)
	if p := properties(resp); p["displayName"] != "quarterly" || p["descriptionForReviewers"] != "r" {
		t.Errorf("a schedule definition's properties = %v; want what was sent", p)
	}
	if p := properties(call("read back", http.MethodGet, schedule+id, ``, http.StatusOK)); p["displayName"] != "quarterly" {
		t.Errorf("read back: properties = %v", p)
	}
	history := sub + "/resourceGroups/app/providers/Microsoft.Authorization/accessReviewHistoryDefinitions/"
	a.AddResource(sub + "/resourceGroups/app")
	call("a history definition named by a word", http.MethodPut, history+"report", `{"displayName":"h"}`, http.StatusBadRequest)
	call("a history definition at a resource group", http.MethodPut, history+id, `{"displayName":"h"}`, http.StatusCreated)
	call("a definition is deleted", http.MethodDelete, schedule+id, ``, http.StatusOK)

	// Default settings: named default, never deleted.
	settings := sub + "/providers/Microsoft.Authorization/accessReviewScheduleSettings/"
	call("settings by another name", http.MethodPut, settings+"mine", `{"mailNotificationsEnabled":true}`, http.StatusBadRequest)
	resp = call("settings", http.MethodPut, settings+"default", `{"mailNotificationsEnabled":true}`, http.StatusCreated)
	if p := properties(resp); p["mailNotificationsEnabled"] != true {
		t.Errorf("settings' properties = %v", p)
	}
	call("settings cannot be deleted", http.MethodDelete, settings+"default", ``, http.StatusMethodNotAllowed)

	// The subscription policy: the tenant's only, exempted principals by object ID.
	policy := "/providers/Microsoft.Subscription/policies/default"
	call("at a subscription", http.MethodPut, sub+policy, `{"blockSubscriptionsLeavingTenant":true}`, http.StatusBadRequest)
	call("by another name", http.MethodPut, "/providers/Microsoft.Subscription/policies/mine", `{}`, http.StatusBadRequest)
	call("a principal by name", http.MethodPut, policy, `{"exemptedPrincipals":["alice"]}`, http.StatusBadRequest)
	resp = call("the tenant's policy", http.MethodPut, policy, `{"blockSubscriptionsLeavingTenant":true,"exemptedPrincipals":["`+id+`"]}`, http.StatusCreated)
	if p := properties(resp); p["blockSubscriptionsLeavingTenant"] != true {
		t.Errorf("the policy's properties = %v", p)
	}
	call("changed", http.MethodPut, policy, `{"blockSubscriptionsIntoTenant":true}`, http.StatusOK)
	call("the policy cannot be deleted", http.MethodDelete, policy, ``, http.StatusMethodNotAllowed)
}
