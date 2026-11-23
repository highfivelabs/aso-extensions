// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"testing"

	rs "github.com/highfivelabs/aso-extensions/apis/arm/recoveryservices/v20260701"
)

// Azure Site Recovery's configuration in an empty vault (docs/plan.md f52;
// free: nothing is replicated): an Azure fabric for the live region, an A2A
// replication policy, changed, and the vault's replication alert settings
// (no DELETE: detached, they go with the vault). No protection container
// (no DELETE either: Azure removes one with a POST, which would leave it
// holding up the fabric's deletion) and no replicated item (replicating a
// VM bills per instance and makes disks in the target region).
func TestLiveSiteRecovery(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.RecoveryServices")
	ns := namespace(t, "asr")
	vault := &rs.Vault{}
	vault.Namespace, vault.Name = ns, "vault"
	vault.Spec = rs.Vault_Spec{
		AzureName: azName("asr"), Owner: owner("rg"), Location: ptr(location),
		Sku: &rs.Sku{Name: ptr(rs.SkuName_RS0), Tier: ptr("Standard")},
	}
	create(t, timeout, vault)
	vaultID := rgID() + "/providers/Microsoft.RecoveryServices/vaults/" + vault.Spec.AzureName

	fabric := &rs.VaultsReplicationFabric{}
	fabric.Namespace, fabric.Name = ns, "fabric"
	fabric.Spec = rs.VaultsReplicationFabric_Spec{AzureName: azName("fabric"), Owner: owner("vault"), Properties: &rs.FabricCreationInputProperties{
		CustomDetails: &rs.FabricSpecificCreationInput{Azure: &rs.AzureFabricCreationInput{
			InstanceType: ptr(rs.AzureFabricCreationInput_InstanceType_Azure), Location: ptr(location),
		}},
	}}
	policy := &rs.VaultsReplicationPolicy{}
	policy.Namespace, policy.Name = ns, "policy"
	policy.Spec = rs.VaultsReplicationPolicy_Spec{AzureName: azName("a2a"), Owner: owner("vault"), Properties: &rs.CreatePolicyInputProperties{
		ProviderSpecificInput: &rs.PolicyProviderSpecificInput{A2A: &rs.A2APolicyCreationInput{
			InstanceType: ptr(rs.A2APolicyCreationInput_InstanceType_A2A), RecoveryPointHistory: ptr(1440),
			AppConsistentFrequencyInMinutes: ptr(240), MultiVmSyncStatus: ptr(rs.SetMultiVmSyncStatus_Enable),
		}},
	}}
	alerts := &rs.VaultsReplicationAlertSetting{}
	alerts.Namespace, alerts.Name = ns, "alerts"
	alerts.Annotations = map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}
	alerts.Spec = rs.VaultsReplicationAlertSetting_Spec{AzureName: "defaultAlertSetting", Owner: owner("vault"), Properties: &rs.ConfigureAlertRequestProperties{
		SendToOwners: ptr("DoNotSend"),
	}}
	create(t, timeout, fabric, policy, alerts)
	expectResource(t, vaultID+"/replicationFabrics/"+fabric.Spec.AzureName, vaultSettingsAPI, func(r map[string]any) error {
		if fmt.Sprint(properties(r)["fabricType"]) != "Azure" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	update(t, policy, func(p *rs.VaultsReplicationPolicy) {
		p.Spec.Properties.ProviderSpecificInput.A2A.RecoveryPointHistory = ptr(2880)
	})
	eventually(t, timeout, "the policy Ready after the change", ready(policy))
	expectResource(t, vaultID+"/replicationPolicies/"+policy.Spec.AzureName, vaultSettingsAPI, func(r map[string]any) error {
		d, _ := properties(r)["providerSpecificDetails"].(map[string]any)
		if fmt.Sprint(d["recoveryPointHistory"]) != "2880" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, alerts)
	remove(t, timeout, policy)
	remove(t, timeout, fabric)
	remove(t, timeout, vault)
	expectGone(t, vaultID, vaultSettingsAPI)
}
