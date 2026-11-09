// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package postgresql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/rotisserie/eris"
)

// DefaultReassignOwnedTo is the role that receives a User's databases when
// the User gives them up (it is deleted, or a database leaves its
// ownedDatabases), so that no database is left without a real owner.
const DefaultReassignOwnedTo = "hfl_owner"

// azurePGAdmin is the role Azure makes every server administrator a member
// of.
const azurePGAdmin = "azure_pg_admin"

// EnsureOwnerRole makes sure role exists as a role nobody signs in as
// (NOLOGIN) and that the server's administrators, through azure_pg_admin,
// hold its privileges, so any of them can later hand its databases to a
// real owner. A role of that name that can sign in is refused.
func EnsureOwnerRole(ctx context.Context, db *sql.DB, role string) error {
	if err := FindBadChars(role); err != nil {
		return eris.Wrapf(err, "problem found with role name %q", role)
	}
	var canLogin bool
	err := db.QueryRowContext(ctx, "SELECT rolcanlogin FROM pg_roles WHERE rolname = $1", role).Scan(&canLogin)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := db.ExecContext(ctx, fmt.Sprintf("CREATE ROLE %s NOLOGIN", escapeIdentifier(role))); err != nil {
			return eris.Wrapf(err, "creating role %s", role)
		}
	case err != nil:
		return eris.Wrapf(err, "reading role %s", role)
	case canLogin:
		return eris.Errorf("role %s can sign in (LOGIN); databases are given only to a NOLOGIN role", role)
	}

	var adminExists, held bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1),
EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1) AND pg_has_role($1::name, $2::name, 'USAGE')`, azurePGAdmin, role).Scan(&adminExists, &held); err != nil {
		return eris.Wrapf(err, "checking %s holds role %s", azurePGAdmin, role)
	}
	if adminExists && !held {
		if _, err := db.ExecContext(ctx, fmt.Sprintf("GRANT %s TO %s", escapeIdentifier(role), escapeIdentifier(azurePGAdmin))); err != nil {
			return eris.Wrapf(err, "granting role %s to %s", role, azurePGAdmin)
		}
	}
	return nil
}

// DatabasesOwnedBy lists, sorted, the databases user owns.
func DatabasesOwnedBy(ctx context.Context, db *sql.DB, user string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT d.datname FROM pg_database d JOIN pg_roles r ON r.oid = d.datdba WHERE r.rolname = $1`, user)
	if err != nil {
		return nil, eris.Wrapf(err, "listing databases owned by %s", user)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, eris.Wrap(err, "extracting database name")
		}
		result = append(result, name)
	}
	if err := rows.Err(); err != nil {
		return nil, eris.Wrap(err, "iterating databases")
	}
	sort.Strings(result)
	return result, nil
}

// DatabaseOwner returns the owner of database; ok is false when there is no
// such database.
func DatabaseOwner(ctx context.Context, db *sql.DB, database string) (owner string, ok bool, err error) {
	err = db.QueryRowContext(ctx, "SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = $1", database).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, eris.Wrapf(err, "finding the owner of database %s", database)
	}
	return owner, true, nil
}

// SetDatabaseOwner makes newOwner the owner of database (ALTER DATABASE …
// OWNER TO). That needs the privileges of the current owner, SET ROLE to
// the new one (PostgreSQL 16) and CREATEDB, which an Azure administrator
// has; the roles' privileges are borrowed for the one transaction when
// missing (withRoles).
func SetDatabaseOwner(ctx context.Context, db *sql.DB, database string, newOwner string) error {
	owner, ok, err := DatabaseOwner(ctx, db, database)
	if err != nil {
		return err
	}
	if !ok {
		return eris.Errorf("database %s does not exist", database)
	}
	if owner == newOwner {
		return nil
	}
	return withRoles(ctx, db, []roleNeed{{role: owner}, {role: newOwner, set: true}}, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, fmt.Sprintf("ALTER DATABASE %s OWNER TO %s", escapeIdentifier(database), escapeIdentifier(newOwner)))
		return eris.Wrapf(err, "giving database %s to %s", database, newOwner)
	})
}

// ReassignOwned gives everything user owns in the database db is connected
// to, and the databases it owns, to role (REASSIGN OWNED BY … TO), with the
// privileges of both borrowed for the one transaction when missing.
func ReassignOwned(ctx context.Context, db *sql.DB, user string, role string) error {
	return withRoles(ctx, db, []roleNeed{{role: user}, {role: role, set: true}}, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, fmt.Sprintf("REASSIGN OWNED BY %s TO %s", escapeIdentifier(user), escapeIdentifier(role)))
		return eris.Wrapf(err, "reassigning what %s owns in database %s to %s", user, currentDatabaseTx(ctx, tx), role)
	})
}

func currentDatabaseTx(ctx context.Context, tx *sql.Tx) string {
	var name string
	if err := tx.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil {
		return "(unknown)"
	}
	return name
}
