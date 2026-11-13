// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	asoinsights "github.com/Azure/azure-service-operator/v2/api/insights/v1api20200202/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"

	insightslsa "github.com/highfivelabs/aso-extensions/apis/arm/insights/v20200301preview"
	identity "github.com/highfivelabs/aso-extensions/apis/arm/managedidentity/v20241130"
)

// appInsightsComponent stands in for an ASO insights.azure.com Component ASO
// has provisioned, in the resource group rg.
func appInsightsComponent(t *testing.T, ns, name, rg, rgAzureName, azureName string) {
	t.Helper()
	ctx := context.Background()
	c := &asoinsights.Component{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name,
		Annotations: map[string]string{genruntime.ResourceIDAnnotation: "/subscriptions/" + subID + "/resourceGroups/" + rgAzureName +
			"/providers/Microsoft.Insights/components/" + azureName}}}
	c.Spec.AzureName = azureName
	c.Spec.Owner = &genruntime.KnownResourceReference{Name: rg}
	if err := k8s.Create(ctx, c); err != nil {
		t.Fatal(err)
	}
	c.Status.Conditions = []conditions.Condition{asoReady(c.Generation)}
	if err := k8s.Status().Update(ctx, c); err != nil {
		t.Fatal(err)
	}
}

// Our UserAssignedIdentity (2024-11-30), beside ASO's (2023-01-31), for
// isolationScope: PUT with it, a change PUT again, deleted.
func TestUserAssignedIdentityIsolationScope(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "uai-scope")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")

	uai := &identity.UserAssignedIdentity{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "workload"},
		Spec: identity.UserAssignedIdentity_Spec{
			Owner:          &genruntime.KnownResourceReference{Name: "rg"},
			Location:       ptr("westus"),
			IsolationScope: ptr(identity.IsolationScope("Regional")),
		},
	}
	if err := k8s.Create(ctx, uai); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "UserAssignedIdentity Ready", ready(uai))
	id := rgID + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/workload"
	expectResource(t, id, func(r map[string]any) error {
		if r["type"] != "Microsoft.ManagedIdentity/userAssignedIdentities" || properties(r)["isolationScope"] != "Regional" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	if err := k8s.Get(ctx, client.ObjectKeyFromObject(uai), uai); err != nil {
		t.Fatal(err)
	}
	uai.Spec.IsolationScope = ptr(identity.IsolationScope("None"))
	if err := k8s.Update(ctx, uai); err != nil {
		t.Fatal(err)
	}
	expectResource(t, id, func(r map[string]any) error {
		if properties(r)["isolationScope"] != "None" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	if err := k8s.Delete(ctx, uai); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "UserAssignedIdentity gone", gone(uai))
	if armFake.HasResource(id) {
		t.Fatalf("%s is still in Resource Manager", id)
	}
}

// An Application Insights component's linked storage account (the
// profiler's, the fixed name ServiceProfiler), owned by ASO's Component: the
// storage account by ARM ID; deleting the object unlinks it.
func TestComponentsLinkedStorageAccount(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "ai-linked")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	componentID := rgID + "/providers/Microsoft.Insights/components/" + ns + "-ai"
	storageID := rgID + "/providers/Microsoft.Storage/storageAccounts/profiler"
	armFake.AddResource(rgID)
	armFake.AddResource(componentID)
	armFake.AddResource(storageID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	appInsightsComponent(t, ns, "ai", "rg", ns+"-rg", ns+"-ai")

	link := &insightslsa.ComponentsLinkedStorageAccount{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "profiler"},
		Spec: insightslsa.ComponentsLinkedStorageAccount_Spec{
			Owner:                         &genruntime.KnownResourceReference{Name: "ai"},
			LinkedStorageAccountReference: &genruntime.ResourceReference{ARMID: storageID},
		},
	}
	if err := k8s.Create(ctx, link); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "linked storage account Ready", ready(link))
	id := componentID + "/linkedStorageAccounts/ServiceProfiler"
	expectResource(t, id, func(r map[string]any) error {
		if properties(r)["linkedStorageAccount"] != storageID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	if err := k8s.Delete(ctx, link); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "linked storage account gone", gone(link))
	if armFake.HasResource(id) {
		t.Fatalf("%s is still in Resource Manager", id)
	}
	if !armFake.HasResource(componentID) {
		t.Fatalf("the component %s was deleted with its link", componentID)
	}
}
