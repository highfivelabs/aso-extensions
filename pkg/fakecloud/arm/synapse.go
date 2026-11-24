// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"bytes"
	"net"
	"net/http"
	"regexp"
	"strings"
)

// A Synapse workspace's children (Microsoft.Synapse, 2021-06-01;
// docs/plan.md f80), as far as the fake models them. Each rule is one the
// spec or Synapse's documentation states:
//
//   - an IP firewall rule names a start and an end IPv4 address, the start
//     not after the end (400);
//   - an Entra admin (administrators or sqlAdministrators, activeDirectory)
//     names its login and object ID (sid) (400);
//   - Entra-only authentication is enabled only once the workspace has an
//     Entra admin (400);
//   - a workspace key names a Key Vault key URL (400);
//   - an AzureKeyVault encryption protector names a key of the workspace
//     (400), which exists (404);
//   - enabled auditing names a storage endpoint or sends to Azure Monitor
//     (400);
//   - a vulnerability assessment names a blob container URL (400);
//   - an integration runtime is Managed or SelfHosted (400); a self-hosted
//     runtime linked by RBAC names the shared runtime, which exists (404);
//   - the singletons are named as the spec's enums say (default, Default
//     for the security alert policy, current for the encryption protector)
//     (400), and have no DELETE (405): Azure deletes them with the
//     workspace.
//
// Private endpoint connections follow the shared rule (privateendpoints.go).
// Not modelled: the workspace's SQL endpoints (no audit, alert or scan
// runs), the key's activation, the runtimes' nodes and states, and the
// error codes (the fake's own).
var (
	synWorkspace  = `(?i)^(.*/providers/microsoft\.synapse/workspaces/[^/]+)`
	synFirewall   = regexp.MustCompile(synWorkspace + `/firewallrules/[^/]+$`)
	synAdmin      = regexp.MustCompile(synWorkspace + `/(sql)?administrators/activedirectory$`)
	synEntraOnly  = regexp.MustCompile(synWorkspace + `/azureadonlyauthentications/([^/]+)$`)
	synKey        = regexp.MustCompile(synWorkspace + `/keys/[^/]+$`)
	synProtector  = regexp.MustCompile(synWorkspace + `/encryptionprotector/([^/]+)$`)
	synAuditing   = regexp.MustCompile(synWorkspace + `/(extended)?auditingsettings/([^/]+)$`)
	synAlerts     = regexp.MustCompile(synWorkspace + `/securityalertpolicies/([^/]+)$`)
	synAssessment = regexp.MustCompile(synWorkspace + `/vulnerabilityassessments/([^/]+)$`)
	synMinimalTLS = regexp.MustCompile(synWorkspace + `/dedicatedsqlminimaltlssettings/([^/]+)$`)
	synSQLControl = regexp.MustCompile(synWorkspace + `/managedidentitysqlcontrolsettings/default$`)
	synRuntime    = regexp.MustCompile(synWorkspace + `/integrationruntimes/[^/]+$`)
	synKeyVault   = regexp.MustCompile(`(?i)^https://[^/]+\.vault\.azure\.net/keys/[^/]+(/[^/]*)?$`)
	synBlobURL    = regexp.MustCompile(`(?i)^https://[^/]+\.blob\.core\.windows\.net/[^/]+`)
)

// synapseUndeletable is whether id is a workspace setting Azure has no
// DELETE for.
func synapseUndeletable(id string) bool {
	for _, re := range []*regexp.Regexp{synEntraOnly, synProtector, synAuditing, synAlerts, synMinimalTLS, synSQLControl} {
		if re.MatchString(id) {
			return true
		}
	}
	return false
}

