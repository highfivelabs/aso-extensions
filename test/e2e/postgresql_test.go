// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	pgv1 "github.com/highfivelabs/aso-extensions/apis/dbforpostgresql/v1"
)

const timeout = 60 * time.Second

// privileges reads, as the superuser, the user's privileges on each database.
func privileges(user string) (map[string][]string, error) {
	rows, err := pgSuper.Query(`SELECT d.datname, a.privilege_type FROM pg_database d
CROSS JOIN LATERAL aclexplode(d.datacl) a JOIN pg_roles r ON r.oid = a.grantee WHERE r.rolname = $1`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var d, p string
		if err := rows.Scan(&d, &p); err != nil {
			return nil, err
		}
		out[d] = append(out[d], p)
	}
	for d := range out {
		sort.Strings(out[d])
	}
	return out, rows.Err()
}

func expectPrivileges(t *testing.T, user string, want map[string][]string) {
	t.Helper()
	eventually(t, timeout, "database privileges of "+user, func() error {
		got, err := privileges(user)
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

func roleExists(user string) (bool, string, error) {
	var comment *string
	err := pgSuper.QueryRow(`SELECT shobj_description(oid, 'pg_authid') FROM pg_roles WHERE rolname = $1`, user).Scan(&comment)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return false, "", nil
		}
		return false, "", err
	}
	if comment == nil {
		return true, "", nil
	}
	return true, *comment, nil
}

func memberOf(user string) ([]string, error) {
	rows, err := pgSuper.Query(`SELECT c.rolname FROM pg_roles a JOIN pg_auth_members b ON a.oid = b.member JOIN pg_roles c ON b.roleid = c.oid WHERE a.rolname = $1 ORDER BY 1`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func privs(p ...pgv1.DatabasePrivilege) []pgv1.DatabasePrivilege { return p }

// A password user with databasePrivileges: granted, changed to exactly the
// new list, revoked from databases no longer listed, and revoked before the
// role is dropped.
func TestPostgreSQLLocalUserDatabasePrivileges(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "pg-local")
	flexibleServer(t, ns, "pg")
	secret(t, ns, "admin", map[string]string{"password": pgSetup.LocalAdminPassword})
	secret(t, ns, "app-user", map[string]string{"password": "app-user-password-1"})

	user := &pgv1.User{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "app-user"},
		Spec: pgv1.UserSpec{
			Owner: &genruntime.KubernetesOwnerReference{Name: "pg"},
			Roles: []string{"app_readers"},
			LocalUser: &pgv1.LocalUserSpec{
				ServerAdminUsername: pgSetup.LocalAdmin,
				ServerAdminPassword: &genruntime.SecretReference{Name: "admin", Key: "password"},
				Password:            &genruntime.SecretReference{Name: "app-user", Key: "password"},
			},
			DatabasePrivileges: map[string][]pgv1.DatabasePrivilege{
				"app":   privs(pgv1.DatabasePrivilegeConnect, pgv1.DatabasePrivilegeCreate),
				"other": privs(pgv1.DatabasePrivilegeConnect),
			},
		},
	}
	if err := k8s.Create(ctx, user); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "User Ready", ready(user))
	expectPrivileges(t, "app-user", map[string][]string{"app": {"CONNECT", "CREATE"}, "other": {"CONNECT"}})
	if got, err := memberOf("app-user"); err != nil || !reflect.DeepEqual(got, []string{"app_readers"}) {
		t.Fatalf("roles: %v %v", got, err)
	}
	eventually(t, timeout, "status.appliedDatabasePrivileges", func() error {
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(user), user); err != nil {
			return err
		}
		want := map[string][]pgv1.DatabasePrivilege{"app": {"CONNECT", "CREATE"}, "other": {"CONNECT"}}
		if !reflect.DeepEqual(user.Status.AppliedDatabasePrivileges, want) {
			return fmt.Errorf("got %v", user.Status.AppliedDatabasePrivileges)
		}
		return nil
	})

	// Exactly the new list: ALL on app, nothing on other.
	user.Spec.DatabasePrivileges = map[string][]pgv1.DatabasePrivilege{"app": privs(pgv1.DatabasePrivilegeAll)}
	if err := k8s.Update(ctx, user); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "User Ready after change", ready(user))
	expectPrivileges(t, "app-user", map[string][]string{"app": {"CONNECT", "CREATE", "TEMPORARY"}})

	// A grant made by hand is taken back on the next reconcile.
	if _, err := pgSuper.Exec(`GRANT CONNECT ON DATABASE other TO "app-user"`); err != nil {
		t.Fatal(err)
	}
	user.Spec.Roles = []string{"app_readers", "app_writers"}
	if err := k8s.Update(ctx, user); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "User Ready after roles change", ready(user))
	expectPrivileges(t, "app-user", map[string][]string{"app": {"CONNECT", "CREATE", "TEMPORARY"}})

	// Deleting drops the role, privileges and all.
	if err := k8s.Delete(ctx, user); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "User gone", func() error {
		err := k8s.Get(ctx, client.ObjectKeyFromObject(user), user)
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("still there: %v", err)
	})
	if ok, _, err := roleExists("app-user"); err != nil || ok {
		t.Fatalf("role app-user still exists (%v)", err)
	}
}

