// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// Certificates carry their certificate; a provisioning service is S1 and links hubs it can reach.
func TestDevices(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/iot"
	hub := rg + "/providers/Microsoft.Devices/IotHubs/plant"
	a.AddResource(rg)
	a.AddResource(hub)
	put := func(what, id, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, id, body)
		expect(t, what, code, resp, want)
	}
	put("a certificate with none", hub+"/certificates/root", `{"properties":{"isVerified":false}}`, http.StatusBadRequest)
	put("a certificate", hub+"/certificates/root", `{"properties":{"certificate":"MIIBroot"}}`, http.StatusCreated)

	dps := rg + "/providers/Microsoft.Devices/provisioningServices/enroll"
	put("a provisioning service of another SKU", dps, `{"location":"westus","sku":{"name":"F1","capacity":1},"properties":{}}`, http.StatusBadRequest)
	put("a linked hub with no connection string", dps, `{"location":"westus","sku":{"name":"S1","capacity":1},"properties":{"iotHubs":[{"location":"westus"}]}}`, http.StatusBadRequest)
	put("a provisioning service", dps, `{"location":"westus","sku":{"name":"S1","capacity":1},"properties":{"allocationPolicy":"Hashed","iotHubs":[{"location":"westus","connectionString":"HostName=plant.azure-devices.net;SharedAccessKeyName=iothubowner;SharedAccessKey=x"}]}}`, http.StatusCreated)
	put("a provisioning service's certificate", dps+"/certificates/root", `{"properties":{"certificate":"MIIBroot"}}`, http.StatusCreated)
}
