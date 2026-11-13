// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	appenv "github.com/highfivelabs/aso-extensions/apis/arm/app/v20251002preview"
	app2026 "github.com/highfivelabs/aso-extensions/apis/arm/app/v20260101"
	app202607 "github.com/highfivelabs/aso-extensions/apis/arm/app/v20260701"
)

// Our ManagedEnvironment (2025-10-02-preview), beside ASO's (2025-01-01), for
// the fields ASO's lacks: Application Insights (its connection string from a
// Secret), OpenTelemetry (a Datadog key from a Secret), ingress settings,
// public network access, availability zones and disk encryption with a key
// from Key Vault. They reach Resource Manager with the secrets resolved; a
// change is PUT again; a child of ours names the environment by ARM ID (our
// children's owner kind is ASO's ManagedEnvironment); deleting the object
// deletes the environment.
func TestManagedEnvironmentNewerFields(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "app-env-newer")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	identityID := rgID + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/env-id"
	armFake.AddResource(identityID)
	secret(t, ns, "telemetry", map[string]string{
		"appinsights": "InstrumentationKey=00000000-0000-0000-0000-000000000001",
		"datadog":     "dd-key-1",
	})

	env := &appenv.ManagedEnvironment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "env"},
		Spec: appenv.ManagedEnvironment_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
			AppInsightsConfiguration: &appenv.AppInsightsConfiguration{
				ConnectionString: &genruntime.SecretReference{Name: "telemetry", Key: "appinsights"},
			},
			OpenTelemetryConfiguration: &appenv.OpenTelemetryConfiguration{
				DestinationsConfiguration: &appenv.DestinationsConfiguration{
					DataDogConfiguration: &appenv.DataDogConfiguration{
						Site: ptr("datadoghq.eu"),
						Key:  &genruntime.SecretReference{Name: "telemetry", Key: "datadog"},
					},
				},
				TracesConfiguration: &appenv.TracesConfiguration{Destinations: []string{"dataDog"}},
			},
			IngressConfiguration: &appenv.IngressConfiguration{
				HeaderCountLimit: ptr(100), RequestIdleTimeout: ptr(4), TerminationGracePeriodSeconds: ptr(30),
			},
			PublicNetworkAccess: ptr(appenv.PublicNetworkAccess("Disabled")),
			AvailabilityZones:   []string{"1", "2", "3"},
			DiskEncryptionConfiguration: &appenv.DiskEncryptionConfiguration{
				KeyVaultConfiguration: &appenv.DiskEncryptionConfigurationKeyVaultConfiguration{
					KeyUrl: ptr("https://kv.vault.azure.net/keys/env-disk/0123456789abcdef"),
					Auth: &appenv.DiskEncryptionConfigurationKeyVaultConfigurationAuth{
						IdentityReference: &genruntime.ResourceReference{ARMID: identityID},
					},
				},
			},
		},
	}
	if err := k8s.Create(ctx, env); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "ManagedEnvironment Ready", ready(env))

	envID := rgID + "/providers/Microsoft.App/managedEnvironments/env"
	expectResource(t, envID, func(r map[string]any) error {
		p := properties(r)
		ai, _ := p["appInsightsConfiguration"].(map[string]any)
		otel, _ := p["openTelemetryConfiguration"].(map[string]any)
		dests, _ := otel["destinationsConfiguration"].(map[string]any)
		dd, _ := dests["dataDogConfiguration"].(map[string]any)
		ingress, _ := p["ingressConfiguration"].(map[string]any)
		disk, _ := p["diskEncryptionConfiguration"].(map[string]any)
		kv, _ := disk["keyVaultConfiguration"].(map[string]any)
		auth, _ := kv["auth"].(map[string]any)
		switch {
		case r["type"] != "Microsoft.App/managedEnvironments":
			return fmt.Errorf("type: %v", r["type"])
		case ai["connectionString"] != "InstrumentationKey=00000000-0000-0000-0000-000000000001":
			return fmt.Errorf("appInsightsConfiguration: %v", ai)
		case dd["key"] != "dd-key-1" || dd["site"] != "datadoghq.eu":
			return fmt.Errorf("dataDogConfiguration: %v", dd)
		case ingress["headerCountLimit"] != float64(100) || ingress["terminationGracePeriodSeconds"] != float64(30):
			return fmt.Errorf("ingressConfiguration: %v", ingress)
		case p["publicNetworkAccess"] != "Disabled":
			return fmt.Errorf("publicNetworkAccess: %v", p["publicNetworkAccess"])
		case !reflect.DeepEqual(p["availabilityZones"], []any{"1", "2", "3"}):
			return fmt.Errorf("availabilityZones: %v", p["availabilityZones"])
		case auth["identity"] != identityID:
			return fmt.Errorf("diskEncryptionConfiguration: %v", disk)
		}
		return nil
	})
	if env.Status.Id == nil || *env.Status.Id != envID {
		t.Fatalf("status id: %v", env.Status.Id)
	}

	// A change is PUT again.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(env), env); err != nil {
		t.Fatal(err)
	}
	env.Spec.PublicNetworkAccess = ptr(appenv.PublicNetworkAccess("Enabled"))
	if err := k8s.Update(ctx, env); err != nil {
		t.Fatal(err)
	}
	expectResource(t, envID, func(r map[string]any) error {
		if properties(r)["publicNetworkAccess"] != "Enabled" {
			return fmt.Errorf("publicNetworkAccess: %v", properties(r)["publicNetworkAccess"])
		}
		return nil
	})

	// A child of ours names our environment by ARM ID.
	share := &app2026.ManagedEnvironmentsStorage{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "files"},
		Spec: app2026.ManagedEnvironmentsStorage_Spec{
			Owner: &genruntime.KnownResourceReference{ARMID: envID},
			Properties: &app2026.ManagedEnvironmentStorageProperties{AzureFile: &app2026.AzureFileProperties{
				AccountName: ptr("appstorage"), ShareName: ptr("data"),
				AccountKeyVaultProperties: &app2026.SecretKeyVaultProperties{
					KeyVaultUrl:       ptr("https://kv.vault.azure.net/secrets/share-key"),
					IdentityReference: &genruntime.WellKnownResourceReference{ResourceReference: genruntime.ResourceReference{ARMID: identityID}},
				},
				AccessMode: ptr(app2026.AccessMode("ReadOnly")),
			}},
		},
	}
	if err := k8s.Create(ctx, share); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "storage Ready", ready(share))
	if !armFake.HasResource(envID + "/storages/files") {
		t.Fatalf("%s/storages/files is not in Resource Manager", envID)
	}
	if err := k8s.Delete(ctx, share); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "storage gone", gone(share))

	if err := k8s.Delete(ctx, env); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "ManagedEnvironment gone", gone(env))
	if armFake.HasResource(envID) {
		t.Fatalf("%s is still in Resource Manager", envID)
	}
}

