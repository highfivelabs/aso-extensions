// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	apr "github.com/highfivelabs/aso-extensions/apis/arm/alertsmanagement/v20210808"
)

const alertProcessingAPI = "2021-08-08"

// A disabled alert processing rule that would suppress the notifications of
// the live group's Sev4 alerts on weekends, then enabled for Saturdays only
// (docs/plan.md f58). Free: alert processing rules bill nothing, and the
// group fires no alerts. Its location is Global whatever the live region.
func TestLiveAlertProcessingRule(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.AlertsManagement")
	ns := namespace(t, "apr")
	rule := &apr.ActionRule{}
	rule.Namespace, rule.Name = ns, "quiet"
	rule.Spec = apr.ActionRule_Spec{
		AzureName: azName("quiet"), Owner: owner("rg"), Location: ptr("Global"),
		Properties: &apr.AlertProcessingRuleProperties{
			ScopesReferences: []genruntime.ResourceReference{{ARMID: rgID()}},
			Enabled:          ptr(false), Description: ptr("live test " + runID),
			Conditions: []apr.Condition{{Field: ptr(apr.Field_Severity), Operator: ptr(apr.Operator_Equals), Values: []string{"Sev4"}}},
			Actions:    []apr.Action{{RemoveAll: &apr.RemoveAllActionGroups{ActionType: ptr(apr.RemoveAllActionGroups_ActionType_RemoveAllActionGroups)}}},
			Schedule: &apr.Schedule{TimeZone: ptr("UTC"), Recurrences: []apr.Recurrence{{Weekly: &apr.WeeklyRecurrence{
				RecurrenceType: ptr(apr.WeeklyRecurrence_RecurrenceType_Weekly), DaysOfWeek: []apr.DaysOfWeek{apr.DaysOfWeek_Saturday, apr.DaysOfWeek_Sunday},
			}}}},
		},
	}
	create(t, timeout, rule)
	ruleID := rgID() + "/providers/Microsoft.AlertsManagement/actionRules/" + rule.Spec.AzureName
	expectResource(t, ruleID, alertProcessingAPI, func(r map[string]any) error {
		p := properties(r)
		scopes, _ := p["scopes"].([]any)
		if p["enabled"] != false || len(scopes) != 1 || !strings.EqualFold(fmt.Sprint(scopes[0]), rgID()) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, rule, func(r *apr.ActionRule) {
		r.Spec.Properties.Enabled = ptr(true)
		r.Spec.Properties.Schedule.Recurrences[0].Weekly.DaysOfWeek = []apr.DaysOfWeek{apr.DaysOfWeek_Saturday}
	})
	eventually(t, timeout, "the rule Ready after the change", ready(rule))
	expectResource(t, ruleID, alertProcessingAPI, func(r map[string]any) error {
		schedule, _ := properties(r)["schedule"].(map[string]any)
		recurrences, _ := schedule["recurrences"].([]any)
		if len(recurrences) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		if w, _ := recurrences[0].(map[string]any); !reflect.DeepEqual(w["daysOfWeek"], []any{"Saturday"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, rule)
	expectGone(t, ruleID, alertProcessingAPI)
}
