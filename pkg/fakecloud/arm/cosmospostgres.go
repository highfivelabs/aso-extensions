// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Azure Cosmos DB for PostgreSQL clusters and their children
// (Microsoft.DBforPostgreSQL/serverGroupsv2, 2022-11-08; docs/plan.md f46),
// as far as the fake models them. Each rule is one the spec states:
//
//   - a new cluster that is not a replica or a restore has an administrator
//     password, a coordinator edition, storage and vCores, and a node count
//     ("Required for creation"; 400);
//   - a cluster's node count is 0 or at least 2 ("Node count value cannot
//     be 1"; 400);
//   - a read replica or a restore names its source cluster
//     (sourceResourceId), which must exist (404, as for any missing
//     reference);
//   - a firewall rule has a start and an end IPv4 address (required, "Must
//     be IPv4 format"; 400);
//   - a role has a password, and a coordinator or node configuration a
//     value (both required in the spec; 400).
//
// Private endpoint connections are privateendpoints.go's. Not modelled: the
// servers a cluster makes (serverNames), its state, high availability,
// replicas being kept in step, a configuration's name being a server
// parameter and its value an allowed one, a role's grants, and the error
// codes (the fake's own).
var (
	cosmosPgCluster  = regexp.MustCompile(`(?i)/providers/microsoft\.dbforpostgresql/servergroupsv2/[^/]+$`)
	cosmosPgFirewall = regexp.MustCompile(`(?i)/providers/microsoft\.dbforpostgresql/servergroupsv2/[^/]+/firewallrules/[^/]+$`)
	cosmosPgRole     = regexp.MustCompile(`(?i)/providers/microsoft\.dbforpostgresql/servergroupsv2/[^/]+/roles/[^/]+$`)
	cosmosPgConfig   = regexp.MustCompile(`(?i)/providers/microsoft\.dbforpostgresql/servergroupsv2/[^/]+/(coordinator|node)configurations/[^/]+$`)
)

// checkCosmosPostgres refuses what the rules above do not allow; code is ""
// when it is allowed or id is none of these.
func (a *ARM) checkCosmosPostgres(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	switch {
	case cosmosPgCluster.MatchString(id):
		if n, ok := props["nodeCount"].(float64); ok && n == 1 {
			return http.StatusBadRequest, "InvalidNodeCount", "A cluster's node count is 0 (a single node) or at least 2, not 1."
		}
		if source, _ := props["sourceResourceId"].(string); source != "" {
			if !a.exists(source) {
				return http.StatusNotFound, "ResourceNotFound", "The source cluster " + source + " was not found."
			}
			return 0, "", ""
		}
		if a.exists(id) {
			return 0, "", ""
		}
		for _, p := range []string{"administratorLoginPassword", "coordinatorServerEdition", "coordinatorStorageQuotaInMb", "coordinatorVCores", "nodeCount"} {
			if v, ok := props[p]; !ok || v == nil || v == "" {
				return http.StatusBadRequest, "MissingRequiredProperty", "A new cluster needs properties." + p + "."
			}
		}
	case cosmosPgFirewall.MatchString(id):
		for _, p := range []string{"startIpAddress", "endIpAddress"} {
			if v, _ := props[p].(string); !ipv4(v) || strings.Contains(v, ":") {
				return http.StatusBadRequest, "InvalidIpAddress", "A firewall rule's " + p + " must be an IPv4 address, not " + strings.TrimSpace(v) + "."
			}
		}
	case cosmosPgRole.MatchString(id):
		if p, _ := props["password"].(string); p == "" {
			return http.StatusBadRequest, "MissingRequiredProperty", "A role needs a password."
		}
	case cosmosPgConfig.MatchString(id):
		if _, ok := props["value"].(string); !ok {
			return http.StatusBadRequest, "MissingRequiredProperty", "A configuration needs a value."
		}
	}
	return 0, "", ""
}
