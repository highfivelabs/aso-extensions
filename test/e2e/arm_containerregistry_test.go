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

	acr "github.com/highfivelabs/aso-extensions/apis/arm/containerregistry/v20251101"
)

// registryFootprint makes the ASO footprint the children of a container
// registry hang from: a resource group and a Registry ASO has provisioned
// (stand-ins), and the same two in the fake Resource Manager. It returns the
// registry's ARM ID.
func registryFootprint(t *testing.T, ns string) string {
	t.Helper()
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	regID := rgID + "/providers/Microsoft.ContainerRegistry/registries/" + ns + "reg"
	armFake.AddResource(rgID)
	armFake.AddResource(regID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	containerRegistry(t, ns, "reg", "rg", ns+"-rg", ns+"reg")
	return regID
}

// A scope map of ASO's Registry, a token bound to it (the token's scope map
// is an ARM reference to our kind's ID), a credential set and a webhook whose
// URI and headers come from a Secret: PUT with the namespace's identity,
// changed, deleted token first.
func TestRegistryTokensAndWebhooks(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "acr")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	regID := registryFootprint(t, ns)
	secret(t, ns, "hook", map[string]string{"uri": "https://ci.example.com/acr-hook"})
	secret(t, ns, "headers", map[string]string{"Authorization": "Bearer hook-token-1"})

	scopeMap := &acr.RegistriesScopeMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "pull-apps"},
		Spec: acr.RegistriesScopeMap_Spec{
			AzureName:   "pull-apps",
			Owner:       &genruntime.KnownResourceReference{Name: "reg"},
			Description: ptr("read the apps repository"),
			Actions:     []string{"repositories/apps/content/read", "repositories/apps/metadata/read"},
		},
	}
	if err := k8s.Create(ctx, scopeMap); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "RegistriesScopeMap Ready", ready(scopeMap))
	scopeMapID := regID + "/scopeMaps/pull-apps"
	expectResource(t, scopeMapID, func(r map[string]any) error {
		p := properties(r)
		if r["type"] != "Microsoft.ContainerRegistry/registries/scopeMaps" || p["description"] != "read the apps repository" ||
			!reflect.DeepEqual(p["actions"], []any{"repositories/apps/content/read", "repositories/apps/metadata/read"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	token := &acr.RegistriesToken{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "ci"},
		Spec: acr.RegistriesToken_Spec{
			AzureName:         "ci-puller",
			Owner:             &genruntime.KnownResourceReference{Name: "reg"},
			ScopeMapReference: &genruntime.ResourceReference{ARMID: scopeMapID},
			Status:            ptr(acr.TokenStatus("enabled")),
		},
	}
	hook := &acr.RegistriesWebhook{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "ci-hook"},
		Spec: acr.RegistriesWebhook_Spec{
			AzureName:     "cihook",
			Owner:         &genruntime.KnownResourceReference{Name: "reg"},
			Location:      ptr("westus"),
			Actions:       []acr.WebhookAction{acr.WebhookAction_Push},
			Scope:         ptr("apps:*"),
			Status:        ptr(acr.WebhookStatus("enabled")),
			ServiceUri:    &genruntime.SecretReference{Name: "hook", Key: "uri"},
			CustomHeaders: &genruntime.SecretMapReference{Name: "headers"},
		},
	}
	for _, o := range []client.Object{token, hook} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []client.Object{token, hook} {
		eventually(t, timeout, fmt.Sprintf("%T Ready", o), ready(o.(conditioned)))
	}
	tokenID := regID + "/tokens/ci-puller"
	hookID := regID + "/webhooks/cihook"
	expectResource(t, tokenID, func(r map[string]any) error {
		p := properties(r)
		if r["type"] != "Microsoft.ContainerRegistry/registries/tokens" || p["scopeMapId"] != scopeMapID || p["status"] != "enabled" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, hookID, func(r map[string]any) error {
		p := properties(r)
		headers, _ := p["customHeaders"].(map[string]any)
		if r["type"] != "Microsoft.ContainerRegistry/registries/webhooks" || p["serviceUri"] != "https://ci.example.com/acr-hook" ||
			headers["Authorization"] != "Bearer hook-token-1" ||
			p["scope"] != "apps:*" || !reflect.DeepEqual(p["actions"], []any{"push"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if refs := token.GetOwnerReferences(); len(refs) != 1 || refs[0].Name != "reg" {
		t.Fatalf("owner references: %+v", refs)
	}

	// Disabling the token is a change PUT again.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(token), token); err != nil {
		t.Fatal(err)
	}
	token.Spec.Status = ptr(acr.TokenStatus("disabled"))
	if err := k8s.Update(ctx, token); err != nil {
		t.Fatal(err)
	}
	expectResource(t, tokenID, func(r map[string]any) error {
		if properties(r)["status"] != "disabled" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range []client.Object{token, hook, scopeMap} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T gone", o), gone(o))
	}
	for _, id := range []string{tokenID, hookID, scopeMapID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
	if !armFake.HasResource(regID) {
		t.Fatal("the registry is gone from Resource Manager")
	}
}

// A credential set holds the Key Vault secrets of an upstream registry for
// a cache rule; the CRD refuses a credential name that does not exist.
func TestRegistryCredentialSet(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "acr-cred")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	regID := registryFootprint(t, ns)

	const user = "https://kv.vault.azure.net/secrets/ghcr-user"
	const pass = "https://kv.vault.azure.net/secrets/ghcr-token"
	cs := &acr.RegistriesCredentialSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "ghcr"},
		Spec: acr.RegistriesCredentialSet_Spec{
			AzureName:   "ghcr-hub",
			Owner:       &genruntime.KnownResourceReference{Name: "reg"},
			LoginServer: ptr("ghcr.io"),
			AuthCredentials: []acr.AuthCredential{{
				Name:                     ptr(acr.CredentialName("Credential1")),
				UsernameSecretIdentifier: ptr(user),
				PasswordSecretIdentifier: ptr(pass),
			}},
		},
	}
	if err := k8s.Create(ctx, cs); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "RegistriesCredentialSet Ready", ready(cs))
	expectResource(t, regID+"/credentialSets/ghcr-hub", func(r map[string]any) error {
		p := properties(r)
		creds, _ := p["authCredentials"].([]any)
		if r["type"] != "Microsoft.ContainerRegistry/registries/credentialSets" || p["loginServer"] != "ghcr.io" || len(creds) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		c, _ := creds[0].(map[string]any)
		if c["name"] != "Credential1" || c["usernameSecretIdentifier"] != user || c["passwordSecretIdentifier"] != pass {
			return fmt.Errorf("credential: %v", c)
		}
		return nil
	})
	if err := k8s.Delete(ctx, cs); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "credential set gone", gone(cs))

	bad := &acr.RegistriesCredentialSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "bad"},
		Spec: acr.RegistriesCredentialSet_Spec{
			AzureName:       "bad-set",
			Owner:           &genruntime.KnownResourceReference{Name: "reg"},
			AuthCredentials: []acr.AuthCredential{{Name: ptr(acr.CredentialName("Credential9"))}},
		},
	}
	if err := k8s.Create(ctx, bad); err == nil || !contains(err.Error(), "Unsupported value") {
		t.Fatalf("want a refusal of the credential name, got %v", err)
	}
}
