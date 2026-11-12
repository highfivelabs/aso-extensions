// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// Role definitions and Privileged Identity Management (Microsoft.Authorization,
// API 2020-10-01), as far as the fake models them:
//
//   - role definitions: a handful of built-in roles (BuiltInRoles) exist at
//     every scope, by their real IDs, as
//     /providers/Microsoft.Authorization/roleDefinitions/{id} and under any
//     subscription; listing role definitions at any scope (GET
//     {scope}/providers/Microsoft.Authorization/roleDefinitions, with an
//     optional $filter=roleName eq '...') returns them and every custom role
//     definition a test stored, each with its roleName and type (BuiltInRole
//     or CustomRole, properties.type as Azure names it), which
//     is how ASO resolves a built-in role's well-known name;
//   - a role eligibility or assignment schedule request's name must be a
//     GUID (400), its principalId is required (400), its requestType is one
//     of Azure's (400), and its roleDefinitionId must name a role definition
//     that exists (404, as for a reference not there yet);
//   - a request is never deleted: Azure has no DELETE for it, and the fake
//     refuses one (405); a request is undone by another request;
//   - an eligibility request of type AdminAssign, AdminUpdate, AdminExtend or
//     AdminRenew makes or replaces the eligibility schedule of its principal,
//     role and scope (roleEligibilitySchedules/{request name}) and names it
//     in targetRoleEligibilityScheduleId; AdminRemove needs that schedule
//     (404) and removes it;
//   - an assignment request does the same with roleAssignmentSchedules;
//     SelfActivate also needs an eligibility schedule of the principal and
//     role at or above its scope (404), and SelfDeactivate or AdminRemove
//     needs the assignment schedule (404);
//   - a request's status is Provisioned (Revoked for a removal); a PUT of
//     a request that exists (ASO PUTs again on every reconcile) keeps its
//     status and schedule and applies nothing again;
//   - a role management policy assignment's name is {policy GUID}_{role
//     definition GUID} of its policyId and roleDefinitionId (400), its
//     policy must exist (404) and its role definition too (404); it can be
//     deleted.
//
// Not modelled: approval, justification, ticket and MFA rules (no policy
// rule is evaluated: every request is Provisioned at once, where Azure may
// leave one PendingApproval), schedule start times and expiry, the
// principal (any ID is accepted), the request's cancel and validate
// actions, role management policies themselves (Azure makes one per scope
// and role; a test stores the ones it names), and the error codes (this
// fake's own).
var (
	roleDefinitionsList    = regexp.MustCompile(`(?i)^(.*)/providers/microsoft\.authorization/roledefinitions$`)
	roleDefinitionID       = regexp.MustCompile(`(?i)^(/subscriptions/[^/]+)?/providers/microsoft\.authorization/roledefinitions/([^/]+)$`)
	eligibilityRequestType = regexp.MustCompile(`(?i)/providers/microsoft\.authorization/roleeligibilityschedulerequests/[^/]+$`)
	assignmentRequestType  = regexp.MustCompile(`(?i)/providers/microsoft\.authorization/roleassignmentschedulerequests/[^/]+$`)
	policyAssignmentPIM    = regexp.MustCompile(`(?i)/providers/microsoft\.authorization/rolemanagementpolicyassignments/[^/]+$`)
	guid                   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	roleNameFilter         = regexp.MustCompile(`(?i)^roleName eq '([^']*)'$`)
)

// BuiltInRoles are the built-in role definitions the fake has, by name: the
// real IDs Azure gives them.
var BuiltInRoles = map[string]string{
	"Owner":                     "8e3af657-a8ff-443c-a75c-2fe8c4bcb635",
	"Contributor":               "b24988ac-6180-42a0-ab88-20f7382dd24c",
	"Reader":                    "acdd72a7-3385-48ef-bd42-f606fba81ae7",
	"User Access Administrator": "18d7d88d-d35e-4fb5-a5c3-7773c20a72d9",
}

var requestTypes = map[string]bool{
	"AdminAssign": true, "AdminUpdate": true, "AdminExtend": true, "AdminRenew": true, "AdminRemove": true,
	"SelfActivate": true, "SelfDeactivate": true, "SelfExtend": true, "SelfRenew": true,
}

