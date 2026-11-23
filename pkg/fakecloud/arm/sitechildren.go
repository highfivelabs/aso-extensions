// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// The children of an App Service app or slot that docs/plan.md f40 added
// (Microsoft.Web, API 2026-07-15), as far as the fake models them. Each rule
// is one the spec states:
//
//   - a sidecar container (sitecontainers) gives its image and whether it is
//     the main container, isMain (the spec's required properties; 400);
//   - an app or slot has one main container ("the main site container"):
//     a second container with isMain true is refused (409);
//   - private access (privateAccess/virtualNetworks) names virtual networks
//     that exist (404, as for any missing reference);
//   - a slot's virtual network connection names a network that exists, as an
//     app's does (staticsites.go).
//
// Not modelled: the image being pulled and the container started (a
// container is Succeeded at once), the registry credentials being checked,
// a sidecar app needing a Linux plan with sitecontainers enabled, functions
// being deployed or run (a function is kept as PUT), premier add-ons being
// bought from the Marketplace, and the error codes (the fake's own).
var (
	siteContainer      = regexp.MustCompile(`(?i)^(/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.web/sites/[^/]+(?:/slots/[^/]+)?)/sitecontainers/([^/]+)$`)
	sitePrivateAccess  = regexp.MustCompile(`(?i)/providers/microsoft\.web/sites/[^/]+(?:/slots/[^/]+)?/privateaccess/virtualnetworks$`)
	slotVnetConnection = regexp.MustCompile(`(?i)/providers/microsoft\.web/sites/[^/]+/slots/[^/]+/virtualnetworkconnections/[^/]+$`)
)

// checkSiteChildren refuses what the rules above do not allow; code is ""
// when it is allowed or id is none of these.
func (a *ARM) checkSiteChildren(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	switch {
	case siteContainer.MatchString(id):
		m := siteContainer.FindStringSubmatch(id)
		if image, _ := props["image"].(string); image == "" {
			return http.StatusBadRequest, "SiteContainerImageMissing", "A site container needs its image."
		}
		isMain, ok := props["isMain"].(bool)
		if !ok {
			return http.StatusBadRequest, "SiteContainerIsMainMissing", "A site container says whether it is the main container (isMain)."
		}
		if !isMain {
			return 0, "", ""
		}
		prefix := strings.ToLower(m[1]) + "/sitecontainers/"
		self := strings.ToLower(id)
		for k, r := range a.resources {
			if k == self || !strings.HasPrefix(k, prefix) || strings.Contains(k[len(prefix):], "/") {
				continue
			}
			other, _ := (*r)["properties"].(map[string]any)
			if main, _ := other["isMain"].(bool); main {
				return http.StatusConflict, "MainSiteContainerExists",
					"The app " + m[1] + " already has a main container, " + lastSegment(k) + "."
			}
		}
	case sitePrivateAccess.MatchString(id):
		networks, _ := props["virtualNetworks"].([]any)
		for _, n := range networks {
			network, _ := n.(map[string]any)
			if ref, _ := network["resourceId"].(string); ref != "" && !a.exists(ref) {
				return http.StatusNotFound, "ResourceNotFound", "The virtual network " + ref + " was not found."
			}
		}
	case slotVnetConnection.MatchString(id):
		if ref, _ := props["vnetResourceId"].(string); !a.exists(ref) {
			return http.StatusNotFound, "ResourceNotFound", "The vnetResourceId " + ref + " was not found."
		}
	}
	return 0, "", ""
}
