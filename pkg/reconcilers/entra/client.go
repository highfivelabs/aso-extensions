/*
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 */

// Modified by High Five Labs: from Azure Service Operator's
// v2/internal/reconcilers/entra/entra_client_cache.go (and entra_client.go)
// at the pin. Changes: the credential provider is pkg/outoftree's; a client
// has an Endpoint (Microsoft Graph's address, for a national cloud or the
// fake) whose host the token is for and requests go to; a client is the
// Graph service client alone.

package entra

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"

	msgraphsdk "github.com/microsoftgraph/msgraph-beta-sdk-go"
	msgraphauth "github.com/microsoftgraph/msgraph-sdk-go-core/authentication"
	"github.com/rotisserie/eris"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"
)

// DefaultGraphEndpoint is Microsoft Graph in Azure's public cloud.
const DefaultGraphEndpoint = "https://graph.microsoft.com"

// GraphClientCache holds a Microsoft Graph client for each credential.
type GraphClientCache struct {
	lock               sync.Mutex
	clients            map[string]*graphClient
	credentialProvider outoftree.CredentialProvider

	// Endpoint is Microsoft Graph's address (empty: DefaultGraphEndpoint);
	// the beta API is under it, at /beta, as ASO's Entra kinds use it.
	Endpoint string
	// HTTPClient is what every Graph request goes through; nil is the
	// default transport.
	HTTPClient *http.Client
}

type graphClient struct {
	client     *msgraphsdk.GraphServiceClient
	credential *outoftree.Credential
}

// NewGraphClientCache makes a cache that finds credentials with credentialProvider.
func NewGraphClientCache(credentialProvider outoftree.CredentialProvider) *GraphClientCache {
	return &GraphClientCache{
		clients:            make(map[string]*graphClient),
		credentialProvider: credentialProvider,
	}
}

// GetClient returns the Graph client for obj's credential.
func (c *GraphClientCache) GetClient(ctx context.Context, obj genruntime.MetaObject) (*msgraphsdk.GraphServiceClient, error) {
	cred, err := c.credentialProvider.GetCredential(ctx, obj)
	if err != nil {
		return nil, err
	}

	key := cred.CredentialFrom().String()
	c.lock.Lock()
	cached, ok := c.clients[key]
	c.lock.Unlock()
	if ok && cred.SecretsEqual(cached.credential) {
		return cached.client, nil
	}

	endpoint := strings.TrimSuffix(c.Endpoint, "/")
	if endpoint == "" {
		endpoint = DefaultGraphEndpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return nil, eris.Errorf("the Microsoft Graph endpoint %q is no URL", c.Endpoint)
	}

	// Reuse the Azure credential we already have, with a token for the
	// endpoint's own audience (ASO's cache leaves the scope to the SDK,
	// which assumes the public cloud).
	authProvider, err := msgraphauth.NewAzureIdentityAuthenticationProviderWithScopesAndValidHosts(
		cred.TokenCredential(),
		[]string{endpoint + "/.default"},
		[]string{u.Host},
	)
	if err != nil {
		return nil, err
	}

	// We need to use this factory method in order to set the httpClient.
	requestAdapter, err := msgraphsdk.NewGraphRequestAdapterWithParseNodeFactoryAndSerializationWriterFactoryAndHttpClient(
		authProvider,
		nil, // ParseNodeFactory
		nil, // SerializationWriterFactory
		c.HTTPClient,
	)
	if err != nil {
		return nil, err
	}
	requestAdapter.SetBaseUrl(endpoint + "/beta")

	client := &graphClient{client: msgraphsdk.NewGraphServiceClient(requestAdapter), credential: cred}
	c.lock.Lock()
	c.clients[key] = client
	c.lock.Unlock()

	return client.client, nil
}
