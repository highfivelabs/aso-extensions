// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

//go:build live

package live

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	asoworkspace "github.com/Azure/azure-service-operator/v2/api/operationalinsights/v20250701/storage"

	insights "github.com/highfivelabs/aso-extensions/apis/arm/insights/v20210901"
	oi "github.com/highfivelabs/aso-extensions/apis/arm/operationalinsights/v20250701"
)

const (
	oiAPI       = "2025-07-01"
	insightsAPI = "2021-09-01"
)

// workspaceStandIn makes a Log Analytics workspace fixture and the ASO
// Workspace "law" standing for it in ns; it returns the workspace's ID.
func workspaceStandIn(t *testing.T, ns string) string {
	t.Helper()
	id := logAnalyticsWorkspace(t, "law")
	ws := &asoworkspace.Workspace{}
	ws.Namespace, ws.Name = ns, "law"
	ws.Spec.AzureName = id[strings.LastIndex(id, "/")+1:]
	ws.Spec.Owner = owner("rg")
	standIn(t, ws, id, nil)
	return id
}

// A query pack and a query in it (free).
func TestLiveQueryPack(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.OperationalInsights")
	ns := namespace(t, "qpack")
	pack := &oi.QueryPack{}
	pack.Namespace, pack.Name = ns, "ops"
	pack.Spec = oi.QueryPack_Spec{AzureName: azName("ops"), Owner: owner("rg"), Location: ptr(location), Tags: map[string]string{"team": "platform"}}
	const queryName = "6a0f1e1c-2a53-4a4e-9c3f-4d0a4f1b8a01"
	query := &oi.QueryPacksQuery{}
	query.Namespace, query.Name = ns, "slow-requests"
	query.Spec = oi.QueryPacksQuery_Spec{
		AzureName: queryName, Owner: owner("ops"), DisplayName: ptr("Slow requests"),
		Body:    ptr("requests | where duration > 5s"),
		Related: &oi.LogAnalyticsQueryPackQueryPropertiesRelated{Categories: []string{"applications"}},
	}
	create(t, timeout, pack, query)
	packID := rgID() + "/providers/Microsoft.OperationalInsights/queryPacks/" + pack.Spec.AzureName
	queryID := packID + "/queries/" + queryName
	expectResource(t, packID, oiAPI, func(r map[string]any) error {
		if tags, _ := r["tags"].(map[string]any); tags["team"] != "platform" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, queryID, oiAPI, func(r map[string]any) error {
		if p := properties(r); p["displayName"] != "Slow requests" || p["body"] != "requests | where duration > 5s" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, query, func(q *oi.QueryPacksQuery) { q.Spec.Body = ptr("requests | where duration > 10s") })
	eventually(t, timeout, "QueryPacksQuery Ready after the change", ready(query))
	expectResource(t, queryID, oiAPI, func(r map[string]any) error {
		if properties(r)["body"] != "requests | where duration > 10s" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, query)
	expectGone(t, queryID, oiAPI)
	remove(t, timeout, pack)
	expectGone(t, packID, oiAPI)
}

// A workspace's custom table, saved search, linked storage account, data
// export and linked service (an Automation account), on a workspace fixture
// with nothing ingested (free; storage cents).
func TestLiveWorkspaceChildren(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.OperationalInsights")
	ns := namespace(t, "lawkids")
	wsID := workspaceStandIn(t, ns)
	storageID, _ := storageAccount(t, "lawst")

	table := &oi.WorkspacesTable{}
	table.Namespace, table.Name = ns, "appevents"
	table.Spec = oi.WorkspacesTable_Spec{
		AzureName: "AppEvents_CL", Owner: owner("law"), Plan: ptr(oi.TablePlanEnum_Analytics),
		RetentionInDays: ptr(30), TotalRetentionInDays: ptr(180),
		Schema: &oi.Schema{Name: ptr("AppEvents_CL"), Columns: []oi.Column{
			{Name: ptr("TimeGenerated"), Type: ptr(oi.ColumnTypeEnum_DateTime)},
			{Name: ptr("Message"), Type: ptr(oi.ColumnTypeEnum_String)},
		}},
	}
	search := &oi.WorkspacesSavedSearch{}
	search.Namespace, search.Name = ns, "errors"
	search.Spec = oi.WorkspacesSavedSearch_Spec{
		AzureName: "app-errors", Owner: owner("law"), Category: ptr("Application"), DisplayName: ptr("Application errors"),
		Query: ptr("AppEvents_CL | where Message contains 'error'"), Version: ptr(2),
		Tags: []oi.Tag{{Name: ptr("team"), Value: ptr("platform")}},
	}
	linkedStorage := &oi.WorkspacesLinkedStorageAccount{}
	linkedStorage.Namespace, linkedStorage.Name = ns, "customlogs"
	linkedStorage.Spec = oi.WorkspacesLinkedStorageAccount_Spec{
		AzureName: "CustomLogs", Owner: owner("law"), StorageAccountReferences: []genruntime.ResourceReference{{ARMID: storageID}},
	}
	export := &oi.WorkspacesDataExport{}
	export.Namespace, export.Name = ns, "archive"
	export.Spec = oi.WorkspacesDataExport_Spec{
		AzureName: "archive", Owner: owner("law"), TableNames: []string{"AppEvents_CL"}, Enable: ptr(true),
		Destination: &oi.Destination{ResourceReference: &genruntime.ResourceReference{ARMID: storageID}},
	}
	create(t, timeout, table, search, linkedStorage, export)

	tableID := wsID + "/tables/AppEvents_CL"
	expectResource(t, tableID, oiAPI, func(r map[string]any) error {
		p := properties(r)
		if p["plan"] != "Analytics" || p["retentionInDays"] != float64(30) || p["totalRetentionInDays"] != float64(180) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, wsID+"/savedSearches/app-errors", oiAPI, func(r map[string]any) error {
		if p := properties(r); p["category"] != "Application" || p["query"] != "AppEvents_CL | where Message contains 'error'" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, wsID+"/linkedStorageAccounts/CustomLogs", oiAPI, func(r map[string]any) error {
		ids, _ := properties(r)["storageAccountIds"].([]any)
		if len(ids) != 1 || !strings.EqualFold(fmt.Sprint(ids[0]), storageID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, wsID+"/dataExports/archive", oiAPI, func(r map[string]any) error {
		p := properties(r)
		dest, _ := p["destination"].(map[string]any)
		if p["enable"] != true || !reflect.DeepEqual(p["tableNames"], []any{"AppEvents_CL"}) || !strings.EqualFold(fmt.Sprint(dest["resourceId"]), storageID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	update(t, table, func(tb *oi.WorkspacesTable) { tb.Spec.RetentionInDays = ptr(90) })
	eventually(t, timeout, "WorkspacesTable Ready after the change", ready(table))
	expectResource(t, tableID, oiAPI, func(r map[string]any) error {
		if properties(r)["retentionInDays"] != float64(90) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, export, func(e *oi.WorkspacesDataExport) { e.Spec.Enable = ptr(false) })
	eventually(t, timeout, "WorkspacesDataExport Ready after the change", ready(export))
	expectResource(t, wsID+"/dataExports/archive", oiAPI, func(r map[string]any) error {
		if properties(r)["enable"] != false {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	t.Run("LinkedService", func(t *testing.T) {
		requireProvider(t, "Microsoft.Automation")
		autoID := rgID() + "/providers/Microsoft.Automation/automationAccounts/" + azName("auto")
		fixture(t, autoID, "2023-11-01", map[string]any{
			"location": location, "properties": map[string]any{"sku": map[string]any{"name": "Basic"}},
		})
		linked := &oi.WorkspacesLinkedService{}
		linked.Namespace, linked.Name = ns, "automation"
		linked.Spec = oi.WorkspacesLinkedService_Spec{
			AzureName: "Automation", Owner: owner("law"), ResourceReference: &genruntime.ResourceReference{ARMID: autoID},
		}
		create(t, timeout, linked)
		id := wsID + "/linkedServices/Automation"
		expectResource(t, id, oiAPI, func(r map[string]any) error {
			if !strings.EqualFold(fmt.Sprint(properties(r)["resourceId"]), autoID) {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})
		remove(t, timeout, linked)
		expectGone(t, id, oiAPI)
	})

	for _, o := range []conditioned{export, linkedStorage, search, table} {
		remove(t, timeout, o)
	}
	for _, id := range []string{wsID + "/dataExports/archive", wsID + "/linkedStorageAccounts/CustomLogs", wsID + "/savedSearches/app-errors"} {
		expectGone(t, id, oiAPI)
	}
	// A deleted table is kept, soft-deleted, while its retention runs; Azure
	// answers 404 once the delete is accepted.
	expectGone(t, tableID, oiAPI)
}

// An Azure Monitor private link scope (free) with the workspace in it, and
// the scope's private endpoint connection approved by our kind (the endpoint
// a fixture, about $0.01 an hour).
func TestLiveMonitorPrivateLinkScope(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.Insights")
	requireProvider(t, "Microsoft.OperationalInsights")
	ns := namespace(t, "ampls")
	wsID := workspaceStandIn(t, ns)

	scope := &insights.PrivateLinkScope{}
	scope.Namespace, scope.Name = ns, "ampls"
	scope.Spec = insights.PrivateLinkScope_Spec{
		AzureName: azName("ampls"), Owner: owner("rg"), Location: ptr("global"),
		AccessModeSettings: &insights.AccessModeSettings{
			QueryAccessMode: ptr(insights.AccessMode_PrivateOnly), IngestionAccessMode: ptr(insights.AccessMode_PrivateOnly),
		},
	}
	scoped := &insights.PrivateLinkScopesScopedResource{}
	scoped.Namespace, scoped.Name = ns, "law"
	scoped.Spec = insights.PrivateLinkScopesScopedResource_Spec{
		AzureName: "law", Owner: owner("ampls"), LinkedResourceReference: &genruntime.ResourceReference{ARMID: wsID},
	}
	create(t, timeout, scope, scoped)
	scopeID := rgID() + "/providers/Microsoft.Insights/privateLinkScopes/" + scope.Spec.AzureName
	expectResource(t, scopeID, insightsAPI, func(r map[string]any) error {
		modes, _ := properties(r)["accessModeSettings"].(map[string]any)
		if modes["queryAccessMode"] != "PrivateOnly" || modes["ingestionAccessMode"] != "PrivateOnly" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, scopeID+"/scopedResources/law", insightsAPI, func(r map[string]any) error {
		if !strings.EqualFold(fmt.Sprint(properties(r)["linkedResourceId"]), wsID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, scope, func(s *insights.PrivateLinkScope) {
		s.Spec.AccessModeSettings.QueryAccessMode = ptr(insights.AccessMode_Open)
	})
	eventually(t, timeout, "PrivateLinkScope Ready after the change", ready(scope))
	expectResource(t, scopeID, insightsAPI, func(r map[string]any) error {
		if modes, _ := properties(r)["accessModeSettings"].(map[string]any); modes["queryAccessMode"] != "Open" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	peID := privateEndpoint(t, "amplspe", "10.81.0.0/16", scopeID, "azuremonitor")
	connName := pendingConnection(t, scopeID, insightsAPI, peID)
	conn := &insights.PrivateLinkScopesPrivateEndpointConnection{}
	conn.Namespace, conn.Name = ns, "pe"
	conn.Spec = insights.PrivateLinkScopesPrivateEndpointConnection_Spec{
		AzureName: connName, Owner: owner("ampls"),
		PrivateLinkServiceConnectionState: &insights.PrivateLinkServiceConnectionState{
			Status: ptr(insights.PrivateEndpointServiceConnectionStatus_Approved), Description: ptr("approved by the live test"),
		},
	}
	create(t, timeout, conn)
	connID := scopeID + "/privateEndpointConnections/" + connName
	expectResource(t, connID, insightsAPI, func(r map[string]any) error {
		st, _ := properties(r)["privateLinkServiceConnectionState"].(map[string]any)
		if st["status"] != "Approved" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, conn)
	expectGone(t, connID, insightsAPI)
}
