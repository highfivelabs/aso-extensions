// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	asomongo "github.com/Azure/azure-service-operator/v2/api/documentdb/v1api20240701/storage"
	asocosmos "github.com/Azure/azure-service-operator/v2/api/documentdb/v1api20240815/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	fleets "github.com/highfivelabs/aso-extensions/apis/arm/documentdb/v20260315"
	mongo "github.com/highfivelabs/aso-extensions/apis/arm/documentdb/v20260601"
)

// Azure Cosmos DB fleets and a MongoDB vCore cluster's Microsoft Entra users
// (docs/plan.md f47): a fleet, a fleetspace with a throughput pool, a
// fleetspace account naming ASO's DatabaseAccount stand-in by object, and a
// user of ASO's MongoCluster stand-in, named by a service principal's object
// ID. A pool whose minimum is above its maximum and a user named by a word
// are refused (the fake's rules, cosmosfleet.go); a change is PUT again;
// all are deleted, children first.
func TestCosmosFleetsAndMongoUsers(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "cosmosfleet")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	accountID := rgID + "/providers/Microsoft.DocumentDB/databaseAccounts/tenant1-" + ns
	clusterID := rgID + "/providers/Microsoft.DocumentDB/mongoClusters/docs-" + ns
	for _, id := range []string{rgID, accountID, clusterID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	account := &asocosmos.DatabaseAccount{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "tenant1"}}
	account.Spec.AzureName, account.Spec.Owner = "tenant1-"+ns, &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, account, accountID)
	cluster := &asomongo.MongoCluster{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "docs"}}
	cluster.Spec.AzureName, cluster.Spec.Owner = "docs-"+ns, &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, cluster, clusterID)

	fleet := &fleets.Fleet{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "tenants"},
		Spec:       fleets.Fleet_Spec{AzureName: "tenants-" + ns, Owner: &genruntime.KnownResourceReference{Name: "rg"}, Location: ptr("westus")},
	}
	space := &fleets.FleetsFleetspace{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "shared"},
		Spec: fleets.FleetsFleetspace_Spec{
			AzureName: "shared", Owner: &genruntime.KnownResourceReference{Name: "tenants"},
			DataRegions:       []string{"westus"},
			FleetspaceApiKind: ptr(fleets.FleetspacePropertiesFleetspaceApiKind_NoSQL),
			ServiceTier:       ptr(fleets.FleetspacePropertiesServiceTier_GeneralPurpose),
			ThroughputPoolConfiguration: &fleets.FleetspacePropertiesThroughputPoolConfiguration{
				MinThroughput: ptr(1000), MaxThroughput: ptr(4000),
			},
		},
	}
	member := &fleets.FleetsFleetspacesFleetspaceAccount{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "tenant1"},
		Spec: fleets.FleetsFleetspacesFleetspaceAccount_Spec{
			AzureName: "tenant1", Owner: &genruntime.KnownResourceReference{Name: "shared"},
			GlobalDatabaseAccountProperties: &fleets.FleetspaceAccountPropertiesGlobalDatabaseAccountProperties{
				ResourceReference: &genruntime.ResourceReference{Group: "documentdb.azure.com", Kind: "DatabaseAccount", Name: "tenant1"},
				ArmLocation:       ptr("westus"),
			},
		},
	}
	const principal = "00000000-0000-0000-0000-0000000000b7"
	user := &mongo.MongoClustersUser{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "app"},
		Spec: mongo.MongoClustersUser_Spec{
			AzureName: principal, Owner: &genruntime.KnownResourceReference{Name: "docs"},
			Properties: &mongo.UserProperties{
				IdentityProvider: &mongo.IdentityProvider{MicrosoftEntraID: &mongo.EntraIdentityProvider{
					Type:       ptr(mongo.IdentityProviderType_MicrosoftEntraID),
					Properties: &mongo.EntraIdentityProviderProperties{PrincipalType: ptr(mongo.EntraPrincipalType_ServicePrincipal)},
				}},
				Roles: []mongo.DatabaseRole{{Db: ptr("admin"), Role: ptr(mongo.UserRole_Root)}},
			},
		},
	}
	objs := []conditioned{fleet, space, member, user}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}

	fleetID := rgID + "/providers/Microsoft.DocumentDB/fleets/tenants-" + ns
	spaceID := fleetID + "/fleetspaces/shared"
	expectResource(t, spaceID, func(r map[string]any) error {
		pool, _ := properties(r)["throughputPoolConfiguration"].(map[string]any)
		if r["type"] != "Microsoft.DocumentDB/fleets/fleetspaces" || pool["maxThroughput"] != float64(4000) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	memberID := spaceID + "/fleetspaceAccounts/tenant1"
	expectResource(t, memberID, func(r map[string]any) error {
		acct, _ := properties(r)["globalDatabaseAccountProperties"].(map[string]any)
		if acct["resourceId"] != accountID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	userID := clusterID + "/users/" + principal
	expectResource(t, userID, func(r map[string]any) error {
		provider, _ := properties(r)["identityProvider"].(map[string]any)
		roles, _ := properties(r)["roles"].([]any)
		if provider["type"] != "MicrosoftEntraID" || len(roles) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(space), space); err != nil {
		t.Fatal(err)
	}
	space.Spec.ThroughputPoolConfiguration.MaxThroughput = ptr(8000)
	if err := k8s.Update(ctx, space); err != nil {
		t.Fatal(err)
	}
	expectResource(t, spaceID, func(r map[string]any) error {
		pool, _ := properties(r)["throughputPoolConfiguration"].(map[string]any)
		if pool["maxThroughput"] != float64(8000) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	// A pool's minimum above its maximum is refused.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(space), space); err != nil {
		t.Fatal(err)
	}
	space.Spec.ThroughputPoolConfiguration.MinThroughput = ptr(10000)
	if err := k8s.Update(ctx, space); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "a pool's minimum above its maximum refused", notReadyWith(space, "minimum throughput is above"))
	// A user named by a word is refused.
	word := user.DeepCopy()
	word.ObjectMeta = metav1.ObjectMeta{Namespace: ns, Name: "word"}
	word.Spec.AzureName = "app"
	if err := k8s.Create(ctx, word); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "a user named by a word refused", notReadyWith(word, "Entra object ID"))

	for _, o := range []client.Object{word, user, member, space, fleet} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, id := range []string{userID, memberID, spaceID, fleetID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in the fake", id)
		}
	}
}
