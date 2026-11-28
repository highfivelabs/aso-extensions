// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// A MySQL flexible server has one threat protection setting, Default,
// Enabled or Disabled, which cannot be deleted; a connection exists only
// when a private endpoint made it.
func TestMySQLFlexibleServerChildren(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/db"
	server := rg + "/providers/Microsoft.DBforMySQL/flexibleServers/orders"
	for _, id := range []string{rg, server} {
		a.AddResource(id)
	}
	put := func(what, id, props string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, id, `{"properties":`+props+`}`)
		expect(t, what, code, resp, want)
	}
	atp := server + "/advancedThreatProtectionSettings/Default"
	put("a setting not named Default", server+"/advancedThreatProtectionSettings/other", `{"state":"Enabled"}`, http.StatusNotFound)
	put("a setting of no state", atp, `{}`, http.StatusBadRequest)
	put("the setting", atp, `{"state":"Enabled"}`, http.StatusCreated)
	put("the setting turned off", atp, `{"state":"Disabled"}`, http.StatusOK)
	code, resp := do(t, a, http.MethodDelete, atp, "")
	expect(t, "deleting the setting", code, resp, http.StatusMethodNotAllowed)
	put("a connection no private endpoint made", server+"/privateEndpointConnections/pe", `{"privateLinkServiceConnectionState":{"status":"Approved"}}`, http.StatusNotFound)
}
