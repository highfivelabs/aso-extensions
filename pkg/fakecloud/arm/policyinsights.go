// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Policy remediations and attestations (Microsoft.PolicyInsights, API
// 2024-10-01), as far as the fake models them:
//
//   - a remediation is kept at a management group, a subscription, a
//     resource group or a resource; an attestation at a subscription, a
//     resource group or a resource (the spec has no management group path
//     for it), else 400;
//   - either one's policyAssignmentId must name an assignment that exists
//     (404 otherwise, as for any missing reference), and it must be at or
//     under the assignment's scope, following the management group
//     hierarchy (400);
//   - a remediation of an assignment of a set definition must say which
//     definition of the set it remediates (policyDefinitionReferenceId), and
//     its filters.resourceIds cannot be used with resourceDiscoveryMode
//     ReEvaluateCompliance (both from the spec's descriptions; 400).
//
// Not modelled: running a remediation (no deployment is made, nothing is
// evaluated: a remediation is Succeeded at once with no deployments, where
// Azure's is Evaluating, then Complete or Failed), cancelling one, whether
// the definition's effect can be remediated (deployIfNotExists or modify) or
// attested (manual), and the error codes (this fake's own).
var (
	remediationType = regexp.MustCompile(`(?i)/providers/microsoft\.policyinsights/remediations/[^/]+$`)
	attestationType = regexp.MustCompile(`(?i)/providers/microsoft\.policyinsights/attestations/[^/]+$`)
	setDefinition   = regexp.MustCompile(`(?i)/providers/microsoft\.authorization/policysetdefinitions/[^/]+$`)
)

// checkPolicyInsights refuses a remediation or attestation the rules above
// do not allow; code is "" when it is allowed or id is neither.
func (a *ARM) checkPolicyInsights(id string, body map[string]any) (status int, code, message string) {
	remediation := remediationType.MatchString(id)
	if !remediation && !attestationType.MatchString(id) {
		return 0, "", ""
	}
	props, _ := body["properties"].(map[string]any)
	scope := parentOf(id)
	if !remediation && (scope == "/" || managementGroup.MatchString(scope)) {
		return http.StatusBadRequest, "InvalidAttestationScope", "An attestation is kept at a subscription, a resource group or a resource, not at " + scope + "."
	}
	assignment, _ := props["policyAssignmentId"].(string)
	stored, ok := a.resources[strings.ToLower(assignment)]
	if !ok || !policyAssignmentType.MatchString(assignment) {
		return http.StatusNotFound, "PolicyAssignmentNotFound", "The policy assignment '" + assignment + "' was not found."
	}
	if !a.atOrUnder(scope, parentOf(assignment)) {
		return http.StatusBadRequest, "InvalidPolicyAssignmentScope", "The scope " + scope + " is not at or under the scope of the assignment " + assignment + "."
	}
	if !remediation {
		return 0, "", ""
	}
	assigned, _ := (*stored)["properties"].(map[string]any)
	def, _ := assigned["policyDefinitionId"].(string)
	if ref, _ := props["policyDefinitionReferenceId"].(string); ref == "" && setDefinition.MatchString(def) {
		return http.StatusBadRequest, "MissingPolicyDefinitionReferenceId", "The assignment " + assignment + " assigns a policy set definition; say which of its definitions to remediate (policyDefinitionReferenceId)."
	}
	filters, _ := props["filters"].(map[string]any)
	if ids, _ := filters["resourceIds"].([]any); len(ids) > 0 && props["resourceDiscoveryMode"] == "ReEvaluateCompliance" {
		return http.StatusBadRequest, "InvalidRemediationFilters", "filters.resourceIds cannot be used when resourceDiscoveryMode is ReEvaluateCompliance."
	}
	return 0, "", ""
}
