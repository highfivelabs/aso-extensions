// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"testing"
)

// What an Operations Management solution must give: a workspace that
// exists, a plan, and for a Microsoft gallery solution the name and plan
// Azure expects.
func TestOperationsManagementSolution(t *testing.T) {
	a := New(func(string) bool { return true })
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	rg := "/subscriptions/" + sub1 + "/resourceGroups/logs"
	put("resource group", rg, `{"location":"westus"}`, http.StatusCreated)
	ws := rg + "/providers/Microsoft.OperationalInsights/workspaces/law"
	solutions := rg + "/providers/Microsoft.OperationsManagement/solutions/"
	solution := func(workspace, plan, extra string) string {
		return `{"location":"westus","plan":` + plan + `,"properties":{"workspaceResourceId":"` + workspace + `"` + extra + `}}`
	}
	gallery := `{"name":"Updates(law)","publisher":"Microsoft","product":"OMSGallery/Updates","promotionCode":""}`

	put("solution without a workspace", solutions+"Updates(law)", `{"location":"westus","plan":`+gallery+`,"properties":{}}`, http.StatusBadRequest)
	put("solution on a missing workspace", solutions+"Updates(law)", solution(ws, gallery, ""), http.StatusNotFound)
	a.AddResource(ws)
	put("solution without a plan", solutions+"Updates(law)", `{"location":"westus","properties":{"workspaceResourceId":"`+ws+`"}}`, http.StatusBadRequest)
	put("gallery solution named for another workspace", solutions+"Updates(other)", solution(ws, `{"name":"Updates(other)","publisher":"Microsoft","product":"OMSGallery/Updates"}`, ""), http.StatusBadRequest)
	put("gallery solution whose plan has another name", solutions+"Updates(law)", solution(ws, `{"name":"Security(law)","publisher":"Microsoft","product":"OMSGallery/Updates"}`, ""), http.StatusBadRequest)
	put("gallery solution of another product", solutions+"Updates(law)", solution(ws, `{"name":"Updates(law)","publisher":"Microsoft","product":"OMSGallery/Security"}`, ""), http.StatusBadRequest)
	put("solution containing a missing resource", solutions+"Updates(law)", solution(ws, gallery, `,"containedResources":["`+rg+`/providers/Microsoft.Automation/automationAccounts/none"]`), http.StatusNotFound)
	put("gallery solution", solutions+"Updates(law)", solution(ws, gallery, ""), http.StatusCreated)
	put("third-party solution", solutions+"anything", solution(ws, `{"name":"anything","publisher":"Contoso","product":"contoso-monitor"}`, ""), http.StatusCreated)
}
