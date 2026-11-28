// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Azure Site Recovery (Microsoft.RecoveryServices/vaults/*/replication*,
// 2026-07-01; docs/plan.md f52), as far as the fake models it, for the
// Azure-to-Azure (A2A) case. Each rule is one Site Recovery's documentation
// of enabling replication states:
//
//   - an Azure fabric names its region (customDetails.location; 400), and a
//     vault has one Azure fabric per region (409);
//   - an A2A policy keeps app-consistent snapshots no less often than it
//     keeps recovery points (appConsistentFrequencyInMinutes at most
//     recoveryPointHistory; 400);
//   - a protection container mapping names a policy and a target container
//     that exist (404), the target in another container than its own (400);
//   - a replicated item names a policy that exists (404); for A2A, the VM
//     (fabricObjectId), the recovery container and the recovery resource
//     group exist (404), and its container has a mapping to that recovery
//     container with that policy (400);
//   - a recovery plan names fabrics that exist (404) and replicated items
//     that exist (404).
//
// Not modelled: replication running (initial replication, recovery points,
// health), failover and its tests (POST actions), the mobility agent, the
// on-premises scenarios (Hyper-V, VMware, physical) beyond the generic
// resource, and the error codes (the fake's own).
var (
	asrVault      = `(?i)^(.*/providers/microsoft\.recoveryservices/vaults/[^/]+)`
	asrFabric     = regexp.MustCompile(asrVault + `/replicationfabrics/[^/]+$`)
	asrPolicy     = regexp.MustCompile(asrVault + `/replicationpolicies/[^/]+$`)
	asrMapping    = regexp.MustCompile(`(?i)^(.*/replicationprotectioncontainers/[^/]+)/replicationprotectioncontainermappings/[^/]+$`)
	asrItem       = regexp.MustCompile(`(?i)^(.*/replicationprotectioncontainers/[^/]+)/replicationprotecteditems/[^/]+$`)
	asrRecoveryPl = regexp.MustCompile(asrVault + `/replicationrecoveryplans/[^/]+$`)
)

// checkSiteRecovery refuses what the rules above do not allow; code is ""
// when it is allowed or id is none of these.
func (a *ARM) checkSiteRecovery(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	num := func(m map[string]any, k string) float64 { f, _ := m[k].(float64); return f }
	missing := func(what, ref string) (int, string, string) {
		return http.StatusNotFound, "ResourceNotFound", "The " + what + " " + ref + " was not found."
	}
	switch {
	case asrFabric.MatchString(id):
		custom, _ := props["customDetails"].(map[string]any)
		if !strings.EqualFold(str(custom, "instanceType"), "Azure") {
			return 0, "", ""
		}
		loc := str(custom, "location")
		if loc == "" {
			return http.StatusBadRequest, "AzureFabricLocationMissing", "An Azure fabric names its region (customDetails.location)."
		}
		vault := strings.ToLower(asrFabric.FindStringSubmatch(id)[1])
		for k, res := range a.resources {
			if k == strings.ToLower(id) || !asrFabric.MatchString(k) || strings.ToLower(asrFabric.FindStringSubmatch(k)[1]) != vault {
				continue
			}
			p, _ := (*res)["properties"].(map[string]any)
			c, _ := p["customDetails"].(map[string]any)
			if strings.EqualFold(str(c, "location"), loc) {
				return http.StatusConflict, "AzureFabricExists", "The vault already has an Azure fabric for " + loc + "."
			}
		}
	case asrPolicy.MatchString(id):
		in, _ := props["providerSpecificInput"].(map[string]any)
		if strings.EqualFold(str(in, "instanceType"), "A2A") {
			if app, keep := num(in, "appConsistentFrequencyInMinutes"), num(in, "recoveryPointHistory"); app > 0 && keep > 0 && app > keep {
				return http.StatusBadRequest, "AppConsistentFrequencyTooLong", "App-consistent snapshots are taken at least as often as recovery points are kept (appConsistentFrequencyInMinutes at most recoveryPointHistory)."
			}
		}
	case asrMapping.MatchString(id):
		container := asrMapping.FindStringSubmatch(id)[1]
		policy, target := str(props, "policyId"), str(props, "targetProtectionContainerId")
		if policy != "" && !a.exists(policy) {
			return missing("replication policy", policy)
		}
		if target != "" && !a.exists(target) {
			return missing("protection container", target)
		}
		if strings.EqualFold(target, container) {
			return http.StatusBadRequest, "SameSourceAndTargetContainer", "A protection container mapping's target is another container than its own."
		}
	case asrItem.MatchString(id):
		container := strings.ToLower(asrItem.FindStringSubmatch(id)[1])
		policy := str(props, "policyId")
		if policy != "" && !a.exists(policy) {
			return missing("replication policy", policy)
		}
		in, _ := props["providerSpecificDetails"].(map[string]any)
		if !strings.EqualFold(str(in, "instanceType"), "A2A") {
			return 0, "", ""
		}
		for _, f := range []struct{ what, key string }{{"virtual machine", "fabricObjectId"}, {"protection container", "recoveryContainerId"}, {"resource group", "recoveryResourceGroupId"}} {
			if ref := str(in, f.key); ref != "" && !a.exists(ref) {
				return missing(f.what, ref)
			}
		}
		target := str(in, "recoveryContainerId")
		mapped := false
		for k, res := range a.resources {
			if !strings.HasPrefix(k, container+"/replicationprotectioncontainermappings/") {
				continue
			}
			p, _ := (*res)["properties"].(map[string]any)
			if strings.EqualFold(str(p, "targetProtectionContainerId"), target) && strings.EqualFold(str(p, "policyId"), policy) {
				mapped = true
				break
			}
		}
		if !mapped {
			return http.StatusBadRequest, "ProtectionContainerMappingMissing", "The protection container has no mapping to the recovery container with this policy."
		}
	case asrRecoveryPl.MatchString(id):
		for _, k := range []string{"primaryFabricId", "recoveryFabricId"} {
			if ref := str(props, k); ref != "" && !a.exists(ref) {
				return missing("fabric", ref)
			}
		}
		groups, _ := props["groups"].([]any)
		for _, g := range groups {
			gm, _ := g.(map[string]any)
			items, _ := gm["replicationProtectedItems"].([]any)
			for _, it := range items {
				im, _ := it.(map[string]any)
				if ref := str(im, "id"); ref != "" && !a.exists(ref) {
					return missing("replicated item", ref)
				}
			}
		}
	}
	return 0, "", ""
}
