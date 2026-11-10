// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const (
	principal  = "11111111-2222-3333-4444-555555555555"
	request1   = "aaaaaaaa-0000-0000-0000-000000000001"
	request2   = "aaaaaaaa-0000-0000-0000-000000000002"
	request3   = "aaaaaaaa-0000-0000-0000-000000000003"
	request4   = "aaaaaaaa-0000-0000-0000-000000000004"
	policyGUID = "bbbbbbbb-0000-0000-0000-000000000001"
)

func pimRequest(requestType, role string) string {
	return `{"properties":{"principalId":"` + principal + `","requestType":"` + requestType + `","roleDefinitionId":"` + role + `"}}`
}

// Built-in roles are listed by name at any scope, as ASO's role-name
// resolution reads them; a custom role a test stored is listed too.
func TestRoleDefinitionsList(t *testing.T) {
	a := New(func(string) bool { return true })
	sub := "/subscriptions/" + sub1
	custom := sub + authz + "roleDefinitions/cccccccc-0000-0000-0000-000000000001"
	a.AddResource(custom)

	list := func(path string) map[string]string {
		t.Helper()
		code, body := do(t, a, http.MethodGet, path, "")
		expect(t, "list "+path, code, body, http.StatusOK)
		var out struct {
			Value []struct {
				Name       string `json:"name"`
				Properties struct {
					RoleName string `json:"roleName"`
					RoleType string `json:"type"`
				} `json:"properties"`
			} `json:"value"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, v := range out.Value {
			if v.Properties.RoleType == "" {
				t.Errorf("%s: %s has no properties.type", path, v.Name)
			}
			got[v.Properties.RoleName+"/"+v.Properties.RoleType] = v.Name
		}
		return got
	}
	for _, path := range []string{"/providers/Microsoft.Authorization/roleDefinitions", "//providers/Microsoft.Authorization/roleDefinitions", sub + authz + "roleDefinitions"} {
		got := list(path)
		if got["Reader/BuiltInRole"] != BuiltInRoles["Reader"] {
			t.Errorf("%s: Reader is %q, want %q", path, got["Reader/BuiltInRole"], BuiltInRoles["Reader"])
		}
		if _, ok := got["/CustomRole"]; !ok {
			t.Errorf("%s: the custom role is not listed: %v", path, got)
		}
	}
	code, body := do(t, a, http.MethodGet, sub+authz+"roleDefinitions/"+BuiltInRoles["Reader"], "")
	expect(t, "get Reader under the subscription", code, body, http.StatusOK)
	if !strings.Contains(body, `"roleName":"Reader"`) {
		t.Errorf("get Reader: %s", body)
	}
}

// Eligibility and activation: names are GUIDs, roles must exist, a removal
// needs what it removes, and a request is never deleted.
func TestScheduleRequests(t *testing.T) {
	a := New(func(string) bool { return true })
	put := func(what, path, body string, want int) string {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
		return resp
	}
	sub := "/subscriptions/" + sub1
	rg := sub + "/resourceGroups/app"
	put("resource group", rg, `{"location":"westus"}`, http.StatusCreated)
	reader := sub + authz + "roleDefinitions/" + BuiltInRoles["Reader"]
	eligible := rg + authz + "roleEligibilityScheduleRequests/"
	active := rg + authz + "roleAssignmentScheduleRequests/"

	put("a name that is no GUID", eligible+"eligible-reader", pimRequest("AdminAssign", reader), http.StatusBadRequest)
	put("no principal", eligible+request1, `{"properties":{"requestType":"AdminAssign","roleDefinitionId":"`+reader+`"}}`, http.StatusBadRequest)
	put("an unknown request type", eligible+request1, pimRequest("Grant", reader), http.StatusBadRequest)
	put("a missing role", eligible+request1, pimRequest("AdminAssign", sub+authz+"roleDefinitions/cccccccc-0000-0000-0000-00000000dead"), http.StatusNotFound)
	put("activation before eligibility", active+request2, pimRequest("SelfActivate", reader), http.StatusNotFound)
	put("removal before eligibility", eligible+request3, pimRequest("AdminRemove", reader), http.StatusNotFound)

	resp := put("eligibility", eligible+request1, pimRequest("AdminAssign", reader), http.StatusCreated)
	schedule := rg + authz + "roleEligibilitySchedules/" + request1
	if !strings.Contains(resp, `"targetRoleEligibilityScheduleId":"`+schedule+`"`) || !strings.Contains(resp, `"status":"Provisioned"`) {
		t.Errorf("eligibility: %s", resp)
	}
	if !a.HasResource(schedule) {
		t.Errorf("no eligibility schedule %s", schedule)
	}
	put("activation", active+request2, pimRequest("SelfActivate", reader), http.StatusCreated)
	if !a.HasResource(rg + authz + "roleAssignmentSchedules/" + request2) {
		t.Errorf("no assignment schedule")
	}
	put("the activation again (ASO PUTs on every reconcile)", active+request2, pimRequest("SelfActivate", reader), http.StatusOK)
	put("deactivation", active+request4, pimRequest("SelfDeactivate", reader), http.StatusCreated)
	resp = put("the deactivation again", active+request4, pimRequest("SelfDeactivate", reader), http.StatusOK)
	if !strings.Contains(resp, `"status":"Revoked"`) {
		t.Errorf("the deactivation again: %s", resp)
	}
	if a.HasResource(rg + authz + "roleAssignmentSchedules/" + request2) {
		t.Errorf("the assignment schedule outlived its deactivation")
	}

	for _, id := range []string{eligible + request1, active + request2} {
		code, body := do(t, a, http.MethodDelete, id, "")
		expect(t, "delete "+id, code, body, http.StatusMethodNotAllowed)
		if !a.HasResource(id) {
			t.Errorf("%s was deleted", id)
		}
	}

	resp = put("removal", eligible+request3, pimRequest("AdminRemove", reader), http.StatusCreated)
	if !strings.Contains(resp, `"status":"Revoked"`) || a.HasResource(schedule) {
		t.Errorf("removal: %s (schedule still there: %v)", resp, a.HasResource(schedule))
	}
}

// A role management policy assignment names its policy and role in its
// name; both must exist; it can be deleted.
func TestRoleManagementPolicyAssignments(t *testing.T) {
	a := New(func(string) bool { return true })
	sub := "/subscriptions/" + sub1
	rg := sub + "/resourceGroups/app"
	a.AddResource(rg)
	reader := sub + authz + "roleDefinitions/" + BuiltInRoles["Reader"]
	policy := rg + authz + "roleManagementPolicies/" + policyGUID
	body := `{"properties":{"policyId":"` + policy + `","roleDefinitionId":"` + reader + `"}}`
	path := rg + authz + "roleManagementPolicyAssignments/"
	right := policyGUID + "_" + BuiltInRoles["Reader"]

	code, resp := do(t, a, http.MethodPut, path+"assignment", body)
	expect(t, "a name that is not policy_role", code, resp, http.StatusBadRequest)
	code, resp = do(t, a, http.MethodPut, path+right, body)
	expect(t, "a missing policy", code, resp, http.StatusNotFound)
	a.AddResource(policy)
	code, resp = do(t, a, http.MethodPut, path+right, body)
	expect(t, "the assignment", code, resp, http.StatusCreated)
	code, resp = do(t, a, http.MethodDelete, path+right, "")
	expect(t, "delete", code, resp, http.StatusOK)
}
