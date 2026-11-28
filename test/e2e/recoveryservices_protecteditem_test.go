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

	rs "github.com/highfivelabs/aso-extensions/apis/arm/recoveryservices/v20260701"
	rsv "github.com/highfivelabs/aso-extensions/apis/recoveryservices/v20260701"
)

// Backup turned on for ASO's VirtualMachine in our Recovery Services vault,
// with our backup policy: the hand-written VaultsProtectedItem names its
// container and item from the machine, refreshes the vault's containers
// before its first PUT (the fake refuses an undiscovered machine), follows
// the long-running PUT and DELETE, PUTs again on a change of disks and of
// state, and is deleted before the vault (which the fake refuses to delete
// while it holds an item).
func TestVaultsProtectedItem(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "rspi")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	vmID := rgID + "/providers/Microsoft.Compute/virtualMachines/app1"
	armFake.AddResource(vmID)
	virtualMachine(t, ns, "vm", "rg", ns+"-rg", "app1")

	vault := &rs.Vault{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "vault"},
		Spec: rs.Vault_Spec{
			AzureName: "backups", Owner: &genruntime.KnownResourceReference{Name: "rg"}, Location: ptr("westus"),
			Sku: &rs.Sku{Name: ptr(rs.SkuName_RS0), Tier: ptr("Standard")},
		},
	}
	policy := &rs.VaultsBackupPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "daily"},
		Spec: rs.VaultsBackupPolicy_Spec{
			AzureName: "daily-vm", Owner: &genruntime.KnownResourceReference{Name: "vault"}, Location: ptr("westus"),
			Properties: &rs.ProtectionPolicy{AzureIaasVM: &rs.AzureIaaSVMProtectionPolicy{
				BackupManagementType: ptr(rs.AzureIaaSVMProtectionPolicy_BackupManagementType_AzureIaasVM),
				SchedulePolicy: &rs.SchedulePolicy{SimpleSchedulePolicy: &rs.SimpleSchedulePolicy{
					SchedulePolicyType:   ptr(rs.SimpleSchedulePolicy_SchedulePolicyType("SimpleSchedulePolicy")),
					ScheduleRunFrequency: ptr(rs.ScheduleRunType_Daily),
					ScheduleRunTimes:     []string{"2026-01-01T02:00:00Z"},
				}},
			}},
		},
	}
	item := &rsv.VaultsProtectedItem{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "app1-backup"},
		Spec: rsv.VaultsProtectedItem_Spec{
			Owner:                   &genruntime.KnownResourceReference{Name: "vault"},
			SourceResourceReference: &genruntime.ResourceReference{Group: "compute.azure.com", Kind: "VirtualMachine", Name: "vm"},
			PolicyReference:         &genruntime.ResourceReference{Group: "recoveryservices.azure.hfl.dev", Kind: "VaultsBackupPolicy", Name: "daily"},
		},
	}
	defer func() {
		if t.Failed() {
			for _, l := range armFake.Log {
				if contains(l, ns) {
					t.Log("fake resource manager: " + l)
				}
			}
			_ = k8s.Get(ctx, client.ObjectKeyFromObject(item), item)
			t.Logf("item: annotations %v, spec %+v, status %+v", item.Annotations, item.Spec, item.Status)
		}
	}()
	// All at once, as a chart applies them: the item waits for the vault
	// and the policy.
	for _, o := range []client.Object{vault, policy, item} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "VaultsProtectedItem Ready", ready(item))

	vaultID := rgID + "/providers/Microsoft.RecoveryServices/vaults/backups"
	policyID := vaultID + "/backupPolicies/daily-vm"
	containerID := vaultID + "/backupFabrics/Azure/protectionContainers/iaasvmcontainer;iaasvmcontainerv2;" + ns + "-rg;app1"
	itemID := containerID + "/protectedItems/vm;iaasvmcontainerv2;" + ns + "-rg;app1"
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(item), item); err != nil {
		t.Fatal(err)
	}
	if item.Spec.ContainerName != "iaasvmcontainer;iaasvmcontainerv2;"+ns+"-rg;app1" || item.Spec.AzureName != "vm;iaasvmcontainerv2;"+ns+"-rg;app1" {
		t.Errorf("names not derived from the machine: container %q, item %q", item.Spec.ContainerName, item.Spec.AzureName)
	}
	if got := item.Annotations[genruntime.ResourceIDAnnotation]; got != itemID {
		t.Errorf("resource ID %q, want %q", got, itemID)
	}
	if !strings.EqualFold(item.Status.Id, itemID) || item.Status.ProtectionState != "IRPending" || item.Status.PolicyId != policyID ||
		item.Status.SourceResourceId != vmID || item.Status.ContainerName != "iaasvmcontainerv2;"+ns+"-rg;app1" || item.Status.WorkloadType != "VM" {
		t.Errorf("status: %+v", item.Status)
	}
	if !armFake.HasResource(containerID) {
		t.Error("the machine's protection container was not registered")
	}
	refreshed := false
	for _, l := range armFake.Log {
		if strings.HasPrefix(l, "POST "+vaultID+"/backupFabrics/Azure/refreshContainers ") {
			refreshed = true
		}
	}
	if !refreshed {
		t.Error("the vault's containers were not refreshed before the first PUT")
	}

	// Only the first data disk, then protection stopped (data kept).
	if err := change(t, item, func(item *rsv.VaultsProtectedItem) {
		item.Spec.DiskExclusionProperties = &rsv.DiskExclusionProperties{DiskLunList: []int{0}, IsInclusionList: ptr(true)}
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, itemID, func(r map[string]any) error {
		ext, _ := properties(r)["extendedProperties"].(map[string]any)
		d, _ := ext["diskExclusionProperties"].(map[string]any)
		if d["isInclusionList"] != true || fmt.Sprint(d["diskLunList"]) != "[0]" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if err := change(t, item, func(item *rsv.VaultsProtectedItem) {
		item.Spec.ProtectionState = ptr("ProtectionStopped")
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, itemID, func(r map[string]any) error {
		if properties(r)["protectionState"] != "ProtectionStopped" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	eventually(t, timeout, "VaultsProtectedItem Ready, stopped", func() error {
		if err := ready(item)(); err != nil {
			return err
		}
		if item.Status.ProtectionState != "ProtectionStopped" {
			return fmt.Errorf("status protectionState %q", item.Status.ProtectionState)
		}
		return nil
	})

	// The item goes first; the vault refuses to go while it holds one.
	for _, o := range []client.Object{item, policy, vault} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T gone", o), gone(o))
	}
	if armFake.HasResource(itemID) || armFake.HasResource(containerID) || armFake.HasResource(vaultID) {
		t.Fatal("the item, its container or the vault is still in Resource Manager")
	}
}

// What is not a virtual machine is refused when the item is claimed: the
// kind backs up virtual machines only, so far.
func TestVaultsProtectedItemRefusesOtherSources(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "rspx")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	vault := &rs.Vault{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "vault"},
		Spec: rs.Vault_Spec{
			AzureName: "backups", Owner: &genruntime.KnownResourceReference{Name: "rg"}, Location: ptr("westus"),
			Sku: &rs.Sku{Name: ptr(rs.SkuName_RS0), Tier: ptr("Standard")},
		},
	}
	item := &rsv.VaultsProtectedItem{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "share"},
		Spec: rsv.VaultsProtectedItem_Spec{
			Owner:                   &genruntime.KnownResourceReference{Name: "vault"},
			SourceResourceReference: &genruntime.ResourceReference{ARMID: rgID + "/providers/Microsoft.Storage/storageAccounts/files"},
			PolicyReference:         &genruntime.ResourceReference{ARMID: rgID + "/providers/Microsoft.RecoveryServices/vaults/backups/backupPolicies/p"},
		},
	}
	for _, o := range []client.Object{vault, item} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "VaultsProtectedItem refused", notReadyWith(item, "not a virtual machine"))
	for _, o := range []client.Object{item, vault} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T gone", o), gone(o))
	}
}
