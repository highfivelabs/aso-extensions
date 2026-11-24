// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// A Synapse workspace's children: firewall rules are ordered IPv4 ranges;
// Entra admins name a login and sid, and Entra-only authentication waits
// for one; keys are Key Vault key URLs and a protector names one of them;
// enabled auditing has a destination; an assessment writes to a blob
// container; a runtime is Managed or SelfHosted and a linked one names what
// it shares; the singletons keep their names and cannot be deleted.
func TestSynapse(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/syn"
	ws := rg + "/providers/Microsoft.Synapse/workspaces/analytics"
	shared := rg + "/providers/Microsoft.Synapse/workspaces/hub/integrationRuntimes/shared"
	for _, id := range []string{rg, ws, shared} {
		a.AddResource(id)
	}
	put := func(what, id, props string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, id, `{"properties":`+props+`}`)
		expect(t, what, code, resp, want)
	}
	put("a rule with no end", ws+"/firewallRules/office", `{"startIpAddress":"10.0.0.1"}`, http.StatusBadRequest)
	put("a rule ending before it starts", ws+"/firewallRules/office", `{"startIpAddress":"10.0.0.9","endIpAddress":"10.0.0.1"}`, http.StatusBadRequest)
	put("a firewall rule", ws+"/firewallRules/office", `{"startIpAddress":"10.0.0.1","endIpAddress":"10.0.0.9"}`, http.StatusCreated)

	put("Entra-only authentication with no admin", ws+"/azureADOnlyAuthentications/default", `{"azureADOnlyAuthentication":true}`, http.StatusBadRequest)
	put("Entra-only authentication off", ws+"/azureADOnlyAuthentications/default", `{"azureADOnlyAuthentication":false}`, http.StatusCreated)
	put("an admin with no sid", ws+"/sqlAdministrators/activeDirectory", `{"login":"dba"}`, http.StatusBadRequest)
	put("an admin", ws+"/sqlAdministrators/activeDirectory", `{"login":"dba","sid":"00000000-0000-0000-0000-000000000001","tenantId":"00000000-0000-0000-0000-000000000002","administratorType":"ActiveDirectory"}`, http.StatusCreated)
	put("Entra-only authentication", ws+"/azureADOnlyAuthentications/default", `{"azureADOnlyAuthentication":true}`, http.StatusOK)
	put("Entra-only authentication misnamed", ws+"/azureADOnlyAuthentications/other", `{"azureADOnlyAuthentication":false}`, http.StatusBadRequest)

	put("a key that is no Key Vault key", ws+"/keys/cmk", `{"keyVaultUrl":"https://example.com/cmk"}`, http.StatusBadRequest)
	put("a key", ws+"/keys/cmk", `{"keyVaultUrl":"https://vault.vault.azure.net/keys/cmk","isActiveCMK":true}`, http.StatusCreated)
	put("a protector to a missing key", ws+"/encryptionProtector/current", `{"serverKeyType":"AzureKeyVault","serverKeyName":"other"}`, http.StatusNotFound)
	put("a protector misnamed", ws+"/encryptionProtector/next", `{"serverKeyType":"ServiceManaged"}`, http.StatusBadRequest)
	put("a protector", ws+"/encryptionProtector/current", `{"serverKeyType":"AzureKeyVault","serverKeyName":"cmk"}`, http.StatusCreated)

	put("auditing to nowhere", ws+"/auditingSettings/default", `{"state":"Enabled"}`, http.StatusBadRequest)
	put("auditing to Azure Monitor", ws+"/auditingSettings/default", `{"state":"Enabled","isAzureMonitorTargetEnabled":true}`, http.StatusCreated)
	put("extended auditing to storage", ws+"/extendedAuditingSettings/default", `{"state":"Enabled","storageEndpoint":"https://logs.blob.core.windows.net"}`, http.StatusCreated)
	put("alerts misnamed", ws+"/securityAlertPolicies/default", `{"state":"Enabled"}`, http.StatusBadRequest)
	put("alerts", ws+"/securityAlertPolicies/Default", `{"state":"Enabled"}`, http.StatusCreated)
	put("an assessment with no container", ws+"/vulnerabilityAssessments/default", `{"storageContainerPath":"logs"}`, http.StatusBadRequest)
	put("an assessment", ws+"/vulnerabilityAssessments/default", `{"storageContainerPath":"https://logs.blob.core.windows.net/scans"}`, http.StatusCreated)
	put("a minimal TLS setting", ws+"/dedicatedSQLminimalTlsSettings/default", `{"minimalTlsVersion":"1.2"}`, http.StatusCreated)

	put("a runtime of no type", ws+"/integrationRuntimes/ir", `{"typeProperties":{}}`, http.StatusBadRequest)
	put("a linked runtime to nothing", ws+"/integrationRuntimes/linked", `{"type":"SelfHosted","typeProperties":{"linkedInfo":{"authorizationType":"RBAC","resourceId":"`+shared+`x"}}}`, http.StatusNotFound)
	put("a linked runtime", ws+"/integrationRuntimes/linked", `{"type":"SelfHosted","typeProperties":{"linkedInfo":{"authorizationType":"RBAC","resourceId":"`+shared+`"}}}`, http.StatusCreated)
	put("a managed runtime", ws+"/integrationRuntimes/managed", `{"type":"Managed","typeProperties":{"computeProperties":{"location":"AutoResolve"}}}`, http.StatusCreated)

	for _, id := range []string{ws + "/auditingSettings/default", ws + "/encryptionProtector/current", ws + "/azureADOnlyAuthentications/default", ws + "/securityAlertPolicies/Default"} {
		code, resp := do(t, a, http.MethodDelete, id, "")
		expect(t, "deleting "+id, code, resp, http.StatusMethodNotAllowed)
	}
	for _, id := range []string{ws + "/firewallRules/office", ws + "/keys/cmk", ws + "/vulnerabilityAssessments/default", ws + "/integrationRuntimes/managed"} {
		code, resp := do(t, a, http.MethodDelete, id, "")
		if code != http.StatusOK && code != http.StatusNoContent && code != http.StatusAccepted {
			t.Errorf("deleting %s: %d %s", id, code, resp)
		}
	}
}
