// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Azure Automation (added 2026-11-13, for the catalog's
// avm-ptn-alz-management): an account's automationHybridServiceUrl, which
// Azure computes, and its agent registration information
// (GET …/automationAccounts/{name}/agentRegistrationInformation), which is
// not a resource of its own: the DSC server endpoint and the registration
// keys, read by a hand-written exporter of the AutomationAccount kind.
// Both are derived from the account's ID, as Azure derives them from the
// account's own GUID; the keys are not real keys.
var (
	automationAccount = regexp.MustCompile(`(?i)/providers/microsoft\.automation/automationaccounts/[^/]+$`)
	agentRegistration = regexp.MustCompile(`(?i)^(.*/providers/microsoft\.automation/automationaccounts/[^/]+)/agentregistrationinformation$`)
)

// automationHost is the account's GUID and region, as Azure writes them in
// the account's service URLs.
func automationHost(id, location string) (guid, region string) {
	return derivedGUID(id, "automation-account"), strings.ToLower(strings.ReplaceAll(location, " ", ""))
}

// computeAutomationAccount fills an account's automationHybridServiceUrl.
func computeAutomationAccount(id, location string, props map[string]any) {
	guid, region := automationHost(id, location)
	setIfMissing(props, "automationHybridServiceUrl", "https://"+guid+".jrds."+region+".azure-automation.net/automationAccounts/"+guid)
}

// serveAgentRegistration answers GET …/agentRegistrationInformation for an
// account the store holds.
func (a *ARM) serveAgentRegistration(w http.ResponseWriter, r *http.Request, path string) (handled bool) {
	m := agentRegistration.FindStringSubmatch(path)
	if m == nil {
		return false
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
		return true
	}
	res, ok := a.resources[strings.ToLower(m[1])]
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "The Automation account "+m[1]+" was not found.")
		return true
	}
	location, _ := (*res)["location"].(string)
	guid, region := automationHost(m[1], location)
	endpoint := "https://" + guid + ".agentsvc." + region + ".azure-automation.net/accounts/" + guid
	writeJSON(w, http.StatusOK, map[string]any{
		"id":                   path,
		"endpoint":             endpoint,
		"dscMetaConfiguration": "[DscLocalConfigurationManager()] Configuration DscMetaConfigs { } # " + endpoint,
		"keys": map[string]any{
			"primary":   derivedGUID(m[1], "agent-primary-key"),
			"secondary": derivedGUID(m[1], "agent-secondary-key"),
		},
	})
	return true
}
