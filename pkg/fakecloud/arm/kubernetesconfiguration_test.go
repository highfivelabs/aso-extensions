// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"strings"
	"testing"
)

// Where a source control configuration may be and what it gives; its
// protected settings kept aside; a private link scope's cluster.
func TestKubernetesConfiguration(t *testing.T) {
	a := New(func(string) bool { return true })
	put := func(what, path, body string, want int) string {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
		return resp
	}
	rg := "/subscriptions/" + sub1 + "/resourceGroups/k8s"
	put("resource group", rg, `{"location":"westus"}`, http.StatusCreated)
	aks := rg + "/providers/Microsoft.ContainerService/managedClusters/c1"
	vm := rg + "/providers/Microsoft.Compute/virtualMachines/vm1"
	arc := rg + "/providers/Microsoft.Kubernetes/connectedClusters/arc1"
	a.AddResource(aks)
	a.AddResource(vm)
	scc := "/providers/Microsoft.KubernetesConfiguration/sourceControlConfigurations/cluster-config"
	cfg := `{"properties":{"repositoryUrl":"git@github.com:contoso/cluster-config.git","operatorType":"Flux","operatorScope":"cluster","configurationProtectedSettings":{"sshPrivateKey":"a2V5"}}}`

	put("configuration on a VM", vm+scc, cfg, http.StatusBadRequest)
	put("configuration without a repository", aks+scc, `{"properties":{"operatorType":"Flux"}}`, http.StatusBadRequest)
	put("configuration with another operator", aks+scc, `{"properties":{"repositoryUrl":"https://x","operatorType":"Helm"}}`, http.StatusBadRequest)
	resp := put("configuration on an AKS cluster", aks+scc, cfg, http.StatusCreated)
	if strings.Contains(resp, "a2V5") {
		t.Fatalf("the protected setting was returned: %s", resp)
	}
	if k, ok := a.SharedKey(aks + scc + "/sshPrivateKey"); !ok || k != "a2V5" {
		t.Fatalf("the protected setting kept: %q %v", k, ok)
	}

	scope := rg + "/providers/Microsoft.KubernetesConfiguration/privateLinkScopes/pls"
	put("scope for a missing cluster", scope, `{"location":"westus","properties":{"clusterResourceId":"`+arc+`"}}`, http.StatusNotFound)
	a.AddResource(arc)
	put("scope", scope, `{"location":"westus","properties":{"clusterResourceId":"`+arc+`","publicNetworkAccess":"Disabled"}}`, http.StatusCreated)
	put("configuration on an Arc cluster", arc+scc, cfg, http.StatusCreated)
}
