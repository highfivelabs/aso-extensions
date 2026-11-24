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

// A dedicated SQL pool names a SKU and, for a restore, what it restores;
// its settings keep their names and cannot be deleted; masking rules carry
// their function's parameters; workload groups' percentages are ordered.
func TestSynapseSQLPool(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/syn"
	ws := rg + "/providers/Microsoft.Synapse/workspaces/analytics"
	for _, id := range []string{rg, ws} {
		a.AddResource(id)
	}
	pool := ws + "/sqlPools/dw"
	put := func(what, id, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, id, body)
		expect(t, what, code, resp, want)
	}
	put("a pool with no SKU", pool, `{"location":"westus","properties":{}}`, http.StatusBadRequest)
	put("a restore with no point in time", ws+"/sqlPools/copy", `{"location":"westus","sku":{"name":"DW100c"},"properties":{"createMode":"PointInTimeRestore","sourceDatabaseId":"`+pool+`"}}`, http.StatusBadRequest)
	put("a restore of a missing pool", ws+"/sqlPools/copy", `{"location":"westus","sku":{"name":"DW100c"},"properties":{"createMode":"PointInTimeRestore","sourceDatabaseId":"`+pool+`","restorePointInTime":"2026-11-24T00:00:00Z"}}`, http.StatusNotFound)
	put("a pool", pool, `{"location":"westus","sku":{"name":"DW100c"},"properties":{"collation":"SQL_Latin1_General_CP1_CI_AS"}}`, http.StatusCreated)
	put("a restore", ws+"/sqlPools/copy", `{"location":"westus","sku":{"name":"DW100c"},"properties":{"createMode":"PointInTimeRestore","sourceDatabaseId":"`+pool+`","restorePointInTime":"2026-11-24T00:00:00Z"}}`, http.StatusCreated)

	put("TDE misnamed", pool+"/transparentDataEncryption/default", `{"properties":{"status":"Enabled"}}`, http.StatusBadRequest)
	put("TDE", pool+"/transparentDataEncryption/current", `{"properties":{"status":"Enabled"}}`, http.StatusCreated)
	put("a geo-backup policy", pool+"/geoBackupPolicies/Default", `{"properties":{"state":"Enabled"}}`, http.StatusCreated)
	put("pool auditing to nowhere", pool+"/auditingSettings/default", `{"properties":{"state":"Enabled"}}`, http.StatusBadRequest)
	put("pool auditing", pool+"/auditingSettings/default", `{"properties":{"state":"Enabled","isAzureMonitorTargetEnabled":true}}`, http.StatusCreated)
	put("a pool assessment with no container", pool+"/vulnerabilityAssessments/default", `{"properties":{}}`, http.StatusBadRequest)
	put("a pool assessment", pool+"/vulnerabilityAssessments/default", `{"properties":{"storageContainerPath":"https://logs.blob.core.windows.net/scans"}}`, http.StatusCreated)
	put("a maintenance window", pool+"/maintenancewindows/current", `{"properties":{"timeRanges":[{"dayOfWeek":"Saturday","startTime":"00:00","duration":"PT60M"}]}}`, http.StatusCreated)
	put("a masking policy", pool+"/dataMaskingPolicies/Default", `{"properties":{"dataMaskingState":"Enabled"}}`, http.StatusCreated)
	rule := pool + "/dataMaskingPolicies/Default/rules/ssn"
	put("a Number rule with no range", rule, `{"properties":{"schemaName":"dbo","tableName":"people","columnName":"ssn","maskingFunction":"Number"}}`, http.StatusBadRequest)
	put("a Text rule", rule, `{"properties":{"schemaName":"dbo","tableName":"people","columnName":"ssn","maskingFunction":"Text","prefixSize":"0","suffixSize":"4","replacementString":"xxx-xx-"}}`, http.StatusCreated)
	put("a workload group upside down", pool+"/workloadGroups/etl", `{"properties":{"minResourcePercent":50,"maxResourcePercent":20,"minResourcePercentPerRequest":5}}`, http.StatusBadRequest)
	put("a workload group", pool+"/workloadGroups/etl", `{"properties":{"minResourcePercent":10,"maxResourcePercent":50,"minResourcePercentPerRequest":5,"importance":"normal"}}`, http.StatusCreated)
	put("a classifier", pool+"/workloadGroups/etl/workloadClassifiers/loader", `{"properties":{"memberName":"loader","importance":"high"}}`, http.StatusCreated)

	for _, id := range []string{pool + "/transparentDataEncryption/current", pool + "/auditingSettings/default", rule, pool + "/maintenancewindows/current", pool + "/dataMaskingPolicies/Default"} {
		code, resp := do(t, a, http.MethodDelete, id, "")
		expect(t, "deleting "+id, code, resp, http.StatusMethodNotAllowed)
	}
	for _, id := range []string{pool + "/workloadGroups/etl/workloadClassifiers/loader", pool + "/workloadGroups/etl", pool + "/vulnerabilityAssessments/default", ws + "/sqlPools/copy"} {
		code, resp := do(t, a, http.MethodDelete, id, "")
		if code != http.StatusOK && code != http.StatusNoContent && code != http.StatusAccepted {
			t.Errorf("deleting %s: %d %s", id, code, resp)
		}
	}
}
