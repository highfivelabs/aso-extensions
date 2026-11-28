// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	app "github.com/highfivelabs/aso-extensions/apis/arm/app/v20260701"
)

// Container Apps sandbox groups and Azure SRE Agent agents (docs/plan.md
// f49) on ASO's ResourceGroup and ManagedEnvironment stand-ins: a sandbox
// group linked to the environment by object and its network connection to a
// subnet by ARM ID; an agent in review mode with its sandbox egress through
// that subnet (AzureVNet), and a connector whose data source string comes
// from a Secret. A sandbox group's link to another environment is refused
// (409) and so is an agent whose subnet is dropped while its egress stays
// AzureVNet (the fake's rules, appextras.go); a change is PUT again; all are
// deleted, children first.
func TestAppSandboxesAndAgents(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "appsandbox")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	envID := rgID + "/providers/Microsoft.App/managedEnvironments/env-" + ns
	otherEnvID := rgID + "/providers/Microsoft.App/managedEnvironments/other-" + ns
	subnetID := rgID + "/providers/Microsoft.Network/virtualNetworks/net/subnets/sandboxes"
	identityID := rgID + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/sre"
	for _, id := range []string{rgID, envID, otherEnvID, subnetID, identityID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	managedEnvironment(t, ns, "env", "rg", ns+"-rg", "env-"+ns)
	secret(t, ns, "kusto", map[string]string{"source": "https://logs.kusto.windows.net;Database=ops"})

	group := &app.SandboxGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "ci"},
		Spec: app.SandboxGroup_Spec{
			AzureName: "ci", Owner: &genruntime.KnownResourceReference{Name: "rg"}, Location: ptr("westus"),
			Properties: &app.SandboxGroupProperties{
				EnvironmentReference: &genruntime.ResourceReference{Group: "app.azure.com", Kind: "ManagedEnvironment", Name: "env"},
			},
		},
	}
	conn := &app.SandboxGroupsVnetConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "ci-net"},
		Spec: app.SandboxGroupsVnetConnection_Spec{
			AzureName: "net", Owner: &genruntime.KnownResourceReference{Name: "ci"},
			Properties: &app.VnetConnectionProperties{SubnetReference: &genruntime.ResourceReference{ARMID: subnetID}},
		},
	}
	agent := &app.Agent{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "sre"},
		Spec: app.Agent_Spec{
			AzureName: "sre-" + ns, Owner: &genruntime.KnownResourceReference{Name: "rg"}, Location: ptr("westus"),
			Properties: &app.AgentProperties{
				ActionConfiguration: &app.ActionConfiguration{
					Mode: ptr(app.AgentMode_Review), AccessLevel: ptr(app.AgentAccessLevel_Low),
					IdentityReference: &genruntime.ResourceReference{ARMID: identityID},
				},
				VnetConfiguration: &app.SreagentVnetConfiguration{SubnetResourceReference: &genruntime.ResourceReference{ARMID: subnetID}},
				SandboxConfiguration: &app.SandboxConfiguration{Egress: &app.SandboxEgressConfiguration{
					Mode: ptr(app.SandboxEgressMode_AzureVNet),
				}},
			},
		},
	}
	connector := &app.AgentsConnector{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "logs"},
		Spec: app.AgentsConnector_Spec{
			AzureName: "logs", Owner: &genruntime.KnownResourceReference{Name: "sre"},
			Properties: &app.AgentConnectorProperties{
				DataConnectorType: ptr("Kusto"),
				DataSource:        &genruntime.SecretReference{Name: "kusto", Key: "source"},
				IdentityReference: &genruntime.ResourceReference{ARMID: identityID},
			},
		},
	}
	objs := []conditioned{group, conn, agent, connector}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}

	groupID := rgID + "/providers/Microsoft.App/sandboxGroups/ci"
	expectResource(t, groupID, func(r map[string]any) error {
		if properties(r)["environmentId"] != envID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, groupID+"/vnetConnections/net", func(r map[string]any) error {
		if properties(r)["subnetId"] != subnetID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	agentID := rgID + "/providers/Microsoft.App/agents/sre-" + ns
	expectResource(t, agentID, func(r map[string]any) error {
		action, _ := properties(r)["actionConfiguration"].(map[string]any)
		if action["mode"] != "Review" || action["identity"] != identityID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, agentID+"/connectors/logs", func(r map[string]any) error {
		if p := properties(r); p["dataSource"] != "https://logs.kusto.windows.net;Database=ops" || p["dataConnectorType"] != "Kusto" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again.
	if err := change(t, agent, func(agent *app.Agent) {
		agent.Spec.Properties.ActionConfiguration.Mode = ptr(app.AgentMode_ReadOnly)
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, agentID, func(r map[string]any) error {
		if action, _ := properties(r)["actionConfiguration"].(map[string]any); action["mode"] != "ReadOnly" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A subnet dropped while egress stays AzureVNet is refused; so is a
	// sandbox group's move to another environment.
	if err := change(t, agent, func(agent *app.Agent) {
		agent.Spec.Properties.VnetConfiguration = nil
	}); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "AzureVNet egress without a subnet refused", notReadyWith(agent, "configured together"))
	if err := change(t, group, func(group *app.SandboxGroup) {
		group.Spec.Properties.EnvironmentReference = &genruntime.ResourceReference{ARMID: otherEnvID}
	}); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "a move to another environment refused", notReadyWith(group, "cannot be changed or removed"))

	for _, o := range []client.Object{connector, agent, conn, group} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, id := range []string{agentID, groupID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in the fake", id)
		}
	}
}
