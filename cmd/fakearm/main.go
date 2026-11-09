// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// fakearm serves the fake Resource Manager (pkg/fakecloud/arm) over TLS, for
// the install test on kind. Tests only; not in the manager's image. It
// accepts fake Entra tokens (pkg/fakecloud/entra) for --audience whose
// client and subject are listed with --trust, and starts with the
// resources --resource names.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/arm"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/entra"
)

type listFlag []string

func (l *listFlag) String() string     { return fmt.Sprint([]string(*l)) }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

func main() {
	listen := flag.String("listen", ":8443", "address to serve on")
	cert := flag.String("cert", "/tls/tls.crt", "server certificate")
	key := flag.String("key", "/tls/tls.key", "server key")
	audience := flag.String("audience", "", "the Resource Manager audience tokens must be for")
	var trust, resources listFlag
	flag.Var(&trust, "trust", "<client ID>=<subject> a token may carry; repeat")
	flag.Var(&resources, "resource", "ARM IDs (comma-separated) of resources that exist at start; repeat")
	flag.Parse()

	scope := strings.TrimSuffix(*audience, "/") + "/.default"
	trusted := map[string]bool{}
	for _, t := range trust {
		if !strings.Contains(t, "=") {
			log.Fatalf("--trust wants <client ID>=<subject>, got %q", t)
		}
		trusted[t] = true
	}
	a := arm.New(func(token string) bool {
		c, err := entra.Parse(token)
		ok := err == nil && c.Scope == scope && trusted[c.ClientID+"="+c.Subject]
		log.Printf("token client=%s sub=%s scope=%s accepted=%v", c.ClientID, c.Subject, c.Scope, ok)
		return ok
	})
	for _, list := range resources {
		for _, id := range strings.Split(list, ",") {
			if id = strings.TrimSpace(id); id != "" {
				a.AddResource(id)
			}
		}
	}
	log.Printf("fake resource manager (audience %s) on %s, trusting %v, with %v", *audience, *listen, trust, resources)
	logged := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		a.ServeHTTP(rec, r)
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
