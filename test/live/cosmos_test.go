// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

//go:build live

package live

import (
	"fmt"
	"testing"
	"time"

	asocosmos "github.com/Azure/azure-service-operator/v2/api/documentdb/v1api20240815/storage"

	cosmos "github.com/highfivelabs/aso-extensions/apis/arm/documentdb/v20240815"
)

const cosmosAPI = "2024-08-15"

// A Gremlin database and graph on a serverless Cosmos DB account fixture
// (billed per request and per GB stored: nothing while idle). The account is
// made in the first region that takes it (Cosmos DB refuses new accounts in
// busy regions).
func TestLiveCosmosGremlin(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.DocumentDB")
	ns := namespace(t, "cosmos")
	var cands []candidate
	for i, r := range regions() {
		cands = append(cands, candidate{
			id: rgID() + "/providers/Microsoft.DocumentDB/databaseAccounts/" + azName(fmt.Sprintf("gremlin%d", i)),
			body: map[string]any{
				"location": r, "kind": "GlobalDocumentDB",
				"properties": map[string]any{
					"databaseAccountOfferType": "Standard",
					"locations":                []any{map[string]any{"locationName": r, "failoverPriority": 0}},
					"capabilities":             []any{map[string]any{"name": "EnableGremlin"}, map[string]any{"name": "EnableServerless"}},
				},
			},
		})
	}
	i, _ := fixtureFirst(t, 30*time.Minute, cosmosAPI, cands)
	accountID := cands[i].id
	t.Logf("Cosmos DB account in %v", cands[i].body["location"])
	account := &asocosmos.DatabaseAccount{}
	account.Namespace, account.Name = ns, "account"
	account.Spec.AzureName, account.Spec.Owner = accountID[len(rgID()+"/providers/Microsoft.DocumentDB/databaseAccounts/"):], owner("rg")
	standIn(t, account, accountID, nil)

	db := &cosmos.DatabaseAccountsGremlinDatabase{}
	db.Namespace, db.Name = ns, "graphs"
	db.Spec = cosmos.DatabaseAccountsGremlinDatabase_Spec{
		AzureName: "graphs", Owner: owner("account"),
		Resource: &cosmos.GremlinDatabaseResource{Id: ptr("graphs")},
	}
	create(t, timeout, db)
	dbID := accountID + "/gremlinDatabases/graphs"
	expectResource(t, dbID, cosmosAPI, func(r map[string]any) error {
		if res, _ := properties(r)["resource"].(map[string]any); res["id"] != "graphs" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	graph := &cosmos.DatabaseAccountsGremlinDatabasesGraph{}
	graph.Namespace, graph.Name = ns, "people"
	graph.Spec = cosmos.DatabaseAccountsGremlinDatabasesGraph_Spec{
		AzureName: "people", Owner: owner("graphs"),
		Resource: &cosmos.GremlinGraphResource{
			Id:           ptr("people"),
			PartitionKey: &cosmos.ContainerPartitionKey{Paths: []string{"/team"}, Kind: ptr(cosmos.ContainerPartitionKey_Kind("Hash"))},
			DefaultTtl:   ptr(-1),
		},
	}
	create(t, timeout, graph)
	graphID := dbID + "/graphs/people"
	expectResource(t, graphID, cosmosAPI, func(r map[string]any) error {
		res, _ := properties(r)["resource"].(map[string]any)
		if pk, _ := res["partitionKey"].(map[string]any); res["id"] != "people" || fmt.Sprint(pk["paths"]) != "[/team]" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, graph, func(g *cosmos.DatabaseAccountsGremlinDatabasesGraph) { g.Spec.Resource.DefaultTtl = ptr(3600) })
	eventually(t, timeout, "graph Ready after the change", ready(graph))
	expectResource(t, graphID, cosmosAPI, func(r map[string]any) error {
		if res, _ := properties(r)["resource"].(map[string]any); fmt.Sprint(res["defaultTtl"]) != "3600" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	remove(t, timeout, graph)
	expectGone(t, graphID, cosmosAPI)
	remove(t, timeout, db)
	expectGone(t, dbID, cosmosAPI)
}
