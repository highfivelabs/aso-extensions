// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
)

// App Service Environment v3 (Microsoft.Web/hostingEnvironments, API
// 2026-07-15; docs/plan.md f41), as far as the fake models it. Each rule is
// one the spec states:
//
//   - an environment names the network it is injected into,
//     virtualNetwork.id (the spec's required property; 400), which must
//     exist (404, as for any missing reference);
//   - its custom DNS suffix configuration gives both the suffix and the Key
//     Vault certificate for it, or neither (400; "the default SSL/TLS
//     certificate for sites with the custom domain suffix").
//
// Its private endpoint connections are made by a private endpoint
// (privateendpoints.go). Not modelled: the subnet's delegation to
// Microsoft.Web/hostingEnvironments and its size, the hours an environment
// takes to be made, the Key Vault certificate being read, inbound and
// outbound addresses (none are computed), and the error codes (the fake's
// own).
var (
	hostingEnvironment    = regexp.MustCompile(`(?i)/providers/microsoft\.web/hostingenvironments/[^/]+$`)
	hostingEnvironmentDNS = regexp.MustCompile(`(?i)/providers/microsoft\.web/hostingenvironments/[^/]+/configurations/customdnssuffix$`)
)

// checkHostingEnvironments refuses what the rules above do not allow; code
// is "" when it is allowed or id is none of these.
func (a *ARM) checkHostingEnvironments(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	switch {
	case hostingEnvironment.MatchString(id):
		network, _ := props["virtualNetwork"].(map[string]any)
		ref, _ := network["id"].(string)
		if ref == "" {
			return http.StatusBadRequest, "VirtualNetworkMissing", "An App Service Environment names the network it is injected into (virtualNetwork.id)."
		}
		if !a.exists(ref) {
			return http.StatusNotFound, "ResourceNotFound", "The virtual network " + ref + " was not found."
		}
	case hostingEnvironmentDNS.MatchString(id):
		suffix, _ := props["dnsSuffix"].(string)
		cert, _ := props["certificateUrl"].(string)
		if (suffix == "") != (cert == "") {
			return http.StatusBadRequest, "CustomDnsSuffixIncomplete", "A custom DNS suffix needs its certificate (certificateUrl), and a certificate its suffix (dnsSuffix)."
		}
	}
	return 0, "", ""
}
