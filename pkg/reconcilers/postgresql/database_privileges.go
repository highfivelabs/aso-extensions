// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package postgresql

import (
	"context"
	"database/sql"

	"github.com/go-logr/logr"
	"github.com/rotisserie/eris"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	asopostgresql "github.com/highfivelabs/aso-extensions/apis/dbforpostgresql/v1"
	postgresqlutil "github.com/highfivelabs/aso-extensions/pkg/util/postgresql"
	"github.com/highfivelabs/aso-extensions/pkg/util/set"
)

// What inDatabases does in each database it visits.
type databaseMode int

const (
	// modeReconcile makes the user's schema and default privileges exactly
	// the spec's, then reads them back into status.
	modeReconcile databaseMode = iota
	// modeRead only reads them back into status.
	modeRead
	// modeRevokeAll revokes them all, and the user's privileges on objects
	// in those databases, before the role is dropped.
	modeRevokeAll
)

// inDatabases handles schemaPrivileges and defaultPrivileges, which live in
// each database's own catalog, on a connection to each database they name
// in spec or named at the last reconcile (status), so that a database
// removed from spec has its privileges revoked. maintenance is the
// connection to the maintenance database.
func (r *PostgreSQLUserReconciler) inDatabases(
	ctx context.Context,
	log logr.Logger,
	user *asopostgresql.User,
	secrets genruntime.Resolved[genruntime.SecretReference, string],
	maintenance *sql.DB,
	mode databaseMode,
) error {
	username := user.AzureName()

	specSchema := set.Make[string]()
	for d := range user.Spec.SchemaPrivileges {
		specSchema.Add(d)
	}
	specDefault := set.Make[string]()
	for _, d := range user.Spec.DefaultPrivileges {
		specDefault.Add(d.Database)
	}
	schemaDBs := specSchema.Copy()
	for d := range user.Status.AppliedSchemaPrivileges {
		schemaDBs.Add(d)
	}
	defaultDBs := specDefault.Copy()
	for _, d := range user.Status.AppliedDefaultPrivileges {
		defaultDBs.Add(d.Database)
	}
	databases := schemaDBs.Copy()
	databases.AddAll(defaultDBs)

	appliedSchema := map[string]map[string][]string{}
	var appliedDefault []asopostgresql.AppliedDefaultPrivileges

	for _, database := range set.AsSortedSlice(databases) {
		exists, err := postgresqlutil.DatabaseExists(ctx, maintenance, database)
		if err != nil {
			return eris.Wrapf(err, "checking database %s exists", database)
		}
		if !exists {
			if mode == modeReconcile && (specSchema.Contains(database) || specDefault.Contains(database)) {
				return eris.Errorf("database %s does not exist", database)
			}
			// A database that is gone holds no privileges.
			continue
		}

		err = func() error {
			conn, err := r.connectToDB(ctx, log, user, secrets, database)
			if err != nil {
				return err
			}
			defer conn.Close()

			if schemaDBs.Contains(database) {
				switch mode {
				case modeReconcile:
					err = postgresqlutil.ReconcileUserSchemaPrivileges(ctx, conn, username, schemaPrivileges(user.Spec.SchemaPrivileges[database]))
				case modeRevokeAll:
					err = postgresqlutil.RevokeAllSchemaPrivileges(ctx, conn, username)
				}
				if err != nil {
					return eris.Wrapf(err, "schema privileges in database %s", database)
				}
				if mode != modeRevokeAll {
					got, err := postgresqlutil.SchemaPrivilegesOf(ctx, conn, username)
					if err != nil {
						return err
					}
					if len(got) > 0 {
						appliedSchema[database] = got
					}
				}
			}

			if defaultDBs.Contains(database) {
				switch mode {
				case modeReconcile:
					err = postgresqlutil.ReconcileUserDefaultPrivileges(ctx, conn, username, defaultPrivileges(user.Spec.DefaultPrivileges, database))
				case modeRevokeAll:
					err = postgresqlutil.RevokeAllDefaultPrivileges(ctx, conn, username)
				}
				if err != nil {
					return eris.Wrapf(err, "default privileges in database %s", database)
				}
				if mode != modeRevokeAll {
					got, err := postgresqlutil.DefaultPrivilegesOf(ctx, conn, username)
					if err != nil {
						return err
					}
					for _, g := range got {
						appliedDefault = append(appliedDefault, asopostgresql.AppliedDefaultPrivileges{
							Database: database, Schema: g.Schema, ForRole: g.ForRole, ObjectType: g.ObjectType, Privileges: g.Privileges,
						})
					}
				}
			}

			// Default privileges have granted privileges on the objects other
			// roles created meanwhile; PostgreSQL refuses to drop a role that
			// holds any, so they go too.
			if mode == modeRevokeAll {
				if err := postgresqlutil.RevokeAllObjectPrivileges(ctx, conn, username); err != nil {
					return eris.Wrapf(err, "object privileges in database %s", database)
				}
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}

	if mode != modeRevokeAll {
		user.Status.AppliedSchemaPrivileges = nil
		if len(appliedSchema) > 0 {
			user.Status.AppliedSchemaPrivileges = appliedSchema
		}
		user.Status.AppliedDefaultPrivileges = appliedDefault
	}
	return nil
}

func schemaPrivileges(privs map[string][]asopostgresql.SchemaPrivilege) map[string][]string {
	result := make(map[string][]string, len(privs))
	for schema, ps := range privs {
		values := make([]string, 0, len(ps))
		for _, p := range ps {
			values = append(values, string(p))
		}
		result[schema] = values
	}
	return result
}

func defaultPrivileges(specs []asopostgresql.DefaultPrivilegesSpec, database string) []postgresqlutil.DefaultPrivileges {
	var result []postgresqlutil.DefaultPrivileges
	for _, s := range specs {
		if s.Database != database {
			continue
		}
		values := make([]string, 0, len(s.Privileges))
		for _, p := range s.Privileges {
			values = append(values, string(p))
		}
		result = append(result, postgresqlutil.DefaultPrivileges{
			Schema: s.Schema, ForRole: s.ForRole, ObjectType: string(s.ObjectType), Privileges: values,
		})
	}
	return result
}
