// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"strings"
	"testing"
	"time"

	asoapim "github.com/Azure/azure-service-operator/v2/api/apimanagement/v20240501/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	apim "github.com/highfivelabs/aso-extensions/apis/arm/apimanagement/v20240501"
)

const apimAPI = "2024-05-01"

// An API's operation with a policy and a tag, on a Consumption API
// Management service and an API made as fixtures standing in for ASO's
// Service and Api (docs/plan.md f64): created, the operation's path
// changed, deleted. Cheap: the Consumption tier bills calls only, and none
// is made; the service takes a few minutes to make. Not here: identity
// providers (the developer portal, which Consumption lacks), authorization
// servers and caches (written for the fake only).
func TestLiveAPIManagementOperation(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.ApiManagement")
	ns := namespace(t, "apimop")
	name := azName("apim")
	svcID := rgID() + "/providers/Microsoft.ApiManagement/service/" + name
	fixtureWait(t, 45*time.Minute, svcID, apimAPI, map[string]any{
		"location": location, "sku": map[string]any{"name": "Consumption", "capacity": 0},
		"properties": map[string]any{"publisherEmail": "apim@example.com", "publisherName": "live test"},
	})
	apiID := svcID + "/apis/orders"
	fixture(t, apiID, apimAPI, map[string]any{"properties": map[string]any{
		"displayName": "Orders", "path": "orders", "protocols": []string{"https"}, "serviceUrl": "https://orders.example.com",
	}})
	svc := &asoapim.Service{}
	svc.Namespace, svc.Name = ns, "gateway"
	svc.Spec.AzureName = name
	svc.Spec.Location = ptr(location)
	svc.Spec.Owner = owner("rg")
	standIn(t, svc, svcID, nil)
	api := &asoapim.Api{}
	api.Namespace, api.Name = ns, "orders"
	api.Spec.AzureName = "orders"
	api.Spec.Owner = owner("gateway")
	standIn(t, api, apiID, nil)

	tag := &apim.Tag{}
	tag.Namespace, tag.Name = ns, "public"
	tag.Spec = apim.Tag_Spec{AzureName: "public", Owner: owner("gateway"), DisplayName: ptr("Public")}
	op := &apim.ApiOperation{}
	op.Namespace, op.Name = ns, "get-order"
	op.Spec = apim.ApiOperation_Spec{
		AzureName: "get-order", Owner: owner("orders"), DisplayName: ptr("Get order"), Method: ptr("GET"), UrlTemplate: ptr("/orders/{id}"),
		TemplateParameters: []apim.ParameterContract{{Name: ptr("id"), Type: ptr("string"), Required: ptr(true)}},
	}
	create(t, timeout, tag, op)
	policy := &apim.ApiOperationPolicy{}
	policy.Namespace, policy.Name = ns, "get-order-policy"
	policy.Spec = apim.ApiOperationPolicy_Spec{
		Owner: owner("get-order"), Format: ptr(apim.PolicyContractProperties_Format_Xml),
		Value: ptr(`<policies><inbound><base /><set-header name="x-live-test" exists-action="override"><value>1</value></set-header></inbound><backend><base /></backend><outbound><base /></outbound><on-error><base /></on-error></policies>`),
	}
	apiTag := &apim.ApiTag{}
	apiTag.Namespace, apiTag.Name = ns, "orders-public"
	apiTag.Spec = apim.ApiTag_Spec{AzureName: "public", Owner: owner("orders")}
	create(t, timeout, policy, apiTag)

	opID := apiID + "/operations/get-order"
	expectResource(t, opID, apimAPI, func(r map[string]any) error {
		if p := properties(r); p["method"] != "GET" || p["urlTemplate"] != "/orders/{id}" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, op, func(o *apim.ApiOperation) { o.Spec.UrlTemplate = ptr("/orders/{id}/details") })
	eventually(t, timeout, "the operation Ready after the change", ready(op))
	expectResource(t, opID, apimAPI, func(r map[string]any) error {
		if p := properties(r); p["urlTemplate"] != "/orders/{id}/details" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, apiTag)
	remove(t, timeout, policy)
	remove(t, timeout, op)
	expectGone(t, opID, apimAPI)
	remove(t, timeout, tag)
	expectGone(t, svcID+"/tags/public", apimAPI)
}

// A tag's link to an API, a product's link to it and the API's release, on
// a Consumption service with an API and a product made as fixtures
// (docs/plan.md f71): created, the release's notes changed, deleted. Cheap
// as TestLiveAPIManagementOperation. Not here: resolvers (a GraphQL API),
// wikis and documentations (the developer portal, which Consumption
// lacks), and the v2 workspace gateways (billed by the unit-hour, with a
// Premium service's workspace).
func TestLiveAPIManagementLinks(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.ApiManagement")
	ns := namespace(t, "apimlinks")
	name := azName("apim")
	svcID := rgID() + "/providers/Microsoft.ApiManagement/service/" + name
	fixtureWait(t, 45*time.Minute, svcID, apimAPI, map[string]any{
		"location": location, "sku": map[string]any{"name": "Consumption", "capacity": 0},
		"properties": map[string]any{"publisherEmail": "apim@example.com", "publisherName": "live test"},
	})
	apiID := svcID + "/apis/catalog"
	fixture(t, apiID, apimAPI, map[string]any{"properties": map[string]any{
		"displayName": "Catalog", "path": "catalog", "protocols": []string{"https"}, "serviceUrl": "https://catalog.example.com",
	}})
	productID := svcID + "/products/starter"
	fixture(t, productID, apimAPI, map[string]any{"properties": map[string]any{
		"displayName": "Starter", "subscriptionRequired": false, "state": "notPublished",
	}})
	svc := &asoapim.Service{}
	svc.Namespace, svc.Name = ns, "gateway"
	svc.Spec.AzureName = name
	svc.Spec.Location = ptr(location)
	svc.Spec.Owner = owner("rg")
	standIn(t, svc, svcID, nil)
	product := &asoapim.Product{}
	product.Namespace, product.Name = ns, "starter"
	product.Spec.AzureName = "starter"
	product.Spec.Owner = owner("gateway")
	standIn(t, product, productID, nil)
	api := &asoapim.Api{}
	api.Namespace, api.Name = ns, "catalog"
	api.Spec.AzureName = "catalog"
	api.Spec.Owner = owner("gateway")
	standIn(t, api, apiID, nil)

	tag := &apim.Tag{}
	tag.Namespace, tag.Name = ns, "featured"
	tag.Spec = apim.Tag_Spec{AzureName: "featured", Owner: owner("gateway"), DisplayName: ptr("Featured")}
	create(t, timeout, tag)
	tagLink := &apim.TagApiLink{}
	tagLink.Namespace, tagLink.Name = ns, "featured-catalog"
	tagLink.Spec = apim.TagApiLink_Spec{AzureName: "catalog", Owner: owner("featured"), ApiReference: &genruntime.ResourceReference{ARMID: apiID}}
	productLink := &apim.ProductApiLink{}
	productLink.Namespace, productLink.Name = ns, "starter-catalog"
	productLink.Spec = apim.ProductApiLink_Spec{AzureName: "catalog", Owner: owner("starter"), ApiReference: &genruntime.ResourceReference{ARMID: apiID}}
	release := &apim.ApiRelease{}
	release.Namespace, release.Name = ns, "catalog-v1"
	release.Spec = apim.ApiRelease_Spec{AzureName: "v1", Owner: owner("catalog"), Notes: ptr("The first release"), ApiReference: &genruntime.ResourceReference{ARMID: apiID}}
	create(t, timeout, tagLink, productLink, release)

	expectResource(t, svcID+"/tags/featured/apiLinks/catalog", apimAPI, func(r map[string]any) error {
		if p := properties(r); !strings.EqualFold(fmt.Sprint(p["apiId"]), apiID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, release, func(r *apim.ApiRelease) { r.Spec.Notes = ptr("The first release, revised") })
	eventually(t, timeout, "the release Ready after the change", ready(release))
	expectResource(t, apiID+"/releases/v1", apimAPI, func(r map[string]any) error {
		if p := properties(r); p["notes"] != "The first release, revised" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, release)
	expectGone(t, apiID+"/releases/v1", apimAPI)
	remove(t, timeout, productLink)
	expectGone(t, productID+"/apiLinks/catalog", apimAPI)
	remove(t, timeout, tagLink)
	expectGone(t, svcID+"/tags/featured/apiLinks/catalog", apimAPI)
	remove(t, timeout, tag)
}
