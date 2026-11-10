// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"strings"
	"testing"
)

// A cluster's commitment tier, a data source's kind, and a storage insight
// config's account and key, the key kept aside and not returned.
func TestLogAnalyticsClusterSourceInsight(t *testing.T) {
	a := New(func(string) bool { return true })
	put := func(what, path, body string, want int) string {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
		return resp
	}
	rg := "/subscriptions/" + sub1 + "/resourceGroups/logs"
	put("resource group", rg, `{"location":"westus"}`, http.StatusCreated)
	cluster := rg + "/providers/Microsoft.OperationalInsights/clusters/c1"
	put("cluster without a tier", cluster, `{"location":"westus","sku":{"name":"CapacityReservation","capacity":150}}`, http.StatusBadRequest)
	put("cluster", cluster, `{"location":"westus","sku":{"name":"CapacityReservation","capacity":500}}`, http.StatusCreated)

	ws := rg + "/providers/Microsoft.OperationalInsights/workspaces/law"
	a.AddResource(ws)
	src := ws + "/dataSources/syslog"
	put("data source without a kind", src, `{"properties":{"syslogName":"auth"}}`, http.StatusBadRequest)
	put("data source", src, `{"kind":"LinuxSyslog","properties":{"syslogName":"auth"}}`, http.StatusCreated)

	acct := rg + "/providers/Microsoft.Storage/storageAccounts/diag"
	cfg := ws + "/storageInsightConfigs/diag"
	body := `{"properties":{"containers":["wad-iis-logfiles"],"storageAccount":{"id":"` + acct + `","key":"s3cret"}}}`
	put("insight config without a key", cfg, `{"properties":{"storageAccount":{"id":"`+acct+`"}}}`, http.StatusBadRequest)
	put("insight config on a missing account", cfg, body, http.StatusNotFound)
	a.AddResource(acct)
	if resp := put("insight config", cfg, body, http.StatusCreated); strings.Contains(resp, "s3cret") {
		t.Fatalf("the key was returned: %s", resp)
	}
	if k, ok := a.SharedKey(cfg); !ok || k != "s3cret" {
		t.Fatalf("the key was not kept: %q %v", k, ok)
	}
	if _, resp := do(t, a, http.MethodGet, cfg, ``); strings.Contains(resp, "s3cret") {
		t.Fatalf("the key was returned by GET: %s", resp)
	}
}
