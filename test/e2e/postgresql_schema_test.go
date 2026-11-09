// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	pgv1 "github.com/highfivelabs/aso-extensions/apis/dbforpostgresql/v1"
	postgresqlutil "github.com/highfivelabs/aso-extensions/pkg/util/postgresql"
)

// connectAs opens a connection to database as user, over TLS like the
// operator's.
func connectAs(t *testing.T, database, user, password string) *sql.DB {
	t.Helper()
	db, err := postgresqlutil.ConnectToDB(context.Background(), "localhost", database, pgPort, user, password)
	if err != nil {
		t.Fatalf("connecting to %s as %s: %v", database, user, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func sqlExec(t *testing.T, db *sql.DB, statements ...string) {
	t.Helper()
	for _, s := range statements {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// schemaPrivileges reads, as the superuser, the user's privileges on each
// schema of the database db is connected to.
func schemaPrivileges(db *sql.DB, user string) (map[string][]string, error) {
	rows, err := db.Query(`SELECT n.nspname, a.privilege_type FROM pg_namespace n
CROSS JOIN LATERAL aclexplode(n.nspacl) a JOIN pg_roles r ON r.oid = a.grantee WHERE r.rolname = $1`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var s, p string
		if err := rows.Scan(&s, &p); err != nil {
			return nil, err
		}
		out[s] = append(out[s], p)
	}
	for s := range out {
		sort.Strings(out[s])
	}
	return out, rows.Err()
}

// defaultPrivileges reads, as the superuser, the user's default privileges
// in the database db is connected to, as "schema/forRole/objtype" to the
// sorted privileges.
func defaultPrivileges(db *sql.DB, user string) (map[string][]string, error) {
	rows, err := db.Query(`SELECT coalesce(n.nspname, '-') || '/' || pg_get_userbyid(d.defaclrole) || '/' || d.defaclobjtype::text, a.privilege_type
FROM pg_default_acl d LEFT JOIN pg_namespace n ON n.oid = d.defaclnamespace
CROSS JOIN LATERAL aclexplode(d.defaclacl) a JOIN pg_roles r ON r.oid = a.grantee WHERE r.rolname = $1`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var k, p string
		if err := rows.Scan(&k, &p); err != nil {
			return nil, err
		}
		out[k] = append(out[k], p)
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out, rows.Err()
}

func expectIn(t *testing.T, what string, read func() (map[string][]string, error), want map[string][]string) {
	t.Helper()
	eventually(t, timeout, what, func() error {
		got, err := read()
		if err != nil {
			return err
		}
		if len(got) == 0 && len(want) == 0 {
			return nil
		}
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("got %v, want %v", got, want)
		}
		return nil
	})
}

func schemaPrivs(p ...pgv1.SchemaPrivilege) []pgv1.SchemaPrivilege { return p }

func update(t *testing.T, user *pgv1.User, change func(*pgv1.User)) {
	t.Helper()
	ctx := context.Background()
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(user), user); err != nil {
		t.Fatal(err)
	}
	change(user)
	if err := k8s.Update(ctx, user); err != nil {
		t.Fatal(err)
	}
}

func deleteUser(t *testing.T, user *pgv1.User) {
	t.Helper()
	ctx := context.Background()
	if err := k8s.Delete(ctx, user); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "User "+user.Name+" gone", func() error {
		got := &pgv1.User{}
		err := k8s.Get(ctx, client.ObjectKeyFromObject(user), got)
		if errors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, c := range got.Status.Conditions {
			if c.Type == "Ready" {
				return fmt.Errorf("still there: Ready=%s %s", c.Status, c.Message)
			}
		}
		return fmt.Errorf("still there")
	})
	if ok, _, err := roleExists(user.AzureName()); err != nil || ok {
		t.Fatalf("role %s still exists (%v)", user.AzureName(), err)
	}
}

// A password user with schemaPrivileges: granted in the database named,
// changed to exactly the new list (taking back a grant made by hand),
// revoked when the database is no longer named, refused for a schema that
// does not exist, and revoked before the role is dropped.
func TestPostgreSQLSchemaPrivileges(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "pg-schema")
	flexibleServer(t, ns, "pg")
	secret(t, ns, "admin", map[string]string{"password": pgSetup.LocalAdminPassword})
	secret(t, ns, "schema-user", map[string]string{"password": "schema-user-password-1"})

	app := connectAs(t, "app", "postgres", "super-password")
	sqlExec(t, app,
		"CREATE SCHEMA reports AUTHORIZATION azure_pg_admin",
		"CREATE SCHEMA scratch AUTHORIZATION azure_pg_admin")

	user := &pgv1.User{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "schema-user"},
		Spec: pgv1.UserSpec{
			Owner: &genruntime.KubernetesOwnerReference{Name: "pg"},
			LocalUser: &pgv1.LocalUserSpec{
				ServerAdminUsername: pgSetup.LocalAdmin,
				ServerAdminPassword: &genruntime.SecretReference{Name: "admin", Key: "password"},
				Password:            &genruntime.SecretReference{Name: "schema-user", Key: "password"},
			},
			DatabasePrivileges: map[string][]pgv1.DatabasePrivilege{"app": privs(pgv1.DatabasePrivilegeConnect)},
			SchemaPrivileges: map[string]map[string][]pgv1.SchemaPrivilege{
				"app": {
					"public":  schemaPrivs(pgv1.SchemaPrivilegeUsage, pgv1.SchemaPrivilegeCreate),
					"reports": schemaPrivs(pgv1.SchemaPrivilegeUsage),
				},
			},
		},
	}
	if err := k8s.Create(ctx, user); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "User Ready", ready(user))
	read := func() (map[string][]string, error) { return schemaPrivileges(app, "schema-user") }
	expectIn(t, "schema privileges", read, map[string][]string{"public": {"CREATE", "USAGE"}, "reports": {"USAGE"}})
	eventually(t, timeout, "status.appliedSchemaPrivileges", func() error {
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(user), user); err != nil {
			return err
		}
		want := map[string]map[string][]string{"app": {"public": {"CREATE", "USAGE"}, "reports": {"USAGE"}}}
		if !reflect.DeepEqual(user.Status.AppliedSchemaPrivileges, want) {
			return fmt.Errorf("got %v", user.Status.AppliedSchemaPrivileges)
		}
		return nil
	})

	// The user can now create a table in public (PostgreSQL 15 and later
	// let only the database owner do that by default).
	asUser := connectAs(t, "app", "schema-user", "schema-user-password-1")
	sqlExec(t, asUser, "CREATE TABLE public.schema_user_notes (id int)", "DROP TABLE public.schema_user_notes")

	// Exactly the new list: ALL on public; reports and a grant made by hand
	// on scratch are revoked.
	sqlExec(t, app, `GRANT USAGE ON SCHEMA scratch TO "schema-user"`)
	update(t, user, func(u *pgv1.User) {
		u.Spec.SchemaPrivileges = map[string]map[string][]pgv1.SchemaPrivilege{"app": {"public": schemaPrivs(pgv1.SchemaPrivilegeAll)}}
	})
	eventually(t, timeout, "User Ready after change", ready(user))
	expectIn(t, "schema privileges after change", read, map[string][]string{"public": {"CREATE", "USAGE"}})

	// A schema that does not exist is refused, and nothing else changes.
	update(t, user, func(u *pgv1.User) {
		u.Spec.SchemaPrivileges["app"]["nosuch"] = schemaPrivs(pgv1.SchemaPrivilegeUsage)
	})
	eventually(t, timeout, "User refused", notReadyWith(user, "schema nosuch does not exist in database app"))

	// No longer naming the database revokes what the user holds there
	// (the database is remembered in status).
	update(t, user, func(u *pgv1.User) { u.Spec.SchemaPrivileges = nil })
	eventually(t, timeout, "User Ready without schemaPrivileges", ready(user))
	expectIn(t, "schema privileges revoked", read, nil)
	if user.Status.AppliedSchemaPrivileges != nil {
		t.Fatalf("status.appliedSchemaPrivileges: %v", user.Status.AppliedSchemaPrivileges)
	}

	// Deleting revokes the schema privileges, then drops the role
	// (PostgreSQL refuses to drop a role that holds any).
	update(t, user, func(u *pgv1.User) {
		u.Spec.SchemaPrivileges = map[string]map[string][]pgv1.SchemaPrivilege{"app": {"reports": schemaPrivs(pgv1.SchemaPrivilegeUsage)}}
	})
	eventually(t, timeout, "User Ready before delete", ready(user))
	expectIn(t, "schema privileges before delete", read, map[string][]string{"reports": {"USAGE"}})
	deleteUser(t, user)
}

