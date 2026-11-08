/*
Copyright (c) Microsoft Corporation.
Licensed under the MIT license.
*/

// Modified by High Five Labs: Setup follows Azure Service Operator's
// v2/cmd/controller/app/setup.go (initializeClients, initializeWatchers) for
// our kinds only: the same global credential, the same credential provider
// (per-resource credential-from, the namespace's aso-credential, global;
// strict or relaxed workload identity), the same generic reconciler and
// controller options. There is no CRD management, no webhook and no ARM
// client: our kinds are data plane only.

// Package manager wires our kinds into a controller-runtime manager the way
// ASO's own manager wires its kinds.
package manager

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/benbjohnson/clock"
	"github.com/go-logr/logr"
	"github.com/rotisserie/eris"
	ctrl "sigs.k8s.io/controller-runtime"

	"sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	"github.com/highfivelabs/aso-extensions/pkg/controllers"
)

// Options are what tests and test builds change; production uses the zero value.
type Options struct {
	// TokenProvider makes the credentials for aso-credential secrets; nil
	// is ASO's default (azidentity).
	TokenProvider outoftree.TokenCredentialProvider
	// GlobalCredential replaces the one read from the AZURE_* settings.
	GlobalCredential *outoftree.Credential
	// KeyVaultClientOptions are passed to the Key Vault data plane client.
	KeyVaultClientOptions azcore.ClientOptions
	// EnableAzureCLI allows the Azure CLI credential in the global chain,
	// as ASO's --enable-azure-cli-authentication.
	EnableAzureCLI bool
}

// Setup registers our kinds on mgr.
func Setup(ctx context.Context, mgr ctrl.Manager, cfg outoftree.ConfigValues, opts Options) (*controllers.Reconcilers, error) {
	log := ctrl.Log.WithName("controllers")

	global := opts.GlobalCredential
	if global == nil {
		var err error
		global, err = outoftree.DefaultAzureCredential(ctx, cfg, log, opts.EnableAzureCLI)
		if err != nil {
			return nil, eris.Wrap(err, "error while fetching default global credential")
		}
	}

	kubeClient := outoftree.NewKubeClient(mgr.GetClient())
	clk := clock.New()
	credentialProvider := outoftree.NewCredentialProvider(
		global,
		kubeClient,
		&outoftree.CredentialProviderOptions{
			TokenProvider:               opts.TokenProvider,
			ServiceAccountTokenProvider: outoftree.NewServiceAccountTokenProvider(kubeClient, clk),
			WorkloadIdentityAuthMode:    cfg.WorkloadIdentityAuthMode,
			Cloud:                       ptr(cfg.Cloud()),
			AllowMultiEnvManagement:     cfg.AllowMultiEnvManagement,
			FederatedTokenFilePath:      cfg.FederatedTokenFilePath,
		},
	)
	positiveConditions := conditions.NewPositiveConditionBuilder(clk)

	resolver := outoftree.NewResolver(kubeClient)
	types, reconcilers := controllers.StorageTypes(kubeClient, resolver, positiveConditions, credentialProvider, cfg)
	reconcilers.VaultSecret.ClientOptions = opts.KeyVaultClientOptions
	if err := resolver.IndexStorageTypes(mgr.GetScheme(), append(controllers.OwnerTypes(), types...)); err != nil {
		return nil, eris.Wrap(err, "failed to index storage types")
	}

	// The owners are read from the cache, which (as in ASO) fails on a type
	// without an informer; ASO's informers are its controllers', ours are these.
	for _, owner := range controllers.OwnerTypes() {
		if _, err := mgr.GetCache().GetInformer(ctx, owner.Obj, cache.BlockUntilSynced(false)); err != nil {
			return nil, eris.Wrapf(err, "failed to start informer for %T", owner.Obj)
		}
	}

	logConfig(log, cfg)
	if err := outoftree.RegisterAll(mgr, mgr.GetFieldIndexer(), kubeClient, positiveConditions, types, outoftree.ControllerOptions(cfg)); err != nil {
		return nil, eris.Wrap(err, "failed to register gvks")
	}
	return reconcilers, nil
}

func logConfig(log logr.Logger, cfg outoftree.ConfigValues) {
	log.V(0).Info("Configuration details", "config", cfg.String())
}

func ptr[T any](v T) *T { return &v }
