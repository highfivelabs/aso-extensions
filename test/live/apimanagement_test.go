// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"testing"
	"time"

	asoapim "github.com/Azure/azure-service-operator/v2/api/apimanagement/v20240501/storage"

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
