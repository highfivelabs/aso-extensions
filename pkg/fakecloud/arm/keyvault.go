// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Managed HSMs, their keys, and the private endpoint connections of managed
// HSMs and key vaults (Microsoft.KeyVault, API 2026-05-15), as far as the
// fake models them:
//
//   - a managed HSM is created with a sku, a properties.tenantId and at
//     least one properties.initialAdminObjectIds (400 otherwise); soft
//     delete cannot be turned off (enableSoftDelete false, 400);
//     softDeleteRetentionInDays is 7 to 90 and cannot change once set, and
//     purge protection once on cannot be turned off (400 each);
//   - a managed HSM key needs its kty (400); a PUT on a key that exists
//     returns the key unchanged, as the API documents ("If it already
//     exists, then the existing key is returned without any write
//     operations being performed. This API does not create subsequent
//     versions, and does not update existing keys."); a key cannot be
//     deleted through Resource Manager (the spec has no DELETE: 405);
//   - a private endpoint connection is made by a private endpoint, not by a
//     PUT: a PUT updates one that exists (its connection state) and is
//     refused with 404 otherwise. Tests add the connection with AddResource,
//     as the private endpoint's request would.
//
// Not modelled: the HSM's security domain (activation), its regions and
// network rules taking effect, key versions and rotation, the release
// policy, and the error codes (this fake's own).
var (
	managedHSM         = regexp.MustCompile(`(?i)/providers/microsoft\.keyvault/managedhsms/[^/]+$`)
	managedHSMKey      = regexp.MustCompile(`(?i)/providers/microsoft\.keyvault/managedhsms/[^/]+/keys/[^/]+$`)
	keyVaultConnection = regexp.MustCompile(`(?i)/providers/microsoft\.keyvault/(vaults|managedhsms)/[^/]+/privateendpointconnections/[^/]+$`)
)

// isManagedHSMKey is whether id is a managed HSM key, which Resource Manager
// cannot delete.
func isManagedHSMKey(id string) bool {
	return managedHSMKey.MatchString(id)
}

// checkKeyVault refuses a managed HSM, key or private endpoint connection
// the rules above do not allow; code is "" when it is allowed or id is none
// of them. existing is the stored key when a PUT names a key that exists:
// the PUT changes nothing and returns it.
func (a *ARM) checkKeyVault(id string, body map[string]any) (status int, code, message string, existing *resource) {
	key := strings.ToLower(id)
	old := a.resources[key]
	props, _ := body["properties"].(map[string]any)
	switch {
	case managedHSM.MatchString(id):
		return checkManagedHSM(old, body, props)
	case managedHSMKey.MatchString(id):
		if old != nil {
			return 0, "", "", old
		}
		if kty, _ := props["kty"].(string); kty == "" {
			return http.StatusBadRequest, "KeyTypeMissing", "A key needs its kty.", nil
		}
	case keyVaultConnection.MatchString(id):
		if old == nil {
			return http.StatusNotFound, "PrivateEndpointConnectionNotFound",
				"The private endpoint connection " + id + " was not found; a private endpoint makes it.", nil
		}
	}
	return 0, "", "", nil
}

func checkManagedHSM(old *resource, body, props map[string]any) (status int, code, message string, existing *resource) {
	if props == nil {
		props = map[string]any{}
	}
	var oldProps map[string]any
	if old != nil {
		oldProps, _ = (*old)["properties"].(map[string]any)
	}
	if old == nil {
		if _, ok := body["sku"].(map[string]any); !ok {
			return http.StatusBadRequest, "SkuMissing", "A managed HSM needs its sku.", nil
		}
		if t, _ := props["tenantId"].(string); t == "" {
			return http.StatusBadRequest, "TenantIdMissing", "A managed HSM needs its tenantId.", nil
		}
		if admins, _ := props["initialAdminObjectIds"].([]any); len(admins) == 0 {
			return http.StatusBadRequest, "InitialAdminObjectIdsMissing", "A managed HSM needs at least one initial administrator.", nil
		}
	}
	if v, ok := props["enableSoftDelete"].(bool); ok && !v {
		return http.StatusBadRequest, "SoftDeleteCannotBeDisabled", "Soft delete is always on for a managed HSM.", nil
	}
	if d, ok := props["softDeleteRetentionInDays"].(float64); ok {
		if d < 7 || d > 90 {
			return http.StatusBadRequest, "SoftDeleteRetentionInvalid", "softDeleteRetentionInDays is 7 to 90.", nil
		}
		if was, ok := oldProps["softDeleteRetentionInDays"].(float64); ok && was != d {
			return http.StatusBadRequest, "SoftDeleteRetentionImmutable", "softDeleteRetentionInDays cannot change once the managed HSM is created.", nil
		}
	}
	if was, _ := oldProps["enablePurgeProtection"].(bool); was {
		if v, ok := props["enablePurgeProtection"].(bool); ok && !v {
			return http.StatusBadRequest, "PurgeProtectionCannotBeDisabled", "Purge protection, once on, cannot be turned off.", nil
		}
	}
	return 0, "", "", nil
}
