// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	asosql "github.com/Azure/azure-service-operator/v2/api/sql/v20250101/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	sql "github.com/highfivelabs/aso-extensions/apis/arm/sql/v20250101"
)

// SQL Managed Instance children (docs/plan.md f42) on ASO's ManagedInstance
// and ManagedInstancesDatabase stand-ins: a Microsoft Entra administrator
// (its SID from a ConfigMap), then Entra-only authentication once the
// administrator is Ready (the fake refuses Entra-only on an instance with no
// Entra administrator, and the object says so); a Key Vault key and the
// encryption protector naming it; a
// database's vulnerability assessment (its SAS from a Secret, never in the
// status) and short-term backup retention; a stop and start schedule. A
// protector naming a key that is not there is refused; the objects are
// deleted, those Azure cannot delete detached.
func TestSQLManagedInstanceChildren(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "sqlmi")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	miID := rgID + "/providers/Microsoft.Sql/managedInstances/mi-" + ns
	dbID := miID + "/databases/app"
	bareID := rgID + "/providers/Microsoft.Sql/managedInstances/bare-" + ns
	for _, id := range []string{rgID, miID, dbID, bareID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	mi := &asosql.ManagedInstance{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "mi"}}
	mi.Spec.AzureName, mi.Spec.Owner = "mi-"+ns, &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, mi, miID)
	db := &asosql.ManagedInstancesDatabase{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "app"}}
	db.Spec.AzureName, db.Spec.Owner = "app", &genruntime.KnownResourceReference{Name: "mi"}
	asoStandIn(t, db, dbID)
	bare := &asosql.ManagedInstance{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "bare"}}
	bare.Spec.AzureName, bare.Spec.Owner = "bare-"+ns, &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, bare, bareID)
	secret(t, ns, "va", map[string]string{"sas": "sv=2024-01-01&sig=c2FzLXNpZw"})
	if err := k8s.Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "dba-group"},
		Data:       map[string]string{"objectId": "00000000-0000-0000-0000-0000000000db"},
	}); err != nil {
		t.Fatal(err)
	}
	onMI := &genruntime.KnownResourceReference{Name: "mi"}
	onDB := &genruntime.KnownResourceReference{Name: "app"}
	detach := map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}
	keyName := "contoso_tde_0123456789abcdef0123456789abcdef"

	entraOnly := &sql.ManagedInstancesAzureADOnlyAuthentication{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "entra-only"},
		Spec:       sql.ManagedInstancesAzureADOnlyAuthentication_Spec{Owner: onMI, AzureADOnlyAuthentication: ptr(true)},
	}
	admin := &sql.ManagedInstancesAdministrator{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "admin"},
		Spec: sql.ManagedInstancesAdministrator_Spec{
			Owner: onMI, AdministratorType: ptr(sql.ManagedInstanceAdministratorType_ActiveDirectory), Login: ptr("dba-group"),
			SidFromConfig: &genruntime.ConfigMapReference{Name: "dba-group", Key: "objectId"},
		},
	}
	key := &sql.ManagedInstancesKey{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "tde"},
		Spec: sql.ManagedInstancesKey_Spec{
			AzureName: keyName, Owner: onMI, ServerKeyType: ptr(sql.ServerKeyType_AzureKeyVault),
			Uri: ptr("https://contoso.vault.azure.net/keys/tde/0123456789abcdef0123456789abcdef"),
		},
	}
	protector := &sql.ManagedInstancesEncryptionProtector{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "protector", Annotations: detach},
		Spec: sql.ManagedInstancesEncryptionProtector_Spec{
			Owner: onMI, ServerKeyType: ptr(sql.ServerKeyType_AzureKeyVault), ServerKeyName: ptr(keyName), AutoRotationEnabled: ptr(true),
		},
	}
	assessment := &sql.ManagedInstancesDatabasesVulnerabilityAssessment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "va"},
		Spec: sql.ManagedInstancesDatabasesVulnerabilityAssessment_Spec{
			Owner: onDB, StorageContainerPath: ptr("https://contosova.blob.core.windows.net/scans/"),
			StorageContainerSasKey: &genruntime.SecretReference{Name: "va", Key: "sas"},
		},
	}
	retention := &sql.ManagedInstancesDatabasesBackupShortTermRetentionPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "pitr", Annotations: detach},
		Spec:       sql.ManagedInstancesDatabasesBackupShortTermRetentionPolicy_Spec{Owner: onDB, RetentionDays: ptr(14)},
	}
	schedule := &sql.ManagedInstancesStartStopSchedule{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "office-hours"},
		Spec: sql.ManagedInstancesStartStopSchedule_Spec{
			Owner: onMI, TimeZoneId: ptr("UTC"), Description: ptr("weekdays only"),
			ScheduleList: []sql.ScheduleItem{{
				StartDay: ptr(sql.DayOfWeek("Monday")), StartTime: ptr("07:00"),
				StopDay: ptr(sql.DayOfWeek("Friday")), StopTime: ptr("19:00"),
			}},
		},
	}
	// Entra-only on an instance with no Entra administrator is refused, and
	// the object says so. This is the first attempt's answer, so it does not
	// wait on ASO's retry backoff.
	refused := &sql.ManagedInstancesAzureADOnlyAuthentication{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "entra-only-bare"},
		Spec: sql.ManagedInstancesAzureADOnlyAuthentication_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "bare"}, AzureADOnlyAuthentication: ptr(true),
		},
	}
	if err := k8s.Create(ctx, refused); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "Entra-only without an Entra administrator refused", notReadyWith(refused, "needs a Microsoft Entra administrator"))
	if armFake.HasResource(bareID + "/azureADOnlyAuthentications/Default") {
		t.Fatal("the fake made Entra-only authentication on an instance with no Entra administrator")
	}
	if err := k8s.Delete(ctx, refused); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, refused.GetName()+" gone", gone(refused))

	// The administrator first, then Entra-only once it is Ready, as a person
	// would apply them; the rest together.
	if err := k8s.Create(ctx, admin); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, admin.GetName()+" Ready", ready(admin))
	objs := []conditioned{entraOnly, key, protector, assessment, retention, schedule}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}

	expectResource(t, miID+"/administrators/ActiveDirectory", func(r map[string]any) error {
		if p := properties(r); p["sid"] != "00000000-0000-0000-0000-0000000000db" || p["login"] != "dba-group" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, miID+"/azureADOnlyAuthentications/Default", func(r map[string]any) error {
		if properties(r)["azureADOnlyAuthentication"] != true {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, miID+"/encryptionProtector/current", func(r map[string]any) error {
		if p := properties(r); p["serverKeyName"] != keyName || p["serverKeyType"] != "AzureKeyVault" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, dbID+"/vulnerabilityAssessments/default", func(r map[string]any) error {
		if p := properties(r); p["storageContainerSasKey"] != "sv=2024-01-01&sig=c2FzLXNpZw" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, dbID+"/backupShortTermRetentionPolicies/default", func(r map[string]any) error {
		if properties(r)["retentionDays"] != float64(14) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, miID+"/startStopSchedules/default", func(r map[string]any) error {
		if list, _ := properties(r)["scheduleList"].([]any); len(list) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(assessment), assessment); err != nil {
		t.Fatal(err)
	}
	if raw := fmt.Sprintf("%+v", assessment.Status); contains(raw, "sig=") {
		t.Fatalf("the SAS is in the assessment's status: %s", raw)
	}

	// A change is PUT again.
	if err := change(t, retention, func(r *sql.ManagedInstancesDatabasesBackupShortTermRetentionPolicy) {
		r.Spec.RetentionDays = ptr(28)
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, dbID+"/backupShortTermRetentionPolicies/default", func(r map[string]any) error {
		if properties(r)["retentionDays"] != float64(28) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A protector naming a key the instance does not have is refused.
	if err := change(t, protector, func(p *sql.ManagedInstancesEncryptionProtector) {
		p.Spec.ServerKeyName = ptr("contoso_other_0123")
	}); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "a protector naming a missing key refused", notReadyWith(protector, "was not found"))

	for _, o := range []client.Object{protector, retention, entraOnly, admin, key, assessment, schedule} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, id := range []string{miID + "/keys/" + keyName, miID + "/administrators/ActiveDirectory", miID + "/startStopSchedules/default", dbID + "/vulnerabilityAssessments/default"} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in the fake", id)
		}
	}
	if !armFake.HasResource(miID + "/encryptionProtector/current") {
		t.Fatal("the encryption protector was deleted; it has no DELETE and was detached")
	}
}
