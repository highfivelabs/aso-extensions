// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	kvv1 "github.com/highfivelabs/aso-extensions/apis/keyvault/v1"
)

func strPtr(s string) *string { return &s }

func vaultValue(t *testing.T, name, want string, versions int) {
	t.Helper()
	eventually(t, timeout, "value of "+name+" in the vault", func() error {
		got, ok := vault.Value(name)
		if !ok {
			return fmt.Errorf("no live secret %s", name)
		}
		if got != want {
			return fmt.Errorf("value %q, want %q", got, want)
		}
		if n := vault.Versions(name); versions > 0 && n != versions {
			return fmt.Errorf("%d versions, want %d", n, versions)
		}
		return nil
	})
}

func gone(obj client.Object) func() error {
	return func() error {
		err := k8s.Get(context.Background(), client.ObjectKeyFromObject(obj), obj)
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("still there: %v", err)
	}
}

// A VaultSecret's value follows its Kubernetes Secret; attribute changes
// patch the current version; deleteMode delete soft-deletes; the create
// modes handle the soft-deleted name. All with team-a's identity.
func TestVaultSecretLifecycle(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "kv-team-a")
	// The vault trusts client A as this namespace's aso-workload.
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	keyVault(t, ns, "vault")
	secret(t, ns, "db", map[string]string{"url": "postgres://one"})

	vs := &kvv1.VaultSecret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "db-url"},
		Spec: kvv1.VaultSecretSpec{
			Owner:       &genruntime.KubernetesOwnerReference{Name: "vault"},
			Value:       &genruntime.SecretReference{Name: "db", Key: "url"},
			ContentType: "text/plain",
			Tags:        map[string]string{"app": "one"},
			DeleteMode:  strPtr("delete"),
		},
	}
	if err := k8s.Create(ctx, vs); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "VaultSecret Ready", ready(vs))
	vaultValue(t, "db-url", "postgres://one", 1)
	if vs.Status.Version == "" || vs.Status.ContentType != "text/plain" || vs.Status.Tags["app"] != "one" {
		t.Fatalf("status: %+v", vs.Status)
	}

	// A new value in the Kubernetes Secret is a new version.
	var k8sSecret corev1.Secret
	if err := k8s.Get(ctx, client.ObjectKey{Namespace: ns, Name: "db"}, &k8sSecret); err != nil {
		t.Fatal(err)
	}
	k8sSecret.Data["url"] = []byte("postgres://two")
	if err := k8s.Update(ctx, &k8sSecret); err != nil {
		t.Fatal(err)
	}
	vaultValue(t, "db-url", "postgres://two", 2)

	// Attributes and tags change the current version, not a new one.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(vs), vs); err != nil {
		t.Fatal(err)
	}
	disabled := false
	vs.Spec.Enabled = &disabled
	vs.Spec.Tags = map[string]string{"app": "two"}
	if err := k8s.Update(ctx, vs); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "disabled in the vault", func() error {
		if en, ok := vault.Enabled("db-url"); !ok || en {
			return fmt.Errorf("enabled=%v live=%v", en, ok)
		}
		return nil
	})
	eventually(t, timeout, "VaultSecret Ready after update", ready(vs))
	if n := vault.Versions("db-url"); n != 2 {
		t.Fatalf("%d versions, want 2", n)
	}
	if vs.Status.Tags["app"] != "two" || vs.Status.Enabled == nil || *vs.Status.Enabled {
		t.Fatalf("status: %+v", vs.Status)
	}

	// deleteMode delete soft-deletes it.
	if err := k8s.Delete(ctx, vs); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "VaultSecret gone", gone(vs))
	if !vault.IsDeleted("db-url") {
		t.Fatal("the secret is not soft-deleted")
	}

	// default createMode: the soft-deleted name is a conflict, reported.
	again := &kvv1.VaultSecret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "db-url"},
		Spec: kvv1.VaultSecretSpec{
			Owner: &genruntime.KubernetesOwnerReference{Name: "vault"},
			Value: &genruntime.SecretReference{Name: "db", Key: "url"},
		},
	}
	if err := k8s.Create(ctx, again); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "conflict reported", notReadyWith(again, "set createMode to recover, createOrRecover or purgeThenCreate"))

	// createOrRecover: recovered, then set to the current value (a new version).
	again.Spec.CreateMode = strPtr("createOrRecover")
	if err := k8s.Update(ctx, again); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "recovered and Ready", ready(again))
	vaultValue(t, "db-url", "postgres://two", 0)
	if vault.IsDeleted("db-url") {
		t.Fatal("still soft-deleted")
	}
	// The default deleteMode, detach, leaves it in the vault.
	if err := k8s.Delete(ctx, again); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "VaultSecret gone", gone(again))
	if _, ok := vault.Value("db-url"); !ok {
		t.Fatal("detach removed the secret")
	}
}

