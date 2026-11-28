// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"strings"
	"testing"

	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	logic "github.com/highfivelabs/aso-extensions/apis/arm/web/v20160601"
)

const logicAPI = "2016-06-01"

// A Logic Apps custom API (a custom connector) from an inline OpenAPI
// document, an API connection to it, and one to the RSS managed API, which
// needs no sign-in (docs/plan.md f56). All free: connections and custom
// connectors bill nothing until a Logic App runs an action through them.
// The connection to the custom API has no parameters, so Azure may report
// it unauthenticated; it exists all the same. A data gateway is not made: it
// needs a gateway installed on a machine.
func TestLiveLogicAppsConnections(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.Web")
	ns := namespace(t, "logic")
	swagger := map[string]v1.JSON{
		"swagger": {Raw: []byte(`"2.0"`)},
		"info":    {Raw: []byte(`{"title":"Echo","version":"1.0"}`)},
		"host":    {Raw: []byte(`"echo.contoso.example"`)},
		"schemes": {Raw: []byte(`["https"]`)},
		"paths":   {Raw: []byte(`{"/echo":{"get":{"operationId":"Echo","responses":{"200":{"description":"OK"}}}}}`)},
	}
	api := &logic.CustomApi{}
	api.Namespace, api.Name = ns, "echo"
	api.Spec = logic.CustomApi_Spec{
		AzureName: azName("echo"), Owner: owner("rg"), Location: ptr(location),
		Properties: &logic.CustomApiPropertiesDefinition{
			DisplayName: ptr("Echo " + runID), ApiType: ptr(logic.ApiType_Rest), Swagger: swagger,
			BackendService: &logic.ApiResourceBackendService{ServiceUrl: ptr("https://echo.contoso.example")},
		},
	}
	create(t, timeout, api)
	apiID := rgID() + "/providers/Microsoft.Web/customApis/" + api.Spec.AzureName

	rssAPI := "/subscriptions/" + subID + "/providers/Microsoft.Web/locations/" + location + "/managedApis/rss"
	rss := &logic.ApiConnection{}
	rss.Namespace, rss.Name = ns, "rss"
	rss.Spec = logic.ApiConnection_Spec{
		AzureName: azName("rss"), Owner: owner("rg"), Location: ptr(location),
		Properties: &logic.Connection_Properties_Spec{
			DisplayName: ptr("RSS"), Api: &logic.ApiReference{Reference: &genruntime.ResourceReference{ARMID: rssAPI}},
		},
	}
	echo := &logic.ApiConnection{}
	echo.Namespace, echo.Name = ns, "echo-conn"
	echo.Spec = logic.ApiConnection_Spec{
		AzureName: azName("echo-conn"), Owner: owner("rg"), Location: ptr(location),
		Properties: &logic.Connection_Properties_Spec{
			Api: &logic.ApiReference{Reference: &genruntime.ResourceReference{Group: "web.azure.hfl.dev", Kind: "CustomApi", Name: "echo"}},
		},
	}
	create(t, timeout, rss, echo)
	rssID := rgID() + "/providers/Microsoft.Web/connections/" + rss.Spec.AzureName
	expectResource(t, rssID, logicAPI, func(r map[string]any) error {
		ref, _ := properties(r)["api"].(map[string]any)
		if !strings.EqualFold(fmt.Sprint(ref["id"]), rssAPI) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, rgID()+"/providers/Microsoft.Web/connections/"+echo.Spec.AzureName, logicAPI, func(r map[string]any) error {
		ref, _ := properties(r)["api"].(map[string]any)
		if !strings.EqualFold(fmt.Sprint(ref["id"]), apiID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	update(t, rss, func(c *logic.ApiConnection) { c.Spec.Properties.DisplayName = ptr("RSS feeds") })
	eventually(t, timeout, "the RSS connection Ready after the change", ready(rss))
	expectResource(t, rssID, logicAPI, func(r map[string]any) error {
		if properties(r)["displayName"] != "RSS feeds" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	remove(t, timeout, echo)
	remove(t, timeout, rss)
	remove(t, timeout, api)
	expectGone(t, rssID, logicAPI)
	expectGone(t, apiID, logicAPI)
}
