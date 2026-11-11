// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Command fakegraph serves the fake Microsoft Graph (pkg/fakecloud/graph)
// over TLS, for the kind tests: directory role assignments. It accepts the
// tokens of the identities --trust names for its own audience (the fake
// Entra's tokens, pkg/fakecloud/entra), and logs every token and request.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/entra"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/graph"
)

type listFlag []string

func (l *listFlag) String() string     { return fmt.Sprint([]string(*l)) }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

func main() {
	listen := flag.String("listen", ":8443", "address to serve on")
	cert := flag.String("cert", "/tls/tls.crt", "server certificate")
	key := flag.String("key", "/tls/tls.key", "server key")
	audience := flag.String("audience", "", "the Microsoft Graph audience tokens must be for")
	var trust, principals, units listFlag
	flag.Var(&trust, "trust", "<client ID>=<subject> a token may carry; repeat")
	flag.Var(&principals, "principal", "object IDs (comma-separated) of the directory's users, groups and service principals; repeat")
	flag.Var(&units, "administrative-unit", "IDs (comma-separated) of the directory's administrative units; repeat")
	flag.Parse()

	scope := strings.TrimSuffix(*audience, "/") + "/.default"
	trusted := map[string]bool{}
	for _, t := range trust {
		if !strings.Contains(t, "=") {
			log.Fatalf("--trust wants <client ID>=<subject>, got %q", t)
		}
		trusted[t] = true
	}
	g := graph.New(func(token string) bool {
		c, err := entra.Parse(token)
		ok := err == nil && c.Scope == scope && trusted[c.ClientID+"="+c.Subject]
		log.Printf("token client=%s sub=%s scope=%s accepted=%v", c.ClientID, c.Subject, c.Scope, ok)
		return ok
	})
	each := func(lists listFlag, add func(string)) {
		for _, list := range lists {
			for _, id := range strings.Split(list, ",") {
				if id = strings.TrimSpace(id); id != "" {
					add(id)
				}
			}
		}
	}
	each(principals, g.AddPrincipal)
	each(units, g.AddAdministrativeUnit)
	log.Printf("fake Microsoft Graph (audience %s) on %s, trusting %v, principals %v, administrative units %v", *audience, *listen, trust, principals, units)
	logged := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		g.ServeHTTP(rec, r)
		log.Printf("request %s %s %d", r.Method, r.URL.Path, rec.status)
	})
	log.Fatal(http.ListenAndServeTLS(*listen, *cert, *key, logged))
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
