// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package postgresql

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/rotisserie/eris"

	"github.com/highfivelabs/aso-extensions/pkg/util/set"
)

// DefaultPrivileges are the privileges a user gets on objects of one type
// that ForRole creates later in Schema (ALTER DEFAULT PRIVILEGES FOR ROLE
// … IN SCHEMA …), in the database a connection is to.
type DefaultPrivileges struct {
	Schema     string
	ForRole    string
	ObjectType string // tables, sequences, functions or types
	Privileges []string
}

type defaultPrivilegesKey struct {
	Schema, ForRole, ObjectType string
}

// The object types schema-level default privileges apply to: pg_default_acl's
// defaclobjtype, the keyword in ALTER DEFAULT PRIVILEGES, and the privileges
// ALL stands for (MAINTAIN on tables from PostgreSQL 17).
var defaultPrivilegesObjectTypes = map[string]struct {
	catalog string
	keyword string
	all     []string
}{
	"tables":    {"r", "TABLES", []string{"DELETE", "INSERT", "REFERENCES", "SELECT", "TRIGGER", "TRUNCATE", "UPDATE"}},
	"sequences": {"S", "SEQUENCES", []string{"SELECT", "UPDATE", "USAGE"}},
	"functions": {"f", "FUNCTIONS", []string{"EXECUTE"}},
	"types":     {"T", "TYPES", []string{"USAGE"}},
}

// allDefaultPrivileges is what ALL means for objectType on a server of
// version (server_version_num).
func allDefaultPrivileges(objectType string, version int) []string {
	all := defaultPrivilegesObjectTypes[objectType].all
	if objectType == "tables" && version >= 170000 {
		all = append(append([]string{}, all...), "MAINTAIN")
	}
	return all
}

// NormalizeDefaultPrivileges expands ALL, upper-cases, and refuses anything
// that is not a privilege on objectType on a server of version.
func NormalizeDefaultPrivileges(objectType string, privs []string, version int) (set.Set[string], error) {
	if _, ok := defaultPrivilegesObjectTypes[objectType]; !ok {
		return nil, eris.Errorf("%q is not an object type for default privileges (tables, sequences, functions, types)", objectType)
	}
	all := allDefaultPrivileges(objectType, version)
	valid := set.Make(all...)
	result := set.Make[string]()
	for _, p := range privs {
		switch u := strings.ToUpper(strings.TrimSpace(p)); {
		case u == "ALL" || u == "ALL PRIVILEGES":
			for _, a := range all {
				result.Add(a)
			}
		case valid.Contains(u):
			result.Add(u)
		default:
			return nil, eris.Errorf("%q is not a privilege on %s (%s, ALL) on this server", p, objectType, strings.Join(all, ", "))
		}
	}
	return result, nil
}

// GetUserDefaultPrivileges reads the schema-level default privileges granted
// to the user in the database db is connected to. Default privileges set
// without a schema, and object types other than the four above, are not
// read.
func GetUserDefaultPrivileges(ctx context.Context, db queryer, user string) (map[defaultPrivilegesKey]set.Set[string], error) {
	rows, err := db.QueryContext(ctx, `
SELECT n.nspname, pg_get_userbyid(d.defaclrole), d.defaclobjtype::text, a.privilege_type
FROM pg_default_acl d
JOIN pg_namespace n ON n.oid = d.defaclnamespace
CROSS JOIN LATERAL aclexplode(d.defaclacl) a
JOIN pg_roles r ON r.oid = a.grantee
WHERE r.rolname = $1`, user)
	if err != nil {
		return nil, eris.Wrapf(err, "listing default privileges for user %s", user)
	}
	defer rows.Close()

	objectTypes := map[string]string{}
	for name, t := range defaultPrivilegesObjectTypes {
		objectTypes[t.catalog] = name
	}
	result := map[defaultPrivilegesKey]set.Set[string]{}
	for rows.Next() {
		var schema, forRole, objType, privilege string
		if err := rows.Scan(&schema, &forRole, &objType, &privilege); err != nil {
			return nil, eris.Wrap(err, "extracting default privilege")
		}
		name, ok := objectTypes[objType]
		if !ok {
			continue
		}
		k := defaultPrivilegesKey{Schema: schema, ForRole: forRole, ObjectType: name}
		if _, ok := result[k]; !ok {
			result[k] = set.Make[string]()
		}
		result[k].Add(privilege)
	}
	if err := rows.Err(); err != nil {
		return nil, eris.Wrap(err, "iterating default privileges")
	}
	return result, nil
}

