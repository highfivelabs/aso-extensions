// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	insights "github.com/highfivelabs/aso-extensions/apis/arm/insights/v20210901"
	network "github.com/highfivelabs/aso-extensions/apis/arm/network/v20250501"
)

// An Azure Monitor private link scope owned by the resource group (a global
// resource), a workspace scoped into it (owned by our scope), changed and
// deleted child first.
func TestMonitorPrivateLinkScope(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "monitor-pls")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	workspaceID, rgID := logAnalytics(t, ns)

	scope := &insights.PrivateLinkScope{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "ampls"},
		Spec: insights.PrivateLinkScope_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("global"),
			AccessModeSettings: &insights.AccessModeSettings{
				QueryAccessMode:     ptr(insights.AccessMode_PrivateOnly),
				IngestionAccessMode: ptr(insights.AccessMode_PrivateOnly),
			},
		},
	}
	if err := k8s.Create(ctx, scope); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "PrivateLinkScope Ready", ready(scope))
	scopeID := rgID + "/providers/Microsoft.Insights/privateLinkScopes/ampls"
	expectResource(t, scopeID, func(r map[string]any) error {
		modes, _ := properties(r)["accessModeSettings"].(map[string]any)
		if r["location"] != "global" || modes["queryAccessMode"] != "PrivateOnly" || modes["ingestionAccessMode"] != "PrivateOnly" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	scoped := &insights.PrivateLinkScopesScopedResource{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "law"},
		Spec: insights.PrivateLinkScopesScopedResource_Spec{
			AzureName:               "law",
			Owner:                   &genruntime.KnownResourceReference{Name: "ampls"},
			LinkedResourceReference: &genruntime.ResourceReference{ARMID: workspaceID},
		},
	}
	if err := k8s.Create(ctx, scoped); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "PrivateLinkScopesScopedResource Ready", ready(scoped))
	scopedID := scopeID + "/scopedResources/law"
	expectResource(t, scopedID, func(r map[string]any) error {
		if properties(r)["linkedResourceId"] != workspaceID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	if err := k8s.Get(ctx, client.ObjectKeyFromObject(scope), scope); err != nil {
		t.Fatal(err)
	}
	scope.Spec.AccessModeSettings.QueryAccessMode = ptr(insights.AccessMode_Open)
	if err := k8s.Update(ctx, scope); err != nil {
		t.Fatal(err)
	}
	expectResource(t, scopeID, func(r map[string]any) error {
		modes, _ := properties(r)["accessModeSettings"].(map[string]any)
		if modes["queryAccessMode"] != "Open" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range []client.Object{scoped, scope} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T gone", o), gone(o))
	}
	if armFake.HasResource(scopeID) || armFake.HasResource(scopedID) || !armFake.HasResource(workspaceID) {
		t.Fatalf("scope %v scoped resource %v workspace %v", armFake.HasResource(scopeID), armFake.HasResource(scopedID), armFake.HasResource(workspaceID))
	}
}

// A network security perimeter with a profile, an access rule and an
// association of a storage account with the profile: owners are our kinds all
// the way down (the association is owned by the perimeter, the rule by the
// profile).
func TestNetworkSecurityPerimeter(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "nsp")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	_, rgID := logAnalytics(t, ns)
	storageID := rgID + "/providers/Microsoft.Storage/storageAccounts/nspstore"
	armFake.AddResource(storageID)

	nsp := &network.NetworkSecurityPerimeter{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "perimeter"},
		Spec: network.NetworkSecurityPerimeter_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
		},
	}
	if err := k8s.Create(ctx, nsp); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "NetworkSecurityPerimeter Ready", ready(nsp))
	nspID := rgID + "/providers/Microsoft.Network/networkSecurityPerimeters/perimeter"
	profileID := nspID + "/profiles/default"
	ruleID := profileID + "/accessRules/office"
	assocID := nspID + "/resourceAssociations/store"

	profile := &network.NetworkSecurityPerimetersProfile{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "default"},
		Spec:       network.NetworkSecurityPerimetersProfile_Spec{Owner: &genruntime.KnownResourceReference{Name: "perimeter"}},
	}
	if err := k8s.Create(ctx, profile); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "Profile Ready", ready(profile))
	rule := &network.NetworkSecurityPerimetersProfilesAccessRule{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "office"},
		Spec: network.NetworkSecurityPerimetersProfilesAccessRule_Spec{
			Owner:           &genruntime.KnownResourceReference{Name: "default"},
			Direction:       ptr(network.AccessRuleDirection("Inbound")),
			AddressPrefixes: []string{"203.0.113.0/24"},
		},
	}
	assoc := &network.NetworkSecurityPerimetersResourceAssociation{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "store"},
		Spec: network.NetworkSecurityPerimetersResourceAssociation_Spec{
			Owner:               &genruntime.KnownResourceReference{Name: "perimeter"},
			AccessMode:          ptr(network.AssociationAccessMode_Learning),
			PrivateLinkResource: &network.NetworkSecurityPerimeterSubResource{Reference: &genruntime.ResourceReference{ARMID: storageID}},
			Profile:             &network.NetworkSecurityPerimeterSubResource{Reference: &genruntime.ResourceReference{ARMID: profileID}},
		},
	}
	for _, o := range []client.Object{rule, assoc} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []client.Object{rule, assoc} {
		eventually(t, timeout, fmt.Sprintf("%T Ready", o), ready(o.(conditioned)))
	}
	expectResource(t, nspID, func(r map[string]any) error {
		if r["type"] != "Microsoft.Network/networkSecurityPerimeters" || r["location"] != "westus" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, ruleID, func(r map[string]any) error {
		p := properties(r)
		if r["type"] != "Microsoft.Network/networkSecurityPerimeters/profiles/accessRules" || p["direction"] != "Inbound" ||
			!reflect.DeepEqual(p["addressPrefixes"], []any{"203.0.113.0/24"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, assocID, func(r map[string]any) error {
		p := properties(r)
		pl, _ := p["privateLinkResource"].(map[string]any)
		pr, _ := p["profile"].(map[string]any)
		if r["type"] != "Microsoft.Network/networkSecurityPerimeters/resourceAssociations" || p["accessMode"] != "Learning" ||
			pl["id"] != storageID || pr["id"] != profileID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// Enforcing the perimeter is a change PUT again.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(assoc), assoc); err != nil {
		t.Fatal(err)
	}
	assoc.Spec.AccessMode = ptr(network.AssociationAccessMode_Enforced)
	if err := k8s.Update(ctx, assoc); err != nil {
		t.Fatal(err)
	}
	expectResource(t, assocID, func(r map[string]any) error {
		if properties(r)["accessMode"] != "Enforced" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range []client.Object{assoc, rule, profile, nsp} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T gone", o), gone(o))
	}
	for _, id := range []string{nspID, profileID, ruleID, assocID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
	if !armFake.HasResource(storageID) {
		t.Fatal("the storage account is gone from Resource Manager")
	}
}

// The CRD refuses an association access mode that does not exist and an
// access rule direction that does not exist.
func TestMonitorValidation(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "monitor-validation")
	owner := &genruntime.KnownResourceReference{Name: "perimeter"}
	badMode := &network.NetworkSecurityPerimetersResourceAssociation{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "bad"},
		Spec: network.NetworkSecurityPerimetersResourceAssociation_Spec{
			Owner: owner, AccessMode: ptr(network.AssociationAccessMode("Sometimes")),
		},
	}
	if err := k8s.Create(ctx, badMode); err == nil || !contains(err.Error(), "Unsupported value") {
		t.Fatalf("want a refusal of the access mode, got %v", err)
	}
	badDirection := &network.NetworkSecurityPerimetersProfilesAccessRule{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "bad"},
		Spec: network.NetworkSecurityPerimetersProfilesAccessRule_Spec{
			Owner: owner, Direction: ptr(network.AccessRuleDirection("Sideways")),
		},
	}
	if err := k8s.Create(ctx, badDirection); err == nil || !contains(err.Error(), "Unsupported value") {
		t.Fatalf("want a refusal of the direction, got %v", err)
	}
}
