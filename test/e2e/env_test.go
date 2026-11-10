// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// End-to-end tests: a real kube-apiserver (envtest) with our CRDs and the
// ASO owner CRDs, the manager wired exactly as cmd/manager wires it (ASO's
// generic reconciler and credential provider), azidentity's real
// credentials against a fake Entra, a fake Key Vault and a fake Resource
// Manager over TLS, and a real PostgreSQL in Docker set up like a flexible
// server.
package e2e

import (
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	"github.com/highfivelabs/aso-extensions/pkg/controllers"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/arm"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/entra"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/keyvault"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/postgres"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/tlsutil"
	"github.com/highfivelabs/aso-extensions/pkg/manager"
	postgresqlutil "github.com/highfivelabs/aso-extensions/pkg/util/postgresql"
)

const (
	tenantID = "00000000-0000-0000-0000-00000000000a"
	clientA  = "00000000-0000-0000-0000-0000000000c1"
	clientB  = "00000000-0000-0000-0000-0000000000c2"
	subID    = "00000000-0000-0000-0000-0000000000f1"
	// The fake vault's host: its token audience is https://vault.test/.default.
	vaultHost  = "fakevault.vault.test"
	vaultScope = "https://vault.test/.default"
	// The fake Resource Manager's host and audience.
	armHost     = "fakearm.test"
	armAudience = "https://fakearm.test"
	armScope    = "https://fakearm.test/.default"
	pgImage     = "postgres:17"
)

var (
	k8s         client.Client
	entraFake   = &entra.Transport{}
	vault       *keyvault.Vault
	armFake     *arm.ARM
	armURL      = "https://" + armHost
	armHTTP     *http.Client
	vaultURL    = "https://" + vaultHost
	pgPort      int
	pgSuper     *sql.DB
	pgSetup     postgres.Setup
	containerID string
)

