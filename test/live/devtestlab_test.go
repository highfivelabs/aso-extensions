// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"testing"
	"time"

	dtl "github.com/highfivelabs/aso-extensions/apis/arm/devtestlab/v20180915"
)

// A DevTest Lab with no VMs (docs/plan.md f51): the lab, its nightly
// shutdown schedule, a notification channel by email and a cost target,
// then a change of the schedule's time. A lab bills only for what it runs;
// DevTest Labs makes a storage account and a Key Vault for it beside it in
// the live resource group (storage cents), named from the lab's name, and
// deletes them with the lab (the sweeper, by creation time, if a run stops
// early). The cost target has no DELETE:
// detached, it goes with the lab. A lab takes a few minutes to make and up
// to ten to delete.
func TestLiveDevTestLab(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.DevTestLab")
	ns := namespace(t, "dtl")
	wait := 30 * time.Minute
	lab := &dtl.Lab{}
	lab.Namespace, lab.Name = ns, "lab"
	lab.Spec = dtl.Lab_Spec{
		AzureName: azName("dtl"), Owner: owner("rg"), Location: ptr(location),
		LabStorageType: ptr(dtl.LabProperties_LabStorageType_Standard),
	}
	create(t, wait, lab)
	labID := rgID() + "/providers/Microsoft.DevTestLab/labs/" + lab.Spec.AzureName

	shutdown := &dtl.LabsSchedule{}
	shutdown.Namespace, shutdown.Name = ns, "shutdown"
	shutdown.Spec = dtl.LabsSchedule_Spec{
		AzureName: "LabVmsShutdown", Owner: owner("lab"), TaskType: ptr("LabVmsShutdownTask"),
		Status: ptr(dtl.ScheduleProperties_Status_Enabled), TimeZoneId: ptr("UTC"), DailyRecurrence: &dtl.DayDetails{Time: ptr("1900")},
	}
	channel := &dtl.LabsNotificationChannel{}
	channel.Namespace, channel.Name = ns, "ops"
	channel.Spec = dtl.LabsNotificationChannel_Spec{
		AzureName: azName("ops"), Owner: owner("lab"), EmailRecipient: ptr("ops@example.com"),
		Events: []dtl.Event{{EventName: ptr(dtl.NotificationChannelEventType_AutoShutdown)}},
	}
	cost := &dtl.LabsCost{}
	cost.Namespace, cost.Name = ns, "cost"
	cost.Annotations = map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}
	cost.Spec = dtl.LabsCost_Spec{
		AzureName: "targetCost", Owner: owner("lab"), CurrencyCode: ptr("USD"),
		TargetCost: &dtl.TargetCostProperties{Status: ptr(dtl.TargetCostStatus_Enabled), Target: ptr(10), CycleType: ptr(dtl.ReportingCycleType_CalendarMonth)},
	}
	create(t, timeout, shutdown, channel, cost)
	expectResource(t, labID+"/schedules/LabVmsShutdown", devTestLabAPI, func(r map[string]any) error {
		if properties(r)["taskType"] != "LabVmsShutdownTask" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	update(t, shutdown, func(s *dtl.LabsSchedule) { s.Spec.DailyRecurrence.Time = ptr("2000") })
	eventually(t, timeout, "the schedule Ready after the change", ready(shutdown))
	expectResource(t, labID+"/schedules/LabVmsShutdown", devTestLabAPI, func(r map[string]any) error {
		d, _ := properties(r)["dailyRecurrence"].(map[string]any)
		if d["time"] != "2000" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, cost)
	remove(t, timeout, channel)
	remove(t, timeout, shutdown)
	remove(t, wait, lab)
	expectGone(t, labID, devTestLabAPI)
}
