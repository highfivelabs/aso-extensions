// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// An App Service app's (or slot's) app settings and connection strings
// (Microsoft.Web sites/config/appsettings, .../connectionstrings, the same
// under sites/slots), as the 2026-07-15 spec describes them: a PUT replaces
// the whole set, a POST .../list reads it back (locks are not applied to
// either), and there is neither a GET
// nor a DELETE (405 here; the spec has none). The app must exist. An app
// whose section was never PUT lists an empty set, as Azure's does for a new
// app. A connection string's type must be one the spec names. The section is
// kept under the app and goes when the app is deleted.
//
// Not modelled: the settings a Site's (or slot's) PUT carries in
// siteConfig.appSettings and siteConfig.connectionStrings, which Azure
// writes to the same sections; here the Site keeps its body and the
// sections are separate.
var webConfigPath = regexp.MustCompile(`(?i)^(/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.Web/sites/[^/]+(?:/slots/[^/]+)?)/config/(appsettings|connectionstrings)(/list)?$`)

// connectionStringTypes are ConnectionStringType's values (2026-07-15).
var connectionStringTypes = map[string]bool{
	"MySql": true, "SQLServer": true, "SQLAzure": true, "Custom": true, "NotificationHub": true,
	"ServiceBus": true, "EventHub": true, "ApiHub": true, "DocDb": true, "RedisCache": true, "PostgreSQL": true,
}

// serveWebConfig serves the sections; false when path is none of them.
func (a *ARM) serveWebConfig(w http.ResponseWriter, r *http.Request, path string) bool {
	m := webConfigPath.FindStringSubmatch(path)
	if m == nil {
		return false
	}
	app, section, list := m[1], strings.ToLower(m[2]), m[3] != ""
	id := app + "/config/" + section
	key := strings.ToLower(id)
	if !a.exists(app) {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "The resource "+app+" was not found.")
		return true
	}
	switch {
	case list && r.Method == http.MethodPost:
		props := map[string]any{}
		if res, ok := a.resources[key]; ok {
			props, _ = (*res)["properties"].(map[string]any)
		}
		writeJSON(w, http.StatusOK, webConfigBody(id, props))
	case !list && r.Method == http.MethodPut:
		var body struct {
			Properties map[string]any `json:"properties"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
			return true
		}
		if body.Properties == nil {
			body.Properties = map[string]any{}
		}
		if msg := checkWebConfig(section, body.Properties); msg != "" {
			writeError(w, http.StatusBadRequest, "BadRequest", msg)
			return true
		}
		res := resource(webConfigBody(id, body.Properties))
		a.resources[key] = &res
		writeJSON(w, http.StatusOK, res)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed",
			"The requested resource does not support http method '"+r.Method+"'.")
	}
	return true
}

// checkWebConfig is "" when the section's properties are what the spec
// allows: strings for app settings, a value and a known type for each
// connection string.
func checkWebConfig(section string, props map[string]any) string {
	for name, v := range props {
		if section == "appsettings" {
			if _, ok := v.(string); !ok {
				return "The value of app setting " + name + " must be a string."
			}
			continue
		}
		cs, ok := v.(map[string]any)
		if !ok {
			return "Connection string " + name + " must be an object with a value and a type."
		}
		if _, ok := cs["value"].(string); !ok {
			return "Connection string " + name + " has no value."
		}
		if t, _ := cs["type"].(string); !connectionStringTypes[t] {
			return "Connection string " + name + " has an unknown type."
		}
	}
	return ""
}

func webConfigBody(id string, props map[string]any) map[string]any {
	return map[string]any{
		"id":         id,
		"name":       lastSegment(id),
		"type":       webConfigType(id),
		"properties": props,
	}
}

// webConfigType is the section's type: a slot's sections are
// Microsoft.Web/sites/slots/config.
func webConfigType(id string) string {
	if strings.Contains(strings.ToLower(id), "/slots/") {
		return "Microsoft.Web/sites/slots/config"
	}
	return "Microsoft.Web/sites/config"
}
