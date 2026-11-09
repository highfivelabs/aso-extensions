// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	policy "github.com/highfivelabs/aso-extensions/apis/arm/authorization/v20260701"
	management "github.com/highfivelabs/aso-extensions/apis/arm/management/v20230401"
	insights "github.com/highfivelabs/aso-extensions/apis/arm/policyinsights/v20241001"
)

const insightsPrefix = "/providers/Microsoft.PolicyInsights/"

// Remediations at a management group, a subscription and a resource group,
// and an attestation on a resource: each names a PolicyAssignment by
// Kubernetes object (resolved to its ARM ID), and a remediation of a set
// assignment says which definition of the set it remediates. Then a change
// and the deletes.
func TestPolicyRemediationAndAttestation(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "insights")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	subscription := "/subscriptions/" + subID
	rgID := subscription + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	accountID := rgID + "/providers/Microsoft.Storage/storageAccounts/" + ns + "logs"
	armFake.AddResource(accountID)
	groupID := mgPrefix + ns + "-platform"
	byObject := func(group, kind, name string) *genruntime.ResourceReference {
		return &genruntime.ResourceReference{Group: group, Kind: kind, Name: name}
	}
	const authzGroup = "authorization.azure.hfl.dev"
	toGroup := &genruntime.ArbitraryOwnerReference{Group: "management.azure.hfl.dev", Kind: "ManagementGroup", Name: "platform"}
	toRG := &genruntime.ArbitraryOwnerReference{Group: "resources.azure.com", Kind: "ResourceGroup", Name: "rg"}

	// A group, a definition and a set kept at it, and three assignments.
	platform := &management.ManagementGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "platform"},
		Spec:       management.ManagementGroup_Spec{AzureName: ns + "-platform"},
	}
	diagnostics := &policy.PolicyDefinition{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "diagnostics"},
		Spec: policy.PolicyDefinition_Spec{
			Owner:      toGroup,
			PolicyType: ptr(policy.PolicyType_Custom),
			Mode:       ptr("Indexed"),
			PolicyRule: rawJSON(`{"if":{"field":"type","equals":"Microsoft.Storage/storageAccounts"},"then":{"effect":"deployIfNotExists","details":{"type":"Microsoft.Insights/diagnosticSettings"}}}`),
		},
	}
	baseline := &policy.PolicySetDefinition{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "baseline"},
		Spec: policy.PolicySetDefinition_Spec{
			Owner:      toGroup,
			PolicyType: ptr(policy.PolicyType_Custom),
			PolicyDefinitions: []policy.PolicyDefinitionReference{{
				PolicyDefinitionReference:   byObject(authzGroup, "PolicyDefinition", "diagnostics"),
				PolicyDefinitionReferenceId: ptr("diagnostics"),
			}},
		},
	}
	atGroup := &policy.PolicyAssignment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "diagnostics-platform"},
		Spec: policy.PolicyAssignment_Spec{
			Owner:                     toGroup,
			PolicyDefinitionReference: byObject(authzGroup, "PolicyDefinition", "diagnostics"),
		},
	}
	atSubscription := &policy.PolicyAssignment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "baseline-sub"},
		Spec: policy.PolicyAssignment_Spec{
			AzureName:                 ns + "-baseline",
			Owner:                     &genruntime.ArbitraryOwnerReference{ARMID: subscription},
			PolicyDefinitionReference: byObject(authzGroup, "PolicySetDefinition", "baseline"),
		},
	}
	atRG := &policy.PolicyAssignment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "diagnostics-rg"},
		Spec: policy.PolicyAssignment_Spec{
			Owner:                     toRG,
			PolicyDefinitionReference: byObject(authzGroup, "PolicyDefinition", "diagnostics"),
		},
	}
	// The subscription is placed in the group, so the group's definitions
	// serve it.
	placement := &management.ManagementGroupsSubscription{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "subscription"},
		Spec: management.ManagementGroupsSubscription_Spec{
			AzureName: subID,
			Owner:     &genruntime.KnownResourceReference{Name: "platform"},
		},
	}
	for _, o := range []conditioned{platform, placement, diagnostics, baseline, atGroup, atSubscription, atRG} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	atGroupID := groupID + "/providers/Microsoft.Authorization/policyAssignments/diagnostics-platform"
	atSubscriptionID := subscription + "/providers/Microsoft.Authorization/policyAssignments/" + ns + "-baseline"
	atRGID := rgID + "/providers/Microsoft.Authorization/policyAssignments/diagnostics-rg"

	// Remediations at the three scopes.
	groupWide := &insights.PolicyRemediation{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "diagnostics-platform"},
		Spec: insights.PolicyRemediation_Spec{
			Owner:                     toGroup,
			PolicyAssignmentReference: byObject(authzGroup, "PolicyAssignment", "diagnostics-platform"),
		},
	}
	ofSet := &insights.PolicyRemediation{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "baseline-sub"},
		Spec: insights.PolicyRemediation_Spec{
			AzureName:                   ns + "-baseline",
			Owner:                       &genruntime.ArbitraryOwnerReference{ARMID: subscription},
			PolicyAssignmentReference:   byObject(authzGroup, "PolicyAssignment", "baseline-sub"),
			PolicyDefinitionReferenceId: ptr("diagnostics"),
			ResourceDiscoveryMode:       ptr(insights.ResourceDiscoveryMode_ReEvaluateCompliance),
		},
	}
	inRG := &insights.PolicyRemediation{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "diagnostics-rg"},
		Spec: insights.PolicyRemediation_Spec{
			Owner:                     toRG,
			PolicyAssignmentReference: byObject(authzGroup, "PolicyAssignment", "diagnostics-rg"),
			ResourceDiscoveryMode:     ptr(insights.ResourceDiscoveryMode_ExistingNonCompliant),
			Filters: &insights.RemediationFilters{
				Locations:          []string{"westus"},
				ResourceReferences: []genruntime.ResourceReference{{ARMID: accountID}},
			},
			FailureThreshold:    &insights.RemediationPropertiesFailureThreshold{Percentage: ptr(0.1)},
			ParallelDeployments: ptr(5),
			ResourceCount:       ptr(100),
		},
	}
	for _, o := range []conditioned{groupWide, ofSet, inRG} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, "PolicyRemediation "+o.GetName()+" Ready", ready(o))
	}
	groupWideID := groupID + insightsPrefix + "remediations/diagnostics-platform"
	ofSetID := subscription + insightsPrefix + "remediations/" + ns + "-baseline"
	inRGID := rgID + insightsPrefix + "remediations/diagnostics-rg"
	wantRemediation := func(id, assignment string, more func(map[string]any) error) {
		t.Helper()
		expectResource(t, id, func(r map[string]any) error {
			if r["type"] != "Microsoft.PolicyInsights/remediations" || properties(r)["policyAssignmentId"] != assignment {
				return fmt.Errorf("remediation %s: %v", id, r)
			}
			return more(properties(r))
		})
	}
	wantRemediation(groupWideID, atGroupID, func(map[string]any) error { return nil })
	wantRemediation(ofSetID, atSubscriptionID, func(p map[string]any) error {
		if p["policyDefinitionReferenceId"] != "diagnostics" || p["resourceDiscoveryMode"] != "ReEvaluateCompliance" {
			return fmt.Errorf("remediation of the set: %v", p)
		}
		return nil
	})
	wantRemediation(inRGID, atRGID, func(p map[string]any) error {
		filters, _ := p["filters"].(map[string]any)
		ids, _ := filters["resourceIds"].([]any)
		locations, _ := filters["locations"].([]any)
		threshold, _ := p["failureThreshold"].(map[string]any)
		if len(ids) != 1 || ids[0] != accountID || len(locations) != 1 || locations[0] != "westus" ||
			threshold["percentage"] != 0.1 || p["parallelDeployments"] != float64(5) || p["resourceCount"] != float64(100) {
			return fmt.Errorf("remediation in the resource group: %v", p)
		}
		return nil
	})
	if inRG.Status.ProvisioningState == nil || *inRG.Status.ProvisioningState != "Succeeded" {
		t.Fatalf("status.provisioningState: %v", inRG.Status.ProvisioningState)
	}

	// An attestation on the storage account (an ARM ID owner), under the
	// resource group's assignment.
	attestation := &insights.PolicyAttestation{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "logs-diagnostics"},
		Spec: insights.PolicyAttestation_Spec{
			Owner:                     &genruntime.ArbitraryOwnerReference{ARMID: accountID},
			PolicyAssignmentReference: byObject(authzGroup, "PolicyAssignment", "diagnostics-rg"),
			ComplianceState:           ptr(insights.ComplianceState_Compliant),
			Comments:                  ptr("diagnostics go to the central workspace by another route"),
			ExpiresOn:                 ptr("2027-06-30T00:00:00Z"),
			PropertiesOwner:           ptr("00000000-0000-0000-0000-0000000000aa"),
			Evidence:                  []insights.AttestationEvidence{{Description: ptr("the runbook"), SourceUri: ptr("https://example.com/runbook")}},
			Metadata:                  rawJSON(`{"ticket":"SEC-12"}`),
		},
	}
	if err := k8s.Create(ctx, attestation); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "PolicyAttestation Ready", ready(attestation))
	attestationID := accountID + insightsPrefix + "attestations/logs-diagnostics"
	wantAttestation := func(state string) {
		t.Helper()
		expectResource(t, attestationID, func(r map[string]any) error {
			p := properties(r)
			evidence, _ := p["evidence"].([]any)
			metadata, _ := p["metadata"].(map[string]any)
			if r["type"] != "Microsoft.PolicyInsights/attestations" || p["policyAssignmentId"] != atRGID || p["complianceState"] != state ||
				p["owner"] != "00000000-0000-0000-0000-0000000000aa" || len(evidence) != 1 || metadata["ticket"] != "SEC-12" {
				return fmt.Errorf("attestation: %v", r)
			}
			return nil
		})
	}
	wantAttestation("Compliant")

	// A change is PUT again.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(attestation), attestation); err != nil {
		t.Fatal(err)
	}
	attestation.Spec.ComplianceState = ptr(insights.ComplianceState_NonCompliant)
	if err := k8s.Update(ctx, attestation); err != nil {
		t.Fatal(err)
	}
	wantAttestation("NonCompliant")

	// Deletes, the remediations and the attestation before what they name.
	for _, o := range []client.Object{attestation, groupWide, ofSet, inRG, atGroup, atSubscription, atRG, baseline, diagnostics, placement, platform} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%s gone", o.GetName()), gone(o))
	}
	for _, id := range []string{attestationID, groupWideID, ofSetID, inRGID, atGroupID, atSubscriptionID, atRGID, groupID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
}

