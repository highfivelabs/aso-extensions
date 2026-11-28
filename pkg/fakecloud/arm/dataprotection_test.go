// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// A backup vault's resource guard proxy is deleted only once its
// unlockDelete names it and its guard's delete request, as a Recovery
// Services vault's is (backup.go).
func TestDataProtectionResourceGuardProxy(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/backup"
	vault := rg + "/providers/Microsoft.DataProtection/backupVaults/blobs"
	for _, id := range []string{rg, vault} {
		a.AddResource(id)
	}
	guard := rg + "/providers/Microsoft.DataProtection/resourceGuards/mua"
	code, resp := do(t, a, http.MethodPut, guard, `{"location":"westus","properties":{"vaultCriticalOperationExclusionList":[]}}`)
	expect(t, "a resource guard", code, resp, http.StatusCreated)
	proxy := vault + "/backupResourceGuardProxies/DppResourceGuardProxy"
	code, resp = do(t, a, http.MethodPut, proxy, `{"properties":{"resourceGuardResourceId":"`+guard+`"}}`)
	expect(t, "a proxy", code, resp, http.StatusCreated)
	code, resp = do(t, a, http.MethodDelete, proxy, "")
	expect(t, "deleting the proxy unlocked by nothing", code, resp, http.StatusBadRequest)
	code, resp = do(t, a, http.MethodPost, proxy+"/unlockDelete", `{"resourceGuardOperationRequests":["`+guard+`x/deleteResourceGuardProxyRequests/default"],"resourceToBeDeleted":"`+proxy+`"}`)
	expect(t, "an unlock naming another guard", code, resp, http.StatusBadRequest)
	code, resp = do(t, a, http.MethodPost, proxy+"/unlockDelete", `{"resourceGuardOperationRequests":["`+guard+`/deleteResourceGuardProxyRequests/default"],"resourceToBeDeleted":"`+proxy+`"}`)
	expect(t, "the unlock", code, resp, http.StatusOK)
	code, resp = do(t, a, http.MethodDelete, proxy, "")
	expect(t, "deleting the unlocked proxy", code, resp, http.StatusOK)
}
