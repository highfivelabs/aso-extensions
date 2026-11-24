// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"strings"
	"testing"

	asobatch "github.com/Azure/azure-service-operator/v2/api/batch/v20240701/storage"

	batch "github.com/highfivelabs/aso-extensions/apis/arm/batch/v20250601"
)

const batchAPI = "2025-06-01"

// An application and a pool of zero nodes on a Batch account made as a
// fixture standing in for ASO's BatchAccount (docs/plan.md f73): created,
// the pool's target changed (still zero low-priority nodes), deleted. Free:
// Batch bills the nodes, and none is allocated; a Batch account costs
// nothing by itself.
func TestLiveBatchPool(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.Batch")
	ns := namespace(t, "batch")
	name := strings.ReplaceAll(azName("batch"), "-", "")
	accountID := rgID() + "/providers/Microsoft.Batch/batchAccounts/" + name
	fixture(t, accountID, batchAPI, map[string]any{
		"location": location, "properties": map[string]any{"poolAllocationMode": "BatchService", "publicNetworkAccess": "Enabled"},
	})
	account := &asobatch.BatchAccount{}
	account.Namespace, account.Name = ns, "jobs"
	account.Spec.AzureName = name
	account.Spec.Location = ptr(location)
	account.Spec.Owner = owner("rg")
	standIn(t, account, accountID, nil)

	app := &batch.BatchAccountsApplication{}
	app.Namespace, app.Name = ns, "render"
	app.Spec = batch.BatchAccountsApplication_Spec{AzureName: "render", Owner: owner("jobs"), DisplayName: ptr("Render"), AllowUpdates: ptr(true)}
	pool := &batch.BatchAccountsPool{}
	pool.Namespace, pool.Name = ns, "render-pool"
	pool.Spec = batch.BatchAccountsPool_Spec{
		AzureName: "render-pool", Owner: owner("jobs"), VmSize: ptr("STANDARD_D2S_V3"),
		DeploymentConfiguration: &batch.DeploymentConfiguration{VirtualMachineConfiguration: &batch.VirtualMachineConfiguration{
			ImageReference: &batch.ImageReference{Publisher: ptr("canonical"), Offer: ptr("ubuntu-24_04-lts"), Sku: ptr("server")},
			NodeAgentSkuId: ptr("batch.node.ubuntu 24.04"),
		}},
		ScaleSettings: &batch.ScaleSettings{FixedScale: &batch.FixedScaleSettings{TargetDedicatedNodes: ptr(0), TargetLowPriorityNodes: ptr(0)}},
		Metadata:      []batch.MetadataItem{{Name: ptr("purpose"), Value: ptr("live test")}},
	}
	create(t, timeout, app, pool)

	poolID := accountID + "/pools/render-pool"
	expectResource(t, poolID, batchAPI, func(r map[string]any) error {
		if p := properties(r); !strings.EqualFold(fmt.Sprint(p["vmSize"]), "STANDARD_D2S_V3") {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, pool, func(p *batch.BatchAccountsPool) {
		p.Spec.Metadata = []batch.MetadataItem{{Name: ptr("purpose"), Value: ptr("live test, changed")}}
	})
	eventually(t, timeout, "the pool Ready after the change", ready(pool))
	expectResource(t, poolID, batchAPI, func(r map[string]any) error {
		items, _ := properties(r)["metadata"].([]any)
		if len(items) != 1 || !strings.Contains(fmt.Sprint(items[0]), "changed") {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, pool)
	expectGone(t, poolID, batchAPI)
	remove(t, timeout, app)
	expectGone(t, accountID+"/applications/render", batchAPI)
}
