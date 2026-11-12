// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

//go:build live

package live

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/jackc/pgx/v5"
	_ "github.com/microsoft/go-mssqldb"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	asopostgresql "github.com/Azure/azure-service-operator/v2/api/dbforpostgresql/v20250801/storage"
	asokeyvault "github.com/Azure/azure-service-operator/v2/api/keyvault/v1api20230701/storage"
	asosql "github.com/Azure/azure-service-operator/v2/api/sql/v20250101/storage"

	pgv1 "github.com/highfivelabs/aso-extensions/apis/dbforpostgresql/v1"
	kvv1 "github.com/highfivelabs/aso-extensions/apis/keyvault/v1"
	sqlv1 "github.com/highfivelabs/aso-extensions/apis/sql/v1"
)

// vaultGet reads a Key Vault data-plane URL with the live identity; a 404 is
// (404, nil, nil).
func vaultGet(t *testing.T, u string) (int, map[string]any, error) {
	t.Helper()
	tok, err := azure.cred.GetToken(context.Background(), policy.TokenRequestOptions{Scopes: []string{"https://vault.azure.net/.default"}})
	if err != nil {
		return 0, nil, err
	}
	req, _ := http.NewRequest(http.MethodGet, u+"?api-version=7.4", nil)
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	resp, err := azure.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode == http.StatusNotFound {
		return resp.StatusCode, nil, nil
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, nil, fmt.Errorf("GET %s: %d %s", u, resp.StatusCode, strings.Join(strings.Fields(string(raw)), " "))
	}
	return resp.StatusCode, out, nil
}

