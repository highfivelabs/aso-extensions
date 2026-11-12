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

	rs "github.com/highfivelabs/aso-extensions/apis/arm/recoveryservices/v20260701"
)

// A Recovery Services vault owned by the resource group, and a backup policy
// owned by our vault (the owner chain runs through our kind): PUT with the
// namespace's identity, changed, and deleted policy first.
func TestRecoveryServicesVaultAndPolicy(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "rsv")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")

	vault := &rs.Vault{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "vault"},
		Spec: rs.Vault_Spec{
			AzureName: "backups",
			Owner:     &genruntime.KnownResourceReference{Name: "rg"},
			Location:  ptr("westus"),
			Sku:       &rs.Sku{Name: ptr(rs.SkuName_RS0), Tier: ptr("Standard")},
			Identity:  &rs.IdentityData{Type: ptr(rs.ResourceIdentityType("SystemAssigned"))},
			Properties: &rs.VaultProperties{
				PublicNetworkAccess: ptr(rs.PublicNetworkAccess("Enabled")),
				RedundancySettings: &rs.VaultPropertiesRedundancySettings{
					StandardTierStorageRedundancy: ptr(rs.StandardTierStorageRedundancy_LocallyRedundant),
					CrossRegionRestore:            ptr(rs.CrossRegionRestore("Disabled")),
				},
			},
		},
	}
	if err := k8s.Create(ctx, vault); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "Vault Ready", ready(vault))
	vaultID := rgID + "/providers/Microsoft.RecoveryServices/vaults/backups"
	expectResource(t, vaultID, func(r map[string]any) error {
		p := properties(r)
		red, _ := p["redundancySettings"].(map[string]any)
		sku, _ := r["sku"].(map[string]any)
		id, _ := r["identity"].(map[string]any)
		if r["type"] != "Microsoft.RecoveryServices/vaults" || sku["name"] != "RS0" || id["type"] != "SystemAssigned" ||
			red["standardTierStorageRedundancy"] != "LocallyRedundant" || p["publicNetworkAccess"] != "Enabled" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	policy := &rs.VaultsBackupPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "daily"},
		Spec: rs.VaultsBackupPolicy_Spec{
			AzureName: "daily-vm",
			Owner:     &genruntime.KnownResourceReference{Name: "vault"},
			Location:  ptr("westus"),
			Properties: &rs.ProtectionPolicy{AzureIaasVM: &rs.AzureIaaSVMProtectionPolicy{
				BackupManagementType: ptr(rs.AzureIaaSVMProtectionPolicy_BackupManagementType_AzureIaasVM),
				PolicyType:           ptr(rs.IAASVMPolicyType_V1),
				TimeZone:             ptr("UTC"),
				SchedulePolicy: &rs.SchedulePolicy{SimpleSchedulePolicy: &rs.SimpleSchedulePolicy{
					SchedulePolicyType:   ptr(rs.SimpleSchedulePolicy_SchedulePolicyType("SimpleSchedulePolicy")),
					ScheduleRunFrequency: ptr(rs.ScheduleRunType_Daily),
					ScheduleRunTimes:     []string{"2026-01-01T02:00:00Z"},
				}},
				RetentionPolicy: &rs.RetentionPolicy{LongTerm: &rs.LongTermRetentionPolicy{
					RetentionPolicyType: ptr(rs.LongTermRetentionPolicy_RetentionPolicyType_LongTermRetentionPolicy),
					DailySchedule: &rs.DailyRetentionSchedule{
						RetentionTimes:    []string{"2026-01-01T02:00:00Z"},
						RetentionDuration: &rs.RetentionDuration{Count: ptr(30), DurationType: ptr(rs.RetentionDurationType_Days)},
					},
				}},
			}},
		},
	}
	if err := k8s.Create(ctx, policy); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "VaultsBackupPolicy Ready", ready(policy))
	policyID := vaultID + "/backupPolicies/daily-vm"
	expectResource(t, policyID, func(r map[string]any) error {
		p := properties(r)
		sched, _ := p["schedulePolicy"].(map[string]any)
		ret, _ := p["retentionPolicy"].(map[string]any)
		daily, _ := ret["dailySchedule"].(map[string]any)
		dur, _ := daily["retentionDuration"].(map[string]any)
		if r["type"] != "Microsoft.RecoveryServices/vaults/backupPolicies" || p["backupManagementType"] != "AzureIaasVM" ||
			sched["schedulePolicyType"] != "SimpleSchedulePolicy" || sched["scheduleRunFrequency"] != "Daily" ||
			ret["retentionPolicyType"] != "LongTermRetentionPolicy" || dur["count"] != float64(30) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A longer retention is PUT again.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(policy), policy); err != nil {
		t.Fatal(err)
	}
	policy.Spec.Properties.AzureIaasVM.RetentionPolicy.LongTerm.DailySchedule.RetentionDuration.Count = ptr(60)
	if err := k8s.Update(ctx, policy); err != nil {
		t.Fatal(err)
	}
	expectResource(t, policyID, func(r map[string]any) error {
		ret, _ := properties(r)["retentionPolicy"].(map[string]any)
		daily, _ := ret["dailySchedule"].(map[string]any)
		dur, _ := daily["retentionDuration"].(map[string]any)
		if dur["count"] != float64(60) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range []client.Object{policy, vault} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T gone", o), gone(o))
	}
	if armFake.HasResource(vaultID) || armFake.HasResource(policyID) {
		t.Fatal("the vault or its policy is still in Resource Manager")
	}
}

// A resource guard proxy on our vault, naming a resource guard: Azure
// refuses its DELETE until an unlockDelete names it and the guard's delete
// request (live run of 2026-11-12), so the kind unlocks before it deletes.
func TestResourceGuardProxyUnlocksDelete(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "rsg")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	guardID := rgID + "/providers/Microsoft.DataProtection/resourceGuards/guard"
	armFake.AddResource(guardID)

	vault := &rs.Vault{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "vault"},
		Spec: rs.Vault_Spec{
			AzureName: "backups", Owner: &genruntime.KnownResourceReference{Name: "rg"}, Location: ptr("westus"),
			Sku: &rs.Sku{Name: ptr(rs.SkuName_RS0), Tier: ptr("Standard")},
		},
	}
	proxy := &rs.VaultsBackupResourceGuardProxy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "guard"},
		Spec: rs.VaultsBackupResourceGuardProxy_Spec{
			AzureName: "VaultProxy", Owner: &genruntime.KnownResourceReference{Name: "vault"},
			Properties: &rs.ResourceGuardProxyBase{ResourceGuardResourceReference: &genruntime.ResourceReference{ARMID: guardID}},
		},
	}
	for _, o := range []client.Object{vault, proxy} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "VaultsBackupResourceGuardProxy Ready", ready(proxy))
	proxyID := rgID + "/providers/Microsoft.RecoveryServices/vaults/backups/backupResourceGuardProxies/VaultProxy"
	expectResource(t, proxyID, func(r map[string]any) error {
		if properties(r)["resourceGuardResourceId"] != guardID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	if err := k8s.Delete(ctx, proxy); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "VaultsBackupResourceGuardProxy gone", gone(proxy))
	if armFake.HasResource(proxyID) {
		t.Fatal("the proxy is still in Resource Manager")
	}
	if err := k8s.Delete(ctx, vault); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "Vault gone", gone(vault))
}