// What Resource Manager refuses reaches the Ready condition: a remediation
// above the scope of its assignment, a remediation of a set assignment that
// does not say which definition, and an attestation at a management group.
func TestPolicyRemediationRefused(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "insights-refused")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	subscription := "/subscriptions/" + subID
	rgID := subscription + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	byObject := func(group, kind, name string) *genruntime.ResourceReference {
		return &genruntime.ResourceReference{Group: group, Kind: kind, Name: name}
	}
	const authzGroup = "authorization.azure.hfl.dev"
	toGroup := &genruntime.ArbitraryOwnerReference{Group: "management.azure.hfl.dev", Kind: "ManagementGroup", Name: "group"}

	group := &management.ManagementGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "group"},
		Spec:       management.ManagementGroup_Spec{AzureName: ns + "-group"},
	}
	definition := &policy.PolicyDefinition{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "definition"},
		Spec: policy.PolicyDefinition_Spec{
			AzureName:  ns + "-definition",
			Owner:      &genruntime.ArbitraryOwnerReference{ARMID: subscription},
			PolicyRule: rawJSON(`{"if":{"field":"type","equals":"x"},"then":{"effect":"modify"}}`),
		},
	}
	set := &policy.PolicySetDefinition{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "set"},
		Spec: policy.PolicySetDefinition_Spec{
			AzureName: ns + "-set",
			Owner:     &genruntime.ArbitraryOwnerReference{ARMID: subscription},
			PolicyDefinitions: []policy.PolicyDefinitionReference{{
				PolicyDefinitionReference:   byObject(authzGroup, "PolicyDefinition", "definition"),
				PolicyDefinitionReferenceId: ptr("definition"),
			}},
		},
	}
	atRG := &policy.PolicyAssignment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "at-rg"},
		Spec: policy.PolicyAssignment_Spec{
			Owner:                     &genruntime.ArbitraryOwnerReference{Group: "resources.azure.com", Kind: "ResourceGroup", Name: "rg"},
			PolicyDefinitionReference: byObject(authzGroup, "PolicyDefinition", "definition"),
		},
	}
	ofSet := &policy.PolicyAssignment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "of-set"},
		Spec: policy.PolicyAssignment_Spec{
			AzureName:                 ns + "-of-set",
			Owner:                     &genruntime.ArbitraryOwnerReference{ARMID: subscription},
			PolicyDefinitionReference: byObject(authzGroup, "PolicySetDefinition", "set"),
		},
	}
	for _, o := range []conditioned{group, definition, set, atRG, ofSet} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}

	above := &insights.PolicyRemediation{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "above"},
		Spec: insights.PolicyRemediation_Spec{
			AzureName:                 ns + "-above",
			Owner:                     &genruntime.ArbitraryOwnerReference{ARMID: subscription},
			PolicyAssignmentReference: byObject(authzGroup, "PolicyAssignment", "at-rg"),
		},
	}
	whichDefinition := &insights.PolicyRemediation{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "which-definition"},
		Spec: insights.PolicyRemediation_Spec{
			AzureName:                 ns + "-which-definition",
			Owner:                     &genruntime.ArbitraryOwnerReference{ARMID: subscription},
			PolicyAssignmentReference: byObject(authzGroup, "PolicyAssignment", "of-set"),
		},
	}
	atGroup := &insights.PolicyAttestation{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "at-group"},
		Spec: insights.PolicyAttestation_Spec{
			Owner:                     toGroup,
			PolicyAssignmentReference: byObject(authzGroup, "PolicyAssignment", "at-rg"),
			ComplianceState:           ptr(insights.ComplianceState_Compliant),
		},
	}
	for _, c := range []struct {
		o    conditioned
		text string
	}{
		{above, "not at or under the scope of the assignment"},
		{whichDefinition, "policyDefinitionReferenceId"},
		{atGroup, "a subscription, a resource group or a resource"},
	} {
		if err := k8s.Create(ctx, c.o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, c.o.GetName()+" refused", notReadyWith(c.o, c.text))
	}
	if armFake.HasResource(subscription + insightsPrefix + "remediations/" + ns + "-above") {
		t.Fatal("a remediation was kept above its assignment")
	}
	for _, o := range []client.Object{above, whichDefinition, atGroup, ofSet, atRG, set, definition, group} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%s gone", o.GetName()), gone(o))
	}
}
