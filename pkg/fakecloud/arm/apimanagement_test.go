// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// An API Management operation declares its template parameters, its policy
// is <policies> XML, a tag link names a tag that exists, an identity
// provider is named by its type, and each kind sends what the spec requires.
func TestAPIManagement(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/apis"
	svc := rg + "/providers/Microsoft.ApiManagement/service/gateway"
	api := svc + "/apis/orders"
	product := svc + "/products/starter"
	redis := rg + "/providers/Microsoft.Cache/redis/apim-cache"
	for _, id := range []string{rg, svc, api, product, redis} {
		a.AddResource(id)
	}
	put := func(what, id, props string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, id, `{"properties":`+props+`}`)
		expect(t, what, code, resp, want)
	}
	op := api + "/operations/get-order"
	put("an operation with no method", op, `{"displayName":"Get order","urlTemplate":"/orders"}`, http.StatusBadRequest)
	put("an undeclared template parameter", op, `{"displayName":"Get order","method":"GET","urlTemplate":"/orders/{id}"}`, http.StatusBadRequest)
	put("an operation", op, `{"displayName":"Get order","method":"GET","urlTemplate":"/orders/{id}","templateParameters":[{"name":"id","type":"string","required":true}]}`, http.StatusCreated)
	put("a policy that is no XML", op+"/policies/policy", `{"value":"rate-limit 10","format":"xml"}`, http.StatusBadRequest)
	put("a policy of another root", op+"/policies/policy", `{"value":"<inbound/>","format":"xml"}`, http.StatusBadRequest)
	put("a policy", op+"/policies/policy", `{"value":"<policies><inbound><base /></inbound><backend><base /></backend><outbound><base /></outbound></policies>"}`, http.StatusCreated)
	put("a policy in another format", op+"/policies/policy", `{"value":"anything","format":"xml-link"}`, http.StatusOK)

	put("a tag with no display name", svc+"/tags/public", `{}`, http.StatusBadRequest)
	put("a link to a missing tag", api+"/tags/public", `{}`, http.StatusNotFound)
	put("a tag", svc+"/tags/public", `{"displayName":"Public"}`, http.StatusCreated)
	put("an API's tag", api+"/tags/public", `{}`, http.StatusCreated)
	put("a product's tag", product+"/tags/public", `{}`, http.StatusCreated)
	put("an operation's tag", op+"/tags/public", `{}`, http.StatusCreated)

	put("a schema with no content type", api+"/schemas/order", `{"document":{"value":"{}"}}`, http.StatusBadRequest)
	put("a schema", api+"/schemas/order", `{"contentType":"application/vnd.oai.openapi.components+json","document":{"components":{}}}`, http.StatusCreated)
	put("an identity provider of no known type", svc+"/identityProviders/github", `{"clientId":"c","clientSecret":"s"}`, http.StatusBadRequest)
	put("an identity provider with no secret", svc+"/identityProviders/aad", `{"clientId":"c"}`, http.StatusBadRequest)
	put("an identity provider", svc+"/identityProviders/aad", `{"type":"aad","clientId":"c","clientSecret":"s","allowedTenants":["contoso.example"]}`, http.StatusCreated)
	put("an OpenID provider with no metadata endpoint", svc+"/openidConnectProviders/corp", `{"displayName":"Corp","clientId":"c"}`, http.StatusBadRequest)
	put("an authorization server with no grant types", svc+"/authorizationServers/corp", `{"displayName":"Corp","clientRegistrationEndpoint":"https://idp.example/register","authorizationEndpoint":"https://idp.example/authorize","clientId":"c"}`, http.StatusBadRequest)
	put("an authorization server", svc+"/authorizationServers/corp", `{"displayName":"Corp","clientRegistrationEndpoint":"https://idp.example/register","authorizationEndpoint":"https://idp.example/authorize","grantTypes":["authorizationCode"],"clientId":"c"}`, http.StatusCreated)
	put("a cache naming a missing Redis", svc+"/caches/default", `{"connectionString":"x","useFromLocation":"default","resourceId":"`+redis+`x"}`, http.StatusNotFound)
	put("a cache", svc+"/caches/default", `{"connectionString":"x","useFromLocation":"default","resourceId":"`+redis+`"}`, http.StatusCreated)
}

