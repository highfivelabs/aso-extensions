// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"testing"
)

// What a managed namespace's name may be, and what a snapshot must name.
func TestContainerService(t *testing.T) {
	a := New(func(string) bool { return true })
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	rg := "/subscriptions/" + sub1 + "/resourceGroups/aks"
	put("resource group", rg, `{"location":"westus"}`, http.StatusCreated)
	cluster := rg + "/providers/Microsoft.ContainerService/managedClusters/c1"
	fleet := rg + "/providers/Microsoft.ContainerService/fleets/f1"
	a.AddResource(cluster)
	a.AddResource(fleet)
	ns := `{"location":"westus","properties":{"labels":{"team":"a"},"defaultResourceQuota":{"cpuRequest":"1"}}}`
	put("namespace with an upper-case name", cluster+"/managedNamespaces/TeamA", ns, http.StatusBadRequest)
	put("kube-system", cluster+"/managedNamespaces/kube-system", ns, http.StatusBadRequest)
	put("managed namespace", cluster+"/managedNamespaces/team-a", ns, http.StatusCreated)
	put("fleet managed namespace named kube-public", fleet+"/managedNamespaces/kube-public", `{"location":"westus","properties":{}}`, http.StatusBadRequest)
	put("fleet managed namespace", fleet+"/managedNamespaces/team-a", `{"location":"westus","properties":{"adoptionPolicy":"Never","deletePolicy":"Keep"}}`, http.StatusCreated)

	snap := rg + "/providers/Microsoft.ContainerService/snapshots/s1"
	pool := cluster + "/agentPools/system"
	snapshot := func(source string) string {
		return `{"location":"westus","properties":{"creationData":{"sourceResourceId":"` + source + `"}}}`
	}
	put("snapshot without a source", snap, `{"location":"westus","properties":{}}`, http.StatusBadRequest)
	put("snapshot of a cluster, not a pool", snap, snapshot(cluster), http.StatusBadRequest)
	put("snapshot of a missing pool", snap, snapshot(pool), http.StatusNotFound)
	a.AddResource(pool)
	put("snapshot", snap, snapshot(pool), http.StatusCreated)
}
