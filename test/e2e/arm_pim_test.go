// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	authorization "github.com/highfivelabs/aso-extensions/apis/arm/authorization/v20201001"
	fakearm "github.com/highfivelabs/aso-extensions/pkg/fakecloud/arm"
)

const pimPrincipal = "11111111-2222-3333-4444-555555555555"

// Privileged Identity Management, generated from the 2020-10-01 spec with
// the eligibility request's configuration copied from ASO pull request
// #5638: a request's name is defaulted to a UUID when the reconciler first
// claims it and recorded in spec.azureName (Azure takes only a GUID); a
// built-in role named by its well-known name is resolved by listing the role
// definitions; a request cannot be deleted (Azure has no DELETE, and ASO's
// reconciler sends none), so the object waits until it is detached, and a
// removal is another request; a role
// management policy assignment is created and deleted.
func TestPrivilegedIdentityManagement(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "pim")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	readerID := "/subscriptions/" + subID + "/providers/Microsoft.Authorization/roleDefinitions/" + fakearm.BuiltInRoles["Reader"]
	authz := rgID + "/providers/Microsoft.Authorization/"
	rgOwner := &genruntime.ArbitraryOwnerReference{Group: "resources.azure.com", Kind: "ResourceGroup", Name: "rg"}
	var eligible *authorization.RoleEligibilityScheduleRequest
	defer func() {
		if t.Failed() {
			for _, l := range armFake.Log {
				if contains(l, "Microsoft.Authorization") || contains(l, ns) {
					t.Log("fake resource manager: " + l)
				}
			}
			t.Logf("eligibility request: annotations %v, finalizers %v, azureName %q, conditions %+v", eligible.Annotations, eligible.Finalizers, eligible.Spec.AzureName, eligible.Status.Conditions)
		}
	}()

	// The principal comes from a ConfigMap, as ASO's own test takes it from
	// a managed identity's exported principal ID.
	if err := k8s.Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "identity-settings"},
		Data:       map[string]string{"principalId": pimPrincipal},
	}); err != nil {
		t.Fatal(err)
	}

	// Eligibility for Reader on the resource group, by the role's well-known
	// name; no azureName.
	eligible = &authorization.RoleEligibilityScheduleRequest{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "eligible-reader"},
		Spec: authorization.RoleEligibilityScheduleRequest_Spec{
			Owner:                   rgOwner,
			PrincipalIdFromConfig:   &genruntime.ConfigMapReference{Name: "identity-settings", Key: "principalId"},
			RoleDefinitionReference: &genruntime.WellKnownResourceReference{WellKnownName: "Reader"},
			RequestType:             ptr(authorization.RoleEligibilityScheduleRequestProperties_RequestType_AdminAssign),
			Justification:           ptr("on call"),
			ScheduleInfo: &authorization.RoleEligibilityScheduleRequestProperties_ScheduleInfo{
				Expiration: &authorization.RoleEligibilityScheduleRequestProperties_ScheduleInfo_Expiration{
					Duration: ptr("P30D"),
					Type:     ptr(authorization.RoleEligibilityScheduleRequestProperties_ScheduleInfo_Expiration_Type_AfterDuration),
				},
			},
		},
	}
	if err := k8s.Create(ctx, eligible); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "eligibility request Ready", ready(eligible))

	// The name: the stable UUID ASO's webhook would give it, recorded.
	want := outoftree.MakeUUIDName(eligible.Name, outoftree.MakeUniqueOwnerScopedString(
		eligible.Owner(), authorization.GroupVersion.WithKind("RoleEligibilityScheduleRequest").GroupKind(), ns, eligible.Name))
	if eligible.Spec.AzureName != want {
		t.Fatalf("azureName %q, want the stable UUID %q", eligible.Spec.AzureName, want)
	}
	eligibleID := authz + "roleEligibilityScheduleRequests/" + want
	expectResource(t, eligibleID, func(r map[string]any) error {
		p := properties(r)
		if p["roleDefinitionId"] != readerID || p["principalId"] != pimPrincipal || p["requestType"] != "AdminAssign" || p["justification"] != "on call" {
			return fmt.Errorf("properties: %v", p)
		}
		return nil
	})
	schedule := authz + "roleEligibilitySchedules/" + want
	if !armFake.HasResource(schedule) {
		t.Fatalf("no eligibility schedule %s", schedule)
	}
	if eligible.Status.TargetRoleEligibilityScheduleId == nil || *eligible.Status.TargetRoleEligibilityScheduleId != schedule {
		t.Fatalf("status.targetRoleEligibilityScheduleId: %v", eligible.Status.TargetRoleEligibilityScheduleId)
	}

	// Activation, by the role's ARM ID, with a random name.
	activate := &authorization.RoleAssignmentScheduleRequest{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "activate-reader"},
		Spec: authorization.RoleAssignmentScheduleRequest_Spec{
			Owner:                   rgOwner,
			OperatorSpec:            &authorization.RoleAssignmentScheduleRequestOperatorSpec{NamingConvention: ptr("random")},
			PrincipalId:             ptr(pimPrincipal),
			RoleDefinitionReference: &genruntime.WellKnownResourceReference{ResourceReference: genruntime.ResourceReference{ARMID: readerID}},
			RequestType:             ptr(authorization.RoleAssignmentScheduleRequestProperties_RequestType_SelfActivate),
		},
	}
	if err := k8s.Create(ctx, activate); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "activation request Ready", ready(activate))
	if _, err := uuid.Parse(activate.Spec.AzureName); err != nil || activate.Spec.AzureName == want {
		t.Fatalf("activation azureName %q: %v", activate.Spec.AzureName, err)
	}
	activeID := authz + "roleAssignmentScheduleRequests/" + activate.Spec.AzureName
	expectResource(t, activeID, func(r map[string]any) error {
		if p := properties(r); p["roleDefinitionId"] != readerID || p["requestType"] != "SelfActivate" {
			return fmt.Errorf("properties: %v", p)
		}
		return nil
	})
	// The name stays what was recorded on later reconciles.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(activate), activate); err != nil {
		t.Fatal(err)
	}
	recorded := activate.Spec.AzureName
	activate.Spec.Justification = ptr("incident 42")
	if err := k8s.Update(ctx, activate); err != nil {
		t.Fatal(err)
	}
	expectResource(t, activeID, func(r map[string]any) error {
		if properties(r)["justification"] != "incident 42" {
			return fmt.Errorf("properties: %v", properties(r))
		}
		return nil
	})
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(activate), activate); err != nil {
		t.Fatal(err)
	}
	if activate.Spec.AzureName != recorded {
		t.Fatalf("azureName changed from %q to %q", recorded, activate.Spec.AzureName)
	}

	// Deleting a request: Azure has no DELETE for one, which the generated
	// kind knows from the spec, so ASO's reconciler sends none and the object
	// waits with that on its Ready condition; the request stays.
	if err := k8s.Delete(ctx, eligible); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the refused delete on the Ready condition", notReadyWith(eligible, "does not support deletion"))
	if !armFake.HasResource(eligibleID) {
		t.Fatal("the eligibility request was deleted")
	}
	// detach-on-delete lets the object go, the request staying in Azure.
	detach := func(obj client.Object) {
		t.Helper()
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			t.Fatal(err)
		}
		a := obj.GetAnnotations()
		if a == nil {
			a = map[string]string{}
		}
		a["serviceoperator.azure.com/reconcile-policy"] = "detach-on-delete"
		obj.SetAnnotations(a)
		if err := k8s.Update(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	detach(eligible)
	// ASO reconciles on a change of reconcile-policy only to or from skip, so
	// on an object already being deleted detach-on-delete takes effect at the
	// next retry, which for this refusal is very slow (minutes); any change
	// of the spec reconciles it at once.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(eligible), eligible); err != nil {
		t.Fatal(err)
	}
	eligible.Spec.Justification = ptr("detached")
	if err := k8s.Update(ctx, eligible); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the eligibility request object gone", gone(eligible))
	if !armFake.HasResource(eligibleID) || !armFake.HasResource(schedule) {
		t.Fatal("detaching removed the request or the eligibility")
	}

	// Undoing the eligibility is another request, AdminRemove, naming the
	// schedule; the object's own name is already a GUID, which is kept.
	removeName := "aaaaaaaa-bbbb-cccc-dddd-000000000001"
	remove := &authorization.RoleEligibilityScheduleRequest{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: removeName,
			Annotations: map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}},
		Spec: authorization.RoleEligibilityScheduleRequest_Spec{
			Owner:                           rgOwner,
			PrincipalId:                     ptr(pimPrincipal),
			RoleDefinitionReference:         &genruntime.WellKnownResourceReference{WellKnownName: "Reader"},
			RequestType:                     ptr(authorization.RoleEligibilityScheduleRequestProperties_RequestType_AdminRemove),
			TargetRoleEligibilityScheduleId: ptr(schedule),
		},
	}
	if err := k8s.Create(ctx, remove); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "removal request Ready", ready(remove))
	if remove.Spec.AzureName != removeName {
		t.Fatalf("removal azureName %q, want its name %q", remove.Spec.AzureName, removeName)
	}
	if armFake.HasResource(schedule) {
		t.Fatal("the eligibility schedule outlived its removal")
	}

	// A role management policy assignment (its name is policy_role).
	policyGUID := "bbbbbbbb-0000-0000-0000-000000000001"
	policyID := authz + "roleManagementPolicies/" + policyGUID
	armFake.AddResource(policyID)
	policyAssignment := &authorization.RoleManagementPolicyAssignment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "reader-policy"},
		Spec: authorization.RoleManagementPolicyAssignment_Spec{
			AzureName:               policyGUID + "_" + fakearm.BuiltInRoles["Reader"],
			Owner:                   rgOwner,
			PolicyReference:         &genruntime.ResourceReference{ARMID: policyID},
			RoleDefinitionReference: &genruntime.ResourceReference{ARMID: readerID},
		},
	}
	if err := k8s.Create(ctx, policyAssignment); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "role management policy assignment Ready", ready(policyAssignment))
	policyAssignmentID := authz + "roleManagementPolicyAssignments/" + policyAssignment.Spec.AzureName
	expectResource(t, policyAssignmentID, func(r map[string]any) error {
		if p := properties(r); p["policyId"] != policyID || p["roleDefinitionId"] != readerID {
			return fmt.Errorf("properties: %v", p)
		}
		return nil
	})

	for _, o := range []client.Object{activate, remove} {
		detach(o)
	}
	for _, o := range []client.Object{activate, remove, policyAssignment} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	if armFake.HasResource(policyAssignmentID) {
		t.Fatal("the role management policy assignment is still in Resource Manager")
	}
	if !armFake.HasResource(activeID) {
		t.Fatal("the activation request was deleted")
	}
	for _, l := range armFake.Log {
		if contains(l, "DELETE ") && contains(l, "ScheduleRequests/") && contains(l, ns) {
			t.Errorf("a schedule request was sent a DELETE: %s", l)
		}
	}
}
