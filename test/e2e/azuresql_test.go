// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	asosql "github.com/Azure/azure-service-operator/v2/api/sql/v20250101/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	sqlv1 "github.com/highfivelabs/aso-extensions/apis/sql/v1"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/azuresql"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/entra"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/tlsutil"
	sqlreconciler "github.com/highfivelabs/aso-extensions/pkg/reconcilers/azuresql"
	azuresqlutil "github.com/highfivelabs/aso-extensions/pkg/util/azuresql"
)

const (
	sqlAdmin         = "sqladmin"
	sqlAdminPassword = "sql-admin-password-1"
	sqlEntraAdmin    = "aso-sql-admins"
	sqlTeam          = "sql-team"
	sqlOtherTeam     = "sql-team-b"
)

var sqlFake *azuresql.Server

// startSQL serves the fake Azure SQL server on localhost. An access token
// signs in as the server's Entra administrator when it is client A's for
// sql-team's aso-workload service account and Azure SQL's audience, and as
// "team-b-identity" (in the directory, no administrator) for client B.
func startSQL(ca *tlsutil.CA) func() {
	cert, err := ca.ServerTLS("localhost")
	must(err)
	sqlFake = &azuresql.Server{
		Name:          "fakesql",
		TLS:           &tls.Config{Certificates: []tls.Certificate{cert}},
		Admin:         sqlAdmin,
		AdminPassword: sqlAdminPassword,
		EntraAdmin:    sqlEntraAdmin,
		Principal: func(token string) string {
			c, err := entra.Parse(token)
			if err != nil || c.Scope != sqlreconciler.Scope || c.Tenant != tenantID {
				return ""
			}
			switch {
			case c.ClientID == clientA && c.Subject == entra.ServiceAccountSubject(sqlTeam, outoftree.DefaultWorkloadIdentityServiceAccount):
				return sqlEntraAdmin
			case c.ClientID == clientB:
				return "team-b-identity"
			}
			return ""
		},
		Directory: []string{"app-identity", "data-readers", "team-b-identity", sqlEntraAdmin},
		Databases: []string{"appdb"},
	}
	if os.Getenv("E2E_VERBOSE") != "" {
		sqlFake.Log = log.New(os.Stderr, "fake sql: ", 0)
	}
	addr, err := sqlFake.Listen("127.0.0.1:0")
	must(err)
	_, port, _ := net.SplitHostPort(addr.String())
	azuresqlutil.ServerPort, _ = strconv.Atoi(port)
	azuresqlutil.TLSRootCAs = ca.Pool()
	return func() { _ = sqlFake.Close() }
}

// sqlServerAndDatabase stand in for an ASO sql.azure.com Server (its status
// names the fake's host) and a ServersDatabase "appdb" on it, in a
// resource group, as ASO would have provisioned them.
func sqlServerAndDatabase(t *testing.T, ns string) {
	t.Helper()
	ctx := context.Background()
	resourceGroup(t, ns, "rg", "rg-"+ns)
	srv := &asosql.Server{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "sql"}}
	srv.Spec.AzureName = "sql-" + ns
	srv.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	if err := k8s.Create(ctx, srv); err != nil {
		t.Fatal(err)
	}
	host := "localhost"
	srv.Status.FullyQualifiedDomainName = &host
	srv.Status.Conditions = []conditions.Condition{asoReady(srv.Generation)}
	if err := k8s.Status().Update(ctx, srv); err != nil {
		t.Fatal(err)
	}
	db := &asosql.ServersDatabase{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "appdb"}}
	db.Spec.AzureName = "appdb"
	db.Spec.Owner = &genruntime.KnownResourceReference{Name: "sql"}
	if err := k8s.Create(ctx, db); err != nil {
		t.Fatal(err)
	}
	db.Status.Conditions = []conditions.Condition{asoReady(db.Generation)}
	if err := k8s.Status().Update(ctx, db); err != nil {
		t.Fatal(err)
	}
}

func sqlUserIn(name string) (azuresql.User, bool) {
	for _, u := range sqlFake.Users("appdb") {
		if strings.EqualFold(u.Name, name) {
			return u, true
		}
	}
	return azuresql.User{}, false
}