// Default privileges: a reader gets SELECT on the tables a migrator user
// creates later. The administrator, which holds only ADMIN OPTION on the
// migrator (PostgreSQL 16 and later), takes the migrator's privileges for
// the one transaction and keeps none. A schema the migrator owns is granted
// the same way.
func TestPostgreSQLDefaultPrivileges(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "pg-default")
	flexibleServer(t, ns, "pg")
	secret(t, ns, "admin", map[string]string{"password": pgSetup.LocalAdminPassword})
	secret(t, ns, "migrator", map[string]string{"password": "migrator-password-1"})
	secret(t, ns, "reader", map[string]string{"password": "reader-password-1"})

	localUser := func(name string) *pgv1.LocalUserSpec {
		return &pgv1.LocalUserSpec{
			ServerAdminUsername: pgSetup.LocalAdmin,
			ServerAdminPassword: &genruntime.SecretReference{Name: "admin", Key: "password"},
			Password:            &genruntime.SecretReference{Name: name, Key: "password"},
		}
	}
	migrator := &pgv1.User{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "migrator"},
		Spec: pgv1.UserSpec{
			Owner:              &genruntime.KubernetesOwnerReference{Name: "pg"},
			LocalUser:          localUser("migrator"),
			DatabasePrivileges: map[string][]pgv1.DatabasePrivilege{"app": privs(pgv1.DatabasePrivilegeConnect, pgv1.DatabasePrivilegeCreate)},
			SchemaPrivileges:   map[string]map[string][]pgv1.SchemaPrivilege{"app": {"public": schemaPrivs(pgv1.SchemaPrivilegeAll)}},
		},
	}
	if err := k8s.Create(ctx, migrator); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "migrator Ready", ready(migrator))

	reader := &pgv1.User{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "reader"},
		Spec: pgv1.UserSpec{
			Owner:              &genruntime.KubernetesOwnerReference{Name: "pg"},
			LocalUser:          localUser("reader"),
			DatabasePrivileges: map[string][]pgv1.DatabasePrivilege{"app": privs(pgv1.DatabasePrivilegeConnect)},
			SchemaPrivileges:   map[string]map[string][]pgv1.SchemaPrivilege{"app": {"public": schemaPrivs(pgv1.SchemaPrivilegeUsage)}},
			DefaultPrivileges: []pgv1.DefaultPrivilegesSpec{
				{Database: "app", Schema: "public", ForRole: "migrator", ObjectType: pgv1.DefaultPrivilegesObjectTypeTables, Privileges: []pgv1.DefaultPrivilege{"SELECT"}},
				{Database: "app", Schema: "public", ForRole: "migrator", ObjectType: pgv1.DefaultPrivilegesObjectTypeSequences, Privileges: []pgv1.DefaultPrivilege{"USAGE", "SELECT"}},
			},
		},
	}
	if err := k8s.Create(ctx, reader); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "reader Ready", ready(reader))

	app := connectAs(t, "app", "postgres", "super-password")
	readDefaults := func() (map[string][]string, error) { return defaultPrivileges(app, "reader") }
	expectIn(t, "default privileges", readDefaults, map[string][]string{
		"public/migrator/r": {"SELECT"},
		"public/migrator/S": {"SELECT", "USAGE"},
	})
	eventually(t, timeout, "status.appliedDefaultPrivileges", func() error {
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(reader), reader); err != nil {
			return err
		}
		want := []pgv1.AppliedDefaultPrivileges{
			{Database: "app", Schema: "public", ForRole: "migrator", ObjectType: "sequences", Privileges: []string{"SELECT", "USAGE"}},
			{Database: "app", Schema: "public", ForRole: "migrator", ObjectType: "tables", Privileges: []string{"SELECT"}},
		}
		if !reflect.DeepEqual(reader.Status.AppliedDefaultPrivileges, want) {
			return fmt.Errorf("got %+v", reader.Status.AppliedDefaultPrivileges)
		}
		return nil
	})
	expectAdminHoldsOnlyAdminOption(t, "migrator")

	// What the migrator creates, the reader may read, and only read.
	asMigrator := connectAs(t, "app", "migrator", "migrator-password-1")
	sqlExec(t, asMigrator,
		"CREATE TABLE public.orders (id serial PRIMARY KEY)",
		"CREATE SCHEMA migrator_owned")
	var selectOK, insertOK, seqOK bool
	if err := app.QueryRow(`SELECT has_table_privilege('reader', 'public.orders', 'SELECT'),
has_table_privilege('reader', 'public.orders', 'INSERT'),
has_sequence_privilege('reader', 'public.orders_id_seq', 'USAGE')`).Scan(&selectOK, &insertOK, &seqOK); err != nil {
		t.Fatal(err)
	}
	if !selectOK || insertOK || !seqOK {
		t.Fatalf("reader on the migrator's new objects: SELECT=%v INSERT=%v sequence USAGE=%v", selectOK, insertOK, seqOK)
	}

	// Exactly the new list: tables gain INSERT, UPDATE, DELETE; sequences
	// go; a default privilege made by hand is revoked. A schema the
	// migrator owns is granted with the migrator's privileges.
	sqlExec(t, app, `ALTER DEFAULT PRIVILEGES FOR ROLE migrator IN SCHEMA public GRANT EXECUTE ON FUNCTIONS TO reader`)
	update(t, reader, func(u *pgv1.User) {
		u.Spec.SchemaPrivileges["app"]["migrator_owned"] = schemaPrivs(pgv1.SchemaPrivilegeUsage)
		u.Spec.DefaultPrivileges = []pgv1.DefaultPrivilegesSpec{
			{Database: "app", Schema: "public", ForRole: "migrator", ObjectType: pgv1.DefaultPrivilegesObjectTypeTables, Privileges: []pgv1.DefaultPrivilege{"SELECT", "INSERT", "UPDATE", "DELETE"}},
		}
	})
	eventually(t, timeout, "reader Ready after change", ready(reader))
	expectIn(t, "default privileges after change", readDefaults, map[string][]string{
		"public/migrator/r": {"DELETE", "INSERT", "SELECT", "UPDATE"},
	})
	expectIn(t, "schema privileges with the migrator's schema", func() (map[string][]string, error) { return schemaPrivileges(app, "reader") },
		map[string][]string{"public": {"USAGE"}, "migrator_owned": {"USAGE"}})
	expectAdminHoldsOnlyAdminOption(t, "migrator")

	// ALL on tables is every table privilege of this server (PostgreSQL 17
	// adds MAINTAIN).
	update(t, reader, func(u *pgv1.User) {
		u.Spec.DefaultPrivileges[0].Privileges = []pgv1.DefaultPrivilege{"ALL"}
	})
	eventually(t, timeout, "reader Ready with ALL", ready(reader))
	expectIn(t, "default privileges ALL", readDefaults, map[string][]string{
		"public/migrator/r": {"DELETE", "INSERT", "MAINTAIN", "REFERENCES", "SELECT", "TRIGGER", "TRUNCATE", "UPDATE"},
	})

	// Deleting the reader revokes its schema and default privileges, and
	// the privileges those gave it on the migrator's table and sequence, and
	// drops it; then the migrator, once what it owns is gone, drops too
	// (the reader's default privileges for it would have blocked that).
	deleteUser(t, reader)
	sqlExec(t, app, "DROP TABLE public.orders", "DROP SCHEMA migrator_owned")
	deleteUser(t, migrator)
}