// The Entra administrator of the fake PostgreSQL that team-a's identity
// (strict workload identity through team-a/aso-workload, client A) signs in as.
var (
	teamAToken = entra.Token(entra.Claims{Tenant: tenantID, ClientID: clientA,
		Subject: entra.ServiceAccountSubject("team-a", outoftree.DefaultWorkloadIdentityServiceAccount),
		Scope:   postgresqlutil.EntraScope})
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	if os.Getenv("E2E_VERBOSE") != "" {
		ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ca, err := tlsutil.NewCA()
	must(err)

	stop, err := startPostgres(ctx, ca)
	if err != nil {
		fmt.Fprintln(os.Stderr, "starting PostgreSQL in Docker:", err)
		return 1
	}
	defer stop()

	kvClient, stopVault := startVault(ca)
	defer stopVault()
	stopARM := startARM(ca)
	defer stopARM()
	stopSQL := startSQL(ca)
	defer stopSQL()

	root, _ := filepath.Abs("../..")
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join(root, "config/crds"), filepath.Join(root, "config/test/aso-crds")},
		ErrorIfCRDPathMissing: true,
	}
	restConfig, err := env.Start()
	if err != nil {
		fmt.Fprintln(os.Stderr, "starting envtest (KUBEBUILDER_ASSETS):", err)
		return 1
	}
	defer func() { _ = env.Stop() }()

	scheme := controllers.CreateScheme()
	k8s, err = client.New(restConfig, client.Options{Scheme: scheme})
	must(err)

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:  scheme,
		Cache:   cache.Options{ReaderFailOnMissingInformer: true},
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	must(err)

	cfg, err := outoftree.ReadConfigUnvalidated()
	must(err)
	cfg.PodNamespace = "azureserviceoperator-system"
	cfg.WorkloadIdentityAuthMode = outoftree.WorkloadIdentityAuthModeStrict
	cfg.MaxConcurrentReconciles = 2
	cfg.ResourceManagerEndpoint = armURL
	cfg.ResourceManagerAudience = armAudience
	_, err = manager.Setup(ctx, mgr, cfg, manager.Options{
		TokenProvider:         entra.TokenProvider(entraFake),
		KeyVaultClientOptions: azcore.ClientOptions{Transport: kvClient},
		ARMHTTPClient:         armHTTP,
	})
	must(err)
	go func() {
		if err := mgr.Start(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "manager:", err)
		}
	}()

	return m.Run()
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// startPostgres runs PostgreSQL in Docker with TLS (a certificate for
// localhost from ca) and the flexible-server stand-in set up.
func startPostgres(ctx context.Context, ca *tlsutil.CA) (func(), error) {
	pgSetup = postgres.Setup{
		LocalAdmin:         "pgadmin",
		LocalAdminPassword: "admin-password-1",
		EntraAdmins:        map[string]string{"aso-team-a": teamAToken},
		Databases:          []string{"app", "other"},
		Roles:              []string{"app_readers", "app_writers"},
	}
	dir, err := os.MkdirTemp("", "aso-ext-pg-")
	if err != nil {
		return nil, err
	}
	certPEM, keyPEM, err := ca.Server("localhost", "127.0.0.1")
	if err != nil {
		return nil, err
	}
	for name, data := range map[string][]byte{"server.crt": certPEM, "server.key": keyPEM, "init.sql": []byte(pgSetup.InitSQL())} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return nil, err
		}
	}
	script := "install -o postgres -m 600 /certs/server.key /tmp/server.key && " +
		"install -o postgres -m 644 /certs/server.crt /tmp/server.crt && " +
		"cp /certs/init.sql /docker-entrypoint-initdb.d/init.sql && " +
		"exec docker-entrypoint.sh postgres -c ssl=on -c ssl_cert_file=/tmp/server.crt -c ssl_key_file=/tmp/server.key"
	out, err := exec.CommandContext(ctx, "docker", "create", "-p", "127.0.0.1::5432",
		"-e", "POSTGRES_PASSWORD=super-password", "--label", "aso-extensions-e2e=1",
		pgImage, "sh", "-c", script).Output()
	if err != nil {
		return nil, fmt.Errorf("docker create: %w", err)
	}
	containerID = strings.TrimSpace(string(out))
	stop := func() { _ = exec.Command("docker", "rm", "-f", containerID).Run() }
	if out, err := exec.CommandContext(ctx, "docker", "cp", dir+"/.", containerID+":/certs").CombinedOutput(); err != nil {
		stop()
		return nil, fmt.Errorf("docker cp: %v: %s", err, out)
	}
	if out, err := exec.CommandContext(ctx, "docker", "start", containerID).CombinedOutput(); err != nil {
		stop()
		return nil, fmt.Errorf("docker start: %v: %s", err, out)
	}
	out, err = exec.CommandContext(ctx, "docker", "port", containerID, "5432/tcp").Output()
	if err != nil {
		stop()
		return nil, fmt.Errorf("docker port: %w", err)
	}
	_, portStr, err := net.SplitHostPort(strings.TrimSpace(strings.Split(string(out), "\n")[0]))
	if err != nil {
		stop()
		return nil, err
	}
	pgPort, _ = strconv.Atoi(portStr)

	// What the operator uses to reach a "flexible server" at localhost.
	postgresqlutil.ServerPort = pgPort
	postgresqlutil.TLSRootCAs = ca.Pool()

	cfg, err := pgx.ParseConfig(fmt.Sprintf("host=localhost port=%d user=postgres password=super-password dbname=postgres sslmode=verify-full", pgPort))
	if err != nil {
		stop()
		return nil, err
	}
	cfg.TLSConfig.RootCAs = ca.Pool()
	deadline := time.Now().Add(90 * time.Second)
	for {
		db := stdlib.OpenDB(*cfg)
		err = db.PingContext(ctx)
		if err == nil {
			// The init script runs before the server listens on TCP, so a
			// successful ping means it has run.
			var n int
			if err = db.QueryRowContext(ctx, "SELECT count(*) FROM pg_roles WHERE rolname = 'azure_pg_admin'").Scan(&n); err == nil && n == 1 {
				pgSuper = db
				break
			}
		}
		_ = db.Close()
		if time.Now().After(deadline) {
			logs, _ := exec.Command("docker", "logs", containerID).CombinedOutput()
			stop()
			return nil, fmt.Errorf("PostgreSQL did not become ready: %v\n%s", err, logs)
		}
		time.Sleep(time.Second)
	}
	return func() {
		_ = pgSuper.Close()
		stop()
		_ = os.RemoveAll(dir)
	}, nil
}

