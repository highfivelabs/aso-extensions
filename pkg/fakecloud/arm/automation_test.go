// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// An Automation account's hybrid service URL is computed, and its agent
// registration information (the DSC server endpoint) is read beside it, for
// the account's GUID and region; for an account that is not there, 404.
func TestAutomationAgentRegistration(t *testing.T) {
	a := New(func(string) bool { return true })
	a.AddResource(computedRG)
	acct := computedRG + "/providers/Microsoft.Automation/automationAccounts/aa1"

	code, body := do(t, a, http.MethodGet, acct+"/agentRegistrationInformation", "")
	expect(t, "registration of a missing account", code, body, http.StatusNotFound)

	code, body = do(t, a, http.MethodPut, acct, `{"location":"East US 2","properties":{"sku":{"name":"Basic"}}}`)
	expect(t, "account", code, body, http.StatusCreated)
	hybrid, _ := props(t, body)["automationHybridServiceUrl"].(string)
	if !strings.HasPrefix(hybrid, "https://") || !strings.Contains(hybrid, ".jrds.eastus2.azure-automation.net/automationAccounts/") {
		t.Errorf("automationHybridServiceUrl %q", hybrid)
	}
	guid := strings.TrimPrefix(strings.SplitN(hybrid, ".", 2)[0], "https://")

	code, body = do(t, a, http.MethodGet, acct+"/agentRegistrationInformation", "")
	expect(t, "registration", code, body, http.StatusOK)
	var reg struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			Primary string `json:"primary"`
		} `json:"keys"`
	}
	if err := json.Unmarshal([]byte(body), &reg); err != nil {
		t.Fatal(err)
	}
	if want := "https://" + guid + ".agentsvc.eastus2.azure-automation.net/accounts/" + guid; reg.Endpoint != want {
		t.Errorf("endpoint %q, want %q", reg.Endpoint, want)
	}
	if reg.Keys.Primary == "" {
		t.Errorf("no primary key: %s", body)
	}
	code, body = do(t, a, http.MethodPut, acct+"/agentRegistrationInformation", `{}`)
	expect(t, "registration is read-only", code, body, http.StatusMethodNotAllowed)
}
