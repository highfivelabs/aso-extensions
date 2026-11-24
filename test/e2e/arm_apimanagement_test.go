// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	asoapim "github.com/Azure/azure-service-operator/v2/api/apimanagement/v20240501/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	apim "github.com/highfivelabs/aso-extensions/apis/arm/apimanagement/v20240501"
)

// The API Management kinds of docs/plan.md f64, on ASO's Service, Api and
// Product by name (new external owners): an API's operation with its policy
// and a tag, the API's tag, schema and tag description, the product's tag,
// the service's tag, Entra ID identity provider and OAuth authorization
// server (their secrets from a Secret). An operation whose urlTemplate
// names an undeclared parameter, a policy that is not <policies> XML and a
// tag link to a missing tag are refused (the fake's rules,
// apimanagement.go); a change is PUT again; deleted, all go.
func TestAPIManagementKinds(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "apim")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	svcID := rgID + "/providers/Microsoft.ApiManagement/service/gateway"
	apiID := svcID + "/apis/orders"
	productID := svcID + "/products/starter"
	for _, id := range []string{rgID, svcID, apiID, productID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	svc := &asoapim.Service{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "gateway"}}
	svc.Spec.AzureName, svc.Spec.Location = "gateway", ptr("westus")
	svc.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, svc, svcID)
	api := &asoapim.Api{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "orders"}}
	api.Spec.AzureName = "orders"
	api.Spec.Owner = &genruntime.KnownResourceReference{Name: "gateway"}
	asoStandIn(t, api, apiID)
	product := &asoapim.Product{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "starter"}}
	product.Spec.AzureName = "starter"
	product.Spec.Owner = &genruntime.KnownResourceReference{Name: "gateway"}
	asoStandIn(t, product, productID)
	if err := k8s.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "idp"},
		StringData: map[string]string{"aad": "aad-client-secret", "oauth": "oauth-client-secret"},
	}); err != nil {
		t.Fatal(err)
	}
	onService := &genruntime.KnownResourceReference{Name: "gateway"}
	onAPI := &genruntime.KnownResourceReference{Name: "orders"}

	// What the links name first: the service's tag and the operation.
	tag := &apim.Tag{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "public"},
		Spec:       apim.Tag_Spec{Owner: onService, DisplayName: ptr("Public")},
	}
	op := &apim.ApiOperation{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "get-order"},
		Spec: apim.ApiOperation_Spec{
			Owner: onAPI, DisplayName: ptr("Get order"), Method: ptr("GET"), UrlTemplate: ptr("/orders/{id}"),
			TemplateParameters: []apim.ParameterContract{{Name: ptr("id"), Type: ptr("string"), Required: ptr(true)}},
		},
	}
	undeclared := &apim.ApiOperation{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cancel-order"},
		Spec: apim.ApiOperation_Spec{
			Owner: onAPI, DisplayName: ptr("Cancel order"), Method: ptr("POST"), UrlTemplate: ptr("/orders/{id}/cancel"),
		},
	}
	for _, o := range []conditioned{tag, op, undeclared} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "the tag Ready", ready(tag))
	eventually(t, timeout, "the operation Ready", ready(op))
	eventually(t, timeout, "an undeclared template parameter refused", notReadyWith(undeclared, "templateParameters"))

	policies := "<policies><inbound><base /><rate-limit calls=\"10\" renewal-period=\"60\" /></inbound><backend><base /></backend><outbound><base /></outbound></policies>"
	policy := &apim.ApiOperationPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "get-order-policy"},
		Spec: apim.ApiOperationPolicy_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "get-order"}, Format: ptr(apim.PolicyContractProperties_Format_Xml), Value: ptr(policies),
		},
	}
	broken := &apim.ApiOperationPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "broken-policy"},
		Spec: apim.ApiOperationPolicy_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "get-order"}, Format: ptr(apim.PolicyContractProperties_Format_Xml), Value: ptr("<inbound/>"),
		},
	}
	opTag := &apim.ApiOperationTag{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "get-order-public"},
		Spec:       apim.ApiOperationTag_Spec{AzureName: "public", Owner: &genruntime.KnownResourceReference{Name: "get-order"}},
	}
	apiTag := &apim.ApiTag{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "orders-public"},
		Spec:       apim.ApiTag_Spec{AzureName: "public", Owner: onAPI},
	}
	strayTag := &apim.ApiTag{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "orders-internal"},
		Spec:       apim.ApiTag_Spec{AzureName: "internal", Owner: onAPI},
	}
	productTag := &apim.ProductTag{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "starter-public"},
		Spec:       apim.ProductTag_Spec{AzureName: "public", Owner: &genruntime.KnownResourceReference{Name: "starter"}},
	}
	schema := &apim.ApiSchema{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "orders-schema"},
		Spec: apim.ApiSchema_Spec{
			Owner: onAPI, ContentType: ptr("application/vnd.oai.openapi.components+json"),
			Document: &apim.SchemaDocumentProperties{Value: ptr(`{"Order":{"type":"object"}}`)},
		},
	}
	described := &apim.ApiTagDescription{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "orders-public-description"},
		Spec:       apim.ApiTagDescription_Spec{AzureName: "public", Owner: onAPI, Description: ptr("Open to every subscriber")},
	}
	aad := &apim.IdentityProvider{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "aad"},
		Spec: apim.IdentityProvider_Spec{
			AzureName: "aad", Owner: onService, Type: ptr(apim.IdentityProviderCreateContractProperties_Type_Aad),
			ClientId: ptr("00000000-0000-0000-0000-0000000000c1"), ClientSecret: &genruntime.SecretReference{Name: "idp", Key: "aad"},
			AllowedTenants: []string{tenantID},
		},
	}
	oauth := &apim.AuthorizationServer{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "corp-oauth"},
		Spec: apim.AuthorizationServer_Spec{
			Owner: onService, DisplayName: ptr("Corp OAuth"), ClientId: ptr("orders-console"),
			ClientRegistrationEndpoint: ptr("https://idp.example.com/register"), AuthorizationEndpoint: ptr("https://idp.example.com/authorize"),
			GrantTypes:   []apim.AuthorizationServerContractProperties_GrantTypes{apim.AuthorizationServerContractProperties_GrantTypes_AuthorizationCode},
			ClientSecret: &genruntime.SecretReference{Name: "idp", Key: "oauth"},
		},
	}
	later := []conditioned{policy, broken, opTag, apiTag, strayTag, productTag, schema, described, aad, oauth}
	for _, o := range later {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []conditioned{policy, opTag, apiTag, productTag, schema, described, aad, oauth} {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	eventually(t, timeout, "a policy that is no <policies> refused", notReadyWith(broken, "<policies>"))
	eventually(t, timeout, "a link to a missing tag refused", notReadyWith(strayTag, "was not found"))

	opID := apiID + "/operations/get-order"
	expectResource(t, opID+"/policies/policy", func(r map[string]any) error {
		if p := properties(r); p["value"] != policies || p["format"] != "xml" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, svcID+"/identityProviders/aad", func(r map[string]any) error {
		if p := properties(r); p["clientSecret"] != "aad-client-secret" || p["type"] != "aad" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	for _, id := range []string{apiID + "/tags/public", productID + "/tags/public", opID + "/tags/public"} {
		if !armFake.HasResource(id) {
			t.Errorf("%s is not in the fake", id)
		}
	}

	// A change is PUT again: a path that is the operation's own.
	if err := change(t, op, func(op *apim.ApiOperation) {
		op.Spec.UrlTemplate = ptr("/orders/{id}/details")
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, opID, func(r map[string]any) error {
		if p := properties(r); p["urlTemplate"] != "/orders/{id}/details" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, objs := range [][]conditioned{later, {tag, op, undeclared}} {
		for _, o := range objs {
			if err := k8s.Delete(ctx, o); err != nil {
				t.Fatal(err)
			}
		}
		for _, o := range objs {
			eventually(t, timeout, o.GetName()+" gone", gone(o))
		}
	}
	for _, id := range []string{opID, opID + "/policies/policy", apiID + "/tags/public", svcID + "/tags/public", svcID + "/identityProviders/aad", svcID + "/authorizationServers/corp-oauth"} {
		if armFake.HasResource(id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
}

// The second part of the API Management kinds (docs/plan.md f71): a
// GraphQL API's resolver and its policy; a tag's links to an API, an
// operation and a product, and a product's links to an API and a group,
// by object and by ARM ID; an API's release and wiki over a documentation;
// a policy restriction; a workspace gateway in a subnet and its
// configuration connection to a workspace. A resolver whose path is not
// Type/field, a link to another service's API and a connection from a
// Standard gateway are refused (the fake's rules, apimanagement.go); a
// change is PUT again; deleted, all go.
func TestAPIManagementLinksAndGateways(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "apim-links")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	svcID := rgID + "/providers/Microsoft.ApiManagement/service/gateway"
	apiID := svcID + "/apis/catalog"
	productID := svcID + "/products/starter"
	groupID := svcID + "/groups/developers"
	workspaceID := svcID + "/workspaces/team"
	otherAPI := rgID + "/providers/Microsoft.ApiManagement/service/other/apis/catalog"
	vnetID := rgID + "/providers/Microsoft.Network/virtualNetworks/edge"
	subnetID := vnetID + "/subnets/gateway"
	for _, id := range []string{rgID, svcID, apiID, productID, groupID, workspaceID, parentOf(parentOf(otherAPI)), parentOf(otherAPI), otherAPI, vnetID, subnetID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	svc := &asoapim.Service{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "gateway"}}
	svc.Spec.AzureName, svc.Spec.Location = "gateway", ptr("westus")
	svc.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, svc, svcID)
	api := &asoapim.Api{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "catalog"}}
	api.Spec.AzureName = "catalog"
	api.Spec.Owner = &genruntime.KnownResourceReference{Name: "gateway"}
	asoStandIn(t, api, apiID)
	product := &asoapim.Product{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "starter"}}
	product.Spec.AzureName = "starter"
	product.Spec.Owner = &genruntime.KnownResourceReference{Name: "gateway"}
	asoStandIn(t, product, productID)
	onService := &genruntime.KnownResourceReference{Name: "gateway"}
	onAPI := &genruntime.KnownResourceReference{Name: "catalog"}
	onProduct := &genruntime.KnownResourceReference{Name: "starter"}
	onRG := &genruntime.KnownResourceReference{Name: "rg"}

	// What the others name first: the tag, an operation, the
	// documentation, the resolver and the gateways.
	tag := &apim.Tag{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "featured"},
		Spec:       apim.Tag_Spec{Owner: onService, DisplayName: ptr("Featured")},
	}
	op := &apim.ApiOperation{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "list-items"},
		Spec:       apim.ApiOperation_Spec{Owner: onAPI, DisplayName: ptr("List items"), Method: ptr("GET"), UrlTemplate: ptr("/items")},
	}
	doc := &apim.Documentation{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "intro"},
		Spec:       apim.Documentation_Spec{Owner: onService, Title: ptr("Introduction"), Content: ptr("# The catalog API")},
	}
	resolver := &apim.ApiResolver{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "item-resolver"},
		Spec:       apim.ApiResolver_Spec{Owner: onAPI, DisplayName: ptr("Item"), Path: ptr("Query/item")},
	}
	badResolver := &apim.ApiResolver{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "query-resolver"},
		Spec:       apim.ApiResolver_Spec{Owner: onAPI, DisplayName: ptr("Query"), Path: ptr("Query")},
	}
	workspaceGW := &apim.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "team-gateway"},
		Spec: apim.Gateway_Spec{
			Owner: onRG, Location: ptr("westus"),
			Sku: &apim.ApiManagementGatewaySkuProperties{Name: ptr(apim.ApiManagementGatewaySkuProperties_Name_WorkspaceGatewayPremium), Capacity: ptr(1)},
			Backend: &apim.BackendConfiguration{Subnet: &apim.BackendSubnetConfiguration{
				Reference: &genruntime.ResourceReference{ARMID: subnetID},
			}},
		},
	}
	standardGW := &apim.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "standard-gateway"},
		Spec: apim.Gateway_Spec{
			Owner: onRG, Location: ptr("westus"),
			Sku: &apim.ApiManagementGatewaySkuProperties{Name: ptr(apim.ApiManagementGatewaySkuProperties_Name_Standard), Capacity: ptr(1)},
		},
	}
	first := []conditioned{tag, op, doc, resolver, badResolver, workspaceGW, standardGW}
	for _, o := range first {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []conditioned{tag, op, doc, resolver, workspaceGW, standardGW} {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	eventually(t, timeout, "a resolver path that is not Type/field refused", notReadyWith(badResolver, "Type/field"))

	onTag := &genruntime.KnownResourceReference{Name: "featured"}
	resolverPolicy := &apim.ApiResolverPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "item-resolver-policy"},
		Spec: apim.ApiResolverPolicy_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "item-resolver"},
			Value: ptr(`<policies><inbound><base /></inbound><backend><http-data-source><http-request><set-method>GET</set-method><set-url>https://items.example.com/</set-url></http-request></http-data-source></backend><outbound><base /></outbound></policies>`),
		},
	}
	tagAPI := &apim.TagApiLink{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "featured-catalog"},
		Spec: apim.TagApiLink_Spec{Owner: onTag, AzureName: "catalog",
			ApiReference: &genruntime.ResourceReference{Group: "apimanagement.azure.com", Kind: "Api", Name: "catalog"}},
	}
	tagOther := &apim.TagApiLink{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "featured-other"},
		Spec:       apim.TagApiLink_Spec{Owner: onTag, ApiReference: &genruntime.ResourceReference{ARMID: otherAPI}},
	}
	tagOp := &apim.TagOperationLink{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "featured-list-items"},
		Spec: apim.TagOperationLink_Spec{Owner: onTag,
			OperationReference: &genruntime.ResourceReference{Group: "apimanagement.azure.hfl.dev", Kind: "ApiOperation", Name: "list-items"}},
	}
	tagProduct := &apim.TagProductLink{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "featured-starter"},
		Spec:       apim.TagProductLink_Spec{Owner: onTag, ProductReference: &genruntime.ResourceReference{ARMID: productID}},
	}
	productAPI := &apim.ProductApiLink{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "starter-catalog"},
		Spec:       apim.ProductApiLink_Spec{Owner: onProduct, ApiReference: &genruntime.ResourceReference{ARMID: apiID}},
	}
	productGroup := &apim.ProductGroupLink{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "starter-developers"},
		Spec:       apim.ProductGroupLink_Spec{Owner: onProduct, GroupReference: &genruntime.ResourceReference{ARMID: groupID}},
	}
	release := &apim.ApiRelease{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "catalog-v1"},
		Spec: apim.ApiRelease_Spec{Owner: onAPI, Notes: ptr("The first release"),
			ApiReference: &genruntime.ResourceReference{Group: "apimanagement.azure.com", Kind: "Api", Name: "catalog"}},
	}
	wiki := &apim.ApiWiki{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "catalog-wiki"},
		Spec:       apim.ApiWiki_Spec{Owner: onAPI, Documents: []apim.WikiDocumentationContract{{DocumentationId: ptr("intro")}}},
	}
	restriction := &apim.PolicyRestriction{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "require-base"},
		Spec: apim.PolicyRestriction_Spec{Owner: onService, Scope: ptr("Sample Path to the policy document."),
			RequireBase: ptr(apim.PolicyRestrictionContractProperties_RequireBase_True)},
	}
	conn := &apim.GatewayConfigConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "team-connection"},
		Spec: apim.GatewayConfigConnection_Spec{Owner: &genruntime.KnownResourceReference{Name: "team-gateway"},
			SourceReference: &genruntime.ResourceReference{ARMID: workspaceID}, Hostnames: []string{"team.example.com"}},
	}
	standardConn := &apim.GatewayConfigConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "standard-connection"},
		Spec: apim.GatewayConfigConnection_Spec{Owner: &genruntime.KnownResourceReference{Name: "standard-gateway"},
			SourceReference: &genruntime.ResourceReference{ARMID: workspaceID}},
	}
	later := []conditioned{resolverPolicy, tagAPI, tagOther, tagOp, tagProduct, productAPI, productGroup, release, wiki, restriction, conn, standardConn}
	for _, o := range later {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []conditioned{resolverPolicy, tagAPI, tagOp, tagProduct, productAPI, productGroup, release, wiki, restriction, conn} {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	eventually(t, timeout, "a link to another service's API refused", notReadyWith(tagOther, "same service"))
	eventually(t, timeout, "a Standard gateway's connection refused", notReadyWith(standardConn, "workspace gateway"))

	tagID := svcID + "/tags/featured"
	expectResource(t, tagID+"/operationLinks/featured-list-items", func(r map[string]any) error {
		if p := properties(r); p["operationId"] != apiID+"/operations/list-items" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, tagID+"/apiLinks/catalog", func(r map[string]any) error {
		if p := properties(r); p["apiId"] != apiID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	gwID := rgID + "/providers/Microsoft.ApiManagement/gateways/team-gateway"
	expectResource(t, gwID, func(r map[string]any) error {
		backend, _ := properties(r)["backend"].(map[string]any)
		sn, _ := backend["subnet"].(map[string]any)
		if sn["id"] != subnetID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	for _, id := range []string{apiID + "/resolvers/item-resolver/policies/policy", apiID + "/wikis/default", apiID + "/releases/catalog-v1",
		productID + "/apiLinks/starter-catalog", productID + "/groupLinks/starter-developers", tagID + "/productLinks/featured-starter",
		svcID + "/policyRestrictions/require-base", gwID + "/configConnections/team-connection"} {
		if !armFake.HasResource(id) {
			t.Errorf("%s is not in the fake", id)
		}
	}

	// A change is PUT again: the release's notes.
	if err := change(t, release, func(r *apim.ApiRelease) { r.Spec.Notes = ptr("The first release, revised") }); err != nil {
		t.Fatal(err)
	}
	expectResource(t, apiID+"/releases/catalog-v1", func(r map[string]any) error {
		if p := properties(r); p["notes"] != "The first release, revised" || p["apiId"] != apiID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, objs := range [][]conditioned{later, first} {
		for _, o := range objs {
			if err := k8s.Delete(ctx, o); err != nil {
				t.Fatal(err)
			}
		}
		for _, o := range objs {
			eventually(t, timeout, o.GetName()+" gone", gone(o))
		}
	}
	for _, id := range []string{gwID, gwID + "/configConnections/team-connection", tagID, tagID + "/apiLinks/catalog", apiID + "/resolvers/item-resolver", apiID + "/wikis/default"} {
		if armFake.HasResource(id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
}
