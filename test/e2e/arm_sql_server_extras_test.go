// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	asosql "github.com/Azure/azure-service-operator/v2/api/sql/v20250101/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	sql "github.com/highfivelabs/aso-extensions/apis/arm/sql/v20250101"
)

// The Azure SQL server and database kinds of docs/plan.md f43 on ASO's
// Server and ServersDatabase stand-ins: Microsoft support operations
// auditing (its storage key from a Secret, never in the status), dynamic
// data masking, a maintenance window, ledger digest uploads, a workload
// group with a classifier, and the express vulnerability assessment's
// baseline and one rule's baseline under our ServersSqlVulnerabilityAssessment.
// A workload group whose minimum is above its cap is refused; a change is
// PUT again; the objects are deleted, the singletons Azure cannot delete
// detached.
func TestSQLServerAuditingAndWorkloads(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "sqlextras")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	serverID := rgID + "/providers/Microsoft.Sql/servers/sql-" + ns
	dbID := serverID + "/databases/dw"
	for _, id := range []string{rgID, serverID, dbID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	srv := &asosql.Server{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "sql"}}
	srv.Spec.AzureName, srv.Spec.Owner = "sql-"+ns, &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, srv, serverID)
	db := &asosql.ServersDatabase{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "dw"}}
	db.Spec.AzureName, db.Spec.Owner = "dw", &genruntime.KnownResourceReference{Name: "sql"}
	asoStandIn(t, db, dbID)
	secret(t, ns, "audit", map[string]string{"key": "audit-storage-key-1"})
	onServer := &genruntime.KnownResourceReference{Name: "sql"}
	onDB := &genruntime.KnownResourceReference{Name: "dw"}
	detach := map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}

	audit := &sql.ServersDevOpsAuditingSetting{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "devops-audit", Annotations: detach},
		Spec: sql.ServersDevOpsAuditingSetting_Spec{
			Owner: onServer, State: ptr(sql.BlobAuditingPolicyState("Enabled")),
			StorageEndpoint:         ptr("https://contosoaudit.blob.core.windows.net"),
			StorageAccountAccessKey: &genruntime.SecretReference{Name: "audit", Key: "key"},
		},
	}
	masking := &sql.ServersDatabasesDataMaskingPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "masking", Annotations: detach},
		Spec: sql.ServersDatabasesDataMaskingPolicy_Spec{
			Owner: onDB, DataMaskingState: ptr(sql.DataMaskingState("Enabled")), ExemptPrincipals: ptr("dbo;auditor"),
		},
	}
	window := &sql.ServersDatabasesMaintenanceWindow{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "window", Annotations: detach},
		Spec: sql.ServersDatabasesMaintenanceWindow_Spec{
			Owner:      onDB,
			TimeRanges: []sql.MaintenanceWindowTimeRange{{DayOfWeek: ptr(sql.DayOfWeek("Sunday")), StartTime: ptr("PT2H"), Duration: ptr("PT4H")}},
		},
	}
	ledger := &sql.ServersDatabasesLedgerDigestUpload{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "ledger", Annotations: detach},
		Spec:       sql.ServersDatabasesLedgerDigestUpload_Spec{Owner: onDB, DigestStorageEndpoint: ptr("https://contosoledger.confidential-ledger.azure.com")},
	}
	group := &sql.ServersDatabasesWorkloadGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "etl"},
		Spec: sql.ServersDatabasesWorkloadGroup_Spec{
			AzureName: "etl", Owner: onDB, MinResourcePercent: ptr(10), MaxResourcePercent: ptr(50),
			MinResourcePercentPerRequest: ptr(5.0), Importance: ptr("normal"),
		},
	}
	classifier := &sql.ServersDatabasesWorkloadGroupsWorkloadClassifier{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "loader"},
		Spec: sql.ServersDatabasesWorkloadGroupsWorkloadClassifier_Spec{
			AzureName: "loader", Owner: &genruntime.KnownResourceReference{Name: "etl"}, MemberName: ptr("etl_loader"), Importance: ptr("high"),
		},
	}
	assessment := &sql.ServersSqlVulnerabilityAssessment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "va"},
		Spec:       sql.ServersSqlVulnerabilityAssessment_Spec{Owner: onServer, State: ptr(sql.SqlVulnerabilityAssessmentState("Enabled"))},
	}
	baseline := &sql.ServersSqlVulnerabilityAssessmentsBaseline{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "baseline", Annotations: detach},
		Spec: sql.ServersSqlVulnerabilityAssessmentsBaseline_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "va"}, LatestScan: ptr(false),
			Results: map[string][][]string{"VA2108": {{"dbo", "db_owner", "SQL_USER"}}},
		},
	}
	rule := &sql.ServersSqlVulnerabilityAssessmentsBaselinesRule{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "va1020"},
		Spec: sql.ServersSqlVulnerabilityAssessmentsBaselinesRule_Spec{
			AzureName: "VA1020", Owner: &genruntime.KnownResourceReference{Name: "baseline"}, LatestScan: ptr(false),
			Results: [][]string{{"public"}},
		},
	}
	objs := []conditioned{audit, masking, window, ledger, group, classifier, assessment, baseline, rule}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}

	expectResource(t, serverID+"/devOpsAuditingSettings/Default", func(r map[string]any) error {
		if p := properties(r); p["state"] != "Enabled" || p["storageAccountAccessKey"] != "audit-storage-key-1" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, dbID+"/dataMaskingPolicies/Default", func(r map[string]any) error {
		if p := properties(r); p["dataMaskingState"] != "Enabled" || p["exemptPrincipals"] != "dbo;auditor" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, dbID+"/maintenanceWindows/current", func(r map[string]any) error {
		if ranges, _ := properties(r)["timeRanges"].([]any); len(ranges) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, dbID+"/ledgerDigestUploads/current", func(r map[string]any) error {
		if properties(r)["digestStorageEndpoint"] != "https://contosoledger.confidential-ledger.azure.com" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, dbID+"/workloadGroups/etl/workloadClassifiers/loader", func(r map[string]any) error {
		if properties(r)["memberName"] != "etl_loader" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	baselineID := serverID + "/sqlVulnerabilityAssessments/default/baselines/default"
	expectResource(t, baselineID+"/rules/VA1020", func(r map[string]any) error {
		if !reflect.DeepEqual(properties(r)["results"], []any{[]any{"public"}}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(audit), audit); err != nil {
		t.Fatal(err)
	}
	if raw := fmt.Sprintf("%+v", audit.Status); contains(raw, "audit-storage-key-1") {
		t.Fatalf("the storage key is in the status: %s", raw)
	}

	// A change is PUT again; a minimum above the cap is refused.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(group), group); err != nil {
		t.Fatal(err)
	}
	group.Spec.MaxResourcePercent = ptr(80)
	if err := k8s.Update(ctx, group); err != nil {
		t.Fatal(err)
	}
	expectResource(t, dbID+"/workloadGroups/etl", func(r map[string]any) error {
		if properties(r)["maxResourcePercent"] != float64(80) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	wide := &sql.ServersDatabasesWorkloadGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "wide"},
		Spec: sql.ServersDatabasesWorkloadGroup_Spec{
			AzureName: "wide", Owner: onDB, MinResourcePercent: ptr(60), MaxResourcePercent: ptr(40), MinResourcePercentPerRequest: ptr(5.0),
		},
	}
	if err := k8s.Create(ctx, wide); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "a group whose minimum is above its cap refused", notReadyWith(wide, "no more than its maxResourcePercent"))

	for _, o := range []client.Object{wide, rule, baseline, assessment, classifier, group, ledger, window, masking, audit} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, id := range []string{dbID + "/workloadGroups/etl", baselineID + "/rules/VA1020", serverID + "/sqlVulnerabilityAssessments/default"} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in the fake", id)
		}
	}
	if !armFake.HasResource(dbID + "/dataMaskingPolicies/Default") {
		t.Fatal("the masking policy was deleted; it has no DELETE and was detached")
	}
}
