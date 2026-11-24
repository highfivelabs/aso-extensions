// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"encoding/xml"
	"net/http"
	"regexp"
	"slices"
	"strings"
)

// The API Management children of docs/plan.md f64 (Microsoft.ApiManagement,
// 2024-05-01), as far as the fake models them. Each rule is one the spec or
// API Management's documentation states:
//
//   - each kind sends what the spec requires of it (400): an operation its
//     displayName, method and urlTemplate; a tag its displayName; an API
//     schema its contentType; a global schema its schemaType; an identity
//     provider its clientId and clientSecret; an OpenID Connect provider
//     its displayName, metadataEndpoint and clientId; an authorization
//     server its displayName, clientRegistrationEndpoint,
//     authorizationEndpoint, grantTypes and clientId; a cache its
//     connectionString and useFromLocation;
//   - every {parameter} of an operation's urlTemplate is one of its
//     templateParameters (400);
//   - an operation's policy is XML whose root is <policies> when its format
//     is xml or rawxml, the default (400);
//   - an API's, a product's or an operation's tag names a tag of the
//     service that exists (404);
//   - an identity provider's name is its type: aad, aadB2C, facebook,
//     google, microsoft or twitter (400);
//   - a cache's resourceId, when given, exists (404).
//
// Not modelled: the gateway running the APIs, policy semantics, the
// developer portal, an identity provider's or authorization server's
// endpoints answering, and the error codes (the fake's own).
var (
	apimChild     = regexp.MustCompile(`(?i)^(/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.apimanagement/service/[^/]+)/(.+)$`)
	apimTemplate  = regexp.MustCompile(`\{([^}]+)\}`)
	apimProviders = []string{"aad", "aadB2C", "facebook", "google", "microsoft", "twitter"}
	apimRequired  = []struct {
		path  *regexp.Regexp
		props []string
	}{
		{regexp.MustCompile(`(?i)^apis/[^/]+/operations/[^/]+$`), []string{"displayName", "method", "urlTemplate"}},
		{regexp.MustCompile(`(?i)^tags/[^/]+$`), []string{"displayName"}},
		{regexp.MustCompile(`(?i)^apis/[^/]+/schemas/[^/]+$`), []string{"contentType"}},
		{regexp.MustCompile(`(?i)^schemas/[^/]+$`), []string{"schemaType"}},
		{regexp.MustCompile(`(?i)^identityproviders/[^/]+$`), []string{"clientId", "clientSecret"}},
		{regexp.MustCompile(`(?i)^openidconnectproviders/[^/]+$`), []string{"displayName", "metadataEndpoint", "clientId"}},
		{regexp.MustCompile(`(?i)^authorizationservers/[^/]+$`), []string{"displayName", "clientRegistrationEndpoint", "authorizationEndpoint", "grantTypes", "clientId"}},
		{regexp.MustCompile(`(?i)^caches/[^/]+$`), []string{"connectionString", "useFromLocation"}},
	}
	apimTagLink  = regexp.MustCompile(`(?i)^(apis/[^/]+|products/[^/]+|apis/[^/]+/operations/[^/]+)/tags/([^/]+)$`)
	apimOpPolicy = regexp.MustCompile(`(?i)^apis/[^/]+/operations/[^/]+/policies/[^/]+$`)
	apimOp       = regexp.MustCompile(`(?i)^apis/[^/]+/operations/[^/]+$`)
	apimIdP      = regexp.MustCompile(`(?i)^identityproviders/([^/]+)$`)
	apimCache    = regexp.MustCompile(`(?i)^caches/[^/]+$`)
)

// checkAPIManagement refuses what the rules above do not allow; code is ""
// when it is allowed or id is none of these.
func (a *ARM) checkAPIManagement(id string, body map[string]any) (status int, code, message string) {
	m := apimChild.FindStringSubmatch(id)
	if m == nil {
		return 0, "", ""
	}
	service, rest := m[1], m[2]
	props, _ := body["properties"].(map[string]any)
	bad := func(msg string) (int, string, string) {
		return http.StatusBadRequest, "ValidationError", msg
	}
	for _, r := range apimRequired {
		if !r.path.MatchString(rest) {
			continue
		}
		for _, p := range r.props {
			if v, ok := props[p]; !ok || v == "" || v == nil {
				return bad("'" + p + "' is required.")
			}
		}
	}
	switch {
	case apimOp.MatchString(rest):
		declared := map[string]bool{}
		params, _ := props["templateParameters"].([]any)
		for _, p := range params {
			pm, _ := p.(map[string]any)
			declared[strings.ToLower(stringOf(pm["name"]))] = true
		}
		for _, t := range apimTemplate.FindAllStringSubmatch(stringOf(props["urlTemplate"]), -1) {
			if !declared[strings.ToLower(t[1])] {
				return bad("The template parameter " + t[1] + " of the urlTemplate is not one of templateParameters.")
			}
		}
	case apimOpPolicy.MatchString(rest):
		value := stringOf(props["value"])
		if value == "" {
			return bad("'value' is required.")
		}
		if f := stringOf(props["format"]); f == "" || f == "xml" || f == "rawxml" {
			var root struct{ XMLName xml.Name }
			if err := xml.Unmarshal([]byte(value), &root); err != nil || root.XMLName.Local != "policies" {
				return bad("A policy is XML whose root element is <policies>.")
			}
		}
	case apimIdP.MatchString(rest):
		name := apimIdP.FindStringSubmatch(rest)[1]
		if !slices.Contains(apimProviders, name) {
			return bad("An identity provider's name is its type, one of " + strings.Join(apimProviders, ", ") + "; not " + name + ".")
		}
	case apimCache.MatchString(rest):
		if r := stringOf(props["resourceId"]); r != "" && !a.exists(r) {
			return http.StatusNotFound, "ResourceNotFound", "The cache " + r + " was not found."
		}
	}
	if t := apimTagLink.FindStringSubmatch(rest); t != nil {
		if tag := service + "/tags/" + t[2]; !a.exists(tag) {
			return http.StatusNotFound, "ResourceNotFound", "The tag " + tag + " was not found."
		}
	}
	return 0, "", ""
}
