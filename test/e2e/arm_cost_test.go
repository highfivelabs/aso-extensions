// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	consumption "github.com/highfivelabs/aso-extensions/apis/arm/consumption/v20260601"
	costmanagement "github.com/highfivelabs/aso-extensions/apis/arm/costmanagement/v20260601"
	management "github.com/highfivelabs/aso-extensions/apis/arm/management/v20230401"
)

const (
	budgetsPrefix = "/providers/Microsoft.Consumption/budgets/"
	exportsPrefix = "/providers/Microsoft.CostManagement/exports/"
)

// emailAt is a notification to an address at a threshold.
func emailAt(threshold float64, kind consumption.Notification_ThresholdType, emails ...string) consumption.Notification {
	return consumption.Notification{
		Enabled:       ptr(true),
		Operator:      ptr(consumption.OperatorType_GreaterThanOrEqualTo),
		Threshold:     ptr(threshold),
		ThresholdType: ptr(kind),
		ContactEmails: emails,
	}
}

// Budgets at a subscription (an ARM ID owner), an ASO ResourceGroup and our
// ManagementGroup, with notifications to addresses, roles and an action
// group and a filter; exports at the subscription to a storage account by
// reference, with a managed identity and a schedule, and at the resource
// group to an account by name with a SAS token from a Secret. Then a change
// and the deletes.
func TestBudgetsAndCostExports(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "cost")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	subscription := "/subscriptions/" + subID
	rgID := subscription + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	actionGroupID := rgID + "/providers/Microsoft.Insights/actionGroups/finops"
	armFake.AddResource(actionGroupID)
	accountID := rgID + "/providers/Microsoft.Storage/storageAccounts/" + ns + "costs"
	armFake.AddResource(accountID)
	groupID := mgPrefix + ns + "-platform"
	secret(t, ns, "partner", map[string]string{"sas": "sv=2026-01-01&sig=not-a-real-signature"})

	platform := &management.ManagementGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "platform"},
		Spec:       management.ManagementGroup_Spec{AzureName: ns + "-platform"},
	}
	if err := k8s.Create(ctx, platform); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "ManagementGroup Ready", ready(platform))

	monthly := &consumption.BudgetTimePeriod{StartDate: ptr("2026-11-01T00:00:00Z"), EndDate: ptr("2027-10-31T00:00:00Z")}
	forecast := emailAt(100, consumption.Notification_ThresholdType_Forecasted, "finops@example.com")
	forecast.ContactRoles = []string{"Owner", "Contributor"}
	forecast.ContactGroupsReferences = []genruntime.ResourceReference{{ARMID: actionGroupID}}
	forecast.Locale = ptr(consumption.CultureCode_EnGb)
	subBudget := &consumption.Budget{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "subscription"},
		Spec: consumption.Budget_Spec{
			AzureName:  ns + "-monthly",
			Owner:      &genruntime.ArbitraryOwnerReference{ARMID: subscription},
			Category:   ptr(consumption.CategoryType_Cost),
			Amount:     ptr(5000.0),
			TimeGrain:  ptr(consumption.TimeGrainType_Monthly),
			TimePeriod: monthly,
			Notifications: map[string]consumption.Notification{
				"actual80":    emailAt(80, consumption.Notification_ThresholdType_Actual, "finops@example.com", "lead@example.com"),
				"forecast100": forecast,
			},
			Filter: &consumption.BudgetFilter{
				And: []consumption.BudgetFilterProperties{
					{Tags: &consumption.BudgetComparisonExpression{Name: ptr("env"), Operator: ptr(consumption.BudgetOperatorType_In), Values: []string{"prod"}}},
					{Dimensions: &consumption.BudgetComparisonExpression{Name: ptr("ResourceGroupName"), Operator: ptr(consumption.BudgetOperatorType_In), Values: []string{ns + "-rg"}}},
				},
			},
		},
	}
	rgBudget := &consumption.Budget{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "rg"},
		Spec: consumption.Budget_Spec{
			Owner:         &genruntime.ArbitraryOwnerReference{Group: "resources.azure.com", Kind: "ResourceGroup", Name: "rg"},
			Category:      ptr(consumption.CategoryType_Cost),
			Amount:        ptr(250.5),
			TimeGrain:     ptr(consumption.TimeGrainType_Quarterly),
			TimePeriod:    &consumption.BudgetTimePeriod{StartDate: ptr("2026-10-01T00:00:00Z")},
			Notifications: map[string]consumption.Notification{"actual90": emailAt(90, consumption.Notification_ThresholdType_Actual, "app@example.com")},
		},
	}
	groupBudget := &consumption.Budget{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "platform"},
		Spec: consumption.Budget_Spec{
			Owner:         &genruntime.ArbitraryOwnerReference{Group: "management.azure.hfl.dev", Kind: "ManagementGroup", Name: "platform"},
			Category:      ptr(consumption.CategoryType_Cost),
			Amount:        ptr(100000.0),
			TimeGrain:     ptr(consumption.TimeGrainType_Annually),
			TimePeriod:    &consumption.BudgetTimePeriod{StartDate: ptr("2026-01-01T00:00:00Z")},
			Notifications: map[string]consumption.Notification{"actual100": emailAt(100, consumption.Notification_ThresholdType_Actual, "cfo@example.com")},
		},
	}
	for _, o := range []conditioned{subBudget, rgBudget, groupBudget} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, "Budget "+o.GetName()+" Ready", ready(o))
	}
	subBudgetID := subscription + budgetsPrefix + ns + "-monthly"
	rgBudgetID := rgID + budgetsPrefix + "rg"
	groupBudgetID := groupID + budgetsPrefix + "platform"
	wantBudget := func(id string, amount float64, more func(map[string]any) error) {
		t.Helper()
		expectResource(t, id, func(r map[string]any) error {
			p := properties(r)
			if r["type"] != "Microsoft.Consumption/budgets" || p["category"] != "Cost" || p["amount"] != amount {
				return fmt.Errorf("budget %s: %v", id, r)
			}
			return more(p)
		})
	}
	wantSubBudget := func(amount float64) {
		t.Helper()
		wantBudget(subBudgetID, amount, func(p map[string]any) error {
			period, _ := p["timePeriod"].(map[string]any)
			notifications, _ := p["notifications"].(map[string]any)
			f, _ := notifications["forecast100"].(map[string]any)
			a, _ := notifications["actual80"].(map[string]any)
			filter, _ := p["filter"].(map[string]any)
			and, _ := filter["and"].([]any)
			if p["timeGrain"] != "Monthly" || period["startDate"] != "2026-11-01T00:00:00Z" || period["endDate"] != "2027-10-31T00:00:00Z" ||
				len(notifications) != 2 || f["thresholdType"] != "Forecasted" || f["locale"] != "en-gb" || f["threshold"] != float64(100) ||
				!reflect.DeepEqual(f["contactGroups"], []any{actionGroupID}) || !reflect.DeepEqual(f["contactRoles"], []any{"Owner", "Contributor"}) ||
				!reflect.DeepEqual(a["contactEmails"], []any{"finops@example.com", "lead@example.com"}) || a["operator"] != "GreaterThanOrEqualTo" ||
				len(and) != 2 {
				return fmt.Errorf("budget at the subscription: %v", p)
			}
			return nil
		})
	}
	wantSubBudget(5000)
	wantBudget(rgBudgetID, 250.5, func(p map[string]any) error {
		if p["timeGrain"] != "Quarterly" {
			return fmt.Errorf("budget at the resource group: %v", p)
		}
		return nil
	})
	wantBudget(groupBudgetID, 100000, func(p map[string]any) error {
		if p["timeGrain"] != "Annually" {
			return fmt.Errorf("budget at the management group: %v", p)
		}
		return nil
	})

	// Exports.
	daily := &costmanagement.Export{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "daily"},
		Spec: costmanagement.Export_Spec{
			Owner:    &genruntime.ArbitraryOwnerReference{ARMID: subscription},
			Location: ptr("westus"),
			Identity: &costmanagement.SystemAssignedServiceIdentity{Type: ptr(costmanagement.SystemAssignedServiceIdentityType_SystemAssigned)},
			Format:   ptr(costmanagement.FormatType_Parquet),
			DeliveryInfo: &costmanagement.ExportDeliveryInfo{Destination: &costmanagement.ExportDeliveryDestination{
				Type:              ptr(costmanagement.DestinationType_AzureBlob),
				ResourceReference: &genruntime.ResourceReference{ARMID: accountID},
				Container:         ptr("exports"),
				RootFolderPath:    ptr("subscription"),
			}},
			Definition: &costmanagement.ExportDefinition{
				Type:      ptr(costmanagement.ExportType_AmortizedCost),
				Timeframe: ptr(costmanagement.TimeframeType_MonthToDate),
				DataSet: &costmanagement.ExportDataset{
					Granularity: ptr(costmanagement.GranularityType_Daily),
					Configuration: &costmanagement.ExportDatasetConfiguration{
						Columns: []string{"Date", "MeterId", "CostInBillingCurrency"},
					},
				},
			},
			Schedule: &costmanagement.ExportSchedule{
				Status:           ptr(costmanagement.StatusType_Active),
				Recurrence:       ptr(costmanagement.RecurrenceType_Daily),
				RecurrencePeriod: &costmanagement.ExportRecurrencePeriod{From: ptr("2026-12-01T00:00:00Z"), To: ptr("2027-12-01T00:00:00Z")},
			},
			PartitionData:         ptr(true),
			CompressionMode:       ptr(costmanagement.CompressionModeType_Snappy),
			DataOverwriteBehavior: ptr(costmanagement.DataOverwriteBehaviorType_OverwritePreviousReport),
			ExportDescription:     ptr("daily amortized cost"),
		},
	}
	partner := &costmanagement.Export{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "partner"},
		Spec: costmanagement.Export_Spec{
			Owner:  &genruntime.ArbitraryOwnerReference{Group: "resources.azure.com", Kind: "ResourceGroup", Name: "rg"},
			Format: ptr(costmanagement.FormatType_Csv),
			DeliveryInfo: &costmanagement.ExportDeliveryInfo{Destination: &costmanagement.ExportDeliveryDestination{
				StorageAccount: ptr("partnercosts"),
				SasToken:       &genruntime.SecretReference{Name: "partner", Key: "sas"},
				Container:      ptr("exports"),
			}},
			Definition: &costmanagement.ExportDefinition{
				Type:       ptr(costmanagement.ExportType_ActualCost),
				Timeframe:  ptr(costmanagement.TimeframeType_Custom),
				TimePeriod: &costmanagement.ExportTimePeriod{From: ptr("2026-10-01T00:00:00Z"), To: ptr("2026-10-31T00:00:00Z")},
			},
		},
	}
	for _, o := range []conditioned{daily, partner} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, "Export "+o.GetName()+" Ready", ready(o))
	}
	dailyID := subscription + exportsPrefix + "daily"
	partnerID := rgID + exportsPrefix + "partner"
	wantDaily := func(status string) {
		t.Helper()
		expectResource(t, dailyID, func(r map[string]any) error {
			p := properties(r)
			identity, _ := r["identity"].(map[string]any)
			dest, _ := (p["deliveryInfo"].(map[string]any))["destination"].(map[string]any)
			def, _ := p["definition"].(map[string]any)
			dataSet, _ := def["dataSet"].(map[string]any)
			config, _ := dataSet["configuration"].(map[string]any)
			schedule, _ := p["schedule"].(map[string]any)
			if r["type"] != "Microsoft.CostManagement/exports" || r["location"] != "westus" || identity["type"] != "SystemAssigned" ||
				dest["resourceId"] != accountID || dest["container"] != "exports" || dest["rootFolderPath"] != "subscription" ||
				def["type"] != "AmortizedCost" || def["timeframe"] != "MonthToDate" || dataSet["granularity"] != "Daily" ||
				!reflect.DeepEqual(config["columns"], []any{"Date", "MeterId", "CostInBillingCurrency"}) ||
				schedule["status"] != status || schedule["recurrence"] != "Daily" ||
				p["format"] != "Parquet" || p["partitionData"] != true || p["compressionMode"] != "snappy" ||
				p["dataOverwriteBehavior"] != "OverwritePreviousReport" {
				return fmt.Errorf("daily export: %v", r)
			}
			return nil
		})
	}
	wantDaily("Active")
	if daily.Status.Identity == nil || daily.Status.Identity.PrincipalId == nil || *daily.Status.Identity.PrincipalId == "" {
		t.Fatalf("status.identity: %+v", daily.Status.Identity)
	}
	// The fake refuses an account name without a SAS token, so the partner
	// export being Ready shows the token was read from the Secret and sent;
	// what is kept has it obfuscated, as Azure returns it.
	expectResource(t, partnerID, func(r map[string]any) error {
		p := properties(r)
		dest, _ := (p["deliveryInfo"].(map[string]any))["destination"].(map[string]any)
		def, _ := p["definition"].(map[string]any)
		period, _ := def["timePeriod"].(map[string]any)
		if dest["storageAccount"] != "partnercosts" || dest["sasToken"] != "********" || def["timeframe"] != "Custom" ||
			period["from"] != "2026-10-01T00:00:00Z" || period["to"] != "2026-10-31T00:00:00Z" {
			return fmt.Errorf("partner export: %v", r)
		}
		return nil
	})

	// Changes are PUT again: a budget's amount, an export's schedule paused.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(subBudget), subBudget); err != nil {
		t.Fatal(err)
	}
	subBudget.Spec.Amount = ptr(6500.0)
	if err := k8s.Update(ctx, subBudget); err != nil {
		t.Fatal(err)
	}
	wantSubBudget(6500)
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(daily), daily); err != nil {
		t.Fatal(err)
	}
	daily.Spec.Schedule.Status = ptr(costmanagement.StatusType_Inactive)
	if err := k8s.Update(ctx, daily); err != nil {
		t.Fatal(err)
	}
	wantDaily("Inactive")

	for _, o := range []client.Object{subBudget, rgBudget, groupBudget, daily, partner, platform} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%s gone", o.GetName()), gone(o))
	}
	for _, id := range []string{subBudgetID, rgBudgetID, groupBudgetID, dailyID, partnerID, groupID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
}

