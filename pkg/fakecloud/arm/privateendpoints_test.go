// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// A private endpoint makes a connection; a PUT changes one that exists and
// cannot make one.
func TestPrivateEndpointConnections(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/pec"
	a.AddResource(rg)
	approve := `{"properties":{"privateLinkServiceConnectionState":{"status":"Approved","description":"ok"}}}`
	for _, parent := range []string{
		rg + "/providers/Microsoft.Sql/servers/sql",
		rg + "/providers/Microsoft.App/containerApps/app",
		rg + "/providers/Microsoft.Compute/diskAccesses/disks",
		rg + "/providers/Microsoft.Cache/redis/cache",
		rg + "/providers/Microsoft.Cache/redisEnterprise/managed",
		rg + "/providers/Microsoft.AppConfiguration/configurationStores/config",
		rg + "/providers/Microsoft.DBforPostgreSQL/flexibleServers/pg",
		rg + "/providers/Microsoft.DocumentDB/mongoClusters/mongo",
	} {
		a.AddResource(parent)
		conn := parent + "/privateEndpointConnections/pe-1"
		code, resp := do(t, a, http.MethodPut, conn, approve)
		expect(t, "a connection no private endpoint made under "+parent, code, resp, http.StatusNotFound)
		a.AddResource(conn)
		code, resp = do(t, a, http.MethodPut, conn, approve)
		expect(t, "approving the connection under "+parent, code, resp, http.StatusOK)
	}
	// Another service's connections are not this rule's.
	other := rg + "/providers/Microsoft.Web/sites/web/privateEndpointConnections/pe-1"
	a.AddResource(rg + "/providers/Microsoft.Web/sites/web")
	code, resp := do(t, a, http.MethodPut, other, approve)
	expect(t, "a site's connection", code, resp, http.StatusCreated)
}
