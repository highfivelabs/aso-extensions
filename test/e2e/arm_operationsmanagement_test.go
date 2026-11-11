// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	om "github.com/highfivelabs/aso-extensions/apis/arm/operationsmanagement/v20151101preview"
)

// An Operations Management solution (the Updates gallery solution) in a
// resource group, enabled on ASO's Workspace named by object, with a
// referenced resource by ARM ID; its tags changed; deleted.
func TestOperationsManagementSolution(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "oms")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	workspaceID, rgID := logAnalytics(t, ns)
	automationID := rgID + "/providers/Microsoft.Automation/automationAccounts/auto"
	armFake.AddResource(automationID)
	name := "Updates(" + ns + "-law)"

	solution := &om.Solution{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "updates"},
		Spec: om.Solution_Spec{
			AzureName: name,
			Owner:     &genruntime.KnownResourceReference{Name: "rg"},
			Location:  ptr("westus"),
			Plan: &om.SolutionPlan{
				Name:          ptr(name),
				Publisher:     ptr("Microsoft"),
				Product:       ptr("OMSGallery/Updates"),
				PromotionCode: ptr(""),
			},
			Properties: &om.SolutionProperties{
				WorkspaceResourceReference:    &genruntime.ResourceReference{Group: "operationalinsights.azure.com", Kind: "Workspace", Name: "law"},
				ReferencedResourcesReferences: []genruntime.ResourceReference{{ARMID: automationID}},
			},
			Tags: map[string]string{"team": "a"},
		},
	}
	if err := k8s.Create(ctx, solution); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "Solution Ready", ready(solution))
	solutionID := rgID + "/providers/Microsoft.OperationsManagement/solutions/" + name
	expectResource(t, solutionID, func(r map[string]any) error {
		p := properties(r)
		plan, _ := r["plan"].(map[string]any)
		if r["type"] != "Microsoft.OperationsManagement/solutions" || p["workspaceResourceId"] != workspaceID ||
			plan["product"] != "OMSGallery/Updates" || plan["name"] != name ||
			!reflect.DeepEqual(p["referencedResources"], []any{automationID}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(solution), solution); err != nil {
		t.Fatal(err)
	}
	if solution.Status.Properties == nil || solution.Status.Properties.WorkspaceResourceId == nil || *solution.Status.Properties.WorkspaceResourceId != workspaceID {
		t.Fatalf("status: %+v", solution.Status.Properties)
	}
	solution.Spec.Tags = map[string]string{"team": "a", "tier": "1"}
	if err := k8s.Update(ctx, solution); err != nil {
		t.Fatal(err)
	}
	expectResource(t, solutionID, func(r map[string]any) error {
		if tags, _ := r["tags"].(map[string]any); tags["tier"] != "1" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	if err := k8s.Delete(ctx, solution); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "Solution gone", gone(solution))
	if armFake.HasResource(solutionID) {
		t.Fatalf("%s is still in Resource Manager", solutionID)
	}
}

// A gallery solution the fake refuses: named for another workspace.
func TestOperationsManagementSolutionRefused(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "oms-no")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	workspaceID, _ := logAnalytics(t, ns)
	solution := &om.Solution{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "updates"},
		Spec: om.Solution_Spec{
			AzureName: "Updates(elsewhere)",
			Owner:     &genruntime.KnownResourceReference{Name: "rg"},
			Location:  ptr("westus"),
			Plan:      &om.SolutionPlan{Name: ptr("Updates(elsewhere)"), Publisher: ptr("Microsoft"), Product: ptr("OMSGallery/Updates")},
			Properties: &om.SolutionProperties{
				WorkspaceResourceReference: &genruntime.ResourceReference{ARMID: workspaceID},
			},
		},
	}
	if err := k8s.Create(ctx, solution); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the refusal on the Ready condition", notReadyWith(solution, "A Microsoft gallery solution is named"))
}
