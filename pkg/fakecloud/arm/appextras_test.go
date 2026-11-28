// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// A sandbox group keeps its environment link, and it and its network
// connection name what exists; an agent's subnet comes with AzureVNet
// egress.
func TestAppExtras(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/apps"
	env := rg + "/providers/Microsoft.App/managedEnvironments/prod"
	subnet := rg + "/providers/Microsoft.Network/virtualNetworks/net/subnets/sandboxes"
	for _, id := range []string{rg, env} {
		a.AddResource(id)
	}
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	group := rg + "/providers/Microsoft.App/sandboxGroups/ci"
	put("a group linked to a missing environment", group, `{"location":"westus","properties":{"environmentId":"`+rg+`/providers/Microsoft.App/managedEnvironments/none"}}`, http.StatusNotFound)
	put("a group", group, `{"location":"westus","properties":{"environmentId":"`+env+`"}}`, http.StatusCreated)
	put("a group's link removed", group, `{"location":"westus","properties":{}}`, http.StatusConflict)
	put("a group changed", group, `{"location":"westus","tags":{"a":"b"},"properties":{"environmentId":"`+env+`"}}`, http.StatusOK)
	put("a connection to a missing subnet", group+"/vnetConnections/net", `{"properties":{"subnetId":"`+subnet+`"}}`, http.StatusNotFound)
	a.AddResource(subnet)
	put("a connection", group+"/vnetConnections/net", `{"properties":{"subnetId":"`+subnet+`"}}`, http.StatusCreated)
	agent := rg + "/providers/Microsoft.App/agents/sre"
	put("a subnet with limited egress", agent, `{"location":"westus","properties":{"vnetConfiguration":{"subnetResourceId":"`+subnet+`"},"sandboxConfiguration":{"egress":{"mode":"Limited"}}}}`, http.StatusBadRequest)
	put("AzureVNet egress without a subnet", agent, `{"location":"westus","properties":{"sandboxConfiguration":{"egress":{"mode":"AzureVNet"}}}}`, http.StatusBadRequest)
	put("an agent in a network", agent, `{"location":"westus","properties":{"vnetConfiguration":{"subnetResourceId":"`+subnet+`"},"sandboxConfiguration":{"egress":{"mode":"AzureVNet"}}}}`, http.StatusCreated)
	put("an agent with limited egress", agent, `{"location":"westus","properties":{"sandboxConfiguration":{"egress":{"mode":"Limited"}}}}`, http.StatusOK)
}
