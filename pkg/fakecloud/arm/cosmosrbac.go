// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Azure Cosmos DB role-based access for the Cassandra, Gremlin, MongoDB
// (Entra) and Table APIs (Microsoft.DocumentDB, API 2026-03-15), as far as
// the fake models it. Every rule is one the spec or Azure's documentation of
// the SQL API's role assignments, which these follow, states:
//
//   - a role definition's or role assignment's name is a GUID (400);
//   - a role assignment's roleDefinitionId names a role definition of the
//     same API in the same account, which must exist (404; the built-in
//     definitions are not faked), and its scope is the account or a path
//     under it (400).
//
// Not modelled: built-in role definitions, permissions being enforced on
// the data plane, the principal existing, assignable scopes checked against
// the assignment's scope, and the error codes (the fake's own).
var cosmosRBAC = regexp.MustCompile(`(?i)^(.*/providers/microsoft\.documentdb/databaseaccounts/[^/]+)/(cassandra|gremlin|mongomi|table)role(definitions|assignments)/([^/]+)$`)

// checkCosmosRBAC refuses a role definition or assignment the rules above
// do not allow; code is "" when it is allowed or id is neither.
func (a *ARM) checkCosmosRBAC(id string, body map[string]any) (status int, code, message string) {
	m := cosmosRBAC.FindStringSubmatch(id)
	if m == nil {
		return 0, "", ""
	}
	account, api, what, name := m[1], m[2], m[3], m[4]
	if !guid.MatchString(name) {
		return http.StatusBadRequest, "BadRequest", "A Cosmos DB role definition's or assignment's name is a GUID, not " + name + "."
	}
	if strings.EqualFold(what, "definitions") {
		return 0, "", ""
	}
	props, _ := body["properties"].(map[string]any)
	definition, _ := props["roleDefinitionId"].(string)
	prefix := strings.ToLower(account + "/" + api + "RoleDefinitions/")
	if !strings.HasPrefix(strings.ToLower(definition), prefix) {
		return http.StatusBadRequest, "BadRequest", "A " + api + " role assignment names a " + api + " role definition of its account, not " + definition + "."
	}
	if !a.exists(definition) {
		return http.StatusNotFound, "NotFound", "The role definition " + definition + " was not found."
	}
	scope, _ := props["scope"].(string)
	if !strings.HasPrefix(strings.ToLower(scope), strings.ToLower(account)) && !strings.HasPrefix(scope, "/dbs/") && scope != "/" {
		return http.StatusBadRequest, "BadRequest", "A role assignment's scope is its account or a path under it, not " + scope + "."
	}
	return 0, "", ""
}
