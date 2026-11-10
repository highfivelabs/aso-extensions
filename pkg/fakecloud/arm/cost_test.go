// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const (
	consumption = "/providers/Microsoft.Consumption/budgets/"
	costExports = "/providers/Microsoft.CostManagement/exports/"
)

// budgetBody is a budget with the given notifications (a JSON object's
// members) and time period start.
func budgetBody(start, notifications string) string {
	return `{"properties":{"category":"Cost","amount":100,"timeGrain":"Monthly","timePeriod":{"startDate":"` + start +
		`"},"notifications":{` + notifications + `}}}`
}

func notification(name, contacts string) string {
	return `"` + name + `":{"enabled":true,"operator":"GreaterThan","threshold":80` + contacts + `}`
}

// Where budgets may be kept and what their notifications, periods and
// filters must hold.
func TestBudgets(t *testing.T) {
	a := New(func(string) bool { return true })
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	sub := "/subscriptions/" + sub1
	rg := sub + "/resourceGroups/app"
	put("resource group", rg, `{"location":"westus"}`, http.StatusCreated)
	put("group", mgPrefix+"platform", group(""), http.StatusCreated)
	account := rg + "/providers/Microsoft.Storage/storageAccounts/s"
	a.AddResource(account)
	billing := "/providers/Microsoft.Billing/billingAccounts/1234"
	a.AddResource(billing)
	actions := rg + "/providers/Microsoft.Insights/actionGroups/oncall"
	a.AddResource(actions)
	email := `,"contactEmails":["a@example.com"]`
	byGroup := `,"contactEmails":[],"contactGroups":["` + actions + `"]`
	const start = "2026-11-01T00:00:00Z"

	// Scopes.
	put("at a subscription", sub+consumption+"b1", budgetBody(start, notification("n", email)), http.StatusCreated)
	put("at a resource group", rg+consumption+"b1", budgetBody(start, notification("n", email)), http.StatusCreated)
	put("at a management group", mgPrefix+"platform"+consumption+"b1", budgetBody(start, notification("n", email)), http.StatusCreated)
	put("at a billing account", billing+consumption+"b1", budgetBody(start, notification("n", email)), http.StatusCreated)
	put("at a resource", account+consumption+"b1", budgetBody(start, notification("n", email)), http.StatusBadRequest)

	// Notifications.
	put("an action group at a resource group", rg+consumption+"b2", budgetBody(start, notification("n", byGroup)), http.StatusCreated)
	put("an action group at a management group", mgPrefix+"platform"+consumption+"b2", budgetBody(start, notification("n", byGroup)), http.StatusBadRequest)
	put("a missing action group", rg+consumption+"b3", budgetBody(start, notification("n", `,"contactEmails":[],"contactGroups":["`+actions+`-nope"]`)), http.StatusNotFound)
	put("no contact", rg+consumption+"b3", budgetBody(start, notification("n", `,"contactEmails":[]`)), http.StatusBadRequest)
	six := []string{}
	for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
		six = append(six, notification(n, email))
	}
	put("six notifications", sub+consumption+"b3", budgetBody(start, strings.Join(six, ",")), http.StatusBadRequest)
	put("five notifications", sub+consumption+"b3", budgetBody(start, strings.Join(six[:5], ",")), http.StatusCreated)
	put("a threshold over 1000", sub+consumption+"b4",
		budgetBody(start, `"n":{"enabled":true,"operator":"GreaterThan","threshold":1001`+email+`}`), http.StatusBadRequest)

	// The time period and the filter.
	put("a start not on the first", sub+consumption+"b4", budgetBody("2026-11-02T00:00:00Z", notification("n", email)), http.StatusBadRequest)
	put("a start before June 2017", sub+consumption+"b4", budgetBody("2017-05-01T00:00:00Z", notification("n", email)), http.StatusBadRequest)
	put("an end before the start", sub+consumption+"b4",
		`{"properties":{"amount":1,"timePeriod":{"startDate":"`+start+`","endDate":"2026-10-01T00:00:00Z"}}}`, http.StatusBadRequest)
	put("an and of one", sub+consumption+"b4",
		`{"properties":{"amount":1,"timePeriod":{"startDate":"`+start+`"},"filter":{"and":[{"tags":{"name":"env","operator":"In","values":["prod"]}}]}}}`, http.StatusBadRequest)
	put("an and of two", sub+consumption+"b4",
		`{"properties":{"amount":1,"timePeriod":{"startDate":"`+start+`"},"filter":{"and":[{"tags":{"name":"env","operator":"In","values":["prod"]}},{"dimensions":{"name":"ResourceGroupName","operator":"In","values":["app"]}}]}}}`, http.StatusCreated)
}

