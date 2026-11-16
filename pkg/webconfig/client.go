// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

// Package webconfig is a small Resource Manager client for an App Service
// app's app settings and connection strings (Microsoft.Web
// sites/config/appsettings and .../connectionstrings, and the same under a
// slot): a PUT that replaces the whole set, and the POST .../list that reads
// it back (the sections have no GET). It signs requests with a bearer token
// for Resource Manager's audience from the credential it is given.
// https://learn.microsoft.com/rest/api/appservice/web-apps/update-application-settings
package webconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// APIVersion is the Microsoft.Web API version used: ASO's Site's.
const APIVersion = "2026-07-15"

// ConnectionString is a connection string's value and type
// (ConnStringValueTypePair).
type ConnectionString struct {
	Value string `json:"value"`
	Type  string `json:"type"`
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

// StatusCode is the HTTP status of an *Error, or 0.
func StatusCode(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.StatusCode
	}
	return 0
}

// Client talks to Resource Manager.
type Client struct {
	endpoint string
	pipeline runtime.Pipeline
}

// New returns a client for the Resource Manager at endpoint, authenticating
// with cred for audience.
func New(endpoint, audience string, cred azcore.TokenCredential, opts *azcore.ClientOptions) (*Client, error) {
	if endpoint == "" || audience == "" {
		return nil, errors.New("webconfig: the Resource Manager endpoint and audience are required")
	}
	if opts == nil {
		opts = &azcore.ClientOptions{}
	}
	scope := strings.TrimSuffix(audience, "/") + "/.default"
	auth := runtime.NewBearerTokenPolicy(cred, []string{scope}, nil)
	pl := runtime.NewPipeline("hfl-webconfig", "v1", runtime.PipelineOptions{PerRetry: []policy.Policy{auth}}, opts)
	return &Client{endpoint: strings.TrimSuffix(endpoint, "/"), pipeline: pl}, nil
}

// ListAppSettings reads the app settings of the section id
// (.../config/appsettings).
func (c *Client) ListAppSettings(ctx context.Context, id string) (map[string]string, error) {
	var out struct {
		Properties map[string]string `json:"properties"`
	}
	if err := c.do(ctx, http.MethodPost, id+"/list", nil, &out); err != nil {
		return nil, err
	}
	return out.Properties, nil
}

// PutAppSettings replaces the app settings of the section id with settings.
func (c *Client) PutAppSettings(ctx context.Context, id string, settings map[string]string) error {
	if settings == nil {
		settings = map[string]string{}
	}
	return c.do(ctx, http.MethodPut, id, map[string]any{"properties": settings}, nil)
}

// ListConnectionStrings reads the connection strings of the section id
// (.../config/connectionstrings).
func (c *Client) ListConnectionStrings(ctx context.Context, id string) (map[string]ConnectionString, error) {
	var out struct {
		Properties map[string]ConnectionString `json:"properties"`
	}
	if err := c.do(ctx, http.MethodPost, id+"/list", nil, &out); err != nil {
		return nil, err
	}
	return out.Properties, nil
}

// PutConnectionStrings replaces the connection strings of the section id.
func (c *Client) PutConnectionStrings(ctx context.Context, id string, strs map[string]ConnectionString) error {
	if strs == nil {
		strs = map[string]ConnectionString{}
	}
	return c.do(ctx, http.MethodPut, id, map[string]any{"properties": strs}, nil)
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	req, err := runtime.NewRequest(ctx, method, c.endpoint+path+"?api-version="+APIVersion)
	if err != nil {
		return err
	}
	req.Raw().Header.Set("Accept", "application/json")
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
	if resp.StatusCode != http.StatusOK {
		return readError(resp)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func readError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	e := &Error{StatusCode: resp.StatusCode}
	if json.Unmarshal(b, &body) == nil && body.Error.Code != "" {
		e.Code, e.Message = body.Error.Code, body.Error.Message
	} else {
		e.Message = strings.TrimSpace(string(b))
	}
	return e
}
