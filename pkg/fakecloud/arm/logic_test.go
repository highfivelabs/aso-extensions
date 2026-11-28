// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// A workflow's definition names its schema and version; an integration
// account has a SKU; its schemas, maps, partners, agreements, certificates,
// batch configurations and assemblies carry what the spec requires, and an
// agreement names partners and identities that exist.
func TestLogicIntegrationAccount(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/logic"
	vault := rg + "/providers/Microsoft.KeyVault/vaults/b2b"
	a.AddResource(rg)
	a.AddResource(vault)
	put := func(what, id, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, id, body)
		expect(t, what, code, resp, want)
	}
	props := func(p string) string { return `{"properties":` + p + `}` }
	acct := rg + "/providers/Microsoft.Logic/integrationAccounts/b2b"
	wf := rg + "/providers/Microsoft.Logic/workflows/orders"
	def := `"definition":{"$schema":"https://schema.management.azure.com/providers/Microsoft.Logic/schemas/2016-06-01/workflowdefinition.json#","contentVersion":"1.0.0.0","triggers":{},"actions":{}}`

	put("a workflow whose definition has no schema", wf, `{"location":"westus","properties":{"definition":{"triggers":{}}}}`, http.StatusBadRequest)
	put("a workflow on a missing account", wf, `{"location":"westus","properties":{`+def+`,"integrationAccount":{"id":"`+acct+`"}}}`, http.StatusNotFound)
	put("an account with no SKU", acct, `{"location":"westus","properties":{}}`, http.StatusBadRequest)
	put("an account", acct, `{"location":"westus","sku":{"name":"Free"},"properties":{"state":"Enabled"}}`, http.StatusCreated)
	put("a workflow", wf, `{"location":"westus","properties":{"state":"Disabled",`+def+`,"integrationAccount":{"id":"`+acct+`"}}}`, http.StatusCreated)

	put("a schema with no content", acct+"/schemas/order", props(`{"schemaType":"Xml"}`), http.StatusBadRequest)
	put("a schema", acct+"/schemas/order", props(`{"schemaType":"Xml","content":"<xs:schema xmlns:xs=\"http://www.w3.org/2001/XMLSchema\"/>","contentType":"application/xml"}`), http.StatusCreated)
	put("a map with no type", acct+"/maps/order", props(`{"content":"{}"}`), http.StatusBadRequest)
	put("a map", acct+"/maps/order", props(`{"mapType":"Liquid","content":"{{ content }}","contentType":"text/plain"}`), http.StatusCreated)
	put("an assembly with no content", acct+"/assemblies/codec", props(`{"assemblyName":"Codec"}`), http.StatusBadRequest)

	partner := func(name, value string) string {
		return props(`{"partnerType":"B2B","content":{"b2b":{"businessIdentities":[{"qualifier":"ZZ","value":"` + value + `"}]}}}`)
	}
	put("a partner identity with no value", acct+"/partners/contoso", props(`{"partnerType":"B2B","content":{"b2b":{"businessIdentities":[{"qualifier":"ZZ"}]}}}`), http.StatusBadRequest)
	put("a host partner", acct+"/partners/contoso", partner("contoso", "CONTOSO"), http.StatusCreated)
	agreement := func(guest, guestValue, kind, content string) string {
		return props(`{"agreementType":"` + kind + `","hostPartner":"contoso","guestPartner":"` + guest + `","hostIdentity":{"qualifier":"ZZ","value":"CONTOSO"},"guestIdentity":{"qualifier":"ZZ","value":"` + guestValue + `"},"content":` + content + `}`)
	}
	put("an agreement with a missing guest", acct+"/agreements/x12", agreement("fabrikam", "FABRIKAM", "X12", `{"x12":{}}`), http.StatusNotFound)
	put("a guest partner", acct+"/partners/fabrikam", partner("fabrikam", "FABRIKAM"), http.StatusCreated)
	put("an agreement with another identity", acct+"/agreements/x12", agreement("fabrikam", "OTHER", "X12", `{"x12":{}}`), http.StatusBadRequest)
	put("an agreement with the wrong content", acct+"/agreements/x12", agreement("fabrikam", "FABRIKAM", "X12", `{"aS2":{}}`), http.StatusBadRequest)
	put("an agreement", acct+"/agreements/x12", agreement("fabrikam", "FABRIKAM", "X12", `{"x12":{}}`), http.StatusCreated)

	put("a certificate with nothing", acct+"/certificates/signing", props(`{}`), http.StatusBadRequest)
	put("a certificate in a missing vault", acct+"/certificates/signing", props(`{"key":{"keyName":"signing","keyVault":{"id":"`+vault+`x"}}}`), http.StatusNotFound)
	put("a certificate", acct+"/certificates/signing", props(`{"key":{"keyName":"signing","keyVault":{"id":"`+vault+`"}}}`), http.StatusCreated)
	put("a batch with no release criteria", acct+"/batchConfigurations/daily", props(`{"batchGroupName":"daily","releaseCriteria":{}}`), http.StatusBadRequest)
	put("a batch configuration", acct+"/batchConfigurations/daily", props(`{"batchGroupName":"daily","releaseCriteria":{"messageCount":10}}`), http.StatusCreated)
	put("a session", acct+"/sessions/run", props(`{"content":{"controlNumber":"1"}}`), http.StatusCreated)
}
