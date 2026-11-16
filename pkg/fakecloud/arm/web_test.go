// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"strings"
	"testing"
)

// An app's app settings and connection strings: PUT replaces the set, POST
// .../list reads it, no GET or DELETE, the app must exist, a connection
// string's type is checked, and the sections go with the app.
func TestWebConfigSections(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/web"
	a.AddResource(rg)
	site := rg + "/providers/Microsoft.Web/sites/app"
	settings := site + "/config/appsettings"
	strs := site + "/config/connectionstrings"

	code, resp := do(t, a, http.MethodPut, settings, `{"properties":{"A":"1"}}`)
	expect(t, "settings of a missing app", code, resp, http.StatusNotFound)
	a.AddResource(site)

	code, resp = do(t, a, http.MethodPost, settings+"/list", ``)
	expect(t, "list before any PUT", code, resp, http.StatusOK)
	if !strings.Contains(resp, `"properties":{}`) {
		t.Fatalf("a new app lists an empty set: %s", resp)
	}
	code, resp = do(t, a, http.MethodPut, settings, `{"properties":{"A":"1","B":"2"}}`)
	expect(t, "put settings", code, resp, http.StatusOK)
	code, resp = do(t, a, http.MethodPut, settings, `{"properties":{"B":"3"}}`)
	expect(t, "replace settings", code, resp, http.StatusOK)
	code, resp = do(t, a, http.MethodPost, settings+"/list", ``)
	expect(t, "list settings", code, resp, http.StatusOK)
	if !strings.Contains(resp, `"properties":{"B":"3"}`) || !strings.Contains(resp, `"type":"Microsoft.Web/sites/config"`) {
		t.Fatalf("the PUT replaces the whole set: %s", resp)
	}
	code, resp = do(t, a, http.MethodPut, settings, `{"properties":{"A":1}}`)
	expect(t, "a setting that is not a string", code, resp, http.StatusBadRequest)
	for _, m := range []string{http.MethodGet, http.MethodDelete} {
		code, resp = do(t, a, m, settings, ``)
		expect(t, m+" of a section", code, resp, http.StatusMethodNotAllowed)
	}

	code, resp = do(t, a, http.MethodPut, strs, `{"properties":{"db":{"value":"Server=x","type":"SQLAzure"}}}`)
	expect(t, "put connection strings", code, resp, http.StatusOK)
	code, resp = do(t, a, http.MethodPut, strs, `{"properties":{"db":{"value":"Server=x","type":"Oracle"}}}`)
	expect(t, "an unknown type", code, resp, http.StatusBadRequest)
	code, resp = do(t, a, http.MethodPut, strs, `{"properties":{"db":{"type":"Custom"}}}`)
	expect(t, "no value", code, resp, http.StatusBadRequest)
	code, resp = do(t, a, http.MethodPost, strs+"/list", ``)
	expect(t, "list connection strings", code, resp, http.StatusOK)
	if !strings.Contains(resp, `"db":{"type":"SQLAzure","value":"Server=x"}`) {
		t.Fatalf("connection strings: %s", resp)
	}

	slot := site + "/slots/staging"
	a.AddResource(slot)
	code, resp = do(t, a, http.MethodPut, slot+"/config/appsettings", `{"properties":{"S":"1"}}`)
	expect(t, "a slot's settings", code, resp, http.StatusOK)
	if !strings.Contains(resp, `"type":"Microsoft.Web/sites/slots/config"`) {
		t.Fatalf("a slot's section: %s", resp)
	}

	code, resp = do(t, a, http.MethodDelete, site, ``)
	expect(t, "delete the app", code, resp, http.StatusOK)
	if a.HasResource(settings) || a.HasResource(slot+"/config/appsettings") {
		t.Fatal("the sections outlived the app")
	}
}
