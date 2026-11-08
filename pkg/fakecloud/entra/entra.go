// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Package entra is a fake Microsoft Entra ID token service, for tests and
// test builds only. It is an http.RoundTripper, so azidentity's real
// credentials (client secret, client assertion, workload identity) run
// unchanged against it: instance discovery, OpenID metadata and the token
// endpoint are answered in process, and nothing leaves the process.
//
// An access token it issues is readable: "fake-entra." followed by the
// base64url JSON of who asked (tenant, client ID, the subject of the client
// assertion or a digest of the client secret) and the scope. The fake
// vault and the fake PostgreSQL decide what to accept from that, which is
// how a test proves which identity an operator used.
package entra

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/msi-dataplane/pkg/dataplane"

	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"
)

// Claims is what a fake access token says.
type Claims struct {
	Tenant   string `json:"tid"`
	ClientID string `json:"appid"`
	// Subject is the client assertion's sub (a Kubernetes service account
	// token: system:serviceaccount:<ns>:<name>), or "secret:<digest>" for
	// a client secret.
	Subject string `json:"sub"`
	Scope   string `json:"scp"`
}

const prefix = "fake-entra."

// Token is the access token the fake issues for c.
func Token(c Claims) string {
	b, _ := json.Marshal(c)
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

// Parse reads a fake access token.
func Parse(token string) (Claims, error) {
	var c Claims
	if !strings.HasPrefix(token, prefix) {
		return c, fmt.Errorf("not a fake-entra token")
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, prefix))
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	return c, err
}

// SecretSubject is the Subject a client secret signs in as.
func SecretSubject(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return "secret:" + hex.EncodeToString(sum[:4])
}

// ServiceAccountSubject is the Subject of a Kubernetes service account.
func ServiceAccountSubject(namespace, name string) string {
	return "system:serviceaccount:" + namespace + ":" + name
}

// Transport answers Entra's endpoints. Requests records what was asked.
type Transport struct {
	mu       sync.Mutex
	Requests []string
}

var _ interface {
	Do(*http.Request) (*http.Response, error)
} = &Transport{}

// Do implements azcore's policy.Transporter.
func (t *Transport) Do(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.Requests = append(t.Requests, req.Method+" "+req.URL.Path)
	t.mu.Unlock()

	host := req.URL.Host
	path := req.URL.Path
	switch {
	case req.Method == http.MethodGet && strings.HasSuffix(path, "/discovery/instance"):
		tenant := tenantFromAuthorize(req.URL.Query().Get("authorization_endpoint"))
		return reply(req, http.StatusOK, map[string]any{
			"tenant_discovery_endpoint": fmt.Sprintf("https://%s/%s/v2.0/.well-known/openid-configuration", host, tenant),
			"api-version":               "1.1",
			"metadata": []map[string]any{{
				"preferred_network": host, "preferred_cache": host, "aliases": []string{host},
			}},
		})
	case req.Method == http.MethodGet && strings.HasSuffix(path, "/.well-known/openid-configuration"):
		tenant := strings.Split(strings.TrimPrefix(path, "/"), "/")[0]
		base := fmt.Sprintf("https://%s/%s", host, tenant)
		return reply(req, http.StatusOK, map[string]any{
			"token_endpoint":                base + "/oauth2/v2.0/token",
			"authorization_endpoint":        base + "/oauth2/v2.0/authorize",
			"issuer":                        base + "/v2.0",
			"device_authorization_endpoint": base + "/oauth2/v2.0/devicecode",
		})
	case req.Method == http.MethodPost && strings.HasSuffix(path, "/oauth2/v2.0/token"):
		return t.token(req)
	}
	return reply(req, http.StatusNotFound, map[string]any{"error": "not_found", "error_description": "fake entra: " + req.Method + " " + path})
}

