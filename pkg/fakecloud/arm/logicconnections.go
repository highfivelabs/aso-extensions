// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Logic Apps API connections, custom APIs and on-premises data gateways
// (Microsoft.Web/connections, customApis, connectionGateways, 2016-06-01;
// docs/plan.md f56), as far as the fake models them:
//
//   - a connection names the API it uses (properties.api.id; 400); a managed
//     API (.../providers/Microsoft.Web/locations/{l}/managedApis/{api}) is in
//     the connection's region, as the connection and its API are made
//     together in a region (400); a custom API exists (404);
//   - a connection's parameterValues and customParameterValues (its
//     credentials) are kept aside under the connection's ID and
//     "/parameterValues/<name>" or "/customParameterValues/<name>"
//     (SharedKey) and never returned, as Azure does not return them;
//   - a custom API carries its definition: an OpenAPI document (swagger),
//     an OpenAPI URL (apiDefinitions.originalSwaggerUrl) or a WSDL
//     (wsdlDefinition url or content) (400); an OAuth connection
//     parameter's client secret is kept aside under the API's ID and
//     "/connectionParameters/<name>/clientSecret" and never returned;
//   - a connection gateway names its gateway installation
//     (connectionGatewayInstallation.id; 400) that exists (404).
//
// Not modelled: consent and OAuth sign-in (a connection's status, its
// consent links), connections testing their target, the managed APIs'
// catalogue (any name is accepted), the gateway's machine, and the error
// codes (the fake's own).
var (
	logicConnection = regexp.MustCompile(`(?i)/providers/microsoft\.web/connections/[^/]+$`)
	logicCustomAPI  = regexp.MustCompile(`(?i)/providers/microsoft\.web/customapis/[^/]+$`)
	logicGateway    = regexp.MustCompile(`(?i)/providers/microsoft\.web/connectiongateways/[^/]+$`)
	logicManagedAPI = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/providers/microsoft\.web/locations/([^/]+)/managedapis/[^/]+$`)
)

// checkLogicConnections refuses what the rules above do not allow and keeps
// the secrets aside; code is "" when it is allowed or id is none of these.
func (a *ARM) checkLogicConnections(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	switch {
	case logicConnection.MatchString(id):
		api, _ := props["api"].(map[string]any)
		apiID := str(api, "id")
		if apiID == "" {
			return http.StatusBadRequest, "ApiMissing", "A connection names the API it uses (properties.api.id)."
		}
		if m := logicManagedAPI.FindStringSubmatch(apiID); m != nil {
			if loc := str(body, "location"); !strings.EqualFold(normalizeLocation(loc), normalizeLocation(m[1])) {
				return http.StatusBadRequest, "ApiLocationMismatch", "The managed API " + apiID + " is not in the connection's region, " + loc + "."
			}
		} else if !a.exists(apiID) {
			return http.StatusNotFound, "ResourceNotFound", "The API " + apiID + " was not found."
		}
		for _, field := range []string{"parameterValues", "customParameterValues"} {
			values, _ := props[field].(map[string]any)
			for name := range values {
				a.keep(id+"/"+field+"/"+name, values, name)
			}
			delete(props, field)
		}
	case logicCustomAPI.MatchString(id):
		defs, _ := props["apiDefinitions"].(map[string]any)
		wsdl, _ := props["wsdlDefinition"].(map[string]any)
		swagger, _ := props["swagger"].(map[string]any)
		if len(swagger) == 0 && str(defs, "originalSwaggerUrl") == "" && str(wsdl, "url") == "" && str(wsdl, "content") == "" {
			return http.StatusBadRequest, "ApiDefinitionMissing", "A custom API carries its definition: swagger, apiDefinitions.originalSwaggerUrl or a wsdlDefinition."
		}
		params, _ := props["connectionParameters"].(map[string]any)
		for name, p := range params {
			pm, _ := p.(map[string]any)
			oauth, _ := pm["oAuthSettings"].(map[string]any)
			a.keep(id+"/connectionParameters/"+name+"/clientSecret", oauth, "clientSecret")
		}
	case logicGateway.MatchString(id):
		inst, _ := props["connectionGatewayInstallation"].(map[string]any)
		instID := str(inst, "id")
		if instID == "" {
			return http.StatusBadRequest, "GatewayInstallationMissing", "A connection gateway names its gateway installation (properties.connectionGatewayInstallation.id)."
		}
		if !a.exists(instID) {
			return http.StatusNotFound, "ResourceNotFound", "The gateway installation " + instID + " was not found."
		}
	}
	return 0, "", ""
}

// normalizeLocation folds a region's display name ("West US") into its name
// ("westus").
func normalizeLocation(l string) string {
	return strings.ToLower(strings.ReplaceAll(l, " ", ""))
}
