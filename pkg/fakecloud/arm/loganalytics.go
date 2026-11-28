// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Log Analytics dedicated clusters, workspace data sources, storage
// insight configs, summary rules and query packs
// (Microsoft.OperationalInsights, API 2025-07-01), as far as the fake models
// them:
//
//   - a query pack's PUT carries a properties object, even an empty one
//     (the spec marks it required and every property in it read-only; real
//     Azure answered 400 "Value cannot be null." to a pack without one,
//     live run of 2026-11-12);
//   - a cluster's sku is CapacityReservation with a capacity of one of
//     Azure's commitment tiers (100, 200, 300, 400, 500, 1000, 2000, 5000,
//     10000, 25000 or 50000 GB a day; 400 otherwise);
//   - a data source gives its kind (400 otherwise);
//   - a summary rule's definition gives a query and a bin size of 20, 30,
//     60, 120, 180, 360, 720 or 1440 minutes (the spec's description of
//     binSize; 400 otherwise);
//   - a storage insight config names a storage account and gives its key
//     (400 otherwise); the account must exist (404); the key is kept aside
//     (SharedKey, under the config's ID) and never returned, as Azure does
//     not return it.
//
// Not modelled: linking a workspace to a cluster, data flowing, a summary
// rule running and its destination table being made, which
// properties each data source kind takes, the containers and tables a
// storage account offers, and the error codes (this fake's own).
var (
	logAnalyticsCluster   = regexp.MustCompile(`(?i)/providers/microsoft\.operationalinsights/clusters/[^/]+$`)
	logAnalyticsSource    = regexp.MustCompile(`(?i)/providers/microsoft\.operationalinsights/workspaces/[^/]+/datasources/[^/]+$`)
	logAnalyticsQueryPack = regexp.MustCompile(`(?i)/providers/microsoft\.operationalinsights/querypacks/[^/]+$`)
	storageInsightConfig  = regexp.MustCompile(`(?i)/providers/microsoft\.operationalinsights/workspaces/[^/]+/storageinsightconfigs/[^/]+$`)
	summaryLog            = regexp.MustCompile(`(?i)/providers/microsoft\.operationalinsights/workspaces/[^/]+/summarylogs/[^/]+$`)
	summaryBinSizes       = map[float64]bool{20: true, 30: true, 60: true, 120: true, 180: true, 360: true, 720: true, 1440: true}
	clusterCapacities     = map[float64]bool{100: true, 200: true, 300: true, 400: true, 500: true, 1000: true, 2000: true, 5000: true, 10000: true, 25000: true, 50000: true}
)

// checkLogAnalytics refuses a cluster, data source, storage insight config
// or query pack the rules above do not allow; code is "" when it is allowed or id is none
// of them.
func (a *ARM) checkLogAnalytics(id string, body map[string]any) (status int, code, message string) {
	props, hasProps := body["properties"].(map[string]any)
	switch {
	case logAnalyticsQueryPack.MatchString(id):
		if !hasProps {
			return http.StatusBadRequest, "BadRequest", "Value cannot be null."
		}
	case logAnalyticsCluster.MatchString(id):
		sku, _ := body["sku"].(map[string]any)
		capacity, _ := sku["capacity"].(float64)
		if sku["name"] != "CapacityReservation" || !clusterCapacities[capacity] {
			return http.StatusBadRequest, "InvalidClusterSku", "A cluster's sku is CapacityReservation with a capacity of 100, 200, 300, 400, 500, 1000, 2000, 5000, 10000, 25000 or 50000."
		}
	case summaryLog.MatchString(id):
		definition, _ := props["ruleDefinition"].(map[string]any)
		if q, _ := definition["query"].(string); q == "" {
			return http.StatusBadRequest, "InvalidSummaryRule", "A summary rule's definition gives a query."
		}
		if size, _ := definition["binSize"].(float64); !summaryBinSizes[size] {
			return http.StatusBadRequest, "InvalidSummaryRule", "A summary rule's bin size is 20, 30, 60, 120, 180, 360, 720 or 1440 minutes."
		}
	case logAnalyticsSource.MatchString(id):
		if k, _ := body["kind"].(string); k == "" {
			return http.StatusBadRequest, "DataSourceKindMissing", "A data source needs its kind."
		}
	case storageInsightConfig.MatchString(id):
		account, _ := props["storageAccount"].(map[string]any)
		accountID, _ := account["id"].(string)
		key, _ := account["key"].(string)
		if accountID == "" || key == "" {
			return http.StatusBadRequest, "StorageAccountMissing", "A storage insight config needs its storage account's id and key."
		}
		if !a.exists(accountID) {
			return http.StatusNotFound, "ResourceNotFound", "The storage account " + accountID + " was not found."
		}
		a.sharedKeys[strings.ToLower(id)] = key
		delete(account, "key")
	}
	return 0, "", ""
}
