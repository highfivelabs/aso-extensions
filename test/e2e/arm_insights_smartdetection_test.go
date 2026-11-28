// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	smart "github.com/highfivelabs/aso-extensions/apis/arm/insights/v20180501preview"
)

// Application Insights smart detection rules of docs/plan.md f57, on ASO's
// Component stand-in: a rule turned off with custom mail recipients, and an
// extension rule with the subscription owners' mail off; a rule name
// Application Insights does not define is refused (the fake's rule,
// smartdetection.go); a change is PUT again; a rule has no DELETE, so the
// objects are detached and the rules stay with the component.
func TestSmartDetectionRules(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "ai-smart")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	componentID := rgID + "/providers/Microsoft.Insights/components/" + ns + "-ai"
	armFake.AddResource(rgID)
	armFake.AddResource(componentID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	appInsightsComponent(t, ns, "ai", "rg", ns+"-rg", ns+"-ai")
	detach := map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}
	rule := func(name, azureName string, spec smart.ComponentsProactiveDetectionConfig_Spec) *smart.ComponentsProactiveDetectionConfig {
		spec.AzureName, spec.Owner = azureName, &genruntime.KnownResourceReference{Name: "ai"}
		return &smart.ComponentsProactiveDetectionConfig{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Annotations: detach}, Spec: spec}
	}
	slow := rule("slow-pages", "slowpageloadtime", smart.ComponentsProactiveDetectionConfig_Spec{
		Enabled: ptr(false), SendEmailsToSubscriptionOwners: ptr(false), CustomEmails: []string{"web-team@example.com"},
	})
	volume := rule("data-volume", "extension_billingdatavolumedailyspikeextension", smart.ComponentsProactiveDetectionConfig_Spec{
		Enabled: ptr(true), SendEmailsToSubscriptionOwners: ptr(false),
	})
	unknown := rule("cpu", "cpuspike", smart.ComponentsProactiveDetectionConfig_Spec{Enabled: ptr(true)})
	objs := []conditioned{slow, volume, unknown}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "the slow page rule Ready", ready(slow))
	eventually(t, timeout, "the data volume rule Ready", ready(volume))
	eventually(t, timeout, "a rule Application Insights does not define refused", notReadyWith(unknown, "not a smart detection rule"))

	slowID := componentID + "/ProactiveDetectionConfigs/slowpageloadtime"
	expectResource(t, slowID, func(r map[string]any) error {
		p := properties(r)
		if p["enabled"] != false || p["sendEmailsToSubscriptionOwners"] != false || !reflect.DeepEqual(p["customEmails"], []any{"web-team@example.com"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	volumeID := componentID + "/ProactiveDetectionConfigs/extension_billingdatavolumedailyspikeextension"
	expectResource(t, volumeID, func(r map[string]any) error {
		if properties(r)["enabled"] != true {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again.
	if err := change(t, slow, func(slow *smart.ComponentsProactiveDetectionConfig) {
		slow.Spec.Enabled = ptr(true)
		slow.Spec.CustomEmails = []string{"web-team@example.com", "oncall@example.com"}
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, slowID, func(r map[string]any) error {
		p := properties(r)
		if p["enabled"] != true || !reflect.DeepEqual(p["customEmails"], []any{"web-team@example.com", "oncall@example.com"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// Detached: the objects go, the rules stay with the component.
	for _, o := range objs {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, id := range []string{slowID, volumeID} {
		if !armFake.HasResource(id) {
			t.Fatalf("%s was deleted; a smart detection rule has no DELETE", id)
		}
	}
}
