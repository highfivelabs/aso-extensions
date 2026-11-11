// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	asofleet "github.com/Azure/azure-service-operator/v2/api/containerservice/v1api20250301/storage"
	asoaks "github.com/Azure/azure-service-operator/v2/api/containerservice/v20260501/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"

	cs "github.com/highfivelabs/aso-extensions/apis/arm/containerservice/v20260501"
	csfleet "github.com/highfivelabs/aso-extensions/apis/arm/containerservice/v20260601"
)

// aksAndFleet stands in for an ASO ManagedCluster (with a system agent pool
// in the fake) and an ASO Fleet that ASO has provisioned in the namespace's
// resource group; it returns their ARM IDs and the resource group's.
func aksAndFleet(t *testing.T, ns string) (clusterID, fleetID, rgID string) {
	t.Helper()
	ctx := context.Background()
	rgID = "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	clusterID = rgID + "/providers/Microsoft.ContainerService/managedClusters/" + ns + "-aks"
	fleetID = rgID + "/providers/Microsoft.ContainerService/fleets/" + ns + "-fleet"
	for _, id := range []string{rgID, clusterID, clusterID + "/agentPools/system", fleetID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	aks := &asoaks.ManagedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "aks",
		Annotations: map[string]string{genruntime.ResourceIDAnnotation: clusterID}}}
	aks.Spec.AzureName = ns + "-aks"
	aks.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	fleet := &asofleet.Fleet{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "fleet",
		Annotations: map[string]string{genruntime.ResourceIDAnnotation: fleetID}}}
	fleet.Spec.AzureName = ns + "-fleet"
	fleet.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	if err := k8s.Create(ctx, aks); err != nil {
		t.Fatal(err)
	}
	aks.Status.Conditions = []conditions.Condition{asoReady(aks.Generation)}
	if err := k8s.Status().Update(ctx, aks); err != nil {
		t.Fatal(err)
	}
	if err := k8s.Create(ctx, fleet); err != nil {
		t.Fatal(err)
	}
	fleet.Status.Conditions = []conditions.Condition{asoReady(fleet.Generation)}
	if err := k8s.Status().Update(ctx, fleet); err != nil {
		t.Fatal(err)
	}
	return clusterID, fleetID, rgID
}

