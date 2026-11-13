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

	management "github.com/highfivelabs/aso-extensions/apis/arm/management/v20230401"
)

const mgPrefix = "/providers/Microsoft.Management/managementGroups/"

// A management hierarchy: tenant scope resources (no owner, no resource
// group; the ARM ID starts at /providers), a child group that names its parent
// by Kubernetes object, a grandchild that names it by ARM ID, the hierarchy
// settings and a subscription placed in a group (both owned by the group, a
// child of a tenant scope resource), then a change and the deletes.
func TestManagementGroups(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "mg")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	subscription := "00000000-0000-0000-0000-0000000000f2"

	platformID := mgPrefix + ns + "-platform"
	landingID := mgPrefix + ns + "-landing"
	sandboxID := mgPrefix + ns + "-sandbox"

	platform := &management.ManagementGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "platform"},
		Spec: management.ManagementGroup_Spec{
			AzureName:   ns + "-platform",
			DisplayName: ptr("Platform"),
		},
	}
	if err := k8s.Create(ctx, platform); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "ManagementGroup Ready", ready(platform))
	expectResource(t, platformID, func(r map[string]any) error {
		if r["type"] != "Microsoft.Management/managementGroups" || properties(r)["displayName"] != "Platform" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if platform.Status.Id == nil || *platform.Status.Id != platformID {
		t.Fatalf("status.id: %v, want %s", platform.Status.Id, platformID)
	}

	// A child group naming its parent by Kubernetes object.
	landing := &management.ManagementGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "landing"},
		Spec: management.ManagementGroup_Spec{
			AzureName:   ns + "-landing",
			DisplayName: ptr("Landing zones"),
			Details: &management.CreateManagementGroupDetails{
				Parent: &management.CreateParentGroupInfo{Reference: &genruntime.ResourceReference{
					Group: "management.azure.hfl.dev", Kind: "ManagementGroup", Name: "platform"}},
			},
		},
	}
	if err := k8s.Create(ctx, landing); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "child ManagementGroup Ready", ready(landing))
	expectResource(t, landingID, func(r map[string]any) error {
		details, _ := properties(r)["details"].(map[string]any)
		parent, _ := details["parent"].(map[string]any)
		if parent["id"] != platformID {
			return fmt.Errorf("parent of landing: %v", r)
		}
		return nil
	})

	// A grandchild naming its parent by ARM ID, which Azure may hold from
	// outside Kubernetes.
	sandbox := &management.ManagementGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "sandbox"},
		Spec: management.ManagementGroup_Spec{
			AzureName: ns + "-sandbox",
			Details: &management.CreateManagementGroupDetails{
				Parent: &management.CreateParentGroupInfo{Reference: &genruntime.ResourceReference{ARMID: landingID}},
			},
		},
	}
	if err := k8s.Create(ctx, sandbox); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "grandchild ManagementGroup Ready", ready(sandbox))
	expectResource(t, sandboxID, func(r map[string]any) error {
		details, _ := properties(r)["details"].(map[string]any)
		parent, _ := details["parent"].(map[string]any)
		if parent["id"] != landingID {
			return fmt.Errorf("parent of sandbox: %v", r)
		}
		return nil
	})

	// The hierarchy settings (a fixed name, default) and a subscription
	// placed in the child group: both are children of a tenant scope resource.
	settings := &management.ManagementGroupsSetting{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "settings"},
		Spec: management.ManagementGroupsSetting_Spec{
			Owner:                                &genruntime.KnownResourceReference{Name: "platform"},
			RequireAuthorizationForGroupCreation: ptr(true),
			DefaultManagementGroup:               ptr(landingID),
		},
	}
	placement := &management.ManagementGroupsSubscription{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "workload"},
		Spec: management.ManagementGroupsSubscription_Spec{
			AzureName: subscription,
			Owner:     &genruntime.KnownResourceReference{Name: "landing"},
		},
	}
	for _, o := range []client.Object{settings, placement} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "ManagementGroupsSetting Ready", ready(settings))
	eventually(t, timeout, "ManagementGroupsSubscription Ready", ready(placement))
	expectResource(t, platformID+"/settings/default", func(r map[string]any) error {
		p := properties(r)
		if p["requireAuthorizationForGroupCreation"] != true || p["defaultManagementGroup"] != landingID {
			return fmt.Errorf("settings: %v", r)
		}
		return nil
	})
	placementID := landingID + "/subscriptions/" + subscription
	expectResource(t, placementID, func(r map[string]any) error {
		if r["type"] != "Microsoft.Management/managementGroups/subscriptions" {
			return fmt.Errorf("placement: %v", r)
		}
		return nil
	})

	// A change: a new display name is a PUT.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(platform), platform); err != nil {
		t.Fatal(err)
	}
	platform.Spec.DisplayName = ptr("Platform team")
	if err := k8s.Update(ctx, platform); err != nil {
		t.Fatal(err)
	}
	expectResource(t, platformID, func(r map[string]any) error {
		if properties(r)["displayName"] != "Platform team" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// Deletes, children first: the placement and the settings, then the groups
	// from the bottom (a group with a child cannot be deleted in Azure).
	for _, o := range []client.Object{placement, settings, sandbox, landing, platform} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, 2*timeout, fmt.Sprintf("%T %s gone", o, o.GetName()), gone(o))
	}
	for _, id := range []string{placementID, platformID + "/settings/default", sandboxID, landingID, platformID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
}

