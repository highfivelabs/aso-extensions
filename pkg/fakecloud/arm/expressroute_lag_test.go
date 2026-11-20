// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// What an ExpressRoute Direct LAG must give, and its authorizations, which a
// LAG PUT leaves as they are.
func TestExpressRouteLag(t *testing.T) {
	a := New(func(string) bool { return true })
	put := func(what, path, body string, want int) map[string]any {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
		out := map[string]any{}
		_ = json.Unmarshal([]byte(resp), &out)
		return out
	}
	rg := "/subscriptions/" + sub1 + "/resourceGroups/net"
	put("resource group", rg, `{"location":"westus"}`, http.StatusCreated)
	lag := rg + "/providers/Microsoft.Network/expressRouteLags/lag"
	put("LAG without a peering location", lag, `{"location":"westus","properties":{"bandwidthInGbps":100}}`, http.StatusBadRequest)
	put("LAG without bandwidth", lag, `{"location":"westus","properties":{"peeringLocation":"Equinix-Silicon-Valley"}}`, http.StatusBadRequest)
	body := `{"location":"westus","properties":{"peeringLocation":"Equinix-Silicon-Valley","bandwidthInGbps":100,"numberOfPorts":2%s}}`
	put("LAG", lag, strings.Replace(body, "%s", "", 1), http.StatusCreated)
	r := put("authorization", lag+"/authorizations/to-team-b", `{"properties":{}}`, http.StatusCreated)
	if p, _ := r["properties"].(map[string]any); p["authorizationUseStatus"] != "Available" {
		t.Fatalf("a new LAG authorization is not Available: %v", r)
	}
	put("LAG again", lag, strings.Replace(body, "%s", "", 1), http.StatusOK)
	if !a.HasResource(lag + "/authorizations/to-team-b") {
		t.Fatal("a LAG PUT deleted its authorization")
	}
}
