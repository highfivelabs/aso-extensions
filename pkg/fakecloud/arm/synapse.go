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
// A dedicated SQL pool's (f81):
//
//   - a pool names a SKU (400); a restore names its source pool and point
//     in time, a recovery its recoverable database, a restore of a
//     dropped pool its source and deletion date (400), and what it names
//     exists (404);
//   - its settings are named as the spec's enums say (Default for the
//     geo-backup and data masking policies, current for transparent data
//     encryption, default for the rest) (400) and have no DELETE (405);
//   - a data masking rule's Number function names numberFrom and numberTo,
//     its Text function prefixSize, suffixSize and replacementString (400);
//   - a workload group's percentages run 0 <= min <= max <= 100, and the
//     per-request minimum is not over the maximum (400).
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
	synPool       = synWorkspace + `/sqlpools/[^/]+`
	synSQLPool    = regexp.MustCompile(synWorkspace + `/sqlpools/[^/]+$`)
	synPoolNamed  = regexp.MustCompile(synPool + `/(geobackuppolicies|transparentdataencryption|auditingsettings|extendedauditingsettings|securityalertpolicies|vulnerabilityassessments|datamaskingpolicies)/([^/]+)$`)
	synPoolWindow = regexp.MustCompile(synPool + `/maintenancewindows/current$`)
	synMaskRule   = regexp.MustCompile(synPool + `/datamaskingpolicies/[^/]+/rules/[^/]+$`)
	synWorkload   = regexp.MustCompile(synPool + `/workloadgroups/[^/]+$`)
	synPoolNames  = map[string]string{
		"geobackuppolicies": "Default", "transparentdataencryption": "current", "auditingsettings": "default",
		"extendedauditingsettings": "default", "securityalertpolicies": "default", "vulnerabilityassessments": "default",
		"datamaskingpolicies": "Default",
	}
	synKeyVault = regexp.MustCompile(`(?i)^https://[^/]+\.vault\.azure\.net/keys/[^/]+(/[^/]*)?$`)
	synBlobURL  = regexp.MustCompile(`(?i)^https://[^/]+\.blob\.core\.windows\.net/[^/]+`)
)

// synapseUndeletable is whether id is a workspace setting Azure has no
// DELETE for.
func synapseUndeletable(id string) bool {
	for _, re := range []*regexp.Regexp{synEntraOnly, synProtector, synAuditing, synAlerts, synMinimalTLS, synSQLControl, synPoolWindow, synMaskRule} {
		if re.MatchString(id) {
			return true
		}
	}
	m := synPoolNamed.FindStringSubmatch(id)
	return m != nil && !strings.EqualFold(m[2], "vulnerabilityassessments")
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
	case synSQLPool.MatchString(id):
		return a.checkSynapseSQLPool(body, props)
	case synPoolNamed.MatchString(id):
		m := synPoolNamed.FindStringSubmatch(id)
		want := synPoolNames[strings.ToLower(m[2])]
		if m[3] != want {
			return bad("A SQL pool's " + m[2] + " is named " + want + ", not " + m[3] + ".")
		}
		if strings.EqualFold(m[2], "vulnerabilityassessments") && !synBlobURL.MatchString(stringOf(props["storageContainerPath"])) {
			return bad("A vulnerability assessment names a blob container URL (storageContainerPath).")
		}
		if strings.HasSuffix(strings.ToLower(m[2]), "auditingsettings") {
			monitor, _ := props["isAzureMonitorTargetEnabled"].(bool)
			if stringOf(props["state"]) == "Enabled" && stringOf(props["storageEndpoint"]) == "" && !monitor {
				return bad("Enabled auditing names a storageEndpoint or sets isAzureMonitorTargetEnabled.")
			}
		}
	case synMaskRule.MatchString(id):
		switch stringOf(props["maskingFunction"]) {
		case "Number":
			if stringOf(props["numberFrom"]) == "" || stringOf(props["numberTo"]) == "" {
				return bad("A Number masking rule names numberFrom and numberTo.")
			}
		case "Text":
			if stringOf(props["prefixSize"]) == "" || stringOf(props["suffixSize"]) == "" || props["replacementString"] == nil {
				return bad("A Text masking rule names prefixSize, suffixSize and replacementString.")
			}
		}
	case synWorkload.MatchString(id):
		num := func(k string) float64 { f, _ := props[k].(float64); return f }
		lo, hi := num("minResourcePercent"), num("maxResourcePercent")
		if lo < 0 || lo > hi || hi > 100 || hi == 0 {
			return bad("A workload group's minResourcePercent and maxResourcePercent run 0 <= min <= max <= 100.")
		}
		if num("minResourcePercentPerRequest") > hi {
			return bad("A workload group's minResourcePercentPerRequest is not over its maxResourcePercent.")
		}
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

// checkSynapseSQLPool checks a dedicated SQL pool's SKU and what its create
// mode names.
func (a *ARM) checkSynapseSQLPool(body, props map[string]any) (status int, code, message string) {
	bad := func(msg string) (int, string, string) {
		return http.StatusBadRequest, "BadRequest", msg
	}
	sku, _ := body["sku"].(map[string]any)
	if stringOf(sku["name"]) == "" {
		return bad("A SQL pool names its SKU (DW100c and up).")
	}
	var needs []string
	switch stringOf(props["createMode"]) {
	case "PointInTimeRestore":
		needs = []string{"sourceDatabaseId", "restorePointInTime"}
	case "Recovery":
		needs = []string{"recoverableDatabaseId"}
	case "Restore":
		needs = []string{"sourceDatabaseId", "sourceDatabaseDeletionDate"}
	}
	for _, k := range needs {
		if stringOf(props[k]) == "" {
			return bad("A SQL pool made with createMode " + stringOf(props["createMode"]) + " names its " + k + ".")
		}
	}
	for _, k := range []string{"sourceDatabaseId", "recoverableDatabaseId"} {
		if id := stringOf(props[k]); id != "" && !a.exists(id) {
			return http.StatusNotFound, "ResourceNotFound", "The " + k + " " + id + " was not found."
		}
	}
	return 0, "", ""
}
