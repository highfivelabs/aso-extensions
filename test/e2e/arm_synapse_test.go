// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	asosynapse "github.com/Azure/azure-service-operator/v2/api/synapse/v20210601/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	synapse "github.com/highfivelabs/aso-extensions/apis/arm/synapse/v20210601"
)

// The Synapse kinds of docs/plan.md f80, on ASO's Workspace by name (a new
// external owner): a firewall rule, the SQL Entra admin and then Entra-only
// authentication, a customer-managed key and the encryption protector
// naming it, auditing to a storage account with its key from a Secret, a
// security alert policy, a vulnerability assessment, the minimal TLS and
// managed identity settings, a managed and a linked self-hosted integration
// runtime, the approval of a private endpoint's connection, and a private
// link hub in the resource group. A firewall rule ending before it starts,
// a key that is no Key Vault key and a protector naming a missing key are
// refused (the fake's rules, synapse.go); a change is PUT again. Deleted,
// all go from the fake but the settings Azure has no DELETE for (left by
// the Deleter extension).
func TestSynapseKinds(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "syn")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	wsID := rgID + "/providers/Microsoft.Synapse/workspaces/analytics"
	sharedID := rgID + "/providers/Microsoft.Synapse/workspaces/hub/integrationRuntimes/shared"
	for _, id := range []string{rgID, wsID, sharedID, wsID + "/privateEndpointConnections/analytics.pe"} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	ws := &asosynapse.Workspace{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "analytics"}}
	ws.Spec.AzureName, ws.Spec.Location = "analytics", ptr("westus")
	ws.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, ws, wsID)
	onWS := &genruntime.KnownResourceReference{Name: "analytics"}
	if err := k8s.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "audit-storage"},
		StringData: map[string]string{"key": "c3RvcmFnZS1rZXk="},
	}); err != nil {
		t.Fatal(err)
	}
	meta := func(name string) metav1.ObjectMeta { return metav1.ObjectMeta{Namespace: ns, Name: name} }

	rule := &synapse.WorkspacesFirewallRule{ObjectMeta: meta("office"), Spec: synapse.WorkspacesFirewallRule_Spec{
		Owner: onWS, StartIpAddress: ptr("10.0.0.1"), EndIpAddress: ptr("10.0.0.9")}}
	reversed := &synapse.WorkspacesFirewallRule{ObjectMeta: meta("reversed"), Spec: synapse.WorkspacesFirewallRule_Spec{
		Owner: onWS, StartIpAddress: ptr("10.0.0.9"), EndIpAddress: ptr("10.0.0.1")}}
	admin := &synapse.WorkspacesSqlAdministrator{ObjectMeta: meta("sql-admin"), Spec: synapse.WorkspacesSqlAdministrator_Spec{
		Owner: onWS, AdministratorType: ptr("ActiveDirectory"), Login: ptr("dba"),
		Sid: ptr("00000000-0000-0000-0000-0000000000a1"), TenantId: ptr("00000000-0000-0000-0000-0000000000a2")}}
	key := &synapse.WorkspacesKey{ObjectMeta: meta("cmk"), Spec: synapse.WorkspacesKey_Spec{
		Owner: onWS, KeyVaultUrl: ptr("https://analytics.vault.azure.net/keys/cmk"), IsActiveCMK: ptr(true)}}
	badKey := &synapse.WorkspacesKey{ObjectMeta: meta("not-a-key"), Spec: synapse.WorkspacesKey_Spec{
		Owner: onWS, KeyVaultUrl: ptr("https://example.com/cmk")}}
	auditing := &synapse.WorkspacesAuditingSetting{ObjectMeta: meta("auditing"), Spec: synapse.WorkspacesAuditingSetting_Spec{
		Owner: onWS, State: ptr(synapse.ServerBlobAuditingPolicyProperties_State_Enabled), RetentionDays: ptr(30),
		StorageEndpoint:         ptr("https://auditlogs.blob.core.windows.net"),
		StorageAccountAccessKey: &genruntime.SecretReference{Name: "audit-storage", Key: "key"}}}
	alerts := &synapse.WorkspacesSecurityAlertPolicy{ObjectMeta: meta("alerts"), Spec: synapse.WorkspacesSecurityAlertPolicy_Spec{
		Owner: onWS, State: ptr(synapse.ServerSecurityAlertPolicyProperties_State_Enabled), EmailAddresses: []string{"security@example.com"}}}
	assessment := &synapse.WorkspacesVulnerabilityAssessment{ObjectMeta: meta("assessment"), Spec: synapse.WorkspacesVulnerabilityAssessment_Spec{
		Owner: onWS, StorageContainerPath: ptr("https://auditlogs.blob.core.windows.net/scans"),
		StorageAccountAccessKey: &genruntime.SecretReference{Name: "audit-storage", Key: "key"},
		RecurringScans:          &synapse.VulnerabilityAssessmentRecurringScansProperties{IsEnabled: ptr(true), EmailSubscriptionAdmins: ptr(false)}}}
	tls := &synapse.WorkspacesDedicatedSQLMinimalTlsSetting{ObjectMeta: meta("tls"), Spec: synapse.WorkspacesDedicatedSQLMinimalTlsSetting_Spec{
		Owner: onWS, MinimalTlsVersion: ptr("1.2")}}
	sqlControl := &synapse.WorkspacesManagedIdentitySqlControlSetting{ObjectMeta: meta("sql-control"), Spec: synapse.WorkspacesManagedIdentitySqlControlSetting_Spec{
		Owner: onWS, GrantSqlControlToManagedIdentity: &synapse.Workspaces_ManagedIdentitySqlControlSetting_Properties_GrantSqlControlToManagedIdentity_Spec{
			DesiredState: ptr(synapse.Workspaces_ManagedIdentitySqlControlSetting_Properties_GrantSqlControlToManagedIdentity_DesiredState_Spec_Enabled)}}}
	managed := &synapse.WorkspacesIntegrationRuntime{ObjectMeta: meta("managed"), Spec: synapse.WorkspacesIntegrationRuntime_Spec{
		Owner: onWS, Properties: &synapse.IntegrationRuntime{Managed: &synapse.ManagedIntegrationRuntime{
			Type: ptr(synapse.ManagedIntegrationRuntime_Type_Managed), Description: ptr("data flows"),
			ComputeProperties: &synapse.IntegrationRuntimeComputeProperties{Location: ptr("AutoResolve"), DataFlowProperties: &synapse.IntegrationRuntimeDataFlowProperties{
				ComputeType: ptr(synapse.IntegrationRuntimeDataFlowProperties_ComputeType_General), CoreCount: ptr(8), TimeToLive: ptr(10)}}}}}}
	linked := &synapse.WorkspacesIntegrationRuntime{ObjectMeta: meta("linked"), Spec: synapse.WorkspacesIntegrationRuntime_Spec{
		Owner: onWS, Properties: &synapse.IntegrationRuntime{SelfHosted: &synapse.SelfHostedIntegrationRuntime{
			Type: ptr(synapse.SelfHostedIntegrationRuntime_Type_SelfHosted),
			LinkedInfo: &synapse.LinkedIntegrationRuntimeType{RBAC: &synapse.LinkedIntegrationRuntimeRbacAuthorization{
				AuthorizationType: ptr(synapse.LinkedIntegrationRuntimeRbacAuthorization_AuthorizationType_RBAC),
				ResourceReference: &genruntime.ResourceReference{ARMID: sharedID}}}}}}}
	conn := &synapse.WorkspacesPrivateEndpointConnection{ObjectMeta: meta("analytics-pe"), Spec: synapse.WorkspacesPrivateEndpointConnection_Spec{
		AzureName: "analytics.pe", Owner: onWS,
		PrivateLinkServiceConnectionState: &synapse.PrivateLinkServiceConnectionState{Status: ptr("Approved"), Description: ptr("approved")}}}
	hub := &synapse.PrivateLinkHub{ObjectMeta: meta("studio"), Spec: synapse.PrivateLinkHub_Spec{
		Owner: &genruntime.KnownResourceReference{Name: "rg"}, Location: ptr("westus"), Tags: map[string]string{"team": "data"}}}

	first := []conditioned{rule, reversed, admin, key, badKey, auditing, alerts, assessment, tls, sqlControl, managed, linked, conn, hub}
	for _, o := range first {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []conditioned{rule, admin, key, auditing, alerts, assessment, tls, sqlControl, managed, linked, conn, hub} {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	eventually(t, timeout, "a firewall rule ending before it starts refused", notReadyWith(reversed, "not after"))
	eventually(t, timeout, "a key that is no Key Vault key refused", notReadyWith(badKey, "Key Vault key URL"))

	// What needs the admin and the key: Entra-only authentication and the
	// protector; a protector naming a missing key is refused first.
	entraOnly := &synapse.WorkspacesAzureADOnlyAuthentication{ObjectMeta: meta("entra-only"), Spec: synapse.WorkspacesAzureADOnlyAuthentication_Spec{
		Owner: onWS, AzureADOnlyAuthentication: ptr(true)}}
	protector := &synapse.WorkspacesEncryptionProtector{ObjectMeta: meta("protector"), Spec: synapse.WorkspacesEncryptionProtector_Spec{
		Owner: onWS, ServerKeyType: ptr(synapse.EncryptionProtectorProperties_ServerKeyType_AzureKeyVault), ServerKeyName: ptr("other")}}
	second := []conditioned{entraOnly, protector}
	for _, o := range second {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "Entra-only authentication Ready", ready(entraOnly))
	eventually(t, timeout, "a protector naming a missing key refused", notReadyWith(protector, "was not found"))
	if err := change(t, protector, func(p *synapse.WorkspacesEncryptionProtector) { p.Spec.ServerKeyName = ptr("cmk") }); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the protector Ready", ready(protector))

	expectResource(t, wsID+"/auditingSettings/default", func(r map[string]any) error {
		if p := properties(r); p["state"] != "Enabled" || p["storageAccountAccessKey"] != "c3RvcmFnZS1rZXk=" || p["storageEndpoint"] != "https://auditlogs.blob.core.windows.net" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, wsID+"/integrationRuntimes/linked", func(r map[string]any) error {
		p := properties(r)
		tp, _ := p["typeProperties"].(map[string]any)
		li, _ := tp["linkedInfo"].(map[string]any)
		if p["type"] != "SelfHosted" || li["authorizationType"] != "RBAC" || li["resourceId"] != sharedID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, wsID+"/encryptionProtector/current", func(r map[string]any) error {
		if p := properties(r); p["serverKeyName"] != "cmk" || p["serverKeyType"] != "AzureKeyVault" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	// A change is PUT again: the firewall rule's range.
	if err := change(t, rule, func(r *synapse.WorkspacesFirewallRule) { r.Spec.EndIpAddress = ptr("10.0.0.20") }); err != nil {
		t.Fatal(err)
	}
	expectResource(t, wsID+"/firewallRules/office", func(r map[string]any) error {
		if properties(r)["endIpAddress"] != "10.0.0.20" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, objs := range [][]conditioned{second, first} {
		for _, o := range objs {
			if err := k8s.Delete(ctx, o); err != nil {
				t.Fatal(err)
			}
		}
		for _, o := range objs {
			eventually(t, timeout, o.GetName()+" gone", gone(o))
		}
	}
	for _, id := range []string{wsID + "/firewallRules/office", wsID + "/sqlAdministrators/activeDirectory", wsID + "/keys/cmk",
		wsID + "/vulnerabilityAssessments/default", wsID + "/integrationRuntimes/managed", wsID + "/integrationRuntimes/linked",
		wsID + "/privateEndpointConnections/analytics.pe", rgID + "/providers/Microsoft.Synapse/privateLinkHubs/studio"} {
		if armFake.HasResource(id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
	for _, id := range []string{wsID + "/auditingSettings/default", wsID + "/securityAlertPolicies/Default", wsID + "/encryptionProtector/current",
		wsID + "/azureADOnlyAuthentications/default", wsID + "/dedicatedSQLminimalTlsSettings/default", wsID + "/managedIdentitySqlControlSettings/default"} {
		if !armFake.HasResource(id) {
			t.Errorf("%s, which Azure cannot delete, is gone from the fake", id)
		}
	}
}
