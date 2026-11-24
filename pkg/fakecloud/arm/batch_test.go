// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// A Batch pool has a VM size and a virtual machine configuration, fixed or
// automatic scaling, a subnet and application packages that exist.
func TestBatchPool(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/batch"
	account := rg + "/providers/Microsoft.Batch/batchAccounts/jobs"
	other := rg + "/providers/Microsoft.Batch/batchAccounts/other"
	subnet := rg + "/providers/Microsoft.Network/virtualNetworks/vnet/subnets/pools"
	for _, id := range []string{rg, account, other, other + "/applications/render", subnet} {
		a.AddResource(id)
	}
	put := func(what, id, props string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, id, `{"properties":`+props+`}`)
		expect(t, what, code, resp, want)
	}
	vm := `"deploymentConfiguration":{"virtualMachineConfiguration":{"imageReference":{"publisher":"canonical","offer":"ubuntu-24_04-lts","sku":"server"},"nodeAgentSkuId":"batch.node.ubuntu 24.04"}}`
	pool := account + "/pools/render"
	put("an application", account+"/applications/render", `{"displayName":"Render","allowUpdates":true}`, http.StatusCreated)
	put("a pool with no VM size", pool, `{`+vm+`}`, http.StatusBadRequest)
	put("a pool with no VM configuration", pool, `{"vmSize":"STANDARD_D2S_V3"}`, http.StatusBadRequest)
	put("a pool with no node agent", pool, `{"vmSize":"STANDARD_D2S_V3","deploymentConfiguration":{"virtualMachineConfiguration":{"imageReference":{"publisher":"canonical"}}}}`, http.StatusBadRequest)
	put("a pool scaling both ways", pool, `{"vmSize":"STANDARD_D2S_V3",`+vm+`,"scaleSettings":{"fixedScale":{"targetDedicatedNodes":0},"autoScale":{"formula":"$TargetDedicatedNodes=0;"}}}`, http.StatusBadRequest)
	put("an autoscale with no formula", pool, `{"vmSize":"STANDARD_D2S_V3",`+vm+`,"scaleSettings":{"autoScale":{"evaluationInterval":"PT5M"}}}`, http.StatusBadRequest)
	put("no task slots", pool, `{"vmSize":"STANDARD_D2S_V3",`+vm+`,"taskSlotsPerNode":0}`, http.StatusBadRequest)
	put("a pool in a missing subnet", pool, `{"vmSize":"STANDARD_D2S_V3",`+vm+`,"networkConfiguration":{"subnetId":"`+subnet+`x"}}`, http.StatusNotFound)
	put("another account's package", pool, `{"vmSize":"STANDARD_D2S_V3",`+vm+`,"applicationPackages":[{"id":"`+other+`/applications/render"}]}`, http.StatusBadRequest)
	put("a missing package", pool, `{"vmSize":"STANDARD_D2S_V3",`+vm+`,"applicationPackages":[{"id":"`+account+`/applications/encode"}]}`, http.StatusNotFound)
	put("a pool", pool, `{"vmSize":"STANDARD_D2S_V3",`+vm+`,"scaleSettings":{"fixedScale":{"targetDedicatedNodes":0}},"networkConfiguration":{"subnetId":"`+subnet+`"},"applicationPackages":[{"id":"`+account+`/applications/render/versions/1.0"}]}`, http.StatusCreated)
}