func expectSQLUser(t *testing.T, name string, want *azuresql.User) {
	t.Helper()
	eventually(t, timeout, "database user "+name, func() error {
		got, ok := sqlUserIn(name)
		switch {
		case want == nil && ok:
			return fmt.Errorf("still there: %+v", got)
		case want == nil:
			return nil
		case !ok:
			return fmt.Errorf("not there")
		case !reflect.DeepEqual(got, *want):
			return fmt.Errorf("got %+v, want %+v", got, *want)
		}
		return nil
	})
}

// A Microsoft Entra user (ASO pull request #5104): the operator signs in
// with the token of the identity ASO would use in this namespace (strict
// workload identity, client A), which is the server's Entra administrator,
// and runs CREATE USER ... FROM EXTERNAL PROVIDER; roles follow the spec;
// an alias names the user when given; deleting drops it.
func TestAzureSQLEntraUser(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, sqlTeam)
	strictCredential(t, ns, clientA)
	sqlServerAndDatabase(t, ns)

	byName := &sqlv1.User{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "app-identity"},
		Spec: sqlv1.UserSpec{
			Owner:   &genruntime.KnownResourceReference{Name: "appdb"},
			Roles:   []string{"db_datareader", "db_datawriter"},
			AADUser: &sqlv1.AADUserSpec{},
		},
	}
	byAlias := &sqlv1.User{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "readers"},
		Spec: sqlv1.UserSpec{
			AzureName: "a-group-name-too-long-for-the-database",
			Owner:     &genruntime.KnownResourceReference{Name: "appdb"},
			Roles:     []string{"db_datareader"},
			AADUser:   &sqlv1.AADUserSpec{Alias: "data-readers", ServerAdminUsername: sqlEntraAdmin},
		},
	}
	unknown := &sqlv1.User{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "nobody-in-entra"},
		Spec: sqlv1.UserSpec{
			Owner:   &genruntime.KnownResourceReference{Name: "appdb"},
			AADUser: &sqlv1.AADUserSpec{},
		},
	}
	for _, u := range []*sqlv1.User{byName, byAlias, unknown} {
		if err := k8s.Create(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "Entra User Ready", ready(byName))
	eventually(t, timeout, "Entra User by alias Ready", ready(byAlias))
	expectSQLUser(t, "app-identity", &azuresql.User{Name: "app-identity", External: true, Roles: []string{"db_datareader", "db_datawriter"}})
	expectSQLUser(t, "data-readers", &azuresql.User{Name: "data-readers", External: true, Roles: []string{"db_datareader"}})
	eventually(t, timeout, "a name Entra does not know is refused", notReadyWith(unknown, "could not be found"))

	// Roles follow the spec.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(byName), byName); err != nil {
		t.Fatal(err)
	}
	byName.Spec.Roles = []string{"db_owner"}
	if err := k8s.Update(ctx, byName); err != nil {
		t.Fatal(err)
	}
	expectSQLUser(t, "app-identity", &azuresql.User{Name: "app-identity", External: true, Roles: []string{"db_owner"}})

	// The alias cannot change, nor the kind of user.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(byAlias), byAlias); err != nil {
		t.Fatal(err)
	}
	changed := byAlias.DeepCopy()
	changed.Spec.AADUser.Alias = "other"
	if err := k8s.Update(ctx, changed); err == nil || !strings.Contains(err.Error(), "aadUser.alias is immutable") {
		t.Fatalf("changing the alias: %v", err)
	}
	changed = byAlias.DeepCopy()
	changed.Spec.AADUser = nil
	changed.Spec.LocalUser = &sqlv1.LocalUserSpec{ServerAdminUsername: sqlAdmin}
	if err := k8s.Update(ctx, changed); err == nil || !strings.Contains(err.Error(), "cannot change from aadUser to localUser") {
		t.Fatalf("changing to a local user: %v", err)
	}

	// The token the operator signed in with came from the namespace's
	// service account through ASO's strict mode.
	found := false
	for _, r := range entraFake.Requests {
		if strings.HasSuffix(r, "/oauth2/v2.0/token") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no token request reached the fake Entra: %v", entraFake.Requests)
	}

	for _, u := range []*sqlv1.User{byName, byAlias, unknown} {
		if err := k8s.Delete(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	expectSQLUser(t, "app-identity", nil)
	expectSQLUser(t, "data-readers", nil)
	for _, u := range []*sqlv1.User{byName, byAlias, unknown} {
		eventually(t, timeout, "User gone", func() error {
			err := k8s.Get(ctx, client.ObjectKeyFromObject(u), u)
			if errors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("still there: %v", err)
		})
	}
}

// Another namespace's identity is no administrator of the server: the
// sign-in is refused and no user is made.
func TestAzureSQLEntraUserNotAdministrator(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, sqlOtherTeam)
	strictCredential(t, ns, clientB)
	sqlServerAndDatabase(t, ns)
	u := &sqlv1.User{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "intruder"},
		Spec: sqlv1.UserSpec{
			Owner:   &genruntime.KnownResourceReference{Name: "appdb"},
			AADUser: &sqlv1.AADUserSpec{},
		},
	}
	if err := k8s.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "User refused", notReadyWith(u, "Login failed for user 'team-b-identity'"))
	if _, ok := sqlUserIn("intruder"); ok {
		t.Fatal("a user was made")
	}
}