// The second part (docs/plan.md f71): a resolver's path is Type/field and
// its policy <policies> XML; a link names what it links, in its service,
// that exists; a release belongs to its API; a wiki's documents exist; a
// v2 gateway has a SKU, and a configuration connection is a workspace
// gateway's and names a workspace that exists.
func TestAPIManagementLinksAndGateways(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/apis"
	svc := rg + "/providers/Microsoft.ApiManagement/service/gateway"
	other := rg + "/providers/Microsoft.ApiManagement/service/other"
	api := svc + "/apis/orders"
	product := svc + "/products/starter"
	group := svc + "/groups/developers"
	tag := svc + "/tags/public"
	workspace := svc + "/workspaces/team"
	subnet := rg + "/providers/Microsoft.Network/virtualNetworks/vnet/subnets/gateway"
	for _, id := range []string{rg, svc, other, other + "/apis/orders", api, api + "/operations/get", product, group, tag, workspace, subnet} {
		a.AddResource(id)
	}
	put := func(what, id, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, id, body)
		expect(t, what, code, resp, want)
	}
	props := func(p string) string { return `{"properties":` + p + `}` }

	put("a resolver with no path", api+"/resolvers/order", props(`{"displayName":"Order"}`), http.StatusBadRequest)
	put("a resolver whose path is not Type/field", api+"/resolvers/order", props(`{"displayName":"Order","path":"Query"}`), http.StatusBadRequest)
	put("a resolver", api+"/resolvers/order", props(`{"displayName":"Order","path":"Query/order"}`), http.StatusCreated)
	put("a resolver's policy of another root", api+"/resolvers/order/policies/policy", props(`{"value":"<http-data-source/>"}`), http.StatusBadRequest)
	put("a resolver's policy", api+"/resolvers/order/policies/policy", props(`{"value":"<policies><inbound><base /></inbound></policies>"}`), http.StatusCreated)

	put("a tag's link with no API", tag+"/apiLinks/orders", props(`{}`), http.StatusBadRequest)
	put("a tag's link to another service's API", tag+"/apiLinks/orders", props(`{"apiId":"`+other+`/apis/orders"}`), http.StatusBadRequest)
	put("a tag's link to an operation as an API", tag+"/apiLinks/orders", props(`{"apiId":"`+api+`/operations/get"}`), http.StatusBadRequest)
	put("a tag's link to a missing API", tag+"/apiLinks/orders", props(`{"apiId":"`+svc+`/apis/missing"}`), http.StatusNotFound)
	put("a tag's API link", tag+"/apiLinks/orders", props(`{"apiId":"`+api+`"}`), http.StatusCreated)
	put("a tag's operation link", tag+"/operationLinks/get", props(`{"operationId":"`+api+`/operations/get"}`), http.StatusCreated)
	put("a tag's product link", tag+"/productLinks/starter", props(`{"productId":"`+product+`"}`), http.StatusCreated)
	put("a link from a missing tag", svc+"/tags/missing/apiLinks/orders", props(`{"apiId":"`+api+`"}`), http.StatusNotFound)
	put("a product's API link", product+"/apiLinks/orders", props(`{"apiId":"`+api+`"}`), http.StatusCreated)
	put("a product's group link to a missing group", product+"/groupLinks/admins", props(`{"groupId":"`+svc+`/groups/admins"}`), http.StatusNotFound)
	put("a product's group link", product+"/groupLinks/developers", props(`{"groupId":"`+group+`"}`), http.StatusCreated)

	put("a release of another API", api+"/releases/v1", props(`{"apiId":"`+other+`/apis/orders","notes":"first"}`), http.StatusBadRequest)
	put("a release", api+"/releases/v1", props(`{"apiId":"`+api+`","notes":"first"}`), http.StatusCreated)
	put("a wiki naming a missing documentation", api+"/wikis/default", props(`{"documents":[{"documentationId":"intro"}]}`), http.StatusNotFound)
	put("a documentation", svc+"/documentations/intro", props(`{"title":"Introduction","content":"# Orders"}`), http.StatusCreated)
	put("a wiki", api+"/wikis/default", props(`{"documents":[{"documentationId":"intro"}]}`), http.StatusCreated)
	put("a product's wiki", product+"/wikis/default", props(`{"documents":[{"documentationId":"intro"}]}`), http.StatusCreated)

	gws := rg + "/providers/Microsoft.ApiManagement/gateways/"
	put("a gateway with no SKU", gws+"edge", `{"location":"westus","properties":{}}`, http.StatusBadRequest)
	put("a gateway in a missing subnet", gws+"edge", `{"location":"westus","sku":{"name":"WorkspaceGatewayPremium","capacity":1},"properties":{"backend":{"subnet":{"id":"`+subnet+`x"}}}}`, http.StatusNotFound)
	put("a workspace gateway", gws+"edge", `{"location":"westus","sku":{"name":"WorkspaceGatewayPremium","capacity":1},"properties":{"backend":{"subnet":{"id":"`+subnet+`"}}}}`, http.StatusCreated)
	put("a Standard gateway", gws+"standard", `{"location":"westus","sku":{"name":"Standard","capacity":1},"properties":{}}`, http.StatusCreated)
	put("a Standard gateway's connection", gws+"standard/configConnections/team", props(`{"sourceId":"`+workspace+`"}`), http.StatusBadRequest)
	put("a connection to no workspace", gws+"edge/configConnections/team", props(`{"sourceId":"`+svc+`"}`), http.StatusBadRequest)
	put("a connection to a missing workspace", gws+"edge/configConnections/team", props(`{"sourceId":"`+svc+`/workspaces/missing"}`), http.StatusNotFound)
	put("a connection", gws+"edge/configConnections/team", props(`{"sourceId":"`+workspace+`"}`), http.StatusCreated)
}
