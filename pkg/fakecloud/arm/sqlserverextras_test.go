// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// Enabled DevOps auditing names a target; a workload group's minimum is
// within its cap.
func TestSQLServerExtras(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/sql"
	server := rg + "/providers/Microsoft.Sql/servers/sql"
	for _, id := range []string{rg, server, server + "/databases/dw"} {
		a.AddResource(id)
	}
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	audit := server + "/devOpsAuditingSettings/Default"
	put("DevOps auditing with no target", audit, `{"properties":{"state":"Enabled"}}`, http.StatusBadRequest)
	put("DevOps auditing to Azure Monitor", audit, `{"properties":{"state":"Enabled","isAzureMonitorTargetEnabled":true}}`, http.StatusCreated)
	put("DevOps auditing off", audit, `{"properties":{"state":"Disabled"}}`, http.StatusOK)
	group := server + "/databases/dw/workloadGroups/etl"
	put("a group whose minimum is above its cap", group, `{"properties":{"minResourcePercent":60,"maxResourcePercent":50,"minResourcePercentPerRequest":5}}`, http.StatusBadRequest)
	put("a group over 100 percent", group, `{"properties":{"minResourcePercent":0,"maxResourcePercent":120,"minResourcePercentPerRequest":5}}`, http.StatusBadRequest)
	put("a group", group, `{"properties":{"minResourcePercent":10,"maxResourcePercent":50,"minResourcePercentPerRequest":5}}`, http.StatusCreated)
	put("a classifier", group+"/workloadClassifiers/loader", `{"properties":{"memberName":"etl_loader","importance":"high"}}`, http.StatusCreated)
}
