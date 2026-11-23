// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	asoapp "github.com/Azure/azure-service-operator/v2/api/app/v20250101/storage"
	asocache "github.com/Azure/azure-service-operator/v2/api/cache/v20241101/storage"
	asodisk "github.com/Azure/azure-service-operator/v2/api/compute/v20240302/storage"
	asosql "github.com/Azure/azure-service-operator/v2/api/sql/v20250101/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"

	app "github.com/highfivelabs/aso-extensions/apis/arm/app/v20260701"
	cache "github.com/highfivelabs/aso-extensions/apis/arm/cache/v20241101"
	compute "github.com/highfivelabs/aso-extensions/apis/arm/compute/v20260302"
	sql "github.com/highfivelabs/aso-extensions/apis/arm/sql/v20250101"
)

// asoOwner is an ASO kind a stand-in is made of.
type asoOwner interface {
	client.Object
	SetConditions(conditions.Conditions)
}

// asoStandIn creates obj as ASO leaves it once provisioned: the ARM ID
// annotation, Ready.
func asoStandIn(t *testing.T, obj asoOwner, armID string) {
	t.Helper()
	ctx := context.Background()
	obj.SetAnnotations(map[string]string{genruntime.ResourceIDAnnotation: armID})
	if err := k8s.Create(ctx, obj); err != nil {
		t.Fatal(err)
	}
	obj.SetConditions(conditions.Conditions{asoReady(obj.GetGeneration())})
	if err := k8s.Status().Update(ctx, obj); err != nil {
		t.Fatal(err)
	}
}

