// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

//go:build live

// Live tests: our kinds against a real Azure subscription (docs/live-runs.md).
// A real kube-apiserver (envtest) with our CRDs and ASO's owner CRDs, and the
// manager wired exactly as production wires it (manager.Options{} zero):
// ASO's global credential from the AZURE_* environment, workload identity
// with AZURE_FEDERATED_TOKEN_FILE (a Google ID token, hack/live/credentials.sh),
// and the public Resource Manager. Nothing here runs without -tags live and
// ASO_EXT_LIVE=create-real-azure-resources; hack/live/run.sh is the way in.
package live

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	"github.com/highfivelabs/aso-extensions/pkg/controllers"
	"github.com/highfivelabs/aso-extensions/pkg/manager"
)

// optIn is the value ASO_EXT_LIVE must have: a live run creates real
// resources, and says so.
const optIn = "create-real-azure-resources"

var (
	k8s      client.Client
	subID    string
	rgName   string
	location string
	runID    string
	azure    *armClient
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

// refuse is the reason a live run must not start, or "".
func refuse() string {
	if os.Getenv("ASO_EXT_LIVE") != optIn {
		return "set ASO_EXT_LIVE=" + optIn + " to mean it (hack/live/run.sh)"
	}
	// Federated credentials only: no secret, certificate or CLI.
	for _, v := range []string{"AZURE_CLIENT_SECRET", "AZURE_CLIENT_CERTIFICATE", "AZURE_CLIENT_CERTIFICATE_PATH", "AZURE_CLIENT_CERTIFICATE_PASSWORD", "AZURE_PASSWORD", "AZURE_USERNAME"} {
		if os.Getenv(v) != "" {
			return v + " is set: live runs use federated credentials only"
		}
	}
	for _, v := range []string{"AZURE_SUBSCRIPTION_ID", "AZURE_TENANT_ID", "AZURE_CLIENT_ID", "AZURE_FEDERATED_TOKEN_FILE", "LIVE_RESOURCE_GROUP", "LIVE_LOCATION", "LIVE_RUN_ID"} {
		if os.Getenv(v) == "" {
			return v + " is not set (hack/live/run.sh sets it)"
		}
	}
	if os.Getenv("USE_WORKLOAD_IDENTITY_AUTH") != "true" {
		return "USE_WORKLOAD_IDENTITY_AUTH must be true"
	}
	allowed, err := allowedTargets()
	if err != nil {
		return err.Error()
	}
	target := os.Getenv("AZURE_SUBSCRIPTION_ID") + "/" + os.Getenv("LIVE_RESOURCE_GROUP")
	if !allowed[target] {
		return target + " is not in hack/live/targets.txt"
	}
	return ""
}

// allowedTargets reads hack/live/targets.txt: "<subscription>/<resource group>"
// per line, a reviewed change, the subscription as ${AZURE_SUBSCRIPTION_ID}.
func allowedTargets() (map[string]bool, error) {
	root, _ := filepath.Abs("../..")
	f, err := os.Open(filepath.Join(root, "hack/live/targets.txt"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]bool{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(strings.SplitN(s.Text(), "#", 2)[0])
		// The subscription is written as ${AZURE_SUBSCRIPTION_ID}: the ID is
		// not in the tree (.env.example).
		line = strings.ReplaceAll(line, "${AZURE_SUBSCRIPTION_ID}", os.Getenv("AZURE_SUBSCRIPTION_ID"))
		if line != "" {
			out[line] = true
		}
	}
	return out, s.Err()
}

func run(m *testing.M) int {
	if why := refuse(); why != "" {
		fmt.Fprintln(os.Stderr, "live:", why)
		return 2
	}
	subID = os.Getenv("AZURE_SUBSCRIPTION_ID")
	rgName = os.Getenv("LIVE_RESOURCE_GROUP")
	location = os.Getenv("LIVE_LOCATION")
	runID = os.Getenv("LIVE_RUN_ID")
	if os.Getenv("E2E_VERBOSE") != "" {
		ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var err error
	azure, err = newARMClient()
	if err != nil {
		fmt.Fprintln(os.Stderr, "live: the tests' own credential:", err)
		return 1
	}
	if _, _, err := azure.get(ctx, rgID(), "2021-04-01"); err != nil {
		fmt.Fprintln(os.Stderr, "live: reading the resource group:", err)
		return 1
	}

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
		Scheme: scheme,
		// The slots' namespaces are their own managers' (slot_test.go).
		// Namespaces themselves are cluster scoped: a metadata.namespace
		// selector on them is refused, the informer never syncs, and the
		// manager stopped two minutes in (run ax1215322672), so they are
		// listed whole.
		Cache: cache.Options{
			ReaderFailOnMissingInformer: true, DefaultFieldSelector: notSlotNamespaces(),
			ByObject: map[client.Object]cache.ByObject{&corev1.Namespace{}: {Field: fields.Everything()}},
		},
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	must(err)

	// As production: every setting from the environment, the global
	// credential is workload identity, the cloud is the public one.
	cfg, err := outoftree.ReadConfigUnvalidated()
	must(err)
	cfg.PodNamespace = "azureserviceoperator-system"
	cfg.MaxConcurrentReconciles = 4
	if _, err := manager.Setup(ctx, mgr, cfg, manager.Options{}); err != nil {
		fmt.Fprintln(os.Stderr, "live: manager setup:", err)
		return 1
	}
	go func() {
		if err := mgr.Start(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "manager:", err)
		}
	}()
	if err := startSlotManagers(ctx, restConfig); err != nil {
		fmt.Fprintln(os.Stderr, "live: slot managers:", err)
		return 1
	}
	return m.Run()
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
