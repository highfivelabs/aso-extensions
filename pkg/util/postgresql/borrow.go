// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package postgresql

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/rotisserie/eris"
)

// roleNeed is a role whose privileges a statement needs, and whether it
// also needs to be able to SET ROLE to it (to make the role an owner).
type roleNeed struct {
	role string
	set  bool
}

// withPrivilegesOf runs f in a transaction in which the current user has
// the privileges of role. Changing a schema's ACL needs the privileges of
// its owner, and ALTER DEFAULT PRIVILEGES FOR ROLE needs those of that
// role. An Azure administrator is not a superuser: since PostgreSQL 16 it
// holds only ADMIN OPTION (without INHERIT) on the roles it creates, which
// lets it grant them but not use their privileges. When it lacks them, it
// grants itself the role inside the transaction and revokes that grant
// before committing, so no membership outlives f.
func withPrivilegesOf(ctx context.Context, db *sql.DB, role string, f func(tx *sql.Tx) error) error {
	return withRoles(ctx, db, []roleNeed{{role: role}}, f)
}

// withRoles is withPrivilegesOf for several roles at once; a role with set
// is also one the current user can SET ROLE to (PostgreSQL 16 asks that of
// the new owner of an object).
func withRoles(ctx context.Context, db *sql.DB, needs []roleNeed, f func(tx *sql.Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return eris.Wrap(err, "starting a transaction")
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var version int
	if err = tx.QueryRowContext(ctx, "SELECT current_setting('server_version_num')::int").Scan(&version); err != nil {
		return eris.Wrap(err, "reading the server version")
	}
	// Before PostgreSQL 16 membership is all or nothing: a member may SET
	// ROLE, and has the privileges when it inherits.
	setCheck := "MEMBER"
	if version >= 160000 {
		setCheck = "SET"
	}

	var borrowed []string
	for _, n := range needs {
		var has bool
		if err = tx.QueryRowContext(ctx, "SELECT pg_has_role(current_user, $1::name, 'USAGE') AND (NOT $2 OR pg_has_role(current_user, $1::name, $3))",
			n.role, n.set, setCheck).Scan(&has); err != nil {
			return eris.Wrapf(err, "checking the privileges of role %s", n.role)
		}
		if has || contains(borrowed, n.role) {
			continue
		}

		// A grant of the role to the current user by the current user would
		// be the one revoked below; if one already exists (without INHERIT
		// or SET, or pg_has_role would be true), it is not ours to take away.
		var self int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM pg_auth_members m
JOIN pg_roles r ON r.oid = m.roleid
WHERE r.rolname = $1 AND m.member = (SELECT oid FROM pg_roles WHERE rolname = current_user) AND m.grantor = m.member`, n.role).Scan(&self); err != nil {
			return eris.Wrapf(err, "reading memberships in role %s", n.role)
		}
		if self > 0 {
			return eris.Errorf("the administrator %s has granted itself role %s without INHERIT or SET; it needs the privileges of %s", currentUser(ctx, tx), n.role, n.role)
		}

		// From PostgreSQL 16 a membership carries its own INHERIT and SET
		// options; ask for both, so a NOINHERIT administrator gets the
		// privileges too.
		grant := fmt.Sprintf("GRANT %s TO CURRENT_USER", escapeIdentifier(n.role))
		if version >= 160000 {
			grant += " WITH INHERIT TRUE, SET TRUE"
		}
		if _, err = tx.ExecContext(ctx, grant); err != nil {
			return eris.Wrapf(err, "the administrator needs the privileges of role %s (or ADMIN OPTION on it, to take them for this transaction)", n.role)
		}
		borrowed = append(borrowed, n.role)
	}

	if err = f(tx); err != nil {
		return err
	}
	for _, role := range borrowed {
		if _, err = tx.ExecContext(ctx, fmt.Sprintf("REVOKE %s FROM CURRENT_USER GRANTED BY CURRENT_USER", escapeIdentifier(role))); err != nil {
			return eris.Wrapf(err, "revoking role %s from the administrator", role)
		}
	}
	return eris.Wrap(tx.Commit(), "committing")
}

func contains(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

func currentUser(ctx context.Context, tx *sql.Tx) string {
	var name string
	if err := tx.QueryRowContext(ctx, "SELECT current_user").Scan(&name); err != nil {
		return "(unknown)"
	}
	return name
}
