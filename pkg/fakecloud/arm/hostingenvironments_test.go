// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// An App Service Environment names a network that exists; its custom DNS
// suffix comes with a certificate.
func TestHostingEnvironments(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/ase"
	subnet := rg + "/providers/Microsoft.Network/virtualNetworks/net/subnets/ase"
	a.AddResource(rg)
	ase := rg + "/providers/Microsoft.Web/hostingEnvironments/ase"
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	put("an environment with no network", ase, `{"location":"westus","kind":"ASEv3","properties":{}}`, http.StatusBadRequest)
	put("an environment in a subnet that is not there", ase, `{"location":"westus","kind":"ASEv3","properties":{"virtualNetwork":{"id":"`+subnet+`"}}}`, http.StatusNotFound)
	a.AddResource(rg + "/providers/Microsoft.Network/virtualNetworks/net")
	a.AddResource(subnet)
	put("an environment", ase, `{"location":"westus","kind":"ASEv3","properties":{"virtualNetwork":{"id":"`+subnet+`"}}}`, http.StatusCreated)
	put("a suffix with no certificate", ase+"/configurations/customdnssuffix", `{"properties":{"dnsSuffix":"apps.contoso.com"}}`, http.StatusBadRequest)
	put("a custom DNS suffix", ase+"/configurations/customdnssuffix",
		`{"properties":{"dnsSuffix":"apps.contoso.com","certificateUrl":"https://kv.vault.azure.net/secrets/apps"}}`, http.StatusCreated)
	put("the networking configuration", ase+"/configurations/networking", `{"properties":{"ftpEnabled":false,"allowNewPrivateEndpointConnections":true}}`, http.StatusCreated)
}
