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
// And of the second part, docs/plan.md f71:
//
//   - a GraphQL resolver sends its displayName and path, and its path is
//     Type/field (400); its policy is <policies> XML as an operation's;
//   - a link made from a tag's side (apiLinks, operationLinks,
//     productLinks) or a product's (apiLinks, groupLinks) names its API,
//     operation, product or group (400), of the same service (400), that
//     exists (404);
//   - a release's apiId, when given, is the API it belongs to (400);
//   - a wiki's documents name documentations of the service that exist
//     (404);
//   - a v2 gateway (Microsoft.ApiManagement/gateways) has a SKU (400) and
//     its backend subnet, when given, exists (404); a configuration
//     connection is a workspace gateway's (400) and names a workspace that
//     exists (404).
//
// And of the third part, docs/plan.md f72:
//
//   - a workspace sends its displayName (400), on a service of the Premium
//     or PremiumV2 tier when the fake holds the service's SKU (400; a
//     service made outside the fake, as ASO's stand-ins are, is not
//     checked);
//   - a workspace's children follow the rules above, scoped to the
//     workspace: a tag link names a tag of the workspace, a link what is
//     in the workspace, a release the workspace's API.
//
// Not modelled: the gateway running the APIs, policy semantics, resolvers
// being called, a workspace's APIs answering only through a workspace
// gateway, a workspace's own role assignments, the developer portal, an identity provider's or
// authorization server's endpoints answering, and the error codes (the
// fake's own).
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
		{regexp.MustCompile(`(?i)^apis/[^/]+/resolvers/[^/]+$`), []string{"displayName", "path"}},
		{regexp.MustCompile(`(?i)^tags/[^/]+/apilinks/[^/]+$`), []string{"apiId"}},
		{regexp.MustCompile(`(?i)^tags/[^/]+/operationlinks/[^/]+$`), []string{"operationId"}},
		{regexp.MustCompile(`(?i)^tags/[^/]+/productlinks/[^/]+$`), []string{"productId"}},
		{regexp.MustCompile(`(?i)^products/[^/]+/apilinks/[^/]+$`), []string{"apiId"}},
		{regexp.MustCompile(`(?i)^products/[^/]+/grouplinks/[^/]+$`), []string{"groupId"}},
	}
	// The property of each link kind that names what it links, and the
	// path, under the service, of what it may name.
	apimLinks = []struct {
		path   *regexp.Regexp
		prop   string
		target *regexp.Regexp
	}{
		{regexp.MustCompile(`(?i)^(tags|products)/[^/]+/apilinks/[^/]+$`), "apiId", regexp.MustCompile(`(?i)^apis/[^/]+$`)},
		{regexp.MustCompile(`(?i)^tags/[^/]+/operationlinks/[^/]+$`), "operationId", regexp.MustCompile(`(?i)^apis/[^/]+/operations/[^/]+$`)},
		{regexp.MustCompile(`(?i)^tags/[^/]+/productlinks/[^/]+$`), "productId", regexp.MustCompile(`(?i)^products/[^/]+$`)},
		{regexp.MustCompile(`(?i)^products/[^/]+/grouplinks/[^/]+$`), "groupId", regexp.MustCompile(`(?i)^groups/[^/]+$`)},
	}
	apimResolver     = regexp.MustCompile(`(?i)^apis/[^/]+/resolvers/[^/]+$`)
	apimResolverPath = regexp.MustCompile(`^[^/]+/[^/]+$`)
	apimRelease      = regexp.MustCompile(`(?i)^(apis/[^/]+)/releases/[^/]+$`)
	apimWiki         = regexp.MustCompile(`(?i)^(apis|products)/[^/]+/wikis/default$`)
	apimGateway      = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.apimanagement/gateways/[^/]+$`)
	apimConfigConn   = regexp.MustCompile(`(?i)^(/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.apimanagement/gateways/[^/]+)/configconnections/[^/]+$`)
	apimWorkspace    = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.apimanagement/service/[^/]+/workspaces/[^/]+$`)
	apimWorkspaceGW  = []string{"WorkspaceGatewayStandard", "WorkspaceGatewayPremium"}
	apimInWorkspace  = regexp.MustCompile(`(?i)^(workspaces/[^/]+)(/.*)?$`)
	// The tiers that take workspaces (API Management's documentation,
	// "Workspaces in Azure API Management").
	apimWorkspaceTiers = []string{"Premium", "PremiumV2"}
	apimTagLink        = regexp.MustCompile(`(?i)^(apis/[^/]+|products/[^/]+|apis/[^/]+/operations/[^/]+)/tags/([^/]+)$`)
	apimOpPolicy       = regexp.MustCompile(`(?i)^apis/[^/]+/(operations|resolvers)/[^/]+/policies/[^/]+$`)
	apimOp             = regexp.MustCompile(`(?i)^apis/[^/]+/operations/[^/]+$`)
	apimIdP            = regexp.MustCompile(`(?i)^identityproviders/([^/]+)$`)
	apimCache          = regexp.MustCompile(`(?i)^caches/[^/]+$`)
)