// startVault serves the fake Key Vault over TLS for vaultHost and returns a
// transport that reaches it and trusts its certificate.
func startVault(ca *tlsutil.CA) (*http.Client, func()) {
	vault = keyvault.New(vaultURL, func(token string) bool {
		c, err := entra.Parse(token)
		if os.Getenv("E2E_VERBOSE") != "" {
			fmt.Fprintf(os.Stderr, "fake vault: token %+v (%v)\n", c, err)
		}
		if err != nil || c.Scope != vaultScope || c.Tenant != tenantID || c.ClientID != clientA {
			return false
		}
		trustedMu.Lock()
		defer trustedMu.Unlock()
		return trusted[c.Subject]
	})
	srv := httptest.NewUnstartedServer(vault)
	cert, err := ca.ServerTLS(vaultHost)
	must(err)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	addr := srv.Listener.Addr().String()
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: ca.Pool()},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}
	return &http.Client{Transport: tr}, srv.Close
}

// startARM serves the fake Resource Manager over TLS for armHost, trusting
// the same identities as the fake vault (client A), for the Resource
// Manager audience; armHTTP reaches it and trusts its certificate.
func startARM(ca *tlsutil.CA) func() {
	armFake = arm.New(func(token string) bool {
		c, err := entra.Parse(token)
		if err != nil || c.Scope != armScope || c.Tenant != tenantID || c.ClientID != clientA {
			return false
		}
		trustedMu.Lock()
		defer trustedMu.Unlock()
		return trusted[c.Subject]
	})
	srv := httptest.NewUnstartedServer(armFake)
	cert, err := ca.ServerTLS(armHost)
	must(err)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	addr := srv.Listener.Addr().String()
	armHTTP = &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: ca.Pool()},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}
	return srv.Close
}

// The subjects the fake vault trusts (with client A): a client secret, and
// the aso-workload service accounts of the namespaces trustNamespace names.
var (
	trustedMu sync.Mutex
	trusted   = map[string]bool{entra.SecretSubject("team-c-secret"): true}
)

func trustNamespace(ns string) {
	trustedMu.Lock()
	defer trustedMu.Unlock()
	trusted[entra.ServiceAccountSubject(ns, outoftree.DefaultWorkloadIdentityServiceAccount)] = true
}

// namespace makes a namespace for one test.
func namespace(t *testing.T, name string) string {
	t.Helper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := k8s.Create(context.Background(), ns); err != nil {
		t.Fatalf("creating namespace %s: %v", name, err)
	}
	return name
}

// strictCredential is the aso-credential of a namespace using ASO's strict
// workload identity: the operator requests a token for <ns>/aso-workload.
func strictCredential(t *testing.T, ns, clientID string) {
	t.Helper()
	ctx := context.Background()
	if err := k8s.Create(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: outoftree.DefaultWorkloadIdentityServiceAccount}}); err != nil {
		t.Fatal(err)
	}
	if err := k8s.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: outoftree.NamespacedSecretName},
		StringData: map[string]string{"AZURE_SUBSCRIPTION_ID": subID, "AZURE_TENANT_ID": tenantID, "AZURE_CLIENT_ID": clientID},
	}); err != nil {
		t.Fatal(err)
	}
}

func secret(t *testing.T, ns, name string, data map[string]string) {
	t.Helper()
	if err := k8s.Create(context.Background(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}, StringData: data}); err != nil {
		t.Fatal(err)
	}
}

// eventually retries f until it returns nil or the timeout passes.
func eventually(t *testing.T, timeout time.Duration, what string, f func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var err error
	for time.Now().Before(deadline) {
		if err = f(); err == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("%s: %v", what, err)
}