func exportBody(destination, extra string) string {
	return `{"properties":{"format":"Csv","deliveryInfo":{"destination":` + destination +
		`},"definition":{"type":"ActualCost","timeframe":"MonthToDate"}` + extra + `}}`
}

// Where exports may be kept, where they deliver, and that a SAS token is
// never read back.
func TestCostExports(t *testing.T) {
	a := New(func(string) bool { return true })
	put := func(what, path, body string, want int) string {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
		return resp
	}
	sub := "/subscriptions/" + sub1
	rg := sub + "/resourceGroups/app"
	put("resource group", rg, `{"location":"westus"}`, http.StatusCreated)
	account := rg + "/providers/Microsoft.Storage/storageAccounts/s"
	a.AddResource(account)
	toAccount := `{"type":"AzureBlob","resourceId":"` + account + `","container":"exports"}`

	put("at a subscription", sub+costExports+"e1", exportBody(toAccount, ""), http.StatusCreated)
	put("at a resource group", rg+costExports+"e1", exportBody(toAccount, ""), http.StatusCreated)
	put("at a resource", account+costExports+"e1", exportBody(toAccount, ""), http.StatusBadRequest)
	put("to a missing account", sub+costExports+"e2", exportBody(`{"resourceId":"`+account+`-nope","container":"exports"}`, ""), http.StatusNotFound)
	put("to nothing", sub+costExports+"e2", exportBody(`{"container":"exports"}`, ""), http.StatusBadRequest)
	put("to an account name without a SAS token", sub+costExports+"e2", exportBody(`{"storageAccount":"other","container":"exports"}`, ""), http.StatusBadRequest)

	// A SAS token is kept and returned obfuscated.
	put("to an account name with a SAS token", sub+costExports+"e2",
		exportBody(`{"storageAccount":"other","container":"exports","sasToken":"sv=2026&sig=secret"}`, ""), http.StatusCreated)
	_, got := do(t, a, http.MethodGet, sub+costExports+"e2", "")
	if strings.Contains(got, "sig=secret") || !strings.Contains(got, obfuscated) {
		t.Fatalf("the SAS token was read back: %s", got)
	}

	// Periods.
	put("Custom without a time period", sub+costExports+"e3", `{"properties":{"deliveryInfo":{"destination":`+toAccount+`},"definition":{"type":"ActualCost","timeframe":"Custom"}}}`, http.StatusBadRequest)
	put("Custom with a time period", sub+costExports+"e3",
		`{"properties":{"deliveryInfo":{"destination":`+toAccount+`},"definition":{"type":"ActualCost","timeframe":"Custom","timePeriod":{"from":"2026-01-01T00:00:00Z","to":"2026-02-01T00:00:00Z"}}}}`, http.StatusCreated)
	put("a recurrence that ends before it starts", sub+costExports+"e4",
		exportBody(toAccount, `,"schedule":{"status":"Active","recurrence":"Daily","recurrencePeriod":{"from":"2027-01-01T00:00:00Z","to":"2026-01-01T00:00:00Z"}}`), http.StatusBadRequest)

	// A managed identity needs a location, and is given a principal.
	put("an identity without a location", sub+costExports+"e5", `{"identity":{"type":"SystemAssigned"},`+exportBody(toAccount, "")[1:], http.StatusBadRequest)
	resp := put("an identity with a location", sub+costExports+"e5", `{"identity":{"type":"SystemAssigned"},"location":"westus",`+exportBody(toAccount, "")[1:], http.StatusCreated)
	var r struct {
		Identity struct{ PrincipalID, TenantID string } `json:"identity"`
	}
	if err := json.Unmarshal([]byte(resp), &r); err != nil || r.Identity.PrincipalID == "" || r.Identity.TenantID == "" {
		t.Fatalf("identity not filled (%v): %s", err, resp)
	}
}
