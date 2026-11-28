// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"strings"
	"testing"

	asosynapse "github.com/Azure/azure-service-operator/v2/api/synapse/v20210601/storage"

	synapse "github.com/highfivelabs/aso-extensions/apis/arm/synapse/v20210601"
)

const synapseAPI = "2021-06-01"

// A Synapse workspace's firewall rule and managed integration runtime, and
// a private link hub, on a workspace made as a fixture (docs/plan.md f80):
// created, the rule changed, deleted. Storage cents: the workspace needs a
// Data Lake Storage Gen2 account and file system; the workspace itself,
// with no SQL or Spark pool, bills only serverless queries (none is run);
// the runtime bills data flow runs (none); the hub is free. Not here: the
// Entra admin and Entra-only authentication (a real principal), keys and
// the encryption protector (a Key Vault key and its access policy),
// auditing, alerts and assessments (their storage or Monitor targets),
// connections (a private endpoint), linked self-hosted runtimes.
func TestLiveSynapseWorkspaceChildren(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.Synapse")
	ns := namespace(t, "syn")
	storageName := strings.ReplaceAll(azName("synlake"), "-", "")
	storageID := rgID() + "/providers/Microsoft.Storage/storageAccounts/" + storageName
	fixture(t, storageID, "2023-05-01", map[string]any{
		"location": location, "kind": "StorageV2", "sku": map[string]any{"name": "Standard_LRS"},
		"properties": map[string]any{"minimumTlsVersion": "TLS1_2", "allowBlobPublicAccess": false, "isHnsEnabled": true},
	})
	fixture(t, storageID+"/blobServices/default/containers/synapse", "2023-05-01", map[string]any{"properties": map[string]any{}})
	name := azName("syn")
	wsID := rgID() + "/providers/Microsoft.Synapse/workspaces/" + name
	fixture(t, wsID, synapseAPI, map[string]any{
		"location": location, "identity": map[string]any{"type": "SystemAssigned"},
		"properties": map[string]any{
			"defaultDataLakeStorage": map[string]any{"accountUrl": "https://" + storageName + ".dfs.core.windows.net", "filesystem": "synapse", "resourceId": storageID},
			"sqlAdministratorLogin":  "sqladminuser", "sqlAdministratorLoginPassword": password(),
		},
	})
	ws := &asosynapse.Workspace{}
	ws.Namespace, ws.Name = ns, "analytics"
	ws.Spec.AzureName = name
	ws.Spec.Location = ptr(location)
	ws.Spec.Owner = owner("rg")
	standIn(t, ws, wsID, nil)

	rule := &synapse.WorkspacesFirewallRule{}
	rule.Namespace, rule.Name = ns, "office"
	rule.Spec = synapse.WorkspacesFirewallRule_Spec{Owner: owner("analytics"), StartIpAddress: ptr("203.0.113.1"), EndIpAddress: ptr("203.0.113.9")}
	runtime := &synapse.WorkspacesIntegrationRuntime{}
	runtime.Namespace, runtime.Name = ns, "dataflows"
	runtime.Spec = synapse.WorkspacesIntegrationRuntime_Spec{Owner: owner("analytics"), Properties: &synapse.IntegrationRuntime{Managed: &synapse.ManagedIntegrationRuntime{
		Type: ptr(synapse.ManagedIntegrationRuntime_Type_Managed), Description: ptr("live test"),
		ComputeProperties: &synapse.IntegrationRuntimeComputeProperties{Location: ptr("AutoResolve"), DataFlowProperties: &synapse.IntegrationRuntimeDataFlowProperties{
			ComputeType: ptr(synapse.IntegrationRuntimeDataFlowProperties_ComputeType_General), CoreCount: ptr(8), TimeToLive: ptr(10)}}}}}
	hub := &synapse.PrivateLinkHub{}
	hub.Namespace, hub.Name = ns, "studio"
	hubName := strings.ReplaceAll(azName("synhub"), "-", "")
	hub.Spec = synapse.PrivateLinkHub_Spec{AzureName: hubName, Owner: owner("rg"), Location: ptr(location)}
	create(t, timeout, rule, runtime, hub)

	expectResource(t, wsID+"/integrationRuntimes/dataflows", synapseAPI, func(r map[string]any) error {
		if properties(r)["type"] != "Managed" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, rule, func(r *synapse.WorkspacesFirewallRule) { r.Spec.EndIpAddress = ptr("203.0.113.20") })
	eventually(t, timeout, "the rule Ready after the change", ready(rule))
	expectResource(t, wsID+"/firewallRules/office", synapseAPI, func(r map[string]any) error {
		if properties(r)["endIpAddress"] != "203.0.113.20" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, hub)
	remove(t, timeout, runtime)
	remove(t, timeout, rule)
	expectGone(t, wsID+"/firewallRules/office", synapseAPI)
	expectGone(t, rgID()+"/providers/Microsoft.Synapse/privateLinkHubs/"+hubName, synapseAPI)
}
