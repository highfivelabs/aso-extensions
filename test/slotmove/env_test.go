// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// The slot move (docs/plan.md, "Moving resources between slots"): two
// managers, wired as cmd/manager wires them, each the controller of one slot
// (blue and green) watching its own namespace, against one fake Resource
// Manager and one fake Key Vault over TLS, with azidentity's real
// credentials against the fake Entra. A real kube-apiserver (envtest) holds
// both slots' objects; in hosted Bundt each slot is its own cluster, which
// changes nothing the managers see (each reads only its namespace).
package slotmove

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	"github.com/highfivelabs/aso-extensions/pkg/controllers"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/arm"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/entra"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/keyvault"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/tlsutil"
	"github.com/highfivelabs/aso-extensions/pkg/manager"
)

const (
	tenantID    = "00000000-0000-0000-0000-00000000000a"
	clientA     = "00000000-0000-0000-0000-0000000000c1"
	subID       = "00000000-0000-0000-0000-0000000000f1"
	vaultHost   = "fakevault.vault.test"
	vaultScope  = "https://vault.test/.default"
	armHost     = "fakearm.test"
	armAudience = "https://fakearm.test"
	armScope    = "https://fakearm.test/.default"
	// The slots, and the namespace each one's manager watches.
	blue, green     = "blue", "green"
	blueNS, greenNS = "slot-blue", "slot-green"
	rgName          = "slotmove-rg"
	timeout         = 60 * time.Second
)

var (
	k8s      client.Client
	armFake  *arm.ARM
	vault    *keyvault.Vault
	armURL   = "https://" + armHost
	vaultURL = "https://" + vaultHost
	rgID     = "/subscriptions/" + subID + "/resourceGroups/" + rgName
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
	armHTTP, stopARM := serve(ca, armHost, func() http.Handler {
		armFake = arm.New(trustedFor(armScope))
		armFake.AddResource(rgID)
		return armFake
	})
	defer stopARM()
	vaultHTTP, stopVault := serve(ca, vaultHost, func() http.Handler {
		vault = keyvault.New(vaultURL, trustedFor(vaultScope))
		return vault
	})
	defer stopVault()

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

	k8s, err = client.New(restConfig, client.Options{Scheme: controllers.CreateScheme()})
	must(err)
	for _, ns := range []string{blueNS, greenNS} {
		must(k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}))
		must(credential(ctx, ns))
		trustNamespace(ns)
	}

	for slot, ns := range map[string]string{blue: blueNS, green: greenNS} {
		startManager(ctx, restConfig, slot, ns, armHTTP, vaultHTTP)
	}
	return m.Run()
}

// startManager runs one slot's manager, watching ns only, as cmd/manager
// does with AZURE_TARGET_NAMESPACES=ns and --slot=slot.
func startManager(ctx context.Context, restConfig *rest.Config, slot, ns string, armHTTP, vaultHTTP *http.Client) {
	// Each manager registers ASO's Resource Manager and CEL metrics, which
	// a process may register once: the second gets its own registry.
	ctrlmetrics.Registry = prometheus.NewRegistry()
	skip := true
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:  controllers.CreateScheme(),
		Cache:   cache.Options{ReaderFailOnMissingInformer: true, DefaultNamespaces: map[string]cache.Config{ns: {}}},
		Metrics: metricsserver.Options{BindAddress: "0"},
		// Both managers name their controllers alike, as two processes would.
		Controller: ctrlconfig.Controller{SkipNameValidation: &skip},
	})
	must(err)
	cfg, err := outoftree.ReadConfigUnvalidated()
	must(err)
	cfg.PodNamespace = "aso-" + slot
	cfg.TargetNamespaces = []string{ns}
	cfg.WorkloadIdentityAuthMode = outoftree.WorkloadIdentityAuthModeStrict
	cfg.MaxConcurrentReconciles = 2
	cfg.ResourceManagerEndpoint = armURL
	cfg.ResourceManagerAudience = armAudience
	_, err = manager.Setup(ctx, mgr, cfg, manager.Options{
		TokenProvider:         entra.TokenProvider(&entra.Transport{}),
		KeyVaultClientOptions: azcore.ClientOptions{Transport: vaultHTTP},
		ARMHTTPClient:         armHTTP,
		Slot:                  slot,
	})
	must(err)
	go func() {
		if err := mgr.Start(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "manager", slot+":", err)
		}
	}()
}

// serve runs a fake over TLS for host and returns a client that reaches it
// and trusts its certificate.
func serve(ca *tlsutil.CA, host string, handler func() http.Handler) (*http.Client, func()) {
	srv := httptest.NewUnstartedServer(handler())
	cert, err := ca.ServerTLS(host)
	must(err)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	addr := srv.Listener.Addr().String()
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: ca.Pool()},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}, srv.Close
}

var (
	trustedMu sync.Mutex
	trusted   = map[string]bool{}
)

func trustNamespace(ns string) {
	trustedMu.Lock()
	defer trustedMu.Unlock()
	trusted[entra.ServiceAccountSubject(ns, outoftree.DefaultWorkloadIdentityServiceAccount)] = true
}

// trustedFor accepts client A's tokens for scope, for a trusted namespace.
func trustedFor(scope string) func(string) bool {
	return func(token string) bool {
		c, err := entra.Parse(token)
		if err != nil || c.Scope != scope || c.Tenant != tenantID || c.ClientID != clientA {
			return false
		}
		trustedMu.Lock()
		defer trustedMu.Unlock()
		return trusted[c.Subject]
	}
}

// credential is ns's aso-credential with ASO's strict workload identity.
func credential(ctx context.Context, ns string) error {
	if err := k8s.Create(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: outoftree.DefaultWorkloadIdentityServiceAccount}}); err != nil {
		return err
	}
	return k8s.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: outoftree.NamespacedSecretName},
		StringData: map[string]string{"AZURE_SUBSCRIPTION_ID": subID, "AZURE_TENANT_ID": tenantID, "AZURE_CLIENT_ID": clientA},
	})
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
