// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"crypto/sha1" //nolint:gosec // Key Vault's x5t is a SHA-1 thumbprint.
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	kvv1 "github.com/highfivelabs/aso-extensions/apis/keyvault/v1"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/tlsutil"
)

// leaf is a PEM certificate's first certificate, with its x5t (base64url
// SHA-1) and hex thumbprint.
func leaf(t *testing.T, certPEM []byte) (*x509.Certificate, string, string) {
	t.Helper()
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("no PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum(block.Bytes) //nolint:gosec // see the import
	return cert, base64.RawURLEncoding.EncodeToString(sum[:]), strings.ToUpper(hex.EncodeToString(sum[:]))
}

func tlsSecret(t *testing.T, ns, name string, certPEM, keyPEM []byte) {
	t.Helper()
	ctx := context.Background()
	s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
	err := k8s.Get(ctx, client.ObjectKeyFromObject(s), s)
	s.Type = corev1.SecretTypeTLS
	s.Data = map[string][]byte{corev1.TLSCertKey: certPEM, corev1.TLSPrivateKeyKey: keyPEM}
	if err != nil {
		if err := k8s.Create(ctx, s); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := k8s.Update(ctx, s); err != nil {
		t.Fatal(err)
	}
}

func expectCertificate(t *testing.T, name, x5t string, versions int, enabled bool) {
	t.Helper()
	eventually(t, timeout, "certificate "+name+" in the vault", func() error {
		got, gotEnabled, ok := vault.CertificateThumbprint(name)
		if !ok {
			return fmt.Errorf("no live certificate %s", name)
		}
		if got != x5t || gotEnabled != enabled {
			return fmt.Errorf("x5t %s enabled %v, want %s %v", got, gotEnabled, x5t, enabled)
		}
		if n := vault.CertificateVersions(name); n != versions {
			return fmt.Errorf("%d versions, want %d", n, versions)
		}
		return nil
	})
}

// A VaultCertificate imports the certificate and key of a kubernetes.io/tls
// Secret, with the secret Key Vault keeps beside it; a renewed certificate
// is a new version; enabled patches the current version; a key that does
// not match is refused; deleteMode delete soft-deletes; the create modes
// handle the soft-deleted name. All with the namespace's identity.
func TestVaultCertificateLifecycle(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "kv-cert")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	keyVault(t, ns, "vault")

	ca, err := tlsutil.NewCA()
	if err != nil {
		t.Fatal(err)
	}
	cert1, key1, err := ca.Server("orders.example")
	if err != nil {
		t.Fatal(err)
	}
	cert2, key2, err := ca.Server("orders.example")
	if err != nil {
		t.Fatal(err)
	}
	leaf1, x5t1, thumb1 := leaf(t, cert1)
	_, x5t2, thumb2 := leaf(t, cert2)
	tlsSecret(t, ns, "orders-tls", cert1, key1)

	vc := &kvv1.VaultCertificate{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "orders-tls"},
		Spec: kvv1.VaultCertificateSpec{
			Owner:       &genruntime.KubernetesOwnerReference{Name: "vault"},
			Certificate: &genruntime.SecretReference{Name: "orders-tls", Key: corev1.TLSCertKey},
			PrivateKey:  &genruntime.SecretReference{Name: "orders-tls", Key: corev1.TLSPrivateKeyKey},
			Tags:        map[string]string{"app": "orders"},
			DeleteMode:  strPtr("delete"),
		},
	}
	if err := k8s.Create(ctx, vc); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "VaultCertificate Ready", ready(vc))
	expectCertificate(t, "orders-tls", x5t1, 1, true)
	if vc.Status.Thumbprint != thumb1 || vc.Status.Version == "" || !strings.HasSuffix(vc.Status.SecretId, "/secrets/orders-tls/"+vc.Status.Version) ||
		vc.Status.ExpiresOn == nil || vc.Status.ExpiresOn.Unix() != leaf1.NotAfter.Unix() || vc.Status.Tags["app"] != "orders" {
		t.Fatalf("status: %+v", vc.Status)
	}
	// The secret beside the certificate holds the certificate and its key.
	if pemValue, ok := vault.Value("orders-tls"); !ok || !strings.Contains(pemValue, "BEGIN CERTIFICATE") || !strings.Contains(pemValue, "PRIVATE KEY") {
		t.Fatalf("the certificate's secret: %v %q", ok, pemValue)
	}

	// A renewal in the Secret (as cert-manager does) is a new version.
	tlsSecret(t, ns, "orders-tls", cert2, key2)
	expectCertificate(t, "orders-tls", x5t2, 2, true)
	eventually(t, timeout, "status follows the renewal", func() error {
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(vc), vc); err != nil {
			return err
		}
		if vc.Status.Thumbprint != thumb2 {
			return fmt.Errorf("thumbprint %s, want %s", vc.Status.Thumbprint, thumb2)
		}
		return nil
	})

	// Disabling patches the current version; no new import.
	vc.Spec.Enabled = new(bool)
	if err := k8s.Update(ctx, vc); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "VaultCertificate Ready disabled", ready(vc))
	expectCertificate(t, "orders-tls", x5t2, 2, false)

	// A key that does not match the certificate is refused before Key Vault.
	tlsSecret(t, ns, "orders-tls", cert1, key2)
	eventually(t, timeout, "mismatched key refused", notReadyWith(vc, "do not make a pair"))
	expectCertificate(t, "orders-tls", x5t2, 2, false)
	tlsSecret(t, ns, "orders-tls", cert2, key2)
	eventually(t, timeout, "VaultCertificate Ready again", ready(vc))

	// deleteMode delete soft-deletes the certificate and its secret.
	if err := k8s.Delete(ctx, vc); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "VaultCertificate gone", gone(vc))
	if !vault.IsCertificateDeleted("orders-tls") || !vault.IsDeleted("orders-tls") {
		t.Fatalf("certificate soft-deleted=%v, its secret=%v", vault.IsCertificateDeleted("orders-tls"), vault.IsDeleted("orders-tls"))
	}

	// The soft-deleted certificate holds the name: refused by default,
	// recovered with createOrRecover.
	again := &kvv1.VaultCertificate{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "orders-tls"},
		Spec: kvv1.VaultCertificateSpec{
			Owner:       &genruntime.KubernetesOwnerReference{Name: "vault"},
			Certificate: &genruntime.SecretReference{Name: "orders-tls", Key: corev1.TLSCertKey},
			PrivateKey:  &genruntime.SecretReference{Name: "orders-tls", Key: corev1.TLSPrivateKeyKey},
			Tags:        map[string]string{"app": "orders"},
		},
	}
	if err := k8s.Create(ctx, again); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "soft-deleted name refused", notReadyWith(again, "a soft-deleted certificate orders-tls holds the name"))
	again.Spec.CreateMode = strPtr("createOrRecover")
	if err := k8s.Update(ctx, again); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "recovered and Ready", ready(again))
	if vault.IsCertificateDeleted("orders-tls") {
		t.Fatal("the certificate is still soft-deleted")
	}
	// Recovered as it was (disabled); this object wants it enabled.
	expectCertificate(t, "orders-tls", x5t2, 2, true)
}

