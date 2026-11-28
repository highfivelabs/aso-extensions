// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"os"
	"testing"

	asostorage "github.com/Azure/azure-service-operator/v2/api/storage/v20260601/storage"

	storage "github.com/highfivelabs/aso-extensions/apis/arm/storage/v20260601"
)

const storageNewcomersAPI = "2026-06-01"

// A data share of a folder of a StorageV2 account fixture (storage cents)
// to the live-test identity itself, read-only: made, its description
// changed, and deleted (docs/plan.md f48). Connectors (they need another
// tenant's share URI), blob access points (an S3-compatible store), the
// capacity metrics rule (billed as custom metrics) and context caches are not
// made here.
func TestLiveStorageDataShare(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.Storage")
	ns := namespace(t, "stshare")
	accountID, accountName := storageAccount(t, "share")
	account := &asostorage.StorageAccount{}
	account.Namespace, account.Name = ns, "account"
	account.Spec.AzureName, account.Spec.Owner = accountName, owner("rg")
	standIn(t, account, accountID, nil)

	share := &storage.StorageAccountsDataShare{}
	share.Namespace, share.Name = ns, "reports"
	share.Spec = storage.StorageAccountsDataShare_Spec{
		AzureName: "reports", Owner: owner("account"), Location: ptr(location),
		Properties: &storage.StorageDataShareProperties{
			Description: ptr("live test"),
			AccessPolicies: []storage.StorageDataShareAccessPolicy{{
				PrincipalId: ptr(identityObjectID(t)), TenantId: ptr(os.Getenv("AZURE_TENANT_ID")),
				Permission: ptr(storage.StorageDataShareAccessPolicyPermission_Read),
			}},
			Assets: []storage.StorageDataShareAsset{{AssetPath: ptr("reports/2026"), DisplayName: ptr("reports")}},
		},
	}
	create(t, timeout, share)
	shareID := accountID + "/dataShares/reports"
	expectResource(t, shareID, storageNewcomersAPI, func(r map[string]any) error {
		if assets, _ := properties(r)["assets"].([]any); len(assets) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, share, func(s *storage.StorageAccountsDataShare) { s.Spec.Properties.Description = ptr("live test, changed") })
	eventually(t, timeout, "the data share Ready after the change", ready(share))
	expectResource(t, shareID, storageNewcomersAPI, func(r map[string]any) error {
		if properties(r)["description"] != "live test, changed" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, share)
	expectGone(t, shareID, storageNewcomersAPI)
}
