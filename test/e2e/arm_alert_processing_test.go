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

	apr "github.com/highfivelabs/aso-extensions/apis/arm/alertsmanagement/v20210808"
)

// Alert processing rules of docs/plan.md f58, in a resource group: one that
// routes the Sev0 alerts of the resource group to an on-call action group
// (both by ARM ID), and one that suppresses every notification on weekends.
// A rule in a region, one naming a missing action group and one whose
// schedule ends before it starts are refused (the fake's rules,
// alertprocessing.go); a change is PUT again; deleted, both go.
func TestAlertProcessingRules(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "alert-proc")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	agID := rgID + "/providers/Microsoft.Insights/actionGroups/oncall"
	for _, id := range []string{rgID, agID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	rule := func(name, location string, props *apr.AlertProcessingRuleProperties) *apr.ActionRule {
		return &apr.ActionRule{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec: apr.ActionRule_Spec{
				Owner: &genruntime.KnownResourceReference{Name: "rg"}, Location: ptr(location), Properties: props,
			},
		}
	}
	scopes := []genruntime.ResourceReference{{ARMID: rgID}}
	addGroups := func(ids ...string) []apr.Action {
		refs := make([]genruntime.ResourceReference, len(ids))
		for i, id := range ids {
			refs[i] = genruntime.ResourceReference{ARMID: id}
		}
		return []apr.Action{{Add: &apr.AddActionGroups{ActionType: ptr(apr.AddActionGroups_ActionType_AddActionGroups), ActionGroupReferences: refs}}}
	}
	route := rule("sev0-oncall", "Global", &apr.AlertProcessingRuleProperties{
		ScopesReferences: scopes, Actions: addGroups(agID), Enabled: ptr(true), Description: ptr("Sev0 to on-call"),
		Conditions: []apr.Condition{{Field: ptr(apr.Field_Severity), Operator: ptr(apr.Operator_Equals), Values: []string{"Sev0"}}},
	})
	quiet := rule("weekends", "Global", &apr.AlertProcessingRuleProperties{
		ScopesReferences: scopes,
		Actions:          []apr.Action{{RemoveAll: &apr.RemoveAllActionGroups{ActionType: ptr(apr.RemoveAllActionGroups_ActionType_RemoveAllActionGroups)}}},
		Schedule: &apr.Schedule{TimeZone: ptr("UTC"), Recurrences: []apr.Recurrence{{Weekly: &apr.WeeklyRecurrence{
			RecurrenceType: ptr(apr.WeeklyRecurrence_RecurrenceType_Weekly), DaysOfWeek: []apr.DaysOfWeek{apr.DaysOfWeek_Saturday, apr.DaysOfWeek_Sunday},
		}}}},
	})
	regional := rule("regional", "westus", &apr.AlertProcessingRuleProperties{ScopesReferences: scopes, Actions: addGroups(agID)})
	missing := rule("missing-group", "Global", &apr.AlertProcessingRuleProperties{ScopesReferences: scopes, Actions: addGroups(agID + "-gone")})
	backwards := rule("backwards", "Global", &apr.AlertProcessingRuleProperties{
		ScopesReferences: scopes, Actions: addGroups(agID),
		Schedule: &apr.Schedule{EffectiveFrom: ptr("2026-12-02T00:00:00"), EffectiveUntil: ptr("2026-12-01T00:00:00")},
	})
	objs := []conditioned{route, quiet, regional, missing, backwards}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "the routing rule Ready", ready(route))
	eventually(t, timeout, "the weekend rule Ready", ready(quiet))
	eventually(t, timeout, "a rule in a region refused", notReadyWith(regional, "location is Global"))
	eventually(t, timeout, "a missing action group refused", notReadyWith(missing, "was not found"))
	eventually(t, timeout, "a schedule ending before it starts refused", notReadyWith(backwards, "effectiveUntil is after"))

	routeID := rgID + "/providers/Microsoft.AlertsManagement/actionRules/sev0-oncall"
	expectResource(t, routeID, func(r map[string]any) error {
		p := properties(r)
		actions, _ := p["actions"].([]any)
		if len(actions) != 1 || !reflect.DeepEqual(p["scopes"], []any{rgID}) {
			return fmt.Errorf("resource: %v", r)
		}
		if a, _ := actions[0].(map[string]any); a["actionType"] != "AddActionGroups" || !reflect.DeepEqual(a["actionGroupIds"], []any{agID}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, rgID+"/providers/Microsoft.AlertsManagement/actionRules/weekends", func(r map[string]any) error {
		schedule, _ := properties(r)["schedule"].(map[string]any)
		recurrences, _ := schedule["recurrences"].([]any)
		if len(recurrences) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		if w, _ := recurrences[0].(map[string]any); w["recurrenceType"] != "Weekly" || !reflect.DeepEqual(w["daysOfWeek"], []any{"Saturday", "Sunday"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again: Sev1 too.
	if err := change(t, route, func(route *apr.ActionRule) {
		route.Spec.Properties.Conditions[0].Values = []string{"Sev0", "Sev1"}
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, routeID, func(r map[string]any) error {
		conds, _ := properties(r)["conditions"].([]any)
		if len(conds) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		if c, _ := conds[0].(map[string]any); !reflect.DeepEqual(c["values"], []any{"Sev0", "Sev1"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range objs {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	if armFake.HasResource(routeID) {
		t.Fatalf("%s is still in the fake", routeID)
	}
}
