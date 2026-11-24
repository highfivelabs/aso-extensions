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
