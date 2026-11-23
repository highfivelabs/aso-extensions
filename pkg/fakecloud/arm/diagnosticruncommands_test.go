// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// A diagnostic run command has exactly one source, and a gallery script
// version it names exists.
func TestDiagnosticRunCommand(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/vm"
	vm := rg + "/providers/Microsoft.Compute/virtualMachines/app"
	script := rg + "/providers/Microsoft.Compute/galleries/g/scripts/collect/versions/1.0.0"
	for _, id := range []string{rg, vm, script} {
		a.AddResource(id)
	}
	put := func(what, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, vm+"/diagnosticRunCommands/collect", body)
		expect(t, what, code, resp, want)
	}
	put("no source", `{"location":"westus","properties":{"source":{}}}`, http.StatusBadRequest)
	put("two sources", `{"location":"westus","properties":{"source":{"script":"echo","commandId":"RunShellScript"}}}`, http.StatusBadRequest)
	put("a missing gallery script", `{"location":"westus","properties":{"source":{"galleryScriptReferenceId":"`+rg+`/providers/Microsoft.Compute/galleries/g/scripts/none/versions/1.0.0"}}}`, http.StatusNotFound)
	put("a gallery script", `{"location":"westus","properties":{"source":{"galleryScriptReferenceId":"`+script+`"}}}`, http.StatusCreated)
}
