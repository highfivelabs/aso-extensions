// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// A Recovery Services vault's settings singletons
// (vaults/backupconfig/vaultconfig, backupstorageconfig/vaultstorageconfig,
// backupEncryptionConfigs/backupResourceEncryptionConfig, 2026-07-01;
// docs/plan.md f50), as far as the fake models them. Each rule is one
// Azure's documentation of the vault states:
//
//   - soft delete set to AlwaysON cannot be turned off again (400), and
//     its retention is 14 to 180 days (400);
//   - the storage redundancy cannot change once the vault protects an item
//     (a protected item under the vault: storageTypeState Locked; 400);
//   - customer-managed encryption names its key (keyUri; 400), and a vault
//     encrypted with a customer-managed key cannot go back to
//     Microsoft-managed keys (400).
//
// Not modelled: enhanced security and multi-user authorization (the
// resource guard requests a change may need), cross-region restore taking
// effect, the key being reachable, and the error codes (the fake's own).
var (
	vaultBackupConfig     = regexp.MustCompile(`(?i)^(.*/providers/microsoft\.recoveryservices/vaults/[^/]+)/backupconfig/vaultconfig$`)
	vaultStorageConfig    = regexp.MustCompile(`(?i)^(.*/providers/microsoft\.recoveryservices/vaults/[^/]+)/backupstorageconfig/vaultstorageconfig$`)
	vaultEncryptionConfig = regexp.MustCompile(`(?i)/providers/microsoft\.recoveryservices/vaults/[^/]+/backupencryptionconfigs/backupresourceencryptionconfig$`)
)

// checkVaultSettings refuses what the rules above do not allow; code is ""
// when it is allowed or id is none of these.
func (a *ARM) checkVaultSettings(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	old := map[string]any{}
	if res, ok := a.resources[strings.ToLower(id)]; ok {
		old, _ = (*res)["properties"].(map[string]any)
	}
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	switch {
	case vaultBackupConfig.MatchString(id):
		if strings.EqualFold(str(old, "softDeleteFeatureState"), "AlwaysON") && !strings.EqualFold(str(props, "softDeleteFeatureState"), "AlwaysON") {
			return http.StatusBadRequest, "BMSUserErrorSoftDeleteAlwaysOn", "Soft delete is AlwaysON for this vault and cannot be turned off."
		}
		if days, ok := props["softDeleteRetentionPeriodInDays"].(float64); ok && (days < 14 || days > 180) {
			return http.StatusBadRequest, "BMSUserErrorInvalidSoftDeleteRetention", "Soft delete retention is 14 to 180 days."
		}
	case vaultStorageConfig.MatchString(id):
		vault := strings.ToLower(vaultStorageConfig.FindStringSubmatch(id)[1])
		protects := false
		for k := range a.resources {
			if strings.HasPrefix(k, vault+"/backupfabrics/") && strings.Contains(k, "/protecteditems/") {
				protects = true
				break
			}
		}
		for _, p := range []string{"storageModelType", "storageType"} {
			was, now := str(old, p), str(props, p)
			if protects && was != "" && now != "" && !strings.EqualFold(was, now) {
				return http.StatusBadRequest, "BMSUserErrorStorageTypeLocked", "The vault protects items, so its storage redundancy cannot change (storageTypeState Locked)."
			}
		}
	case vaultEncryptionConfig.MatchString(id):
		kind := str(props, "encryptionAtRestType")
		if strings.EqualFold(kind, "CustomerManaged") && str(props, "keyUri") == "" {
			return http.StatusBadRequest, "BMSUserErrorKeyUriMissing", "Customer-managed encryption names its key (keyUri)."
		}
		if strings.EqualFold(str(old, "encryptionAtRestType"), "CustomerManaged") && strings.EqualFold(kind, "MicrosoftManaged") {
			return http.StatusBadRequest, "BMSUserErrorCannotRevertToPlatformKeys", "A vault encrypted with a customer-managed key cannot go back to Microsoft-managed keys."
		}
	}
	return 0, "", ""
}
