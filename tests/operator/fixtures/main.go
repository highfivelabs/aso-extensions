// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// fixtures writes what the install test on kind needs and the shell cannot
// make: a CA and server certificates for the fake cloud's services, and the
// fake PostgreSQL's init script, whose Entra administrator signs in with the
// fake Entra token of team-a's identity. Tests only.
package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/entra"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/postgres"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/tlsutil"
	postgresqlutil "github.com/highfivelabs/aso-extensions/pkg/util/postgresql"
)

func main() {
	out := flag.String("out", "bin/operator-e2e/fixtures", "directory to write to")
	tenant := flag.String("tenant", "", "tenant ID")
	client := flag.String("client", "", "the client ID of team-a's identity")
	ns := flag.String("namespace", "team-a", "the namespace whose aso-workload is the Entra administrator")
	flag.Parse()

	must(os.MkdirAll(*out, 0o755))
	ca, err := tlsutil.NewCA()
	must(err)
	write(*out, "ca.crt", ca.CertPEM)
	for name, host := range map[string]string{"postgres": "postgres.fake-cloud.svc", "fakevault": "fakevault.fake-cloud.svc", "fakearm": "fakearm.fake-cloud.svc"} {
		c, k, err := ca.Server(host)
		must(err)
		write(*out, name+".crt", c)
		write(*out, name+".key", k)
	}
	// An application's TLS certificate, as cert-manager would leave in a
	// kubernetes.io/tls Secret, for VaultCertificate.
	appCert, appKey, err := ca.Server("app.example")
	must(err)
	write(*out, "app-tls.crt", appCert)
	write(*out, "app-tls.key", appKey)

	token := entra.Token(entra.Claims{Tenant: *tenant, ClientID: *client,
		Subject: entra.ServiceAccountSubject(*ns, "aso-workload"), Scope: postgresqlutil.EntraScope})
	setup := postgres.Setup{
		LocalAdmin: "pgadmin", LocalAdminPassword: "admin-password-1",
		EntraAdmins: map[string]string{"aso-team-a": token},
		Databases:   []string{"app", "orders"},
		Roles:       []string{"app_readers"},
	}
	write(*out, "init.sql", []byte(setup.InitSQL()))
}

func write(dir, name string, b []byte) { must(os.WriteFile(filepath.Join(dir, name), b, 0o644)) }

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