// checkSynapse refuses what the rules above do not allow; code is "" when
// it is allowed or id is none of these.
func (a *ARM) checkSynapse(id string, body map[string]any) (status int, code, message string) {
	bad := func(msg string) (int, string, string) {
		return http.StatusBadRequest, "BadRequest", msg
	}
	named := func(name, want, what string) (int, string, string) {
		if name != want {
			return bad("A workspace's " + what + " is named " + want + ", not " + name + ".")
		}
		return 0, "", ""
	}
	props, _ := body["properties"].(map[string]any)
	switch {
	case synFirewall.MatchString(id):
		start, end := net.ParseIP(stringOf(props["startIpAddress"])).To4(), net.ParseIP(stringOf(props["endIpAddress"])).To4()
		if start == nil || end == nil {
			return bad("A firewall rule names its startIpAddress and endIpAddress, IPv4 addresses.")
		}
		if bytes.Compare(start, end) > 0 {
			return bad("A firewall rule's startIpAddress is not after its endIpAddress.")
		}
	case synAdmin.MatchString(id):
		if stringOf(props["login"]) == "" || stringOf(props["sid"]) == "" {
			return bad("An Entra admin names its login and sid.")
		}
	case synEntraOnly.MatchString(id):
		m := synEntraOnly.FindStringSubmatch(id)
		if s, c, msg := named(m[2], "default", "Entra-only authentication"); c != "" {
			return s, c, msg
		}
		if on, _ := props["azureADOnlyAuthentication"].(bool); on && !a.exists(m[1]+"/administrators/activeDirectory") && !a.exists(m[1]+"/sqlAdministrators/activeDirectory") {
			return bad("Entra-only authentication cannot be enabled before the workspace has an Entra admin.")
		}
	case synKey.MatchString(id):
		if !synKeyVault.MatchString(stringOf(props["keyVaultUrl"])) {
			return bad("A workspace key names a Key Vault key URL (https://{vault}.vault.azure.net/keys/{key}).")
		}
	case synProtector.MatchString(id):
		m := synProtector.FindStringSubmatch(id)
		if s, c, msg := named(m[2], "current", "encryption protector"); c != "" {
			return s, c, msg
		}
		switch stringOf(props["serverKeyType"]) {
		case "ServiceManaged":
		case "AzureKeyVault":
			key := stringOf(props["serverKeyName"])
			if key == "" {
				return bad("An AzureKeyVault encryption protector names its serverKeyName.")
			}
			if !a.exists(m[1] + "/keys/" + key) {
				return http.StatusNotFound, "ResourceNotFound", "The workspace key " + key + " was not found."
			}
		default:
			return bad("An encryption protector's serverKeyType is ServiceManaged or AzureKeyVault.")
		}
	case synAuditing.MatchString(id):
		if s, c, msg := named(synAuditing.FindStringSubmatch(id)[3], "default", "auditing setting"); c != "" {
			return s, c, msg
		}
		monitor, _ := props["isAzureMonitorTargetEnabled"].(bool)
		if stringOf(props["state"]) == "Enabled" && stringOf(props["storageEndpoint"]) == "" && !monitor {
			return bad("Enabled auditing names a storageEndpoint or sets isAzureMonitorTargetEnabled.")
		}
	case synAlerts.MatchString(id):
		return named(synAlerts.FindStringSubmatch(id)[2], "Default", "security alert policy")
	case synAssessment.MatchString(id):
		if s, c, msg := named(synAssessment.FindStringSubmatch(id)[2], "default", "vulnerability assessment"); c != "" {
			return s, c, msg
		}
		if !synBlobURL.MatchString(stringOf(props["storageContainerPath"])) {
			return bad("A vulnerability assessment names a blob container URL (storageContainerPath).")
		}
	case synMinimalTLS.MatchString(id):
		return named(synMinimalTLS.FindStringSubmatch(id)[2], "default", "dedicated SQL minimal TLS setting")
	case synRuntime.MatchString(id):
		switch stringOf(props["type"]) {
		case "Managed":
		case "SelfHosted":
			tp, _ := props["typeProperties"].(map[string]any)
			linked, _ := tp["linkedInfo"].(map[string]any)
			if strings.EqualFold(stringOf(linked["authorizationType"]), "RBAC") {
				shared := stringOf(linked["resourceId"])
				if shared == "" {
					return bad("A linked self-hosted integration runtime names the shared runtime (resourceId).")
				}
				if !a.exists(shared) {
					return http.StatusNotFound, "ResourceNotFound", "The integration runtime " + shared + " was not found."
				}
			}
		default:
			return bad("An integration runtime's type is Managed or SelfHosted.")
		}
	}
	return 0, "", ""
}
