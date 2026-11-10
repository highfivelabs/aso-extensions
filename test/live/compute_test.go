// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

//go:build live

package live

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	asocompute "github.com/Azure/azure-service-operator/v2/api/compute/v20220301/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"

	compute "github.com/highfivelabs/aso-extensions/apis/arm/compute/v20220301"
)

// A run command on a small Linux VM (a fixture, two vCPUs, about $0.03
// an hour, no public IP), owned by a stand-in for ASO's VirtualMachine.
func TestLiveVirtualMachineRunCommand(t *testing.T) {
	ns := namespace(t, "vm")
	vnetID := vnet(t, azName("vmnet"), "10.70.0.0/16", map[string]string{"default": "10.70.0.0/24"})
	nicID := rgID() + "/providers/Microsoft.Network/networkInterfaces/" + azName("vmnic")
	fixture(t, nicID, "2024-05-01", map[string]any{
		"location": location,
		"properties": map[string]any{"ipConfigurations": []any{map[string]any{"name": "default", "properties": map[string]any{
			"subnet": map[string]any{"id": vnetID + "/subnets/default"}, "privateIPAllocationMethod": "Dynamic",
		}}}},
	})
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	vmName := azName("vm")
	vmID := rgID() + "/providers/Microsoft.Compute/virtualMachines/" + vmName
	// Small sizes in order; a new subscription is refused some sizes for
	// capacity (Standard_B1s in eastus2, 2026-11-10: SkuNotAvailable).
	var made bool
	for _, size := range []string{"Standard_B2ats_v2", "Standard_B2als_v2", "Standard_D2als_v6", "Standard_D2as_v5", "Standard_B1s"} {
		vmBody := map[string]any{
			"location": location,
			"properties": map[string]any{
				"hardwareProfile": map[string]any{"vmSize": size},
				"storageProfile": map[string]any{
					"imageReference": map[string]any{"publisher": "Canonical", "offer": "ubuntu-24_04-lts", "sku": "server", "version": "latest"},
					"osDisk":         map[string]any{"createOption": "FromImage", "deleteOption": "Delete", "managedDisk": map[string]any{"storageAccountType": "Standard_LRS"}},
				},
				"osProfile": map[string]any{
					"computerName": "live", "adminUsername": "live",
					"adminPassword":      "Aa1!" + hex.EncodeToString(b),
					"linuxConfiguration": map[string]any{"disablePasswordAuthentication": false},
				},
				"networkProfile": map[string]any{"networkInterfaces": []any{map[string]any{"id": nicID, "properties": map[string]any{"deleteOption": "Detach"}}}},
			},
		}
		_, _, err := azure.do(context.Background(), "PUT", vmID, "2024-07-01", vmBody)
		if err != nil && strings.Contains(err.Error(), "SkuNotAvailable") {
			t.Logf("%s not available: trying the next size", size)
			continue
		}
		if err != nil {
			t.Fatalf("fixture VM: %v", err)
		}
		t.Logf("VM size %s", size)
		made = true
		break
	}
	if !made {
		t.Fatal("no VM size available in " + location)
	}
	t.Cleanup(func() { deleteARM(t, vmID, "2024-07-01") })
	eventually(t, timeout, "fixture VM Succeeded", func() error {
		_, r, err := azure.get(context.Background(), vmID, "2024-07-01")
		if err != nil {
			return err
		}
		if s := state(r); s != "Succeeded" {
			return fmt.Errorf("provisioningState %s", s)
		}
		return nil
	})

	ctx := context.Background()
	vm := &asocompute.VirtualMachine{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "vm",
		Annotations: map[string]string{genruntime.ResourceIDAnnotation: vmID}}}
	vm.Spec.AzureName = vmName
	vm.Spec.Owner = owner("rg")
	if err := k8s.Create(ctx, vm); err != nil {
		t.Fatal(err)
	}
	vm.Status.Conditions = []conditions.Condition{{
		Type: conditions.ConditionTypeReady, Status: metav1.ConditionTrue, Severity: conditions.ConditionSeverityNone,
		Reason: conditions.ReasonSucceeded, LastTransitionTime: metav1.Now(), ObservedGeneration: vm.Generation,
	}}
	if err := k8s.Status().Update(ctx, vm); err != nil {
		t.Fatal(err)
	}

	run := &compute.VirtualMachinesRunCommand{}
	run.Namespace, run.Name = ns, "hello"
	run.Spec = compute.VirtualMachinesRunCommand_Spec{
		AzureName: "hello", Owner: owner("vm"), Location: ptr(location),
		Source:           &compute.VirtualMachineRunCommandScriptSource{Script: ptr("echo hello $who")},
		Parameters:       []compute.RunCommandInputParameter{{Name: ptr("who"), Value: ptr("world")}},
		TimeoutInSeconds: ptr(120),
	}
	create(t, timeout, run)
	id := vmID + "/runCommands/hello"
	expectResource(t, id, "2022-03-01", func(r map[string]any) error {
		src, _ := properties(r)["source"].(map[string]any)
		if src["script"] != "echo hello $who" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, run, func(c *compute.VirtualMachinesRunCommand) { c.Spec.Source.Script = ptr("echo bye $who") })
	eventually(t, timeout, "run command Ready after the change", ready(run))
	expectResource(t, id, "2022-03-01", func(r map[string]any) error {
		if src, _ := properties(r)["source"].(map[string]any); src["script"] != "echo bye $who" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, run)
	expectGone(t, id, "2022-03-01")
}
