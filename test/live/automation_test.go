// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

//go:build live

package live

import (
	"fmt"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	automation "github.com/highfivelabs/aso-extensions/apis/arm/automation/v20241023"
)

const automationAPI = "2024-10-23"

// An Automation account (Basic: free until jobs run) with a runbook (never
// published or run), a variable and a daily schedule, all made by our kinds,
// changed and deleted.
func TestLiveAutomation(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.Automation")
	ns := namespace(t, "automation")

	account := &automation.AutomationAccount{}
	account.Namespace, account.Name = ns, "ops"
	account.Spec = automation.AutomationAccount_Spec{
		AzureName: azName("ops"), Owner: owner("rg"), Location: ptr(location),
		Sku: &automation.Sku{Name: ptr(automation.SkuNameEnum_Basic)},
	}
	create(t, timeout, account)
	accountID := rgID() + "/providers/Microsoft.Automation/automationAccounts/" + account.Spec.AzureName
	expectResource(t, accountID, automationAPI, func(r map[string]any) error {
		if sku, _ := properties(r)["sku"].(map[string]any); sku["name"] != "Basic" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	runbook := &automation.AutomationAccountsRunbook{}
	runbook.Namespace, runbook.Name = ns, "hello"
	runbook.Spec = automation.AutomationAccountsRunbook_Spec{
		AzureName: "hello", Owner: owner("ops"), Location: ptr(location),
		RunbookType: ptr(automation.RunbookTypeEnum_PowerShell72), Description: ptr("live run " + runID),
	}
	variable := &automation.AutomationAccountsVariable{}
	variable.Namespace, variable.Name = ns, "greeting"
	variable.Spec = automation.AutomationAccountsVariable_Spec{
		AzureName: "greeting", Owner: owner("ops"), Value: ptr(`"hello"`), IsEncrypted: ptr(false),
	}
	schedule := &automation.AutomationAccountsSchedule{}
	schedule.Namespace, schedule.Name = ns, "daily"
	schedule.Spec = automation.AutomationAccountsSchedule_Spec{
		AzureName: "daily", Owner: owner("ops"),
		StartTime: ptr(time.Now().UTC().Add(24 * time.Hour).Truncate(time.Hour).Format(time.RFC3339)),
		Frequency: ptr(automation.ScheduleFrequency_Day), Interval: &apiextensionsv1.JSON{Raw: []byte("1")}, TimeZone: ptr("UTC"),
	}
	create(t, timeout, runbook, variable, schedule)
	expectResource(t, accountID+"/runbooks/hello", automationAPI, func(r map[string]any) error {
		if p := properties(r); p["runbookType"] != "PowerShell72" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, accountID+"/variables/greeting", automationAPI, func(r map[string]any) error {
		if p := properties(r); p["value"] != `"hello"` {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, accountID+"/schedules/daily", automationAPI, func(r map[string]any) error {
		if p := properties(r); p["frequency"] != "Day" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	update(t, variable, func(v *automation.AutomationAccountsVariable) { v.Spec.Value = ptr(`"changed"`) })
	eventually(t, timeout, "variable Ready after the change", ready(variable))
	expectResource(t, accountID+"/variables/greeting", automationAPI, func(r map[string]any) error {
		if p := properties(r); p["value"] != `"changed"` {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range []conditioned{schedule, variable, runbook} {
		remove(t, timeout, o)
	}
	for _, c := range []string{"/schedules/daily", "/variables/greeting", "/runbooks/hello"} {
		expectGone(t, accountID+c, automationAPI)
	}
	remove(t, timeout, account)
	expectGone(t, accountID, automationAPI)
}
