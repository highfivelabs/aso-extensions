// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	containerservice "github.com/highfivelabs/aso-extensions/apis/arm/containerservice/v20250701"
	costmanagement "github.com/highfivelabs/aso-extensions/apis/arm/costmanagement/v20260601"
	insights2018 "github.com/highfivelabs/aso-extensions/apis/arm/insights/v20180601preview"
	insights2021 "github.com/highfivelabs/aso-extensions/apis/arm/insights/v20210308"
	insights2023 "github.com/highfivelabs/aso-extensions/apis/arm/insights/v20230301preview"
	opsmgmt "github.com/highfivelabs/aso-extensions/apis/arm/operationsmanagement/v20151101preview"
)

// DeploymentSafeguard on an AKS cluster, updating level from Warn to Enforce.
func TestDeploymentSafeguard(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "dep-safe")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	aksID := rgID + "/providers/Microsoft.ContainerService/managedClusters/aks-1"
	armFake.AddResource(rgID)
	armFake.AddResource(aksID)

	ds := &containerservice.DeploymentSafeguard{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "default"},
		Spec: containerservice.DeploymentSafeguard_Spec{
			Owner:              &genruntime.ArbitraryOwnerReference{ARMID: aksID},
			Level:              ptr(containerservice.DeploymentSafeguardsLevel_Warn),
			ExcludedNamespaces: []string{"kube-system"},
		},
	}
	if err := k8s.Create(ctx, ds); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "DeploymentSafeguard Ready", ready(ds))

	dsID := aksID + "/providers/Microsoft.ContainerService/deploymentSafeguards/default"
	expectResource(t, dsID, func(r map[string]any) error {
		props, _ := r["properties"].(map[string]any)
		if props["level"] != "Warn" {
			return fmt.Errorf("unexpected DeploymentSafeguard: %v", r)
		}
		return nil
	})

	if err := k8s.Get(ctx, client.ObjectKeyFromObject(ds), ds); err != nil {
		t.Fatal(err)
	}
	ds.Spec.Level = ptr(containerservice.DeploymentSafeguardsLevel_Enforce)
	if err := k8s.Update(ctx, ds); err != nil {
		t.Fatal(err)
	}
	expectResource(t, dsID, func(r map[string]any) error {
		props, _ := r["properties"].(map[string]any)
		if props["level"] != "Enforce" {
			return fmt.Errorf("unexpected updated DeploymentSafeguard: %v", r)
		}
		return nil
	})

	if err := k8s.Delete(ctx, ds); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "DeploymentSafeguard gone", gone(ds))
}

// ManagementAssociation and ManagementConfiguration in OperationsManagement.
func TestOperationsManagementAssociationAndConfig(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "ops-mgmt")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	resourceGroup(t, ns, "rg", ns+"-rg")
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	vmID := rgID + "/providers/Microsoft.Compute/virtualMachines/vm-1"
	armFake.AddResource(rgID)
	armFake.AddResource(vmID)

	assoc := &opsmgmt.ManagementAssociation{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "assoc-1"},
		Spec: opsmgmt.ManagementAssociation_Spec{
			Owner:    &genruntime.ArbitraryOwnerReference{ARMID: vmID},
			Location: ptr("westus"),
			Properties: &opsmgmt.ManagementAssociationProperties{
				ApplicationId: ptr(vmID),
			},
		},
	}
	cfg := &opsmgmt.ManagementConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cfg-1"},
		Spec: opsmgmt.ManagementConfiguration_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
			Properties: &opsmgmt.ManagementConfigurationProperties{
				ApplicationId:      ptr(vmID),
				ParentResourceType: ptr("Microsoft.Compute/virtualMachines"),
				Parameters: []opsmgmt.ArmTemplateParameter{
					{Name: ptr("env"), Value: ptr("test")},
				},
				Template: map[string]v1.JSON{
					"schema": {Raw: []byte(`"https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#"`)},
				},
			},
		},
	}
	for _, obj := range []client.Object{assoc, cfg} {
		if err := k8s.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "ManagementAssociation Ready", ready(assoc))
	eventually(t, timeout, "ManagementConfiguration Ready", ready(cfg))

	assocID := vmID + "/providers/Microsoft.OperationsManagement/ManagementAssociations/assoc-1"
	cfgID := rgID + "/providers/Microsoft.OperationsManagement/ManagementConfigurations/cfg-1"
	if !armFake.HasResource(assocID) {
		t.Fatalf("%s missing from Resource Manager", assocID)
	}
	if !armFake.HasResource(cfgID) {
		t.Fatalf("%s missing from Resource Manager", cfgID)
	}

	for _, obj := range []client.Object{assoc, cfg} {
		if err := k8s.Delete(ctx, obj); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, obj.GetName()+" gone", gone(obj))
	}
}