// builtInRole is the definition of a built-in role, as Resource Manager
// returns it, under scope ("" for the tenant root).
func builtInRole(scope, name, id string) map[string]any {
	return map[string]any{
		"id":   scope + "/providers/Microsoft.Authorization/roleDefinitions/" + id,
		"name": id,
		"type": "Microsoft.Authorization/roleDefinitions",
		"properties": map[string]any{
			"roleName":         name,
			"type":             "BuiltInRole",
			"description":      "A built-in role of the fake resource manager.",
			"assignableScopes": []any{"/"},
			"permissions":      []any{},
		},
	}
}

// builtInRoleByID is the built-in role whose GUID is id.
func builtInRoleByID(id string) (name string, ok bool) {
	for n, i := range BuiltInRoles {
		if strings.EqualFold(i, id) {
			return n, true
		}
	}
	return "", false
}

// serveRoleDefinitions answers a GET of a role definition list, or of a
// built-in role definition; handled is false for anything else.
func (a *ARM) serveRoleDefinitions(w http.ResponseWriter, r *http.Request, path string) (handled bool) {
	if r.Method != http.MethodGet {
		return false
	}
	if m := roleDefinitionID.FindStringSubmatch(path); m != nil {
		if name, ok := builtInRoleByID(m[2]); ok {
			writeJSON(w, http.StatusOK, builtInRole(m[1], name, BuiltInRoles[name]))
			return true
		}
		return false
	}
	m := roleDefinitionsList.FindStringSubmatch(path)
	if m == nil {
		return false
	}
	scope := strings.TrimRight(m[1], "/")
	want := ""
	if f := r.URL.Query().Get("$filter"); f != "" {
		fm := roleNameFilter.FindStringSubmatch(f)
		if fm == nil {
			writeError(w, http.StatusBadRequest, "UnsupportedFilter", "fake resource manager: only $filter=roleName eq '...' is supported")
			return true
		}
		want = fm[1]
	}
	var out []any
	for name, id := range BuiltInRoles {
		if want == "" || strings.EqualFold(want, name) {
			out = append(out, builtInRole(subscriptionOf(scope), name, id))
		}
	}
	for key, res := range a.resources {
		if !strings.EqualFold(typeOf(key), "microsoft.authorization/roledefinitions") {
			continue
		}
		props, _ := (*res)["properties"].(map[string]any)
		if props == nil {
			props = map[string]any{}
			(*res)["properties"] = props
		}
		if _, ok := props["type"]; !ok {
			props["type"] = "CustomRole"
		}
		name, _ := props["roleName"].(string)
		if want == "" || strings.EqualFold(want, name) {
			out = append(out, map[string]any(*res))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].(map[string]any)["id"].(string) < out[j].(map[string]any)["id"].(string)
	})
	if out == nil {
		out = []any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"value": out})
	return true
}

// subscriptionOf is the /subscriptions/{s} a scope is in, or "".
func subscriptionOf(scope string) string {
	parts := strings.Split(strings.Trim(scope, "/"), "/")
	if len(parts) >= 2 && strings.EqualFold(parts[0], "subscriptions") {
		return "/subscriptions/" + parts[1]
	}
	return ""
}

// roleDefinitionExists is whether id names a built-in role definition (at
// the tenant root or a subscription) or one a test stored.
func (a *ARM) roleDefinitionExists(id string) bool {
	if m := roleDefinitionID.FindStringSubmatch(id); m != nil {
		if _, ok := builtInRoleByID(m[2]); ok {
			return true
		}
	}
	_, ok := a.resources[strings.ToLower(id)]
	return ok
}

// isScheduleRequest is whether id is a PIM schedule request, which Azure
// never deletes.
func isScheduleRequest(id string) bool {
	return eligibilityRequestType.MatchString(id) || assignmentRequestType.MatchString(id)
}

