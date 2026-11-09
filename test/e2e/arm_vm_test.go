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

	compute "github.com/highfivelabs/aso-extensions/apis/arm/compute/v20220301"
	devtestlab "github.com/highfivelabs/aso-extensions/apis/arm/devtestlab/v20180915"
	maintenance "github.com/highfivelabs/aso-extensions/apis/arm/maintenance/v20230401"
)

// vmFootprint makes the ASO footprint the VM extras hang from: a resource
// group and a VirtualMachine ASO has provisioned (stand-ins), and the same two
// in the fake Resource Manager. It returns the VM's ARM ID and the resource
// group's.
func vmFootprint(t *testing.T, ns string) (vmID, rgID string) {
	t.Helper()
	rgID = "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	vmID = rgID + "/providers/Microsoft.Compute/virtualMachines/" + ns + "-vm"
	armFake.AddResource(rgID)
	armFake.AddResource(vmID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	virtualMachine(t, ns, "vm", "rg", ns+"-rg", ns+"-vm")
	return vmID, rgID
}

// A run command on ASO's VirtualMachine (a script, a parameter, the password
// of the account it runs as from a Secret), the VM's auto-shutdown schedule
// (a DevTest Labs schedule owned by the resource group that names the VM by
// ARM ID), and a maintenance configuration: PUT with the namespace's
// identity, changed and deleted.
func TestVirtualMachineExtras(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "vm")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	vmID, rgID := vmFootprint(t, ns)
	secret(t, ns, "runas", map[string]string{"password": "runas-password-1"})

	run := &compute.VirtualMachinesRunCommand{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "hello"},
		Spec: compute.VirtualMachinesRunCommand_Spec{
			AzureName:               "hello",
			Owner:                   &genruntime.KnownResourceReference{Name: "vm"},
			Location:                ptr("westus"),
			Source:                  &compute.VirtualMachineRunCommandScriptSource{Script: ptr("echo hello")},
			Parameters:              []compute.RunCommandInputParameter{{Name: ptr("who"), Value: ptr("world")}},
			RunAsUser:               ptr("deploy"),
			TimeoutInSeconds:        ptr(60),
			RunAsPasswordFromSecret: &genruntime.SecretReference{Name: "runas", Key: "password"},
		},
	}
	shutdown := &devtestlab.Schedule{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "shutdown"},
		Spec: devtestlab.Schedule_Spec{
			AzureName:               "shutdown-computevm-" + ns + "-vm",
			Owner:                   &genruntime.KnownResourceReference{Name: "rg"},
			Location:                ptr("westus"),
			TaskType:                ptr("ComputeVmShutdownTask"),
			Status:                  ptr(devtestlab.ScheduleProperties_Status_Enabled),
			TimeZoneId:              ptr("UTC"),
			DailyRecurrence:         &devtestlab.DayDetails{Time: ptr("1900")},
			TargetResourceReference: &genruntime.ResourceReference{ARMID: vmID},
			NotificationSettings: &devtestlab.NotificationSettings{
				Status:        ptr(devtestlab.NotificationSettings_Status_Disabled),
				TimeInMinutes: ptr(30),
			},
		},
	}
	window := &maintenance.MaintenanceConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "patching"},
		Spec: maintenance.MaintenanceConfiguration_Spec{
			AzureName:           "patching",
			Owner:               &genruntime.KnownResourceReference{Name: "rg"},
			Location:            ptr("westus"),
			MaintenanceScope:    ptr(maintenance.MaintenanceConfigurationProperties_MaintenanceScope_InGuestPatch),
			ExtensionProperties: map[string]string{"InGuestPatchMode": "User"},
			InstallPatches: &maintenance.InputPatchConfiguration{
				RebootSetting: ptr(maintenance.InputPatchConfiguration_RebootSetting("IfRequired")),
			},
			MaintenanceWindow: &maintenance.MaintenanceWindow{
				StartDateTime: ptr("2026-12-01 02:00"),
				Duration:      ptr("03:00"),
				TimeZone:      ptr("UTC"),
				RecurEvery:    ptr("Week Saturday"),
			},
		},
	}
	all := []client.Object{run, shutdown, window}
	for _, o := range all {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range all {
		eventually(t, timeout, fmt.Sprintf("%T Ready", o), ready(o.(conditioned)))
	}
	runID := vmID + "/runCommands/hello"
	shutdownID := rgID + "/providers/Microsoft.DevTestLab/schedules/shutdown-computevm-" + ns + "-vm"
	windowID := rgID + "/providers/Microsoft.Maintenance/maintenanceConfigurations/patching"
	expectResource(t, runID, func(r map[string]any) error {
		p := properties(r)
		src, _ := p["source"].(map[string]any)
		params, _ := p["parameters"].([]any)
		if r["type"] != "Microsoft.Compute/virtualMachines/runCommands" || src["script"] != "echo hello" || len(params) != 1 ||
			p["runAsUser"] != "deploy" || p["runAsPassword"] != "runas-password-1" || p["timeoutInSeconds"] != float64(60) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, shutdownID, func(r map[string]any) error {
		p := properties(r)
		daily, _ := p["dailyRecurrence"].(map[string]any)
		if p["taskType"] != "ComputeVmShutdownTask" || p["status"] != "Enabled" || p["targetResourceId"] != vmID || daily["time"] != "1900" || p["timeZoneId"] != "UTC" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, windowID, func(r map[string]any) error {
		p := properties(r)
		w, _ := p["maintenanceWindow"].(map[string]any)
		ext, _ := p["extensionProperties"].(map[string]any)
		if p["maintenanceScope"] != "InGuestPatch" || w["duration"] != "03:00" || w["recurEvery"] != "Week Saturday" || !reflect.DeepEqual(ext, map[string]any{"InGuestPatchMode": "User"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change to the schedule is PUT again.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(shutdown), shutdown); err != nil {
		t.Fatal(err)
	}
	shutdown.Spec.DailyRecurrence = &devtestlab.DayDetails{Time: ptr("2000")}
	if err := k8s.Update(ctx, shutdown); err != nil {
		t.Fatal(err)
	}
	expectResource(t, shutdownID, func(r map[string]any) error {
		daily, _ := properties(r)["dailyRecurrence"].(map[string]any)
		if daily["time"] != "2000" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range []client.Object{run, shutdown, window} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T gone", o), gone(o))
	}
	for _, id := range []string{runID, shutdownID, windowID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
	if !armFake.HasResource(vmID) {
		t.Fatal("the VM is gone from Resource Manager")
	}
}

// The CRD refuses a maintenance scope and a schedule status that do not exist.
func TestVirtualMachineExtrasValidation(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "vm-validation")
	bad := &maintenance.MaintenanceConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "bad"},
		Spec: maintenance.MaintenanceConfiguration_Spec{
			AzureName: "bad", Owner: &genruntime.KnownResourceReference{Name: "rg"},
			MaintenanceScope: ptr(maintenance.MaintenanceConfigurationProperties_MaintenanceScope("Everything")),
		},
	}
	if err := k8s.Create(ctx, bad); err == nil || !contains(err.Error(), "Unsupported value") {
		t.Fatalf("want a refusal of the scope, got %v", err)
	}
	badStatus := &devtestlab.Schedule{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "bad"},
		Spec: devtestlab.Schedule_Spec{
			AzureName: "bad", Owner: &genruntime.KnownResourceReference{Name: "rg"},
			Status: ptr(devtestlab.ScheduleProperties_Status("Maybe")),
		},
	}
	if err := k8s.Create(ctx, badStatus); err == nil || !contains(err.Error(), "Unsupported value") {
		t.Fatalf("want a refusal of the status, got %v", err)
	}
}
