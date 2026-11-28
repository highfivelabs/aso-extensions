// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// A vault's soft delete stays AlwaysON, its retention is bounded, its
// redundancy is locked once it protects an item, and customer-managed
// encryption names a key and is not reverted.
func TestVaultSettings(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/backup"
	vault := rg + "/providers/Microsoft.RecoveryServices/vaults/rsv"
	for _, id := range []string{rg, vault} {
		a.AddResource(id)
	}
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	config := vault + "/backupconfig/vaultconfig"
	put("a retention of 7 days", config, `{"properties":{"softDeleteFeatureState":"Enabled","softDeleteRetentionPeriodInDays":7}}`, http.StatusBadRequest)
	put("soft delete always on", config, `{"properties":{"softDeleteFeatureState":"AlwaysON","softDeleteRetentionPeriodInDays":14}}`, http.StatusCreated)
	put("soft delete turned off", config, `{"properties":{"softDeleteFeatureState":"Disabled"}}`, http.StatusBadRequest)
	storage := vault + "/backupstorageconfig/vaultstorageconfig"
	put("geo-redundant storage", storage, `{"properties":{"storageModelType":"GeoRedundant","crossRegionRestoreFlag":true}}`, http.StatusCreated)
	put("locally redundant before any item", storage, `{"properties":{"storageModelType":"LocallyRedundant"}}`, http.StatusOK)
	a.AddResource(vault + "/backupFabrics/Azure/protectionContainers/c/protectedItems/vm")
	put("zone-redundant with an item protected", storage, `{"properties":{"storageModelType":"ZoneRedundant"}}`, http.StatusBadRequest)
	enc := vault + "/backupEncryptionConfigs/backupResourceEncryptionConfig"
	put("customer-managed without a key", enc, `{"properties":{"encryptionAtRestType":"CustomerManaged"}}`, http.StatusBadRequest)
	put("customer-managed", enc, `{"properties":{"encryptionAtRestType":"CustomerManaged","keyUri":"https://kv.vault.azure.net/keys/backup"}}`, http.StatusCreated)
	put("back to Microsoft-managed", enc, `{"properties":{"encryptionAtRestType":"MicrosoftManaged"}}`, http.StatusBadRequest)
}
