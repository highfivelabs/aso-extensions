// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// A follower names another cluster that exists; a managed private endpoint
// names a groupId and a resource that exists; a custom image names its
// language and a version or base; a script has one source.
func TestKustoClusterChildren(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/adx"
	follower := rg + "/providers/Microsoft.Kusto/clusters/follower"
	leader := rg + "/providers/Microsoft.Kusto/clusters/leader"
	storage := rg + "/providers/Microsoft.Storage/storageAccounts/ingest"
	db := follower + "/databases/telemetry"
	for _, id := range []string{rg, follower, leader, storage, db} {
		a.AddResource(id)
	}
	put := func(what, id, props string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, id, `{"location":"westus","properties":`+props+`}`)
		expect(t, what, code, resp, want)
	}
	attached := follower + "/attachedDatabaseConfigurations/telemetry"
	put("a follower of no leader", attached, `{"databaseName":"*","defaultPrincipalsModificationKind":"Union"}`, http.StatusBadRequest)
	put("a missing leader", attached, `{"databaseName":"*","defaultPrincipalsModificationKind":"Union","clusterResourceId":"`+leader+`x"}`, http.StatusNotFound)
	put("a cluster following itself", attached, `{"databaseName":"*","defaultPrincipalsModificationKind":"Union","clusterResourceId":"`+follower+`"}`, http.StatusBadRequest)
	put("a follower", attached, `{"databaseName":"*","defaultPrincipalsModificationKind":"Union","clusterResourceId":"`+leader+`"}`, http.StatusCreated)

	mpe := follower + "/managedPrivateEndpoints/ingest"
	put("an endpoint with no groupId", mpe, `{"privateLinkResourceId":"`+storage+`"}`, http.StatusBadRequest)
	put("an endpoint to a missing resource", mpe, `{"groupId":"blob","privateLinkResourceId":"`+storage+`x"}`, http.StatusNotFound)
	put("an endpoint", mpe, `{"groupId":"blob","privateLinkResourceId":"`+storage+`"}`, http.StatusCreated)

	image := follower + "/sandboxCustomImages/py"
	put("an image of no version or base", image, `{"language":"Python"}`, http.StatusBadRequest)
	put("an image", image, `{"language":"Python","languageVersion":"3.10.8","requirementsFileContent":"numpy"}`, http.StatusCreated)

	script := db + "/scripts/tables"
	put("a script with two sources", script, `{"scriptUrl":"https://ingest.blob.core.windows.net/s/tables.kql","scriptContent":".create table T (a:int)"}`, http.StatusBadRequest)
	put("a script with none", script, `{}`, http.StatusBadRequest)
	put("a script", script, `{"scriptContent":".create table T (a:int)","continueOnErrors":false}`, http.StatusCreated)
	put("a connection no private endpoint made", follower+"/privateEndpointConnections/pe", `{"privateLinkServiceConnectionState":{"status":"Approved"}}`, http.StatusNotFound)
}
