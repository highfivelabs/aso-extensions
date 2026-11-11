// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Operations Management solutions (Microsoft.OperationsManagement/solutions,
// API 2015-11-01-preview) in a resource group, as far as the fake models
// them:
//
//   - a solution names the Log Analytics workspace it is enabled on
//     (properties.workspaceResourceId, 400 when missing, 404 when the
//     workspace is not there);
//   - it gives a plan (400 without one); a Microsoft gallery solution (plan
//     publisher Microsoft) is named, and its plan is named, "<type>(<the
//     workspace's name>)" and its product is "OMSGallery/<type>", the
//     convention Azure enforces (400 otherwise);
//   - contained and referenced resources, when given, exist (404).
//
// Not modelled: which solution types exist, a solution's contained
// resources being locked and deleted with it, third-party publishers'
// rules, and the error codes (this fake's own).
var (
	omsSolution = regexp.MustCompile(`(?i)/providers/microsoft\.operationsmanagement/solutions/([^/]+)$`)
	omsName     = regexp.MustCompile(`^([^()]+)\(([^()]+)\)$`)
)

// checkOperationsManagement refuses a solution the rules above do not allow;
// code is "" when it is allowed or id is no solution.
func (a *ARM) checkOperationsManagement(id string, body map[string]any) (status int, code, message string) {
	m := omsSolution.FindStringSubmatch(id)
	if m == nil {
		return 0, "", ""
	}
	name := m[1]
	props, _ := body["properties"].(map[string]any)
	workspace, _ := props["workspaceResourceId"].(string)
	if workspace == "" {
		return http.StatusBadRequest, "SolutionWorkspaceMissing", "A solution needs properties.workspaceResourceId."
	}
	if _, ok := a.resources[strings.ToLower(workspace)]; !ok {
		return http.StatusNotFound, "ResourceNotFound", "The workspace " + workspace + " was not found."
	}
	for _, list := range []string{"containedResources", "referencedResources"} {
		ids, _ := props[list].([]any)
		for _, r := range ids {
			rid, _ := r.(string)
			if !a.exists(rid) {
				return http.StatusNotFound, "ResourceNotFound", "The resource " + rid + " in properties." + list + " was not found."
			}
		}
	}
	plan, _ := body["plan"].(map[string]any)
	if plan == nil {
		return http.StatusBadRequest, "SolutionPlanMissing", "A solution needs a plan."
	}
	if p, _ := plan["publisher"].(string); !strings.EqualFold(p, "Microsoft") {
		return 0, "", ""
	}
	n := omsName.FindStringSubmatch(name)
	if n == nil || !strings.EqualFold(n[2], lastSegment(workspace)) {
		return http.StatusBadRequest, "InvalidSolutionName", "A Microsoft gallery solution is named <type>(" + lastSegment(workspace) + "), not " + name + "."
	}
	if pn, _ := plan["name"].(string); pn != name {
		return http.StatusBadRequest, "InvalidSolutionPlan", "A Microsoft gallery solution's plan name is the solution's name, " + name + "."
	}
	if pr, _ := plan["product"].(string); pr != "OMSGallery/"+n[1] {
		return http.StatusBadRequest, "InvalidSolutionPlan", "A Microsoft gallery solution's plan product is OMSGallery/" + n[1] + "."
	}
	return 0, "", ""
}
