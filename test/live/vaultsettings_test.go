// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"testing"

	rs "github.com/highfivelabs/aso-extensions/apis/arm/recoveryservices/v20260701"
)

const vaultSettingsAPI = "2026-07-01"

// An empty Recovery Services vault (free while it protects nothing) with its
// backup storage set to locally redundant and its soft delete retention to
// 14 days, then changed (docs/plan.md f50). The settings have no DELETE:
// detached, they go with the vault. Soft delete is left Enabled, not AlwaysON
// (irreversible), and customer-managed encryption is not set (it needs a
// Key Vault key the vault's identity can read).
func TestLiveVaultSettings(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.RecoveryServices")
	ns := namespace(t, "rsvsettings")
	vault := &rs.Vault{}
	vault.Namespace, vault.Name = ns, "vault"
	vault.Spec = rs.Vault_Spec{
		AzureName: azName("rsvset"), Owner: owner("rg"), Location: ptr(location),
		Sku: &rs.Sku{Name: ptr(rs.SkuName_RS0), Tier: ptr("Standard")},
	}
	create(t, timeout, vault)
	vaultID := rgID() + "/providers/Microsoft.RecoveryServices/vaults/" + vault.Spec.AzureName
	detach := map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}

	storage := &rs.VaultsBackupStorageConfig{}
	storage.Namespace, storage.Name, storage.Annotations = ns, "storage", detach
	storage.Spec = rs.VaultsBackupStorageConfig_Spec{Owner: owner("vault"), Properties: &rs.BackupResourceConfig{
		StorageModelType: ptr(rs.StorageType_LocallyRedundant),
	}}
	config := &rs.VaultsBackupConfig{}
	config.Namespace, config.Name, config.Annotations = ns, "config", detach
	config.Spec = rs.VaultsBackupConfig_Spec{Owner: owner("vault"), Properties: &rs.BackupResourceVaultConfig{
		SoftDeleteFeatureState: ptr(rs.SoftDeleteFeatureState_Enabled), SoftDeleteRetentionPeriodInDays: ptr(14),
	}}
	create(t, timeout, storage, config)
	expectResource(t, vaultID+"/backupstorageconfig/vaultstorageconfig", vaultSettingsAPI, func(r map[string]any) error {
		if properties(r)["storageModelType"] != "LocallyRedundant" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	update(t, config, func(c *rs.VaultsBackupConfig) { c.Spec.Properties.SoftDeleteRetentionPeriodInDays = ptr(21) })
	eventually(t, timeout, "the backup config Ready after the change", ready(config))
	expectResource(t, vaultID+"/backupconfig/vaultconfig", vaultSettingsAPI, func(r map[string]any) error {
		if fmt.Sprint(properties(r)["softDeleteRetentionPeriodInDays"]) != "21" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, config)
	remove(t, timeout, storage)
	remove(t, timeout, vault)
	expectGone(t, vaultID, vaultSettingsAPI)
}
