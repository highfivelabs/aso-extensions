// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	exemptions "github.com/highfivelabs/aso-extensions/apis/arm/authorization/v20260101preview"
	policy "github.com/highfivelabs/aso-extensions/apis/arm/authorization/v20260701"
	management "github.com/highfivelabs/aso-extensions/apis/arm/management/v20230401"
)

const authzPrefix = "/providers/Microsoft.Authorization/"

func rawJSON(s string) *apiextensionsv1.JSON { return &apiextensionsv1.JSON{Raw: []byte(s)} }

// Azure Policy at the three scopes a landing zone uses: a definition kept at a
// management group and one at the subscription; a set definition at a child
// group naming the first; assignments at the child group (the set), at the
// subscription (placed in that group, so the root group's definition serves
// it) and at a resource group (the subscription's definition); an exemption
// at the resource group from the subscription's assignment; a change; the
// deletes. Every reference is by Kubernetes object, resolved to the ARM ID.
func TestAzurePolicy(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "policy")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	rootID, platformID := mgPrefix+ns+"-root", mgPrefix+ns+"-platform"
	byObject := func(group, kind, name string) *genruntime.ResourceReference {
		return &genruntime.ResourceReference{Group: group, Kind: kind, Name: name}
	}
	const authzGroup = "authorization.azure.hfl.dev"

	// The hierarchy: root, platform under it, the subscription in platform.
	root := &management.ManagementGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "root"},
		Spec:       management.ManagementGroup_Spec{AzureName: ns + "-root"},
	}
	platform := &management.ManagementGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "platform"},
		Spec: management.ManagementGroup_Spec{
			AzureName: ns + "-platform",
			Details: &management.CreateManagementGroupDetails{
				Parent: &management.CreateParentGroupInfo{Reference: byObject("management.azure.hfl.dev", "ManagementGroup", "root")},
			},
		},
	}
	placement := &management.ManagementGroupsSubscription{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "subscription"},
		Spec: management.ManagementGroupsSubscription_Spec{
			AzureName: subID,
			Owner:     &genruntime.KnownResourceReference{Name: "platform"},
		},
	}
	for _, o := range []conditioned{root, platform, placement} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}

	// A definition kept at the root group, with a parameter.
	requireTag := &policy.PolicyDefinition{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "require-tag"},
		Spec: policy.PolicyDefinition_Spec{
			Owner:       &genruntime.ArbitraryOwnerReference{Group: "management.azure.hfl.dev", Kind: "ManagementGroup", Name: "root"},
			PolicyType:  ptr(policy.PolicyType_Custom),
			Mode:        ptr("Indexed"),
			DisplayName: ptr("Require a tag"),
			Parameters: map[string]policy.ParameterDefinitionsValue{
				"tagName": {Type: ptr(policy.ParameterType_String)},
			},
			PolicyRule: rawJSON(`{"if":{"field":"[concat('tags[', parameters('tagName'), ']')]","exists":"false"},"then":{"effect":"deny"}}`),
			Metadata:   rawJSON(`{"category":"Tags"}`),
		},
	}
	// A definition kept at the subscription, by ARM ID.
	allowedLocations := &policy.PolicyDefinition{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "allowed-locations"},
		Spec: policy.PolicyDefinition_Spec{
			AzureName:  ns + "-allowed-locations",
			Owner:      &genruntime.ArbitraryOwnerReference{ARMID: "/subscriptions/" + subID},
			PolicyType: ptr(policy.PolicyType_Custom),
			Mode:       ptr("All"),
			PolicyRule: rawJSON(`{"if":{"not":{"field":"location","in":["westus"]}},"then":{"effect":"audit"}}`),
		},
	}
	for _, o := range []conditioned{requireTag, allowedLocations} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	requireTagID := rootID + authzPrefix + "policyDefinitions/require-tag"
	allowedLocationsID := "/subscriptions/" + subID + authzPrefix + "policyDefinitions/" + ns + "-allowed-locations"
	expectResource(t, requireTagID, func(r map[string]any) error {
		rule, _ := json.Marshal(properties(r)["policyRule"])
		if r["type"] != "Microsoft.Authorization/policyDefinitions" || properties(r)["mode"] != "Indexed" ||
			string(rule) != `{"if":{"exists":"false","field":"[concat('tags[', parameters('tagName'), ']')]"},"then":{"effect":"deny"}}` {
			return fmt.Errorf("definition at the root group: %v (rule %s)", r, rule)
		}
		return nil
	})
	expectResource(t, allowedLocationsID, func(r map[string]any) error {
		if properties(r)["mode"] != "All" {
			return fmt.Errorf("definition at the subscription: %v", r)
		}
		return nil
	})
	if requireTag.Status.Id == nil || *requireTag.Status.Id != requireTagID {
		t.Fatalf("status.id: %v, want %s", requireTag.Status.Id, requireTagID)
	}

	// A set at the platform group naming the root group's definition.
	baseline := &policy.PolicySetDefinition{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "baseline"},
		Spec: policy.PolicySetDefinition_Spec{
			Owner:      &genruntime.ArbitraryOwnerReference{Group: "management.azure.hfl.dev", Kind: "ManagementGroup", Name: "platform"},
			PolicyType: ptr(policy.PolicyType_Custom),
			Parameters: map[string]policy.ParameterDefinitionsValue{
				"tagName": {Type: ptr(policy.ParameterType_String), DefaultValue: rawJSON(`"cost-center"`)},
			},
			PolicyDefinitions: []policy.PolicyDefinitionReference{{
				PolicyDefinitionReference:   byObject(authzGroup, "PolicyDefinition", "require-tag"),
				PolicyDefinitionReferenceId: ptr("require-tag"),
				Parameters:                  map[string]policy.ParameterValuesValue{"tagName": {Value: rawJSON(`"[parameters('tagName')]"`)}},
			}},
		},
	}
	if err := k8s.Create(ctx, baseline); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "PolicySetDefinition Ready", ready(baseline))
	baselineID := platformID + authzPrefix + "policySetDefinitions/baseline"
	expectResource(t, baselineID, func(r map[string]any) error {
		defs, _ := properties(r)["policyDefinitions"].([]any)
		if len(defs) != 1 || defs[0].(map[string]any)["policyDefinitionId"] != requireTagID {
			return fmt.Errorf("set definition: %v", r)
		}
		return nil
	})

	// Assignments at the group, the subscription and the resource group.
	atGroup := &policy.PolicyAssignment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "baseline-platform"},
		Spec: policy.PolicyAssignment_Spec{
			Owner:                     &genruntime.ArbitraryOwnerReference{Group: "management.azure.hfl.dev", Kind: "ManagementGroup", Name: "platform"},
			PolicyDefinitionReference: byObject(authzGroup, "PolicySetDefinition", "baseline"),
			Parameters:                map[string]policy.ParameterValuesValue{"tagName": {Value: rawJSON(`"owner"`)}},
			NonComplianceMessages:     []policy.NonComplianceMessage{{Message: ptr("Every resource needs an owner tag.")}},
		},
	}
	atSubscription := &policy.PolicyAssignment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "require-tag-sub"},
		Spec: policy.PolicyAssignment_Spec{
			AzureName:                 ns + "-require-tag",
			Owner:                     &genruntime.ArbitraryOwnerReference{ARMID: "/subscriptions/" + subID},
			PolicyDefinitionReference: byObject(authzGroup, "PolicyDefinition", "require-tag"),
			Parameters:                map[string]policy.ParameterValuesValue{"tagName": {Value: rawJSON(`"env"`)}},
		},
	}
	atResourceGroup := &policy.PolicyAssignment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "locations-rg"},
		Spec: policy.PolicyAssignment_Spec{
			Owner:                     &genruntime.ArbitraryOwnerReference{Group: "resources.azure.com", Kind: "ResourceGroup", Name: "rg"},
			PolicyDefinitionReference: byObject(authzGroup, "PolicyDefinition", "allowed-locations"),
			EnforcementMode:           ptr(policy.PolicyAssignmentProperties_EnforcementMode_DoNotEnforce),
			NotScopes:                 []string{rgID + "/providers/Microsoft.Storage/storageAccounts/legacy"},
		},
	}
	for _, o := range []conditioned{atGroup, atSubscription, atResourceGroup} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	atGroupID := platformID + authzPrefix + "policyAssignments/baseline-platform"
	atSubscriptionID := "/subscriptions/" + subID + authzPrefix + "policyAssignments/" + ns + "-require-tag"
	atResourceGroupID := rgID + authzPrefix + "policyAssignments/locations-rg"
	wantAssignment := func(id, definition string, more func(map[string]any) error) {
		t.Helper()
		expectResource(t, id, func(r map[string]any) error {
			if r["type"] != "Microsoft.Authorization/policyAssignments" || properties(r)["policyDefinitionId"] != definition {
				return fmt.Errorf("assignment %s: %v", id, r)
			}
			return more(properties(r))
		})
	}
	wantAssignment(atGroupID, baselineID, func(p map[string]any) error {
		params, _ := p["parameters"].(map[string]any)
		tag, _ := params["tagName"].(map[string]any)
		if tag["value"] != "owner" {
			return fmt.Errorf("parameters: %v", p["parameters"])
		}
		return nil
	})
	wantAssignment(atSubscriptionID, requireTagID, func(map[string]any) error { return nil })
	wantAssignment(atResourceGroupID, allowedLocationsID, func(p map[string]any) error {
		if p["enforcementMode"] != "DoNotEnforce" {
			return fmt.Errorf("enforcementMode: %v", p["enforcementMode"])
		}
		return nil
	})

	// An exemption at the resource group from the subscription's assignment.
	waiver := &exemptions.PolicyExemption{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "legacy-waiver"},
		Spec: exemptions.PolicyExemption_Spec{
			Owner:                     &genruntime.ArbitraryOwnerReference{Group: "resources.azure.com", Kind: "ResourceGroup", Name: "rg"},
			PolicyAssignmentReference: byObject(authzGroup, "PolicyAssignment", "require-tag-sub"),
			ExemptionCategory:         ptr(exemptions.ExemptionCategory_Waiver),
			ExpiresOn:                 ptr("2027-01-01T00:00:00Z"),
			Description:               ptr("legacy resources, until they move"),
		},
	}
	if err := k8s.Create(ctx, waiver); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "PolicyExemption Ready", ready(waiver))
	waiverID := rgID + authzPrefix + "policyExemptions/legacy-waiver"
	expectResource(t, waiverID, func(r map[string]any) error {
		p := properties(r)
		if r["type"] != "Microsoft.Authorization/policyExemptions" || p["policyAssignmentId"] != atSubscriptionID || p["exemptionCategory"] != "Waiver" {
			return fmt.Errorf("exemption: %v", r)
		}
		return nil
	})

	// A change is PUT again.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(atResourceGroup), atResourceGroup); err != nil {
		t.Fatal(err)
	}
	atResourceGroup.Spec.EnforcementMode = ptr(policy.PolicyAssignmentProperties_EnforcementMode_Default)
	if err := k8s.Update(ctx, atResourceGroup); err != nil {
		t.Fatal(err)
	}
	wantAssignment(atResourceGroupID, allowedLocationsID, func(p map[string]any) error {
		if p["enforcementMode"] != "Default" {
			return fmt.Errorf("enforcementMode: %v", p["enforcementMode"])
		}
		return nil
	})

	// Deletes, the users of a definition first.
	for _, o := range []client.Object{waiver, atGroup, atSubscription, atResourceGroup, baseline, requireTag, allowedLocations, placement, platform, root} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%s gone", o.GetName()), gone(o))
	}
	for _, id := range []string{waiverID, atGroupID, atSubscriptionID, atResourceGroupID, baselineID, requireTagID, allowedLocationsID, rootID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
}