// A secret and an imported certificate in a Key Vault fixture (standard,
// access policies granting the live identity, so no role assignment is
// needed; operations cost fractions of a cent). The vault is soft-deleted
// afterwards: purging needs a subscription-scope right the identity lacks.
func TestLiveKeyVault(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.KeyVault")
	ns := namespace(t, "kv")
	vaultName := strings.ReplaceAll(azName("kv"), "-", "")
	vaultID := rgID() + "/providers/Microsoft.KeyVault/vaults/" + vaultName
	tenant := os.Getenv("AZURE_TENANT_ID")
	all := func(p ...string) []string { return p }
	fixture(t, vaultID, "2023-07-01", map[string]any{
		"location": location,
		"properties": map[string]any{
			"tenantId": tenant, "sku": map[string]any{"family": "A", "name": "standard"},
			"enableRbacAuthorization": false, "enableSoftDelete": true, "softDeleteRetentionInDays": 7,
			"publicNetworkAccess": "Enabled",
			"accessPolicies": []any{map[string]any{"tenantId": tenant, "objectId": identityObjectID(t), "permissions": map[string]any{
				"secrets":      all("get", "list", "set", "delete", "recover", "purge"),
				"certificates": all("get", "list", "create", "import", "update", "delete", "recover", "purge"),
				"keys":         all("get", "list", "delete", "recover", "purge"),
			}}},
		},
	})
	vaultURI := "https://" + vaultName + ".vault.azure.net/"
	vault := &asokeyvault.Vault{}
	vault.Namespace, vault.Name = ns, "vault"
	vault.Spec.AzureName, vault.Spec.Owner = vaultName, owner("rg")
	standIn(t, vault, vaultID, func() { vault.Status.Properties = &asokeyvault.VaultProperties_STATUS{VaultUri: ptr(vaultURI)} })

	certPEM, keyPEM := selfSigned(t, "live.example.com")
	secret(t, ns, "values", map[string]string{"db": "s3cret-value-1", "tls.crt": string(certPEM), "tls.key": string(keyPEM)})
	sec := &kvv1.VaultSecret{}
	sec.Namespace, sec.Name = ns, "db-password"
	sec.Spec = kvv1.VaultSecretSpec{
		Owner: &genruntime.KubernetesOwnerReference{Name: "vault"}, Value: &genruntime.SecretReference{Name: "values", Key: "db"},
		ContentType: "text/plain", Tags: map[string]string{"team": "platform"}, DeleteMode: ptr("delete"),
	}
	cert := &kvv1.VaultCertificate{}
	cert.Namespace, cert.Name = ns, "tls"
	cert.Spec = kvv1.VaultCertificateSpec{
		Owner:       &genruntime.KubernetesOwnerReference{Name: "vault"},
		Certificate: &genruntime.SecretReference{Name: "values", Key: "tls.crt"},
		PrivateKey:  &genruntime.SecretReference{Name: "values", Key: "tls.key"},
		DeleteMode:  ptr("delete"),
	}
	create(t, timeout, sec, cert)

	secretValue := func(want string) func() error {
		return func() error {
			_, r, err := vaultGet(t, vaultURI+"secrets/db-password")
			if err != nil {
				return err
			}
			if r["value"] != want || r["contentType"] != "text/plain" {
				return fmt.Errorf("secret: value %v contentType %v", r["value"] != nil, r["contentType"])
			}
			return nil
		}
	}
	eventually(t, timeout, "the secret in the vault", secretValue("s3cret-value-1"))
	eventually(t, timeout, "the certificate in the vault", func() error {
		_, r, err := vaultGet(t, vaultURI+"certificates/tls")
		if err != nil {
			return err
		}
		if r == nil || r["x5t"] == nil || cert.Status.Thumbprint == "" {
			return fmt.Errorf("certificate: %v, status thumbprint %q", r, cert.Status.Thumbprint)
		}
		return nil
	})

	// A new value is a new version.
	first := sec.Status.Version
	vals := &corev1.Secret{}
	vals.Namespace, vals.Name = ns, "values"
	update(t, vals, func(s *corev1.Secret) { s.Data["db"] = []byte("s3cret-value-2") })
	eventually(t, timeout, "the new secret value in the vault", secretValue("s3cret-value-2"))
	eventually(t, timeout, "a new secret version in status", func() error {
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(sec), sec); err != nil {
			return err
		}
		if sec.Status.Version == "" || sec.Status.Version == first {
			return fmt.Errorf("version %q (was %q)", sec.Status.Version, first)
		}
		return nil
	})

	remove(t, timeout, sec)
	remove(t, timeout, cert)
	for _, u := range []string{vaultURI + "secrets/db-password", vaultURI + "certificates/tls"} {
		eventually(t, timeout, u+" deleted", func() error {
			code, _, err := vaultGet(t, u)
			if err != nil {
				return err
			}
			if code != http.StatusNotFound {
				return fmt.Errorf("still there (%d)", code)
			}
			return nil
		})
	}
}

