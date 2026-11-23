// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
)

// Static Web Apps' children and an App Service app's virtual network
// connections (Microsoft.Web, API 2026-07-15), as far as the fake models
// them. Every rule is one the spec's descriptions state: what a child links
// to must exist (404, as for any missing reference):
//
//   - a linked backend's backendResourceId (a function app, container app or
//     API Management service);
//   - a user-provided function app's functionAppResourceId;
//   - a database connection's resourceId;
//   - an app's virtual network connection's vnetResourceId.
//
// Not modelled: a custom domain's validation (Azure checks a CNAME or TXT
// record; the fake makes a token and leaves the domain Ready), builds and
// environments, the static site's repository and deployment token, one
// backend or function app per site, regions and SKUs, and the error codes
// (the fake's own).
var staticSiteLinks = map[*regexp.Regexp]string{
	regexp.MustCompile(`(?i)/providers/microsoft\.web/staticsites/[^/]+/linkedbackends/[^/]+$`):           "backendResourceId",
	regexp.MustCompile(`(?i)/providers/microsoft\.web/staticsites/[^/]+/userprovidedfunctionapps/[^/]+$`): "functionAppResourceId",
	regexp.MustCompile(`(?i)/providers/microsoft\.web/staticsites/[^/]+/databaseconnections/[^/]+$`):      "resourceId",
	regexp.MustCompile(`(?i)/providers/microsoft\.web/sites/[^/]+/virtualnetworkconnections/[^/]+$`):      "vnetResourceId",
}

// checkStaticSites refuses a link to a resource that does not exist; code is
// "" when it is allowed or id is none of these.
func (a *ARM) checkStaticSites(id string, body map[string]any) (status int, code, message string) {
	for re, field := range staticSiteLinks {
		if !re.MatchString(id) {
			continue
		}
		props, _ := body["properties"].(map[string]any)
		target, _ := props[field].(string)
		if !a.exists(target) {
			return http.StatusNotFound, "ResourceNotFound", "The " + field + " " + target + " was not found."
		}
	}
	return 0, "", ""
}
