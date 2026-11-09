// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package postgresql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/rotisserie/eris"

	"github.com/highfivelabs/aso-extensions/pkg/util/set"
)

// Schema and default privileges live in each database's own catalog
// (pg_namespace, pg_default_acl), so, unlike database privileges, they are
// read and changed on a connection to that database.

// The privileges PostgreSQL grants on a schema.
var schemaPrivileges = []string{"CREATE", "USAGE"}

// NormalizeSchemaPrivileges expands ALL, upper-cases, and refuses anything
// that is not a schema privilege.
func NormalizeSchemaPrivileges(privs []string) (set.Set[string], error) {
	result := set.Make[string]()
	for _, p := range privs {
		switch u := strings.ToUpper(strings.TrimSpace(p)); u {
		case "ALL", "ALL PRIVILEGES":
			for _, all := range schemaPrivileges {
				result.Add(all)
			}
		case "USAGE", "CREATE":
			result.Add(u)
		default:
			return nil, eris.Errorf("%q is not a schema privilege (USAGE, CREATE, ALL)", p)
		}
	}
	return result, nil
}

// GetUserSchemaPrivileges reads, from the ACL of each schema of the
// database db is connected to, the privileges granted directly to the user,
// keyed by schema name.
func GetUserSchemaPrivileges(ctx context.Context, db queryer, user string) (map[string]set.Set[string], error) {
	rows, err := db.QueryContext(ctx, `
SELECT n.nspname, a.privilege_type
FROM pg_namespace n
CROSS JOIN LATERAL aclexplode(n.nspacl) a
JOIN pg_roles r ON r.oid = a.grantee
WHERE r.rolname = $1`, user)
	if err != nil {
		return nil, eris.Wrapf(err, "listing schema privileges for user %s", user)
	}
	defer rows.Close()

	result := map[string]set.Set[string]{}
	for rows.Next() {
		var schema, privilege string
		if err := rows.Scan(&schema, &privilege); err != nil {
			return nil, eris.Wrap(err, "extracting schema privilege")
		}
		if _, ok := result[schema]; !ok {
			result[schema] = set.Make[string]()
		}
		result[schema].Add(privilege)
	}
	if err := rows.Err(); err != nil {
		return nil, eris.Wrap(err, "iterating schema privileges")
	}
	return result, nil
}

// SchemaPrivilegesOf returns the user's schema privileges as sorted lists,
// for status.
func SchemaPrivilegesOf(ctx context.Context, db *sql.DB, user string) (map[string][]string, error) {
	current, err := GetUserSchemaPrivileges(ctx, db, user)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]string, len(current))
	for s, privs := range current {
		result[s] = sortedValues(privs)
	}
	return result, nil
}

