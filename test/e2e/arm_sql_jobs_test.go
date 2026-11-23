// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	asosql "github.com/Azure/azure-service-operator/v2/api/sql/v20250101/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	sql "github.com/highfivelabs/aso-extensions/apis/arm/sql/v20250101"
)

// Azure SQL Elastic Jobs (docs/plan.md f39) as a chart writes them, on ASO's
// Server and ServersDatabase stand-ins: a job agent whose job database is
// ASO's ServersDatabase by object; a credential whose password comes from a
// Secret (and stays out of the status); a target group of one database; a
// job with a schedule and a T-SQL step naming the target group and the
// credential by object; a private endpoint to the server; a DNS alias. The
// step is changed; a step naming another agent's credential is refused (the
// fake's rule, sqljobs.go); everything is deleted, children first.
func TestSQLElasticJobs(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "sqljobs")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	serverID := rgID + "/providers/Microsoft.Sql/servers/sql-" + ns
	databaseID := serverID + "/databases/jobs"
	armFake.AddResource(rgID)
	armFake.AddResource(serverID)
	armFake.AddResource(databaseID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	srv := &asosql.Server{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "sql"}}
	srv.Spec.AzureName, srv.Spec.Owner = "sql-"+ns, &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, srv, serverID)
	db := &asosql.ServersDatabase{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "jobs-db"}}
	db.Spec.AzureName, db.Spec.Owner = "jobs", &genruntime.KnownResourceReference{Name: "sql"}
	asoStandIn(t, db, databaseID)
	secret(t, ns, "job-credential", map[string]string{"password": "job-password-1"})

	byObject := func(kind, name string) *genruntime.ResourceReference {
		return &genruntime.ResourceReference{Group: "sql.azure.hfl.dev", Kind: kind, Name: name}
	}
	inAgent := &genruntime.KnownResourceReference{Name: "agent"}
	agent := &sql.ServersJobAgent{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "agent"},
		Spec: sql.ServersJobAgent_Spec{
			AzureName: "agent", Owner: &genruntime.KnownResourceReference{Name: "sql"}, Location: ptr("westus"),
			DatabaseReference: &genruntime.ResourceReference{Group: "sql.azure.com", Kind: "ServersDatabase", Name: "jobs-db"},
			Sku:               &sql.Sku{Name: ptr("JA100"), Capacity: ptr(100)},
		},
	}
	credential := &sql.ServersJobAgentsCredential{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "runner"},
		Spec: sql.ServersJobAgentsCredential_Spec{
			AzureName: "runner", Owner: inAgent, Username: ptr("job_runner"),
			Password: &genruntime.SecretReference{Name: "job-credential", Key: "password"},
		},
	}
	targets := &sql.ServersJobAgentsTargetGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "app-dbs"},
		Spec: sql.ServersJobAgentsTargetGroup_Spec{
			AzureName: "app-dbs", Owner: inAgent,
			Members: []sql.JobTarget{{
				Type: ptr(sql.JobTargetType_SqlDatabase), MembershipType: ptr(sql.JobTarget_MembershipType_Include),
				ServerName: ptr("sql-" + ns + ".database.windows.net"), DatabaseName: ptr("app"),
			}},
		},
	}
	job := &sql.ServersJobAgentsJob{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "nightly"},
		Spec: sql.ServersJobAgentsJob_Spec{
			AzureName: "nightly", Owner: inAgent, Description: ptr("rebuild indexes"),
			Schedule: &sql.JobSchedule{Type: ptr(sql.JobSchedule_Type_Recurring), Interval: ptr("P1D"), Enabled: ptr(true),
				StartTime: ptr("2030-01-01T02:00:00Z")},
		},
	}
	step := &sql.ServersJobAgentsJobsStep{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "rebuild"},
		Spec: sql.ServersJobAgentsJobsStep_Spec{
			AzureName: "rebuild", Owner: &genruntime.KnownResourceReference{Name: "nightly"}, StepId: ptr(1),
			TargetGroupReference: byObject("ServersJobAgentsTargetGroup", "app-dbs"),
			CredentialReference:  byObject("ServersJobAgentsCredential", "runner"),
			Action:               &sql.JobStepAction{Type: ptr(sql.JobStepAction_Type_TSql), Value: ptr("ALTER INDEX ALL ON dbo.orders REBUILD")},
		},
	}
	endpoint := &sql.ServersJobAgentsPrivateEndpoint{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "to-sql"},
		Spec: sql.ServersJobAgentsPrivateEndpoint_Spec{
			AzureName: "to-sql", Owner: inAgent,
			TargetServerAzureResourceReference: &genruntime.ResourceReference{ARMID: serverID},
		},
	}
	alias := &sql.ServersDnsAlias{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "primary"},
		Spec:       sql.ServersDnsAlias_Spec{AzureName: "primary-" + ns, Owner: &genruntime.KnownResourceReference{Name: "sql"}},
	}
	for _, o := range []conditioned{agent, credential, targets, job, step, endpoint, alias} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	agentID := serverID + "/jobAgents/agent"
	stepID := agentID + "/jobs/nightly/steps/rebuild"
	expectResource(t, agentID, func(r map[string]any) error {
		if properties(r)["databaseId"] != databaseID {
			return fmt.Errorf("agent: %v", r)
		}
		return nil
	})
	expectResource(t, agentID+"/credentials/runner", func(r map[string]any) error {
		if p := properties(r); p["username"] != "job_runner" || p["password"] != "job-password-1" {
			return fmt.Errorf("credential: %v", r)
		}
		return nil
	})
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(credential), credential); err != nil {
		t.Fatal(err)
	}
	if raw := fmt.Sprintf("%+v", credential.Status); contains(raw, "job-password-1") {
		t.Fatalf("the password is in the credential's status: %s", raw)
	}
	expectResource(t, stepID, func(r map[string]any) error {
		p := properties(r)
		action, _ := p["action"].(map[string]any)
		if p["targetGroup"] != agentID+"/targetGroups/app-dbs" || p["credential"] != agentID+"/credentials/runner" ||
			action["value"] != "ALTER INDEX ALL ON dbo.orders REBUILD" {
			return fmt.Errorf("step: %v", r)
		}
		return nil
	})
	expectResource(t, agentID+"/privateEndpoints/to-sql", func(r map[string]any) error {
		if properties(r)["targetServerAzureResourceId"] != serverID {
			return fmt.Errorf("private endpoint: %v", r)
		}
		return nil
	})
	if !armFake.HasResource(serverID + "/dnsAliases/primary-" + ns) {
		t.Fatal("the DNS alias is not in the fake")
	}

	// A change of the step's script is PUT again.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(step), step); err != nil {
		t.Fatal(err)
	}
	step.Spec.Action.Value = ptr("UPDATE STATISTICS dbo.orders")
	if err := k8s.Update(ctx, step); err != nil {
		t.Fatal(err)
	}
	expectResource(t, stepID, func(r map[string]any) error {
		if action, _ := properties(r)["action"].(map[string]any); action["value"] != "UPDATE STATISTICS dbo.orders" {
			return fmt.Errorf("step: %v", r)
		}
		return nil
	})

	// A step naming another agent's credential is refused.
	foreign := &sql.ServersJobAgentsJobsStep{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "foreign"},
		Spec: sql.ServersJobAgentsJobsStep_Spec{
			AzureName: "foreign", Owner: &genruntime.KnownResourceReference{Name: "nightly"},
			TargetGroupReference: byObject("ServersJobAgentsTargetGroup", "app-dbs"),
			CredentialReference:  &genruntime.ResourceReference{ARMID: serverID + "/jobAgents/other/credentials/runner"},
			Action:               &sql.JobStepAction{Value: ptr("SELECT 1")},
		},
	}
	if err := k8s.Create(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "a step with another agent's credential refused", notReadyWith(foreign, "is not in its job agent"))

	for _, o := range []client.Object{foreign, step, job, targets, credential, endpoint, alias, agent} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, id := range []string{stepID, agentID + "/credentials/runner", agentID, serverID + "/dnsAliases/primary-" + ns} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in the fake", id)
		}
	}
}
