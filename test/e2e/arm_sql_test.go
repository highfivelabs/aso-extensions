// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	asosql "github.com/Azure/azure-service-operator/v2/api/sql/v20250101/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"

	sql "github.com/highfivelabs/aso-extensions/apis/arm/sql/v20250101"
)

// A database's geo-backup policy owned by ASO's ServersDatabase, and a
// server's express vulnerability assessment owned by ASO's Server, each by
// name, as a chart writes them: both become Ready and are PUT under their
// owner. The conformance test owns every kind by ARM ID, which did not show
// that the geo-backup policy's owner reference named
// sql.azure.hfl.dev/Servers_Database (its ARM name in our group), which no
// controller serves, instead of ASO's sql.azure.com/ServersDatabase
// (generator/external-owners.txt, "group/ARMName=Kind").
func TestSQLChildrenOwnedByASOKinds(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "sqlchild")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	serverID := rgID + "/providers/Microsoft.Sql/servers/sql-" + ns
	databaseID := serverID + "/databases/dw"
	armFake.AddResource(serverID)
	armFake.AddResource(databaseID)

	srv := &asosql.Server{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "sql",
		Annotations: map[string]string{genruntime.ResourceIDAnnotation: serverID}}}
	srv.Spec.AzureName = "sql-" + ns
	srv.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	if err := k8s.Create(ctx, srv); err != nil {
		t.Fatal(err)
	}
	srv.Status.Conditions = []conditions.Condition{asoReady(srv.Generation)}
	if err := k8s.Status().Update(ctx, srv); err != nil {
		t.Fatal(err)
	}
	db := &asosql.ServersDatabase{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "dw",
		Annotations: map[string]string{genruntime.ResourceIDAnnotation: databaseID}}}
	db.Spec.AzureName = "dw"
	db.Spec.Owner = &genruntime.KnownResourceReference{Name: "sql"}
	if err := k8s.Create(ctx, db); err != nil {
		t.Fatal(err)
	}
	db.Status.Conditions = []conditions.Condition{asoReady(db.Generation)}
	if err := k8s.Status().Update(ctx, db); err != nil {
		t.Fatal(err)
	}

	disabled := sql.GeoBackupPolicyState_Disabled
	policy := &sql.ServersDatabasesGeoBackupPolicy{
		// Azure has no DELETE for a geo-backup policy.
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "dw-geo-backup",
			Annotations: map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}},
		Spec: sql.ServersDatabasesGeoBackupPolicy_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "dw"},
			State: &disabled,
		},
	}
	enabled := sql.SqlVulnerabilityAssessmentState_Enabled
	assessment := &sql.ServersSqlVulnerabilityAssessment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "sql-va"},
		Spec: sql.ServersSqlVulnerabilityAssessment_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "sql"},
			State: &enabled,
		},
	}
	for _, o := range []client.Object{policy, assessment} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "ServersDatabasesGeoBackupPolicy Ready", ready(policy))
	eventually(t, timeout, "ServersSqlVulnerabilityAssessment Ready", ready(assessment))
	expectResource(t, databaseID+"/geoBackupPolicies/Default", func(r map[string]any) error {
		if properties(r)["state"] != "Disabled" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, serverID+"/sqlVulnerabilityAssessments/default", func(r map[string]any) error {
		if properties(r)["state"] != "Enabled" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
}
