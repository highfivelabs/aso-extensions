// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

//go:build live

package live

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
)

// asoObject is an ASO kind our kinds hang from; the tests stand one in for
// ASO, which is not running here.
type asoObject interface {
	conditioned
	SetConditions(conditions.Conditions)
}

// standIn creates obj (its spec already set) as ASO leaves it once
// provisioned: the ARM ID annotation naming the real fixture armID, Ready,
// with whatever status setStatus fills (a vault's URI, a server's name).
func standIn(t *testing.T, obj asoObject, armID string, setStatus func()) {
	t.Helper()
	ctx := context.Background()
	ann := obj.GetAnnotations()
	if ann == nil {
		ann = map[string]string{}
	}
	ann[genruntime.ResourceIDAnnotation] = armID
	obj.SetAnnotations(ann)
	if err := k8s.Create(ctx, obj); err != nil {
		t.Fatal(err)
	}
	if setStatus != nil {
		setStatus()
	}
	obj.SetConditions(conditions.Conditions{{
		Type: conditions.ConditionTypeReady, Status: metav1.ConditionTrue, Severity: conditions.ConditionSeverityNone,
		Reason: conditions.ReasonSucceeded, LastTransitionTime: metav1.Now(), ObservedGeneration: obj.GetGeneration(),
	}})
	if err := k8s.Status().Update(ctx, obj); err != nil {
		t.Fatal(err)
	}
}

// post sends a POST (listKeys and the like) and returns the answer.
func post(t *testing.T, id, apiVersion string) map[string]any {
	t.Helper()
	_, r, err := azure.do(context.Background(), http.MethodPost, id, apiVersion, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// storageAccount makes a StorageV2 Standard_LRS account (storage cents) and
// returns its ID and Azure name.
func storageAccount(t *testing.T, base string) (string, string) {
	t.Helper()
	name := strings.ReplaceAll(azName(base), "-", "")
	id := rgID() + "/providers/Microsoft.Storage/storageAccounts/" + name
	fixture(t, id, "2023-05-01", map[string]any{
		"location": location, "kind": "StorageV2", "sku": map[string]any{"name": "Standard_LRS"},
		"properties": map[string]any{"minimumTlsVersion": "TLS1_2", "allowBlobPublicAccess": false},
	})
	return id, name
}

// logAnalyticsWorkspace makes a workspace (PerGB2018, nothing ingested, so
// free) and returns its ID.
func logAnalyticsWorkspace(t *testing.T, base string) string {
	t.Helper()
	id := rgID() + "/providers/Microsoft.OperationalInsights/workspaces/" + azName(base)
	fixture(t, id, "2023-09-01", map[string]any{
		"location":   location,
		"properties": map[string]any{"sku": map[string]any{"name": "PerGB2018"}, "retentionInDays": 30},
	})
	return id
}

// privateEndpoint makes a private endpoint (about $0.01 an hour) in a new
// network to target's groupID, with a manual connection Azure leaves
// Pending for the kind under test to approve. It returns the endpoint's ID.
func privateEndpoint(t *testing.T, base, prefix, target, groupID string) string {
	t.Helper()
	netID := vnet(t, azName(base+"net"), prefix, map[string]string{"default": strings.Replace(prefix, "0.0/16", "0.0/24", 1)})
	id := rgID() + "/providers/Microsoft.Network/privateEndpoints/" + azName(base)
	fixture(t, id, "2024-05-01", map[string]any{
		"location": location,
		"properties": map[string]any{
			"subnet": map[string]any{"id": netID + "/subnets/default"},
			"manualPrivateLinkServiceConnections": []any{map[string]any{"name": azName(base), "properties": map[string]any{
				"privateLinkServiceId": target, "groupIds": []string{groupID}, "requestMessage": "live test",
			}}},
		},
	})
	return id
}

// pendingConnection waits for target to list a private endpoint connection
// from endpoint and returns its name.
func pendingConnection(t *testing.T, target, apiVersion, endpoint string) string {
	t.Helper()
	var name string
	eventually(t, timeout, "a private endpoint connection on "+target, func() error {
		_, r, err := azure.get(context.Background(), target, apiVersion)
		if err != nil {
			return err
		}
		conns, _ := properties(r)["privateEndpointConnections"].([]any)
		for _, c := range conns {
			cm, _ := c.(map[string]any)
			pe, _ := properties(cm)["privateEndpoint"].(map[string]any)
			if id, _ := pe["id"].(string); strings.EqualFold(id, endpoint) {
				name, _ = cm["name"].(string)
				if name == "" { // some list only the connection's ID
					id, _ := cm["id"].(string)
					name = id[strings.LastIndex(id, "/")+1:]
				}
				return nil
			}
		}
		return fmt.Errorf("none from %s yet: %v", endpoint, conns)
	})
	return name
}

// linuxVM makes a small Linux VM (two vCPUs, about $0.03 an hour, no public
// IP) in a new network, with platform patching on user schedules (as a
// maintenance configuration needs), and returns its ID and Azure name.
func linuxVM(t *testing.T, base, prefix string) (string, string) {
	t.Helper()
	vnetID := vnet(t, azName(base+"net"), prefix, map[string]string{"default": strings.Replace(prefix, "0.0/16", "0.0/24", 1)})
	nicID := rgID() + "/providers/Microsoft.Network/networkInterfaces/" + azName(base+"nic")
	fixture(t, nicID, "2024-05-01", map[string]any{
		"location": location,
		"properties": map[string]any{"ipConfigurations": []any{map[string]any{"name": "default", "properties": map[string]any{
			"subnet": map[string]any{"id": vnetID + "/subnets/default"}, "privateIPAllocationMethod": "Dynamic",
		}}}},
	})
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	vmName := azName(base)
	vmID := rgID() + "/providers/Microsoft.Compute/virtualMachines/" + vmName
	// Small sizes in order; a new subscription is refused some sizes for
	// capacity (Standard_B1s in eastus2, 2026-11-10: SkuNotAvailable).
	var made bool
	for _, size := range []string{"Standard_D2als_v6", "Standard_B2ats_v2", "Standard_B2als_v2", "Standard_D2as_v5", "Standard_B1s"} {
		vmBody := map[string]any{
			"location": location,
			"properties": map[string]any{
				"hardwareProfile": map[string]any{"vmSize": size},
				"storageProfile": map[string]any{
					"imageReference": map[string]any{"publisher": "Canonical", "offer": "ubuntu-24_04-lts", "sku": "server", "version": "latest"},
					"osDisk":         map[string]any{"createOption": "FromImage", "deleteOption": "Delete", "managedDisk": map[string]any{"storageAccountType": "Standard_LRS"}},
				},
				"osProfile": map[string]any{
					"computerName": "live", "adminUsername": "live",
					"adminPassword": "Aa1!" + hex.EncodeToString(b),
					"linuxConfiguration": map[string]any{
						"disablePasswordAuthentication": false,
						"patchSettings": map[string]any{
							"patchMode":                   "AutomaticByPlatform",
							"automaticByPlatformSettings": map[string]any{"bypassPlatformSafetyChecksOnUserSchedule": true},
						},
					},
				},
				"networkProfile": map[string]any{"networkInterfaces": []any{map[string]any{"id": nicID, "properties": map[string]any{"deleteOption": "Detach"}}}},
			},
		}
		_, _, err := azure.do(context.Background(), http.MethodPut, vmID, "2024-07-01", vmBody)
		if err != nil && strings.Contains(err.Error(), "SkuNotAvailable") {
			t.Logf("%s not available: trying the next size", size)
			continue
		}
		if err != nil {
			t.Fatalf("fixture VM: %v", err)
		}
		t.Logf("VM size %s", size)
		made = true
		break
	}
	if !made {
		t.Fatal("no VM size available in " + location)
	}
	t.Cleanup(func() { deleteARM(t, vmID, "2024-07-01") })
	eventually(t, timeout, "fixture VM Succeeded", func() error {
		_, r, err := azure.get(context.Background(), vmID, "2024-07-01")
		if err != nil {
			return err
		}
		if s := state(r); s != "Succeeded" {
			return fmt.Errorf("provisioningState %s", s)
		}
		return nil
	})
	return vmID, vmName
}

// password is a random password Azure's rules accept.
func password() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "Aa1!" + hex.EncodeToString(b)
}

// selfSigned is a fresh self-signed certificate for cn and its key, as PEM.
func selfSigned(t *testing.T, cn string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: cn}, DNSNames: []string{cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(0, 0, 30),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	k, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: k})
}

