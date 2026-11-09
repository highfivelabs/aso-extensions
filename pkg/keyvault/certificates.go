// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package keyvault

import (
	"context"
	"crypto/sha1" //nolint:gosec // Key Vault's x5t is a SHA-1 thumbprint, not a security check.
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// PEMContentType is the content type Key Vault imports PEM with, and gives
// the certificate's secret.
const PEMContentType = "application/x-pem-file"

// Certificate is a certificate bundle (REST 7.5).
// https://learn.microsoft.com/rest/api/keyvault/certificates
type Certificate struct {
	ID         string            `json:"id,omitempty"`
	KID        string            `json:"kid,omitempty"`
	SID        string            `json:"sid,omitempty"`
	X5T        string            `json:"x5t,omitempty"`
	CER        string            `json:"cer,omitempty"`
	Attributes *Attributes       `json:"attributes,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
}

// Version is the version segment of the certificate's ID.
func (c *Certificate) Version() string {
	i := strings.LastIndex(c.ID, "/")
	if i < 0 {
		return ""
	}
	return c.ID[i+1:]
}

// ThumbprintHex is the certificate's SHA-1 thumbprint in upper-case hex,
// from its x5t (base64url).
func (c *Certificate) ThumbprintHex() string {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(c.X5T, "="))
	if err != nil {
		return ""
	}
	return strings.ToUpper(hex.EncodeToString(b))
}

// ImportRequest is the body of a certificate import.
type ImportRequest struct {
	Value      string            `json:"value"`
	Policy     *ImportPolicy     `json:"policy,omitempty"`
	Attributes *Attributes       `json:"attributes,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
}

// ImportPolicy carries the content type of what is imported.
type ImportPolicy struct {
	SecretProperties *SecretProperties `json:"secret_props,omitempty"`
}

// SecretProperties are the certificate secret's properties.
type SecretProperties struct {
	ContentType string `json:"contentType,omitempty"`
}

// CertificateUpdate changes a certificate version's attributes and tags.
type CertificateUpdate struct {
	Attributes *Attributes       `json:"attributes,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
}

func certificatePath(name string) string { return "/certificates/" + url.PathEscape(name) }

// GetCertificate returns the certificate's current version.
func (c *Client) GetCertificate(ctx context.Context, name string) (*Certificate, error) {
	var out Certificate
	err := c.do(ctx, http.MethodGet, certificatePath(name)+"/", nil, &out, http.StatusOK)
	return &out, err
}

// ImportCertificate imports a certificate with its private key as a new
// version.
func (c *Client) ImportCertificate(ctx context.Context, name string, req ImportRequest) (*Certificate, error) {
	var out Certificate
	err := c.do(ctx, http.MethodPost, certificatePath(name)+"/import", req, &out, http.StatusOK)
	return &out, err
}

// UpdateCertificate changes the attributes and tags of a version.
func (c *Client) UpdateCertificate(ctx context.Context, name, version string, u CertificateUpdate) (*Certificate, error) {
	var out Certificate
	err := c.do(ctx, http.MethodPatch, certificatePath(name)+"/"+url.PathEscape(version), u, &out, http.StatusOK)
	return &out, err
}

// DeleteCertificate soft-deletes the certificate, with its key and secret.
func (c *Client) DeleteCertificate(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, certificatePath(name), nil, nil, http.StatusOK)
}

// GetDeletedCertificate returns the soft-deleted certificate holding the name.
func (c *Client) GetDeletedCertificate(ctx context.Context, name string) (*Certificate, error) {
	var out Certificate
	err := c.do(ctx, http.MethodGet, "/deletedcertificates/"+url.PathEscape(name), nil, &out, http.StatusOK)
	return &out, err
}

// RecoverCertificate recovers a soft-deleted certificate.
func (c *Client) RecoverCertificate(ctx context.Context, name string) (*Certificate, error) {
	var out Certificate
	err := c.do(ctx, http.MethodPost, "/deletedcertificates/"+url.PathEscape(name)+"/recover", nil, &out, http.StatusOK)
	return &out, err
}

// PurgeCertificate removes a soft-deleted certificate for good.
func (c *Client) PurgeCertificate(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/deletedcertificates/"+url.PathEscape(name), nil, nil, http.StatusNoContent)
}

// ParsedPEM is a certificate chain and its private key, checked to match.
type ParsedPEM struct {
	// Leaf is the first certificate.
	Leaf *x509.Certificate
	// Thumbprint is the leaf's SHA-1 thumbprint, base64url as Key Vault's x5t.
	Thumbprint string
	// PEM is the private key followed by the certificates, as Key Vault
	// imports it.
	PEM string
}

// ParsePEM checks that certPEM holds at least one certificate and keyPEM a
// private key matching the first, and joins them for import.
func ParsePEM(certPEM, keyPEM string) (*ParsedPEM, error) {
	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return nil, fmt.Errorf("the certificate and private key do not make a pair: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return nil, errors.New("no certificate found")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parsing the certificate: %w", err)
	}
	sum := sha1.Sum(pair.Certificate[0]) //nolint:gosec // see the import
	var b strings.Builder
	b.WriteString(strings.TrimSpace(keyPEM))
	b.WriteString("\n")
	for _, der := range pair.Certificate {
		_ = pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	}
	return &ParsedPEM{Leaf: leaf, Thumbprint: base64.RawURLEncoding.EncodeToString(sum[:]), PEM: b.String()}, nil
}