// Our ContainerApp (2026-07-01), beside ASO's (2025-01-01), for `kind`
// (functionapp, workflowapp), which ASO's version lacks; the environment and
// an identity's lifecycle as ASO's has them.
func TestContainerAppKind(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "app-kind")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	envID := appEnvironment(t, ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	identityID := rgID + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/app-id"
	armFake.AddResource(identityID)

	app := &app202607.ContainerApp{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "func"},
		Spec: app202607.ContainerApp_Spec{
			Owner:                       &genruntime.KnownResourceReference{Name: "rg"},
			Location:                    ptr("westus"),
			Kind:                        ptr(app202607.Kind_Functionapp),
			ManagedEnvironmentReference: &genruntime.ResourceReference{ARMID: envID},
			Configuration: &app202607.Configuration{
				IdentitySettings: []app202607.IdentitySettings{{
					IdentityReference: &genruntime.WellKnownResourceReference{ResourceReference: genruntime.ResourceReference{ARMID: identityID}},
					Lifecycle:         ptr(app202607.IdentitySettings_Lifecycle("Init")),
				}},
			},
			Template: &app202607.Template{Containers: []app202607.Container{{Name: ptr("main"), Image: ptr("mcr.microsoft.com/k8se/quickstart:latest")}}},
		},
	}
	if err := k8s.Create(ctx, app); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "ContainerApp Ready", ready(app))

	id := rgID + "/providers/Microsoft.App/containerApps/func"
	expectResource(t, id, func(r map[string]any) error {
		p := properties(r)
		cfg, _ := p["configuration"].(map[string]any)
		ids, _ := cfg["identitySettings"].([]any)
		if r["kind"] != "functionapp" || p["managedEnvironmentId"] != envID ||
			len(ids) != 1 || ids[0].(map[string]any)["identity"] != identityID || ids[0].(map[string]any)["lifecycle"] != "Init" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if app.Status.Kind == nil || string(*app.Status.Kind) != "functionapp" {
		t.Fatalf("status kind: %v", app.Status.Kind)
	}

	if err := k8s.Delete(ctx, app); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "ContainerApp gone", gone(app))
	if armFake.HasResource(id) {
		t.Fatalf("%s is still in Resource Manager", id)
	}
}