// A group whose parent is not in Resource Manager is refused by it, and the
// refusal reaches the object's Ready condition.
func TestManagementGroupParentMissing(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "mg-orphan")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)

	orphan := &management.ManagementGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "orphan"},
		Spec: management.ManagementGroup_Spec{
			AzureName: ns + "-orphan",
			Details: &management.CreateManagementGroupDetails{
				Parent: &management.CreateParentGroupInfo{Reference: &genruntime.ResourceReference{ARMID: mgPrefix + "does-not-exist"}},
			},
		},
	}
	if err := k8s.Create(ctx, orphan); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "an orphan group refused", notReadyWith(orphan, "parent management group"))
	if armFake.HasResource(mgPrefix + ns + "-orphan") {
		t.Fatal("the group was created under a parent that does not exist")
	}
}

// A child group applied before its parent is in Resource Manager (a
// hierarchy applied at once, as the catalog's avm-ptn-alz applies levels 0
// to 6) is refused first, then retried at the usual pace and made once the
// parent is there, not after ASO's very slow retry of an unknown 400 (the
// sync period). The ManagementGroup's ErrorClassifier
// (generator/_extensions/management).
func TestManagementGroupParentLater(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "mg-later")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	parentID := mgPrefix + ns + "-parent"

	child := &management.ManagementGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "child"},
		Spec: management.ManagementGroup_Spec{
			AzureName: ns + "-child",
			Details: &management.CreateManagementGroupDetails{
				Parent: &management.CreateParentGroupInfo{Reference: &genruntime.ResourceReference{ARMID: parentID}},
			},
		},
	}
	if err := k8s.Create(ctx, child); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the child refused while its parent is missing", notReadyWith(child, "parent management group"))

	parent := &management.ManagementGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "parent"},
		Spec:       management.ManagementGroup_Spec{AzureName: ns + "-parent"},
	}
	if err := k8s.Create(ctx, parent); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "parent ManagementGroup Ready", ready(parent))
	eventually(t, timeout, "the child Ready once its parent is there", ready(child))
	expectResource(t, mgPrefix+ns+"-child", func(r map[string]any) error {
		details, _ := properties(r)["details"].(map[string]any)
		parentInfo, _ := details["parent"].(map[string]any)
		if parentInfo["id"] != parentID {
			return fmt.Errorf("parent of child: %v", r)
		}
		return nil
	})

	for _, o := range []client.Object{child, parent} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, 2*timeout, fmt.Sprintf("%T %s gone", o, o.GetName()), gone(o))
	}
}
