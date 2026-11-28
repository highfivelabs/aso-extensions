// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// A connector and a blob access point configuration keep their source type;
// a new data share has its access policies and assets.
func TestStorageNewcomers(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/data"
	account := rg + "/providers/Microsoft.Storage/storageAccounts/lake"
	for _, id := range []string{rg, account} {
		a.AddResource(id)
	}
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	put("a data share without assets", account+"/dataShares/reports", `{"properties":{"accessPolicies":[]}}`, http.StatusBadRequest)
	put("a data share", account+"/dataShares/reports", `{"properties":{"accessPolicies":[],"assets":[{"assetPath":"reports/2026","displayName":"reports"}]}}`, http.StatusCreated)
	put("a data share's description changed", account+"/dataShares/reports", `{"properties":{"description":"monthly"}}`, http.StatusOK)
	connector := account + "/connectors/partner"
	put("a connector", connector, `{"properties":{"dataSourceType":"Azure_DataShare","source":{"type":"DataShare"}}}`, http.StatusCreated)
	put("a connector's source type changed", connector, `{"properties":{"dataSourceType":"Azure_DataShare","source":{"type":"Other"}}}`, http.StatusBadRequest)
	point := account + "/blobAccessPointConfigurations/nas"
	put("an access point", point, `{"properties":{"source":{"sourceType":"S3Compatible"}}}`, http.StatusCreated)
	put("an access point's source type changed", point, `{"properties":{"source":{"sourceType":"Qumulo"}}}`, http.StatusBadRequest)
	put("an access point changed", point, `{"properties":{"state":"Inactive","source":{"sourceType":"S3Compatible"}}}`, http.StatusOK)
}
