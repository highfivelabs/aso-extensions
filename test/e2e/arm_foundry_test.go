// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	asocognitive "github.com/Azure/azure-service-operator/v2/api/cognitiveservices/v20250601/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	foundry "github.com/highfivelabs/aso-extensions/apis/arm/cognitiveservices/v20260901"
)

// Microsoft Foundry's newer children (docs/plan.md f44) on ASO's Account and
// Project stand-ins: the account's managed network (V2) with an FQDN rule
// and a private endpoint rule to a storage account by ARM ID, a RAI topic,
// a project's agent application and its managed agent deployment, and a
// subscription's RAI policy. A move back to V1 is refused (the fake's rule,
// foundry.go); a change is PUT again; all are deleted, children first.
func TestFoundryNetworkAndAgents(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "foundry")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	accountID := rgID + "/providers/Microsoft.CognitiveServices/accounts/ai-" + ns
	projectID := accountID + "/projects/agents"
	storageID := rgID + "/providers/Microsoft.Storage/storageAccounts/data" + ns[len(ns)-6:]
	for _, id := range []string{rgID, accountID, projectID, storageID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	account := &asocognitive.Account{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "ai"}}
	account.Spec.AzureName, account.Spec.Owner = "ai-"+ns, &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, account, accountID)
	project := &asocognitive.Project{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "agents"}}
	project.Spec.AzureName, project.Spec.Owner = "agents", &genruntime.KnownResourceReference{Name: "ai"}
	asoStandIn(t, project, projectID)
	onAccount := &genruntime.KnownResourceReference{Name: "ai"}

	network := &foundry.AccountsManagedNetwork{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "network"},
		Spec: foundry.AccountsManagedNetwork_Spec{
			AzureName: "default", Owner: onAccount,
			Properties: &foundry.ManagedNetworkSettingsProperties{ManagedNetwork: &foundry.ManagedNetworkSettingsEx{
				IsolationMode:      ptr(foundry.IsolationMode_AllowOnlyApprovedOutbound),
				ManagedNetworkKind: ptr(foundry.ManagedNetworkKind_V2),
			}},
		},
	}
	onNetwork := &genruntime.KnownResourceReference{Name: "network"}
	pypi := &foundry.AccountsManagedNetworksOutboundRule{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "pypi"},
		Spec: foundry.AccountsManagedNetworksOutboundRule_Spec{
			AzureName: "pypi", Owner: onNetwork,
			Properties: &foundry.OutboundRule{FQDN: &foundry.FqdnOutboundRule{
				Type: ptr(foundry.FqdnOutboundRule_Type_FQDN), Destination: ptr("pypi.org"),
			}},
		},
	}
	data := &foundry.AccountsManagedNetworksOutboundRule{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "data"},
		Spec: foundry.AccountsManagedNetworksOutboundRule_Spec{
			AzureName: "data", Owner: onNetwork,
			Properties: &foundry.OutboundRule{PrivateEndpoint: &foundry.PrivateEndpointOutboundRule{
				Type: ptr(foundry.PrivateEndpointOutboundRule_Type_PrivateEndpoint),
				Destination: &foundry.PrivateEndpointOutboundRuleDestination{
					ServiceResourceReference: &genruntime.ResourceReference{ARMID: storageID}, SubresourceTarget: ptr("blob"),
				},
			}},
		},
	}
	topic := &foundry.AccountsRaiTopic{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "competitors"},
		Spec: foundry.AccountsRaiTopic_Spec{
			AzureName: "competitors", Owner: onAccount,
			Properties: &foundry.RaiTopicProperties{
				TopicName: ptr("competitors"), Description: ptr("questions about competitors' products"),
				SampleBlobUrl: ptr("https://contosoai.blob.core.windows.net/topics/competitors.jsonl"),
			},
		},
	}
	app := &foundry.AccountsProjectsApplication{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "helpdesk"},
		Spec: foundry.AccountsProjectsApplication_Spec{
			AzureName: "helpdesk", Owner: &genruntime.KnownResourceReference{Name: "agents"},
			Properties: &foundry.AgenticApplicationProperties{
				DisplayName: ptr("Help desk"),
				Agents:      []foundry.AgentReferenceProperties{{AgentName: ptr("triage")}},
			},
		},
	}
	deployment := &foundry.AccountsProjectsApplicationsAgentDeployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "helpdesk-prod"},
		Spec: foundry.AccountsProjectsApplicationsAgentDeployment_Spec{
			AzureName: "prod", Owner: &genruntime.KnownResourceReference{Name: "helpdesk"},
			Properties: &foundry.AgentDeploymentProperties{Managed: &foundry.ManagedAgentDeployment{
				DeploymentType: ptr(foundry.ManagedAgentDeployment_DeploymentType_Managed), DisplayName: ptr("production"),
				Agents: []foundry.VersionedAgentReference{{AgentName: ptr("triage"), AgentVersion: ptr("3")}},
			}},
		},
	}
	policy := &foundry.RaiPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "strict"},
		Spec: foundry.RaiPolicy_Spec{
			AzureName: "strict-" + ns, Owner: &genruntime.ArbitraryOwnerReference{ARMID: "/subscriptions/" + subID},
			Properties: &foundry.RaiPolicyProperties{BasePolicyName: ptr("Microsoft.DefaultV2")},
		},
	}
	objs := []conditioned{network, pypi, data, topic, app, deployment, policy}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}

	networkID := accountID + "/managedNetworks/default"
	expectResource(t, networkID, func(r map[string]any) error {
		mn, _ := properties(r)["managedNetwork"].(map[string]any)
		if r["type"] != "Microsoft.CognitiveServices/accounts/managedNetworks" || mn["managedNetworkKind"] != "V2" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, networkID+"/outboundRules/data", func(r map[string]any) error {
		dest, _ := properties(r)["destination"].(map[string]any)
		if properties(r)["type"] != "PrivateEndpoint" || dest["serviceResourceId"] != storageID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, networkID+"/outboundRules/pypi", func(r map[string]any) error {
		if properties(r)["destination"] != "pypi.org" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, accountID+"/raitopics/competitors", func(r map[string]any) error {
		if properties(r)["topicName"] != "competitors" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	deploymentID := projectID + "/applications/helpdesk/agentDeployments/prod"
	expectResource(t, deploymentID, func(r map[string]any) error {
		if p := properties(r); p["deploymentType"] != "Managed" || p["displayName"] != "production" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	policyID := "/subscriptions/" + subID + "/providers/Microsoft.CognitiveServices/raiPolicy/strict-" + ns
	expectResource(t, policyID, func(r map[string]any) error {
		if properties(r)["basePolicyName"] != "Microsoft.DefaultV2" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(app), app); err != nil {
		t.Fatal(err)
	}
	app.Spec.Properties.DisplayName = ptr("IT help desk")
	if err := k8s.Update(ctx, app); err != nil {
		t.Fatal(err)
	}
	expectResource(t, projectID+"/applications/helpdesk", func(r map[string]any) error {
		if properties(r)["displayName"] != "IT help desk" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// Back to V1 is refused.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(network), network); err != nil {
		t.Fatal(err)
	}
	network.Spec.Properties.ManagedNetwork.ManagedNetworkKind = ptr(foundry.ManagedNetworkKind_V1)
	if err := k8s.Update(ctx, network); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "a move back to V1 refused", notReadyWith(network, "cannot switch back to V1"))

	for _, o := range []client.Object{deployment, app, topic, pypi, data, network, policy} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, id := range []string{deploymentID, networkID, accountID + "/raitopics/competitors", policyID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in the fake", id)
		}
	}
}
