// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Backup of a virtual machine: discovery first, the machine's own container
// and item names, a policy of the same vault, one vault per machine,
// long-running PUT and DELETE, and a vault that holds items cannot go.
func TestProtectedItems(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/backup"
	a.AddResource(rg)
	vault := rg + "/providers/Microsoft.RecoveryServices/vaults/v1"
	a.AddResource(vault)
	other := rg + "/providers/Microsoft.RecoveryServices/vaults/v2"
	a.AddResource(other)
	policy := vault + "/backupPolicies/daily"
	a.AddResource(policy)
	vm := rg + "/providers/Microsoft.Compute/virtualMachines/app1"
	container := vault + "/backupFabrics/Azure/protectionContainers/iaasvmcontainer;iaasvmcontainerv2;backup;app1"
	item := container + "/protectedItems/vm;iaasvmcontainerv2;backup;app1"
	body := func(policyID, state string) string {
		b := `{"properties":{"protectedItemType":"Microsoft.Compute/virtualMachines","sourceResourceId":"` + vm + `"`
		if policyID != "" {
			b += `,"policyId":"` + policyID + `"`
		}
		if state != "" {
			b += `,"protectionState":"` + state + `"`
		}
		return b + `}}`
	}

	code, resp := do(t, a, http.MethodPut, item, body(policy, ""))
	expect(t, "a machine that does not exist", code, resp, http.StatusNotFound)
	a.AddResource(vm)
	code, resp = do(t, a, http.MethodPut, item, body(policy, ""))
	expect(t, "a machine the vault has not discovered", code, resp, http.StatusNotFound)

	code, resp = do(t, a, http.MethodPost, vault+"/backupFabrics/Other/refreshContainers", ``)
	expect(t, "refresh of another fabric", code, resp, http.StatusBadRequest)
	req := httptest.NewRequest(http.MethodPost, vault+"/backupFabrics/Azure/refreshContainers?api-version=2026-07-01", nil)
	req.Header.Set("Authorization", "Bearer ok")
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)
	expect(t, "refresh", rec.Code, rec.Body.String(), http.StatusAccepted)
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "/backupFabrics/Azure/operationResults/") {
		t.Fatalf("refresh Location: %q", loc)
	}
	opPath := loc[strings.Index(loc, "/subscriptions/"):strings.Index(loc, "?")]
	code, resp = do(t, a, http.MethodGet, opPath, ``)
	expect(t, "refresh in progress", code, resp, http.StatusAccepted)
	code, resp = do(t, a, http.MethodGet, opPath, ``)
	expect(t, "refresh done", code, resp, http.StatusNoContent)

	code, resp = do(t, a, http.MethodPut, vault+"/backupFabrics/Other/protectionContainers/c/protectedItems/i", body(policy, ""))
	expect(t, "another fabric", code, resp, http.StatusBadRequest)
	code, resp = do(t, a, http.MethodPut, vault+"/backupFabrics/Azure/protectionContainers/iaasvmcontainer;iaasvmcontainerv2;backup;other/protectedItems/vm;iaasvmcontainerv2;backup;app1", body(policy, ""))
	expect(t, "another machine's container", code, resp, http.StatusBadRequest)
	code, resp = do(t, a, http.MethodPut, container+"/protectedItems/vm;iaasvmcontainerv2;backup;other", body(policy, ""))
	expect(t, "another machine's item name", code, resp, http.StatusBadRequest)
	code, resp = do(t, a, http.MethodPut, item, body("", ""))
	expect(t, "no policy", code, resp, http.StatusBadRequest)
	code, resp = do(t, a, http.MethodPut, item, body(other+"/backupPolicies/daily", ""))
	expect(t, "another vault's policy", code, resp, http.StatusBadRequest)
	code, resp = do(t, a, http.MethodPut, item, body(vault+"/backupPolicies/missing", ""))
	expect(t, "a missing policy", code, resp, http.StatusNotFound)
	code, resp = do(t, a, http.MethodPut, item, body(policy, "ProtectionError"))
	expect(t, "a state Azure sets", code, resp, http.StatusBadRequest)
	code, resp = do(t, a, http.MethodPut, item, `{"properties":{"protectedItemType":"AzureFileShareProtectedItem","sourceResourceId":"`+vm+`"}}`)
	expect(t, "a file share", code, resp, http.StatusBadRequest)

	code, resp = do(t, a, http.MethodPut, item, body(policy, ""))
	expect(t, "protect", code, resp, http.StatusAccepted)
	if !a.HasResource(container) {
		t.Errorf("the machine's container was not registered")
	}
	r, ok := a.Resource(item)
	if !ok {
		t.Fatalf("the item is not stored")
	}
	p, _ := r["properties"].(map[string]any)
	if p["protectionState"] != "IRPending" || p["containerName"] != "iaasvmcontainerv2;backup;app1" || p["friendlyName"] != "app1" ||
		p["workloadType"] != "VM" || p["policyId"] != policy {
		t.Errorf("item: %v", r)
	}
	code, resp = do(t, a, http.MethodGet, item, ``)
	expect(t, "get", code, resp, http.StatusOK)

	// The same machine in another vault is refused.
	a.discovered[strings.ToLower(other)] = map[string]bool{strings.ToLower(vm): true}
	a.AddResource(other + "/backupPolicies/daily")
	code, resp = do(t, a, http.MethodPut, strings.Replace(item, "/vaults/v1/", "/vaults/v2/", 1), body(other+"/backupPolicies/daily", ""))
	expect(t, "a machine another vault protects", code, resp, http.StatusBadRequest)

	code, resp = do(t, a, http.MethodPut, item, body("", "ProtectionStopped"))
	expect(t, "stop protection, keep the data", code, resp, http.StatusAccepted)
	r, _ = a.Resource(item)
	if p, _ := r["properties"].(map[string]any); p["protectionState"] != "ProtectionStopped" {
		t.Errorf("stopped: %v", r)
	}

	code, resp = do(t, a, http.MethodDelete, vault, ``)
	expect(t, "a vault holding an item", code, resp, http.StatusBadRequest)

	req = httptest.NewRequest(http.MethodDelete, item+"?api-version=2026-07-01", nil)
	req.Header.Set("Authorization", "Bearer ok")
	rec = httptest.NewRecorder()
	a.ServeHTTP(rec, req)
	expect(t, "delete", rec.Code, rec.Body.String(), http.StatusAccepted)
	async := rec.Header().Get("Azure-AsyncOperation")
	if !strings.Contains(async, "/vaults/v1/backupOperations/") {
		t.Fatalf("delete Azure-AsyncOperation: %q", async)
	}
	opPath = async[strings.Index(async, "/subscriptions/"):strings.Index(async, "?")]
	status := func() string {
		code, resp := do(t, a, http.MethodGet, opPath, ``)
		expect(t, "operation status", code, resp, http.StatusOK)
		var s struct{ Status string }
		_ = json.Unmarshal([]byte(resp), &s)
		return s.Status
	}
	if s := status(); s != "InProgress" {
		t.Errorf("first poll: %s", s)
	}
	if s := status(); s != "Succeeded" {
		t.Errorf("second poll: %s", s)
	}
	if a.HasResource(item) || a.HasResource(container) {
		t.Errorf("the item or its container is still there")
	}
	code, resp = do(t, a, http.MethodDelete, item, ``)
	expect(t, "delete again", code, resp, http.StatusNoContent)
	code, resp = do(t, a, http.MethodDelete, vault, ``)
	expect(t, "the empty vault", code, resp, http.StatusOK)
}
