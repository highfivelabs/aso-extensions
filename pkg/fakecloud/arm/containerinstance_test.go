// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// A container group profile has an OS type and containers with names, images
// and requests; an NGroup names profiles that exist and a count not below
// zero.
func TestContainerInstance(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/aci"
	a.AddResource(rg)
	put := func(what, id, props string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, id, `{"location":"westus","properties":`+props+`}`)
		expect(t, what, code, resp, want)
	}
	profile := rg + "/providers/Microsoft.ContainerInstance/containerGroupProfiles/web"
	web := `{"name":"web","properties":{"image":"mcr.microsoft.com/azuredocs/aci-helloworld","resources":{"requests":{"cpu":1,"memoryInGB":1.5}}}}`
	put("a profile with no OS type", profile, `{"containers":[`+web+`]}`, http.StatusBadRequest)
	put("a profile with no container", profile, `{"osType":"Linux","containers":[]}`, http.StatusBadRequest)
	put("two containers of one name", profile, `{"osType":"Linux","containers":[`+web+`,`+web+`]}`, http.StatusBadRequest)
	put("a container with no requests", profile, `{"osType":"Linux","containers":[{"name":"web","properties":{"image":"nginx"}}]}`, http.StatusBadRequest)
	put("a profile", profile, `{"osType":"Linux","sku":"Standard","containers":[`+web+`]}`, http.StatusCreated)

	ngroup := rg + "/providers/Microsoft.ContainerInstance/ngroups/web"
	put("a missing profile", ngroup, `{"containerGroupProfiles":[{"resource":{"id":"`+profile+`x"}}]}`, http.StatusNotFound)
	put("a negative count", ngroup, `{"elasticProfile":{"desiredCount":-1},"containerGroupProfiles":[{"resource":{"id":"`+profile+`"}}]}`, http.StatusBadRequest)
	put("an NGroup", ngroup, `{"elasticProfile":{"desiredCount":0},"containerGroupProfiles":[{"resource":{"id":"`+profile+`"}}]}`, http.StatusCreated)
}