// An Entra user: the operator signs in as the server's Entra administrator
// with the identity ASO would use in this namespace (strict workload
// identity: a token for team-a/aso-workload, client A, exchanged at the
// fake Entra), and binds the role with pgaadauth.
func TestPostgreSQLEntraUser(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "team-a")
	strictCredential(t, ns, clientA)
	flexibleServer(t, ns, "pg")

	byName := &pgv1.User{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "app-identity"},
		Spec: pgv1.UserSpec{
			Owner:              &genruntime.KubernetesOwnerReference{Name: "pg"},
			AADUser:            &pgv1.AADUserSpec{ServerAdminUsername: "aso-team-a"},
			DatabasePrivileges: map[string][]pgv1.DatabasePrivilege{"app": privs(pgv1.DatabasePrivilegeConnect)},
		},
	}
	byOID := &pgv1.User{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "by-oid"},
		Spec: pgv1.UserSpec{
			AzureName: "reporting",
			Owner:     &genruntime.KubernetesOwnerReference{Name: "pg"},
			Roles:     []string{"app_readers"},
			AADUser: &pgv1.AADUserSpec{ServerAdminUsername: "aso-team-a",
				ObjectID: "11111111-2222-3333-4444-555555555555", PrincipalType: "group"},
		},
	}
	for _, u := range []*pgv1.User{byName, byOID} {
		if err := k8s.Create(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "Entra User Ready", ready(byName))
	eventually(t, timeout, "Entra User by object ID Ready", ready(byOID))

	if ok, comment, err := roleExists("app-identity"); err != nil || !ok || comment != "aadauth,name=app-identity,type=service" {
		t.Fatalf("role app-identity: exists=%v comment=%q err=%v", ok, comment, err)
	}
	if ok, comment, err := roleExists("reporting"); err != nil || !ok || comment != "aadauth,oid=11111111-2222-3333-4444-555555555555,type=group" {
		t.Fatalf("role reporting: exists=%v comment=%q err=%v", ok, comment, err)
	}
	expectPrivileges(t, "app-identity", map[string][]string{"app": {"CONNECT"}})
	if got, err := memberOf("reporting"); err != nil || !reflect.DeepEqual(got, []string{"app_readers"}) {
		t.Fatalf("roles of reporting: %v %v", got, err)
	}

	// The token the operator signed in with came from team-a's service
	// account through ASO's strict mode.
	found := false
	for _, r := range entraFake.Requests {
		if strings.HasSuffix(r, "/oauth2/v2.0/token") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no token request reached the fake Entra: %v", entraFake.Requests)
	}

	if err := k8s.Delete(ctx, byName); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "Entra role dropped", func() error {
		ok, _, err := roleExists("app-identity")
		if err != nil {
			return err
		}
		if ok {
			return fmt.Errorf("role still exists")
		}
		return nil
	})
}

// Another namespace's identity cannot sign in as team-a's administrator:
// the identity is the namespace's, not the operator's.
func TestPostgreSQLEntraUserOtherNamespaceIdentity(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "team-b")
	strictCredential(t, ns, clientB)
	flexibleServer(t, ns, "pg")

	u := &pgv1.User{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "intruder"},
		Spec: pgv1.UserSpec{
			Owner:   &genruntime.KubernetesOwnerReference{Name: "pg"},
			AADUser: &pgv1.AADUserSpec{ServerAdminUsername: "aso-team-a"},
		},
	}
	if err := k8s.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "User refused", notReadyWith(u, "password authentication failed"))
	if ok, _, err := roleExists("intruder"); err != nil || ok {
		t.Fatalf("role intruder exists=%v err=%v", ok, err)
	}
}

// The CRD refuses a user with both or neither of localUser and aadUser.
func TestPostgreSQLUserValidation(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "pg-validation")
	neither := &pgv1.User{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "neither"},
		Spec:       pgv1.UserSpec{Owner: &genruntime.KubernetesOwnerReference{Name: "pg"}},
	}
	if err := k8s.Create(ctx, neither); err == nil || !strings.Contains(err.Error(), "exactly one of localUser and aadUser") {
		t.Fatalf("want a refusal, got %v", err)
	}
	half := &pgv1.User{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "half"},
		Spec: pgv1.UserSpec{Owner: &genruntime.KubernetesOwnerReference{Name: "pg"},
			AADUser: &pgv1.AADUserSpec{ServerAdminUsername: "a", ObjectID: "x"}},
	}
	if err := k8s.Create(ctx, half); err == nil || !strings.Contains(err.Error(), "objectId and principalType are set together") {
		t.Fatalf("want a refusal, got %v", err)
	}
}
