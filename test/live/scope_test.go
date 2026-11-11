// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

//go:build live

package live

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	consumption "github.com/highfivelabs/aso-extensions/apis/arm/consumption/v20260601"
	costmanagement "github.com/highfivelabs/aso-extensions/apis/arm/costmanagement/v20260601"
	resources "github.com/highfivelabs/aso-extensions/apis/arm/resources/v20250401"
)

// rgRef is the ASO resource group stand-in as an extension resource's owner.
func rgRef() *genruntime.ArbitraryOwnerReference {
	return &genruntime.ArbitraryOwnerReference{Group: "resources.azure.com", Kind: "ResourceGroup", Name: "rg"}
}

// The resource group's tags (free): set, changed, and removed by deleting the
// object.
func TestLiveTagSet(t *testing.T) {
	const api = "2025-04-01"
	ns := namespace(t, "tags")
	tags := &resources.TagSet{}
	tags.Namespace, tags.Name = ns, "rg"
	tags.Spec = resources.TagSet_Spec{Owner: rgRef(), Properties: &resources.Tags{Tags: map[string]string{"hfl-live-run": runID}}}
	create(t, timeout, tags)
	id := rgID() + "/providers/Microsoft.Resources/tags/default"
	want := func(w map[string]any) func(map[string]any) error {
		return func(r map[string]any) error {
			got, _ := properties(r)["tags"].(map[string]any)
			// No tags is {} from Azure.
			if !reflect.DeepEqual(got, w) && (len(got) != 0 || len(w) != 0) {
				return fmt.Errorf("tags %v, want %v", got, w)
			}
			return nil
		}
	}
	expectResource(t, id, api, want(map[string]any{"hfl-live-run": runID}))

	update(t, tags, func(s *resources.TagSet) { s.Spec.Properties.Tags["team"] = "platform" })
	eventually(t, timeout, "TagSet Ready after the change", ready(tags))
	expectResource(t, id, api, want(map[string]any{"hfl-live-run": runID, "team": "platform"}))

	remove(t, timeout, tags)
	expectResource(t, id, api, want(nil))
}

// A monthly cost budget on the resource group (free; the amount is far above
// what the tests spend, so it never mails).
func TestLiveBudget(t *testing.T) {
	const api = "2024-08-01"
	ns := namespace(t, "budget")
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02T15:04:05Z")
	b := &consumption.Budget{}
	b.Namespace, b.Name = ns, "monthly"
	b.Spec = consumption.Budget_Spec{
		AzureName:  azName("monthly"),
		Owner:      rgRef(),
		Category:   ptr(consumption.CategoryType_Cost),
		Amount:     ptr(1000.0),
		TimeGrain:  ptr(consumption.TimeGrainType_Monthly),
		TimePeriod: &consumption.BudgetTimePeriod{StartDate: ptr(start)},
		Notifications: map[string]consumption.Notification{"actual90": {
			Enabled: ptr(true), Operator: ptr(consumption.OperatorType_GreaterThanOrEqualTo), Threshold: ptr(90.0),
			ThresholdType: ptr(consumption.Notification_ThresholdType_Actual), ContactEmails: []string{"live-tests@highfivelabs.dev"},
		}},
	}
	create(t, timeout, b)
	id := rgID() + "/providers/Microsoft.Consumption/budgets/" + b.Spec.AzureName
	expectResource(t, id, api, func(r map[string]any) error {
		if p := properties(r); p["category"] != "Cost" || p["amount"] != 1000.0 || p["timeGrain"] != "Monthly" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, b, func(s *consumption.Budget) { s.Spec.Amount = ptr(1500.0) })
	eventually(t, timeout, "Budget Ready after the change", ready(b))
	expectResource(t, id, api, func(r map[string]any) error {
		if properties(r)["amount"] != 1500.0 {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, b)
	expectGone(t, id, api)
}

// A cost export of the resource group to a blob container in a storage
// account the test makes (free; storage cents), with storage key access (no
// managed identity: granting one a role needs rights Contributor lacks).
func TestLiveCostExport(t *testing.T) {
	const api = "2025-03-01"
	// Azure, 2026-11-10: "RP Not Registered. Register destination storage
	// account subscription with Microsoft.CostManagementExports".
	requireProvider(t, "Microsoft.CostManagementExports")
	ns := namespace(t, "export")
	account := strings.ReplaceAll(azName("costst"), "-", "")
	accountID := rgID() + "/providers/Microsoft.Storage/storageAccounts/" + account
	fixture(t, accountID, "2023-05-01", map[string]any{
		"location": location, "kind": "StorageV2", "sku": map[string]any{"name": "Standard_LRS"},
		"properties": map[string]any{"minimumTlsVersion": "TLS1_2", "allowBlobPublicAccess": false},
	})
	fixture(t, accountID+"/blobServices/default/containers/exports", "2023-05-01", map[string]any{"properties": map[string]any{}})
	now := time.Now().UTC()
	from := now.AddDate(0, 0, 1).Format("2006-01-02") + "T00:00:00Z"
	to := now.AddDate(0, 1, 0).Format("2006-01-02") + "T00:00:00Z"
	e := &costmanagement.Export{}
	e.Namespace, e.Name = ns, "daily"
	e.Spec = costmanagement.Export_Spec{
		AzureName: azName("daily"),
		Owner:     rgRef(),
		Format:    ptr(costmanagement.FormatType_Csv),
		DeliveryInfo: &costmanagement.ExportDeliveryInfo{Destination: &costmanagement.ExportDeliveryDestination{
			ResourceReference: &genruntime.ResourceReference{ARMID: accountID},
			Container:         ptr("exports"),
			RootFolderPath:    ptr("live"),
		}},
		Definition: &costmanagement.ExportDefinition{
			Type:      ptr(costmanagement.ExportType_ActualCost),
			Timeframe: ptr(costmanagement.TimeframeType_MonthToDate),
			DataSet:   &costmanagement.ExportDataset{Granularity: ptr(costmanagement.GranularityType_Daily)},
		},
		Schedule: &costmanagement.ExportSchedule{
			Status:           ptr(costmanagement.StatusType_Inactive),
			Recurrence:       ptr(costmanagement.RecurrenceType_Daily),
			RecurrencePeriod: &costmanagement.ExportRecurrencePeriod{From: ptr(from), To: ptr(to)},
		},
	}
	create(t, timeout, e)
	id := rgID() + "/providers/Microsoft.CostManagement/exports/" + e.Spec.AzureName
	expectResource(t, id, api, func(r map[string]any) error {
		def, _ := properties(r)["definition"].(map[string]any)
		if def["type"] != "ActualCost" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, e, func(s *costmanagement.Export) { s.Spec.Definition.Type = ptr(costmanagement.ExportType_AmortizedCost) })
	eventually(t, timeout, "Export Ready after the change", ready(e))
	expectResource(t, id, api, func(r map[string]any) error {
		def, _ := properties(r)["definition"].(map[string]any)
		if def["type"] != "AmortizedCost" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, e)
	expectGone(t, id, api)
}