// What Resource Manager refuses reaches the Ready condition: a definition at
// a resource group (definitions are kept at a group or a subscription), and an
// assignment at a group of a definition kept at the subscription (a
// definition serves only scopes at or under where it is kept).
func TestAzurePolicyRefused(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "policy-refused")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")

	atResourceGroup := &policy.PolicyDefinition{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "at-rg"},
		Spec: policy.PolicyDefinition_Spec{
			Owner:      &genruntime.ArbitraryOwnerReference{Group: "resources.azure.com", Kind: "ResourceGroup", Name: "rg"},
			PolicyRule: rawJSON(`{"if":{"field":"type","equals":"x"},"then":{"effect":"audit"}}`),
		},
	}
	if err := k8s.Create(ctx, atResourceGroup); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "a definition at a resource group refused", notReadyWith(atResourceGroup, "management group or a subscription"))
	if armFake.HasResource(rgID + authzPrefix + "policyDefinitions/at-rg") {
		t.Fatal("a definition was kept at a resource group")
	}

	group := &management.ManagementGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "group"},
		Spec:       management.ManagementGroup_Spec{AzureName: ns + "-group"},
	}
	subDefinition := &policy.PolicyDefinition{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "sub-definition"},
		Spec: policy.PolicyDefinition_Spec{
			AzureName:  ns + "-sub-definition",
			Owner:      &genruntime.ArbitraryOwnerReference{ARMID: "/subscriptions/" + subID},
			PolicyRule: rawJSON(`{"if":{"field":"type","equals":"x"},"then":{"effect":"audit"}}`),
		},
	}
	for _, o := range []conditioned{group, subDefinition} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	above := &policy.PolicyAssignment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "above"},
		Spec: policy.PolicyAssignment_Spec{
			Owner:                     &genruntime.ArbitraryOwnerReference{Group: "management.azure.hfl.dev", Kind: "ManagementGroup", Name: "group"},
			PolicyDefinitionReference: &genruntime.ResourceReference{Group: "authorization.azure.hfl.dev", Kind: "PolicyDefinition", Name: "sub-definition"},
		},
	}
	if err := k8s.Create(ctx, above); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "an assignment above its definition refused", notReadyWith(above, "not at or above it"))
	for _, o := range []client.Object{above, atResourceGroup, subDefinition, group} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%s gone", o.GetName()), gone(o))
	}
}
