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

	resources "github.com/highfivelabs/aso-extensions/apis/arm/resources/v20250401"
)

// The tags of a scope as one object (Microsoft.Resources/tags/default, an
// extension resource): on the subscription, by ARM ID, and on an ASO resource
// group, by reference; a change replaces the tags; deleting the object
// removes them all.
func TestTagSets(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "tags")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	subTagsID := "/subscriptions/" + subID + "/providers/Microsoft.Resources/tags/default"
	rgTagsID := rgID + "/providers/Microsoft.Resources/tags/default"

	subTags := &resources.TagSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "subscription"},
		Spec: resources.TagSet_Spec{
			Owner:      &genruntime.ArbitraryOwnerReference{ARMID: "/subscriptions/" + subID},
			Properties: &resources.Tags{Tags: map[string]string{"cost-center": "platform", "env": "prod"}},
		},
	}
	rgTags := &resources.TagSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "rg"},
		Spec: resources.TagSet_Spec{
			Owner:      &genruntime.ArbitraryOwnerReference{Group: "resources.azure.com", Kind: "ResourceGroup", Name: "rg"},
			Properties: &resources.Tags{Tags: map[string]string{"team": ns}},
		},
	}
	for _, o := range []client.Object{subTags, rgTags} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "subscription TagSet Ready", ready(subTags))
	eventually(t, timeout, "resource group TagSet Ready", ready(rgTags))
	wantTags := func(id string, want map[string]string) func(map[string]any) error {
		return func(r map[string]any) error {
			got, _ := properties(r)["tags"].(map[string]any)
			if len(got) != len(want) {
				return fmt.Errorf("tags of %s: %v, want %v", id, got, want)
			}
			for k, v := range want {
				if got[k] != v {
					return fmt.Errorf("tags of %s: %v, want %v", id, got, want)
				}
			}
			return nil
		}
	}
	expectResource(t, subTagsID, wantTags(subTagsID, map[string]string{"cost-center": "platform", "env": "prod"}))
	expectResource(t, rgTagsID, wantTags(rgTagsID, map[string]string{"team": ns}))
	if r, _ := armFake.Resource(rgTagsID); r["type"] != "Microsoft.Resources/tags" {
		t.Fatalf("type: %v", r["type"])
	}

	// A change is a PUT of the whole set: the dropped tag is gone.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(subTags), subTags); err != nil {
		t.Fatal(err)
	}
	subTags.Spec.Properties.Tags = map[string]string{"cost-center": "platform-2"}
	if err := k8s.Update(ctx, subTags); err != nil {
		t.Fatal(err)
	}
	expectResource(t, subTagsID, wantTags(subTagsID, map[string]string{"cost-center": "platform-2"}))

	for _, o := range []client.Object{subTags, rgTags} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%s gone", o.GetName()), gone(o))
	}
	if armFake.HasResource(subTagsID) || armFake.HasResource(rgTagsID) {
		t.Fatal("tags are still in Resource Manager")
	}
}
