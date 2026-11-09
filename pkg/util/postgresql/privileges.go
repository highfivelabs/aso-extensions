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
	kerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/highfivelabs/aso-extensions/pkg/util/set"
)

// The privileges PostgreSQL grants on a database. ALL stands for all of
// them; it is expanded before comparing with what the catalog holds.
var databasePrivileges = []string{"CONNECT", "CREATE", "TEMPORARY"}

// NormalizeDatabasePrivileges expands ALL, upper-cases, and refuses
// anything that is not a database privilege.
func NormalizeDatabasePrivileges(privs []string) (set.Set[string], error) {
	result := set.Make[string]()
	for _, p := range privs {
		switch u := strings.ToUpper(strings.TrimSpace(p)); u {
		case "ALL", "ALL PRIVILEGES":
			for _, all := range databasePrivileges {
				result.Add(all)
			}
		case "TEMP":
			result.Add("TEMPORARY")
		case "CONNECT", "CREATE", "TEMPORARY":
			result.Add(u)
		default:
			return nil, eris.Errorf("%q is not a database privilege (CONNECT, CREATE, TEMPORARY, ALL)", p)
		}
	}
	return result, nil
}

// GetUserDatabasePrivileges reads, from each database's ACL, the
// privileges granted directly to the user, keyed by database name. A
// database the user owns is left out: an owner holds every privilege on
// it, and its ACL entry is the owner's own.
func GetUserDatabasePrivileges(ctx context.Context, db *sql.DB, user string) (map[string]set.Set[string], error) {
	rows, err := db.QueryContext(ctx, `
SELECT d.datname, a.privilege_type
FROM pg_database d
CROSS JOIN LATERAL aclexplode(d.datacl) a
JOIN pg_roles r ON r.oid = a.grantee
WHERE r.rolname = $1 AND d.datdba <> r.oid`, user)
	if err != nil {
		return nil, eris.Wrapf(err, "listing database privileges for user %s", user)
	}
	defer rows.Close()

	result := map[string]set.Set[string]{}
	for rows.Next() {
		var database, privilege string
		if err := rows.Scan(&database, &privilege); err != nil {
			return nil, eris.Wrap(err, "extracting database privilege")
		}
		if _, ok := result[database]; !ok {
			result[database] = set.Make[string]()
		}
		result[database].Add(privilege)
	}
	if err := rows.Err(); err != nil {
		return nil, eris.Wrap(err, "iterating database privileges")
	}
	return result, nil
}

// ReconcileUserDatabasePrivileges grants and revokes so that the user holds
// exactly the given privileges on each database, and none on any database
// not given (as ASO's MySQL ReconcileUserDatabasePrivileges does).
func ReconcileUserDatabasePrivileges(ctx context.Context, db *sql.DB, user string, desired map[string][]string) error {
	want := map[string]set.Set[string]{}
	for database, privs := range desired {
		normalized, err := NormalizeDatabasePrivileges(privs)
		if err != nil {
			return eris.Wrapf(err, "database %s", database)
		}
		want[database] = normalized
	}

	current, err := GetUserDatabasePrivileges(ctx, db, user)
	if err != nil {
		return err
	}

	databases := set.Make[string]()
	for d := range want {
		databases.Add(d)
	}
	for d := range current {
		databases.Add(d)
	}

	var errs []error
	for _, database := range sortedValues(databases) {
		have := current[database]
		if have == nil {
			have = set.Make[string]()
		}
		target := want[database]
		if target == nil {
			target = set.Make[string]()
		}

		var add, remove []string
		for p := range target {
			if !have.Contains(p) {
				add = append(add, p)
			}
		}
		for p := range have {
			if !target.Contains(p) {
				remove = append(remove, p)
			}
		}
		sort.Strings(add)
		sort.Strings(remove)

		if len(add) == 0 && len(remove) == 0 {
			continue
		}

		// Changing a database's ACL needs the privileges of its owner, which
		// may be a User (ownedDatabases): borrow them when missing.
		owner, ok, err := DatabaseOwner(ctx, db, database)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !ok {
			errs = append(errs, eris.Errorf("database %s does not exist", database))
			continue
		}
		err = withPrivilegesOf(ctx, db, owner, func(tx *sql.Tx) error {
			if len(add) > 0 {
				// Privilege words are from the fixed list above; identifiers are escaped.
				_, err := tx.ExecContext(ctx, fmt.Sprintf("GRANT %s ON DATABASE %s TO %s",
					strings.Join(add, ", "), escapeIdentifier(database), escapeIdentifier(user)))
				if err != nil {
					return eris.Wrapf(err, "granting %s on database %s", strings.Join(add, ", "), database)
				}
			}
			if len(remove) > 0 {
				_, err := tx.ExecContext(ctx, fmt.Sprintf("REVOKE %s ON DATABASE %s FROM %s",
					strings.Join(remove, ", "), escapeIdentifier(database), escapeIdentifier(user)))
				if err != nil {
					return eris.Wrapf(err, "revoking %s on database %s", strings.Join(remove, ", "), database)
				}
			}
			return nil
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return kerrors.NewAggregate(errs)
}

// RevokeAllDatabasePrivileges revokes every privilege the user holds on
// any database, so that the role can be dropped.
func RevokeAllDatabasePrivileges(ctx context.Context, db *sql.DB, user string) error {
	return ReconcileUserDatabasePrivileges(ctx, db, user, nil)
}

// DatabasePrivilegesOf returns the user's privileges as sorted lists, for
// status.
func DatabasePrivilegesOf(ctx context.Context, db *sql.DB, user string) (map[string][]string, error) {
	current, err := GetUserDatabasePrivileges(ctx, db, user)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]string, len(current))
	for d, privs := range current {
		result[d] = sortedValues(privs)
	}
	return result, nil
}

func sortedValues(s set.Set[string]) []string {
	values := make([]string, 0, len(s))
	for v := range s {
		values = append(values, v)
	}
	sort.Strings(values)
	return values
}
