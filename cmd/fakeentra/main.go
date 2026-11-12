// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// fakeentra serves the fake Microsoft Entra ID token service
// (pkg/fakecloud/entra) over TLS, so stock Azure Service Operator, which
// fetches tokens from Entra over HTTP, can run against the fake cloud. MSAL
// asks login.microsoftonline.com for instance discovery whatever authority
// host it is given, so the fake answers as login.microsoftonline.com: the
// cluster's DNS sends that name to this server (a CoreDNS rewrite) and the
// clients trust the fake cloud's CA (SSL_CERT_FILE); docs/fake-cloud.md.
// Tests only; not in the manager's image.
//
// With --check it is the client instead: it asks the public cloud's Entra for
// a token the way ASO's credentials do (azidentity, unchanged, default cloud)
// and prints the claims the fake put in it, failing if it gets none. The
// install test runs it to prove the path.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/entra"
)

func main() {
	listen := flag.String("listen", ":8443", "address to serve on")
	cert := flag.String("cert", "/tls/tls.crt", "server certificate")
	key := flag.String("key", "/tls/tls.key", "server key")
	check := flag.Bool("check", false, "be the client: get a client-secret token from the public cloud's Entra and print its claims")
	scope := flag.String("scope", "https://management.azure.com/.default", "the scope --check asks for")
	flag.Parse()

	if *check {
		if err := runCheck(*scope); err != nil {
			log.Fatalf("check: %v", err)
		}
		return
	}
	h := entra.Handler(&entra.Transport{})
	logged := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("request %s %s", r.Method, r.URL.Path)
		h.ServeHTTP(w, r)
	})
	log.Printf("fake entra on %s", *listen)
	log.Fatal(http.ListenAndServeTLS(*listen, *cert, *key, logged))
}

func runCheck(scope string) error {
	cred, err := azidentity.NewClientSecretCredential("00000000-0000-0000-0000-00000000000a", "00000000-0000-0000-0000-00000000000b", "fake-cloud-check", nil)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{scope}})
	if err != nil {
		return err
	}
	c, err := entra.Parse(tok.Token)
	if err != nil {
		return fmt.Errorf("the token is not the fake's (did DNS send login.microsoftonline.com elsewhere?): %w", err)
	}
	fmt.Printf("fake entra token: tenant=%s client=%s sub=%s scope=%s\n", c.Tenant, c.ClientID, c.Subject, c.Scope)
	return nil
}
