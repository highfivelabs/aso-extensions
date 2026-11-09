// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	mgPrefix = "/providers/Microsoft.Management/managementGroups/"
	sub1     = "00000000-0000-0000-0000-0000000000f1"
)

func do(t *testing.T, a *ARM, method, path, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path+"?api-version=2023-04-01", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer ok")
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func group(parent string) string {
	if parent == "" {
		return `{"properties":{"displayName":"x"}}`
	}
	return `{"properties":{"displayName":"x","details":{"parent":{"id":"` + parent + `"}}}}`
}

func expect(t *testing.T, what string, got int, body string, want int) {
	t.Helper()
	if got != want {
		t.Errorf("%s: status %d, want %d: %s", what, got, want, body)
	}
}

// A resource whose ID starts at /providers has the tenant root as its parent,
// which always exists (this was ParentResourceNotFound).
func TestTenantScopeResources(t *testing.T) {
	a := New(func(string) bool { return true })
	code, body := do(t, a, http.MethodPut, mgPrefix+"platform", group(""))
	expect(t, "management group at the tenant root", code, body, http.StatusCreated)
	code, body = do(t, a, http.MethodPut, "/providers/Microsoft.Subscription/aliases/dev", `{"properties":{}}`)
	expect(t, "subscription alias", code, body, http.StatusCreated)
	code, body = do(t, a, http.MethodGet, mgPrefix+"platform", "")
	expect(t, "get", code, body, http.StatusOK)
	if !strings.Contains(body, `"type":"Microsoft.Management/managementGroups"`) {
		t.Errorf("type missing: %s", body)
	}
	// An extension resource on a group needs the group.
	code, body = do(t, a, http.MethodPut, mgPrefix+"nope/providers/Microsoft.Authorization/policyDefinitions/p", `{}`)
	expect(t, "policy definition on a missing group", code, body, http.StatusNotFound)
	code, body = do(t, a, http.MethodPut, mgPrefix+"platform/providers/Microsoft.Authorization/policyDefinitions/p", `{}`)
	expect(t, "policy definition on a group", code, body, http.StatusCreated)
	// Deleting the group deletes what is under it.
	code, body = do(t, a, http.MethodDelete, mgPrefix+"platform", "")
	expect(t, "delete", code, body, http.StatusOK)
	if a.HasResource(mgPrefix + "platform/providers/Microsoft.Authorization/policyDefinitions/p") {
		t.Error("the policy definition outlived its group")
	}
}

func TestManagementGroupHierarchy(t *testing.T) {
	a := New(func(string) bool { return true })
	root, platform, landing := mgPrefix+"root", mgPrefix+"platform", mgPrefix+"landing"
	a.AddResource(root)

	code, body := do(t, a, http.MethodPut, platform, group(root))
	expect(t, "platform under root", code, body, http.StatusCreated)
	code, body = do(t, a, http.MethodPut, landing, group(mgPrefix+"missing"))
	expect(t, "a missing parent", code, body, http.StatusBadRequest)
	code, body = do(t, a, http.MethodPut, landing, group("/subscriptions/"+sub1))
	expect(t, "a parent that is no group", code, body, http.StatusBadRequest)
	code, body = do(t, a, http.MethodPut, landing, group(platform))
	expect(t, "landing under platform", code, body, http.StatusCreated)

	// A cycle: platform under its own child, or under itself.
	code, body = do(t, a, http.MethodPut, platform, group(landing))
	expect(t, "a cycle", code, body, http.StatusBadRequest)
	if !strings.Contains(body, "CycleInHierarchy") {
		t.Errorf("want CycleInHierarchy: %s", body)
	}
	code, body = do(t, a, http.MethodPut, platform, group(platform))
	expect(t, "a group under itself", code, body, http.StatusBadRequest)

	// Moving a group is a PUT with another parent.
	code, body = do(t, a, http.MethodPut, landing, group(root))
	expect(t, "move landing to root", code, body, http.StatusOK)

	// A group with a child group cannot be deleted.
	code, body = do(t, a, http.MethodPut, mgPrefix+"sandbox", group(platform))
	expect(t, "sandbox under platform", code, body, http.StatusCreated)
	code, body = do(t, a, http.MethodDelete, platform, "")
	expect(t, "delete a group with a child", code, body, http.StatusConflict)
	code, body = do(t, a, http.MethodDelete, mgPrefix+"sandbox", "")
	expect(t, "delete the child", code, body, http.StatusOK)
	code, body = do(t, a, http.MethodDelete, platform, "")
	expect(t, "delete the empty group", code, body, http.StatusOK)
}

func TestSubscriptionPlacement(t *testing.T) {
	a := New(func(string) bool { return true })
	g1, g2 := mgPrefix+"one", mgPrefix+"two"
	code, body := do(t, a, http.MethodPut, g1+"/subscriptions/"+sub1, `{}`)
	expect(t, "placement in a missing group", code, body, http.StatusNotFound)
	for _, g := range []string{g1, g2} {
		code, body = do(t, a, http.MethodPut, g, group(""))
		expect(t, "create "+g, code, body, http.StatusCreated)
	}
	code, body = do(t, a, http.MethodPut, g1+"/subscriptions/"+sub1, `{}`)
	expect(t, "place in one", code, body, http.StatusCreated)
	code, body = do(t, a, http.MethodDelete, g1, "")
	expect(t, "delete a group that holds a subscription", code, body, http.StatusConflict)

	// Placing it in another group takes it out of the first.
	code, body = do(t, a, http.MethodPut, g2+"/subscriptions/"+sub1, `{}`)
	expect(t, "place in two", code, body, http.StatusCreated)
	if a.HasResource(g1 + "/subscriptions/" + sub1) {
		t.Error("the subscription is still in group one")
	}
	code, body = do(t, a, http.MethodDelete, g1, "")
	expect(t, "delete the emptied group", code, body, http.StatusOK)
	code, body = do(t, a, http.MethodDelete, g2+"/subscriptions/"+sub1, "")
	expect(t, "remove from two", code, body, http.StatusOK)
}
