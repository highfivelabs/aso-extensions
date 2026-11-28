// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-service-operator/v2/pkg/common/annotations"

	asosql "github.com/Azure/azure-service-operator/v2/api/sql/v20250101/storage"

	sqlarm "github.com/highfivelabs/aso-extensions/apis/arm/sql/v20250101"
)

const sqlAPI = "2025-01-01"

// An Azure SQL server's Microsoft support operations auditing (to Azure
// Monitor; docs/plan.md f43), a database's dynamic data masking and the
// server's express vulnerability assessment (c20), on a server fixture (free)
// and a Basic database (about $0.007 an hour) in the first region that takes
// them. Each is turned on, changed or turned off, and detached: Azure has no
// DELETE for these singletons, which go with the server. Maintenance
// windows, workload groups and ledger digest uploads are not run live: a
// Basic database has none of them (they need a vCore or DataWarehouse
// database, or an Azure Confidential Ledger).
func TestLiveSqlServerExtras(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.Sql")
	ns := namespace(t, "sqlextras")
	var cands []candidate
	for i, region := range regions() {
		cands = append(cands, candidate{
			id: rgID() + "/providers/Microsoft.Sql/servers/" + azName(fmt.Sprintf("sqlx%d", i)),
			body: map[string]any{
				"location": region,
				"properties": map[string]any{
					"administratorLogin": "liveadmin", "administratorLoginPassword": password(), "version": "12.0",
					"minimalTlsVersion": "1.2", "publicNetworkAccess": "Disabled",
				},
			},
		})
	}
	i, _ := fixtureFirst(t, 20*time.Minute, "2023-08-01", cands)
	serverID, sqlRegion := cands[i].id, regions()[i]
	serverName := serverID[strings.LastIndex(serverID, "/")+1:]
	t.Logf("Azure SQL server in %s", sqlRegion)
	dbID := serverID + "/databases/livedb"
	fixtureWait(t, 20*time.Minute, dbID, "2023-08-01", map[string]any{
		"location": sqlRegion, "sku": map[string]any{"name": "Basic", "tier": "Basic"},
	})
	server := &asosql.Server{}
	server.Namespace, server.Name = ns, "server"
	server.Spec.AzureName, server.Spec.Owner = serverName, owner("rg")
	standIn(t, server, serverID, nil)
	db := &asosql.ServersDatabase{}
	db.Namespace, db.Name = ns, "appdb"
	db.Spec.AzureName, db.Spec.Owner = "livedb", owner("server")
	standIn(t, db, dbID, nil)

	audit := &sqlarm.ServersDevOpsAuditingSetting{}
	audit.Namespace, audit.Name = ns, "devops-audit"
	audit.Spec = sqlarm.ServersDevOpsAuditingSetting_Spec{
		Owner: owner("server"), State: ptr(sqlarm.BlobAuditingPolicyState("Enabled")), IsAzureMonitorTargetEnabled: ptr(true),
	}
	masking := &sqlarm.ServersDatabasesDataMaskingPolicy{}
	masking.Namespace, masking.Name = ns, "masking"
	masking.Spec = sqlarm.ServersDatabasesDataMaskingPolicy_Spec{
		Owner: owner("appdb"), DataMaskingState: ptr(sqlarm.DataMaskingState("Enabled")), ExemptPrincipals: ptr("liveadmin"),
	}
	assessment := &sqlarm.ServersSqlVulnerabilityAssessment{}
	assessment.Namespace, assessment.Name = ns, "va"
	assessment.Spec = sqlarm.ServersSqlVulnerabilityAssessment_Spec{
		Owner: owner("server"), State: ptr(sqlarm.SqlVulnerabilityAssessmentState("Enabled")),
	}
	create(t, timeout, audit, masking, assessment)
	expectResource(t, serverID+"/devOpsAuditingSettings/Default", sqlAPI, func(r map[string]any) error {
		if p := properties(r); p["state"] != "Enabled" || p["isAzureMonitorTargetEnabled"] != true {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, dbID+"/dataMaskingPolicies/Default", sqlAPI, func(r map[string]any) error {
		if p := properties(r); p["dataMaskingState"] != "Enabled" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, serverID+"/sqlVulnerabilityAssessments/default", sqlAPI, func(r map[string]any) error {
		if p := properties(r); p["state"] != "Enabled" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	update(t, audit, func(a *sqlarm.ServersDevOpsAuditingSetting) {
		a.Spec.State = ptr(sqlarm.BlobAuditingPolicyState("Disabled"))
	})
	eventually(t, timeout, "ServersDevOpsAuditingSetting Ready after the change", ready(audit))
	expectResource(t, serverID+"/devOpsAuditingSettings/Default", sqlAPI, func(r map[string]any) error {
		if properties(r)["state"] != "Disabled" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, masking, func(m *sqlarm.ServersDatabasesDataMaskingPolicy) {
		m.Spec.DataMaskingState = ptr(sqlarm.DataMaskingState("Disabled"))
	})
	eventually(t, timeout, "ServersDatabasesDataMaskingPolicy Ready after the change", ready(masking))

	// The assessment has a DELETE (it turns the assessment off); the others
	// are detached and go with the server.
	remove(t, timeout, assessment)
	for _, o := range []conditioned{audit, masking} {
		setReconcilePolicy(t, o, annotations.ReconcilePolicyDetachOnDelete)
		remove(t, timeout, o)
	}
	expectResource(t, serverID+"/devOpsAuditingSettings/Default", sqlAPI, func(r map[string]any) error { return nil })
}
