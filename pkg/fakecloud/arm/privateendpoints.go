// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
)

// The private endpoint connections of docs/plan.md f37, f38, f40 to f42 (an
// Azure SQL server's, a container app's, a disk access's, an Azure Cache for
// Redis cache's, a Redis Enterprise cluster's, an App Configuration store's,
// a PostgreSQL flexible server's, an Azure Cosmos DB for PostgreSQL
// cluster's (f46), a MongoDB vCore cluster's, an App Service
// app's and slot's, a static web app's, an App Service Environment's, a
// SQL managed instance's, a Service Bus namespace's (f61) and an Event Hubs
// namespace's (f62)), as far as the fake models them: a private endpoint makes a connection, and a PUT
// only changes one that exists
// (approves, rejects or describes it), as the specs' descriptions say
// ("Approve or reject a private endpoint connection"). A PUT for a
// connection that does not exist is 404, as for Key Vault's (keyvault.go).
// Not modelled: the private endpoint itself, the states Azure allows a move
// between, and the error codes (the fake's own).
var privateEndpointConnection = regexp.MustCompile(`(?i)/providers/(microsoft\.sql/servers|microsoft\.app/containerapps|microsoft\.compute/diskaccesses|microsoft\.cache/redis|microsoft\.cache/redisenterprise|microsoft\.appconfiguration/configurationstores|microsoft\.dbforpostgresql/flexibleservers|microsoft\.dbforpostgresql/servergroupsv2|microsoft\.documentdb/mongoclusters|microsoft\.web/sites|microsoft\.web/sites/[^/]+/slots|microsoft\.web/staticsites|microsoft\.web/hostingenvironments|microsoft\.sql/managedinstances|microsoft\.servicebus/namespaces|microsoft\.eventhub/namespaces)/[^/]+/privateendpointconnections/[^/]+$`)

// checkPrivateEndpointConnections refuses to make a connection; code is ""
// when the connection exists or id is none of these.
func (a *ARM) checkPrivateEndpointConnections(id string) (status int, code, message string) {
	if privateEndpointConnection.MatchString(id) && !a.exists(id) {
		return http.StatusNotFound, "PrivateEndpointConnectionNotFound",
			"The private endpoint connection " + id + " was not found; a private endpoint makes it."
	}
	return 0, "", ""
}
