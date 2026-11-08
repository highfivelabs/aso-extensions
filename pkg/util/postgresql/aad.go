// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package postgresql

import (
	"context"
	"database/sql"

	"github.com/rotisserie/eris"
)

// EntraScope is the token audience Azure Database for PostgreSQL accepts
// for Microsoft Entra sign-in (the same as ASO's MySQL AAD user uses).
const EntraScope = "https://ossrdbms-aad.database.windows.net/.default"

// ConnectToDBAAD signs in as an Entra principal: the user name is the
// principal's role name and the password is an Entra access token.
func ConnectToDBAAD(ctx context.Context, fullServerName string, database string, port int, user string, token string) (*sql.DB, error) {
	return ConnectToDB(ctx, fullServerName, database, port, user, token)
}

// PrincipalTypes are the principal types pgaadauth_create_principal_with_oid
// accepts.
var PrincipalTypes = map[string]bool{"user": true, "group": true, "service": true}

// CreateAADUser creates a role bound to a Microsoft Entra principal with
// Azure's pgaadauth extension. Without an object ID the role is bound to the
// principal whose display name is roleName (pgaadauth_create_principal);
// with one, to that object, under any role name
// (pgaadauth_create_principal_with_oid). The role is created with LOGIN, not
// as an administrator and without MFA.
// https://learn.microsoft.com/azure/postgresql/flexible-server/how-to-manage-azure-ad-users
func CreateAADUser(ctx context.Context, db *sql.DB, roleName string, objectID string, principalType string) (*SQLUser, error) {
	if err := FindBadChars(roleName); err != nil {
		return nil, eris.Wrap(err, "problem found with username")
	}

	var err error
	if objectID == "" {
		_, err = db.ExecContext(ctx, "SELECT * FROM pgaadauth_create_principal($1, false, false)", roleName)
	} else {
		if !PrincipalTypes[principalType] {
			return nil, eris.Errorf("principal type %q is not one of user, group, service", principalType)
		}
		_, err = db.ExecContext(ctx, "SELECT * FROM pgaadauth_create_principal_with_oid($1, $2, $3, false, false)", roleName, objectID, principalType)
	}
	if err != nil {
		return nil, eris.Wrapf(err, "failed to create Entra principal %s", roleName)
	}
	return &SQLUser{Name: roleName}, nil
}