func (t *Transport) token(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	req.Body = io.NopCloser(bytes.NewReader(body))
	if err := req.ParseForm(); err != nil {
		return reply(req, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
	}
	form := req.PostForm
	tenant := strings.Split(strings.TrimPrefix(req.URL.Path, "/"), "/")[0]
	c := Claims{Tenant: tenant, ClientID: form.Get("client_id"), Scope: resourceScope(form.Get("scope"))}
	switch {
	case form.Get("client_assertion") != "":
		sub, err := jwtSubject(form.Get("client_assertion"))
		if err != nil {
			return reply(req, http.StatusBadRequest, map[string]any{"error": "invalid_client", "error_description": err.Error()})
		}
		c.Subject = sub
	case form.Get("client_secret") != "":
		c.Subject = SecretSubject(form.Get("client_secret"))
	default:
		return reply(req, http.StatusBadRequest, map[string]any{"error": "invalid_client", "error_description": "fake entra: no client credential"})
	}
	return reply(req, http.StatusOK, map[string]any{
		"token_type":     "Bearer",
		"expires_in":     3600,
		"ext_expires_in": 3600,
		"access_token":   Token(c),
	})
}

// resourceScope drops the OpenID scopes MSAL adds to every request
// (openid, offline_access, profile), as Entra does for an app token.
func resourceScope(scope string) string {
	var kept []string
	for _, s := range strings.Fields(scope) {
		switch s {
		case "openid", "offline_access", "profile":
		default:
			kept = append(kept, s)
		}
	}
	return strings.Join(kept, " ")
}

func tenantFromAuthorize(endpoint string) string {
	// https://host/<tenant>/oauth2/v2.0/authorize
	parts := strings.Split(endpoint, "/")
	if len(parts) > 3 {
		return parts[3]
	}
	return "common"
}

// jwtSubject reads the sub claim of a JWT without checking its signature:
// the Kubernetes API server issued it to the operator, which is the part
// under test; Entra's own checks (issuer, audience, federated credential)
// are what a real tenant adds.
func jwtSubject(jwt string) (string, error) {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("client assertion is not a JWT")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	var claims struct {
		Sub string `json:"sub"`
		Aud any    `json:"aud"`
	}
	if err := json.Unmarshal(b, &claims); err != nil {
		return "", err
	}
	if claims.Sub == "" {
		return "", fmt.Errorf("client assertion has no sub")
	}
	return claims.Sub, nil
}

func reply(req *http.Request, status int, v any) (*http.Response, error) {
	b, _ := json.Marshal(v)
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(b)),
		Request:    req,
	}, nil
}

// TokenProvider is ASO's credential factory with every credential's HTTP
// traffic sent to t. The credentials themselves are azidentity's, unchanged.
func TokenProvider(t *Transport) outoftree.TokenCredentialProvider {
	return &tokenProvider{inner: outoftree.DefaultTokenCredentialProvider(), t: t}
}

type tokenProvider struct {
	inner outoftree.TokenCredentialProvider
	t     *Transport
}

func (p *tokenProvider) NewClientSecretCredential(tenantID, clientID, clientSecret string, o *azidentity.ClientSecretCredentialOptions) (*azidentity.ClientSecretCredential, error) {
	if o == nil {
		o = &azidentity.ClientSecretCredentialOptions{}
	}
	o.Transport = p.t
	return p.inner.NewClientSecretCredential(tenantID, clientID, clientSecret, o)
}

func (p *tokenProvider) NewClientCertificateCredential(tenantID, clientID string, cert, password []byte, o *azidentity.ClientCertificateCredentialOptions) (*azidentity.ClientCertificateCredential, error) {
	if o == nil {
		o = &azidentity.ClientCertificateCredentialOptions{}
	}
	o.Transport = p.t
	return p.inner.NewClientCertificateCredential(tenantID, clientID, cert, password, o)
}

func (p *tokenProvider) NewManagedIdentityCredential(*azidentity.ManagedIdentityCredentialOptions) (*azidentity.ManagedIdentityCredential, error) {
	return nil, fmt.Errorf("fake entra: managed identity (IMDS) is not faked")
}

func (p *tokenProvider) NewWorkloadIdentityCredential(o *azidentity.WorkloadIdentityCredentialOptions) (*azidentity.WorkloadIdentityCredential, error) {
	if o == nil {
		o = &azidentity.WorkloadIdentityCredentialOptions{}
	}
	o.Transport = p.t
	return p.inner.NewWorkloadIdentityCredential(o)
}

func (p *tokenProvider) NewUserAssignedIdentityCredentials(context.Context, string, ...dataplane.Option) (azcore.TokenCredential, error) {
	return nil, fmt.Errorf("fake entra: user-assigned identity credentials are not faked")
}

func (p *tokenProvider) NewClientAssertionCredential(tenantID, clientID string, getAssertion func(context.Context) (string, error), o *azidentity.ClientAssertionCredentialOptions) (*azidentity.ClientAssertionCredential, error) {
	if o == nil {
		o = &azidentity.ClientAssertionCredentialOptions{}
	}
	o.Transport = p.t
	return p.inner.NewClientAssertionCredential(tenantID, clientID, getAssertion, o)
}
