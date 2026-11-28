// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// A Batch account's applications and pools (docs/plan.md f73;
// Microsoft.Batch, 2025-06-01), as far as the fake models them. Each rule
// is one the spec or Batch's documentation states:
//
//   - a pool has a vmSize and a virtual machine configuration with an
//     image reference and a node agent SKU (400; the cloud service
//     configuration is retired and not in the spec);
//   - a pool's scale settings are fixed or automatic, not both (400), and
//     an automatic one has a formula (400);
//   - a pool's subnet, when given, exists (404);
//   - a pool's application packages are applications of its own account
//     (400) that exist (404);
//   - a pool's task slots per node, when given, are at least 1 (400).
//
// Not modelled: nodes being allocated (a pool is steady at once), quotas,
// images and node agent SKUs being real, start tasks, the application
// packages' versions, and the error codes (the fake's own).
var (
	batchPool    = regexp.MustCompile(`(?i)^(/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.batch/batchaccounts/[^/]+)/pools/[^/]+$`)
	batchAppPath = regexp.MustCompile(`(?i)^/applications/[^/]+(/versions/[^/]+)?$`)
)

// checkBatch refuses what the rules above do not allow; code is "" when it
// is allowed or id is none of these.
func (a *ARM) checkBatch(id string, body map[string]any) (status int, code, message string) {
	m := batchPool.FindStringSubmatch(id)
	if m == nil {
		return 0, "", ""
	}
	account := m[1]
	bad := func(msg string) (int, string, string) {
		return http.StatusBadRequest, "InvalidRequestBody", msg
	}
	props, _ := body["properties"].(map[string]any)
	if stringOf(props["vmSize"]) == "" {
		return bad("'vmSize' is required.")
	}
	deployment, _ := props["deploymentConfiguration"].(map[string]any)
	vm, _ := deployment["virtualMachineConfiguration"].(map[string]any)
	if vm == nil {
		return bad("'deploymentConfiguration.virtualMachineConfiguration' is required.")
	}
	if _, ok := vm["imageReference"].(map[string]any); !ok || stringOf(vm["nodeAgentSkuId"]) == "" {
		return bad("A virtual machine configuration has an imageReference and a nodeAgentSkuId.")
	}
	if scale, ok := props["scaleSettings"].(map[string]any); ok {
		_, fixed := scale["fixedScale"].(map[string]any)
		auto, isAuto := scale["autoScale"].(map[string]any)
		if fixed && isAuto {
			return bad("A pool's scale settings are fixedScale or autoScale, not both.")
		}
		if isAuto && stringOf(auto["formula"]) == "" {
			return bad("An autoScale setting has a formula.")
		}
	}
	if n, ok := props["taskSlotsPerNode"].(float64); ok && n < 1 {
		return bad("'taskSlotsPerNode' is at least 1.")
	}
	network, _ := props["networkConfiguration"].(map[string]any)
	if s := stringOf(network["subnetId"]); s != "" && !a.exists(s) {
		return http.StatusNotFound, "ResourceNotFound", "The subnet " + s + " was not found."
	}
	packages, _ := props["applicationPackages"].([]any)
	for _, p := range packages {
		pm, _ := p.(map[string]any)
		ref := stringOf(pm["id"])
		if !strings.HasPrefix(strings.ToLower(ref), strings.ToLower(account)) || !batchAppPath.MatchString(ref[len(account):]) {
			return bad("An application package is an application of the pool's account, " + account + "; not " + ref + ".")
		}
		rest := ref[len(account):]
		if i := strings.Index(strings.ToLower(rest), "/versions/"); i >= 0 {
			rest = rest[:i]
		}
		app := account + rest
		if !a.exists(app) {
			return http.StatusNotFound, "ResourceNotFound", "The application " + app + " was not found."
		}
	}
	return 0, "", ""
}
