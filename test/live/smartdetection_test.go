// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	asoinsights "github.com/Azure/azure-service-operator/v2/api/insights/v1api20200202/storage"

	smart "github.com/highfivelabs/aso-extensions/apis/arm/insights/v20180501preview"
)

const smartDetectionAPI = "2018-05-01-preview"

// An Application Insights component on a workspace (both free with nothing
// ingested) and one of its smart detection rules turned off with a custom
// recipient, then on again (docs/plan.md f57). A rule has no DELETE: the
// object is detached and the rule goes with the component.
func TestLiveSmartDetectionRule(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.Insights")
	ns := namespace(t, "smart")
	wsID := logAnalyticsWorkspace(t, "smart-law")
	componentID := rgID() + "/providers/Microsoft.Insights/components/" + azName("smart-ai")
	fixture(t, componentID, "2020-02-02", map[string]any{
		"location": location, "kind": "web",
		"properties": map[string]any{"Application_Type": "web", "WorkspaceResourceId": wsID},
	})
	component := &asoinsights.Component{}
	component.Namespace, component.Name = ns, "ai"
	component.Spec.AzureName = componentID[strings.LastIndex(componentID, "/")+1:]
	component.Spec.Owner = owner("rg")
	standIn(t, component, componentID, nil)

	rule := &smart.ComponentsProactiveDetectionConfig{}
	rule.Namespace, rule.Name = ns, "slow-pages"
	rule.Annotations = map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}
	rule.Spec = smart.ComponentsProactiveDetectionConfig_Spec{
		AzureName: "slowpageloadtime", Owner: owner("ai"), Location: ptr(location),
		Enabled: ptr(false), SendEmailsToSubscriptionOwners: ptr(false), CustomEmails: []string{"platform@example.com"},
	}
	create(t, timeout, rule)
	ruleID := componentID + "/ProactiveDetectionConfigs/slowpageloadtime"
	expectResource(t, ruleID, smartDetectionAPI, func(r map[string]any) error {
		p := properties(r)
		if p["enabled"] != false || !reflect.DeepEqual(p["customEmails"], []any{"platform@example.com"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, rule, func(r *smart.ComponentsProactiveDetectionConfig) { r.Spec.Enabled = ptr(true) })
	eventually(t, timeout, "the rule Ready after the change", ready(rule))
	expectResource(t, ruleID, smartDetectionAPI, func(r map[string]any) error {
		if properties(r)["enabled"] != true {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, rule)
}
