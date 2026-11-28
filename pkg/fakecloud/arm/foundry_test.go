// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// An account's managed network is default and does not go back from V2 to
// V1; a private endpoint outbound rule reaches a resource that exists.
func TestFoundry(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/ai"
	account := rg + "/providers/Microsoft.CognitiveServices/accounts/foundry"
	for _, id := range []string{rg, account} {
		a.AddResource(id)
	}
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	put("a managed network not named default", account+"/managedNetworks/other", `{"properties":{"managedNetwork":{"isolationMode":"AllowOnlyApprovedOutbound"}}}`, http.StatusBadRequest)
	network := account + "/managedNetworks/default"
	put("a V2 managed network", network, `{"properties":{"managedNetwork":{"isolationMode":"AllowOnlyApprovedOutbound","managedNetworkKind":"V2"}}}`, http.StatusCreated)
	put("back to V1", network, `{"properties":{"managedNetwork":{"isolationMode":"AllowOnlyApprovedOutbound","managedNetworkKind":"V1"}}}`, http.StatusBadRequest)
	storage := rg + "/providers/Microsoft.Storage/storageAccounts/data"
	rule := `{"properties":{"type":"PrivateEndpoint","destination":{"serviceResourceId":"` + storage + `","subresourceTarget":"blob"}}}`
	put("a rule to a resource that is not there", network+"/outboundRules/data", rule, http.StatusNotFound)
	a.AddResource(storage)
	put("a private endpoint rule", network+"/outboundRules/data", rule, http.StatusCreated)
	put("an FQDN rule", network+"/outboundRules/pypi", `{"properties":{"type":"FQDN","destination":"pypi.org"}}`, http.StatusCreated)
}
