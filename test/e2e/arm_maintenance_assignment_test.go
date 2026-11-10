// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	maintenance "github.com/highfivelabs/aso-extensions/apis/arm/maintenance/v20230401"
)

// A maintenance configuration assigned two ways, each an extension resource
// (generator patch 0006: the spec defines it at four scopes): to ASO's
// VirtualMachine (owner by group and kind, the configuration by object), and
// at the subscription (owner by ARM ID) with a filter that picks machines by
// type, OS and tag (dynamic scope); the filter changed and PUT again. Refused
// on the Ready condition: a dynamic-scope assignment with no filter. Deleted.
func TestMaintenanceConfigurationAssignment(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "maint")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	vmID, rgID := vmFootprint(t, ns)
	subscription := "/subscriptions/" + subID

	window := &maintenance.MaintenanceConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "patching"},
		Spec: maintenance.MaintenanceConfiguration_Spec{
			Owner:               &genruntime.KnownResourceReference{Name: "rg"},
			Location:            ptr("westus"),
			MaintenanceScope:    ptr(maintenance.MaintenanceConfigurationProperties_MaintenanceScope_InGuestPatch),
			ExtensionProperties: map[string]string{"InGuestPatchMode": "User"},
			InstallPatches: &maintenance.InputPatchConfiguration{
				RebootSetting: ptr(maintenance.InputPatchConfiguration_RebootSetting("IfRequired")),
			},
			MaintenanceWindow: &maintenance.MaintenanceWindow{
				StartDateTime: ptr("2026-12-01 02:00"), Duration: ptr("03:00"), TimeZone: ptr("UTC"), RecurEvery: ptr("Week Saturday"),
			},
		},
	}
	configRef := &genruntime.ResourceReference{Group: "maintenance.azure.hfl.dev", Kind: "MaintenanceConfiguration", Name: "patching"}
	onVM := &maintenance.ConfigurationAssignment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "vm-patching"},
		Spec: maintenance.ConfigurationAssignment_Spec{
			AzureName:                         "patching",
			Owner:                             &genruntime.ArbitraryOwnerReference{Group: "compute.azure.com", Kind: "VirtualMachine", Name: "vm"},
			Location:                          ptr("westus"),
			MaintenanceConfigurationReference: configRef,
		},
	}
	dynamic := &maintenance.ConfigurationAssignment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "weekly"},
		Spec: maintenance.ConfigurationAssignment_Spec{
			AzureName:                         ns + "-weekly",
			Owner:                             &genruntime.ArbitraryOwnerReference{ARMID: subscription},
			Location:                          ptr("westus"),
			MaintenanceConfigurationReference: configRef,
			Filter: &maintenance.ConfigurationAssignmentFilterProperties{
				ResourceTypes: []string{"Microsoft.Compute/virtualMachines"},
				OsTypes:       []string{"Linux"},
				TagSettings: &maintenance.TagSettingsProperties{
					Tags:           map[string][]string{"patch": {"weekly"}},
					FilterOperator: ptr(maintenance.TagSettingsProperties_FilterOperator_Any),
				},
			},
		},
	}
	all := []client.Object{window, onVM, dynamic}
	for _, o := range all {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range all {
		eventually(t, timeout, fmt.Sprintf("%T %s Ready", o, o.GetName()), ready(o.(conditioned)))
	}
	windowID := rgID + "/providers/Microsoft.Maintenance/maintenanceConfigurations/patching"
	onVMID := vmID + "/providers/Microsoft.Maintenance/configurationAssignments/patching"
	dynamicID := subscription + "/providers/Microsoft.Maintenance/configurationAssignments/" + ns + "-weekly"
	expectResource(t, onVMID, func(r map[string]any) error {
		p := properties(r)
		if r["type"] != "Microsoft.Maintenance/configurationAssignments" || p["maintenanceConfigurationId"] != windowID || p["resourceId"] != vmID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, dynamicID, func(r map[string]any) error {
		p := properties(r)
		f, _ := p["filter"].(map[string]any)
		ts, _ := f["tagSettings"].(map[string]any)
		os, _ := f["osTypes"].([]any)
		if p["maintenanceConfigurationId"] != windowID || len(os) != 1 || os[0] != "Linux" || ts["filterOperator"] != "Any" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// The filter changed: PUT again.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(dynamic), dynamic); err != nil {
		t.Fatal(err)
	}
	dynamic.Spec.Filter.OsTypes = []string{"Linux", "Windows"}
	if err := k8s.Update(ctx, dynamic); err != nil {
		t.Fatal(err)
	}
	expectResource(t, dynamicID, func(r map[string]any) error {
		f, _ := properties(r)["filter"].(map[string]any)
		if os, _ := f["osTypes"].([]any); len(os) != 2 {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A dynamic scope picks machines with a filter; without one Azure refuses.
	unfiltered := &maintenance.ConfigurationAssignment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "unfiltered"},
		Spec: maintenance.ConfigurationAssignment_Spec{
			AzureName:                         ns + "-unfiltered",
			Owner:                             &genruntime.ArbitraryOwnerReference{Group: "resources.azure.com", Kind: "ResourceGroup", Name: "rg"},
			Location:                          ptr("westus"),
			MaintenanceConfigurationReference: configRef,
		},
	}
	if err := k8s.Create(ctx, unfiltered); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the unfiltered assignment on the Ready condition", notReadyWith(unfiltered, "needs a filter"))

	for _, o := range []client.Object{unfiltered, dynamic, onVM, window} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T %s gone", o, o.GetName()), gone(o))
	}
	for _, id := range []string{dynamicID, onVMID, windowID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
	if !armFake.HasResource(vmID) {
		t.Fatal("the VM is gone from Resource Manager")
	}
}
