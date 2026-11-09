// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	asoapp "github.com/Azure/azure-service-operator/v2/api/app/v20250101/storage"
	asocompute "github.com/Azure/azure-service-operator/v2/api/compute/v20220301/storage"
	asoregistry "github.com/Azure/azure-service-operator/v2/api/containerregistry/v20251101/storage"
	asopostgresql "github.com/Azure/azure-service-operator/v2/api/dbforpostgresql/v20250801/storage"
	asokeyvault "github.com/Azure/azure-service-operator/v2/api/keyvault/v1api20230701/storage"
	asoworkspace "github.com/Azure/azure-service-operator/v2/api/operationalinsights/v20250701/storage"
	asoresources "github.com/Azure/azure-service-operator/v2/api/resources/v1api20200601/storage"
	asoweb "github.com/Azure/azure-service-operator/v2/api/web/v20260715/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
)

type conditioned interface {
	client.Object
	GetConditions() conditions.Conditions
}

// ready is nil when obj's Ready condition is True for its current generation.
func ready(obj conditioned) func() error {
	return func() error {
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(obj), obj); err != nil {
			return err
		}
		for _, c := range obj.GetConditions() {
			if c.Type == conditions.ConditionTypeReady {
				if c.Status == metav1.ConditionTrue && c.ObservedGeneration == obj.GetGeneration() {
					return nil
				}
				return fmt.Errorf("Ready=%s reason=%s: %s", c.Status, c.Reason, c.Message)
			}
		}
		return fmt.Errorf("no Ready condition yet")
	}
}

// notReadyWith is nil when obj's Ready condition is False and its message
// contains text.
func notReadyWith(obj conditioned, text string) func() error {
	return func() error {
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(obj), obj); err != nil {
			return err
		}
		for _, c := range obj.GetConditions() {
			if c.Type == conditions.ConditionTypeReady {
				if c.Status != metav1.ConditionTrue && contains(c.Message, text) {
					return nil
				}
				return fmt.Errorf("Ready=%s reason=%s: %s", c.Status, c.Reason, c.Message)
			}
		}
		return fmt.Errorf("no Ready condition yet")
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})())
}

// flexibleServer stands in for an ASO FlexibleServer ASO has provisioned:
// its status names the fake PostgreSQL's host.
func flexibleServer(t *testing.T, ns, name string) {
	t.Helper()
	ctx := context.Background()
	fs := &asopostgresql.FlexibleServer{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
	fs.Spec.AzureName = name
	fs.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	if err := k8s.Create(ctx, fs); err != nil {
		t.Fatal(err)
	}
	host := "localhost"
	fs.Status.FullyQualifiedDomainName = &host
	fs.Status.Conditions = []conditions.Condition{asoReady(fs.Generation)}
	if err := k8s.Status().Update(ctx, fs); err != nil {
		t.Fatal(err)
	}
}

// keyVault stands in for an ASO Vault ASO has provisioned: its status names
// the fake vault.
func keyVault(t *testing.T, ns, name string) {
	t.Helper()
	ctx := context.Background()
	v := &asokeyvault.Vault{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
	v.Spec.AzureName = name
	v.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	if err := k8s.Create(ctx, v); err != nil {
		t.Fatal(err)
	}
	uri := vaultURL + "/"
	v.Status.Properties = &asokeyvault.VaultProperties_STATUS{VaultUri: &uri}
	v.Status.Conditions = []conditions.Condition{asoReady(v.Generation)}
	if err := k8s.Status().Update(ctx, v); err != nil {
		t.Fatal(err)
	}
}

// resourceGroup stands in for an ASO ResourceGroup ASO has provisioned, with
// the ARM ID annotation ASO writes on what it provisions.
func resourceGroup(t *testing.T, ns, name, azureName string) {
	t.Helper()
	ctx := context.Background()
	rg := &asoresources.ResourceGroup{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name,
		Annotations: map[string]string{genruntime.ResourceIDAnnotation: "/subscriptions/" + subID + "/resourceGroups/" + azureName}}}
	rg.Spec.AzureName = azureName
	rg.Spec.Location = new(string)
	*rg.Spec.Location = "westus"
	if err := k8s.Create(ctx, rg); err != nil {
		t.Fatal(err)
	}
	rg.Status.Conditions = []conditions.Condition{asoReady(rg.Generation)}
	if err := k8s.Status().Update(ctx, rg); err != nil {
		t.Fatal(err)
	}
}

// managedEnvironment stands in for an ASO app.azure.com ManagedEnvironment
// ASO has provisioned, in the resource group rg.
func managedEnvironment(t *testing.T, ns, name, rg, rgAzureName, azureName string) {
	t.Helper()
	ctx := context.Background()
	env := &asoapp.ManagedEnvironment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name,
		Annotations: map[string]string{genruntime.ResourceIDAnnotation: "/subscriptions/" + subID + "/resourceGroups/" + rgAzureName +
			"/providers/Microsoft.App/managedEnvironments/" + azureName}}}
	env.Spec.AzureName = azureName
	env.Spec.Owner = &genruntime.KnownResourceReference{Name: rg}
	if err := k8s.Create(ctx, env); err != nil {
		t.Fatal(err)
	}
	env.Status.Conditions = []conditions.Condition{asoReady(env.Generation)}
	if err := k8s.Status().Update(ctx, env); err != nil {
		t.Fatal(err)
	}
}

