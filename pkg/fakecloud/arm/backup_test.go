// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"testing"
)

// A resource guard proxy is deleted only after an unlockDelete that names
// it and the guard's delete request; one unlock opens one DELETE.
func TestGuardProxyUnlockDelete(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/backup"
	code, resp := do(t, a, http.MethodPut, rg, `{"location":"westus"}`)
	expect(t, "resource group", code, resp, http.StatusCreated)
	vault := rg + "/providers/Microsoft.RecoveryServices/vaults/v1"
	a.AddResource(vault)
	guard := rg + "/providers/Microsoft.DataProtection/resourceGuards/g1"
	a.AddResource(guard)
	proxy := vault + "/backupResourceGuardProxies/VaultProxy"
	put := func() {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, proxy, `{"properties":{"resourceGuardResourceId":"`+guard+`"}}`)
		if code != http.StatusCreated && code != http.StatusOK {
			t.Fatalf("proxy: %d %s", code, resp)
		}
	}
	unlock := func(resource, request string) (int, string) {
		return do(t, a, http.MethodPost, proxy+"/unlockDelete", `{"resourceGuardOperationRequests":["`+request+`"],"resourceToBeDeleted":"`+resource+`"}`)
	}
	request := guard + "/deleteResourceGuardProxyRequests/default"
	put()

	code, resp = do(t, a, http.MethodDelete, proxy, ``)
	expect(t, "delete without an unlock", code, resp, http.StatusBadRequest)
	code, resp = unlock(vault, request)
	expect(t, "unlock naming another resource", code, resp, http.StatusBadRequest)
	code, resp = unlock(proxy, guard)
	expect(t, "unlock without the guard's request", code, resp, http.StatusBadRequest)
	code, resp = unlock(proxy, rg+"/providers/Microsoft.DataProtection/resourceGuards/other/deleteResourceGuardProxyRequests/default")
	expect(t, "unlock with another guard's request", code, resp, http.StatusBadRequest)
	code, resp = unlock(proxy, request)
	expect(t, "unlock", code, resp, http.StatusOK)
	code, resp = do(t, a, http.MethodDelete, proxy, ``)
	expect(t, "delete after an unlock", code, resp, http.StatusOK)

	put()
	code, resp = do(t, a, http.MethodDelete, proxy, ``)
	expect(t, "a second delete needs a second unlock", code, resp, http.StatusBadRequest)
	bare := vault + "/backupResourceGuardProxies/bare"
	code, resp = do(t, a, http.MethodPut, bare, `{"properties":{}}`)
	expect(t, "proxy naming no guard", code, resp, http.StatusCreated)
	code, resp = do(t, a, http.MethodDelete, bare, ``)
	expect(t, "delete of a proxy naming no guard", code, resp, http.StatusOK)
	code, resp = do(t, a, http.MethodPost, vault+"/backupResourceGuardProxies/none/unlockDelete", `{}`)
	expect(t, "unlock a missing proxy", code, resp, http.StatusNotFound)
}
