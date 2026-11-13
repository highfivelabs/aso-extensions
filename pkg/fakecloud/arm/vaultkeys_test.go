// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// vaultDo sends a data-plane request to the named vault, with the token.
func vaultDo(t *testing.T, a *ARM, vault, token, method, path, body string) (int, string, http.Header) {
	t.Helper()
	req := httptest.NewRequest(method, "https://"+vault+".vault.azure.net"+path+"?api-version=7.5", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String(), rec.Header()
}

// What stock ASO's VaultKey does: read the vault's URI, look for the key in
// the data plane (404), PUT it through Resource Manager, read it back from
// the data plane, and leave it there on delete (Resource Manager refuses).
func TestVaultKeyBothPlanes(t *testing.T) {
	a := New(func(t string) bool { return t == "ok" })
	a.Tenant = "11111111-2222-3333-4444-555555555555"
	a.AuthorizeVault = func(t string) bool { return t == "vault" }
	a.AddResource(computedRG)
	vault := computedRG + "/providers/Microsoft.KeyVault/vaults/kv1"
	code, body := do(t, a, http.MethodPut, vault, `{"location":"eastus2","properties":{}}`)
	expect(t, "vault", code, body, http.StatusCreated)

	// The challenge names the tenant and Key Vault's resource.
	code, body, h := vaultDo(t, a, "kv1", "", http.MethodGet, "/keys/k1/", "")
	expect(t, "no token", code, body, http.StatusUnauthorized)
	if c := h.Get("WWW-Authenticate"); !strings.Contains(c, a.Tenant) || !strings.Contains(c, `resource="https://vault.azure.net"`) {
		t.Errorf("challenge %q", c)
	}
	code, body, _ = vaultDo(t, a, "kv1", "ok", http.MethodGet, "/keys/k1/", "")
	expect(t, "a Resource Manager token", code, body, http.StatusUnauthorized)
	code, body, _ = vaultDo(t, a, "kv1", "vault", http.MethodGet, "/keys/k1/", "")
	expect(t, "no key yet", code, body, http.StatusNotFound)
	code, body, _ = vaultDo(t, a, "kv1", "vault", http.MethodGet, "/deletedkeys/k1", "")
	expect(t, "no deleted key", code, body, http.StatusNotFound)

	key := vault + "/keys/k1"
	code, body = do(t, a, http.MethodPut, key, `{"properties":{"keyOps":["sign"]}}`)
	expect(t, "a key without kty", code, body, http.StatusBadRequest)
	code, body = do(t, a, http.MethodPut, key, `{"properties":{"kty":"RSA","keySize":3072,"keyOps":["sign","verify"]}}`)
	expect(t, "key", code, body, http.StatusCreated)
	p := props(t, body)
	if p["keyUri"] != "https://kv1.vault.azure.net/keys/k1" || !strings.HasPrefix(p["keyUriWithVersion"].(string), "https://kv1.vault.azure.net/keys/k1/") {
		t.Errorf("key URIs %v %v", p["keyUri"], p["keyUriWithVersion"])
	}
	// A second PUT changes nothing.
	code, body = do(t, a, http.MethodPut, key, `{"properties":{"kty":"EC"}}`)
	expect(t, "key again", code, body, http.StatusOK)
	if props(t, body)["kty"] != "RSA" {
		t.Errorf("a second PUT changed the key: %s", body)
	}

	for _, path := range []string{"/keys/k1/", "/keys/k1/" + p["keyUriWithVersion"].(string)[len("https://kv1.vault.azure.net/keys/k1/"):]} {
		code, body, _ = vaultDo(t, a, "kv1", "vault", http.MethodGet, path, "")
		expect(t, "get "+path, code, body, http.StatusOK)
		var b struct {
			Key struct {
				Kid, Kty, N string
				KeyOps      []string `json:"key_ops"`
			}
			Attributes struct{ Enabled bool }
		}
		if err := json.Unmarshal([]byte(body), &b); err != nil {
			t.Fatal(err)
		}
		n, err := base64.RawURLEncoding.DecodeString(b.Key.N)
		if err != nil || len(n)*8 != 3072 || n[0]&0x80 == 0 {
			t.Errorf("modulus of %d bits (%v)", len(n)*8, err)
		}
		if b.Key.Kid != p["keyUriWithVersion"] || b.Key.Kty != "RSA" || len(b.Key.KeyOps) != 2 || !b.Attributes.Enabled {
			t.Errorf("key bundle %s", body)
		}
	}
	code, body, _ = vaultDo(t, a, "kv1", "vault", http.MethodGet, "/keys/k1/0000", "")
	expect(t, "another version", code, body, http.StatusNotFound)
	code, body, _ = vaultDo(t, a, "kv2", "vault", http.MethodGet, "/keys/k1/", "")
	expect(t, "another vault", code, body, http.StatusNotFound)

	// An update in the data plane shows in Resource Manager.
	code, body, _ = vaultDo(t, a, "kv1", "vault", http.MethodPatch, "/keys/k1/", `{"key_ops":["sign"],"attributes":{"enabled":false},"tags":{"a":"b"}}`)
	expect(t, "update", code, body, http.StatusOK)
	_, body = do(t, a, http.MethodGet, key, "")
	if !strings.Contains(body, `"keyOps":["sign"]`) || !strings.Contains(body, `"enabled":false`) || !strings.Contains(body, `"a":"b"`) {
		t.Errorf("the update is not in Resource Manager: %s", body)
	}
	code, body, _ = vaultDo(t, a, "kv1", "vault", http.MethodPut, "/keys/k1/rotationpolicy", `{"lifetimeActions":[]}`)
	expect(t, "rotation policy", code, body, http.StatusOK)

	code, body = do(t, a, http.MethodDelete, key, "")
	expect(t, "delete through Resource Manager", code, body, http.StatusMethodNotAllowed)
	code, body, _ = vaultDo(t, a, "kv1", "vault", http.MethodDelete, "/keys/k1", "")
	expect(t, "delete in the data plane", code, body, http.StatusOK)
	code, body = do(t, a, http.MethodGet, key, "")
	expect(t, "gone", code, body, http.StatusNotFound)
}
