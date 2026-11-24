// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// A MySQL flexible server's advanced threat protection setting
// (Microsoft.DBforMySQL/flexibleServers/advancedThreatProtectionSettings,
// 2024-12-30; docs/plan.md f66), as far as the fake models it, from the
// spec: a server has one setting, Default (404 for any other name), whose
// state is Enabled or Disabled (400), and which has no DELETE (405): it
// goes with its server. The server's private endpoint connections are
// privateendpoints.go's. Not modelled: threats being detected, alerts, and
// the error codes (the fake's own).
var mysqlThreatProtection = regexp.MustCompile(`(?i)/providers/microsoft\.dbformysql/flexibleservers/[^/]+/advancedthreatprotectionsettings/([^/]+)$`)

// checkMySQL refuses a threat protection setting the rules above do not
// allow; code is "" when it is allowed or id is none.
func checkMySQL(id string, body map[string]any) (status int, code, message string) {
	m := mysqlThreatProtection.FindStringSubmatch(id)
	if m == nil {
		return 0, "", ""
	}
	if !strings.EqualFold(m[1], "Default") {
		return http.StatusNotFound, "ResourceNotFound", "A server has one advanced threat protection setting, Default; " + m[1] + " is not it."
	}
	props, _ := body["properties"].(map[string]any)
	if s := stringOf(props["state"]); s != "Enabled" && s != "Disabled" {
		return http.StatusBadRequest, "InvalidParameterValue", "The setting's state is Enabled or Disabled, not " + s + "."
	}
	return 0, "", ""
}
