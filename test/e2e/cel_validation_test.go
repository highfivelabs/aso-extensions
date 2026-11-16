// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	authv1 "github.com/highfivelabs/aso-extensions/apis/arm/authorization/v20200501"
	resourcesv1 "github.com/highfivelabs/aso-extensions/apis/arm/resources/v20220201"
)

// change gets obj, applies mutate and updates it, retrying on a conflict
// (the manager adds its finalizer and annotations meanwhile). It returns the
// update's error.
func change[T client.Object](t *testing.T, obj T, mutate func(T)) error {
	t.Helper()
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(obj), obj); err != nil {
			return err
		}
		mutate(obj)
		return k8s.Update(context.Background(), obj)
	})
}

func refused(t *testing.T, err error, what, text string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: accepted, want refused with %q", what, text)
	}
	if !errors.IsInvalid(err) || !strings.Contains(err.Error(), text) {
		t.Fatalf("%s: %v, want an Invalid error with %q", what, err, text)
	}
}

// The CEL rules of patch 0016 (docs/plan.md §8, the answers of 2026-11-16):
// a generated kind's spec.owner cannot change, nor a set spec.azureName; an
// owner reference has the form ASO's webhook requires. The API server checks
// them, with no webhook. The owners named here do not exist, so the manager
// only waits for them.
func TestCELWriteOnceOwnerAndAzureName(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "cel-write-once")
	newSpec := func(name, azureName string) *resourcesv1.TemplateSpec {
		return &resourcesv1.TemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec: resourcesv1.TemplateSpec_Spec{
				AzureName: azureName,
				Location:  ptr("westeurope"),
				Owner:     &genruntime.KnownResourceReference{Name: "rg-none"},
			},
		}
	}

	// Created: the rules let a create through.
	ts := newSpec("named", "spec-a")
	if err := k8s.Create(ctx, ts); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Other fields still change.
	if err := change(t, ts, func(o *resourcesv1.TemplateSpec) { o.Spec.Description = ptr("changed") }); err != nil {
		t.Fatalf("changing the description: %v", err)
	}

	refused(t, change(t, ts, func(o *resourcesv1.TemplateSpec) { o.Spec.AzureName = "spec-b" }),
		"changing spec.azureName", "spec.azureName cannot be changed")
	refused(t, change(t, ts, func(o *resourcesv1.TemplateSpec) { o.Spec.AzureName = "" }),
		"removing spec.azureName", "spec.azureName cannot be changed")
	refused(t, change(t, ts, func(o *resourcesv1.TemplateSpec) {
		o.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg-other"}
	}), "changing spec.owner", "spec.owner cannot be changed")
	refused(t, change(t, ts, func(o *resourcesv1.TemplateSpec) {
		o.Spec.Owner = &genruntime.KnownResourceReference{ARMID: "/subscriptions/" + subID + "/resourceGroups/rg-none"}
	}), "changing spec.owner to an ARM ID", "spec.owner cannot be changed")

	// An empty azureName may be set once (as ASO allows), then not changed.
	unnamed := newSpec("unnamed", "")
	if err := k8s.Create(ctx, unnamed); err != nil {
		t.Fatalf("create without azureName: %v", err)
	}
	if err := change(t, unnamed, func(o *resourcesv1.TemplateSpec) { o.Spec.AzureName = "spec-c" }); err != nil {
		t.Fatalf("setting an empty azureName: %v", err)
	}
	refused(t, change(t, unnamed, func(o *resourcesv1.TemplateSpec) { o.Spec.AzureName = "spec-d" }),
		"changing spec.azureName after setting it", "spec.azureName cannot be changed")

	// The owner's form, on create: exactly one of name and armId.
	both := newSpec("owner-both", "")
	both.Spec.Owner.ARMID = "/subscriptions/" + subID + "/resourceGroups/rg-none"
	refused(t, k8s.Create(ctx, both), "an owner with name and armId", "set exactly one of name and armId")
	neither := newSpec("owner-neither", "")
	neither.Spec.Owner = &genruntime.KnownResourceReference{}
	refused(t, k8s.Create(ctx, neither), "an owner with neither name nor armId", "set exactly one of name and armId")
}

// An extension kind's owner (ArbitraryOwnerReference): armId alone, or
// group, kind and name together; and it cannot change either.
func TestCELArbitraryOwner(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "cel-arbitrary-owner")
	lock := func(name string, owner genruntime.ArbitraryOwnerReference) *authv1.ManagementLock {
		return &authv1.ManagementLock{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec: authv1.ManagementLock_Spec{
				Owner: &owner,
				Level: level(authv1.ManagementLockProperties_Level_CanNotDelete),
			},
		}
	}
	rgID := "/subscriptions/" + subID + "/resourceGroups/rg-none"

	refused(t, k8s.Create(ctx, lock("both", genruntime.ArbitraryOwnerReference{ARMID: rgID, Name: "rg-none"})),
		"armId with a name", "set armId alone, or group, kind and name together")
	refused(t, k8s.Create(ctx, lock("name-only", genruntime.ArbitraryOwnerReference{Name: "rg-none"})),
		"a name without group and kind", "set armId alone, or group, kind and name together")

	byID := lock("by-id", genruntime.ArbitraryOwnerReference{ARMID: rgID})
	if err := k8s.Create(ctx, byID); err != nil {
		t.Fatalf("create with armId: %v", err)
	}
	byName := lock("by-name", genruntime.ArbitraryOwnerReference{Group: "resources.azure.com", Kind: "ResourceGroup", Name: "rg-none"})
	if err := k8s.Create(ctx, byName); err != nil {
		t.Fatalf("create with group, kind and name: %v", err)
	}
	refused(t, change(t, byName, func(o *authv1.ManagementLock) { o.Spec.Owner.Name = "rg-other" }),
		"changing an extension's owner", "spec.owner cannot be changed")
}
