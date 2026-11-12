// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package entra_test

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/entra"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/tlsutil"
)

// serve starts the fake Entra over TLS, as cmd/fakeentra does, and returns
// client options that reach it the way stock ASO does on a cluster whose DNS
// sends login.microsoftonline.com to the fake (a CoreDNS rewrite,
// docs/fake-cloud.md) and which trusts the fake cloud's CA (SSL_CERT_FILE):
// the public cloud's authority host, unchanged, and a dialer that connects
// every host to the server. MSAL's instance discovery goes to
// login.microsoftonline.com whatever the authority host, so the fake must
// answer as that host; nothing here leaves the machine.
func serve(t *testing.T) azcore.ClientOptions {
	t.Helper()
	ca, err := tlsutil.NewCA()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.ServerTLS("login.microsoftonline.com", "fakeentra.fake-cloud.svc")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(entra.Handler(&entra.Transport{}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	client := &http.Client{Transport: &http.Transport{DialContext: dial, TLSClientConfig: &tls.Config{RootCAs: ca.Pool()}}}
	return azcore.ClientOptions{Cloud: cloud.AzurePublic, Transport: client}
}

const scope = "https://fakearm.fake-cloud.svc/.default"

func claims(t *testing.T, cred azcore.TokenCredential) entra.Claims {
	t.Helper()
	tok, err := cred.GetToken(context.Background(), policy.TokenRequestOptions{Scopes: []string{scope}})
	if err != nil {
		t.Fatalf("token over HTTPS: %v", err)
	}
	c, err := entra.Parse(tok.Token)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestHandlerClientSecretOverHTTPS(t *testing.T) {
	opts := serve(t)
	cred, err := azidentity.NewClientSecretCredential("tenant-1", "client-1", "s3cret",
		&azidentity.ClientSecretCredentialOptions{ClientOptions: opts})
	if err != nil {
		t.Fatal(err)
	}
	c := claims(t, cred)
	want := entra.Claims{Tenant: "tenant-1", ClientID: "client-1", Subject: entra.SecretSubject("s3cret"), Scope: scope}
	if c != want {
		t.Fatalf("claims %+v, want %+v", c, want)
	}
}

// Workload identity, ASO's default: the service account token from a file
// is the client assertion; its sub is the token's subject.
func TestHandlerWorkloadIdentityOverHTTPS(t *testing.T) {
	opts := serve(t)
	enc := base64.RawURLEncoding.EncodeToString
	jwt := enc([]byte(`{"alg":"none"}`)) + "." + enc([]byte(`{"sub":"system:serviceaccount:azureserviceoperator-system:azureserviceoperator-default","aud":"api://AzureADTokenExchange"}`)) + ".sig"
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte(jwt), 0o600); err != nil {
		t.Fatal(err)
	}
	cred, err := azidentity.NewWorkloadIdentityCredential(&azidentity.WorkloadIdentityCredentialOptions{
		ClientOptions: opts, TenantID: "tenant-1", ClientID: "client-1", TokenFilePath: file,
	})
	if err != nil {
		t.Fatal(err)
	}
	c := claims(t, cred)
	if c.Subject != entra.ServiceAccountSubject("azureserviceoperator-system", "azureserviceoperator-default") || c.ClientID != "client-1" || c.Tenant != "tenant-1" {
		t.Fatalf("claims %+v", c)
	}
}

// A client that does not trust the fake cloud's CA is refused at TLS: the
// fake is only reachable on purpose.
func TestHandlerRefusesUntrustedClient(t *testing.T) {
	opts := serve(t)
	tr := opts.Transport.(*http.Client).Transport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{}
	opts.Transport = &http.Client{Transport: tr}
	cred, err := azidentity.NewClientSecretCredential("tenant-1", "client-1", "s3cret",
		&azidentity.ClientSecretCredentialOptions{ClientOptions: opts})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cred.GetToken(context.Background(), policy.TokenRequestOptions{Scopes: []string{scope}}); err == nil {
		t.Fatal("a client without the CA got a token")
	}
}