// A password user, ASO's own Azure SQL User unchanged but for its group:
// the operator signs in as the SQL administrator; the password and roles
// follow the spec; the user can sign in with its password.
func TestAzureSQLLocalUser(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "sql-local")
	sqlServerAndDatabase(t, ns)
	secret(t, ns, "admin", map[string]string{"password": sqlAdminPassword})
	secret(t, ns, "app-user", map[string]string{"password": "app-user-password-1"})
	u := &sqlv1.User{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "app-user"},
		Spec: sqlv1.UserSpec{
			Owner: &genruntime.KnownResourceReference{Name: "appdb"},
			Roles: []string{"db_datareader"},
			LocalUser: &sqlv1.LocalUserSpec{
				ServerAdminUsername: sqlAdmin,
				ServerAdminPassword: &genruntime.SecretReference{Name: "admin", Key: "password"},
				Password:            &genruntime.SecretReference{Name: "app-user", Key: "password"},
			},
		},
	}
	if err := k8s.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "User Ready", ready(u))
	expectSQLUser(t, "app-user", &azuresql.User{Name: "app-user", Roles: []string{"db_datareader"}})
	eventually(t, timeout, "the user signs in", func() error {
		db, err := azuresqlutil.ConnectToDB(ctx, "localhost", "appdb", azuresqlutil.ServerPort, "app-user", "app-user-password-1")
		if err != nil {
			return err
		}
		return db.Close()
	})
	if err := k8s.Delete(ctx, u); err != nil {
		t.Fatal(err)
	}
	expectSQLUser(t, "app-user", nil)
}

// The CRD refuses a user with both or neither of localUser and aadUser
// (#5104 checks it in ASO's webhook).
func TestAzureSQLUserValidation(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "sql-validation")
	neither := &sqlv1.User{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "neither"},
		Spec:       sqlv1.UserSpec{Owner: &genruntime.KnownResourceReference{Name: "appdb"}},
	}
	if err := k8s.Create(ctx, neither); err == nil || !strings.Contains(err.Error(), "exactly one of localUser and aadUser") {
		t.Fatalf("neither: want a refusal, got %v", err)
	}
	both := &sqlv1.User{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "both"},
		Spec: sqlv1.UserSpec{Owner: &genruntime.KnownResourceReference{Name: "appdb"},
			AADUser: &sqlv1.AADUserSpec{}, LocalUser: &sqlv1.LocalUserSpec{ServerAdminUsername: sqlAdmin}},
	}
	if err := k8s.Create(ctx, both); err == nil || !strings.Contains(err.Error(), "exactly one of localUser and aadUser") {
		t.Fatalf("both: want a refusal, got %v", err)
	}
}
