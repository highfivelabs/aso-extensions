// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Azure Data Explorer cluster children (Microsoft.Kusto, 2025-02-14;
// docs/plan.md f69), as far as the fake models them, from the spec's
// required properties and Azure Data Explorer's documentation:
//
//   - an attached database configuration names a database (400), a
//     defaultPrincipalsModificationKind (400) and a leader cluster (400)
//     that exists (404) and is another cluster (400);
//   - a managed private endpoint names a groupId (400) and a resource
//     (400) that exists (404);
//   - a sandbox custom image names its language (400) and a language
//     version or a base image (400);
//   - a database script has its script by URL or inline, one of the two
//     (400).
//
// The cluster's private endpoint connections are privateendpoints.go's. Not
// modelled: follower databases being kept in step, scripts running, images
// being built, and the error codes (the fake's own).
var kustoChild = regexp.MustCompile(`(?i)^(.*/providers/microsoft\.kusto/clusters/[^/]+)/(attacheddatabaseconfigurations|managedprivateendpoints|sandboxcustomimages|databases/[^/]+/scripts)/[^/]+$`)

// checkKusto refuses what the rules above do not allow; code is "" when it
// is allowed or id is none of these.
func (a *ARM) checkKusto(id string, body map[string]any) (status int, code, message string) {
	m := kustoChild.FindStringSubmatch(id)
	if m == nil {
		return 0, "", ""
	}
	cluster, kind := m[1], strings.ToLower(m[2])
	props, _ := body["properties"].(map[string]any)
	bad := func(msg string) (int, string, string) {
		return http.StatusBadRequest, "BadRequest", msg
	}
	switch kind {
	case "attacheddatabaseconfigurations":
		if stringOf(props["databaseName"]) == "" || stringOf(props["defaultPrincipalsModificationKind"]) == "" {
			return bad("An attached database configuration names its databaseName and defaultPrincipalsModificationKind.")
		}
		leader := stringOf(props["clusterResourceId"])
		if leader == "" {
			return bad("An attached database configuration names its leader cluster.")
		}
		if !a.exists(leader) {
			return http.StatusNotFound, "ResourceNotFound", "The leader cluster " + leader + " was not found."
		}
		if strings.EqualFold(leader, cluster) {
			return bad("A cluster cannot follow its own databases.")
		}
	case "managedprivateendpoints":
		if stringOf(props["groupId"]) == "" {
			return bad("A managed private endpoint names its groupId.")
		}
		target := stringOf(props["privateLinkResourceId"])
		if target == "" {
			return bad("A managed private endpoint names its private link resource.")
		}
		if !a.exists(target) {
			return http.StatusNotFound, "ResourceNotFound", "The private link resource " + target + " was not found."
		}
	case "sandboxcustomimages":
		if stringOf(props["language"]) == "" {
			return bad("A sandbox custom image names its language.")
		}
		if stringOf(props["languageVersion"]) == "" && stringOf(props["baseImageName"]) == "" {
			return bad("A sandbox custom image names a language version or a base image.")
		}
	default: // a database's scripts
		if (stringOf(props["scriptUrl"]) == "") == (stringOf(props["scriptContent"]) == "") {
			return bad("A script is given by scriptUrl or by scriptContent, one of the two.")
		}
	}
	return 0, "", ""
}
