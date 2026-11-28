// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// Azure-to-Azure replication: one Azure fabric per region, a policy's
// snapshot frequency within its retention, mappings and replicated items
// naming what exists, an item only through a mapping, and a recovery plan's
// fabrics and items.
func TestSiteRecovery(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/dr"
	vault := rg + "/providers/Microsoft.RecoveryServices/vaults/rsv"
	vm := rg + "/providers/Microsoft.Compute/virtualMachines/app"
	target := "/subscriptions/" + sub1 + "/resourceGroups/dr-target"
	for _, id := range []string{rg, vault, vm, target} {
		a.AddResource(id)
	}
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	west, east := vault+"/replicationFabrics/west", vault+"/replicationFabrics/east"
	put("an Azure fabric without a region", west, `{"properties":{"customDetails":{"instanceType":"Azure"}}}`, http.StatusBadRequest)
	put("the West US fabric", west, `{"properties":{"customDetails":{"instanceType":"Azure","location":"westus"}}}`, http.StatusCreated)
	put("a second West US fabric", vault+"/replicationFabrics/west2", `{"properties":{"customDetails":{"instanceType":"Azure","location":"westus"}}}`, http.StatusConflict)
	put("the West US fabric again", west, `{"properties":{"customDetails":{"instanceType":"Azure","location":"westus"}}}`, http.StatusOK)
	put("the East US fabric", east, `{"properties":{"customDetails":{"instanceType":"Azure","location":"eastus"}}}`, http.StatusCreated)
	policy := vault + "/replicationPolicies/a2a"
	put("app-consistent less often than retention", policy, `{"properties":{"providerSpecificInput":{"instanceType":"A2A","recoveryPointHistory":60,"appConsistentFrequencyInMinutes":240}}}`, http.StatusBadRequest)
	put("a policy", policy, `{"properties":{"providerSpecificInput":{"instanceType":"A2A","recoveryPointHistory":1440,"appConsistentFrequencyInMinutes":240}}}`, http.StatusCreated)
	src, dst := west+"/replicationProtectionContainers/src", east+"/replicationProtectionContainers/dst"
	put("the source container", src, `{"properties":{"providerSpecificInput":[{"instanceType":"A2A"}]}}`, http.StatusCreated)
	put("the target container", dst, `{"properties":{"providerSpecificInput":[{"instanceType":"A2A"}]}}`, http.StatusCreated)
	mapping := func(policy, target string) string {
		return `{"properties":{"policyId":"` + policy + `","targetProtectionContainerId":"` + target + `","providerSpecificInput":{"instanceType":"A2A"}}}`
	}
	put("a mapping to a missing container", src+"/replicationProtectionContainerMappings/m", mapping(policy, east+"/replicationProtectionContainers/none"), http.StatusNotFound)
	put("a mapping with a missing policy", src+"/replicationProtectionContainerMappings/m", mapping(vault+"/replicationPolicies/none", dst), http.StatusNotFound)
	put("a mapping to its own container", src+"/replicationProtectionContainerMappings/m", mapping(policy, src), http.StatusBadRequest)
	item := func(policy, vm string) string {
		return `{"properties":{"policyId":"` + policy + `","providerSpecificDetails":{"instanceType":"A2A","fabricObjectId":"` + vm +
			`","recoveryContainerId":"` + dst + `","recoveryResourceGroupId":"` + target + `"}}}`
	}
	put("an item before a mapping", src+"/replicationProtectedItems/app", item(policy, vm), http.StatusBadRequest)
	put("a mapping", src+"/replicationProtectionContainerMappings/m", mapping(policy, dst), http.StatusCreated)
	put("an item of a missing VM", src+"/replicationProtectedItems/app", item(policy, rg+"/providers/Microsoft.Compute/virtualMachines/none"), http.StatusNotFound)
	put("an item", src+"/replicationProtectedItems/app", item(policy, vm), http.StatusCreated)
	plan := func(item string) string {
		return `{"properties":{"primaryFabricId":"` + west + `","recoveryFabricId":"` + east + `","failoverDeploymentModel":"ResourceManager","groups":[{"groupType":"Boot","replicationProtectedItems":[{"id":"` + item + `"}]}]}}`
	}
	put("a plan with a missing item", vault+"/replicationRecoveryPlans/plan", plan(src+"/replicationProtectedItems/none"), http.StatusNotFound)
	put("a plan", vault+"/replicationRecoveryPlans/plan", plan(src+"/replicationProtectedItems/app"), http.StatusCreated)
}