// Another identity is refused by the vault; the CRD needs both the
// certificate and the key.
func TestVaultCertificateRefused(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "kv-cert-b")
	strictCredential(t, ns, clientB)
	keyVault(t, ns, "vault")
	ca, err := tlsutil.NewCA()
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := ca.Server("b.example")
	if err != nil {
		t.Fatal(err)
	}
	tlsSecret(t, ns, "b-tls", certPEM, keyPEM)
	vc := &kvv1.VaultCertificate{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "b-tls"},
		Spec: kvv1.VaultCertificateSpec{
			Owner:       &genruntime.KubernetesOwnerReference{Name: "vault"},
			Certificate: &genruntime.SecretReference{Name: "b-tls", Key: corev1.TLSCertKey},
			PrivateKey:  &genruntime.SecretReference{Name: "b-tls", Key: corev1.TLSPrivateKeyKey},
		},
	}
	if err := k8s.Create(ctx, vc); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "another identity refused", notReadyWith(vc, "401"))

	noKey := &kvv1.VaultCertificate{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "no-key"},
		Spec: kvv1.VaultCertificateSpec{
			Owner:       &genruntime.KubernetesOwnerReference{Name: "vault"},
			Certificate: &genruntime.SecretReference{Name: "b-tls", Key: corev1.TLSCertKey},
		},
	}
	if err := k8s.Create(ctx, noKey); err == nil || !strings.Contains(err.Error(), "privateKey") {
		t.Fatalf("want a refusal naming privateKey, got %v", err)
	}
}
