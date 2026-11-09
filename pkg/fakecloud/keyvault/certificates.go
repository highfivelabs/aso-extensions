// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package keyvault

import (
	"crypto/sha1" //nolint:gosec // Key Vault's x5t is a SHA-1 thumbprint.
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// The fake's certificates follow
// https://learn.microsoft.com/rest/api/keyvault/certificates: an import
// takes a PEM holding the certificate and its private key (policy
// secret_props.contentType application/x-pem-file; PFX is not faked), and
// Key Vault keeps, beside the certificate, a secret of the same name and
// version whose value is that PEM, which is what services such as App
// Service read. Deleting a certificate soft-deletes its secret too.

const pemContentType = "application/x-pem-file"

type certBundle struct {
	ID         string            `json:"id,omitempty"`
	KID        string            `json:"kid,omitempty"`
	SID        string            `json:"sid,omitempty"`
	X5T        string            `json:"x5t,omitempty"`
	CER        string            `json:"cer,omitempty"`
	Attributes *attributes       `json:"attributes,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Policy     *certPolicy       `json:"policy,omitempty"`
	// Deleted certificates only.
	RecoveryID         string `json:"recoveryId,omitempty"`
	DeletedDate        *int64 `json:"deletedDate,omitempty"`
	ScheduledPurgeDate *int64 `json:"scheduledPurgeDate,omitempty"`
}

type certPolicy struct {
	SecretProps *struct {
		ContentType string `json:"contentType,omitempty"`
	} `json:"secret_props,omitempty"`
}

type certificate struct {
	versions []certBundle // oldest first
}

// CertificateVersions is how many versions a live certificate has, for tests.
func (v *Vault) CertificateVersions(name string) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	if c, ok := v.certs[strings.ToLower(name)]; ok {
		return len(c.versions)
	}
	return 0
}

// CertificateThumbprint is the current version's x5t, and whether it is
// enabled, for tests.
func (v *Vault) CertificateThumbprint(name string) (x5t string, enabled bool, ok bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	c, ok := v.certs[strings.ToLower(name)]
	if !ok {
		return "", false, false
	}
	b := c.versions[len(c.versions)-1]
	return b.X5T, b.Attributes.Enabled == nil || *b.Attributes.Enabled, true
}

// IsCertificateDeleted is whether a soft-deleted certificate holds the name, for tests.
func (v *Vault) IsCertificateDeleted(name string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, ok := v.deletedCerts[strings.ToLower(name)]
	return ok
}

// serveCertificates handles the certificate operations; false when the
// request is not one.
func (v *Vault) serveCertificates(w http.ResponseWriter, r *http.Request, parts []string) bool {
	switch {
	case len(parts) == 3 && parts[0] == "certificates" && parts[2] == "import" && r.Method == http.MethodPost:
		v.importCertificate(w, r, parts[1])
	case (len(parts) == 2 || len(parts) == 3) && parts[0] == "certificates" && r.Method == http.MethodGet:
		version := ""
		if len(parts) == 3 {
			version = parts[2]
		}
		v.getCertificate(w, parts[1], version)
	case len(parts) == 3 && parts[0] == "certificates" && r.Method == http.MethodPatch:
		v.updateCertificate(w, r, parts[1], parts[2])
	case len(parts) == 2 && parts[0] == "certificates" && r.Method == http.MethodDelete:
		v.deleteCertificate(w, parts[1])
	case len(parts) == 2 && parts[0] == "deletedcertificates" && r.Method == http.MethodGet:
		c, ok := v.deletedCerts[strings.ToLower(parts[1])]
		if !ok {
			writeError(w, http.StatusNotFound, "CertificateNotFound", "", "Deleted Certificate not found")
			return true
		}
		writeJSON(w, http.StatusOK, v.deletedCertBundle(parts[1], c))
	case len(parts) == 2 && parts[0] == "deletedcertificates" && r.Method == http.MethodDelete:
		key := strings.ToLower(parts[1])
		if _, ok := v.deletedCerts[key]; !ok {
			writeError(w, http.StatusNotFound, "CertificateNotFound", "", "Deleted Certificate not found")
			return true
		}
		delete(v.deletedCerts, key)
		delete(v.deleted, key)
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 3 && parts[0] == "deletedcertificates" && parts[2] == "recover" && r.Method == http.MethodPost:
		key := strings.ToLower(parts[1])
		c, ok := v.deletedCerts[key]
		if !ok {
			writeError(w, http.StatusNotFound, "CertificateNotFound", "", "Deleted Certificate not found")
			return true
		}
		delete(v.deletedCerts, key)
		v.certs[key] = c
		if s, ok := v.deleted[key]; ok {
			delete(v.deleted, key)
			v.live[key] = s
		}
		writeJSON(w, http.StatusOK, c.versions[len(c.versions)-1])
	default:
		return false
	}
	return true
}

func (v *Vault) importCertificate(w http.ResponseWriter, r *http.Request, name string) {
	key := strings.ToLower(name)
	if _, ok := v.deletedCerts[key]; ok {
		writeError(w, http.StatusConflict, "Conflict", "ObjectIsDeletedButRecoverable",
			"Certificate "+name+" is currently in a deleted but recoverable state, and its name cannot be reused; in this state, the certificate can only be recovered or purged.")
		return
	}
	var in struct {
		Value      string            `json:"value"`
		Policy     *certPolicy       `json:"policy"`
		Attributes *attributes       `json:"attributes"`
		Tags       map[string]string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Value == "" {
		writeError(w, http.StatusBadRequest, "BadParameter", "", "a value is required")
		return
	}
	if in.Policy == nil || in.Policy.SecretProps == nil || in.Policy.SecretProps.ContentType != pemContentType {
		writeError(w, http.StatusBadRequest, "BadParameter", "", "fake vault: only PEM import (policy.secret_props.contentType "+pemContentType+") is faked")
		return
	}
	// The PEM must hold a certificate and the private key that matches it.
	pair, err := tls.X509KeyPair([]byte(in.Value), []byte(in.Value))
	if err != nil || len(pair.Certificate) == 0 {
		writeError(w, http.StatusBadRequest, "BadParameter", "",
			"The specified PEM X.509 certificate content can not be read. Please check if certificate is in valid PEM format.")
		return
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		writeError(w, http.StatusBadRequest, "BadParameter", "", err.Error())
		return
	}

	now := time.Now().Unix()
	nbf, exp := leaf.NotBefore.Unix(), leaf.NotAfter.Unix()
	attrs := &attributes{Enabled: ptr(true), NotBefore: &nbf, Expires: &exp, Created: &now, Updated: &now, RecoveryLevel: "Recoverable+Purgeable"}
	if in.Attributes != nil && in.Attributes.Enabled != nil {
		attrs.Enabled = in.Attributes.Enabled
	}
	version := newVersion()
	sum := sha1.Sum(pair.Certificate[0]) //nolint:gosec // see the import
	b := certBundle{
		ID:         v.base + "/certificates/" + name + "/" + version,
		KID:        v.base + "/keys/" + name + "/" + version,
		SID:        v.base + "/secrets/" + name + "/" + version,
		X5T:        base64.RawURLEncoding.EncodeToString(sum[:]),
		CER:        base64.StdEncoding.EncodeToString(pair.Certificate[0]),
		Attributes: attrs,
		Tags:       in.Tags,
		Policy:     in.Policy,
	}
	c, ok := v.certs[key]
	if !ok {
		c = &certificate{}
		v.certs[key] = c
	}
	c.versions = append(c.versions, b)

	// The certificate's secret: the same name and version, the PEM as value.
	value, contentType := in.Value, pemContentType
	sattrs := *attrs
	s, ok := v.live[key]
	if !ok {
		s = &secret{}
		v.live[key] = s
	}
	s.versions = append(s.versions, bundle{ID: b.SID, Value: &value, ContentType: &contentType, Attributes: &sattrs, Tags: in.Tags})
	writeJSON(w, http.StatusOK, b)
}

func (v *Vault) findCertificate(name, version string) (*certBundle, bool) {
	c, ok := v.certs[strings.ToLower(name)]
	if !ok || len(c.versions) == 0 {
		return nil, false
	}
	if version == "" {
		return &c.versions[len(c.versions)-1], true
	}
	for i := range c.versions {
		if strings.HasSuffix(c.versions[i].ID, "/"+version) {
			return &c.versions[i], true
		}
	}
	return nil, false
}

func (v *Vault) getCertificate(w http.ResponseWriter, name, version string) {
	b, ok := v.findCertificate(name, version)
	if !ok {
		writeError(w, http.StatusNotFound, "CertificateNotFound", "", "A certificate with (name/id) "+name+" was not found in this key vault.")
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (v *Vault) updateCertificate(w http.ResponseWriter, r *http.Request, name, version string) {
	b, ok := v.findCertificate(name, version)
	if !ok {
		writeError(w, http.StatusNotFound, "CertificateNotFound", "", "A certificate with (name/id) "+name+"/"+version+" was not found in this key vault.")
		return
	}
	var in struct {
		Attributes *attributes       `json:"attributes"`
		Tags       map[string]string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "BadParameter", "", err.Error())
		return
	}
	now := time.Now().Unix()
	if in.Tags != nil {
		b.Tags = in.Tags
	}
	if in.Attributes != nil && in.Attributes.Enabled != nil {
		b.Attributes.Enabled = in.Attributes.Enabled
	}
	b.Attributes.Updated = &now
	// The secret's version follows.
	if s, ok := v.find(name, version); ok {
		s.Tags = b.Tags
		s.Attributes.Enabled = b.Attributes.Enabled
		s.Attributes.Updated = &now
	}
	writeJSON(w, http.StatusOK, b)
}

func (v *Vault) deleteCertificate(w http.ResponseWriter, name string) {
	key := strings.ToLower(name)
	c, ok := v.certs[key]
	if !ok {
		writeError(w, http.StatusNotFound, "CertificateNotFound", "", "A certificate with (name/id) "+name+" was not found in this key vault.")
		return
	}
	delete(v.certs, key)
	v.deletedCerts[key] = c
	if s, ok := v.live[key]; ok {
		delete(v.live, key)
		v.deleted[key] = s
	}
	writeJSON(w, http.StatusOK, v.deletedCertBundle(name, c))
}

func (v *Vault) deletedCertBundle(name string, c *certificate) certBundle {
	b := c.versions[len(c.versions)-1]
	now := time.Now().Unix()
	purge := now + 90*24*3600
	b.RecoveryID = v.base + "/deletedcertificates/" + name
	b.DeletedDate, b.ScheduledPurgeDate = &now, &purge
	return b
}
