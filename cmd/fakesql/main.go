// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// fakesql serves the fake Azure SQL Database server (pkg/fakecloud/azuresql)
// over TDS with TLS, for the install test on kind. Tests only; not in the
// manager's image. An access token (pkg/fakecloud/entra) for Azure SQL's
// audience signs in as the principal --principal names for its client and
// subject.
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/azuresql"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/entra"
)

// Scope is Azure SQL's token scope (ASO's azuresql Scope).
const scope = "https://database.windows.net/.default"

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, strings.Split(v, ",")...); return nil }

func main() {
	listen := flag.String("listen", ":1433", "address to serve on")
	cert := flag.String("cert", "/tls/tls.crt", "server certificate")
	key := flag.String("key", "/tls/tls.key", "server key")
	admin := flag.String("admin", "sqladmin", "the SQL administrator's login")
	adminPassword := flag.String("admin-password", "", "the SQL administrator's password")
	entraAdmin := flag.String("entra-admin", "", "the name of the server's Microsoft Entra administrator")
	var principals, directory, databases listFlag
	flag.Var(&principals, "principal", "<name>=<client ID>=<subject>: a token for that client and subject signs in as <name>; repeat")
	flag.Var(&directory, "directory", "Entra principal names CREATE USER ... FROM EXTERNAL PROVIDER may name; comma-separated, repeat")
	flag.Var(&databases, "database", "databases; comma-separated, repeat")
	flag.Parse()

	byClient := map[string]string{}
	for _, p := range principals {
		parts := strings.SplitN(p, "=", 3)
		if len(parts) != 3 {
			log.Fatalf("want <name>=<client ID>=<subject>, got %q", p)
		}
		byClient[parts[1]+"="+parts[2]] = parts[0]
	}
	pair, err := tls.LoadX509KeyPair(*cert, *key)
	if err != nil {
		log.Fatal(err)
	}
	srv := &azuresql.Server{
		Name:          "fakesql",
		TLS:           &tls.Config{Certificates: []tls.Certificate{pair}},
		Admin:         *admin,
		AdminPassword: *adminPassword,
		EntraAdmin:    *entraAdmin,
		Principal: func(token string) string {
			c, err := entra.Parse(token)
			who := ""
			if err == nil && c.Scope == scope {
				who = byClient[c.ClientID+"="+c.Subject]
			}
			log.Printf("token client=%s sub=%s scope=%s principal=%q", c.ClientID, c.Subject, c.Scope, who)
			return who
		},
		Directory: directory,
		Databases: databases,
		Log:       log.New(os.Stderr, "", log.LstdFlags),
	}
	addr, err := srv.Listen(*listen)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(os.Stderr, "fake Azure SQL on %s, databases %v, Entra administrator %q\n", addr, databases, *entraAdmin)
	select {}
}