// A contained database user in an Azure SQL database fixture (Basic, about
// $0.007 an hour; the server free), reached over the public endpoint with a
// firewall rule for this machine.
func TestLiveSqlUser(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.Sql")
	ns := namespace(t, "sql")
	adminPass, userPass := password(), password()
	// Azure SQL is ProvisioningDisabled in eastus2 for this subscription
	// (2026-11-12): the server goes to the first region that takes it.
	var cands []candidate
	for i, region := range regions() {
		cands = append(cands, candidate{
			id: rgID() + "/providers/Microsoft.Sql/servers/" + azName(fmt.Sprintf("sql%d", i)),
			body: map[string]any{
				"location": region,
				"properties": map[string]any{
					"administratorLogin": "liveadmin", "administratorLoginPassword": adminPass, "version": "12.0",
					"minimalTlsVersion": "1.2", "publicNetworkAccess": "Enabled",
				},
			},
		})
	}
	i, _ := fixtureFirst(t, 20*time.Minute, "2023-08-01", cands)
	serverID, sqlRegion := cands[i].id, regions()[i]
	serverName := serverID[strings.LastIndex(serverID, "/")+1:]
	t.Logf("Azure SQL server in %s", sqlRegion)
	ip := runnerIP(t)
	fixture(t, serverID+"/firewallRules/runner", "2023-08-01", map[string]any{"properties": map[string]any{"startIpAddress": ip, "endIpAddress": ip}})
	dbID := serverID + "/databases/livedb"
	fixtureWait(t, 20*time.Minute, dbID, "2023-08-01", map[string]any{
		"location": sqlRegion, "sku": map[string]any{"name": "Basic", "tier": "Basic"},
	})
	fqdn := serverName + ".database.windows.net"
	server := &asosql.Server{}
	server.Namespace, server.Name = ns, "server"
	server.Spec.AzureName, server.Spec.Owner = serverName, owner("rg")
	standIn(t, server, serverID, func() { server.Status.FullyQualifiedDomainName = ptr(fqdn) })
	db := &asosql.ServersDatabase{}
	db.Namespace, db.Name = ns, "appdb"
	db.Spec.AzureName, db.Spec.Owner = "livedb", owner("server")
	standIn(t, db, dbID, nil)
	secret(t, ns, "sql", map[string]string{"admin": adminPass, "user": userPass})

	user := &sqlv1.User{}
	user.Namespace, user.Name = ns, "appuser"
	user.Spec = sqlv1.UserSpec{
		Owner: owner("appdb"), Roles: []string{"db_datareader"},
		LocalUser: &sqlv1.LocalUserSpec{
			ServerAdminUsername: "liveadmin",
			ServerAdminPassword: &genruntime.SecretReference{Name: "sql", Key: "admin"},
			Password:            &genruntime.SecretReference{Name: "sql", Key: "user"},
		},
	}
	create(t, timeout, user)
	roles := func(want ...string) func() error {
		return func() error {
			dsn := fmt.Sprintf("sqlserver://appuser:%s@%s?database=livedb&encrypt=true", url.QueryEscape(userPass), fqdn)
			conn, err := sql.Open("sqlserver", dsn)
			if err != nil {
				return err
			}
			defer conn.Close()
			rows, err := conn.Query(`SELECT r.name FROM sys.database_role_members m JOIN sys.database_principals r ON r.principal_id = m.role_principal_id
				JOIN sys.database_principals u ON u.principal_id = m.member_principal_id WHERE u.name = 'appuser' ORDER BY r.name`)
			if err != nil {
				return err
			}
			defer rows.Close()
			var got []string
			for rows.Next() {
				var n string
				if err := rows.Scan(&n); err != nil {
					return err
				}
				got = append(got, n)
			}
			if strings.Join(got, ",") != strings.Join(want, ",") {
				return fmt.Errorf("roles %v, want %v", got, want)
			}
			return nil
		}
	}
	eventually(t, timeout, "appuser signs in with db_datareader", roles("db_datareader"))
	update(t, user, func(u *sqlv1.User) { u.Spec.Roles = []string{"db_datareader", "db_datawriter"} })
	eventually(t, timeout, "User Ready after the change", ready(user))
	eventually(t, timeout, "appuser has db_datawriter too", roles("db_datareader", "db_datawriter"))
	remove(t, timeout, user)
	eventually(t, timeout, "appuser can no longer sign in", func() error {
		if roles()() == nil {
			return fmt.Errorf("still signs in")
		}
		return nil
	})
}

