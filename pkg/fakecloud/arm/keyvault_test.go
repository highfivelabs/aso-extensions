// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"encoding/json"
	"net/http"
	"testing"
)

// What a managed HSM is created with, what cannot change, keys a PUT does
// not change and Resource Manager does not delete, and private endpoint
// connections a PUT only updates.
func TestKeyVaultManagedHSM(t *testing.T) {
	a := New(func(string) bool { return true })
	call := func(what, method, path, body string, want int) map[string]any {
		t.Helper()
		code, resp := do(t, a, method, path, body)
		expect(t, what, code, resp, want)
		out := map[string]any{}
		_ = json.Unmarshal([]byte(resp), &out)
		return out
	}
	rg := "/subscriptions/" + sub1 + "/resourceGroups/keys"
	call("resource group", http.MethodPut, rg, `{"location":"westus"}`, http.StatusCreated)
	hsm := rg + "/providers/Microsoft.KeyVault/managedHSMs/hsm1"
	const sku = `"sku":{"family":"B","name":"Standard_B1"}`
	const tenant = `"tenantId":"00000000-0000-0000-0000-0000000000aa"`
	const admins = `"initialAdminObjectIds":["00000000-0000-0000-0000-0000000000bb"]`

	call("HSM without a sku", http.MethodPut, hsm, `{"location":"westus","properties":{`+tenant+`,`+admins+`}}`, http.StatusBadRequest)
	call("HSM without a tenant", http.MethodPut, hsm, `{"location":"westus",`+sku+`,"properties":{`+admins+`}}`, http.StatusBadRequest)
	call("HSM without administrators", http.MethodPut, hsm, `{"location":"westus",`+sku+`,"properties":{`+tenant+`,"initialAdminObjectIds":[]}}`, http.StatusBadRequest)
	call("HSM without soft delete", http.MethodPut, hsm, `{"location":"westus",`+sku+`,"properties":{`+tenant+`,`+admins+`,"enableSoftDelete":false}}`, http.StatusBadRequest)
	call("HSM retention too short", http.MethodPut, hsm, `{"location":"westus",`+sku+`,"properties":{`+tenant+`,`+admins+`,"softDeleteRetentionInDays":3}}`, http.StatusBadRequest)
	call("HSM", http.MethodPut, hsm, `{"location":"westus",`+sku+`,"properties":{`+tenant+`,`+admins+`,"softDeleteRetentionInDays":30,"enablePurgeProtection":true}}`, http.StatusCreated)
	call("HSM retention changed", http.MethodPut, hsm, `{"location":"westus",`+sku+`,"properties":{`+tenant+`,`+admins+`,"softDeleteRetentionInDays":60}}`, http.StatusBadRequest)
	call("HSM purge protection off", http.MethodPut, hsm, `{"location":"westus",`+sku+`,"properties":{`+tenant+`,`+admins+`,"enablePurgeProtection":false}}`, http.StatusBadRequest)
	call("HSM public access off", http.MethodPut, hsm, `{"location":"westus",`+sku+`,"properties":{`+tenant+`,`+admins+`,"softDeleteRetentionInDays":30,"publicNetworkAccess":"Disabled"}}`, http.StatusOK)

	key := hsm + "/keys/cmk"
	call("key without a type", http.MethodPut, key, `{"properties":{"keySize":3072}}`, http.StatusBadRequest)
	call("key", http.MethodPut, key, `{"properties":{"kty":"RSA-HSM","keySize":3072}}`, http.StatusCreated)
	r := call("key again, changed", http.MethodPut, key, `{"properties":{"kty":"RSA-HSM","keySize":4096}}`, http.StatusOK)
	if size := r["properties"].(map[string]any)["keySize"]; size != float64(3072) {
		t.Fatalf("a PUT on an existing key changes nothing: keySize %v", size)
	}
	call("key delete", http.MethodDelete, key, ``, http.StatusMethodNotAllowed)
	if !a.HasResource(key) {
		t.Fatal("the key is gone")
	}

	vault := rg + "/providers/Microsoft.KeyVault/vaults/kv1"
	a.AddResource(vault)
	for _, conn := range []string{hsm + "/privateEndpointConnections/pe1", vault + "/privateEndpointConnections/pe1"} {
		approve := `{"properties":{"privateLinkServiceConnectionState":{"status":"Approved","description":"ok"}}}`
		call("connection no endpoint made", http.MethodPut, conn, approve, http.StatusNotFound)
		a.AddResource(conn)
		call("connection approved", http.MethodPut, conn, approve, http.StatusOK)
		call("connection removed", http.MethodDelete, conn, ``, http.StatusOK)
	}
}
