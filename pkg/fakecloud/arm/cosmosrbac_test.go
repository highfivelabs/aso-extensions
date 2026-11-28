// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// Cosmos DB role definitions and assignments: GUID names, the definition an
// assignment names, its scope.
func TestCosmosRoleBasedAccess(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/data"
	a.AddResource(rg)
	account := rg + "/providers/Microsoft.DocumentDB/databaseAccounts/cosmos"
	a.AddResource(account)
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	const def, asg = "6b4a2d3c-1e5f-4a7b-8c9d-0e1f2a3b4c5d", "7c5b3e4d-2f6a-4b8c-9d0e-1f2a3b4c5d6e"
	definition := account + "/tableRoleDefinitions/" + def
	assignment := account + "/tableRoleAssignments/" + asg
	put("a definition named by a word", account+"/tableRoleDefinitions/reader", `{"properties":{"roleName":"r"}}`, http.StatusBadRequest)
	put("an assignment of a missing definition", assignment, `{"properties":{"roleDefinitionId":"`+definition+`","scope":"`+account+`","principalId":"p"}}`, http.StatusNotFound)
	put("a definition", definition, `{"properties":{"roleName":"reader","type":"CustomRole","assignableScopes":["`+account+`"],"permissions":[{"dataActions":["Microsoft.DocumentDB/databaseAccounts/tables/containers/entities/read"]}]}}`, http.StatusCreated)
	put("an assignment of another API's definition", assignment, `{"properties":{"roleDefinitionId":"`+account+`/gremlinRoleDefinitions/`+def+`","scope":"`+account+`","principalId":"p"}}`, http.StatusBadRequest)
	put("an assignment scoped elsewhere", assignment, `{"properties":{"roleDefinitionId":"`+definition+`","scope":"`+rg+`","principalId":"p"}}`, http.StatusBadRequest)
	put("an assignment", assignment, `{"properties":{"roleDefinitionId":"`+definition+`","scope":"`+account+`","principalId":"p"}}`, http.StatusCreated)
}
