// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// A new Cosmos DB for PostgreSQL cluster has what creation needs and never
// one node; a replica's source exists; firewall rules take IPv4 addresses;
// a role has a password and a configuration a value.
func TestCosmosPostgres(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/data"
	a.AddResource(rg)
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	cluster := rg + "/providers/Microsoft.DBforPostgreSQL/serverGroupsv2/citus"
	full := `"administratorLoginPassword":"p","coordinatorServerEdition":"GeneralPurpose","coordinatorStorageQuotaInMb":131072,"coordinatorVCores":2`
	put("a cluster with no node count", cluster, `{"location":"westus","properties":{`+full+`}}`, http.StatusBadRequest)
	put("a cluster of one node", cluster, `{"location":"westus","properties":{`+full+`,"nodeCount":1}}`, http.StatusBadRequest)
	put("a cluster with no password", cluster, `{"location":"westus","properties":{"coordinatorServerEdition":"GeneralPurpose","coordinatorStorageQuotaInMb":131072,"coordinatorVCores":2,"nodeCount":0}}`, http.StatusBadRequest)
	put("a single-node cluster", cluster, `{"location":"westus","properties":{`+full+`,"nodeCount":0}}`, http.StatusCreated)
	put("a change without the password", cluster, `{"location":"westus","properties":{"coordinatorVCores":4}}`, http.StatusOK)
	replica := rg + "/providers/Microsoft.DBforPostgreSQL/serverGroupsv2/replica"
	put("a replica of a missing cluster", replica, `{"location":"eastus","properties":{"sourceResourceId":"`+rg+`/providers/Microsoft.DBforPostgreSQL/serverGroupsv2/none","sourceLocation":"westus"}}`, http.StatusNotFound)
	put("a replica", replica, `{"location":"eastus","properties":{"sourceResourceId":"`+cluster+`","sourceLocation":"westus"}}`, http.StatusCreated)
	put("a firewall rule with an IPv6 address", cluster+"/firewallRules/office", `{"properties":{"startIpAddress":"10.0.0.1","endIpAddress":"2001:db8::1"}}`, http.StatusBadRequest)
	put("a firewall rule", cluster+"/firewallRules/office", `{"properties":{"startIpAddress":"10.0.0.1","endIpAddress":"10.0.0.255"}}`, http.StatusCreated)
	put("a role without a password", cluster+"/roles/app", `{"properties":{}}`, http.StatusBadRequest)
	put("a role", cluster+"/roles/app", `{"properties":{"password":"p"}}`, http.StatusCreated)
	put("a configuration without a value", cluster+"/coordinatorConfigurations/work_mem", `{"properties":{}}`, http.StatusBadRequest)
	put("a node configuration", cluster+"/nodeConfigurations/work_mem", `{"properties":{"value":"16384"}}`, http.StatusCreated)
	put("a connection no private endpoint made", cluster+"/privateEndpointConnections/pe", `{"properties":{"privateLinkServiceConnectionState":{"status":"Approved"}}}`, http.StatusNotFound)
}
