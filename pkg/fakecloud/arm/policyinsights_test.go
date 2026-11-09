// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"testing"
)

const insights = "/providers/Microsoft.PolicyInsights/"

func remediation(assignment, extra string) string {
	return `{"properties":{"policyAssignmentId":"` + assignment + `"` + extra + `}}`
}

// Where remediations and attestations may be, and what they must name.
func TestPolicyInsightsScopes(t *testing.T) {
	a := New(func(string) bool { return true })
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	platform := mgPrefix + "platform"
	put("platform group", platform, group(""), http.StatusCreated)
	put("sub1 placed in platform", platform+"/subscriptions/"+sub1, `{}`, http.StatusCreated)
	sub := "/subscriptions/" + sub1
	rg := sub + "/resourceGroups/app"
	put("resource group", rg, `{"location":"westus"}`, http.StatusCreated)
	account := rg + "/providers/Microsoft.Storage/storageAccounts/s"
	a.AddResource(account)

	def := platform + authz + "policyDefinitions/tags"
	put("definition at the group", def, `{"properties":{"policyRule":{}}}`, http.StatusCreated)
	set := platform + authz + "policySetDefinitions/baseline"
	put("set at the group", set, `{"properties":{"policyDefinitions":[{"policyDefinitionId":"`+def+`","policyDefinitionReferenceId":"tags"}]}}`, http.StatusCreated)
	atGroup := platform + authz + "policyAssignments/tags"
	put("assignment at the group", atGroup, assignment(def), http.StatusCreated)
	atRG := rg + authz + "policyAssignments/tags"
	put("assignment at the resource group", atRG, assignment(def), http.StatusCreated)
	ofSet := sub + authz + "policyAssignments/baseline"
	put("assignment of the set at the subscription", ofSet, assignment(set), http.StatusCreated)

	// Remediations: at any scope at or under the assignment's, through the
	// hierarchy (sub1 is placed in platform).
	put("remediation at the assignment's group", platform+insights+"remediations/r1", remediation(atGroup, ""), http.StatusCreated)
	put("remediation at a subscription in the group", sub+insights+"remediations/r2", remediation(atGroup, ""), http.StatusCreated)
	put("remediation at a resource under the assignment", account+insights+"remediations/r3", remediation(atRG, ""), http.StatusCreated)
	put("remediation above the assignment", sub+insights+"remediations/r4", remediation(atRG, ""), http.StatusBadRequest)
	put("remediation of a missing assignment", rg+insights+"remediations/r5", remediation(atRG+"-nope", ""), http.StatusNotFound)
	put("remediation of a set without a reference ID", sub+insights+"remediations/r6", remediation(ofSet, ""), http.StatusBadRequest)
	put("remediation of a set with a reference ID", sub+insights+"remediations/r6", remediation(ofSet, `,"policyDefinitionReferenceId":"tags"`), http.StatusCreated)
	filter := `,"filters":{"resourceIds":["` + account + `"]}`
	put("resource IDs with ReEvaluateCompliance", rg+insights+"remediations/r7",
		remediation(atRG, `,"resourceDiscoveryMode":"ReEvaluateCompliance"`+filter), http.StatusBadRequest)
	put("resource IDs with ExistingNonCompliant", rg+insights+"remediations/r7",
		remediation(atRG, `,"resourceDiscoveryMode":"ExistingNonCompliant"`+filter), http.StatusCreated)

	// Attestations: not at a management group; at or under the assignment.
	attest := func(assignment string) string {
		return `{"properties":{"policyAssignmentId":"` + assignment + `","complianceState":"Compliant"}}`
	}
	put("attestation at a resource under the assignment", account+insights+"attestations/a1", attest(atRG), http.StatusCreated)
	put("attestation at a subscription in the assignment's group", sub+insights+"attestations/a2", attest(atGroup), http.StatusCreated)
	put("attestation at a management group", platform+insights+"attestations/a3", attest(atGroup), http.StatusBadRequest)
	put("attestation above the assignment", sub+insights+"attestations/a4", attest(atRG), http.StatusBadRequest)
	put("attestation of a missing assignment", rg+insights+"attestations/a5", attest(atRG+"-nope"), http.StatusNotFound)
}
