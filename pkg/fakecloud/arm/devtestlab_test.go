// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// A lab's schedules and costs have fixed names, a lab virtual network names a
// virtual network that exists, a lab VM names a network and subnet of its own
// lab and signs in, and a user's secret has a value.
func TestDevTestLab(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/dtl"
	vnet := rg + "/providers/Microsoft.Network/virtualNetworks/vnet"
	lab := rg + "/providers/Microsoft.DevTestLab/labs/lab"
	other := rg + "/providers/Microsoft.DevTestLab/labs/other"
	for _, id := range []string{rg, vnet, lab, other} {
		a.AddResource(id)
	}
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	put("a lab schedule with another name", lab+"/schedules/nightly", `{"properties":{"taskType":"LabVmsShutdownTask"}}`, http.StatusBadRequest)
	put("auto-start with the shutdown task", lab+"/schedules/LabVmAutoStart", `{"properties":{"taskType":"LabVmsShutdownTask"}}`, http.StatusBadRequest)
	put("the lab's shutdown", lab+"/schedules/LabVmsShutdown", `{"properties":{"taskType":"LabVmsShutdownTask","dailyRecurrence":{"time":"1900"}}}`, http.StatusCreated)
	put("the lab's auto-start", lab+"/schedules/LabVmAutoStart", `{"properties":{"taskType":"LabVmsStartupTask"}}`, http.StatusCreated)
	put("costs not named targetCost", lab+"/costs/budget", `{"properties":{"currencyCode":"USD"}}`, http.StatusBadRequest)
	put("costs", lab+"/costs/targetCost", `{"properties":{"currencyCode":"USD"}}`, http.StatusCreated)
	put("a lab network on a missing network", lab+"/virtualnetworks/missing", `{"properties":{"externalProviderResourceId":"`+rg+`/providers/Microsoft.Network/virtualNetworks/none"}}`, http.StatusNotFound)
	put("a lab network", lab+"/virtualnetworks/net", `{"properties":{"externalProviderResourceId":"`+vnet+`","subnetOverrides":[{"resourceId":"`+vnet+`/subnets/default","labSubnetName":"default"}]}}`, http.StatusCreated)
	put("other lab's network", other+"/virtualnetworks/net", `{"properties":{"externalProviderResourceId":"`+vnet+`"}}`, http.StatusCreated)
	vm := func(net, subnet, auth string) string {
		return `{"properties":{"labVirtualNetworkId":"` + net + `","labSubnetName":"` + subnet + `","userName":"labadmin"` + auth + `}}`
	}
	put("a VM on another lab's network", lab+"/virtualmachines/vm", vm(other+"/virtualnetworks/net", "default", `,"password":"p"`), http.StatusNotFound)
	put("a VM on a missing subnet", lab+"/virtualmachines/vm", vm(lab+"/virtualnetworks/net", "apps", `,"password":"p"`), http.StatusBadRequest)
	put("a VM without a password", lab+"/virtualmachines/vm", vm(lab+"/virtualnetworks/net", "default", ``), http.StatusBadRequest)
	put("a VM with SSH but no key", lab+"/virtualmachines/vm", vm(lab+"/virtualnetworks/net", "default", `,"isAuthenticationWithSshKey":true`), http.StatusBadRequest)
	put("a VM", lab+"/virtualmachines/vm", vm(lab+"/virtualnetworks/net", "default", `,"password":"p"`), http.StatusCreated)
	put("a VM schedule with another name", lab+"/virtualmachines/vm/schedules/nightly", `{"properties":{"taskType":"LabVmsShutdownTask"}}`, http.StatusBadRequest)
	put("a VM's shutdown", lab+"/virtualmachines/vm/schedules/LabVmsShutdown", `{"properties":{"taskType":"LabVmsShutdownTask"}}`, http.StatusCreated)
	put("a user", lab+"/users/u", `{"properties":{}}`, http.StatusCreated)
	put("a secret without a value", lab+"/users/u/secrets/s", `{"properties":{}}`, http.StatusBadRequest)
	put("a secret", lab+"/users/u/secrets/s", `{"properties":{"value":"v"}}`, http.StatusCreated)
}
