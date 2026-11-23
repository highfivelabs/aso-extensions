// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"strings"
	"testing"
)

// A connection names an API, a managed one in its region or a custom one
// that exists, and its parameter values are kept aside, never returned; a
// custom API carries a definition and its OAuth client secret is kept
// aside; a gateway names an installation that exists.
func TestLogicConnections(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/logic"
	install := "/subscriptions/" + sub1 + "/providers/Microsoft.Web/locations/westus/connectionGatewayInstallations/gw-1"
	for _, id := range []string{rg, install} {
		a.AddResource(id)
	}
	put := func(what, path, body string, want int) string {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
		return resp
	}
	managed := func(loc string) string {
		return "/subscriptions/" + sub1 + "/providers/Microsoft.Web/locations/" + loc + "/managedApis/azureblob"
	}
	conn := rg + "/providers/Microsoft.Web/connections/blob"
	put("a connection with no API", conn, `{"location":"westus","properties":{"displayName":"blob"}}`, http.StatusBadRequest)
	put("a managed API in another region", conn, `{"location":"westus","properties":{"api":{"id":"`+managed("eastus")+`"}}}`, http.StatusBadRequest)
	put("a missing custom API", conn, `{"location":"westus","properties":{"api":{"id":"`+rg+`/providers/Microsoft.Web/customApis/none"}}}`, http.StatusNotFound)
	resp := put("a connection", conn, `{"location":"West US","properties":{"api":{"id":"`+managed("westus")+`"},"parameterValues":{"accountName":"store","accessKey":"k3y"},"nonSecretParameterValues":{"region":"westus"}}}`, http.StatusCreated)
	if strings.Contains(resp, "k3y") || strings.Contains(resp, "parameterValues\"") {
		t.Fatalf("the parameter values were returned: %s", resp)
	}
	if k, ok := a.SharedKey(conn + "/parameterValues/accessKey"); !ok || k != "k3y" {
		t.Fatalf("the access key kept aside: %q %v", k, ok)
	}
	if _, get := do(t, a, http.MethodGet, conn, ""); strings.Contains(get, "k3y") || !strings.Contains(get, "nonSecretParameterValues") {
		t.Fatalf("GET: %s", get)
	}

	api := rg + "/providers/Microsoft.Web/customApis/orders"
	put("a custom API with no definition", api, `{"location":"westus","properties":{"displayName":"Orders"}}`, http.StatusBadRequest)
	resp = put("a custom API", api, `{"location":"westus","properties":{"apiDefinitions":{"originalSwaggerUrl":"https://example.com/openapi.json"},"connectionParameters":{"token":{"type":"oauthSetting","oAuthSettings":{"clientId":"c","clientSecret":"s3cret"}}}}}`, http.StatusCreated)
	if strings.Contains(resp, "s3cret") {
		t.Fatalf("the client secret was returned: %s", resp)
	}
	if k, ok := a.SharedKey(api + "/connectionParameters/token/clientSecret"); !ok || k != "s3cret" {
		t.Fatalf("the client secret kept aside: %q %v", k, ok)
	}
	put("a connection to the custom API", rg+"/providers/Microsoft.Web/connections/orders", `{"location":"westus","properties":{"api":{"id":"`+api+`"}}}`, http.StatusCreated)

	gw := rg + "/providers/Microsoft.Web/connectionGateways/onprem"
	put("a gateway with no installation", gw, `{"location":"westus","properties":{"displayName":"on-premises"}}`, http.StatusBadRequest)
	put("a gateway on a missing installation", gw, `{"location":"westus","properties":{"connectionGatewayInstallation":{"id":"`+install+`x"}}}`, http.StatusNotFound)
	put("a gateway", gw, `{"location":"westus","properties":{"connectionGatewayInstallation":{"id":"`+install+`"}}}`, http.StatusCreated)
}
