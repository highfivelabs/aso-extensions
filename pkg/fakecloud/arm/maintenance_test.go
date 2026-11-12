// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"encoding/json"
	"net/http"
	"testing"
)

// What a maintenance configuration assignment must give, on a resource and
// at a dynamic scope.
func TestMaintenanceAssignment(t *testing.T) {
	a := New(func(string) bool { return true })
	put := func(what, path, body string, want int) map[string]any {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
		out := map[string]any{}
		_ = json.Unmarshal([]byte(resp), &out)
		return out
	}
	sub := "/subscriptions/" + sub1
	rg := sub + "/resourceGroups/vms"
	put("resource group", rg, `{"location":"westus"}`, http.StatusCreated)
	vm := rg + "/providers/Microsoft.Compute/virtualMachines/vm1"
	a.AddResource(vm)
	cfg := rg + "/providers/Microsoft.Maintenance/maintenanceConfigurations/patching"
	host := rg + "/providers/Microsoft.Maintenance/maintenanceConfigurations/host"
	assign := func(config, extra string) string {
		return `{"location":"westus","properties":{"maintenanceConfigurationId":"` + config + `"` + extra + `}}`
	}

	onVM := vm + "/providers/Microsoft.Maintenance/configurationAssignments/patching"
	put("assignment without a configuration", onVM, `{"location":"westus","properties":{}}`, http.StatusBadRequest)
	put("assignment to a missing configuration", onVM, assign(cfg, ""), http.StatusNotFound)
	put("configuration", cfg, `{"location":"westus","properties":{"maintenanceScope":"InGuestPatch"}}`, http.StatusCreated)
	put("host configuration", host, `{"location":"westus","properties":{"maintenanceScope":"Host"}}`, http.StatusCreated)
	put("assignment naming another resource", onVM, assign(cfg, `,"resourceId":"`+rg+`/providers/Microsoft.Compute/virtualMachines/vm2"`), http.StatusBadRequest)
	r := put("assignment on a VM", onVM, assign(cfg, ""), http.StatusCreated)
	if r["properties"].(map[string]any)["resourceId"] != vm {
		t.Fatalf("Azure sets resourceId to the resource: %v", r)
	}
	put("assignment on a missing VM", rg+"/providers/Microsoft.Compute/virtualMachines/none/providers/Microsoft.Maintenance/configurationAssignments/x", assign(cfg, ""), http.StatusNotFound)

	filter := `,"filter":{"resourceTypes":["Microsoft.Compute/virtualMachines"],"osTypes":["Linux"],"tagSettings":{"tags":{"patch":["weekly"]},"filterOperator":"Any"}}`
	onSub := sub + "/providers/Microsoft.Maintenance/configurationAssignments/weekly"
	put("dynamic scope without a filter", onSub, assign(cfg, ""), http.StatusBadRequest)
	put("dynamic scope with a Host configuration", onSub, assign(host, filter), http.StatusBadRequest)
	put("dynamic scope on a subscription", onSub, assign(cfg, filter), http.StatusCreated)
	put("dynamic scope on a resource group", rg+"/providers/Microsoft.Maintenance/configurationAssignments/weekly", assign(cfg, filter), http.StatusCreated)
	global := `{"location":"global","properties":{"maintenanceConfigurationId":"` + cfg + `"` + filter + `}}`
	put("global dynamic scope on a resource group", rg+"/providers/Microsoft.Maintenance/configurationAssignments/global", global, http.StatusBadRequest)
	put("global dynamic scope on a subscription", sub+"/providers/Microsoft.Maintenance/configurationAssignments/global", global, http.StatusCreated)
}
