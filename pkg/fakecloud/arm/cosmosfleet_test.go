// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// A fleetspace's throughput pool is ordered; a fleetspace account names a
// Cosmos DB account that exists; a MongoDB vCore cluster user is an Entra
// object ID with an identity provider.
func TestCosmosFleet(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/data"
	fleet := rg + "/providers/Microsoft.DocumentDB/fleets/tenants"
	cluster := rg + "/providers/Microsoft.DocumentDB/mongoClusters/docs"
	for _, id := range []string{rg, fleet, cluster} {
		a.AddResource(id)
	}
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	space := fleet + "/fleetspaces/shared"
	put("a pool with its minimum above its maximum", space, `{"properties":{"dataRegions":["westus"],"throughputPoolConfiguration":{"minThroughput":4000,"maxThroughput":1000}}}`, http.StatusBadRequest)
	put("a fleetspace", space, `{"properties":{"dataRegions":["westus"],"throughputPoolConfiguration":{"minThroughput":1000,"maxThroughput":4000}}}`, http.StatusCreated)
	account := rg + "/providers/Microsoft.DocumentDB/databaseAccounts/tenant1"
	body := `{"properties":{"globalDatabaseAccountProperties":{"resourceId":"` + account + `","armLocation":"westus"}}}`
	put("an account that is not there", space+"/fleetspaceAccounts/tenant1", body, http.StatusNotFound)
	put("a storage account", space+"/fleetspaceAccounts/tenant1", `{"properties":{"globalDatabaseAccountProperties":{"resourceId":"`+rg+`/providers/Microsoft.Storage/storageAccounts/s"}}}`, http.StatusBadRequest)
	a.AddResource(account)
	put("a fleetspace account", space+"/fleetspaceAccounts/tenant1", body, http.StatusCreated)
	entra := `{"properties":{"identityProvider":{"type":"MicrosoftEntraID","properties":{"principalType":"servicePrincipal"}},"roles":[{"db":"admin","role":"root"}]}}`
	put("a user named by a word", cluster+"/users/app", entra, http.StatusBadRequest)
	put("a user with no identity provider", cluster+"/users/00000000-0000-0000-0000-0000000000a1", `{"properties":{"roles":[{"db":"admin","role":"root"}]}}`, http.StatusBadRequest)
	put("an Entra user", cluster+"/users/00000000-0000-0000-0000-0000000000a1", entra, http.StatusCreated)
}
