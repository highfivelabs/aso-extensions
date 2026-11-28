// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Azure SQL Managed Instance children and instance pools (Microsoft.Sql,
// 2025-01-01; docs/plan.md f42), as far as the fake models them. The spec
// states each rule but the last, which Azure's documentation states
// ("Microsoft Entra-only authentication" needs a Microsoft Entra admin):
//
//   - an Azure Key Vault key gives its URI ("If the ServerKeyType is
//     AzureKeyVault, then the URI is required"; 400);
//   - an Azure Key Vault encryption protector names a key of its instance
//     (serverKeyName) that exists (404, as for any missing reference);
//   - a vulnerability assessment, the instance's or a database's, gives the
//     container for its results, and the storage account key or a SAS for it
//     (400: "If 'StorageContainerSasKey' isn't specified,
//     storageAccountAccessKey is required");
//   - an instance pool names a subnet that exists (404);
//   - Microsoft Entra-only authentication is turned on only while the
//     instance has a Microsoft Entra administrator (400).
//
// Not modelled: the key being read from Key Vault (and its name matching
// its URI), scans running, schedules stopping and starting the instance,
// the SQL Server end of a distributed availability group, DTC and SQL
// Agent behaviour, an instance pool's capacity, and the error codes (the
// fake's own).
var (
	miScope         = `(?i)^(/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.sql/managedinstances/[^/]+)`
	miKey           = regexp.MustCompile(miScope + `/keys/[^/]+$`)
	miProtector     = regexp.MustCompile(miScope + `/encryptionprotector/current$`)
	miAssessment    = regexp.MustCompile(miScope + `(?:/databases/[^/]+)?/vulnerabilityassessments/default$`)
	miEntraOnly     = regexp.MustCompile(miScope + `/azureadonlyauthentications/default$`)
	sqlInstancePool = regexp.MustCompile(`(?i)/providers/microsoft\.sql/instancepools/[^/]+$`)
)

// checkSQLManagedInstance refuses what the rules above do not allow; code is
// "" when it is allowed or id is none of these.
func (a *ARM) checkSQLManagedInstance(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	str := func(k string) string { v, _ := props[k].(string); return v }
	switch {
	case miKey.MatchString(id):
		if strings.EqualFold(str("serverKeyType"), "AzureKeyVault") && str("uri") == "" {
			return http.StatusBadRequest, "ManagedInstanceKeyUriMissing", "An AzureKeyVault key needs its URI."
		}
	case miProtector.MatchString(id):
		if !strings.EqualFold(str("serverKeyType"), "AzureKeyVault") {
			return 0, "", ""
		}
		name := str("serverKeyName")
		if name == "" {
			return http.StatusBadRequest, "EncryptionProtectorKeyMissing", "An AzureKeyVault encryption protector names its key (serverKeyName)."
		}
		key := miProtector.FindStringSubmatch(id)[1] + "/keys/" + name
		if !a.exists(key) {
			return http.StatusNotFound, "ResourceNotFound", "The managed instance key " + key + " was not found."
		}
	case miAssessment.MatchString(id):
		if str("storageContainerPath") == "" {
			return http.StatusBadRequest, "VulnerabilityAssessmentPathMissing", "A vulnerability assessment needs the container for its results (storageContainerPath)."
		}
		if str("storageAccountAccessKey") == "" && str("storageContainerSasKey") == "" {
			return http.StatusBadRequest, "VulnerabilityAssessmentKeyMissing", "A vulnerability assessment needs the storage account key or a SAS for its container."
		}
	case miEntraOnly.MatchString(id):
		if on, _ := props["azureADOnlyAuthentication"].(bool); on {
			admin := miEntraOnly.FindStringSubmatch(id)[1] + "/administrators/ActiveDirectory"
			if !a.exists(admin) {
				return http.StatusBadRequest, "EntraAdministratorMissing", "Microsoft Entra-only authentication needs a Microsoft Entra administrator on the instance."
			}
		}
	case sqlInstancePool.MatchString(id):
		if subnet := str("subnetId"); !a.exists(subnet) {
			return http.StatusNotFound, "ResourceNotFound", "The subnet " + subnet + " was not found."
		}
	}
	return 0, "", ""
}
