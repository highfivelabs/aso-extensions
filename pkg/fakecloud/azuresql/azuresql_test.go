// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package azuresql_test

import (
	"context"
	"crypto/tls"
	"net"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/azuresql"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/tlsutil"
	azuresqlutil "github.com/highfivelabs/aso-extensions/pkg/util/azuresql"
)

const (
	adminToken = "token-of-the-entra-admin"
	appToken   = "token-of-app-identity"
)

// start runs a fake server on localhost and points the copied ASO code
// (pkg/util/azuresql) at it.
func start(t *testing.T) *azuresql.Server {
	t.Helper()
	ca, err := tlsutil.NewCA()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.ServerTLS("localhost")
	if err != nil {
		t.Fatal(err)
	}
	srv := &azuresql.Server{
		Name:          "fakesql",
		TLS:           &tls.Config{Certificates: []tls.Certificate{cert}},
		Admin:         "sqladmin",
		AdminPassword: "admin-password-1",
		EntraAdmin:    "aso-admins",
		Principal: func(token string) string {
			switch token {
			case adminToken:
				return "aso-admins"
			case appToken:
				return "app-identity"
			}
			return ""
		},
		Directory: []string{"app-identity", "aso-admins", "data-readers"},
		Databases: []string{"appdb"},
	}
	addr, err := srv.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	_, port, _ := net.SplitHostPort(addr.String())
	azuresqlutil.ServerPort, _ = strconv.Atoi(port)
	azuresqlutil.TLSRootCAs = ca.Pool()
	return srv
}

func tokenOf(s string) func() (string, error) { return func() (string, error) { return s, nil } }

func TestEntraUserLifecycle(t *testing.T) {
	ctx := context.Background()
	srv := start(t)

	db, err := azuresqlutil.ConnectToDBUsingAAD(ctx, "localhost", "appdb", azuresqlutil.ServerPort, tokenOf(adminToken))
	if err != nil {
		t.Fatalf("Entra administrator signs in: %v", err)
	}
	defer db.Close()

	if err := azuresqlutil.CreateOrUpdateAADUser(ctx, db, "app-identity"); err != nil {
		t.Fatalf("create Entra user: %v", err)
	}
	// Again: nothing to do.
	if err := azuresqlutil.CreateOrUpdateAADUser(ctx, db, "app-identity"); err != nil {
		t.Fatalf("create Entra user again: %v", err)
	}
	exists, err := azuresqlutil.DoesUserExist(ctx, db, "APP-IDENTITY")
	if err != nil || !exists {
		t.Fatalf("user exists (names ignore case): %v %v", exists, err)
	}
	if err := azuresqlutil.ReconcileUserRoles(ctx, db, "app-identity", []string{"db_datareader", "db_datawriter"}); err != nil {
		t.Fatalf("roles: %v", err)
	}
	if err := azuresqlutil.ReconcileUserRoles(ctx, db, "app-identity", []string{"db_datareader"}); err != nil {
		t.Fatalf("roles again: %v", err)
	}
	got := srv.Users("appdb")
	want := []azuresql.User{{Name: "app-identity", External: true, Roles: []string{"db_datareader"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("users = %+v, want %+v", got, want)
	}

	// The new user signs in with its own token, and may not create users.
	app, err := azuresqlutil.ConnectToDBUsingAAD(ctx, "localhost", "appdb", azuresqlutil.ServerPort, tokenOf(appToken))
	if err != nil {
		t.Fatalf("the Entra user signs in: %v", err)
	}
	err = azuresqlutil.CreateOrUpdateAADUser(ctx, app, "data-readers")
	_ = app.Close()
	if err == nil || !strings.Contains(err.Error(), "permission") {
		t.Fatalf("a user that is no administrator creates a user: %v", err)
	}

	if err := azuresqlutil.CreateOrUpdateAADUser(ctx, db, "nobody-in-entra"); err == nil || !strings.Contains(err.Error(), "could not be found") {
		t.Fatalf("a name Entra does not know: %v", err)
	}

	if err := azuresqlutil.DropUser(ctx, db, "app-identity"); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if err := azuresqlutil.DropUser(ctx, db, "app-identity"); err != nil {
		t.Fatalf("drop again (IF EXISTS): %v", err)
	}
	if exists, err := azuresqlutil.DoesUserExist(ctx, db, "app-identity"); err != nil || exists {
		t.Fatalf("user gone: %v %v", exists, err)
	}
	if _, err := azuresqlutil.ConnectToDBUsingAAD(ctx, "localhost", "appdb", azuresqlutil.ServerPort, tokenOf(appToken)); err == nil {
		t.Fatal("a dropped Entra user still signs in")
	}
}

func TestSignInRefusals(t *testing.T) {
	ctx := context.Background()
	start(t)
	if _, err := azuresqlutil.ConnectToDBUsingAAD(ctx, "localhost", "appdb", azuresqlutil.ServerPort, tokenOf("someone-else")); err == nil || !strings.Contains(err.Error(), "Login failed") {
		t.Fatalf("unknown token: %v", err)
	}
	if _, err := azuresqlutil.ConnectToDB(ctx, "localhost", "appdb", azuresqlutil.ServerPort, "sqladmin", "wrong"); err == nil || !strings.Contains(err.Error(), "Login failed") {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := azuresqlutil.ConnectToDBUsingAAD(ctx, "localhost", "nodb", azuresqlutil.ServerPort, tokenOf(adminToken)); err == nil || !strings.Contains(err.Error(), "Cannot open database") {
		t.Fatalf("missing database: %v", err)
	}
	// The certificate is checked against the host name.
	if _, err := azuresqlutil.ConnectToDBUsingAAD(ctx, "127.0.0.1", "appdb", azuresqlutil.ServerPort, tokenOf(adminToken)); err == nil {
		t.Fatal("a certificate for another name was accepted")
	}
}

func TestLocalUserAndPasswordAdmin(t *testing.T) {
	ctx := context.Background()
	srv := start(t)
	db, err := azuresqlutil.ConnectToDB(ctx, "localhost", "appdb", azuresqlutil.ServerPort, "sqladmin", "admin-password-1")
	if err != nil {
		t.Fatalf("SQL administrator signs in: %v", err)
	}
	defer db.Close()
	if err := azuresqlutil.CreateOrUpdateUser(ctx, db, "local-app", "pw-1"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := azuresqlutil.CreateOrUpdateUser(ctx, db, "local-app", "pw-2"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if u, err := azuresqlutil.ConnectToDB(ctx, "localhost", "appdb", azuresqlutil.ServerPort, "local-app", "pw-2"); err != nil {
		t.Fatalf("the user signs in with its new password: %v", err)
	} else {
		_ = u.Close()
	}
	if err := azuresqlutil.CreateOrUpdateAADUser(ctx, db, "app-identity"); err == nil || !strings.Contains(err.Error(), "Only connections established with Active Directory accounts") {
		t.Fatalf("a password administrator creates an Entra user: %v", err)
	}
	if got := srv.Users("appdb"); len(got) != 1 || got[0].Name != "local-app" || got[0].External {
		t.Fatalf("users = %+v", got)
	}
}