// webSite stands in for an ASO web.azure.com Site ASO has provisioned, in
// the resource group rg.
func webSite(t *testing.T, ns, name, rg, rgAzureName, azureName string) {
	t.Helper()
	ctx := context.Background()
	site := &asoweb.Site{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name,
		Annotations: map[string]string{genruntime.ResourceIDAnnotation: "/subscriptions/" + subID + "/resourceGroups/" + rgAzureName +
			"/providers/Microsoft.Web/sites/" + azureName}}}
	site.Spec.AzureName = azureName
	site.Spec.Owner = &genruntime.KnownResourceReference{Name: rg}
	if err := k8s.Create(ctx, site); err != nil {
		t.Fatal(err)
	}
	site.Status.Conditions = []conditions.Condition{asoReady(site.Generation)}
	if err := k8s.Status().Update(ctx, site); err != nil {
		t.Fatal(err)
	}
}

// logAnalyticsWorkspace stands in for an ASO operationalinsights.azure.com
// Workspace ASO has provisioned, in the resource group rg.
func logAnalyticsWorkspace(t *testing.T, ns, name, rg, rgAzureName, azureName string) {
	t.Helper()
	ctx := context.Background()
	ws := &asoworkspace.Workspace{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name,
		Annotations: map[string]string{genruntime.ResourceIDAnnotation: "/subscriptions/" + subID + "/resourceGroups/" + rgAzureName +
			"/providers/Microsoft.OperationalInsights/workspaces/" + azureName}}}
	ws.Spec.AzureName = azureName
	ws.Spec.Owner = &genruntime.KnownResourceReference{Name: rg}
	if err := k8s.Create(ctx, ws); err != nil {
		t.Fatal(err)
	}
	ws.Status.Conditions = []conditions.Condition{asoReady(ws.Generation)}
	if err := k8s.Status().Update(ctx, ws); err != nil {
		t.Fatal(err)
	}
}

// containerRegistry stands in for an ASO containerregistry.azure.com Registry
// ASO has provisioned, in the resource group rg.
func containerRegistry(t *testing.T, ns, name, rg, rgAzureName, azureName string) {
	t.Helper()
	ctx := context.Background()
	reg := &asoregistry.Registry{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name,
		Annotations: map[string]string{genruntime.ResourceIDAnnotation: "/subscriptions/" + subID + "/resourceGroups/" + rgAzureName +
			"/providers/Microsoft.ContainerRegistry/registries/" + azureName}}}
	reg.Spec.AzureName = azureName
	reg.Spec.Owner = &genruntime.KnownResourceReference{Name: rg}
	if err := k8s.Create(ctx, reg); err != nil {
		t.Fatal(err)
	}
	reg.Status.Conditions = []conditions.Condition{asoReady(reg.Generation)}
	if err := k8s.Status().Update(ctx, reg); err != nil {
		t.Fatal(err)
	}
}

// virtualMachine stands in for an ASO compute.azure.com VirtualMachine ASO
// has provisioned, in the resource group rg.
func virtualMachine(t *testing.T, ns, name, rg, rgAzureName, azureName string) {
	t.Helper()
	ctx := context.Background()
	vm := &asocompute.VirtualMachine{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name,
		Annotations: map[string]string{genruntime.ResourceIDAnnotation: "/subscriptions/" + subID + "/resourceGroups/" + rgAzureName +
			"/providers/Microsoft.Compute/virtualMachines/" + azureName}}}
	vm.Spec.AzureName = azureName
	vm.Spec.Owner = &genruntime.KnownResourceReference{Name: rg}
	if err := k8s.Create(ctx, vm); err != nil {
		t.Fatal(err)
	}
	vm.Status.Conditions = []conditions.Condition{asoReady(vm.Generation)}
	if err := k8s.Status().Update(ctx, vm); err != nil {
		t.Fatal(err)
	}
}

// asoReady is the Ready condition ASO writes on a resource it provisioned;
// ASO's framework claims a resource only under a Ready owner.
func asoReady(generation int64) conditions.Condition {
	return conditions.Condition{
		Type:               conditions.ConditionTypeReady,
		Status:             metav1.ConditionTrue,
		Severity:           conditions.ConditionSeverityNone,
		Reason:             conditions.ReasonSucceeded,
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: generation,
	}
}
