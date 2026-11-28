// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	asodataprotection "github.com/Azure/azure-service-operator/v2/api/dataprotection/v20231101/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	dp "github.com/highfivelabs/aso-extensions/apis/arm/dataprotection/v20260701"
)

// The Data Protection kinds of docs/plan.md f70: a resource guard (multi-user
// authorization) excluding one critical operation, and the guard proxy of
// ASO's BackupVault (by name, a new external owner) naming the guard by its
// object. A change to the guard is PUT again. Azure refuses the proxy's
// DELETE until an unlockDelete names it and the guard's delete request (the
// fake's rule, backup.go), so the kind unlocks before it deletes, as the
// Recovery Services proxy does; then the guard goes.
func TestDataProtectionResourceGuard(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "dpguard")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	vaultID := rgID + "/providers/Microsoft.DataProtection/backupVaults/blobs"
	for _, id := range []string{rgID, vaultID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	vault := &asodataprotection.BackupVault{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "blobs"}}
	vault.Spec.AzureName, vault.Spec.Location = "blobs", ptr("westus")
	vault.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, vault, vaultID)

	exclusion := "Microsoft.DataProtection/backupVaults/backupInstances/write"
	guard := &dp.ResourceGuard{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "mua"},
		Spec: dp.ResourceGuard_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "rg"}, Location: ptr("westus"),
			Properties: &dp.ResourceGuardSpec{VaultCriticalOperationExclusionList: []string{exclusion}},
		},
	}
	if err := k8s.Create(ctx, guard); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the guard Ready", ready(guard))
	proxy := &dp.BackupVaultsBackupResourceGuardProxy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "blobs-mua"},
		Spec: dp.BackupVaultsBackupResourceGuardProxy_Spec{
			AzureName: "DppResourceGuardProxy", Owner: &genruntime.KnownResourceReference{Name: "blobs"},
			Properties: &dp.ResourceGuardProxyBase{ResourceGuardResourceReference: &genruntime.ResourceReference{
				Group: "dataprotection.azure.hfl.dev", Kind: "ResourceGuard", Name: "mua",
			}},
		},
	}
	if err := k8s.Create(ctx, proxy); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the proxy Ready", ready(proxy))
	guardID := rgID + "/providers/Microsoft.DataProtection/resourceGuards/mua"
	proxyID := vaultID + "/backupResourceGuardProxies/DppResourceGuardProxy"
	expectResource(t, proxyID, func(r map[string]any) error {
		if properties(r)["resourceGuardResourceId"] != guardID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again: no exclusion.
	if err := change(t, guard, func(g *dp.ResourceGuard) { g.Spec.Properties.VaultCriticalOperationExclusionList = []string{} }); err != nil {
		t.Fatal(err)
	}
	expectResource(t, guardID, func(r map[string]any) error {
		if l, _ := properties(r)["vaultCriticalOperationExclusionList"].([]any); !reflect.DeepEqual(l, []any{}) && l != nil {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range []conditioned{proxy, guard} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, id := range []string{proxyID, guardID} {
		if armFake.HasResource(id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
}
