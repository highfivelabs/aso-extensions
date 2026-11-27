// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// A factory's managed network and global parameters are named default; a
// managed private endpoint and a credential name what exists; a global
// parameter has a type.
func TestDataFactory(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/adf"
	factory := rg + "/providers/Microsoft.DataFactory/factories/etl"
	storage := rg + "/providers/Microsoft.Storage/storageAccounts/lake"
	identity := rg + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/etl"
	for _, id := range []string{rg, factory, storage, identity} {
		a.AddResource(id)
	}
	put := func(what, id, props string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, id, `{"properties":`+props+`}`)
		expect(t, what, code, resp, want)
	}
	put("a managed network not named default", factory+"/managedVirtualNetworks/isolated", `{}`, http.StatusBadRequest)
	put("a managed network", factory+"/managedVirtualNetworks/default", `{}`, http.StatusCreated)
	mpe := factory + "/managedVirtualNetworks/default/managedPrivateEndpoints/lake"
	put("an endpoint with no group", mpe, `{"privateLinkResourceId":"`+storage+`"}`, http.StatusBadRequest)
	put("an endpoint to nothing", mpe, `{"privateLinkResourceId":"`+storage+`x","groupId":"blob"}`, http.StatusNotFound)
	put("an endpoint", mpe, `{"privateLinkResourceId":"`+storage+`","groupId":"blob","fqdns":["lake.blob.core.windows.net"]}`, http.StatusCreated)
	put("a credential of no type", factory+"/credentials/etl", `{"typeProperties":{}}`, http.StatusBadRequest)
	put("an identity credential with no identity", factory+"/credentials/etl", `{"type":"ManagedIdentity","typeProperties":{}}`, http.StatusBadRequest)
	put("an identity credential to a missing identity", factory+"/credentials/etl", `{"type":"ManagedIdentity","typeProperties":{"resourceId":"`+identity+`x"}}`, http.StatusNotFound)
	put("an identity credential", factory+"/credentials/etl", `{"type":"ManagedIdentity","typeProperties":{"resourceId":"`+identity+`"}}`, http.StatusCreated)
	put("a principal credential with no key", factory+"/credentials/sp", `{"type":"ServicePrincipal","typeProperties":{"servicePrincipalId":"app","tenant":"t"}}`, http.StatusBadRequest)
	put("global parameters not named default", factory+"/globalParameters/env", `{"region":{"type":"String","value":"westus"}}`, http.StatusBadRequest)
	put("a global parameter of no type", factory+"/globalParameters/default", `{"region":{"value":"westus"}}`, http.StatusBadRequest)
	put("global parameters", factory+"/globalParameters/default", `{"region":{"type":"String","value":"westus"},"retries":{"type":"Int","value":3}}`, http.StatusCreated)
	if r, _ := a.Resource(factory + "/globalParameters/default"); r["properties"].(map[string]any)["provisioningState"] != nil {
		t.Errorf("global parameters given a provisioningState: %v", r)
	}
}

// A trigger, data flow, change data capture and integration runtime name
// others of the factory by reference, and each exists (f86); a linked
// self-hosted runtime's shared runtime exists.
func TestDataFactoryReferences(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/adf"
	factory := rg + "/providers/Microsoft.DataFactory/factories/etl"
	for _, id := range []string{rg, factory, factory + "/pipelines/copy", factory + "/datasets/raw", factory + "/linkedservices/lake"} {
		a.AddResource(id)
	}
	put := func(what, id, props string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, id, `{"properties":`+props+`}`)
		expect(t, what, code, resp, want)
	}
	schedule := func(pipeline string) string {
		return `{"type":"ScheduleTrigger","typeProperties":{"recurrence":{"frequency":"Day","interval":1}},` +
			`"pipelines":[{"pipelineReference":{"type":"PipelineReference","referenceName":"` + pipeline + `"}}]}`
	}
	put("a trigger of a missing pipeline", factory+"/triggers/nightly", schedule("train"), http.StatusBadRequest)
	put("a trigger", factory+"/triggers/nightly", schedule("copy"), http.StatusCreated)
	flow := func(dataset string) string {
		return `{"type":"MappingDataFlow","typeProperties":{"sources":[{"name":"src","dataset":{"type":"DatasetReference","referenceName":"` + dataset + `"}}],` +
			`"sinks":[{"name":"out","linkedService":{"type":"LinkedServiceReference","referenceName":"lake"}}]}}`
	}
	put("a data flow of a missing dataset", factory+"/dataflows/clean", flow("cooked"), http.StatusBadRequest)
	put("a data flow", factory+"/dataflows/clean", flow("raw"), http.StatusCreated)
	put("a data flow naming a parameterized dataset", factory+"/dataflows/param", flow("@{dataset().name}"), http.StatusCreated)
	put("a runtime in a missing managed network", factory+"/integrationRuntimes/azure",
		`{"type":"Managed","managedVirtualNetwork":{"type":"ManagedVirtualNetworkReference","referenceName":"default"}}`, http.StatusBadRequest)
	a.AddResource(factory + "/managedVirtualNetworks/default")
	put("a runtime in the managed network", factory+"/integrationRuntimes/azure",
		`{"type":"Managed","managedVirtualNetwork":{"type":"ManagedVirtualNetworkReference","referenceName":"default"}}`, http.StatusCreated)
	put("a linked runtime of a missing shared runtime", factory+"/integrationRuntimes/linked",
		`{"type":"SelfHosted","typeProperties":{"linkedInfo":{"authorizationType":"RBAC","resourceId":"`+rg+`/providers/Microsoft.DataFactory/factories/hub/integrationRuntimes/shared"}}}`, http.StatusNotFound)
	put("a change data capture", factory+"/adfcdcs/orders",
		`{"sourceConnectionsInfo":[{"connection":{"linkedService":{"type":"LinkedServiceReference","referenceName":"lake"},"type":"linkedservicetype"}}],"targetConnectionsInfo":[],"policy":{"mode":"Microbatch"}}`, http.StatusCreated)
}
