// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Package keyvault is a small client for the Key Vault secrets data plane
// (REST API 7.5): set, get, update attributes, delete, and the soft-delete
// operations (get deleted, recover, purge). It signs requests with a bearer
// token for the vault's audience from the credential it is given.
// https://learn.microsoft.com/rest/api/keyvault/secrets
package keyvault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// APIVersion is the Key Vault data plane version used.
const APIVersion = "7.5"

// Attributes are a secret's attributes. Times are Unix seconds.
type Attributes struct {
	Enabled       *bool  `json:"enabled,omitempty"`
	NotBefore     *int64 `json:"nbf,omitempty"`
	Expires       *int64 `json:"exp,omitempty"`
	Created       *int64 `json:"created,omitempty"`
	Updated       *int64 `json:"updated,omitempty"`
	RecoveryLevel string `json:"recoveryLevel,omitempty"`
}

// Secret is a secret bundle.
type Secret struct {
	ID          string            `json:"id,omitempty"`
	Value       *string           `json:"value,omitempty"`
	ContentType *string           `json:"contentType,omitempty"`
	Attributes  *Attributes       `json:"attributes,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
}

// Version is the version segment of the secret's ID.
func (s *Secret) Version() string {
	i := strings.LastIndex(s.ID, "/")
	if i < 0 {
		return ""
	}
	return s.ID[i+1:]
}

// Error is an error the vault returned.
type Error struct {
	StatusCode int
	Code       string
	Inner      string
	Message    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("key vault: %d %s %s: %s", e.StatusCode, e.Code, e.Inner, e.Message)
}

// IsNotFound is true for a 404 from the vault.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.StatusCode == http.StatusNotFound
}

// IsDeletedButRecoverable is true when the name is held by a soft-deleted secret.
func IsDeletedButRecoverable(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.StatusCode == http.StatusConflict && e.Inner == "ObjectIsDeletedButRecoverable"
}

// Client talks to one vault.
type Client struct {
	vaultURL string
	pipeline runtime.Pipeline
}

// Scope is the token audience for a vault URL: https://<dns suffix>/.default,
// the vault's host without its first label (vault.azure.net in Azure's
// public cloud, vault.azure.cn, vault.usgovcloudapi.net).
func Scope(vaultURL string) (string, error) {
	u, err := url.Parse(vaultURL)
	if err != nil {
		return "", err
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("vault URL %q is not https", vaultURL)
	}
	host := u.Hostname()
	i := strings.Index(host, ".")
	if i < 0 || i == len(host)-1 {
		return "", fmt.Errorf("vault URL %q has no DNS suffix", vaultURL)
	}
	return "https://" + host[i+1:] + "/.default", nil
}

// New returns a client for the vault, authenticating with cred.
func New(vaultURL string, cred azcore.TokenCredential, opts *azcore.ClientOptions) (*Client, error) {
	scope, err := Scope(vaultURL)
	if err != nil {
		return nil, err
	}
	if opts == nil {
		opts = &azcore.ClientOptions{}
	}
	auth := runtime.NewBearerTokenPolicy(cred, []string{scope}, nil)
	pl := runtime.NewPipeline("hfl-keyvault", "v1", runtime.PipelineOptions{PerRetry: []policy.Policy{auth}}, opts)
	return &Client{vaultURL: strings.TrimSuffix(vaultURL, "/"), pipeline: pl}, nil
}

func (c *Client) do(ctx context.Context, method string, path string, body any, out any, ok ...int) error {
	req, err := runtime.NewRequest(ctx, method, c.vaultURL+path+"?api-version="+APIVersion)
	if err != nil {
		return err
	}
	if body != nil {
		if err := runtime.MarshalAsJSON(req, body); err != nil {
			return err
		}
	}
	resp, err := c.pipeline.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	for _, code := range ok {
		if resp.StatusCode == code {
			if out == nil {
				return nil
			}
			return json.NewDecoder(resp.Body).Decode(out)
		}
	}
	var payload struct {
		Error struct {
			Code       string `json:"code"`
			Message    string `json:"message"`
			InnerError *struct {
				Code string `json:"code"`
			} `json:"innererror"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	e := &Error{StatusCode: resp.StatusCode, Code: payload.Error.Code, Message: payload.Error.Message}
	if payload.Error.InnerError != nil {
		e.Inner = payload.Error.InnerError.Code
	}
	return e
}

func secretPath(name string) string { return "/secrets/" + url.PathEscape(name) }

// Get returns the secret's current version, with its value.
func (c *Client) Get(ctx context.Context, name string) (*Secret, error) {
	var s Secret
	err := c.do(ctx, http.MethodGet, secretPath(name)+"/", nil, &s, http.StatusOK)
	return &s, err
}

// Set makes a new version of the secret.
func (c *Client) Set(ctx context.Context, name string, s Secret) (*Secret, error) {
	var out Secret
	err := c.do(ctx, http.MethodPut, secretPath(name), s, &out, http.StatusOK)
	return &out, err
}

// Update changes the attributes, content type and tags of a version.
func (c *Client) Update(ctx context.Context, name string, version string, s Secret) (*Secret, error) {
	s.Value = nil
	var out Secret
	err := c.do(ctx, http.MethodPatch, secretPath(name)+"/"+url.PathEscape(version), s, &out, http.StatusOK)
	return &out, err
}

// Delete soft-deletes the secret (all its versions).
func (c *Client) Delete(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, secretPath(name), nil, nil, http.StatusOK)
}

// GetDeleted returns the soft-deleted secret holding the name.
func (c *Client) GetDeleted(ctx context.Context, name string) (*Secret, error) {
	var s Secret
	err := c.do(ctx, http.MethodGet, "/deletedsecrets/"+url.PathEscape(name), nil, &s, http.StatusOK)
	return &s, err
}

// Recover recovers a soft-deleted secret.
func (c *Client) Recover(ctx context.Context, name string) (*Secret, error) {
	var s Secret
	err := c.do(ctx, http.MethodPost, "/deletedsecrets/"+url.PathEscape(name)+"/recover", nil, &s, http.StatusOK)
	return &s, err
}

// Purge removes a soft-deleted secret for good.
func (c *Client) Purge(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/deletedsecrets/"+url.PathEscape(name), nil, nil, http.StatusNoContent)
}

// UnixTime is t in Unix seconds, or nil.
func UnixTime(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := t.Unix()
	return &v
}
