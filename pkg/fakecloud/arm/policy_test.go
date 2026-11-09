// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"testing"
)

const (
	authz = "/providers/Microsoft.Authorization/"
	sub2  = "00000000-0000-0000-0000-0000000000f2"
)

func assignment(def string) string {
	return `{"properties":{"policyDefinitionId":"` + def + `"}}`
}

// Where definitions are kept, what assignments and set definitions may name,
// and where exemptions may be, following the management group hierarchy.
func TestPolicyScopes(t *testing.T) {
	a := New(func(string) bool { return true })
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	root, platform := mgPrefix+"root", mgPrefix+"platform"
	put("root group", root, group(""), http.StatusCreated)
	put("platform group under root", platform, group(root), http.StatusCreated)
	put("sub1 placed in platform", platform+"/subscriptions/"+sub1, `{}`, http.StatusCreated)
	rg := "/subscriptions/" + sub1 + "/resourceGroups/app"
	put("resource group", rg, `{"location":"westus"}`, http.StatusCreated)

	rootDef := root + authz + "policyDefinitions/tags"
	subDef := "/subscriptions/" + sub1 + authz + "policyDefinitions/locations"
	put("definition at a group", rootDef, `{"properties":{"policyRule":{}}}`, http.StatusCreated)
	put("definition at a subscription", subDef, `{"properties":{"policyRule":{}}}`, http.StatusCreated)
	put("definition at a resource group", rg+authz+"policyDefinitions/x", `{"properties":{}}`, http.StatusBadRequest)

	// A set names definitions at or above it.
	put("set at a group naming the group's definition", platform+authz+"policySetDefinitions/baseline",
		`{"properties":{"policyDefinitions":[{"policyDefinitionId":"`+rootDef+`"}]}}`, http.StatusCreated)
	put("set at a group naming a subscription's definition", root+authz+"policySetDefinitions/bad",
		`{"properties":{"policyDefinitions":[{"policyDefinitionId":"`+subDef+`"}]}}`, http.StatusNotFound)
	put("set naming a missing definition", root+authz+"policySetDefinitions/bad",
		`{"properties":{"policyDefinitions":[{"policyDefinitionId":"`+root+authz+`policyDefinitions/nope"}]}}`, http.StatusNotFound)

	// Assignments: at or under the definition's location, through the
	// hierarchy (sub1 is in platform, which is under root).
	put("group definition at a child group", platform+authz+"policyAssignments/a1", assignment(rootDef), http.StatusCreated)
	put("group definition at a placed subscription", "/subscriptions/"+sub1+authz+"policyAssignments/a2", assignment(rootDef), http.StatusCreated)
	put("group definition at a resource group of it", rg+authz+"policyAssignments/a3", assignment(rootDef), http.StatusCreated)
	put("group definition at a subscription outside", "/subscriptions/"+sub2+authz+"policyAssignments/a4", assignment(rootDef), http.StatusNotFound)
	put("subscription definition at its resource group", rg+authz+"policyAssignments/a5", assignment(subDef), http.StatusCreated)
	put("subscription definition at a group", platform+authz+"policyAssignments/a6", assignment(subDef), http.StatusNotFound)
	put("subscription definition at another subscription", "/subscriptions/"+sub2+authz+"policyAssignments/a7", assignment(subDef), http.StatusNotFound)
	put("set at a placed subscription", "/subscriptions/"+sub1+authz+"policyAssignments/a8",
		assignment(platform+authz+"policySetDefinitions/baseline"), http.StatusCreated)
	put("missing definition", rg+authz+"policyAssignments/a9", assignment(subDef+"-nope"), http.StatusNotFound)
	builtin := authz + "policyDefinitions/allowed-locations"
	put("built-in not there", rg+authz+"policyAssignments/a10", assignment(builtin), http.StatusNotFound)
	a.AddResource(builtin)
	put("built-in anywhere", "/subscriptions/"+sub2+authz+"policyAssignments/a10", assignment(builtin), http.StatusCreated)

	// Moving sub1 out of the hierarchy: the group's definition no longer
	// serves it.
	put("sub1 placed in a group outside root", mgPrefix+"other", group(""), http.StatusCreated)
	put("sub1 moved", mgPrefix+"other/subscriptions/"+sub1, `{}`, http.StatusCreated)
	put("group definition at a moved subscription", "/subscriptions/"+sub1+authz+"policyAssignments/a11", assignment(rootDef), http.StatusNotFound)

	// Exemptions: at or under the assignment's scope.
	a1 := platform + authz + "policyAssignments/a1"
	a5 := rg + authz + "policyAssignments/a5"
	exemption := func(assignment string) string {
		return `{"properties":{"policyAssignmentId":"` + assignment + `","exemptionCategory":"Waiver"}}`
	}
	put("exemption at the assignment's scope", rg+authz+"policyExemptions/e1", exemption(a5), http.StatusCreated)
	account := rg + "/providers/Microsoft.Storage/storageAccounts/s"
	a.AddResource(account)
	put("exemption under it", account+authz+"policyExemptions/e2", exemption(a5), http.StatusCreated)
	put("exemption above it", "/subscriptions/"+sub1+authz+"policyExemptions/e3", exemption(a5), http.StatusBadRequest)
	put("exemption above it, not validated", "/subscriptions/"+sub1+authz+"policyExemptions/e3",
		`{"properties":{"policyAssignmentId":"`+a5+`","exemptionCategory":"Waiver","assignmentScopeValidation":"DoNotValidate"}}`, http.StatusCreated)
	put("a group under platform", mgPrefix+"child", group(platform), http.StatusCreated)
	put("exemption at a group under the assignment's group", mgPrefix+"child"+authz+"policyExemptions/e4", exemption(a1), http.StatusCreated)
	put("exemption naming a missing assignment", rg+authz+"policyExemptions/e5", exemption(a5+"-nope"), http.StatusNotFound)
}
