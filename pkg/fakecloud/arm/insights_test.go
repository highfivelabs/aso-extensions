// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"testing"
)

// Where a log profile may be, what it gives, one per subscription; what a
// workbook template gives.
func TestInsightsLogProfileAndWorkbookTemplate(t *testing.T) {
	a := New(func(string) bool { return true })
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	sub := "/subscriptions/" + sub1
	rg := sub + "/resourceGroups/logs"
	put("resource group", rg, `{"location":"westus"}`, http.StatusCreated)
	storage := rg + "/providers/Microsoft.Storage/storageAccounts/archive"
	profile := func(extra string) string {
		return `{"location":"global","properties":{"categories":["Write","Delete","Action"],"locations":["global","westus"],"retentionPolicy":{"enabled":true,"days":90}` + extra + `}}`
	}
	lp := sub + "/providers/Microsoft.Insights/logprofiles/default"
	put("log profile in a resource group", rg+"/providers/Microsoft.Insights/logprofiles/default", profile(`,"storageAccountId":"`+storage+`"`), http.StatusBadRequest)
	put("log profile without a destination", lp, profile(""), http.StatusBadRequest)
	put("log profile to a missing account", lp, profile(`,"storageAccountId":"`+storage+`"`), http.StatusNotFound)
	a.AddResource(storage)
	put("log profile with another category", lp, `{"location":"global","properties":{"categories":["Read"],"locations":["global"],"storageAccountId":"`+storage+`"}}`, http.StatusBadRequest)
	put("log profile without locations", lp, `{"location":"global","properties":{"categories":["Write"],"locations":[],"storageAccountId":"`+storage+`"}}`, http.StatusBadRequest)
	put("log profile keeping too long", lp, `{"location":"global","properties":{"categories":["Write"],"locations":["global"],"storageAccountId":"`+storage+`","retentionPolicy":{"enabled":true,"days":400}}}`, http.StatusBadRequest)
	put("log profile", lp, profile(`,"storageAccountId":"`+storage+`"`), http.StatusCreated)
	put("log profile again", lp, profile(`,"storageAccountId":"`+storage+`"`), http.StatusOK)
	put("a second log profile", sub+"/providers/Microsoft.Insights/logprofiles/other", profile(`,"storageAccountId":"`+storage+`"`), http.StatusConflict)

	tpl := rg + "/providers/Microsoft.Insights/workbooktemplates/costs"
	put("template without data", tpl, `{"location":"westus","properties":{"galleries":[{"name":"Costs","category":"Platform","type":"workbook","resourceType":"Azure Monitor"}]}}`, http.StatusBadRequest)
	put("template in no gallery", tpl, `{"location":"westus","properties":{"templateData":{"version":"Notebook/1.0","items":[]},"galleries":[]}}`, http.StatusBadRequest)
	put("template", tpl, `{"location":"westus","properties":{"priority":1,"templateData":{"version":"Notebook/1.0","items":[]},"galleries":[{"name":"Costs","category":"Platform","type":"workbook","resourceType":"Azure Monitor","order":100}]}}`, http.StatusCreated)
}