// A local role on a PostgreSQL flexible server fixture (Burstable B1ms,
// about $0.02 an hour), reached over the public endpoint with a firewall
// rule for this machine.
func TestLivePostgresUser(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.DBforPostgreSQL")
	ns := namespace(t, "pg")
	const pgAPI = "2024-08-01"
	adminPass, userPass := password(), password()
	// Burstable B1ms is SkuNotAvailable in eastus2 for this subscription
	// (2026-11-12): each region with B1ms, then B2s, the next Burstable size.
	var cands []candidate
	for _, sku := range []string{"Standard_B1ms", "Standard_B2s"} {
		for _, region := range regions() {
			cands = append(cands, candidate{
				id: rgID() + "/providers/Microsoft.DBforPostgreSQL/flexibleServers/" + azName(fmt.Sprintf("pg%d", len(cands))),
				body: map[string]any{
					"location": region,
					"sku":      map[string]any{"name": sku, "tier": "Burstable"},
					"properties": map[string]any{
						"version": "16", "administratorLogin": "liveadmin", "administratorLoginPassword": adminPass,
						"storage": map[string]any{"storageSizeGB": 32}, "backup": map[string]any{"backupRetentionDays": 7, "geoRedundantBackup": "Disabled"},
						"highAvailability": map[string]any{"mode": "Disabled"}, "network": map[string]any{"publicNetworkAccess": "Enabled"},
						"authConfig": map[string]any{"passwordAuth": "Enabled", "activeDirectoryAuth": "Disabled"},
					},
				},
			})
		}
	}
	i, _ := fixtureFirst(t, 30*time.Minute, pgAPI, cands)
	serverID := cands[i].id
	serverName := serverID[strings.LastIndex(serverID, "/")+1:]
	t.Logf("PostgreSQL server %v in %v", cands[i].body["sku"], cands[i].body["location"])
	eventually(t, 30*time.Minute, "the PostgreSQL server Ready", func() error {
		_, r, err := azure.get(context.Background(), serverID, pgAPI)
		if err != nil {
			return err
		}
		if s := properties(r)["state"]; s != "Ready" {
			return fmt.Errorf("state %v", s)
		}
		return nil
	})
	ip := runnerIP(t)
	fixture(t, serverID+"/firewallRules/runner", pgAPI, map[string]any{"properties": map[string]any{"startIpAddress": ip, "endIpAddress": ip}})
	fqdn := serverName + ".postgres.database.azure.com"
	server := &asopostgresql.FlexibleServer{}
	server.Namespace, server.Name = ns, "pg"
	server.Spec.AzureName, server.Spec.Owner = serverName, owner("rg")
	standIn(t, server, serverID, func() { server.Status.FullyQualifiedDomainName = ptr(fqdn) })
	secret(t, ns, "pg", map[string]string{"admin": adminPass, "user": userPass})

	user := &pgv1.User{}
	user.Namespace, user.Name = ns, "appuser"
	user.Spec = pgv1.UserSpec{
		Owner: &genruntime.KubernetesOwnerReference{Name: "pg"},
		LocalUser: &pgv1.LocalUserSpec{
			ServerAdminUsername: "liveadmin",
			ServerAdminPassword: &genruntime.SecretReference{Name: "pg", Key: "admin"},
			Password:            &genruntime.SecretReference{Name: "pg", Key: "user"},
		},
		DatabasePrivileges: map[string][]pgv1.DatabasePrivilege{"postgres": {pgv1.DatabasePrivilegeConnect}},
	}
	create(t, timeout, user)
	signIn := func(check func(*pgx.Conn) error) func() error {
		return func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			conn, err := pgx.Connect(ctx, fmt.Sprintf("postgres://appuser:%s@%s:5432/postgres?sslmode=require", url.QueryEscape(userPass), fqdn))
			if err != nil {
				return err
			}
			defer conn.Close(ctx)
			return check(conn)
		}
	}
	createDB := func(want bool) func(*pgx.Conn) error {
		return func(c *pgx.Conn) error {
			var got bool
			if err := c.QueryRow(context.Background(), "SELECT rolcreatedb FROM pg_roles WHERE rolname = current_user").Scan(&got); err != nil {
				return err
			}
			if got != want {
				return fmt.Errorf("rolcreatedb %v, want %v", got, want)
			}
			return nil
		}
	}
	eventually(t, timeout, "appuser signs in", signIn(createDB(false)))
	update(t, user, func(u *pgv1.User) { u.Spec.RoleOptions = &pgv1.RoleOptionsSpec{Login: true, CreateDb: true} })
	eventually(t, timeout, "User Ready after the change", ready(user))
	eventually(t, timeout, "appuser may create databases", signIn(createDB(true)))
	remove(t, timeout, user)
	eventually(t, timeout, "appuser can no longer sign in", func() error {
		if signIn(func(*pgx.Conn) error { return nil })() == nil {
			return fmt.Errorf("still signs in")
		}
		return nil
	})
}
