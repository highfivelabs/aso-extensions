// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Azure Policy, as far as the fake models it
// (https://learn.microsoft.com/azure/governance/policy/concepts/scope):
//
//   - a policy definition or set definition is kept at a management group or
//     a subscription, nowhere else;
//   - a set definition's policyDefinitions[].policyDefinitionId, and an
//     assignment's policyDefinitionId, must name a definition (or set) that
//     exists and whose location is at or above where it is used: a
//     definition at a subscription serves that subscription and what is in
//     it; one at a management group serves the group, the groups under it,
//     and the subscriptions placed in any of them (and what is in those);
//   - an exemption's policyAssignmentId must name an assignment that exists,
//     and the exemption must be at or under the assignment's scope unless
//     its assignmentScopeValidation is DoNotValidate.
//
// Built-in definitions (/providers/Microsoft.Authorization/policyDefinitions/x)
// exist only when a test adds them. A definition or assignment that is not
// there, or not visible from where it is used, is a 404 (so ASO retries it
// soon, as for any missing reference); a wrong scope is a 400. Deletes are not checked (Azure refuses,
// for one, deleting a definition a set definition still names). The error
// codes are this fake's.
var (
	policyDefinitionType = regexp.MustCompile(`(?i)/providers/microsoft\.authorization/(policydefinitions|policysetdefinitions)/[^/]+$`)
	policyAssignmentType = regexp.MustCompile(`(?i)/providers/microsoft\.authorization/policyassignments/[^/]+$`)
	policyExemptionType  = regexp.MustCompile(`(?i)/providers/microsoft\.authorization/policyexemptions/[^/]+$`)
	subscriptionPrefix   = regexp.MustCompile(`(?i)^/subscriptions/([^/]+)`)
	groupPrefix          = regexp.MustCompile(`(?i)^/providers/microsoft\.management/managementgroups/[^/]+`)
)

// checkPolicy refuses a policy resource the rules above do not allow; code
// is "" when it is allowed.
func (a *ARM) checkPolicy(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	scope := parentOf(id)
	switch {
	case policyDefinitionType.MatchString(id):
		if !subscriptionScope.MatchString(scope) && !managementGroup.MatchString(scope) {
			return http.StatusBadRequest, "InvalidPolicyDefinitionScope", "A policy definition or set definition is kept at a management group or a subscription, not at " + scope + "."
		}
		refs, _ := props["policyDefinitions"].([]any)
		for _, r := range refs {
			ref, _ := r.(map[string]any)
			def, _ := ref["policyDefinitionId"].(string)
			if status, code, msg := a.checkDefinition(def, scope); code != "" {
				return status, code, msg
			}
		}
	case policyAssignmentType.MatchString(id):
		def, _ := props["policyDefinitionId"].(string)
		return a.checkDefinition(def, scope)
	case policyExemptionType.MatchString(id):
		assignment, _ := props["policyAssignmentId"].(string)
		if _, ok := a.resources[strings.ToLower(assignment)]; !ok || !policyAssignmentType.MatchString(assignment) {
			return http.StatusNotFound, "PolicyAssignmentNotFound", "The policy assignment '" + assignment + "' was not found."
		}
		if props["assignmentScopeValidation"] != "DoNotValidate" && !a.atOrUnder(scope, parentOf(assignment)) {
			return http.StatusBadRequest, "InvalidExemptionScope", "The exemption at " + scope + " is not at or under the scope of the assignment " + assignment + "."
		}
	default:
		return a.checkPolicyInsights(id, body)
	}
	return 0, "", ""
}

// checkDefinition refuses a definition that does not exist or that cannot
// be used at scope.
func (a *ARM) checkDefinition(def, scope string) (status int, code, message string) {
	if _, ok := a.resources[strings.ToLower(def)]; !ok || !policyDefinitionType.MatchString(def) {
		return http.StatusNotFound, "PolicyDefinitionNotFound", "The policy definition '" + def + "' was not found."
	}
	location := parentOf(def)
	if location == "/" {
		// A built-in, at the tenant root, serves every scope.
		return 0, "", ""
	}
	if !a.atOrUnder(scope, location) {
		return http.StatusNotFound, "PolicyDefinitionNotFound", "The policy definition '" + def + "' cannot be used at " + scope + ": it is kept at " + location + ", which is not at or above it."
	}
	return 0, "", ""
}

// atOrUnder is whether scope is ancestor or under it, following the
// management group hierarchy: a subscription placed in a group is under the
// group and every group above it.
func (a *ARM) atOrUnder(scope, ancestor string) bool {
	s, anc := strings.ToLower(scope), strings.ToLower(ancestor)
	if within(s, anc) {
		return true
	}
	if !managementGroup.MatchString(anc) {
		return false
	}
	// The group scope is in: its own (a group, or something under one), or
	// the group a subscription is placed in.
	group := groupPrefix.FindString(s)
	if m := subscriptionPrefix.FindStringSubmatch(s); m != nil {
		group = a.groupOf(m[1])
	}
	seen := map[string]bool{}
	for group != "" && !seen[group] {
		if group == anc {
			return true
		}
		seen[group] = true
		r, ok := a.resources[group]
		if !ok {
			return false
		}
		group = strings.ToLower(groupParent(*r))
	}
	return false
}

// groupOf is the lower-case ID of the group a subscription is placed in, or "".
func (a *ARM) groupOf(subscription string) string {
	for k := range a.resources {
		if m := mgSubscription.FindStringSubmatch(k); m != nil && strings.EqualFold(m[2], subscription) {
			return m[1]
		}
	}
	return ""
}
