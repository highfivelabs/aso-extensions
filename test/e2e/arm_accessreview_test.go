// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	authorization "github.com/highfivelabs/aso-extensions/apis/arm/authorization/v20211201preview"
	subscription "github.com/highfivelabs/aso-extensions/apis/arm/subscription/v20211001"
)

// Access reviews and the tenant's subscription policy, generated from specs
// whose PUT body is the properties themselves: a schedule definition with no
// azureName gets the stable UUID ASO's webhook would give it, recorded once
// (Azure takes only a GUID), and its settings and recurrence reach Azure
// nested as the spec has them, though the kind flattens them; a history
// definition named "random" gets a random UUID; the scope's default settings
// and the subscription policy cannot be deleted, so their objects are
// detached and the resources stay.
func TestAccessReviewsAndSubscriptionPolicy(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "accessreview")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	sub := "/subscriptions/" + subID
	owner := &genruntime.ArbitraryOwnerReference{ARMID: sub}
	authz := sub + "/providers/Microsoft.Authorization/"
	defer func() {
		if t.Failed() {
			for _, l := range armFake.Log {
				if contains(l, "accessReview") || contains(l, "Microsoft.Subscription") {
					t.Log("fake resource manager: " + l)
				}
			}
		}
	}()

	// A quarterly review of the subscription's role assignments.
	quarterly := &authorization.AccessReviewScheduleDefinition{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "quarterly"},
		Spec: authorization.AccessReviewScheduleDefinition_Spec{
			Owner:                    owner,
			DisplayName:              ptr("Quarterly review"),
			Reviewers:                []authorization.AccessReviewReviewer{{PrincipalId: ptr(pimPrincipal)}},
			DefaultDecision:          ptr(authorization.DefaultDecisionType_Deny),
			MailNotificationsEnabled: ptr(true),
			InstanceDurationInDays:   ptr(14),
			Recurrence: &authorization.AccessReviewRecurrenceSettings{
				PatternType: ptr(authorization.AccessReviewRecurrencePatternType_AbsoluteMonthly),
				Interval:    ptr(3),
				RangeType:   ptr(authorization.AccessReviewRecurrenceRangeType_NoEnd),
				StartDate:   ptr("2030-01-01T00:00:00Z"),
			},
		},
	}
	if err := k8s.Create(ctx, quarterly); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "schedule definition Ready", ready(quarterly))
	want := outoftree.MakeUUIDName(quarterly.Name, outoftree.MakeUniqueOwnerScopedString(
		quarterly.Owner(), authorization.GroupVersion.WithKind("AccessReviewScheduleDefinition").GroupKind(), ns, quarterly.Name))
	if quarterly.Spec.AzureName != want {
		t.Fatalf("azureName %q, want the stable UUID %q", quarterly.Spec.AzureName, want)
	}
	definitionID := authz + "accessReviewScheduleDefinitions/" + want
	expectResource(t, definitionID, func(r map[string]any) error {
		p := properties(r)
		settings, _ := p["settings"].(map[string]any)
		recurrence, _ := settings["recurrence"].(map[string]any)
		pattern, _ := recurrence["pattern"].(map[string]any)
		rng, _ := recurrence["range"].(map[string]any)
		if p["displayName"] != "Quarterly review" || settings["defaultDecision"] != "Deny" || settings["mailNotificationsEnabled"] != true ||
			pattern["type"] != "absoluteMonthly" || pattern["interval"] != float64(3) || rng["type"] != "noEnd" {
			return fmt.Errorf("properties: %v", p)
		}
		return nil
	})
	// A change is PUT under the same, recorded name.
	if err := change(t, quarterly, func(quarterly *authorization.AccessReviewScheduleDefinition) {
		quarterly.Spec.InstanceDurationInDays = ptr(7)
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, definitionID, func(r map[string]any) error {
		settings, _ := properties(r)["settings"].(map[string]any)
		if settings["instanceDurationInDays"] != float64(7) {
			return fmt.Errorf("settings: %v", settings)
		}
		return nil
	})
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(quarterly), quarterly); err != nil {
		t.Fatal(err)
	}
	if quarterly.Spec.AzureName != want {
		t.Fatalf("azureName changed from %q to %q", want, quarterly.Spec.AzureName)
	}

	// A history report, named at random.
	history := &authorization.AccessReviewHistoryDefinition{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "history"},
		Spec: authorization.AccessReviewHistoryDefinition_Spec{
			Owner:        owner,
			OperatorSpec: &authorization.AccessReviewHistoryDefinitionOperatorSpec{NamingConvention: ptr("random")},
			DisplayName:  ptr("Review history"),
		},
	}
	if err := k8s.Create(ctx, history); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "history definition Ready", ready(history))
	if _, err := uuid.Parse(history.Spec.AzureName); err != nil {
		t.Fatalf("history azureName %q: %v", history.Spec.AzureName, err)
	}
	historyID := authz + "accessReviewHistoryDefinitions/" + history.Spec.AzureName

	// The subscription's default review settings.
	settings := &authorization.AccessReviewScheduleSetting{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "defaults", Annotations: map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}},
		Spec: authorization.AccessReviewScheduleSetting_Spec{
			Owner:                           owner,
			JustificationRequiredOnApproval: ptr(true),
		},
	}
	if err := k8s.Create(ctx, settings); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "settings Ready", ready(settings))
	settingsID := authz + "accessReviewScheduleSettings/default"
	expectResource(t, settingsID, func(r map[string]any) error {
		if p := properties(r); p["justificationRequiredOnApproval"] != true {
			return fmt.Errorf("properties: %v", p)
		}
		return nil
	})

	// The tenant's subscription policy: no owner.
	policy := &subscription.SubscriptionTenantPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "tenant", Annotations: map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}},
		Spec: subscription.SubscriptionTenantPolicy_Spec{
			BlockSubscriptionsLeavingTenant: ptr(true),
			ExemptedPrincipals:              []subscription.Policy_ExemptedPrincipals_Spec{pimPrincipal},
		},
	}
	if err := k8s.Create(ctx, policy); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "subscription policy Ready", ready(policy))
	policyID := "/providers/Microsoft.Subscription/policies/default"
	expectResource(t, policyID, func(r map[string]any) error {
		p := properties(r)
		if p["blockSubscriptionsLeavingTenant"] != true || fmt.Sprint(p["exemptedPrincipals"]) != "["+pimPrincipal+"]" {
			return fmt.Errorf("properties: %v", p)
		}
		return nil
	})

	// Deleting: the definitions are deleted; the singletons are detached and stay.
	for _, obj := range []client.Object{quarterly, history, settings, policy} {
		if err := k8s.Delete(ctx, obj); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, obj.GetName()+" gone", gone(obj))
	}
	for _, id := range []string{definitionID, historyID} {
		if armFake.HasResource(id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
	for _, id := range []string{settingsID, policyID} {
		if !armFake.HasResource(id) {
			t.Errorf("%s was deleted; Azure cannot delete it", id)
		}
	}
}
