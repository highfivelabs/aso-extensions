// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Package postgres makes a PostgreSQL server look enough like Azure
// Database for PostgreSQL flexible server for the User kind: the
// azure_pg_admin role, local and Microsoft Entra administrators, and Azure's
// pgaadauth_create_principal and pgaadauth_create_principal_with_oid
// functions. Tests and test builds only.
//
// The pgaadauth functions are stand-ins written from Azure's documentation
// (https://learn.microsoft.com/azure/postgresql/flexible-server/how-to-manage-azure-ad-users):
// they create a LOGIN role and record the binding in the role's comment
// ("aadauth,oid=…,type=…"), where Azure records it in a security label.
// They also give the caller ADMIN OPTION on the new role, as PostgreSQL 16
// requires for a CREATEROLE administrator to alter or drop it; what Azure's
// own functions do there is not documented. An Entra administrator's
// password is the fake Entra access token its identity would get, so only
// that identity can sign in as it.
package postgres

import (
	"fmt"
	"sort"
	"strings"
)

// Setup describes the server.
type Setup struct {
	// LocalAdmin and LocalAdminPassword: the server's password administrator.
	LocalAdmin         string
	LocalAdminPassword string
	// EntraAdmins maps an Entra administrator's role name to the access
	// token that signs in as it.
	EntraAdmins map[string]string
	// Databases are created owned by azure_pg_admin.
	Databases []string
	// Roles are NOLOGIN roles the administrators may grant.
	Roles []string
}

func ident(s string) string   { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func literal(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// InitSQL is the script a superuser runs once on an empty server.
func (s Setup) InitSQL() string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	w("CREATE ROLE azure_pg_admin NOLOGIN;")
	admins := []string{s.LocalAdmin}
	w("CREATE ROLE %s LOGIN PASSWORD %s CREATEROLE CREATEDB IN ROLE azure_pg_admin;", ident(s.LocalAdmin), literal(s.LocalAdminPassword))
	names := make([]string, 0, len(s.EntraAdmins))
	for n := range s.EntraAdmins {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w("CREATE ROLE %s LOGIN PASSWORD %s CREATEROLE CREATEDB IN ROLE azure_pg_admin;", ident(n), literal(s.EntraAdmins[n]))
		w("COMMENT ON ROLE %s IS %s;", ident(n), literal("aadauth,admin"))
		admins = append(admins, n)
	}
	for _, d := range s.Databases {
		w("CREATE DATABASE %s OWNER azure_pg_admin;", ident(d))
	}
	for _, r := range s.Roles {
		w("CREATE ROLE %s NOLOGIN;", ident(r))
		for _, a := range admins {
			w("GRANT %s TO %s WITH ADMIN OPTION;", ident(r), ident(a))
		}
	}
	w(`CREATE FUNCTION pgaadauth_bind(rolename text, binding text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER AS $$
BEGIN
  EXECUTE format('CREATE ROLE %%I LOGIN', rolename);
  EXECUTE format('COMMENT ON ROLE %%I IS %%L', rolename, binding);
  EXECUTE format('GRANT %%I TO %%I WITH ADMIN OPTION', rolename, session_user);
END $$;`)
	w(`CREATE FUNCTION pgaadauth_create_principal(rolename text, isadmin boolean, ismfa boolean) RETURNS text
LANGUAGE plpgsql SECURITY DEFINER AS $$
BEGIN
  PERFORM pgaadauth_bind(rolename, 'aadauth,name=' || rolename || ',type=service');
  RETURN 'Created role for "' || rolename || '"';
END $$;`)
	w(`CREATE FUNCTION pgaadauth_create_principal_with_oid(rolename text, objectid text, objecttype text, isadmin boolean, ismfa boolean) RETURNS text
LANGUAGE plpgsql SECURITY DEFINER AS $$
BEGIN
  IF objecttype NOT IN ('user', 'group', 'service') THEN
    RAISE EXCEPTION 'objecttype must be user, group or service';
  END IF;
  PERFORM pgaadauth_bind(rolename, 'aadauth,oid=' || objectid || ',type=' || objecttype);
  RETURN 'Created role for "' || rolename || '"';
END $$;`)
	w("REVOKE ALL ON FUNCTION pgaadauth_bind(text, text) FROM PUBLIC;")
	return b.String()
}
