// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	kvv1 "github.com/highfivelabs/aso-extensions/apis/keyvault/v1"
)

// The manager reads a Secret only in the namespace of the object it
// reconciles (docs/plan.md §8 Q4, answered 2026-11-16: "Read Secrets only in
// the object's namespace"). A Secret of the same name in another namespace is
// never used: the object waits, naming the Secret it looked for in its own
// namespace, and becomes Ready once that one exists. A credential-from
// annotation that names another namespace is refused, not followed.
func TestSecretsReadOnlyInObjectNamespace(t *testing.T) {
	ctx := context.Background()
	nsA := namespace(t, "sec-own-a")
	nsB := namespace(t, "sec-own-b")
	strictCredential(t, nsA, clientA)
	trustNamespace(nsA)
	keyVault(t, nsA, "vault")

	// The value's Secret exists in B only.
	secret(t, nsB, "shared", map[string]string{"v": "from-b"})
	vs := &kvv1.VaultSecret{
		ObjectMeta: metav1.ObjectMeta{Namespace: nsA, Name: "own-ns"},
		Spec: kvv1.VaultSecretSpec{
			Owner: &genruntime.KubernetesOwnerReference{Name: "vault"},
			Value: &genruntime.SecretReference{Name: "shared", Key: "v"},
		},
	}
	if err := k8s.Create(ctx, vs); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the Secret looked for in the object's namespace", notReadyWith(vs, nsA+"/shared does not exist"))
	if _, ok := vault.Value("own-ns"); ok {
		t.Fatal("a value reached the vault from another namespace's Secret")
	}

	// The same name in A is the one read.
	secret(t, nsA, "shared", map[string]string{"v": "from-a"})
	eventually(t, timeout, "VaultSecret Ready with its own namespace's Secret", ready(vs))
	vaultValue(t, "own-ns", "from-a", 1)

	// credential-from naming B's Secret is refused (ASO's rule, kept).
	secret(t, nsB, "cred", map[string]string{
		"AZURE_SUBSCRIPTION_ID": subID, "AZURE_TENANT_ID": tenantID,
		"AZURE_CLIENT_ID": clientA, "AZURE_CLIENT_SECRET": "b-secret",
	})
	other := &kvv1.VaultSecret{
		ObjectMeta: metav1.ObjectMeta{Namespace: nsA, Name: "cred-from-b",
			Annotations: map[string]string{"serviceoperator.azure.com/credential-from": nsB + "/cred"}},
		Spec: kvv1.VaultSecretSpec{
			Owner: &genruntime.KubernetesOwnerReference{Name: "vault"},
			Value: &genruntime.SecretReference{Name: "shared", Key: "v"},
		},
	}
	if err := k8s.Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "credential-from in another namespace refused", notReadyWith(other, "Secret must be in same namespace as resource"))
	if _, ok := vault.Value("cred-from-b"); ok {
		t.Fatal("a credential from another namespace was used")
	}
}
