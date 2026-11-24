// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
)

// IoT hub children and Device Provisioning Services (Microsoft.Devices,
// 2023-06-30 and 2026-11-01; docs/plan.md f75), as far as the fake models
// them. Each rule is one the spec or IoT Hub's documentation states:
//
//   - a hub's or a provisioning service's certificate carries its
//     certificate (400);
//   - a provisioning service's SKU is S1 (400), and each IoT hub it links
//     has a connection string and a location (400).
//
// Not modelled: devices, enrollments and their attestation, certificate
// verification (proof of possession), the allocation policies at work,
// and the error codes (the fake's own).
var (
	iotCertificate = regexp.MustCompile(`(?i)/providers/microsoft\.devices/(iothubs|provisioningservices)/[^/]+/certificates/[^/]+$`)
	iotDPS         = regexp.MustCompile(`(?i)/providers/microsoft\.devices/provisioningservices/[^/]+$`)
)

// checkDevices refuses what the rules above do not allow; code is "" when
// it is allowed or id is none of these.
func (a *ARM) checkDevices(id string, body map[string]any) (status int, code, message string) {
	bad := func(msg string) (int, string, string) {
		return http.StatusBadRequest, "BadRequest", msg
	}
	props, _ := body["properties"].(map[string]any)
	switch {
	case iotCertificate.MatchString(id):
		if stringOf(props["certificate"]) == "" {
			return bad("'certificate' is required.")
		}
	case iotDPS.MatchString(id):
		sku, _ := body["sku"].(map[string]any)
		if name := stringOf(sku["name"]); name != "S1" {
			return bad("A provisioning service's SKU is S1, not '" + name + "'.")
		}
		hubs, _ := props["iotHubs"].([]any)
		for _, h := range hubs {
			hm, _ := h.(map[string]any)
			if stringOf(hm["location"]) == "" || (stringOf(hm["connectionString"]) == "" && stringOf(hm["authenticationType"]) != "UserAssigned" && stringOf(hm["authenticationType"]) != "SystemAssigned") {
				return bad("A linked IoT hub has a location and, unless it is reached with a managed identity, a connection string.")
			}
		}
	}
	return 0, "", ""
}
