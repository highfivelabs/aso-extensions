// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Package armlocks is a small client for Azure Resource Manager's
// management locks (Microsoft.Authorization/locks, API 2020-05-01) at any
// scope: get, create or update, delete. The calls are synchronous; there is
// no long-running operation. It signs requests with a bearer token for the
// Resource Manager audience from the credential it is given.
// https://learn.microsoft.com/rest/api/resources/management-locks
package armlocks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// APIVersion is the Microsoft.Authorization/locks version used.
const APIVersion = "2020-05-01"

// ResourceType is a lock's ARM type.
const ResourceType = "Microsoft.Authorization/locks"

// Properties are a lock's properties.
type Properties struct {
	Level string `json:"level"`
	Notes string `json:"notes,omitempty"`
}

// Lock is a management lock.
type Lock struct {
	ID         string     `json:"id,omitempty"`
	Name       string     `json:"name,omitempty"`
	Type       string     `json:"type,omitempty"`
	Properties Properties `json:"properties"`
}

// Error is an error Resource Manager returned.
type Error struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("resource manager: %d %s: %s", e.StatusCode, e.Code, e.Message)
}

// IsNotFound is true for a 404.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.StatusCode == http.StatusNotFound
}

// ID is the ARM ID of the lock named name on scope.
func ID(scope, name string) string {
	return strings.TrimSuffix(scope, "/") + "/providers/" + ResourceType + "/" + name
}

// Client talks to one Resource Manager endpoint.
type Client struct {
	endpoint string
	pipeline runtime.Pipeline
}

// New returns a client for the Resource Manager at endpoint (such as
// https://management.azure.com), authenticating with a token for audience
// (such as https://management.core.windows.net/).
func New(endpoint, audience string, cred azcore.TokenCredential, opts *azcore.ClientOptions) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("resource manager endpoint %q is not https", endpoint)
	}
	if opts == nil {
		opts = &azcore.ClientOptions{}
	}
	scope := strings.TrimSuffix(audience, "/") + "/.default"
	auth := runtime.NewBearerTokenPolicy(cred, []string{scope}, nil)
	pl := runtime.NewPipeline("hfl-armlocks", "v1", runtime.PipelineOptions{PerRetry: []policy.Policy{auth}}, opts)
	return &Client{endpoint: strings.TrimSuffix(endpoint, "/"), pipeline: pl}, nil
}

func (c *Client) do(ctx context.Context, method, id string, body any, out any, ok ...int) (int, error) {
	req, err := runtime.NewRequest(ctx, method, c.endpoint+id+"?api-version="+APIVersion)
	if err != nil {
		return 0, err
	}
	if body != nil {
		if err := runtime.MarshalAsJSON(req, body); err != nil {
			return 0, err
		}
	}
	resp, err := c.pipeline.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	for _, code := range ok {
		if resp.StatusCode == code {
			if out == nil {
				return code, nil
			}
			return code, json.NewDecoder(resp.Body).Decode(out)
		}
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	return resp.StatusCode, &Error{StatusCode: resp.StatusCode, Code: payload.Error.Code, Message: payload.Error.Message}
}

func lockPath(scope, name string) string {
	return ID(scope, url.PathEscape(name))
}

// Get returns the lock named name on scope.
func (c *Client) Get(ctx context.Context, scope, name string) (*Lock, error) {
	var l Lock
	_, err := c.do(ctx, http.MethodGet, lockPath(scope, name), nil, &l, http.StatusOK)
	return &l, err
}

// CreateOrUpdate sets the lock named name on scope.
func (c *Client) CreateOrUpdate(ctx context.Context, scope, name string, p Properties) (*Lock, error) {
	var l Lock
	_, err := c.do(ctx, http.MethodPut, lockPath(scope, name), Lock{Properties: p}, &l, http.StatusOK, http.StatusCreated)
	return &l, err
}

// Delete removes the lock named name on scope; a lock that is not there
// is not an error.
func (c *Client) Delete(ctx context.Context, scope, name string) error {
	_, err := c.do(ctx, http.MethodDelete, lockPath(scope, name), nil, nil, http.StatusOK, http.StatusNoContent)
	return err
}
