// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// An app's or slot's sidecar containers give an image and isMain, and an
// app has one main container; private access and a slot's network
// connection name networks that exist.
func TestSiteChildren(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/web"
	site := rg + "/providers/Microsoft.Web/sites/app"
	slot := site + "/slots/staging"
	vnet := rg + "/providers/Microsoft.Network/virtualNetworks/net"
	for _, id := range []string{rg, site, slot} {
		a.AddResource(id)
	}
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	// The slot's containers are not the app's: the app's main container does
	// not count against the slot's.
	for _, app := range []string{site, slot} {
		put("a container with no image", app+"/sitecontainers/main", `{"properties":{"isMain":true}}`, http.StatusBadRequest)
		put("a container with no isMain", app+"/sitecontainers/main", `{"properties":{"image":"nginx"}}`, http.StatusBadRequest)
		put("the main container", app+"/sitecontainers/main", `{"properties":{"image":"nginx","isMain":true,"targetPort":"80"}}`, http.StatusCreated)
		put("the main container again", app+"/sitecontainers/main", `{"properties":{"image":"nginx:1.27","isMain":true,"targetPort":"80"}}`, http.StatusOK)
		put("a sidecar", app+"/sitecontainers/otel", `{"properties":{"image":"otel/collector","isMain":false}}`, http.StatusCreated)
		put("a second main container", app+"/sitecontainers/other", `{"properties":{"image":"httpd","isMain":true}}`, http.StatusConflict)
		put("private access to a network that is not there", app+"/privateAccess/virtualNetworks",
			`{"properties":{"enabled":true,"virtualNetworks":[{"resourceId":"`+vnet+`"}]}}`, http.StatusNotFound)
	}
	a.AddResource(vnet)
	put("private access", site+"/privateAccess/virtualNetworks", `{"properties":{"enabled":true,"virtualNetworks":[{"resourceId":"`+vnet+`"}]}}`, http.StatusCreated)
	put("a slot's connection to a network that is not there", slot+"/virtualNetworkConnections/other",
		`{"properties":{"vnetResourceId":"`+rg+`/providers/Microsoft.Network/virtualNetworks/other"}}`, http.StatusNotFound)
	put("a slot's connection", slot+"/virtualNetworkConnections/net", `{"properties":{"vnetResourceId":"`+vnet+`"}}`, http.StatusCreated)
}