// pfx is a self-signed certificate for cn as a password-protected PKCS#12
// file, base64, made with openssl (Go's standard library writes no PKCS#12),
// and the password.
func pfx(t *testing.T, cn string) (string, string) {
	t.Helper()
	certPEM, keyPEM := selfSigned(t, cn)
	dir := t.TempDir()
	for name, b := range map[string][]byte{"cert.pem": certPEM, "key.pem": keyPEM} {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pass := password()
	out := filepath.Join(dir, "cert.pfx")
	cmd := exec.Command("openssl", "pkcs12", "-export", "-in", filepath.Join(dir, "cert.pem"), "-inkey", filepath.Join(dir, "key.pem"),
		"-out", out, "-passout", "pass:"+pass, "-keypbe", "AES-256-CBC", "-certpbe", "AES-256-CBC", "-macalg", "SHA256")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("openssl: %v: %s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b), pass
}

// runnerIP is this machine's public address, for a database firewall rule.
func runnerIP(t *testing.T) string {
	t.Helper()
	resp, err := http.Get("https://api.ipify.org")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	ip := strings.TrimSpace(string(b))
	if resp.StatusCode != http.StatusOK || ip == "" {
		t.Fatalf("public address: %d %q", resp.StatusCode, b)
	}
	return ip
}

// identityObjectID is the live-test identity's object ID, from its token.
func identityObjectID(t *testing.T) string {
	t.Helper()
	tok, err := azure.cred.GetToken(context.Background(), policy.TokenRequestOptions{Scopes: []string{armEndpoint + "/.default"}})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok.Token, ".")
	if len(parts) != 3 {
		t.Fatal("the token is not a JWT")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		OID string `json:"oid"`
	}
	if err := json.Unmarshal(b, &claims); err != nil || claims.OID == "" {
		t.Fatalf("no oid in the token: %v", err)
	}
	return claims.OID
}
