// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// fakevault serves the fake Key Vault data plane (pkg/fakecloud/keyvault)
// over TLS, for the install test on kind. Tests only; not in the manager's
// image. It accepts fake Entra tokens (pkg/fakecloud/entra) for the vault's
// audience whose client and subject are listed with --trust.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/entra"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/keyvault"
	kvclient "github.com/highfivelabs/aso-extensions/pkg/keyvault"
)

type trustFlag map[string]bool

func (t trustFlag) String() string { return fmt.Sprint(map[string]bool(t)) }
func (t trustFlag) Set(v string) error {
	if !strings.Contains(v, "=") {
		return fmt.Errorf("want <client ID>=<subject>, got %q", v)
	}
	t[v] = true
	return nil
}

func main() {
	listen := flag.String("listen", ":8443", "address to serve on")
	cert := flag.String("cert", "/tls/tls.crt", "server certificate")
	key := flag.String("key", "/tls/tls.key", "server key")
	baseURL := flag.String("base-url", "", "the vault's URL, as clients name it (https://host)")
	trust := trustFlag{}
	flag.Var(trust, "trust", "<client ID>=<subject> a token may carry; repeat")
	flag.Parse()

	scope, err := kvclient.Scope(*baseURL)
	if err != nil {
		log.Fatal(err)
	}
	v := keyvault.New(*baseURL, func(token string) bool {
		c, err := entra.Parse(token)
		ok := err == nil && c.Scope == scope && trust[c.ClientID+"="+c.Subject]
		log.Printf("token client=%s sub=%s scope=%s accepted=%v", c.ClientID, c.Subject, c.Scope, ok)
		return ok
	})
	log.Printf("fake vault %s (audience %s) on %s, trusting %v", *baseURL, scope, *listen, trust)
	logged := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("request %s %s", r.Method, r.URL.Path)
		v.ServeHTTP(w, r)
	})
	log.Fatal(http.ListenAndServeTLS(*listen, *cert, *key, logged))
}