// checkAPIManagement refuses what the rules above do not allow; code is ""
// when it is allowed or id is none of these.
func (a *ARM) checkAPIManagement(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	bad := func(msg string) (int, string, string) {
		return http.StatusBadRequest, "ValidationError", msg
	}
	if apimGateway.MatchString(id) {
		sku, _ := body["sku"].(map[string]any)
		if stringOf(sku["name"]) == "" {
			return bad("'sku' is required.")
		}
		backend, _ := props["backend"].(map[string]any)
		subnet, _ := backend["subnet"].(map[string]any)
		if s := stringOf(subnet["id"]); s != "" && !a.exists(s) {
			return http.StatusNotFound, "ResourceNotFound", "The subnet " + s + " was not found."
		}
		return 0, "", ""
	}
	if c := apimConfigConn.FindStringSubmatch(id); c != nil {
		gw, _ := a.resources[strings.ToLower(c[1])]
		if gw != nil {
			sku, _ := (*gw)["sku"].(map[string]any)
			if name := stringOf(sku["name"]); !slices.Contains(apimWorkspaceGW, name) {
				return bad("A configuration connection is a workspace gateway's (" + strings.Join(apimWorkspaceGW, " or ") + "), not a " + name + " gateway's.")
			}
		}
		src := stringOf(props["sourceId"])
		if !apimWorkspace.MatchString(src) {
			return bad("'sourceId' is the ARM ID of an API Management workspace.")
		}
		if !a.exists(src) {
			return http.StatusNotFound, "ResourceNotFound", "The workspace " + src + " was not found."
		}
		return 0, "", ""
	}
	m := apimChild.FindStringSubmatch(id)
	if m == nil {
		return 0, "", ""
	}
	service, rest := m[1], m[2]
	// A workspace's children are the service's contracts, scoped to it:
	// what they link and name is the workspace's.
	scope := service
	if w := apimInWorkspace.FindStringSubmatch(rest); w != nil {
		if w[2] == "" {
			if stringOf(props["displayName"]) == "" {
				return bad("'displayName' is required.")
			}
			if svc := a.resources[strings.ToLower(service)]; svc != nil {
				sku, _ := (*svc)["sku"].(map[string]any)
				if name := stringOf(sku["name"]); name != "" && !slices.Contains(apimWorkspaceTiers, name) {
					return bad("Workspaces are available in the " + strings.Join(apimWorkspaceTiers, " and ") + " tiers, not " + name + ".")
				}
			}
			return 0, "", ""
		}
		scope, rest = service+"/"+w[1], w[2][1:]
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
	case apimResolver.MatchString(rest):
		if !apimResolverPath.MatchString(stringOf(props["path"])) {
			return bad("A resolver's path is Type/field, not " + stringOf(props["path"]) + ".")
		}
	case apimRelease.MatchString(rest):
		api := scope + "/" + apimRelease.FindStringSubmatch(rest)[1]
		if r := stringOf(props["apiId"]); r != "" && !strings.EqualFold(r, api) {
			return bad("A release's apiId is the API it belongs to, " + api + "; not " + r + ".")
		}
	case apimWiki.MatchString(rest):
		docs, _ := props["documents"].([]any)
		for _, d := range docs {
			dm, _ := d.(map[string]any)
			if doc := service + "/documentations/" + stringOf(dm["documentationId"]); !a.exists(doc) {
				return http.StatusNotFound, "ResourceNotFound", "The documentation " + doc + " was not found."
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
	for _, l := range apimLinks {
		if !l.path.MatchString(rest) {
			continue
		}
		target := stringOf(props[l.prop])
		if !strings.HasPrefix(strings.ToLower(target), strings.ToLower(scope)+"/") || !l.target.MatchString(target[len(scope)+1:]) {
			return bad("'" + l.prop + "' is the ARM ID of what the link names, in the same service (and workspace); not " + target + ".")
		}
		if !a.exists(target) {
			return http.StatusNotFound, "ResourceNotFound", "The resource " + target + " was not found."
		}
	}
	if t := apimTagLink.FindStringSubmatch(rest); t != nil {
		if tag := scope + "/tags/" + t[2]; !a.exists(tag) {
			return http.StatusNotFound, "ResourceNotFound", "The tag " + tag + " was not found."
		}
	}
	return 0, "", ""
}
