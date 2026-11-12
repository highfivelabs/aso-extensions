// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Package keyvault is a fake Azure Key Vault data plane (REST API 7.5), for
// tests and test builds only. Secrets: set, get (latest or a version),
// update attributes, soft delete, get deleted, recover and purge.
// Certificates (certificates.go): import (PEM), get, update attributes,
// soft delete, get deleted, recover and purge, each with the secret Key
// Vault keeps beside a certificate. Soft delete is always on. It accepts a request only when Authorize accepts
// its bearer token, so a test decides which identity may use the vault.
// The shapes follow https://learn.microsoft.com/rest/api/keyvault/secrets.
package keyvault

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

type attributes struct {
	Enabled       *bool  `json:"enabled,omitempty"`
	NotBefore     *int64 `json:"nbf,omitempty"`
	Expires       *int64 `json:"exp,omitempty"`
	Created       *int64 `json:"created,omitempty"`
	Updated       *int64 `json:"updated,omitempty"`
	RecoveryLevel string `json:"recoveryLevel,omitempty"`
}

type bundle struct {
	ID          string            `json:"id,omitempty"`
	Value       *string           `json:"value,omitempty"`
	ContentType *string           `json:"contentType,omitempty"`
	Attributes  *attributes       `json:"attributes,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	// Deleted secrets only.
	RecoveryID         string `json:"recoveryId,omitempty"`
	DeletedDate        *int64 `json:"deletedDate,omitempty"`
	ScheduledPurgeDate *int64 `json:"scheduledPurgeDate,omitempty"`
}

type secret struct {
	versions []bundle // oldest first
}

// Vault is the fake. Its zero value is not usable; use New.
type Vault struct {
	// Authorize decides whether a bearer token may use the vault.
	Authorize func(token string) bool

	mu      sync.Mutex
	base    string
	live    map[string]*secret
	deleted map[string]*secret
	// Certificates, live and soft-deleted.
	certs        map[string]*certificate
	deletedCerts map[string]*certificate
	// Log records every request: "METHOD path".
	Log []string
}

// New returns a fake vault serving at baseURL (https://host[:port]).
func New(baseURL string, authorize func(token string) bool) *Vault {
	return &Vault{Authorize: authorize, base: strings.TrimSuffix(baseURL, "/"), live: map[string]*secret{}, deleted: map[string]*secret{},
		certs: map[string]*certificate{}, deletedCerts: map[string]*certificate{}}
}

// SetBaseURL sets the URL IDs are written with (when the listener's address
// is known only after New).
func (v *Vault) SetBaseURL(u string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.base = strings.TrimSuffix(u, "/")
}

// Value returns the current value of a live secret, for tests.
func (v *Vault) Value(name string) (string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	s, ok := v.live[strings.ToLower(name)]
	if !ok || len(s.versions) == 0 || s.versions[len(s.versions)-1].Value == nil {
		return "", false
	}
	return *s.versions[len(s.versions)-1].Value, true
}

// Latest is a live secret's latest version: its ID and tags, for tests.
func (v *Vault) Latest(name string) (id string, tags map[string]string, ok bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	s, ok := v.live[strings.ToLower(name)]
	if !ok || len(s.versions) == 0 {
		return "", nil, false
	}
	b := s.versions[len(s.versions)-1]
	tags = map[string]string{}
	for k, val := range b.Tags {
		tags[k] = val
	}
	return b.ID, tags, true
}

// Versions is how many versions a live secret has, for tests.
func (v *Vault) Versions(name string) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	if s, ok := v.live[strings.ToLower(name)]; ok {
		return len(s.versions)
	}
	return 0
}

// IsDeleted is whether a soft-deleted secret holds the name, for tests.
func (v *Vault) IsDeleted(name string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, ok := v.deleted[strings.ToLower(name)]
	return ok
}

// Enabled is the current version's enabled attribute, for tests.
func (v *Vault) Enabled(name string) (bool, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	s, ok := v.live[strings.ToLower(name)]
	if !ok {
		return false, false
	}
	b := s.versions[len(s.versions)-1]
	return b.Attributes == nil || b.Attributes.Enabled == nil || *b.Attributes.Enabled, true
}

// ServeHTTP implements the data plane.
func (v *Vault) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.Log = append(v.Log, r.Method+" "+r.URL.Path)

	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || v.Authorize == nil || !v.Authorize(strings.TrimPrefix(auth, "Bearer ")) {
		w.Header().Set("WWW-Authenticate", `Bearer authorization="https://login.microsoftonline.com/common", resource="https://vault.azure.net"`)
		writeError(w, http.StatusUnauthorized, "Unauthorized", "", "fake vault: this identity may not use the vault")
		return
	}
	if r.URL.Query().Get("api-version") == "" {
		writeError(w, http.StatusBadRequest, "BadParameter", "", "api-version is required")
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if v.serveCertificates(w, r, parts) {
		return
	}
	switch {
	case len(parts) == 2 && parts[0] == "secrets" && r.Method == http.MethodPut:
		v.set(w, r, parts[1])
	case (len(parts) == 2 || len(parts) == 3) && parts[0] == "secrets" && r.Method == http.MethodGet:
		version := ""
		if len(parts) == 3 {
			version = parts[2]
		}
		v.get(w, parts[1], version)
	case len(parts) == 3 && parts[0] == "secrets" && r.Method == http.MethodPatch:
		v.update(w, r, parts[1], parts[2])
	case len(parts) == 2 && parts[0] == "secrets" && r.Method == http.MethodDelete:
		v.softDelete(w, parts[1])
	case len(parts) == 2 && parts[0] == "deletedsecrets" && r.Method == http.MethodGet:
		v.getDeleted(w, parts[1])
	case len(parts) == 2 && parts[0] == "deletedsecrets" && r.Method == http.MethodDelete:
		v.purge(w, parts[1])
	case len(parts) == 3 && parts[0] == "deletedsecrets" && parts[2] == "recover" && r.Method == http.MethodPost:
		v.recover(w, parts[1])
	default:
		writeError(w, http.StatusNotFound, "NotFound", "", "fake vault: no such operation")
	}
}

func (v *Vault) set(w http.ResponseWriter, r *http.Request, name string) {
	key := strings.ToLower(name)
	if _, ok := v.deleted[key]; ok {
		writeError(w, http.StatusConflict, "Conflict", "ObjectIsDeletedButRecoverable",
			"Secret "+name+" is currently in a deleted but recoverable state, and its name cannot be reused; in this state, the secret can only be recovered or purged.")
		return
	}
	var in bundle
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Value == nil {
		writeError(w, http.StatusBadRequest, "BadParameter", "", "a value is required")
		return
	}
	now := time.Now().Unix()
	attrs := &attributes{Enabled: ptr(true), Created: &now, Updated: &now, RecoveryLevel: "Recoverable+Purgeable"}
	if in.Attributes != nil {
		if in.Attributes.Enabled != nil {
			attrs.Enabled = in.Attributes.Enabled
		}
		attrs.NotBefore, attrs.Expires = in.Attributes.NotBefore, in.Attributes.Expires
	}
	b := bundle{ID: v.base + "/secrets/" + name + "/" + newVersion(), Value: in.Value, ContentType: in.ContentType, Attributes: attrs, Tags: in.Tags}
	s, ok := v.live[key]
	if !ok {
		s = &secret{}
		v.live[key] = s
	}
	s.versions = append(s.versions, b)
	writeJSON(w, http.StatusOK, b)
}

func (v *Vault) find(name, version string) (*bundle, bool) {
	s, ok := v.live[strings.ToLower(name)]
	if !ok || len(s.versions) == 0 {
		return nil, false
	}
	if version == "" {
		return &s.versions[len(s.versions)-1], true
	}
	for i := range s.versions {
		if strings.HasSuffix(s.versions[i].ID, "/"+version) {
			return &s.versions[i], true
		}
	}
	return nil, false
}

func (v *Vault) get(w http.ResponseWriter, name, version string) {
	b, ok := v.find(name, version)
	if !ok {
		writeError(w, http.StatusNotFound, "SecretNotFound", "", "A secret with (name/id) "+name+" was not found in this key vault.")
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (v *Vault) update(w http.ResponseWriter, r *http.Request, name, version string) {
	b, ok := v.find(name, version)
	if !ok {
		writeError(w, http.StatusNotFound, "SecretNotFound", "", "A secret with (name/id) "+name+"/"+version+" was not found in this key vault.")
		return
	}
	var in bundle
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "BadParameter", "", err.Error())
		return
	}
	now := time.Now().Unix()
	if in.ContentType != nil {
		b.ContentType = in.ContentType
	}
	if in.Tags != nil {
		b.Tags = in.Tags
	}
	if in.Attributes != nil {
		if in.Attributes.Enabled != nil {
			b.Attributes.Enabled = in.Attributes.Enabled
		}
		b.Attributes.NotBefore, b.Attributes.Expires = in.Attributes.NotBefore, in.Attributes.Expires
	}
	b.Attributes.Updated = &now
	out := *b
	out.Value = nil
	writeJSON(w, http.StatusOK, out)
}

func (v *Vault) softDelete(w http.ResponseWriter, name string) {
	key := strings.ToLower(name)
	s, ok := v.live[key]
	if !ok {
		writeError(w, http.StatusNotFound, "SecretNotFound", "", "A secret with (name/id) "+name+" was not found in this key vault.")
		return
	}
	delete(v.live, key)
	v.deleted[key] = s
	writeJSON(w, http.StatusOK, v.deletedBundle(name, s))
}

func (v *Vault) deletedBundle(name string, s *secret) bundle {
	b := s.versions[len(s.versions)-1]
	b.Value = nil
	now := time.Now().Unix()
	purge := now + 90*24*3600
	b.RecoveryID = v.base + "/deletedsecrets/" + name
	b.DeletedDate, b.ScheduledPurgeDate = &now, &purge
	return b
}

func (v *Vault) getDeleted(w http.ResponseWriter, name string) {
	s, ok := v.deleted[strings.ToLower(name)]
	if !ok {
		writeError(w, http.StatusNotFound, "SecretNotFound", "", "Deleted Secret not found")
		return
	}
	writeJSON(w, http.StatusOK, v.deletedBundle(name, s))
}

func (v *Vault) purge(w http.ResponseWriter, name string) {
	key := strings.ToLower(name)
	if _, ok := v.deleted[key]; !ok {
		writeError(w, http.StatusNotFound, "SecretNotFound", "", "Deleted Secret not found")
		return
	}
	delete(v.deleted, key)
	w.WriteHeader(http.StatusNoContent)
}

func (v *Vault) recover(w http.ResponseWriter, name string) {
	key := strings.ToLower(name)
	s, ok := v.deleted[key]
	if !ok {
		writeError(w, http.StatusNotFound, "SecretNotFound", "", "Deleted Secret not found")
		return
	}
	delete(v.deleted, key)
	v.live[key] = s
	b := s.versions[len(s.versions)-1]
	b.Value = nil
	writeJSON(w, http.StatusOK, b)
}

func newVersion() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func ptr[T any](v T) *T { return &v }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, inner, message string) {
	e := map[string]any{"code": code, "message": message}
	if inner != "" {
		e["innererror"] = map[string]any{"code": inner}
	}
	writeJSON(w, status, map[string]any{"error": e})
}
