// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// A managed instance's Key Vault key gives its URI and the protector names
// one that exists; assessments give their storage; Entra-only
// authentication waits for an Entra admin; a pool's subnet exists.
func TestSQLManagedInstance(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/mi"
	mi := rg + "/providers/Microsoft.Sql/managedInstances/mi"
	for _, id := range []string{rg, mi, mi + "/databases/app"} {
		a.AddResource(id)
	}
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	key := "vault_tde_1"
	put("a Key Vault key with no URI", mi+"/keys/"+key, `{"properties":{"serverKeyType":"AzureKeyVault"}}`, http.StatusBadRequest)
	put("a protector naming a key that is not there", mi+"/encryptionProtector/current",
		`{"properties":{"serverKeyType":"AzureKeyVault","serverKeyName":"`+key+`"}}`, http.StatusNotFound)
	put("a Key Vault key", mi+"/keys/"+key, `{"properties":{"serverKeyType":"AzureKeyVault","uri":"https://vault.vault.azure.net/keys/tde/0123456789abcdef"}}`, http.StatusCreated)
	put("the protector", mi+"/encryptionProtector/current", `{"properties":{"serverKeyType":"AzureKeyVault","serverKeyName":"`+key+`"}}`, http.StatusCreated)
	put("a service-managed protector", mi+"/encryptionProtector/current", `{"properties":{"serverKeyType":"ServiceManaged"}}`, http.StatusOK)
	for _, scope := range []string{mi, mi + "/databases/app"} {
		va := scope + "/vulnerabilityAssessments/default"
		put("an assessment with no container", va, `{"properties":{"storageAccountAccessKey":"k"}}`, http.StatusBadRequest)
		put("an assessment with no key or SAS", va, `{"properties":{"storageContainerPath":"https://st.blob.core.windows.net/va/"}}`, http.StatusBadRequest)
		put("an assessment", va, `{"properties":{"storageContainerPath":"https://st.blob.core.windows.net/va/","storageContainerSasKey":"sv=1&sig=x"}}`, http.StatusCreated)
	}
	put("Entra-only before an Entra admin", mi+"/azureADOnlyAuthentications/Default", `{"properties":{"azureADOnlyAuthentication":true}}`, http.StatusBadRequest)
	put("Entra-only off", mi+"/azureADOnlyAuthentications/Default", `{"properties":{"azureADOnlyAuthentication":false}}`, http.StatusCreated)
	put("an Entra admin", mi+"/administrators/ActiveDirectory",
		`{"properties":{"administratorType":"ActiveDirectory","login":"dba-group","sid":"00000000-0000-0000-0000-000000000001"}}`, http.StatusCreated)
	put("Entra-only", mi+"/azureADOnlyAuthentications/Default", `{"properties":{"azureADOnlyAuthentication":true}}`, http.StatusOK)
	subnet := rg + "/providers/Microsoft.Network/virtualNetworks/net/subnets/pool"
	pool := rg + "/providers/Microsoft.Sql/instancePools/pool"
	put("a pool in a subnet that is not there", pool, `{"location":"westus","sku":{"name":"GP_Gen5","tier":"GeneralPurpose","family":"Gen5"},"properties":{"subnetId":"`+subnet+`","vCores":8,"licenseType":"LicenseIncluded"}}`, http.StatusNotFound)
	a.AddResource(subnet)
	put("a pool", pool, `{"location":"westus","sku":{"name":"GP_Gen5","tier":"GeneralPurpose","family":"Gen5"},"properties":{"subnetId":"`+subnet+`","vCores":8,"licenseType":"LicenseIncluded"}}`, http.StatusCreated)
}