// DefaultPrivilegesOf returns the user's schema-level default privileges,
// sorted, for status.
func DefaultPrivilegesOf(ctx context.Context, db *sql.DB, user string) ([]DefaultPrivileges, error) {
	current, err := GetUserDefaultPrivileges(ctx, db, user)
	if err != nil {
		return nil, err
	}
	keys := sortedKeys(current)
	result := make([]DefaultPrivileges, 0, len(keys))
	for _, k := range keys {
		result = append(result, DefaultPrivileges{Schema: k.Schema, ForRole: k.ForRole, ObjectType: k.ObjectType, Privileges: sortedValues(current[k])})
	}
	return result, nil
}

// ReconcileUserDefaultPrivileges grants and revokes, in the database db is
// connected to, so that the user holds exactly the given schema-level
// default privileges and no others. Each change runs with the privileges of
// its ForRole (withPrivilegesOf). The result is read back: a privilege
// PostgreSQL did not grant or revoke is an error.
func ReconcileUserDefaultPrivileges(ctx context.Context, db *sql.DB, user string, desired []DefaultPrivileges) error {
	var version int
	if err := db.QueryRowContext(ctx, "SELECT current_setting('server_version_num')::int").Scan(&version); err != nil {
		return eris.Wrap(err, "reading the server version")
	}

	want := map[defaultPrivilegesKey]set.Set[string]{}
	for _, d := range desired {
		normalized, err := NormalizeDefaultPrivileges(d.ObjectType, d.Privileges, version)
		if err != nil {
			return eris.Wrapf(err, "default privileges for role %s in schema %s", d.ForRole, d.Schema)
		}
		k := defaultPrivilegesKey{Schema: d.Schema, ForRole: d.ForRole, ObjectType: d.ObjectType}
		if want[k] == nil {
			want[k] = set.Make[string]()
		}
		for p := range normalized {
			want[k].Add(p)
		}
	}

	current, err := GetUserDefaultPrivileges(ctx, db, user)
	if err != nil {
		return err
	}

	keys := map[defaultPrivilegesKey]set.Set[string]{}
	for k := range want {
		keys[k] = nil
	}
	for k := range current {
		keys[k] = nil
	}

	for _, k := range sortedKeys(keys) {
		add, remove := diff(current[k], want[k])
		if len(add) == 0 && len(remove) == 0 {
			continue
		}
		keyword := defaultPrivilegesObjectTypes[k.ObjectType].keyword
		prefix := fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA %s", escapeIdentifier(k.ForRole), escapeIdentifier(k.Schema))
		err := withPrivilegesOf(ctx, db, k.ForRole, func(tx *sql.Tx) error {
			// Privilege words and keywords are from the fixed lists above; identifiers are escaped.
			if len(add) > 0 {
				if _, err := tx.ExecContext(ctx, fmt.Sprintf("%s GRANT %s ON %s TO %s",
					prefix, strings.Join(add, ", "), keyword, escapeIdentifier(user))); err != nil {
					return eris.Wrapf(err, "granting %s on %s created by %s in schema %s", strings.Join(add, ", "), k.ObjectType, k.ForRole, k.Schema)
				}
			}
			if len(remove) > 0 {
				if _, err := tx.ExecContext(ctx, fmt.Sprintf("%s REVOKE %s ON %s FROM %s",
					prefix, strings.Join(remove, ", "), keyword, escapeIdentifier(user))); err != nil {
					return eris.Wrapf(err, "revoking %s on %s created by %s in schema %s", strings.Join(remove, ", "), k.ObjectType, k.ForRole, k.Schema)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	after, err := GetUserDefaultPrivileges(ctx, db, user)
	if err != nil {
		return err
	}
	for _, k := range sortedKeys(keys) {
		if add, remove := diff(after[k], want[k]); len(add) > 0 || len(remove) > 0 {
			return eris.Errorf("default privileges on %s created by %s in schema %s: after granting and revoking, the user still lacks %v and still holds %v", k.ObjectType, k.ForRole, k.Schema, add, remove)
		}
	}
	return nil
}

// RevokeAllDefaultPrivileges revokes every schema-level default privilege
// granted to the user in the database db is connected to.
func RevokeAllDefaultPrivileges(ctx context.Context, db *sql.DB, user string) error {
	return ReconcileUserDefaultPrivileges(ctx, db, user, nil)
}

func sortedKeys[V any](m map[defaultPrivilegesKey]V) []defaultPrivilegesKey {
	keys := make([]defaultPrivilegesKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.Schema != b.Schema {
			return a.Schema < b.Schema
		}
		if a.ForRole != b.ForRole {
			return a.ForRole < b.ForRole
		}
		return a.ObjectType < b.ObjectType
	})
	return keys
}