// ReconcileUserSchemaPrivileges grants and revokes, in the database db is
// connected to, so that the user holds exactly the given privileges on each
// schema and none on any schema not given. A grant or revoke on a schema
// runs with the privileges of the schema's owner (withPrivilegesOf). The
// result is read back: a privilege PostgreSQL did not grant or revoke is an
// error.
func ReconcileUserSchemaPrivileges(ctx context.Context, db *sql.DB, user string, desired map[string][]string) error {
	want := map[string]set.Set[string]{}
	for schema, privs := range desired {
		normalized, err := NormalizeSchemaPrivileges(privs)
		if err != nil {
			return eris.Wrapf(err, "schema %s", schema)
		}
		want[schema] = normalized
	}

	current, err := GetUserSchemaPrivileges(ctx, db, user)
	if err != nil {
		return err
	}

	schemas := set.Make[string]()
	for s := range want {
		schemas.Add(s)
	}
	for s := range current {
		schemas.Add(s)
	}

	for _, schema := range sortedValues(schemas) {
		add, remove := diff(current[schema], want[schema])
		if len(add) == 0 && len(remove) == 0 {
			continue
		}
		var owner string
		err := db.QueryRowContext(ctx, "SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = $1", schema).Scan(&owner)
		if errors.Is(err, sql.ErrNoRows) {
			return eris.Errorf("schema %s does not exist in database %s", schema, currentDatabase(ctx, db))
		}
		if err != nil {
			return eris.Wrapf(err, "finding the owner of schema %s", schema)
		}
		err = withPrivilegesOf(ctx, db, owner, func(tx *sql.Tx) error {
			// Privilege words are from the fixed list above; identifiers are escaped.
			if len(add) > 0 {
				if _, err := tx.ExecContext(ctx, fmt.Sprintf("GRANT %s ON SCHEMA %s TO %s",
					strings.Join(add, ", "), escapeIdentifier(schema), escapeIdentifier(user))); err != nil {
					return eris.Wrapf(err, "granting %s on schema %s", strings.Join(add, ", "), schema)
				}
			}
			if len(remove) > 0 {
				if _, err := tx.ExecContext(ctx, fmt.Sprintf("REVOKE %s ON SCHEMA %s FROM %s",
					strings.Join(remove, ", "), escapeIdentifier(schema), escapeIdentifier(user))); err != nil {
					return eris.Wrapf(err, "revoking %s on schema %s", strings.Join(remove, ", "), schema)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	after, err := GetUserSchemaPrivileges(ctx, db, user)
	if err != nil {
		return err
	}
	for _, schema := range sortedValues(schemas) {
		if add, remove := diff(after[schema], want[schema]); len(add) > 0 || len(remove) > 0 {
			return eris.Errorf("schema %s: after granting and revoking, the user still lacks %v and still holds %v (a grant made by another grantor?)", schema, add, remove)
		}
	}
	return nil
}

// RevokeAllSchemaPrivileges revokes every privilege the user holds on any
// schema of the database db is connected to.
func RevokeAllSchemaPrivileges(ctx context.Context, db *sql.DB, user string) error {
	return ReconcileUserSchemaPrivileges(ctx, db, user, nil)
}

// diff returns what to add to have to reach want, and what to remove, sorted.
func diff(have, want set.Set[string]) (add, remove []string) {
	for p := range want {
		if !have.Contains(p) {
			add = append(add, p)
		}
	}
	for p := range have {
		if !want.Contains(p) {
			remove = append(remove, p)
		}
	}
	sort.Strings(add)
	sort.Strings(remove)
	return add, remove
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func currentDatabase(ctx context.Context, db *sql.DB) string {
	var name string
	if err := db.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil {
		return "(unknown)"
	}
	return name
}

// withPrivilegesOf runs f in a transaction in which the current user has
// the privileges of role. Changing a schema's ACL needs the privileges of
// its owner, and ALTER DEFAULT PRIVILEGES FOR ROLE needs those of that
// role. An Azure administrator is not a superuser: since PostgreSQL 16 it
// holds only ADMIN OPTION (without INHERIT) on the roles it creates, which
// lets it grant them but not use their privileges. When it lacks them, it
// grants itself the role inside the transaction and revokes that grant
// before committing, so no membership outlives f.
func withPrivilegesOf(ctx context.Context, db *sql.DB, role string, f func(tx *sql.Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return eris.Wrap(err, "starting a transaction")
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var has bool
	if err = tx.QueryRowContext(ctx, "SELECT pg_has_role(current_user, $1::name, 'USAGE')", role).Scan(&has); err != nil {
		return eris.Wrapf(err, "checking the privileges of role %s", role)
	}
	if has {
		if err = f(tx); err != nil {
			return err
		}
		return eris.Wrap(tx.Commit(), "committing")
	}

	// A grant of the role to the current user by the current user would be
	// the one revoked below; if one already exists (without INHERIT, or
	// pg_has_role would be true), it is not ours to take away.
	var self int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM pg_auth_members m
JOIN pg_roles r ON r.oid = m.roleid
WHERE r.rolname = $1 AND m.member = (SELECT oid FROM pg_roles WHERE rolname = current_user) AND m.grantor = m.member`, role).Scan(&self); err != nil {
		return eris.Wrapf(err, "reading memberships in role %s", role)
	}
	if self > 0 {
		return eris.Errorf("the administrator %s has granted itself role %s without INHERIT; it needs the privileges of %s", currentUser(ctx, tx), role, role)
	}

	// From PostgreSQL 16 a membership carries its own INHERIT option; ask
	// for it, so a NOINHERIT administrator gets the privileges too.
	grant := fmt.Sprintf("GRANT %s TO CURRENT_USER", escapeIdentifier(role))
	var version int
	if err = tx.QueryRowContext(ctx, "SELECT current_setting('server_version_num')::int").Scan(&version); err != nil {
		return eris.Wrap(err, "reading the server version")
	}
	if version >= 160000 {
		grant += " WITH INHERIT TRUE"
	}
	if _, err = tx.ExecContext(ctx, grant); err != nil {
		return eris.Wrapf(err, "the administrator needs the privileges of role %s (or ADMIN OPTION on it, to take them for this transaction)", role)
	}
	if err = f(tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, fmt.Sprintf("REVOKE %s FROM CURRENT_USER GRANTED BY CURRENT_USER", escapeIdentifier(role))); err != nil {
		return eris.Wrapf(err, "revoking role %s from the administrator", role)
	}
	return eris.Wrap(tx.Commit(), "committing")
}

func currentUser(ctx context.Context, tx *sql.Tx) string {
	var name string
	if err := tx.QueryRowContext(ctx, "SELECT current_user").Scan(&name); err != nil {
		return "(unknown)"
	}
	return name
}