// checkPIM refuses a schedule request or a role management policy
// assignment the rules above do not allow, and otherwise applies a
// request to the schedules it makes or removes (after is called once the
// request is stored); code is "" when it is allowed or id is neither.
func (a *ARM) checkPIM(id string, body map[string]any) (status int, code, message string, after func()) {
	if !policyAssignmentPIM.MatchString(id) && !isScheduleRequest(id) {
		return 0, "", "", nil // not PIM's: the body is left as sent
	}
	props, _ := body["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		body["properties"] = props
	}
	scope := parentOf(id)
	name := lastSegment(id)
	if policyAssignmentPIM.MatchString(id) {
		policy, _ := props["policyId"].(string)
		role, _ := props["roleDefinitionId"].(string)
		if !strings.EqualFold(name, lastSegment(policy)+"_"+lastSegment(role)) {
			return http.StatusBadRequest, "InvalidRoleManagementPolicyAssignmentName", "The name of a role management policy assignment is {policy GUID}_{role definition GUID}: " + lastSegment(policy) + "_" + lastSegment(role) + ", not " + name + ".", nil
		}
		if _, ok := a.resources[strings.ToLower(policy)]; !ok {
			return http.StatusNotFound, "RoleManagementPolicyNotFound", "The role management policy '" + policy + "' was not found.", nil
		}
		if !a.roleDefinitionExists(role) {
			return http.StatusNotFound, "RoleDefinitionDoesNotExist", "The role definition '" + role + "' does not exist.", nil
		}
		props["scope"] = scope
		return 0, "", "", nil
	}
	eligibility := eligibilityRequestType.MatchString(id)
	if !eligibility && !assignmentRequestType.MatchString(id) {
		return 0, "", "", nil
	}
	if !guid.MatchString(name) {
		return http.StatusBadRequest, "InvalidRoleScheduleRequestName", "The name of a schedule request must be a GUID, not '" + name + "'.", nil
	}
	principal, _ := props["principalId"].(string)
	if principal == "" {
		return http.StatusBadRequest, "MissingPrincipalId", "The request's principalId is required.", nil
	}
	requestType, _ := props["requestType"].(string)
	if !requestTypes[requestType] {
		return http.StatusBadRequest, "InvalidRequestType", "The request type '" + requestType + "' is not one of Azure's.", nil
	}
	role, _ := props["roleDefinitionId"].(string)
	if !a.roleDefinitionExists(role) {
		return http.StatusNotFound, "RoleDefinitionDoesNotExist", "The role definition '" + role + "' does not exist.", nil
	}
	kind, target := "roleAssignmentSchedules", "targetRoleAssignmentScheduleId"
	if eligibility {
		kind, target = "roleEligibilitySchedules", "targetRoleEligibilityScheduleId"
	}
	// ASO PUTs a resource again on every reconcile: a request that exists
	// keeps the outcome it had (its status and schedule), and nothing is
	// applied twice.
	if stored, ok := a.resources[strings.ToLower(id)]; ok {
		before, _ := (*stored)["properties"].(map[string]any)
		props["status"], props[target] = before["status"], before[target]
		return 0, "", "", nil
	}
	existing := a.schedule(kind, scope, principal, role, false)
	switch requestType {
	case "AdminRemove", "SelfDeactivate":
		if existing == "" {
			return http.StatusNotFound, "RoleScheduleNotFound", "No " + kind + " of principal " + principal + " for role " + role + " at " + scope + " to remove.", nil
		}
		props["status"] = "Revoked"
		props[target] = existing
		return 0, "", "", func() { delete(a.resources, strings.ToLower(existing)) }
	case "SelfActivate":
		if a.schedule("roleEligibilitySchedules", scope, principal, role, true) == "" {
			return http.StatusNotFound, "RoleEligibilityScheduleNotFound", "Principal " + principal + " is not eligible for role " + role + " at " + scope + " or above.", nil
		}
	}
	scheduleID := scope + "/providers/Microsoft.Authorization/" + kind + "/" + name
	props["status"] = "Provisioned"
	props[target] = scheduleID
	return 0, "", "", func() {
		if existing != "" {
			delete(a.resources, strings.ToLower(existing))
		}
		res := resource{
			"id":   scheduleID,
			"name": name,
			"type": "Microsoft.Authorization/" + kind,
			"properties": map[string]any{
				"scope":            scope,
				"principalId":      principal,
				"roleDefinitionId": role,
				"status":           "Provisioned",
			},
		}
		a.resources[strings.ToLower(scheduleID)] = &res
	}
}

// schedule is the ID of the schedule of kind (roleEligibilitySchedules or
// roleAssignmentSchedules) of principal and role at scope (or, with above,
// at scope or any scope above it), or "".
func (a *ARM) schedule(kind, scope, principal, role string, above bool) string {
	for key, res := range a.resources {
		if !strings.EqualFold(typeOf(key), "microsoft.authorization/"+kind) {
			continue
		}
		props, _ := (*res)["properties"].(map[string]any)
		s, _ := props["scope"].(string)
		p, _ := props["principalId"].(string)
		r, _ := props["roleDefinitionId"].(string)
		if !strings.EqualFold(p, principal) || !strings.EqualFold(lastSegment(r), lastSegment(role)) {
			continue
		}
		if strings.EqualFold(s, scope) || (above && within(strings.ToLower(scope), strings.ToLower(s))) {
			id, _ := (*res)["id"].(string)
			return id
		}
	}
	return ""
}
