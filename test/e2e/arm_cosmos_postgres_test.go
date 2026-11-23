// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	citus "github.com/highfivelabs/aso-extensions/apis/arm/dbforpostgresql/v20221108"
)

// Azure Cosmos DB for PostgreSQL (docs/plan.md f46) on ASO's ResourceGroup
// stand-in: a single-node cluster with its administrator password from a
// Secret and a maintenance window, a firewall rule, a role with its password
// from a Secret, a coordinator and a node configuration (no DELETE: they are
// detached), and a read replica naming the cluster by object. A firewall
// rule with an address that is not IPv4 is refused by the CRD (the spec's
// pattern) and a node count of 1 by the fake (cosmospostgres.go); a change
// is PUT again; all are deleted, children first, the configurations staying
// in Azure.
func TestCosmosDBForPostgreSQL(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "citus")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	secret(t, ns, "citus-passwords", map[string]string{"admin": "admin-password-1", "app": "app-password-1"})
	detach := map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}

	cluster := &citus.ServerGroupsv2{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "citus"},
		Spec: citus.ServerGroupsv2_Spec{
			AzureName: "citus-" + ns, Owner: &genruntime.KnownResourceReference{Name: "rg"}, Location: ptr("westus"),
			AdministratorLoginPassword:  &genruntime.SecretReference{Name: "citus-passwords", Key: "admin"},
			PostgresqlVersion:           ptr("16"),
			CitusVersion:                ptr("12.1"),
			CoordinatorServerEdition:    ptr("GeneralPurpose"),
			CoordinatorStorageQuotaInMb: ptr(131072),
			CoordinatorVCores:           ptr(2),
			NodeCount:                   ptr(0),
			EnableShardsOnCoordinator:   ptr(true),
			MaintenanceWindow:           &citus.MaintenanceWindow{CustomWindow: ptr("Enabled"), DayOfWeek: ptr(0), StartHour: ptr(2), StartMinute: ptr(0)},
		},
	}
	onCluster := &genruntime.KnownResourceReference{Name: "citus"}
	office := &citus.ServerGroupsv2FirewallRule{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "office"},
		Spec: citus.ServerGroupsv2FirewallRule_Spec{
			AzureName: "office", Owner: onCluster, StartIpAddress: ptr("203.0.113.10"), EndIpAddress: ptr("203.0.113.20"),
		},
	}
	role := &citus.ServerGroupsv2Role{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "app"},
		Spec: citus.ServerGroupsv2Role_Spec{
			AzureName: "app", Owner: onCluster, Password: &genruntime.SecretReference{Name: "citus-passwords", Key: "app"},
		},
	}
	coordinator := &citus.ServerGroupsv2CoordinatorConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "coordinator-work-mem", Annotations: detach},
		Spec:       citus.ServerGroupsv2CoordinatorConfiguration_Spec{AzureName: "work_mem", Owner: onCluster, Value: ptr("16384")},
	}
	node := &citus.ServerGroupsv2NodeConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "node-log-connections", Annotations: detach},
		Spec:       citus.ServerGroupsv2NodeConfiguration_Spec{AzureName: "log_connections", Owner: onCluster, Value: ptr("on")},
	}
	replica := &citus.ServerGroupsv2{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "replica"},
		Spec: citus.ServerGroupsv2_Spec{
			AzureName: "replica-" + ns, Owner: &genruntime.KnownResourceReference{Name: "rg"}, Location: ptr("eastus"),
			SourceResourceReference: &genruntime.ResourceReference{Group: "dbforpostgresql.azure.hfl.dev", Kind: "ServerGroupsv2", Name: "citus"},
			SourceLocation:          ptr("westus"),
		},
	}
	objs := []conditioned{cluster, office, role, coordinator, node, replica}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}

	clusterID := rgID + "/providers/Microsoft.DBforPostgreSQL/serverGroupsv2/citus-" + ns
	expectResource(t, clusterID, func(r map[string]any) error {
		p := properties(r)
		mw, _ := p["maintenanceWindow"].(map[string]any)
		if r["type"] != "Microsoft.DBforPostgreSQL/serverGroupsv2" || p["administratorLoginPassword"] != "admin-password-1" ||
			p["nodeCount"] != float64(0) || p["coordinatorVCores"] != float64(2) || mw["startHour"] != float64(2) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, clusterID+"/roles/app", func(r map[string]any) error {
		if properties(r)["password"] != "app-password-1" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, clusterID+"/firewallRules/office", func(r map[string]any) error {
		if p := properties(r); p["startIpAddress"] != "203.0.113.10" || p["endIpAddress"] != "203.0.113.20" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, clusterID+"/coordinatorConfigurations/work_mem", func(r map[string]any) error {
		if properties(r)["value"] != "16384" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, clusterID+"/nodeConfigurations/log_connections", func(r map[string]any) error {
		if properties(r)["value"] != "on" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	replicaID := rgID + "/providers/Microsoft.DBforPostgreSQL/serverGroupsv2/replica-" + ns
	expectResource(t, replicaID, func(r map[string]any) error {
		if p := properties(r); p["sourceResourceId"] != clusterID || p["sourceLocation"] != "westus" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	// The status is the cluster's (the password is write-only and has no
	// field there).
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(cluster), cluster); err != nil {
		t.Fatal(err)
	}
	if cluster.Status.Id == nil || *cluster.Status.Id != clusterID {
		t.Fatalf("status: %+v", cluster.Status)
	}

	// A change is PUT again.
	cluster.Spec.CoordinatorVCores = ptr(4)
	if err := k8s.Update(ctx, cluster); err != nil {
		t.Fatal(err)
	}
	expectResource(t, clusterID, func(r map[string]any) error {
		if properties(r)["coordinatorVCores"] != float64(4) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A firewall rule needs IPv4 addresses; a cluster cannot have one node.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(office), office); err != nil {
		t.Fatal(err)
	}
	office.Spec.EndIpAddress = ptr("2001:db8::1")
	if err := k8s.Update(ctx, office); err == nil || !strings.Contains(err.Error(), "spec.endIpAddress") {
		t.Fatalf("an IPv6 address: the CRD's pattern should refuse it, got %v", err)
	}
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(cluster), cluster); err != nil {
		t.Fatal(err)
	}
	cluster.Spec.NodeCount = ptr(1)
	if err := k8s.Update(ctx, cluster); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "a node count of 1 refused", notReadyWith(cluster, "not 1"))

	for _, o := range []client.Object{replica, office, role, coordinator, node} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, id := range []string{replicaID, clusterID + "/firewallRules/office", clusterID + "/roles/app"} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in the fake", id)
		}
	}
	if !armFake.HasResource(clusterID+"/coordinatorConfigurations/work_mem") || !armFake.HasResource(clusterID+"/nodeConfigurations/log_connections") {
		t.Fatal("a configuration was deleted; it has no DELETE and was detached")
	}
	if err := k8s.Delete(ctx, cluster); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "citus gone", gone(cluster))
	for _, id := range []string{clusterID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in the fake", id)
		}
	}
}