// CostManagement Budget at subscription scope.
func TestCostManagementBudget(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "cost-bgt")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	scopeID := "/subscriptions/" + subID
	armFake.AddResource(scopeID)

	budget := &costmanagement.Budget{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "monthly-budget"},
		Spec: costmanagement.Budget_Spec{
			Owner:     &genruntime.ArbitraryOwnerReference{ARMID: scopeID},
			Category:  ptr(costmanagement.CategoryType_Cost),
			Amount:    ptr(250.0),
			TimeGrain: ptr(costmanagement.TimeGrainType_Monthly),
			TimePeriod: &costmanagement.BudgetTimePeriod{
				StartDate: ptr("2026-01-01T00:00:00Z"),
			},
		},
	}
	if err := k8s.Create(ctx, budget); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "Budget Ready", ready(budget))

	budgetID := scopeID + "/providers/Microsoft.CostManagement/budgets/monthly-budget"
	expectResource(t, budgetID, func(r map[string]any) error {
		props, _ := r["properties"].(map[string]any)
		if props["category"] != "Cost" {
			return fmt.Errorf("unexpected Budget: %v", r)
		}
		return nil
	})

	if err := k8s.Delete(ctx, budget); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "Budget gone", gone(budget))
}

// Insights MyWorkbook, GuestDiagnosticSetting, GuestDiagnosticSettingsAssociation,
// and TenantActionGroup.
func TestInsightsFoundationBatch2(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "ins-b2")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	resourceGroup(t, ns, "rg", ns+"-rg")
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	vmID := rgID + "/providers/Microsoft.Compute/virtualMachines/vm-1"
	mgID := "/providers/Microsoft.Management/managementGroups/ins-mg"
	armFake.AddResource(rgID)
	armFake.AddResource(vmID)
	armFake.AddResource(mgID)

	wb := &insights2021.MyWorkbook{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "my-wb"},
		Spec: insights2021.MyWorkbook_Spec{
			Owner:          &genruntime.KnownResourceReference{Name: "rg"},
			Location:       ptr("westus"),
			DisplayName:    ptr("My Test Workbook"),
			Category:       ptr("workbook"),
			SerializedData: ptr(`{"version":"Notebook/1.0"}`),
		},
	}
	gds := &insights2018.GuestDiagnosticSetting{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "guest-diag"},
		Spec: insights2018.GuestDiagnosticSetting_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
			OsType:   ptr(insights2018.GuestDiagnosticSettings_OsType_Linux),
		},
	}
	gda := &insights2018.GuestDiagnosticSettingsAssociation{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "guest-assoc"},
		Spec: insights2018.GuestDiagnosticSettingsAssociation_Spec{
			Owner:                       &genruntime.ArbitraryOwnerReference{ARMID: vmID},
			Location:                    ptr("westus"),
			GuestDiagnosticSettingsName: ptr("guest-diag"),
		},
	}
	tag := &insights2023.TenantActionGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "tenant-ag"},
		Spec: insights2023.TenantActionGroup_Spec{
			Owner:          &genruntime.ArbitraryOwnerReference{ARMID: mgID},
			Location:       ptr("Global"),
			Enabled:        ptr(true),
			GroupShortName: ptr("tenantag"),
		},
	}

	for _, obj := range []client.Object{wb, gds, gda, tag} {
		if err := k8s.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "MyWorkbook Ready", ready(wb))
	eventually(t, timeout, "GuestDiagnosticSetting Ready", ready(gds))
	eventually(t, timeout, "GuestDiagnosticSettingsAssociation Ready", ready(gda))
	eventually(t, timeout, "TenantActionGroup Ready", ready(tag))

	tagID := mgID + "/providers/Microsoft.Insights/tenantActionGroups/tenant-ag"
	expectResource(t, tagID, func(r map[string]any) error {
		props, _ := r["properties"].(map[string]any)
		if props["groupShortName"] != "tenantag" {
			return fmt.Errorf("unexpected TenantActionGroup: %v", r)
		}
		return nil
	})

	for _, obj := range []client.Object{wb, gds, gda, tag} {
		if err := k8s.Delete(ctx, obj); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, obj.GetName()+" gone", gone(obj))
	}
}
