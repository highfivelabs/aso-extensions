// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
)

// Azure Cosmos DB fleets (Microsoft.DocumentDB/fleets, 2026-03-15) and a
// MongoDB vCore cluster's Microsoft Entra users (mongoClusters/users,
// 2026-06-01; docs/plan.md f47), as far as the fake models them. Each rule
// is one the specs or Azure's documentation of them state:
//
//   - a fleetspace's throughput pool has its minimum at or below its
//     maximum (400);
//   - a fleetspace account names a Cosmos DB account
//     (globalDatabaseAccountProperties.resourceId), which must be one (400)
//     and exist (404, as for any missing reference);
//   - a cluster user is a Microsoft Entra principal, named by its object ID
//     (a GUID; 400), with an identity provider (400).
//
// Not modelled: throughput being pooled, an account's regions matching the
// fleetspace's, an account belonging to one fleetspace only, the principal
// existing, the user's role being granted in the cluster, and the error
// codes (the fake's own).
var (
	cosmosFleetspace        = regexp.MustCompile(`(?i)/providers/microsoft\.documentdb/fleets/[^/]+/fleetspaces/[^/]+$`)
	cosmosFleetspaceAccount = regexp.MustCompile(`(?i)/providers/microsoft\.documentdb/fleets/[^/]+/fleetspaces/[^/]+/fleetspaceaccounts/[^/]+$`)
	cosmosAccountID         = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.documentdb/databaseaccounts/[^/]+$`)
	mongoClusterUser        = regexp.MustCompile(`(?i)/providers/microsoft\.documentdb/mongoclusters/[^/]+/users/([^/]+)$`)
)

// checkCosmosFleet refuses what the rules above do not allow; code is ""
// when it is allowed or id is none of these.
func (a *ARM) checkCosmosFleet(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	switch {
	case cosmosFleetspace.MatchString(id):
		pool, _ := props["throughputPoolConfiguration"].(map[string]any)
		lo, okLo := pool["minThroughput"].(float64)
		hi, okHi := pool["maxThroughput"].(float64)
		if okLo && okHi && lo > hi {
			return http.StatusBadRequest, "BadRequest", "A fleetspace's minimum throughput is above its maximum."
		}
	case cosmosFleetspaceAccount.MatchString(id):
		account, _ := props["globalDatabaseAccountProperties"].(map[string]any)
		target, _ := account["resourceId"].(string)
		if !cosmosAccountID.MatchString(target) {
			return http.StatusBadRequest, "BadRequest", "A fleetspace account names a Cosmos DB account, not " + target + "."
		}
		if !a.exists(target) {
			return http.StatusNotFound, "ResourceNotFound", "The Cosmos DB account " + target + " was not found."
		}
	case mongoClusterUser.MatchString(id):
		if name := mongoClusterUser.FindStringSubmatch(id)[1]; !guid.MatchString(name) {
			return http.StatusBadRequest, "BadRequest", "A cluster user is named by its Microsoft Entra object ID, not " + name + "."
		}
		if provider, _ := props["identityProvider"].(map[string]any); provider["type"] == nil {
			return http.StatusBadRequest, "BadRequest", "A cluster user needs an identity provider."
		}
	}
	return 0, "", ""
}
