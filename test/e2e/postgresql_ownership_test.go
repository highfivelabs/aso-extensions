// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	pgv1 "github.com/highfivelabs/aso-extensions/apis/dbforpostgresql/v1"
)

func databaseOwner(database string) (string, error) {
	var owner string
	err := pgSuper.QueryRow(`SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = $1`, database).Scan(&owner)
	return owner, err
}

func expectDatabaseOwner(t *testing.T, database, want string) {
	t.Helper()
	eventually(t, timeout, "owner of database "+database, func() error {
		got, err := databaseOwner(database)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("owned by %s, want %s", got, want)
		}
		return nil
	})
}

// expectReassignedEvent waits for a DatabaseReassigned event on the User
// naming database and role.
func expectReassignedEvent(t *testing.T, ns, user, database, role string) {
	t.Helper()
	eventually(t, timeout, "DatabaseReassigned event for "+database, func() error {
		var events corev1.EventList
		if err := k8s.List(context.Background(), &events, client.InNamespace(ns)); err != nil {
			return err
		}
		var seen []string
		for _, e := range events.Items {
			if e.InvolvedObject.Name != user || e.Reason != "DatabaseReassigned" {
				continue
			}
			if strings.Contains(e.Message, "database "+database+",") || strings.Contains(e.Message, "Database "+database+" ") {
				if strings.Contains(e.Message, role) {
					return nil
				}
			}
			seen = append(seen, e.Message)
		}
		return fmt.Errorf("no such event; DatabaseReassigned events: %q", seen)
	})
}

// Database ownership: a User owns the databases it lists. Another User is
// granted privileges on them with the owner's privileges, borrowed for the
// transaction. A database that leaves the list goes to the reassignOwnedTo
// role (here a named one), and when the owner is deleted, its databases and
// what it owns in them go to hfl_owner, a role nobody signs in as, which
// the server's administrators hold. Nothing is left without an owner.
func TestPostgreSQLOwnedDatabases(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "pg-owner")
	flexibleServer(t, ns, "pg")
	secret(t, ns, "admin", map[string]string{"password": pgSetup.LocalAdminPassword})
	secret(t, ns, "orders-api", map[string]string{"password": "orders-api-password-1"})
	secret(t, ns, "orders-reader", map[string]string{"password": "orders-reader-password-1"})
	sqlExec(t, pgSuper,
		"CREATE DATABASE orders OWNER azure_pg_admin",
		"CREATE DATABASE orders_archive OWNER azure_pg_admin")

	localUser := func(name string) *pgv1.LocalUserSpec {
		return &pgv1.LocalUserSpec{
			ServerAdminUsername: pgSetup.LocalAdmin,
			ServerAdminPassword: &genruntime.SecretReference{Name: "admin", Key: "password"},
			Password:            &genruntime.SecretReference{Name: name, Key: "password"},
		}
	}
	owner := &pgv1.User{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "orders-api"},
		Spec: pgv1.UserSpec{
			Owner:          &genruntime.KubernetesOwnerReference{Name: "pg"},
			LocalUser:      localUser("orders-api"),
			OwnedDatabases: []string{"orders", "orders_archive"},
			// orders is owned, so only app's privileges are managed here.
			DatabasePrivileges: map[string][]pgv1.DatabasePrivilege{
				"orders": privs(pgv1.DatabasePrivilegeConnect),
				"app":    privs(pgv1.DatabasePrivilegeConnect),
			},
		},
	}
	if err := k8s.Create(ctx, owner); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "orders-api Ready", ready(owner))
	expectDatabaseOwner(t, "orders", "orders-api")
	expectDatabaseOwner(t, "orders_archive", "orders-api")
	expectPrivileges(t, "orders-api", map[string][]string{"app": {"CONNECT"}})
	if !reflect.DeepEqual(owner.Status.OwnedDatabases, []string{"orders", "orders_archive"}) {
		t.Fatalf("status.ownedDatabases: %v", owner.Status.OwnedDatabases)
	}
	expectAdminHoldsOnlyAdminOption(t, "orders-api")

	// The owner runs its migrations: it may create in public, as the
	// database's owner.
	asOwner := connectAs(t, "orders", "orders-api", "orders-api-password-1")
	sqlExec(t, asOwner, "CREATE TABLE public.items (id int)")

	// Another user gets privileges on the owner's database and its public
	// schema, granted with the owner's privileges.
	reader := &pgv1.User{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "orders-reader"},
		Spec: pgv1.UserSpec{
			Owner:              &genruntime.KubernetesOwnerReference{Name: "pg"},
			LocalUser:          localUser("orders-reader"),
			DatabasePrivileges: map[string][]pgv1.DatabasePrivilege{"orders": privs(pgv1.DatabasePrivilegeConnect)},
			SchemaPrivileges:   map[string]map[string][]pgv1.SchemaPrivilege{"orders": {"public": schemaPrivs(pgv1.SchemaPrivilegeUsage)}},
		},
	}
	if err := k8s.Create(ctx, reader); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "orders-reader Ready", ready(reader))
	expectPrivileges(t, "orders-reader", map[string][]string{"orders": {"CONNECT"}})
	orders := connectAs(t, "orders", "postgres", "super-password")
	expectIn(t, "orders-reader on schema public of orders", func() (map[string][]string, error) { return schemaPrivileges(orders, "orders-reader") },
		map[string][]string{"public": {"USAGE"}})
	expectAdminHoldsOnlyAdminOption(t, "orders-api")

	// A database that leaves ownedDatabases goes to reassignOwnedTo.
	update(t, owner, func(u *pgv1.User) {
		u.Spec.OwnedDatabases = []string{"orders"}
		u.Spec.ReassignOwnedTo = "archive_keeper"
	})
	eventually(t, timeout, "orders-api Ready after giving up orders_archive", ready(owner))
	expectDatabaseOwner(t, "orders_archive", "archive_keeper")
	expectDatabaseOwner(t, "orders", "orders-api")
	if !reflect.DeepEqual(owner.Status.ReassignedDatabases, []string{"orders_archive"}) ||
		!reflect.DeepEqual(owner.Status.OwnedDatabases, []string{"orders"}) {
		t.Fatalf("status: reassigned %v, owned %v", owner.Status.ReassignedDatabases, owner.Status.OwnedDatabases)
	}
	expectReassignedEvent(t, ns, "orders-api", "orders_archive", "archive_keeper")
	expectNoLoginHeldByAdmins(t, "archive_keeper")

	// Deleting the owner gives its database, and the table it created
	// there, to hfl_owner, then drops it. The reader keeps its privileges.
	update(t, owner, func(u *pgv1.User) { u.Spec.ReassignOwnedTo = "" })
	eventually(t, timeout, "orders-api Ready before delete", ready(owner))
	deleteUser(t, owner)
	expectDatabaseOwner(t, "orders", "hfl_owner")
	var tableOwner string
	if err := orders.QueryRow(`SELECT tableowner FROM pg_tables WHERE schemaname = 'public' AND tablename = 'items'`).Scan(&tableOwner); err != nil || tableOwner != "hfl_owner" {
		t.Fatalf("owner of table items: %q (%v), want hfl_owner", tableOwner, err)
	}
	expectReassignedEvent(t, ns, "orders-api", "orders", "hfl_owner")
	expectNoLoginHeldByAdmins(t, "hfl_owner")
	expectPrivileges(t, "orders-reader", map[string][]string{"orders": {"CONNECT"}})

	// The reader, whose grants are now hfl_owner's, is revoked and dropped
	// with the administrators' hold on hfl_owner.
	deleteUser(t, reader)
}

