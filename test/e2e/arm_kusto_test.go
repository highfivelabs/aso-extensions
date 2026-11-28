// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	asokusto "github.com/Azure/azure-service-operator/v2/api/kusto/v1api20240413/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	kusto "github.com/highfivelabs/aso-extensions/apis/arm/kusto/v20250214"
)

// The Data Explorer kinds of docs/plan.md f69, on ASO's Cluster and Database
// by name (new external owners): a follower configuration attaching every
// database of a leader cluster (ASO's other Cluster, by object), a managed
// private endpoint to a storage account (by ARM ID), a Python sandbox image,
// an approved private endpoint connection, and a database script whose KQL
// is in a Secret. A cluster following itself and a script with no source are
// refused (the fake's rules, kusto.go); a change is PUT again; deleted, all
// go.
func TestKustoClusterChildren(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "kusto")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	followerID := rgID + "/providers/Microsoft.Kusto/clusters/follower"
	leaderID := rgID + "/providers/Microsoft.Kusto/clusters/leader"
	dbID := followerID + "/databases/telemetry"
	storageID := rgID + "/providers/Microsoft.Storage/storageAccounts/ingest"
	connID := followerID + "/privateEndpointConnections/pe-follower"
	for _, id := range []string{rgID, followerID, leaderID, dbID, storageID, connID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	for name, id := range map[string]string{"follower": followerID, "leader": leaderID} {
		c := &asokusto.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
		c.Spec.AzureName, c.Spec.Location = name, ptr("westus")
		c.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
		asoStandIn(t, c, id)
	}
	db := &asokusto.Database{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "telemetry"}}
	db.Spec.AzureName = "telemetry"
	db.Spec.Owner = &genruntime.KnownResourceReference{Name: "follower"}
	asoStandIn(t, db, dbID)
	if err := k8s.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "kql"},
		StringData: map[string]string{"tables": ".create-merge table Events (At: datetime, Name: string)"},
	}); err != nil {
		t.Fatal(err)
	}
	onFollower := &genruntime.KnownResourceReference{Name: "follower"}

	attached := &kusto.ClustersAttachedDatabaseConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "follow-leader"},
		Spec: kusto.ClustersAttachedDatabaseConfiguration_Spec{
			Owner: onFollower, Location: ptr("westus"), DatabaseName: ptr("*"),
			DefaultPrincipalsModificationKind: ptr(kusto.DefaultPrincipalsModificationKind_Union),
			ClusterResourceReference:          &genruntime.ResourceReference{Group: "kusto.azure.com", Kind: "Cluster", Name: "leader"},
		},
	}
	self := &kusto.ClustersAttachedDatabaseConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "follow-self"},
		Spec: kusto.ClustersAttachedDatabaseConfiguration_Spec{
			Owner: onFollower, Location: ptr("westus"), DatabaseName: ptr("*"),
			DefaultPrincipalsModificationKind: ptr(kusto.DefaultPrincipalsModificationKind_Union),
			ClusterResourceReference:          &genruntime.ResourceReference{ARMID: followerID},
		},
	}
	mpe := &kusto.ClustersManagedPrivateEndpoint{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "ingest-blob"},
		Spec: kusto.ClustersManagedPrivateEndpoint_Spec{
			Owner: onFollower, GroupId: ptr("blob"), PrivateLinkResourceReference: &genruntime.ResourceReference{ARMID: storageID},
		},
	}
	image := &kusto.ClustersSandboxCustomImage{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "python"},
		Spec: kusto.ClustersSandboxCustomImage_Spec{
			Owner: onFollower, Language: ptr(kusto.Language_Python), LanguageVersion: ptr("3.10.8"), RequirementsFileContent: ptr("numpy"),
		},
	}
	conn := &kusto.ClustersPrivateEndpointConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "follower-pe"},
		Spec: kusto.ClustersPrivateEndpointConnection_Spec{
			AzureName: "pe-follower", Owner: onFollower,
			PrivateLinkServiceConnectionState: &kusto.PrivateLinkServiceConnectionStateProperty{Status: ptr("Approved")},
		},
	}
	script := &kusto.ClustersDatabasesScript{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "tables"},
		Spec: kusto.ClustersDatabasesScript_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "telemetry"}, ScriptContent: &genruntime.SecretReference{Name: "kql", Key: "tables"},
			ContinueOnErrors: ptr(false),
		},
	}
	empty := &kusto.ClustersDatabasesScript{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "empty"},
		Spec:       kusto.ClustersDatabasesScript_Spec{Owner: &genruntime.KnownResourceReference{Name: "telemetry"}},
	}
	objs := []conditioned{attached, self, mpe, image, conn, script, empty}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []conditioned{attached, mpe, image, conn, script} {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	eventually(t, timeout, "a cluster following itself refused", notReadyWith(self, "its own databases"))
	eventually(t, timeout, "a script with no source refused", notReadyWith(empty, "one of the two"))

	attachedID := followerID + "/attachedDatabaseConfigurations/follow-leader"
	expectResource(t, attachedID, func(r map[string]any) error {
		if p := properties(r); p["clusterResourceId"] != leaderID || p["databaseName"] != "*" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	scriptID := dbID + "/scripts/tables"
	expectResource(t, scriptID, func(r map[string]any) error {
		if p := properties(r); p["scriptContent"] != ".create-merge table Events (At: datetime, Name: string)" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again: a prefix for the followed databases' names.
	if err := change(t, attached, func(a *kusto.ClustersAttachedDatabaseConfiguration) {
		a.Spec.DatabaseNamePrefix = ptr("leader_")
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, attachedID, func(r map[string]any) error {
		if p := properties(r); p["databaseNamePrefix"] != "leader_" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range objs {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, id := range []string{attachedID, scriptID, followerID + "/managedPrivateEndpoints/ingest-blob", followerID + "/sandboxCustomImages/python", connID} {
		if armFake.HasResource(id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
}
