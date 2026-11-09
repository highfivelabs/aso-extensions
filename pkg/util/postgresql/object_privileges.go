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
)

// The objects of the database a connection is to on which the user holds a
// privilege (on the object, or on a column of a table) and which it does
// not own: owner, the keyword for REVOKE, and the object's name as the
// catalog prints it (quoted and qualified as needed).
const objectsWithPrivilegesQuery = `
WITH u AS (SELECT oid FROM pg_roles WHERE rolname = $1)
SELECT pg_get_userbyid(c.relowner), CASE WHEN c.relkind = 'S' THEN 'SEQUENCE' ELSE 'TABLE' END, c.oid::regclass::text
FROM pg_class c
WHERE c.relowner <> (SELECT oid FROM u)
  AND (EXISTS (SELECT 1 FROM aclexplode(c.relacl) a WHERE a.grantee = (SELECT oid FROM u))
    OR EXISTS (SELECT 1 FROM pg_attribute att CROSS JOIN LATERAL aclexplode(att.attacl) a
               WHERE att.attrelid = c.oid AND a.grantee = (SELECT oid FROM u)))
UNION ALL
SELECT pg_get_userbyid(p.proowner), 'ROUTINE', p.oid::regprocedure::text
FROM pg_proc p
WHERE p.proowner <> (SELECT oid FROM u)
  AND EXISTS (SELECT 1 FROM aclexplode(p.proacl) a WHERE a.grantee = (SELECT oid FROM u))
UNION ALL
SELECT pg_get_userbyid(t.typowner), 'TYPE', t.oid::regtype::text
FROM pg_type t
WHERE t.typowner <> (SELECT oid FROM u)
  AND EXISTS (SELECT 1 FROM aclexplode(t.typacl) a WHERE a.grantee = (SELECT oid FROM u))`

type objectWithPrivileges struct {
	owner, keyword, name string
}

func objectsWithPrivileges(ctx context.Context, db *sql.DB, user string) ([]objectWithPrivileges, error) {
	rows, err := db.QueryContext(ctx, objectsWithPrivilegesQuery, user)
	if err != nil {
		return nil, eris.Wrapf(err, "listing objects on which user %s holds privileges", user)
	}
	defer rows.Close()
	var result []objectWithPrivileges
	for rows.Next() {
		var o objectWithPrivileges
		if err := rows.Scan(&o.owner, &o.keyword, &o.name); err != nil {
			return nil, eris.Wrap(err, "extracting object privileges")
		}
		result = append(result, o)
	}
	return result, eris.Wrap(rows.Err(), "iterating object privileges")
}

// RevokeAllObjectPrivileges revokes every privilege the user holds on
// tables (and their columns), views, sequences, functions and types of the
// database db is connected to, so that the role can be dropped. Default
// privileges grant these as other roles create objects. Each revoke runs
// with the privileges of the object's owner (withPrivilegesOf). Objects
// the user owns are not touched: PostgreSQL refuses to drop the role while
// it owns any, and the operator does not drop them.
func RevokeAllObjectPrivileges(ctx context.Context, db *sql.DB, user string) error {
	objects, err := objectsWithPrivileges(ctx, db, user)
	if err != nil {
		return err
	}
	byOwner := map[string][]objectWithPrivileges{}
	for _, o := range objects {
		byOwner[o.owner] = append(byOwner[o.owner], o)
	}
	owners := make([]string, 0, len(byOwner))
	for o := range byOwner {
		owners = append(owners, o)
	}
	sort.Strings(owners)

	for _, owner := range owners {
		err := withPrivilegesOf(ctx, db, owner, func(tx *sql.Tx) error {
			for _, o := range byOwner[owner] {
				// The keyword is one of three fixed words; the name is the
				// catalog's own quoted rendering of the object.
				if _, err := tx.ExecContext(ctx, fmt.Sprintf("REVOKE ALL ON %s %s FROM %s", o.keyword, o.name, escapeIdentifier(user))); err != nil {
					return eris.Wrapf(err, "revoking privileges on %s %s", strings.ToLower(o.keyword), o.name)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	left, err := objectsWithPrivileges(ctx, db, user)
	if err != nil {
		return err
	}
	if len(left) > 0 {
		names := make([]string, 0, len(left))
		for _, o := range left {
			names = append(names, strings.ToLower(o.keyword)+" "+o.name)
		}
		return eris.Errorf("after revoking, the user still holds privileges on %s (granted by another grantor?)", strings.Join(names, ", "))
	}
	return nil
}
