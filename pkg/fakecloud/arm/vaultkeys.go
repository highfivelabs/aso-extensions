// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Key Vault keys (Microsoft.KeyVault/vaults/keys through Resource Manager,
// and the keys of the vault's data plane, REST 7.5), as far as the fake
// models them (added 2026-11-12: stock ASO's VaultKey reads the vault's
// vaultUri, then the key in the data plane, before and after its PUT):
//
//   - a key's PUT through Resource Manager creates it when it is not there
//     (a kty is needed, 400) and returns an existing key unchanged, as the
//     API documents; Resource Manager cannot delete a key (405); the response carries keyUri and keyUriWithVersion
//     (https://<vault>.vault.azure.net/keys/<name>[/<version>]), the version
//     derived from the key's ID;
//   - the data plane answers at https://<vault>.<VaultSuffix> (requests whose
//     Host is under VaultSuffix; a test cluster's DNS sends that name to the
//     fake): GET a key (latest or its version), PATCH it (keyOps, enabled,
//     exp, nbf, tags; written to the Resource Manager key), DELETE it (soft
//     delete: gone from both planes; the deleted key is not kept), GET a
//     deleted key (always 404), GET and PUT its rotation policy. A request
//     without a bearer token AuthorizeVault accepts gets 401 with Key Vault's
//     challenge, naming the tenant (Tenant) and https://vault.azure.net;
//   - a key's JSON web key has the kty and curve the PUT gave, an RSA modulus
//     of keySize bits (not a real key: bytes derived from the ID, top bit
//     set) and the key's keyOps.
//
// Not modelled: versions (one per key), cryptographic operations,
// soft-deleted keys and their recovery or purge, the release policy,
// rotation itself, access policies and RBAC (any accepted token may do
// anything), the vault's firewall, and the error codes (the fake's own).
var vaultKey = regexp.MustCompile(`(?i)^(/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.keyvault/vaults/([^/]+))/keys/([^/]+)$`)

// keyVersion is the version the fake gives the key with the given ID.
func keyVersion(id string) string {
	return hex.EncodeToString(derived(strings.ToLower(id)+"|version", 16))
}

// computeVaultKey sets a key's keyUri and keyUriWithVersion.
func (a *ARM) computeVaultKey(id string, props map[string]any) {
	m := vaultKey.FindStringSubmatch(id)
	uri := "https://" + strings.ToLower(m[2]) + "." + a.vaultSuffix() + "/keys/" + m[3]
	props["keyUri"] = uri
	props["keyUriWithVersion"] = uri + "/" + keyVersion(id)
}

// vaultOf is the vault name a data-plane request's host names, or "".
func (a *ARM) vaultOf(r *http.Request) string {
	host := strings.ToLower(r.Host)
	if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host[i:], "]") {
		host = host[:i]
	}
	suffix := "." + strings.ToLower(a.vaultSuffix())
	if !strings.HasSuffix(host, suffix) {
		return ""
	}
	return strings.TrimSuffix(host, suffix)
}

// findVaultKey is the stored key of the named vault (whatever its resource
// group) with the given name.
func (a *ARM) findVaultKey(vault, name string) (string, *resource) {
	for k, r := range a.resources {
		if m := vaultKey.FindStringSubmatch(k); m != nil && m[2] == strings.ToLower(vault) && m[3] == strings.ToLower(name) {
			return k, r
		}
	}
	return "", nil
}

