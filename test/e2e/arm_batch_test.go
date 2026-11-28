// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	asobatch "github.com/Azure/azure-service-operator/v2/api/batch/v20240701/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	batch "github.com/highfivelabs/aso-extensions/apis/arm/batch/v20250601"
)

// The Batch kinds of docs/plan.md f73, on ASO's BatchAccount by name (a new
// external owner): an application, and a pool of zero nodes in a subnet
// that installs it, with a user account whose password is from a Secret. A
// pool scaling both ways and one installing another account's application
// are refused (the fake's rules, batch.go); a change of the pool's formula
// is PUT again; deleted, both go.
func TestBatchApplicationAndPool(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "batch")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	accountID := rgID + "/providers/Microsoft.Batch/batchAccounts/jobs"
	otherApp := rgID + "/providers/Microsoft.Batch/batchAccounts/other/applications/render"
	subnetID := rgID + "/providers/Microsoft.Network/virtualNetworks/batch/subnets/pools"
	for _, id := range []string{rgID, accountID, parentOf(parentOf(otherApp)), otherApp, parentOf(parentOf(subnetID)), subnetID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	account := &asobatch.BatchAccount{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "jobs"}}
	account.Spec.AzureName, account.Spec.Location = "jobs", ptr("westus")
	account.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, account, accountID)
	if err := k8s.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "batch-users"},
		StringData: map[string]string{"worker": "worker-password"},
	}); err != nil {
		t.Fatal(err)
	}
	onAccount := &genruntime.KnownResourceReference{Name: "jobs"}

	app := &batch.BatchAccountsApplication{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "render"},
		Spec:       batch.BatchAccountsApplication_Spec{Owner: onAccount, DisplayName: ptr("Render"), AllowUpdates: ptr(true)},
	}
	if err := k8s.Create(ctx, app); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the application Ready", ready(app))

	vm := func() *batch.DeploymentConfiguration {
		return &batch.DeploymentConfiguration{VirtualMachineConfiguration: &batch.VirtualMachineConfiguration{
			ImageReference: &batch.ImageReference{Publisher: ptr("canonical"), Offer: ptr("ubuntu-24_04-lts"), Sku: ptr("server")},
			NodeAgentSkuId: ptr("batch.node.ubuntu 24.04"),
		}}
	}
	pool := &batch.BatchAccountsPool{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "render-pool"},
		Spec: batch.BatchAccountsPool_Spec{
			Owner: onAccount, VmSize: ptr("STANDARD_D2S_V3"), DeploymentConfiguration: vm(),
			ScaleSettings:        &batch.ScaleSettings{AutoScale: &batch.AutoScaleSettings{Formula: ptr("$TargetDedicatedNodes = 0;"), EvaluationInterval: ptr("PT5M")}},
			NetworkConfiguration: &batch.NetworkConfiguration{SubnetReference: &genruntime.ResourceReference{ARMID: subnetID}},
			ApplicationPackages: []batch.ApplicationPackageReference{{Reference: &genruntime.ResourceReference{
				Group: "batch.azure.hfl.dev", Kind: "BatchAccountsApplication", Name: "render"}}},
			UserAccounts: []batch.UserAccount{{Name: ptr("worker"), Password: &genruntime.SecretReference{Name: "batch-users", Key: "worker"}}},
			StartTask:    &batch.StartTask{CommandLine: ptr("/bin/sh -c 'echo ready'"), WaitForSuccess: ptr(true)},
			Metadata:     []batch.MetadataItem{{Name: ptr("team"), Value: ptr("render")}},
		},
	}
	both := &batch.BatchAccountsPool{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "both-ways"},
		Spec: batch.BatchAccountsPool_Spec{
			Owner: onAccount, VmSize: ptr("STANDARD_D2S_V3"), DeploymentConfiguration: vm(),
			ScaleSettings: &batch.ScaleSettings{
				FixedScale: &batch.FixedScaleSettings{TargetDedicatedNodes: ptr(0)},
				AutoScale:  &batch.AutoScaleSettings{Formula: ptr("$TargetDedicatedNodes = 0;")},
			},
		},
	}
	foreign := &batch.BatchAccountsPool{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "foreign-package"},
		Spec: batch.BatchAccountsPool_Spec{
			Owner: onAccount, VmSize: ptr("STANDARD_D2S_V3"), DeploymentConfiguration: vm(),
			ApplicationPackages: []batch.ApplicationPackageReference{{Reference: &genruntime.ResourceReference{ARMID: otherApp}}},
		},
	}
	pools := []conditioned{pool, both, foreign}
	for _, o := range pools {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "the pool Ready", ready(pool))
	eventually(t, timeout, "a pool scaling both ways refused", notReadyWith(both, "not both"))
	eventually(t, timeout, "another account's package refused", notReadyWith(foreign, "the pool's account"))

	poolID := accountID + "/pools/render-pool"
	expectResource(t, poolID, func(r map[string]any) error {
		p := properties(r)
		network, _ := p["networkConfiguration"].(map[string]any)
		packages, _ := p["applicationPackages"].([]any)
		users, _ := p["userAccounts"].([]any)
		if network["subnetId"] != subnetID || len(packages) != 1 || len(users) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		if pkg, _ := packages[0].(map[string]any); pkg["id"] != accountID+"/applications/render" {
			return fmt.Errorf("package: %v", packages[0])
		}
		if user, _ := users[0].(map[string]any); user["password"] != "worker-password" {
			return fmt.Errorf("user: %v", users[0])
		}
		return nil
	})

	// A change is PUT again: the autoscale formula.
	formula := "$TargetDedicatedNodes = min(2, $PendingTasks.GetSample(1));"
	if err := change(t, pool, func(p *batch.BatchAccountsPool) { p.Spec.ScaleSettings.AutoScale.Formula = ptr(formula) }); err != nil {
		t.Fatal(err)
	}
	expectResource(t, poolID, func(r map[string]any) error {
		scale, _ := properties(r)["scaleSettings"].(map[string]any)
		auto, _ := scale["autoScale"].(map[string]any)
		if auto["formula"] != formula {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, objs := range [][]conditioned{pools, {app}} {
		for _, o := range objs {
			if err := k8s.Delete(ctx, o); err != nil {
				t.Fatal(err)
			}
		}
		for _, o := range objs {
			eventually(t, timeout, o.GetName()+" gone", gone(o))
		}
	}
	for _, id := range []string{poolID, accountID + "/applications/render"} {
		if armFake.HasResource(id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
}
