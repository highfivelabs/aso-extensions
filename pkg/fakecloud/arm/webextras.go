// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// The App Service leftovers of docs/plan.md f53 (Microsoft.Web, 2026-07-15),
// as far as the fake models them. Each rule is one the spec or App
// Service's documentation states:
//
//   - an app's or slot's certificate comes from a PFX with its password, a
//     Key Vault secret (keyVaultId with keyVaultSecretName), or is a managed
//     certificate for a host name (canonicalName) (400);
//   - the gateway of a gateway-required virtual network integration is
//     named primary, "the only supported string" (400);
//   - a static web app's basic authentication is named default (400), needs
//     the Standard (or Dedicated) plan, not Free (400), applies to AllEnvironments,
//     StagingEnvironments or SpecifiedEnvironments, the last with a list of
//     environments (400), and has a password or a Key Vault secret URL (400);
//   - a slot's source control names its repository (repoUrl; 400);
//   - a Kubernetes environment (App Service on Azure Arc) names a custom
//     location (extendedLocation.name; the spec makes its type read-only;
//     400) that exists (404).
//
// Not modelled: certificates being read from Key Vault or issued, the
// gateway's VPN package, basic authentication taking effect, deployments
// from source control, the Arc cluster, AI gateways' behaviour, and the
// error codes (the fake's own).
var (
	webSiteCert     = regexp.MustCompile(`(?i)/providers/microsoft\.web/sites/[^/]+(?:/slots/[^/]+)?/certificates/[^/]+$`)
	webVnetGateway  = regexp.MustCompile(`(?i)/providers/microsoft\.web/sites/[^/]+(?:/slots/[^/]+)?/virtualnetworkconnections/[^/]+/gateways/([^/]+)$`)
	webBasicAuth    = regexp.MustCompile(`(?i)^(.*/providers/microsoft\.web/staticsites/[^/]+)/basicauth/([^/]+)$`)
	webSlotSource   = regexp.MustCompile(`(?i)/providers/microsoft\.web/sites/[^/]+/slots/[^/]+/sourcecontrols/web$`)
	webKubeEnv      = regexp.MustCompile(`(?i)/providers/microsoft\.web/kubeenvironments/[^/]+$`)
	basicAuthScopes = map[string]bool{"allenvironments": true, "stagingenvironments": true, "specifiedenvironments": true}
)

// checkWebExtras refuses what the rules above do not allow; code is "" when
// it is allowed or id is none of these.
func (a *ARM) checkWebExtras(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	switch {
	case webSiteCert.MatchString(id):
		pfx := str(props, "pfxBlob") != "" && str(props, "password") != ""
		vault := str(props, "keyVaultId") != "" && str(props, "keyVaultSecretName") != ""
		managed := str(props, "canonicalName") != ""
		if !pfx && !vault && !managed {
			return http.StatusBadRequest, "CertificateSourceMissing", "A certificate comes from a PFX with its password, a Key Vault secret (keyVaultId and keyVaultSecretName), or is a managed certificate (canonicalName)."
		}
	case webVnetGateway.MatchString(id):
		if name := webVnetGateway.FindStringSubmatch(id)[1]; !strings.EqualFold(name, "primary") {
			return http.StatusBadRequest, "InvalidGatewayName", "The only supported gateway name is primary, not " + name + "."
		}
	case webBasicAuth.MatchString(id):
		m := webBasicAuth.FindStringSubmatch(id)
		if !strings.EqualFold(m[2], "default") {
			return http.StatusBadRequest, "InvalidBasicAuthName", "A static web app's basic authentication is named default."
		}
		if site, ok := a.resources[strings.ToLower(m[1])]; ok {
			sku, _ := (*site)["sku"].(map[string]any)
			if tier := str(sku, "name"); tier != "" && !strings.EqualFold(tier, "Standard") && !strings.EqualFold(tier, "Dedicated") {
				return http.StatusBadRequest, "BasicAuthRequiresStandard", "Password protection needs the Standard plan; the static web app is on " + tier + "."
			}
		}
		mode := strings.ToLower(str(props, "applicableEnvironmentsMode"))
		if !basicAuthScopes[mode] {
			return http.StatusBadRequest, "InvalidApplicableEnvironmentsMode", "applicableEnvironmentsMode is AllEnvironments, StagingEnvironments or SpecifiedEnvironments."
		}
		if envs, _ := props["environments"].([]any); mode == "specifiedenvironments" && len(envs) == 0 {
			return http.StatusBadRequest, "EnvironmentsMissing", "SpecifiedEnvironments needs the environments it applies to."
		}
		if str(props, "password") == "" && str(props, "secretUrl") == "" {
			return http.StatusBadRequest, "PasswordMissing", "Basic authentication needs a password or a Key Vault secret URL (secretUrl)."
		}
	case webSlotSource.MatchString(id):
		if str(props, "repoUrl") == "" {
			return http.StatusBadRequest, "RepoUrlMissing", "Source control names its repository (repoUrl)."
		}
	case webKubeEnv.MatchString(id):
		ext, _ := body["extendedLocation"].(map[string]any)
		loc := str(ext, "name")
		if loc == "" {
			return http.StatusBadRequest, "CustomLocationMissing", "A Kubernetes environment names its custom location (extendedLocation.name)."
		}
		if !a.exists(loc) {
			return http.StatusNotFound, "ResourceNotFound", "The custom location " + loc + " was not found."
		}
	}
	return 0, "", ""
}
