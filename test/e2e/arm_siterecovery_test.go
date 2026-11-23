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

	rs "github.com/highfivelabs/aso-extensions/apis/arm/recoveryservices/v20260701"
)

// Azure Site Recovery, Azure to Azure (docs/plan.md f52), on our Vault: an
// Azure fabric in each region, an A2A protection container in each, a
// replication policy, a mapping from the source container to the target
// with that policy (both by object), a VM by ARM ID replicated to a
// recovery resource group, a recovery plan naming the fabrics and the
// replicated item by object, and the vault's replication alert settings.
// A second fabric for a region and an item whose container has no mapping
// to its recovery container are refused (the fake's rules,
// siterecovery.go); a change of the policy is PUT again; deleted, the
// containers and alert settings (no DELETE) are detached and the rest goes.
func TestSiteRecovery(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "asr")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	vmID := rgID + "/providers/Microsoft.Compute/virtualMachines/app"
	targetRG := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-dr"
	for _, id := range []string{rgID, vmID, targetRG} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	detach := map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}
	ref := func(kind, name string) *genruntime.ResourceReference {
		return &genruntime.ResourceReference{Group: "recoveryservices.azure.hfl.dev", Kind: kind, Name: name}
	}
	on := func(name string) *genruntime.KnownResourceReference {
		return &genruntime.KnownResourceReference{Name: name}
	}

	vault := &rs.Vault{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "vault"},
		Spec: rs.Vault_Spec{
			AzureName: "dr", Owner: on("rg"), Location: ptr("eastus"),
			Sku: &rs.Sku{Name: ptr(rs.SkuName_RS0), Tier: ptr("Standard")},
		},
	}
	fabric := func(name, location string) *rs.VaultsReplicationFabric {
		return &rs.VaultsReplicationFabric{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec: rs.VaultsReplicationFabric_Spec{Owner: on("vault"), Properties: &rs.FabricCreationInputProperties{
				CustomDetails: &rs.FabricSpecificCreationInput{Azure: &rs.AzureFabricCreationInput{
					InstanceType: ptr(rs.AzureFabricCreationInput_InstanceType_Azure), Location: ptr(location),
				}},
			}},
		}
	}
	west, east := fabric("west", "westus"), fabric("east", "eastus")
	container := func(name, fabric string) *rs.VaultsReplicationProtectionContainer {
		return &rs.VaultsReplicationProtectionContainer{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Annotations: detach},
			Spec: rs.VaultsReplicationProtectionContainer_Spec{Owner: on(fabric), Properties: &rs.CreateProtectionContainerInputProperties{
				ProviderSpecificInput: []rs.ReplicationProviderSpecificContainerCreationInput{{
					A2A: &rs.A2AContainerCreationInput{InstanceType: ptr(rs.A2AContainerCreationInput_InstanceType_A2A)},
				}},
			}},
		}
	}
	src, dst := container("src", "west"), container("dst", "east")
	policy := &rs.VaultsReplicationPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "a2a"},
		Spec: rs.VaultsReplicationPolicy_Spec{Owner: on("vault"), Properties: &rs.CreatePolicyInputProperties{
			ProviderSpecificInput: &rs.PolicyProviderSpecificInput{A2A: &rs.A2APolicyCreationInput{
				InstanceType: ptr(rs.A2APolicyCreationInput_InstanceType_A2A), RecoveryPointHistory: ptr(1440),
				AppConsistentFrequencyInMinutes: ptr(240), MultiVmSyncStatus: ptr(rs.SetMultiVmSyncStatus_Enable),
			}},
		}},
	}
	mapping := &rs.VaultsReplicationProtectionContainerMapping{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "west-to-east"},
		Spec: rs.VaultsReplicationProtectionContainerMapping_Spec{Owner: on("src"), Properties: &rs.CreateProtectionContainerMappingInputProperties{
			PolicyReference:                    ref("VaultsReplicationPolicy", "a2a"),
			TargetProtectionContainerReference: ref("VaultsReplicationProtectionContainer", "dst"),
			ProviderSpecificInput: &rs.ReplicationProviderSpecificContainerMappingInput{A2A: &rs.A2AContainerMappingInput{
				InstanceType: ptr(rs.A2AContainerMappingInput_InstanceType_A2A), AgentAutoUpdateStatus: ptr(rs.AgentAutoUpdateStatus_Disabled),
			}},
		}},
	}
	enable := func(policy, target string) *rs.EnableProtectionInputProperties {
		return &rs.EnableProtectionInputProperties{
			PolicyReference: ref("VaultsReplicationPolicy", policy),
			ProviderSpecificDetails: &rs.EnableProtectionProviderSpecificInput{A2A: &rs.A2AEnableProtectionInput{
				InstanceType:                   ptr(rs.A2AEnableProtectionInput_InstanceType_A2A),
				FabricObjectReference:          &genruntime.ResourceReference{ARMID: vmID},
				RecoveryContainerReference:     ref("VaultsReplicationProtectionContainer", target),
				RecoveryResourceGroupReference: &genruntime.ResourceReference{ARMID: targetRG},
				RecoveryAvailabilityZone:       ptr("1"),
			}},
		}
	}
	item := &rs.VaultsReplicationProtectedItem{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "app"},
		Spec:       rs.VaultsReplicationProtectedItem_Spec{Owner: on("src"), Properties: enable("a2a", "dst")},
	}
	plan := &rs.VaultsReplicationRecoveryPlan{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "app-plan"},
		Spec: rs.VaultsReplicationRecoveryPlan_Spec{Owner: on("vault"), Properties: &rs.CreateRecoveryPlanInputProperties{
			PrimaryFabricReference: ref("VaultsReplicationFabric", "west"), RecoveryFabricReference: ref("VaultsReplicationFabric", "east"),
			FailoverDeploymentModel: ptr(rs.FailoverDeploymentModel_ResourceManager),
			Groups: []rs.RecoveryPlanGroup{{
				GroupType:                 ptr(rs.RecoveryPlanGroupType_Boot),
				ReplicationProtectedItems: []rs.RecoveryPlanProtectedItem{{Reference: ref("VaultsReplicationProtectedItem", "app")}},
			}},
		}},
	}
	alerts := &rs.VaultsReplicationAlertSetting{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "alerts", Annotations: detach},
		Spec: rs.VaultsReplicationAlertSetting_Spec{AzureName: "defaultAlertSetting", Owner: on("vault"), Properties: &rs.ConfigureAlertRequestProperties{
			SendToOwners: ptr("DoNotSend"), CustomEmailAddresses: []string{"dr@example.com"}, Locale: ptr("en-US"),
		}},
	}
	// The replicated item needs the container mapping, and the plan the
	// item: each is made once what it needs is Ready, so neither waits on
	// ASO's retry backoff after a refusal (too slow under the full suite).
	for _, phase := range [][]conditioned{{vault, west, east, src, dst, policy, mapping, alerts}, {item}, {plan}} {
		for _, o := range phase {
			if err := k8s.Create(ctx, o); err != nil {
				t.Fatal(err)
			}
		}
		for _, o := range phase {
			eventually(t, timeout, o.GetName()+" Ready", ready(o))
		}
	}

	vaultID := rgID + "/providers/Microsoft.RecoveryServices/vaults/dr"
	westID, eastID := vaultID+"/replicationFabrics/west", vaultID+"/replicationFabrics/east"
	srcID, dstID := westID+"/replicationProtectionContainers/src", eastID+"/replicationProtectionContainers/dst"
	policyID := vaultID + "/replicationPolicies/a2a"
	itemID := srcID + "/replicationProtectedItems/app"
	expectResource(t, srcID+"/replicationProtectionContainerMappings/west-to-east", func(r map[string]any) error {
		p := properties(r)
		if p["policyId"] != policyID || p["targetProtectionContainerId"] != dstID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, itemID, func(r map[string]any) error {
		p := properties(r)
		d, _ := p["providerSpecificDetails"].(map[string]any)
		if p["policyId"] != policyID || d["instanceType"] != "A2A" || d["fabricObjectId"] != vmID || d["recoveryContainerId"] != dstID || d["recoveryResourceGroupId"] != targetRG {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, vaultID+"/replicationRecoveryPlans/app-plan", func(r map[string]any) error {
		p := properties(r)
		groups, _ := p["groups"].([]any)
		if p["primaryFabricId"] != westID || p["recoveryFabricId"] != eastID || len(groups) != 1 || !contains(fmt.Sprint(groups[0]), itemID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, vaultID+"/replicationAlertSettings/defaultAlertSetting", func(r map[string]any) error {
		if properties(r)["sendToOwners"] != "DoNotSend" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again.
	if err := change(t, policy, func(p *rs.VaultsReplicationPolicy) {
		p.Spec.Properties.ProviderSpecificInput.A2A.RecoveryPointHistory = ptr(2880)
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, policyID, func(r map[string]any) error {
		in, _ := properties(r)["providerSpecificInput"].(map[string]any)
		if in["recoveryPointHistory"] != float64(2880) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A second fabric for West US, and an item in the target container with
	// no mapping back to the source container, are refused.
	west2 := fabric("west2", "westus")
	back := &rs.VaultsReplicationProtectedItem{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "back"},
		Spec:       rs.VaultsReplicationProtectedItem_Spec{Owner: on("dst"), Properties: enable("a2a", "src")},
	}
	for _, o := range []client.Object{west2, back} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "a second West US fabric refused", notReadyWith(west2, "already has an Azure fabric"))
	eventually(t, timeout, "an item with no mapping refused", notReadyWith(back, "no mapping to the recovery container"))

	for _, o := range []client.Object{west2, back, plan, item, mapping, policy, alerts, src, dst} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, id := range []string{itemID, policyID, vaultID + "/replicationRecoveryPlans/app-plan", srcID + "/replicationProtectionContainerMappings/west-to-east"} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in the fake", id)
		}
	}
	for _, id := range []string{srcID, dstID, vaultID + "/replicationAlertSettings/defaultAlertSetting"} {
		if !armFake.HasResource(id) {
			t.Fatalf("%s was deleted; it has no DELETE and was detached", id)
		}
	}
	for _, o := range []client.Object{west, east, vault} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
}