// purgeThenCreate replaces a soft-deleted secret with a new one.
func TestVaultSecretPurgeThenCreate(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "kv-purge")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	keyVault(t, ns, "vault")
	secret(t, ns, "token", map[string]string{"v": "first"})

	mk := func(name string, mode *string, del *string) *kvv1.VaultSecret {
		return &kvv1.VaultSecret{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec: kvv1.VaultSecretSpec{AzureName: "api-token",
				Owner: &genruntime.KubernetesOwnerReference{Name: "vault"},
				Value: &genruntime.SecretReference{Name: "token", Key: "v"}, CreateMode: mode, DeleteMode: del},
		}
	}
	first := mk("first", nil, strPtr("delete"))
	if err := k8s.Create(ctx, first); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "first Ready", ready(first))
	if err := k8s.Delete(ctx, first); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "first gone", gone(first))

	second := mk("second", strPtr("purgeThenCreate"), nil)
	if err := k8s.Create(ctx, second); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "second Ready", ready(second))
	vaultValue(t, "api-token", "first", 1)

	// recover with nothing to recover is an error.
	third := mk("third", strPtr("recover"), nil)
	third.Spec.AzureName = "never-deleted"
	if err := k8s.Create(ctx, third); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "recover refused", notReadyWith(third, "no soft-deleted secret"))
}

// The vault refuses an identity it does not trust: team-b's.
func TestVaultSecretOtherIdentityRefused(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "kv-team-b")
	strictCredential(t, ns, clientB)
	keyVault(t, ns, "vault")
	secret(t, ns, "s", map[string]string{"v": "x"})
	vs := &kvv1.VaultSecret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "nope"},
		Spec: kvv1.VaultSecretSpec{Owner: &genruntime.KubernetesOwnerReference{Name: "vault"},
			Value: &genruntime.SecretReference{Name: "s", Key: "v"}},
	}
	if err := k8s.Create(ctx, vs); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "refused by the vault", notReadyWith(vs, "401"))
}

// A client-secret credential named per resource with ASO's credential-from
// annotation is used for that object, over the namespace's aso-credential.
func TestVaultSecretCredentialFrom(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "kv-team-c")
	strictCredential(t, ns, clientB) // the namespace default, which the vault refuses
	secret(t, ns, "team-c-cred", map[string]string{
		"AZURE_SUBSCRIPTION_ID": subID, "AZURE_TENANT_ID": tenantID,
		"AZURE_CLIENT_ID": clientA, "AZURE_CLIENT_SECRET": "team-c-secret",
	})
	keyVault(t, ns, "vault")
	secret(t, ns, "s", map[string]string{"v": "from-c"})
	vs := &kvv1.VaultSecret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "team-c",
			Annotations: map[string]string{"serviceoperator.azure.com/credential-from": "team-c-cred"}},
		Spec: kvv1.VaultSecretSpec{Owner: &genruntime.KubernetesOwnerReference{Name: "vault"},
			Value: &genruntime.SecretReference{Name: "s", Key: "v"}},
	}
	if err := k8s.Create(ctx, vs); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "VaultSecret Ready with credential-from", ready(vs))
	vaultValue(t, "team-c", "from-c", 1)
}
