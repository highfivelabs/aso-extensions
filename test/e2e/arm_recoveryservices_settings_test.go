// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

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

// A Recovery Services vault's settings singletons (docs/plan.md f50) on our
// Vault: soft delete always on with 30 days' retention, geo-redundant storage
// with cross-region restore, and encryption with a customer-managed key.
// None has a DELETE, so each is detached. Turning soft delete off, a
// redundancy change once the vault protects an item, and a move back to
// Microsoft-managed keys are refused (the fake's rules, vaultsettings.go);
// a change is PUT again; deleted, each stays in Azure with the vault, which
// is detached too (it then holds a protected item).
func TestRecoveryServicesVaultSettings(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "rsvsettings")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	detach := map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}

	vault := &rs.Vault{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "vault"},
		Spec: rs.Vault_Spec{
			AzureName: "backups", Owner: &genruntime.KnownResourceReference{Name: "rg"}, Location: ptr("westus"),
			Sku: &rs.Sku{Name: ptr(rs.SkuName_RS0), Tier: ptr("Standard")},
		},
	}
	onVault := &genruntime.KnownResourceReference{Name: "vault"}
	config := &rs.VaultsBackupConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "config", Annotations: detach},
		Spec: rs.VaultsBackupConfig_Spec{Owner: onVault, Properties: &rs.BackupResourceVaultConfig{
			SoftDeleteFeatureState:          ptr(rs.SoftDeleteFeatureState_AlwaysON),
			SoftDeleteRetentionPeriodInDays: ptr(30),
			EnhancedSecurityState:           ptr(rs.BmsEnhancedSecurityState_Enabled),
		}},
	}
	storage := &rs.VaultsBackupStorageConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "storage", Annotations: detach},
		Spec: rs.VaultsBackupStorageConfig_Spec{Owner: onVault, Properties: &rs.BackupResourceConfig{
			StorageModelType: ptr(rs.StorageType_GeoRedundant), CrossRegionRestoreFlag: ptr(true),
		}},
	}
	encryption := &rs.VaultsBackupEncryptionConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "encryption", Annotations: detach},
		Spec: rs.VaultsBackupEncryptionConfig_Spec{Owner: onVault, Properties: &rs.BackupResourceEncryptionConfig{
			EncryptionAtRestType: ptr(rs.EncryptionAtRestType_CustomerManaged),
			KeyUri:               ptr("https://kv-" + ns + ".vault.azure.net/keys/backup"),
		}},
	}
	objs := []conditioned{vault, config, storage, encryption}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}

	vaultID := rgID + "/providers/Microsoft.RecoveryServices/vaults/backups"
	configID := vaultID + "/backupconfig/vaultconfig"
	storageID := vaultID + "/backupstorageconfig/vaultstorageconfig"
	encryptionID := vaultID + "/backupEncryptionConfigs/backupResourceEncryptionConfig"
	expectResource(t, configID, func(r map[string]any) error {
		if p := properties(r); p["softDeleteFeatureState"] != "AlwaysON" || p["softDeleteRetentionPeriodInDays"] != float64(30) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, storageID, func(r map[string]any) error {
		if p := properties(r); p["storageModelType"] != "GeoRedundant" || p["crossRegionRestoreFlag"] != true {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, encryptionID, func(r map[string]any) error {
		if p := properties(r); p["encryptionAtRestType"] != "CustomerManaged" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again: retention, and redundancy before any item.
	if err := change(t, config, func(c *rs.VaultsBackupConfig) { c.Spec.Properties.SoftDeleteRetentionPeriodInDays = ptr(60) }); err != nil {
		t.Fatal(err)
	}
	expectResource(t, configID, func(r map[string]any) error {
		if properties(r)["softDeleteRetentionPeriodInDays"] != float64(60) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if err := change(t, storage, func(s *rs.VaultsBackupStorageConfig) {
		s.Spec.Properties.StorageModelType = ptr(rs.StorageType_ZoneRedundant)
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, storageID, func(r map[string]any) error {
		if properties(r)["storageModelType"] != "ZoneRedundant" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// Refused: soft delete off, a redundancy change once an item is
	// protected, a move back to Microsoft-managed keys.
	if err := change(t, config, func(c *rs.VaultsBackupConfig) {
		c.Spec.Properties.SoftDeleteFeatureState = ptr(rs.SoftDeleteFeatureState_Disabled)
	}); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "soft delete off refused", notReadyWith(config, "cannot be turned off"))
	armFake.AddResource(vaultID + "/backupFabrics/Azure/protectionContainers/iaasvmcontainerv2;rg;vm/protectedItems/vm;rg;vm")
	if err := change(t, storage, func(s *rs.VaultsBackupStorageConfig) {
		s.Spec.Properties.StorageModelType = ptr(rs.StorageType_LocallyRedundant)
	}); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "a redundancy change refused", notReadyWith(storage, "cannot change"))
	if err := change(t, encryption, func(e *rs.VaultsBackupEncryptionConfig) {
		e.Spec.Properties.EncryptionAtRestType = ptr(rs.EncryptionAtRestType_MicrosoftManaged)
	}); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "a move back to Microsoft-managed keys refused", notReadyWith(encryption, "cannot go back"))

	for _, o := range []client.Object{config, storage, encryption} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, id := range []string{configID, storageID, encryptionID} {
		if !armFake.HasResource(id) {
			t.Fatalf("%s was deleted; it has no DELETE and was detached", id)
		}
	}
	// The vault now holds a protected item, which Azure would not let it be
	// deleted with (protecteditems.go): it is detached too.
	if err := change(t, vault, func(v *rs.Vault) { v.Annotations = detach }); err != nil {
		t.Fatal(err)
	}
	if err := k8s.Delete(ctx, vault); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "vault gone", gone(vault))
}