// What Resource Manager refuses reaches the Ready condition: a budget on a
// resource (not a Cost Management scope), an action group at a management
// group, a start that is not the first of a month, and an export to a
// storage account that does not exist.
func TestBudgetsAndCostExportsRefused(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "cost-refused")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	subscription := "/subscriptions/" + subID
	rgID := subscription + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	accountID := rgID + "/providers/Microsoft.Storage/storageAccounts/" + ns + "costs"
	armFake.AddResource(accountID)
	actionGroupID := rgID + "/providers/Microsoft.Insights/actionGroups/finops"
	armFake.AddResource(actionGroupID)

	group := &management.ManagementGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "group"},
		Spec:       management.ManagementGroup_Spec{AzureName: ns + "-group"},
	}
	if err := k8s.Create(ctx, group); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "ManagementGroup Ready", ready(group))

	budget := func(name string, owner *genruntime.ArbitraryOwnerReference, start string, n consumption.Notification) *consumption.Budget {
		return &consumption.Budget{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec: consumption.Budget_Spec{
				AzureName:     ns + "-" + name,
				Owner:         owner,
				Category:      ptr(consumption.CategoryType_Cost),
				Amount:        ptr(10.0),
				TimeGrain:     ptr(consumption.TimeGrainType_Monthly),
				TimePeriod:    &consumption.BudgetTimePeriod{StartDate: ptr(start)},
				Notifications: map[string]consumption.Notification{"n": n},
			},
		}
	}
	email := emailAt(50, consumption.Notification_ThresholdType_Actual, "a@example.com")
	withGroup := emailAt(50, consumption.Notification_ThresholdType_Actual, "a@example.com")
	withGroup.ContactGroupsReferences = []genruntime.ResourceReference{{ARMID: actionGroupID}}
	onResource := budget("on-resource", &genruntime.ArbitraryOwnerReference{ARMID: accountID}, "2026-11-01T00:00:00Z", email)
	groupContacts := budget("group-contacts", &genruntime.ArbitraryOwnerReference{Group: "management.azure.hfl.dev", Kind: "ManagementGroup", Name: "group"}, "2026-11-01T00:00:00Z", withGroup)
	midMonth := budget("mid-month", &genruntime.ArbitraryOwnerReference{ARMID: subscription}, "2026-11-15T00:00:00Z", email)
	noAccount := &costmanagement.Export{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "no-account"},
		Spec: costmanagement.Export_Spec{
			AzureName: ns + "-no-account",
			Owner:     &genruntime.ArbitraryOwnerReference{ARMID: subscription},
			DeliveryInfo: &costmanagement.ExportDeliveryInfo{Destination: &costmanagement.ExportDeliveryDestination{
				ResourceReference: &genruntime.ResourceReference{ARMID: accountID + "-gone"},
				Container:         ptr("exports"),
			}},
			Definition: &costmanagement.ExportDefinition{
				Type:      ptr(costmanagement.ExportType_ActualCost),
				Timeframe: ptr(costmanagement.TimeframeType_MonthToDate),
			},
		},
	}
	for _, c := range []struct {
		o    conditioned
		text string
	}{
		{onResource, "not at " + accountID},
		{groupContacts, "contact groups are supported only at a subscription or a resource group"},
		{midMonth, "first of a month"},
		{noAccount, "was not found"},
	} {
		if err := k8s.Create(ctx, c.o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, c.o.GetName()+" refused", notReadyWith(c.o, c.text))
	}
	if armFake.HasResource(accountID + budgetsPrefix + ns + "-on-resource") {
		t.Fatal("a budget was kept on a resource")
	}
	for _, o := range []client.Object{onResource, groupContacts, midMonth, noAccount, group} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%s gone", o.GetName()), gone(o))
	}
}