// A managed namespace of ASO's ManagedCluster (labels, quota, network
// policy), its quota changed; a managed namespace of ASO's Fleet; a node
// pool snapshot of the cluster's system pool; a private endpoint connection
// approved; all deleted.
func TestManagedNamespacesAndSnapshot(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "aks")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	clusterID, fleetID, rgID := aksAndFleet(t, ns)
	connectionID := clusterID + "/privateEndpointConnections/from-hub"
	armFake.AddResource(connectionID)

	namespaceObj := &cs.ManagedClustersManagedNamespace{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "team-a"},
		Spec: cs.ManagedClustersManagedNamespace_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "aks"},
			Location: ptr("westus"),
			Properties: &cs.NamespaceProperties{
				Labels:               map[string]string{"team": "a"},
				DefaultResourceQuota: &cs.ResourceQuota{CpuRequest: ptr("1"), MemoryLimit: ptr("2Gi")},
				DefaultNetworkPolicy: &cs.NetworkPolicies{
					Ingress: ptr(cs.NetworkPolicies_Ingress_AllowSameNamespace),
					Egress:  ptr(cs.NetworkPolicies_Egress_AllowAll),
				},
				AdoptionPolicy: ptr(cs.AdoptionPolicy_Never),
				DeletePolicy:   ptr(cs.DeletePolicy_Keep),
			},
		},
	}
	fleetNamespace := &csfleet.FleetsManagedNamespace{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "team-a-fleet"},
		Spec: csfleet.FleetsManagedNamespace_Spec{
			AzureName: "team-a",
			Owner:     &genruntime.KnownResourceReference{Name: "fleet"},
			Location:  ptr("westus"),
			Properties: &csfleet.FleetManagedNamespaceProperties{
				AdoptionPolicy:             ptr(csfleet.AdoptionPolicy_Never),
				DeletePolicy:               ptr(csfleet.DeletePolicy_Delete),
				ManagedNamespaceProperties: &csfleet.ManagedNamespaceProperties{Labels: map[string]string{"team": "a"}},
			},
		},
	}
	snapshot := &cs.Snapshot{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "system-pool"},
		Spec: cs.Snapshot_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
			CreationData: &cs.CreationData{
				SourceResourceReference: &genruntime.ResourceReference{ARMID: clusterID + "/agentPools/system"},
			},
		},
	}
	connection := &cs.ManagedClustersPrivateEndpointConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "from-hub"},
		Spec: cs.ManagedClustersPrivateEndpointConnection_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "aks"},
			PrivateLinkServiceConnectionState: &cs.PrivateLinkServiceConnectionState{
				Status: ptr(cs.ConnectionStatus_Approved), Description: ptr("the hub's endpoint"),
			},
		},
	}
	objs := []client.Object{namespaceObj, fleetNamespace, snapshot, connection}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, fmt.Sprintf("%T Ready", o), ready(o.(conditioned)))
	}

	nsID := clusterID + "/managedNamespaces/team-a"
	expectResource(t, nsID, func(r map[string]any) error {
		p := properties(r)
		quota, _ := p["defaultResourceQuota"].(map[string]any)
		policy, _ := p["defaultNetworkPolicy"].(map[string]any)
		labels, _ := p["labels"].(map[string]any)
		if r["type"] != "Microsoft.ContainerService/managedClusters/managedNamespaces" || quota["cpuRequest"] != "1" ||
			policy["ingress"] != "AllowSameNamespace" || labels["team"] != "a" || p["deletePolicy"] != "Keep" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	fleetNsID := fleetID + "/managedNamespaces/team-a"
	expectResource(t, fleetNsID, func(r map[string]any) error {
		p := properties(r)
		inner, _ := p["managedNamespaceProperties"].(map[string]any)
		labels, _ := inner["labels"].(map[string]any)
		if r["type"] != "Microsoft.ContainerService/fleets/managedNamespaces" || p["deletePolicy"] != "Delete" || labels["team"] != "a" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	snapshotID := rgID + "/providers/Microsoft.ContainerService/snapshots/system-pool"
	expectResource(t, snapshotID, func(r map[string]any) error {
		creation, _ := properties(r)["creationData"].(map[string]any)
		if r["type"] != "Microsoft.ContainerService/snapshots" || creation["sourceResourceId"] != clusterID+"/agentPools/system" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, connectionID, func(r map[string]any) error {
		state, _ := properties(r)["privateLinkServiceConnectionState"].(map[string]any)
		if state["status"] != "Approved" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	if err := k8s.Get(ctx, client.ObjectKeyFromObject(namespaceObj), namespaceObj); err != nil {
		t.Fatal(err)
	}
	namespaceObj.Spec.Properties.DefaultResourceQuota.CpuRequest = ptr("2")
	if err := k8s.Update(ctx, namespaceObj); err != nil {
		t.Fatal(err)
	}
	expectResource(t, nsID, func(r map[string]any) error {
		if quota, _ := properties(r)["defaultResourceQuota"].(map[string]any); quota["cpuRequest"] != "2" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range objs {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T gone", o), gone(o))
	}
	for _, id := range []string{nsID, fleetNsID, snapshotID, connectionID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
}

// A managed namespace the fake refuses: Kubernetes' own kube-system.
func TestManagedNamespaceRefused(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "aks-no")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	aksAndFleet(t, ns)
	obj := &cs.ManagedClustersManagedNamespace{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "kube-system"},
		Spec: cs.ManagedClustersManagedNamespace_Spec{
			Owner:      &genruntime.KnownResourceReference{Name: "aks"},
			Location:   ptr("westus"),
			Properties: &cs.NamespaceProperties{Labels: map[string]string{"team": "a"}},
		},
	}
	if err := k8s.Create(ctx, obj); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the refusal on the Ready condition", notReadyWith(obj, "is Kubernetes' own namespace"))
}