// expectNoLoginHeldByAdmins checks role cannot sign in and that
// azure_pg_admin, so every administrator, holds its privileges.
func expectNoLoginHeldByAdmins(t *testing.T, role string) {
	t.Helper()
	var canLogin, held bool
	if err := pgSuper.QueryRow(`SELECT rolcanlogin, pg_has_role('azure_pg_admin', rolname, 'USAGE') FROM pg_roles WHERE rolname = $1`, role).Scan(&canLogin, &held); err != nil {
		t.Fatalf("role %s: %v", role, err)
	}
	if canLogin || !held {
		t.Fatalf("role %s: LOGIN=%v, held by azure_pg_admin=%v; want false, true", role, canLogin, held)
	}
}

// A reassignOwnedTo role that can sign in is refused, and so is the user
// itself.
func TestPostgreSQLReassignOwnedToRefused(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "pg-owner-refused")
	flexibleServer(t, ns, "pg")
	secret(t, ns, "admin", map[string]string{"password": pgSetup.LocalAdminPassword})
	secret(t, ns, "keeper", map[string]string{"password": "keeper-password-1"})
	sqlExec(t, pgSuper,
		"CREATE DATABASE keeper_db OWNER azure_pg_admin",
		`CREATE ROLE "login-keeper" LOGIN`)

	u := &pgv1.User{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "keeper"},
		Spec: pgv1.UserSpec{
			Owner: &genruntime.KubernetesOwnerReference{Name: "pg"},
			LocalUser: &pgv1.LocalUserSpec{
				ServerAdminUsername: pgSetup.LocalAdmin,
				ServerAdminPassword: &genruntime.SecretReference{Name: "admin", Key: "password"},
				Password:            &genruntime.SecretReference{Name: "keeper", Key: "password"},
			},
			OwnedDatabases: []string{"keeper_db"},
		},
	}
	if err := k8s.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "keeper Ready", ready(u))
	update(t, u, func(u *pgv1.User) {
		u.Spec.OwnedDatabases = nil
		u.Spec.ReassignOwnedTo = "login-keeper"
	})
	eventually(t, timeout, "giving keeper_db to a LOGIN role refused", notReadyWith(u, "role login-keeper can sign in"))
	expectDatabaseOwner(t, "keeper_db", "keeper")

	self := &pgv1.User{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "self"},
		Spec: pgv1.UserSpec{
			AzureName:       "self",
			Owner:           &genruntime.KubernetesOwnerReference{Name: "pg"},
			AADUser:         &pgv1.AADUserSpec{ServerAdminUsername: "a"},
			OwnedDatabases:  []string{"x"},
			ReassignOwnedTo: "self",
		},
	}
	if err := k8s.Create(ctx, self); err == nil || !strings.Contains(err.Error(), "reassignOwnedTo cannot be the user itself") {
		t.Fatalf("want a refusal, got %v", err)
	}
}