// The private endpoint connections of docs/plan.md f37, each owned by its
// ASO kind by name (an Azure SQL server, a container app, a disk access, an
// Azure Cache for Redis cache; DiskAccess is a new owner type): a connection
// a private endpoint made is approved, then rejected, and the object
// deleted (Azure deletes the connection). A connection no private endpoint
// made is refused (the fake's rule, privateendpoints.go) and the object
// says so.
func TestPrivateEndpointConnections(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "pec")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	rg := &genruntime.KnownResourceReference{Name: "rg"}

	server := &asosql.Server{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "sql"}}
	server.Spec.AzureName, server.Spec.Owner = ns+"-sql", rg
	containerApp := &asoapp.ContainerApp{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "app"}}
	containerApp.Spec.AzureName, containerApp.Spec.Owner = ns+"-app", rg
	diskAccess := &asodisk.DiskAccess{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "disks"}}
	diskAccess.Spec.AzureName, diskAccess.Spec.Owner = ns+"-disks", rg
	redis := &asocache.Redis{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cache"}}
	redis.Spec.AzureName, redis.Spec.Owner = ns+"-cache", rg

	type step struct {
		owner    asoOwner
		parentID string
		// made and refused are the kind under test, owned by the stand-in:
		// the connection a private endpoint made, and one it did not.
		made, refused conditioned
		// decide sets the connection's state on made.
		decide func(status string)
	}
	sqlConn := func(name, status string) *sql.ServersPrivateEndpointConnection {
		return &sql.ServersPrivateEndpointConnection{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "sql-" + name},
			Spec: sql.ServersPrivateEndpointConnection_Spec{
				AzureName: name, Owner: &genruntime.KnownResourceReference{Name: "sql"},
				PrivateLinkServiceConnectionState: &sql.PrivateLinkServiceConnectionStateProperty{
					Status: ptr(sql.PrivateLinkServiceConnectionStateStatus(status)), Description: ptr("by the platform team"),
				},
			},
		}
	}
	appConn := func(name, status string) *app.ContainerAppsPrivateEndpointConnection {
		return &app.ContainerAppsPrivateEndpointConnection{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "app-" + name},
			Spec: app.ContainerAppsPrivateEndpointConnection_Spec{
				AzureName: name, Owner: &genruntime.KnownResourceReference{Name: "app"},
				PrivateLinkServiceConnectionState: &app.PrivateLinkServiceConnectionState{
					Status: ptr(app.PrivateEndpointServiceConnectionStatus(status)), Description: ptr("by the platform team"),
				},
			},
		}
	}
	diskConn := func(name, status string) *compute.DiskAccessesPrivateEndpointConnection {
		return &compute.DiskAccessesPrivateEndpointConnection{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "disks-" + name},
			Spec: compute.DiskAccessesPrivateEndpointConnection_Spec{
				AzureName: name, Owner: &genruntime.KnownResourceReference{Name: "disks"},
				PrivateLinkServiceConnectionState: &compute.PrivateLinkServiceConnectionState{
					Status: ptr(compute.PrivateEndpointServiceConnectionStatus(status)), Description: ptr("by the platform team"),
				},
			},
		}
	}
	cacheConn := func(name, status string) *cache.RedisPrivateEndpointConnection {
		return &cache.RedisPrivateEndpointConnection{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cache-" + name},
			Spec: cache.RedisPrivateEndpointConnection_Spec{
				AzureName: name, Owner: &genruntime.KnownResourceReference{Name: "cache"},
				PrivateLinkServiceConnectionState: &cache.PrivateLinkServiceConnectionState{
					Status: ptr(cache.PrivateEndpointServiceConnectionStatus(status)), Description: ptr("by the platform team"),
				},
			},
		}
	}
	sqlMade, appMade, diskMade, cacheMade := sqlConn("pe-1", "Approved"), appConn("pe-1", "Approved"), diskConn("pe-1", "Approved"), cacheConn("pe-1", "Approved")
	steps := []step{
		{server, rgID + "/providers/Microsoft.Sql/servers/" + ns + "-sql", sqlMade, sqlConn("pe-2", "Approved"), func(s string) {
			sqlMade.Spec.PrivateLinkServiceConnectionState.Status = ptr(sql.PrivateLinkServiceConnectionStateStatus(s))
		}},
		{containerApp, rgID + "/providers/Microsoft.App/containerApps/" + ns + "-app", appMade, appConn("pe-2", "Approved"), func(s string) {
			appMade.Spec.PrivateLinkServiceConnectionState.Status = ptr(app.PrivateEndpointServiceConnectionStatus(s))
		}},
		{diskAccess, rgID + "/providers/Microsoft.Compute/diskAccesses/" + ns + "-disks", diskMade, diskConn("pe-2", "Approved"), func(s string) {
			diskMade.Spec.PrivateLinkServiceConnectionState.Status = ptr(compute.PrivateEndpointServiceConnectionStatus(s))
		}},
		{redis, rgID + "/providers/Microsoft.Cache/redis/" + ns + "-cache", cacheMade, cacheConn("pe-2", "Approved"), func(s string) {
			cacheMade.Spec.PrivateLinkServiceConnectionState.Status = ptr(cache.PrivateEndpointServiceConnectionStatus(s))
		}},
	}
	wantStatus := func(id, status string) {
		t.Helper()
		expectResource(t, id, func(r map[string]any) error {
			state, _ := properties(r)["privateLinkServiceConnectionState"].(map[string]any)
			if state["status"] != status || state["description"] != "by the platform team" {
				return fmt.Errorf("connection %s: %v", id, r)
			}
			return nil
		})
	}
	for _, s := range steps {
		armFake.AddResource(s.parentID)
		asoStandIn(t, s.owner, s.parentID)
		// The private endpoint made pe-1, pending; nothing made pe-2.
		armFake.AddResource(s.parentID + "/privateEndpointConnections/pe-1")
		for _, o := range []conditioned{s.made, s.refused} {
			if err := k8s.Create(ctx, o); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, s := range steps {
		eventually(t, timeout, s.made.GetName()+" Ready", ready(s.made))
		wantStatus(s.parentID+"/privateEndpointConnections/pe-1", "Approved")
		eventually(t, timeout, s.refused.GetName()+" refused", notReadyWith(s.refused, "a private endpoint makes it"))
		if armFake.HasResource(s.parentID + "/privateEndpointConnections/pe-2") {
			t.Fatalf("%s: a connection was made without a private endpoint", s.parentID)
		}
	}
	for _, s := range steps {
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(s.made), s.made); err != nil {
			t.Fatal(err)
		}
		s.decide("Rejected")
		if err := k8s.Update(ctx, s.made); err != nil {
			t.Fatal(err)
		}
		wantStatus(s.parentID+"/privateEndpointConnections/pe-1", "Rejected")
	}
	for _, s := range steps {
		for _, o := range []client.Object{s.made, s.refused} {
			if err := k8s.Delete(ctx, o); err != nil {
				t.Fatal(err)
			}
			eventually(t, timeout, o.GetName()+" gone", gone(o))
		}
		if armFake.HasResource(s.parentID + "/privateEndpointConnections/pe-1") {
			t.Fatalf("%s: the connection is still in the fake", s.parentID)
		}
	}
}
