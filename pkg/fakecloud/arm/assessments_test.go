// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// Custom assessment metadata keys and assessments naming them.
func TestCustomAssessments(t *testing.T) {
	a := New(func(string) bool { return true })
	sub := "/subscriptions/" + sub1
	rg := sub + "/resourceGroups/app"
	a.AddResource(rg)
	vm := rg + "/providers/Microsoft.Compute/virtualMachines/vm"
	a.AddResource(vm)
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	const key = "00000000-0000-0000-0000-0000000000a1"
	metadata := sub + "/providers/Microsoft.Security/assessmentMetadata/"
	assessments := vm + "/providers/Microsoft.Security/assessments/"
	body := `{"properties":{"displayName":"Patched","assessmentType":"CustomerManaged","severity":"Medium"}}`
	put("metadata named by a word", metadata+"patched", body, http.StatusBadRequest)
	put("an assessment with no metadata", assessments+key, `{"properties":{"status":{"code":"Healthy"},"resourceDetails":{"source":"Azure"}}}`, http.StatusNotFound)
	put("metadata", metadata+key, body, http.StatusCreated)
	put("an assessment with no status", assessments+key, `{"properties":{"resourceDetails":{"source":"Azure"}}}`, http.StatusBadRequest)
	put("an assessment", assessments+key, `{"properties":{"status":{"code":"Healthy"},"resourceDetails":{"source":"Azure"}}}`, http.StatusCreated)
}
