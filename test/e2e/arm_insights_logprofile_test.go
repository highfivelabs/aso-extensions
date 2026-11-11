// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	lp "github.com/highfivelabs/aso-extensions/apis/arm/insights/v20160301"
	wbt "github.com/highfivelabs/aso-extensions/apis/arm/insights/v20201120"
)

// The subscription's activity log profile (owned by the subscription, an
// ARM ID: generator patch 0009), archived to a storage account by ARM ID, its
// retention changed; a workbook template in a resource group; both deleted.
func TestLogProfileAndWorkbookTemplate(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "logprofile")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	storageID := rgID + "/providers/Microsoft.Storage/storageAccounts/" + "activityarchive"
	armFake.AddResource(rgID)
	armFake.AddResource(storageID)
	resourceGroup(t, ns, "rg", ns+"-rg")

	profile := &lp.LogProfile{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "activity"},
		Spec: lp.LogProfile_Spec{
			AzureName:               "default",
			Owner:                   &genruntime.ArbitraryOwnerReference{ARMID: "/subscriptions/" + subID},
			Location:                ptr("global"),
			Categories:              []string{"Write", "Delete", "Action"},
			Locations:               []string{"global", "westus"},
			StorageAccountReference: &genruntime.ResourceReference{ARMID: storageID},
			RetentionPolicy:         &lp.MicrosoftCommonRetentionPolicy{Enabled: ptr(true), Days: ptr(90)},
		},
	}
	data, err := json.Marshal(map[string]any{"version": "Notebook/1.0", "items": []any{map[string]any{"type": 1, "content": map[string]any{"json": "# Costs"}}}})
	if err != nil {
		t.Fatal(err)
	}
	template := &wbt.WorkbookTemplate{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "costs"},
		Spec: wbt.WorkbookTemplate_Spec{
			Owner:        &genruntime.KnownResourceReference{Name: "rg"},
			Location:     ptr("westus"),
			Author:       ptr("platform team"),
			Priority:     ptr(1),
			TemplateData: &apiextensionsv1.JSON{Raw: data},
			Galleries: []wbt.WorkbookTemplateGallery{{
				Name: ptr("Costs"), Category: ptr("Platform"), Type: ptr("workbook"), ResourceType: ptr("Azure Monitor"), Order: ptr(100),
			}},
		},
	}
	for _, o := range []client.Object{profile, template} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []client.Object{profile, template} {
		eventually(t, timeout, fmt.Sprintf("%T Ready", o), ready(o.(conditioned)))
	}
	profileID := "/subscriptions/" + subID + "/providers/Microsoft.Insights/logprofiles/default"
	expectResource(t, profileID, func(r map[string]any) error {
		p := properties(r)
		retention, _ := p["retentionPolicy"].(map[string]any)
		if r["type"] != "Microsoft.Insights/logprofiles" || p["storageAccountId"] != storageID || retention["days"] != float64(90) ||
			!reflect.DeepEqual(p["categories"], []any{"Write", "Delete", "Action"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	templateID := rgID + "/providers/Microsoft.Insights/workbooktemplates/costs"
	expectResource(t, templateID, func(r map[string]any) error {
		p := properties(r)
		td, _ := p["templateData"].(map[string]any)
		galleries, _ := p["galleries"].([]any)
		if r["type"] != "Microsoft.Insights/workbooktemplates" || td["version"] != "Notebook/1.0" || len(galleries) != 1 || p["author"] != "platform team" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(profile), profile); err != nil {
		t.Fatal(err)
	}
	profile.Spec.RetentionPolicy.Days = ptr(365)
	if err := k8s.Update(ctx, profile); err != nil {
		t.Fatal(err)
	}
	expectResource(t, profileID, func(r map[string]any) error {
		if retention, _ := properties(r)["retentionPolicy"].(map[string]any); retention["days"] != float64(365) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range []client.Object{profile, template} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T gone", o), gone(o))
	}
	for _, id := range []string{profileID, templateID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
}

// A log profile the fake refuses: no storage account and no Event Hubs rule.
func TestLogProfileRefused(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "logprofile-no")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	profile := &lp.LogProfile{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "activity"},
		Spec: lp.LogProfile_Spec{
			AzureName:       "nowhere",
			Owner:           &genruntime.ArbitraryOwnerReference{ARMID: "/subscriptions/" + subID},
			Location:        ptr("global"),
			Categories:      []string{"Write"},
			Locations:       []string{"global"},
			RetentionPolicy: &lp.MicrosoftCommonRetentionPolicy{Enabled: ptr(false), Days: ptr(0)},
		},
	}
	if err := k8s.Create(ctx, profile); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the refusal on the Ready condition", notReadyWith(profile, "archives to a storage account or streams"))
}
