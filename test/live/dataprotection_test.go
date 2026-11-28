// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"strings"
	"testing"

	asodataprotection "github.com/Azure/azure-service-operator/v2/api/dataprotection/v20231101/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	dp "github.com/highfivelabs/aso-extensions/apis/arm/dataprotection/v20260701"
)

const dataProtectionAPI = "2026-07-01"

// A resource guard and the guard proxy of an empty backup vault, a fixture
// standing in for ASO's BackupVault (docs/plan.md f70): created, the
// guard's exclusions changed, the proxy deleted after its unlockDelete (as
// the Recovery Services proxy's live run of 2026-11-12 showed Azure wants),
// then the guard. Free: a guard and an empty vault bill nothing.
func TestLiveDataProtectionResourceGuard(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.DataProtection")
	ns := namespace(t, "dpguard")
	vaultName := azName("bv")
	vaultID := rgID() + "/providers/Microsoft.DataProtection/backupVaults/" + vaultName
	fixture(t, vaultID, "2023-11-01", map[string]any{
		"location": location, "identity": map[string]any{"type": "None"},
		"properties": map[string]any{"storageSettings": []any{map[string]any{"datastoreType": "VaultStore", "type": "LocallyRedundant"}}},
	})
	vault := &asodataprotection.BackupVault{}
	vault.Namespace, vault.Name = ns, "vault"
	vault.Spec.AzureName = vaultName
	vault.Spec.Location = ptr(location)
	vault.Spec.Owner = owner("rg")
	standIn(t, vault, vaultID, nil)

	guard := &dp.ResourceGuard{}
	guard.Namespace, guard.Name = ns, "mua"
	guard.Spec = dp.ResourceGuard_Spec{
		AzureName: azName("mua"), Owner: owner("rg"), Location: ptr(location),
		Properties: &dp.ResourceGuardSpec{VaultCriticalOperationExclusionList: []string{"Microsoft.DataProtection/backupVaults/backupInstances/write"}},
	}
	create(t, timeout, guard)
	guardID := rgID() + "/providers/Microsoft.DataProtection/resourceGuards/" + guard.Spec.AzureName
	proxy := &dp.BackupVaultsBackupResourceGuardProxy{}
	proxy.Namespace, proxy.Name = ns, "proxy"
	proxy.Spec = dp.BackupVaultsBackupResourceGuardProxy_Spec{
		AzureName: "DppResourceGuardProxy", Owner: owner("vault"),
		Properties: &dp.ResourceGuardProxyBase{ResourceGuardResourceReference: &genruntime.ResourceReference{
			Group: "dataprotection.azure.hfl.dev", Kind: "ResourceGuard", Name: "mua",
		}},
	}
	create(t, timeout, proxy)
	proxyID := vaultID + "/backupResourceGuardProxies/DppResourceGuardProxy"
	expectResource(t, proxyID, dataProtectionAPI, func(r map[string]any) error {
		if !strings.EqualFold(fmt.Sprint(properties(r)["resourceGuardResourceId"]), guardID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, guard, func(g *dp.ResourceGuard) { g.Spec.Properties.VaultCriticalOperationExclusionList = nil })
	eventually(t, timeout, "the guard Ready after the change", ready(guard))
	remove(t, timeout, proxy)
	expectGone(t, proxyID, dataProtectionAPI)
	remove(t, timeout, guard)
	expectGone(t, guardID, dataProtectionAPI)
}