// expectAdminHoldsOnlyAdminOption checks that the local administrator's
// membership in role is still the one CREATE ROLE gave it: ADMIN OPTION,
// without the role's privileges.
func expectAdminHoldsOnlyAdminOption(t *testing.T, role string) {
	t.Helper()
	var usage bool
	var grants int
	if err := pgSuper.QueryRow(`SELECT pg_has_role($1::text::name, $2::text::name, 'USAGE'),
(SELECT count(*) FROM pg_auth_members WHERE roleid = $2::text::regrole AND member = $1::text::regrole)`, pgSetup.LocalAdmin, role).Scan(&usage, &grants); err != nil {
		t.Fatal(err)
	}
	if usage || grants != 1 {
		t.Fatalf("administrator %s in role %s: has its privileges=%v, grants=%d; want false, 1", pgSetup.LocalAdmin, role, usage, grants)
	}
}

// The CRD refuses default privileges that do not apply to the object type.
func TestPostgreSQLDefaultPrivilegesValidation(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "pg-default-validation")
	u := &pgv1.User{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "wrong"},
		Spec: pgv1.UserSpec{
			Owner:   &genruntime.KubernetesOwnerReference{Name: "pg"},
			AADUser: &pgv1.AADUserSpec{ServerAdminUsername: "a"},
			DefaultPrivileges: []pgv1.DefaultPrivilegesSpec{
				{Database: "app", Schema: "public", ForRole: "m", ObjectType: pgv1.DefaultPrivilegesObjectTypeFunctions, Privileges: []pgv1.DefaultPrivilege{"SELECT"}},
			},
		},
	}
	if err := k8s.Create(ctx, u); err == nil || !contains(err.Error(), "privileges on functions are EXECUTE or ALL") {
		t.Fatalf("want a refusal, got %v", err)
	}
	u.Spec.DefaultPrivileges = []pgv1.DefaultPrivilegesSpec{
		{Database: "app", Schema: "public", ForRole: "m", ObjectType: pgv1.DefaultPrivilegesObjectTypeTables, Privileges: []pgv1.DefaultPrivilege{"SELECT"}},
		{Database: "app", Schema: "public", ForRole: "m", ObjectType: pgv1.DefaultPrivilegesObjectTypeTables, Privileges: []pgv1.DefaultPrivilege{"INSERT"}},
	}
	if err := k8s.Create(ctx, u); err == nil || !contains(err.Error(), "Duplicate value") {
		t.Fatalf("want a refusal of the duplicate, got %v", err)
	}
}
