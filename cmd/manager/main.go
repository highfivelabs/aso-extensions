// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// The controller manager: our kinds on Azure Service Operator's generic
// reconciler, with ASO's credentials. It reads the same AZURE_* settings as
// ASO's manager (aso-controller-settings) and the same aso-credential
// secrets and credential-from annotations.
package main

import (
	"flag"
	"os"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	"github.com/highfivelabs/aso-extensions/pkg/controllers"
	"github.com/highfivelabs/aso-extensions/pkg/manager"
)

// testOptions is set only in builds with -tags fakecloud (fakecloud.go).
var testOptions func(*manager.Options)

func main() {
	var (
		metricsAddr    string
		healthAddr     string
		leaderElection bool
		enableAzureCLI bool
	)
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "address the metrics endpoint binds to; 0 disables it")
	flag.StringVar(&healthAddr, "health-addr", ":8081", "address the health endpoint binds to")
	flag.BoolVar(&leaderElection, "enable-leader-election", false, "enable leader election")
	flag.BoolVar(&enableAzureCLI, "enable-azure-cli-authentication", false, "allow the Azure CLI credential in the global credential chain, as ASO's flag")
	zapOpts := zap.Options{}
	zapOpts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))
	log := ctrl.Log.WithName("setup")

	cfg, err := outoftree.ReadConfig()
	if err != nil {
		log.Error(err, "unable to get env configuration values")
		os.Exit(1)
	}

	cacheOpts := cache.Options{ReaderFailOnMissingInformer: true}
	if cfg.TargetNamespaces != nil {
		cacheOpts.DefaultNamespaces = map[string]cache.Config{}
		for _, ns := range cfg.TargetNamespaces {
			cacheOpts.DefaultNamespaces[ns] = cache.Config{}
		}
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                        controllers.CreateScheme(),
		Cache:                         cacheOpts,
		Metrics:                       metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress:        healthAddr,
		LeaderElection:                leaderElection,
		LeaderElectionID:              "azure-extensions-leader-election.azure.hfl.dev",
		LeaderElectionReleaseOnCancel: leaderElection,
		GracefulShutdownTimeout:       ptr(30 * time.Second),
	})
	if err != nil {
		log.Error(err, "unable to create manager")
		os.Exit(1)
	}

	opts := manager.Options{EnableAzureCLI: enableAzureCLI}
	if testOptions != nil {
		testOptions(&opts)
	}
	ctx := ctrl.SetupSignalHandler()
	if _, err := manager.Setup(ctx, mgr, cfg, opts); err != nil {
		log.Error(err, "unable to set up controllers")
		os.Exit(1)
	}
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	log.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		log.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func ptr[T any](v T) *T { return &v }
