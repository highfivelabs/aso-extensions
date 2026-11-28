// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// Static Web Apps' links and an app's virtual network connection name what
// exists.
func TestStaticSiteLinks(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/web"
	a.AddResource(rg)
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	site := rg + "/providers/Microsoft.Web/staticSites/docs"
	put("a static site", site, `{"location":"westus2","sku":{"name":"Standard"},"properties":{}}`, http.StatusCreated)
	functions := rg + "/providers/Microsoft.Web/sites/api"
	put("a backend that is not there", site+"/linkedBackends/api", `{"properties":{"backendResourceId":"`+functions+`"}}`, http.StatusNotFound)
	put("a function app that is not there", site+"/userProvidedFunctionApps/api", `{"properties":{"functionAppResourceId":"`+functions+`"}}`, http.StatusNotFound)
	a.AddResource(functions)
	put("a backend", site+"/linkedBackends/api", `{"properties":{"backendResourceId":"`+functions+`"}}`, http.StatusCreated)
	put("a function app", site+"/userProvidedFunctionApps/api", `{"properties":{"functionAppResourceId":"`+functions+`"}}`, http.StatusCreated)
	cosmos := rg + "/providers/Microsoft.DocumentDB/databaseAccounts/data"
	put("a database that is not there", site+"/databaseConnections/default", `{"properties":{"resourceId":"`+cosmos+`","region":"westus2"}}`, http.StatusNotFound)
	vnet := rg + "/providers/Microsoft.Network/virtualNetworks/net"
	put("a network that is not there", functions+"/virtualNetworkConnections/net", `{"properties":{"vnetResourceId":"`+vnet+`"}}`, http.StatusNotFound)
	a.AddResource(vnet)
	put("a network connection", functions+"/virtualNetworkConnections/net", `{"properties":{"vnetResourceId":"`+vnet+`"}}`, http.StatusCreated)
}
