// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package postgresql

import (
	"context"
	"database/sql"

	"github.com/go-logr/logr"
	"github.com/rotisserie/eris"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	asopostgresql "github.com/highfivelabs/aso-extensions/apis/dbforpostgresql/v1"
	postgresqlutil "github.com/highfivelabs/aso-extensions/pkg/util/postgresql"
	"github.com/highfivelabs/aso-extensions/pkg/util/set"
)

// ReasonDatabaseReassigned is the event reason for a database given to the
// reassignOwnedTo role.
const ReasonDatabaseReassigned = "DatabaseReassigned"

// reassignOwnedTo is the role that receives the databases the user gives
// up: spec.reassignOwnedTo, or hfl_owner.
func reassignOwnedTo(user *asopostgresql.User) (string, error) {
	role := user.Spec.ReassignOwnedTo
	if role == "" {
		role = postgresqlutil.DefaultReassignOwnedTo
	}
	if role == user.AzureName() {
		return "", eris.Errorf("reassignOwnedTo cannot be the user itself (%s)", role)
	}
	return role, nil
}

// reconcileOwnership makes the user the owner of each database of
// spec.ownedDatabases, and gives the reassignOwnedTo role each database it
// owned at the last reconcile (status.ownedDatabases) that has left the
// list. Only the database moves; what the user created in it stays its
// own. It sets status.ownedDatabases and status.reassignedDatabases.
func reconcileOwnership(
	ctx context.Context,
	log logr.Logger,
	eventRecorder record.EventRecorder,
	user *asopostgresql.User,
	db *sql.DB,
) error {
	username := user.AzureName()
	want := set.Make(user.Spec.OwnedDatabases...)

	var reassigned []string
	if len(user.Status.OwnedDatabases) > 0 || len(want) > 0 {
		heir, err := reassignOwnedTo(user)
		if err != nil {
			return err
		}
		ensured := false
		for _, database := range set.AsSortedSlice(set.Make(user.Status.OwnedDatabases...)) {
			if want.Contains(database) {
				continue
			}
			owner, ok, err := postgresqlutil.DatabaseOwner(ctx, db, database)
			if err != nil {
				return err
			}
			if !ok || owner != username {
				// Gone, or someone else already gave it away.
				continue
			}
			if !ensured {
				if err := postgresqlutil.EnsureOwnerRole(ctx, db, heir); err != nil {
					return err
				}
				ensured = true
			}
			if err := postgresqlutil.SetDatabaseOwner(ctx, db, database, heir); err != nil {
				return err
			}
			log.V(logStatus).Info("Gave up database", "database", database, "to", heir)
			eventRecorder.Eventf(user, corev1.EventTypeNormal, ReasonDatabaseReassigned, "Database %s left ownedDatabases; gave it to %s", database, heir)
			reassigned = append(reassigned, database)
		}
	}

	for _, database := range set.AsSortedSlice(want) {
		if err := postgresqlutil.SetDatabaseOwner(ctx, db, database, username); err != nil {
			return err
		}
	}

	user.Status.ReassignedDatabases = reassigned
	return setOwnedDatabases(ctx, db, user)
}

// setOwnedDatabases records which databases of spec.ownedDatabases the
// user owns.
func setOwnedDatabases(ctx context.Context, db *sql.DB, user *asopostgresql.User) error {
	owned, err := postgresqlutil.DatabasesOwnedBy(ctx, db, user.AzureName())
	if err != nil {
		return err
	}
	want := set.Make(user.Spec.OwnedDatabases...)
	user.Status.OwnedDatabases = nil
	for _, d := range owned {
		if want.Contains(d) {
			user.Status.OwnedDatabases = append(user.Status.OwnedDatabases, d)
		}
	}
	return nil
}

// reassignAllOwned gives, before the user is dropped, every database it
// owns, with everything it owns in each (REASSIGN OWNED BY … TO, run in
// each of them), to the reassignOwnedTo role, so nothing is orphaned and
// PostgreSQL lets the role be dropped. Each database moved is an event and
// is listed in status.reassignedDatabases.
func (r *PostgreSQLUserReconciler) reassignAllOwned(
	ctx context.Context,
	log logr.Logger,
	eventRecorder record.EventRecorder,
	user *asopostgresql.User,
	secrets genruntime.Resolved[genruntime.SecretReference, string],
	maintenance *sql.DB,
) error {
	username := user.AzureName()
	owned, err := postgresqlutil.DatabasesOwnedBy(ctx, maintenance, username)
	if err != nil {
		return err
	}
	if len(owned) == 0 {
		return nil
	}
	heir, err := reassignOwnedTo(user)
	if err != nil {
		return err
	}
	if err := postgresqlutil.EnsureOwnerRole(ctx, maintenance, heir); err != nil {
		return err
	}

	// REASSIGN OWNED moves the databases themselves (shared objects) from
	// whichever database it runs in, but the objects inside a database only
	// from that database: run it in each.
	for _, database := range owned {
		err := func() error {
			conn, err := r.connectToDB(ctx, log, user, secrets, database)
			if err != nil {
				return err
			}
			defer conn.Close()
			return postgresqlutil.ReassignOwned(ctx, conn, username, heir)
		}()
		if err != nil {
			return err
		}
		log.V(logStatus).Info("Reassigned database", "database", database, "to", heir)
		eventRecorder.Eventf(user, corev1.EventTypeNormal, ReasonDatabaseReassigned, "Deleting user %s: gave database %s, and what the user owned in it, to %s", username, database, heir)
		user.Status.ReassignedDatabases = append(user.Status.ReassignedDatabases, database)
	}
	user.Status.OwnedDatabases = nil
	return nil
}
