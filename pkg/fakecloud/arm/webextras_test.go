// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// An app's certificate names its source, an integration's gateway is
// primary, a static web app's basic authentication is default on a Standard
// plan with a scope and a password, a slot's source control names its
// repository, and a Kubernetes environment names a custom location.
func TestWebExtras(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/web"
	site := rg + "/providers/Microsoft.Web/sites/app"
	slot := site + "/slots/staging"
	vnetConn := site + "/virtualNetworkConnections/vnet"
	free, std := rg+"/providers/Microsoft.Web/staticSites/free", rg+"/providers/Microsoft.Web/staticSites/std"
	cl := rg + "/providers/Microsoft.ExtendedLocation/customLocations/arc"
	for _, id := range []string{rg, site, slot, vnetConn, cl} {
		a.AddResource(id)
	}
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	put("a Free static web app", free, `{"location":"westus2","sku":{"name":"Free"},"properties":{}}`, http.StatusCreated)
	put("a Standard static web app", std, `{"location":"westus2","sku":{"name":"Standard"},"properties":{}}`, http.StatusCreated)
	put("a certificate with no source", site+"/certificates/tls", `{"location":"westus","properties":{"password":"p"}}`, http.StatusBadRequest)
	put("a certificate from a PFX", site+"/certificates/tls", `{"location":"westus","properties":{"pfxBlob":"MIIC","password":"p"}}`, http.StatusCreated)
	put("a slot's certificate from Key Vault", slot+"/certificates/tls", `{"location":"westus","properties":{"keyVaultId":"`+rg+`/providers/Microsoft.KeyVault/vaults/kv","keyVaultSecretName":"tls"}}`, http.StatusCreated)
	put("a gateway not named primary", vnetConn+"/gateways/backup", `{"properties":{"vnetName":"vnet"}}`, http.StatusBadRequest)
	put("the primary gateway", vnetConn+"/gateways/primary", `{"properties":{"vnetName":"vnet","vpnPackageUri":"https://example.blob.core.windows.net/p.zip"}}`, http.StatusCreated)
	auth := `{"properties":{"applicableEnvironmentsMode":"AllEnvironments","password":"p"}}`
	put("basic auth not named default", std+"/basicAuth/other", auth, http.StatusBadRequest)
	put("basic auth on the Free plan", free+"/basicAuth/default", auth, http.StatusBadRequest)
	put("basic auth for no environment", std+"/basicAuth/default", `{"properties":{"applicableEnvironmentsMode":"SpecifiedEnvironments","password":"p"}}`, http.StatusBadRequest)
	put("basic auth without a password", std+"/basicAuth/default", `{"properties":{"applicableEnvironmentsMode":"StagingEnvironments"}}`, http.StatusBadRequest)
	put("basic auth", std+"/basicAuth/default", auth, http.StatusCreated)
	put("a slot's source control without a repository", slot+"/sourcecontrols/web", `{"properties":{"branch":"main"}}`, http.StatusBadRequest)
	put("a slot's source control", slot+"/sourcecontrols/web", `{"properties":{"repoUrl":"https://github.com/contoso/app","branch":"main","isManualIntegration":true}}`, http.StatusCreated)
	kube := rg + "/providers/Microsoft.Web/kubeEnvironments/arc"
	put("a Kubernetes environment without a custom location", kube, `{"location":"westus","properties":{}}`, http.StatusBadRequest)
	put("a Kubernetes environment on a missing custom location", kube, `{"location":"westus","extendedLocation":{"name":"`+rg+`/providers/Microsoft.ExtendedLocation/customLocations/none"},"properties":{}}`, http.StatusNotFound)
	put("a Kubernetes environment", kube, `{"location":"westus","extendedLocation":{"name":"`+cl+`"},"properties":{}}`, http.StatusCreated)
}