// serveVault answers a Key Vault data-plane request for the named vault.
func (a *ARM) serveVault(w http.ResponseWriter, r *http.Request, vault string) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || a.AuthorizeVault == nil || !a.AuthorizeVault(strings.TrimPrefix(auth, "Bearer ")) {
		w.Header().Set("WWW-Authenticate", `Bearer authorization="https://login.microsoftonline.com/`+a.tenant()+`", resource="https://vault.azure.net"`)
		writeError(w, http.StatusUnauthorized, "Unauthorized", "fake vault: this identity may not use the vault")
		return
	}
	if r.URL.Query().Get("api-version") == "" {
		writeError(w, http.StatusBadRequest, "BadParameter", "api-version is required")
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "deletedkeys" {
		writeError(w, http.StatusNotFound, "KeyNotFound", "A deleted key with (name/id) "+parts[1]+" was not found in this key vault.")
		return
	}
	if len(parts) < 2 || len(parts) > 3 || parts[0] != "keys" {
		writeError(w, http.StatusNotFound, "NotFound", "fake vault: no such operation")
		return
	}
	k, res := a.findVaultKey(vault, parts[1])
	if res == nil {
		writeError(w, http.StatusNotFound, "KeyNotFound", "A key with (name/id) "+parts[1]+" was not found in this key vault.")
		return
	}
	id, _ := (*res)["id"].(string)
	if len(parts) == 3 && parts[2] == "rotationpolicy" {
		a.serveRotationPolicy(w, r, k)
		return
	}
	if len(parts) == 3 && parts[2] != keyVersion(id) {
		writeError(w, http.StatusNotFound, "KeyNotFound", "A key with (name/id) "+parts[1]+"/"+parts[2]+" was not found in this key vault.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, a.keyBundle(id, *res))
	case http.MethodPatch:
		var in struct {
			KeyOps     []any             `json:"key_ops"`
			Attributes map[string]any    `json:"attributes"`
			Tags       map[string]string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "BadParameter", err.Error())
			return
		}
		props, _ := (*res)["properties"].(map[string]any)
		if in.KeyOps != nil {
			props["keyOps"] = in.KeyOps
		}
		if in.Attributes != nil {
			attrs, _ := props["attributes"].(map[string]any)
			if attrs == nil {
				attrs = map[string]any{}
				props["attributes"] = attrs
			}
			for _, f := range []string{"enabled", "exp", "nbf"} {
				if v, ok := in.Attributes[f]; ok {
					attrs[f] = v
				}
			}
		}
		if in.Tags != nil {
			tags := map[string]any{}
			for t, v := range in.Tags {
				tags[t] = v
			}
			(*res)["tags"] = tags
		}
		writeJSON(w, http.StatusOK, a.keyBundle(id, *res))
	case http.MethodDelete:
		bundle := a.keyBundle(id, *res)
		delete(a.resources, k)
		delete(a.rotationPolicies, k)
		writeJSON(w, http.StatusOK, bundle)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
	}
}

// serveRotationPolicy gets or replaces the rotation policy of the key whose
// lower-case ID is given (kept beside the key, not in its properties).
func (a *ARM) serveRotationPolicy(w http.ResponseWriter, r *http.Request, k string) {
	switch r.Method {
	case http.MethodGet:
		p, ok := a.rotationPolicies[k]
		if !ok {
			p = map[string]any{"lifetimeActions": []any{map[string]any{"trigger": map[string]any{"timeBeforeExpiry": "P30D"}, "action": map[string]any{"type": "Notify"}}}, "attributes": map[string]any{}}
		}
		writeJSON(w, http.StatusOK, p)
	case http.MethodPut:
		p := map[string]any{}
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeError(w, http.StatusBadRequest, "BadParameter", err.Error())
			return
		}
		if a.rotationPolicies == nil {
			a.rotationPolicies = map[string]map[string]any{}
		}
		a.rotationPolicies[k] = p
		writeJSON(w, http.StatusOK, p)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
	}
}

// keyBundle is a stored key as the data plane returns it.
func (a *ARM) keyBundle(id string, res resource) map[string]any {
	props, _ := res["properties"].(map[string]any)
	uri, _ := props["keyUriWithVersion"].(string)
	kty, _ := props["kty"].(string)
	jwk := map[string]any{"kid": uri, "kty": kty}
	if ops, ok := props["keyOps"].([]any); ok {
		jwk["key_ops"] = ops
	}
	switch strings.TrimSuffix(kty, "-HSM") {
	case "RSA":
		size := 2048
		if s, ok := props["keySize"].(float64); ok && s > 0 {
			size = int(s)
		}
		n := derived(strings.ToLower(id)+"|n", size/8)
		n[0] |= 0x80
		jwk["n"] = base64.RawURLEncoding.EncodeToString(n)
		jwk["e"] = "AQAB"
	case "EC":
		crv, _ := props["curveName"].(string)
		if crv == "" {
			crv = "P-256"
		}
		jwk["crv"] = crv
		jwk["x"] = base64.RawURLEncoding.EncodeToString(derived(strings.ToLower(id)+"|x", 32))
		jwk["y"] = base64.RawURLEncoding.EncodeToString(derived(strings.ToLower(id)+"|y", 32))
	}
	attrs := map[string]any{"enabled": true, "recoveryLevel": "Recoverable+Purgeable", "recoverableDays": 90}
	if sd, ok := res["systemData"].(map[string]any); ok {
		for f, from := range map[string]string{"created": "createdAt", "updated": "lastModifiedAt"} {
			if s, _ := sd[from].(string); s != "" {
				if t, err := time.Parse(time.RFC3339, s); err == nil {
					attrs[f] = t.Unix()
				}
			}
		}
	}
	if in, ok := props["attributes"].(map[string]any); ok {
		for _, f := range []string{"enabled", "exp", "nbf"} {
			if v, ok := in[f]; ok {
				attrs[f] = v
			}
		}
	}
	out := map[string]any{"key": jwk, "attributes": attrs}
	if tags, ok := res["tags"].(map[string]any); ok {
		out["tags"] = tags
	}
	return out
}
