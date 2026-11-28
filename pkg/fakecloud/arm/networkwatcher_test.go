// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// Connection monitors' test groups and endpoints; address prefix sets' CIDRs.
func TestConnectionMonitorsAndAddressPrefixSets(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/net"
	a.AddResource(rg)
	watcher := rg + "/providers/Microsoft.Network/networkWatchers/nw"
	a.AddResource(watcher)
	vm := rg + "/providers/Microsoft.Compute/virtualMachines/vm"
	a.AddResource(vm)
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	monitor := watcher + "/connectionMonitors/"
	monitorBody := func(sourceResource, groupSource string) string {
		return `{"location":"westus","properties":{"endpoints":[{"name":"vm","resourceId":"` + sourceResource + `"},{"name":"web","address":"www.example.com"}],` +
			`"testConfigurations":[{"name":"https","protocol":"Http"}],` +
			`"testGroups":[{"name":"g","sources":["` + groupSource + `"],"destinations":["web"],"testConfigurations":["https"]}]}}`
	}
	put("no test groups", monitor+"m", `{"location":"westus","properties":{}}`, http.StatusBadRequest)
	put("an endpoint the monitor lacks", monitor+"m", monitorBody(vm, "other"), http.StatusBadRequest)
	put("an endpoint's resource missing", monitor+"m", monitorBody(rg+"/providers/Microsoft.Compute/virtualMachines/gone", "vm"), http.StatusNotFound)
	put("a monitor", monitor+"m", monitorBody(vm, "vm"), http.StatusCreated)
	put("a v1 monitor", monitor+"v1", `{"location":"westus","properties":{"source":{"resourceId":"`+vm+`"},"destination":{"address":"10.0.0.4","port":443}}}`, http.StatusCreated)

	asg := rg + "/providers/Microsoft.Network/applicationSecurityGroups/web"
	a.AddResource(asg)
	sets := asg + "/addressPrefixSets/"
	put("no prefixes", sets+"s", `{"properties":{"addressPrefixes":[]}}`, http.StatusBadRequest)
	put("not CIDR", sets+"s", `{"properties":{"addressPrefixes":["10.0.0.1"]}}`, http.StatusBadRequest)
	put("prefixes", sets+"s", `{"properties":{"addressPrefixes":["10.0.0.0/16","2001:db8::/32"]}}`, http.StatusCreated)
}
