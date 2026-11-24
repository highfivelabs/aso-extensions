/*
Copyright (c) Microsoft Corporation.
Licensed under the MIT license.
*/

// Modified by High Five Labs: the storage-type registration,
// predicate, indexers and secret watches follow Azure Service Operator's
// v2/internal/controllers/controller_resources.go and indexers.go
// (GetKnownStorageTypes, makeStandardPredicate, watchSecretsFactory,
// watchConfigMapsFactory, watchEntity, indexPostgreSQLUserPassword, indexAzureSQLUserPassword,
// getGeneratedStorageTypes, augmentWithARMReconciler, GetResourceExtensions,
// getControllerName), for our kinds only. watchEntity lists with
// apimeta.ExtractList instead of ASO's internal reflecthelpers. The ARM
// kinds are registered from arm_resources_gen.go, which ASO's generator
// writes (hack/generate-arm.sh).

// Package controllers registers our kinds on ASO's generic
// reconciler.
package controllers

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	asoapim "github.com/Azure/azure-service-operator/v2/api/apimanagement/v20240501/storage"
	asoapp "github.com/Azure/azure-service-operator/v2/api/app/v20250101/storage"
	asoappconfig "github.com/Azure/azure-service-operator/v2/api/appconfiguration/v20240601/storage"
	asocache "github.com/Azure/azure-service-operator/v2/api/cache/v20241101/storage"
	asoredisenterprise "github.com/Azure/azure-service-operator/v2/api/cache/v20250401/storage"
	asocognitive "github.com/Azure/azure-service-operator/v2/api/cognitiveservices/v20250601/storage"
	asocompute "github.com/Azure/azure-service-operator/v2/api/compute/v20220301/storage"
	asodisk "github.com/Azure/azure-service-operator/v2/api/compute/v20240302/storage"
	asoregistry "github.com/Azure/azure-service-operator/v2/api/containerregistry/v20251101/storage"
	asofleet "github.com/Azure/azure-service-operator/v2/api/containerservice/v1api20250301/storage"
	asoaks "github.com/Azure/azure-service-operator/v2/api/containerservice/v20260501/storage"
	asodataprotection "github.com/Azure/azure-service-operator/v2/api/dataprotection/v20231101/storage"
	asomysql "github.com/Azure/azure-service-operator/v2/api/dbformysql/v20241230/storage"
	asopostgresql "github.com/Azure/azure-service-operator/v2/api/dbforpostgresql/v20250801/storage"
	asomongo "github.com/Azure/azure-service-operator/v2/api/documentdb/v1api20240701/storage"
	asocosmos "github.com/Azure/azure-service-operator/v2/api/documentdb/v1api20240815/storage"
	asoeventgrid "github.com/Azure/azure-service-operator/v2/api/eventgrid/v20250215/storage"
	asoeventhub "github.com/Azure/azure-service-operator/v2/api/eventhub/v20240101/storage"
	asoinsights "github.com/Azure/azure-service-operator/v2/api/insights/v1api20200202/storage"
	asokeyvault "github.com/Azure/azure-service-operator/v2/api/keyvault/v1api20230701/storage"
	asokusto "github.com/Azure/azure-service-operator/v2/api/kusto/v1api20240413/storage"
	asodns "github.com/Azure/azure-service-operator/v2/api/network/v1api20180501/storage"
	asodnsresolver "github.com/Azure/azure-service-operator/v2/api/network/v1api20220701/storage"
	asoasg "github.com/Azure/azure-service-operator/v2/api/network/v1api20240101/storage"
	asoprivatedns "github.com/Azure/azure-service-operator/v2/api/network/v1api20240601/storage"
	asowatcher "github.com/Azure/azure-service-operator/v2/api/network/v1api20241001/storage"
	asonetwork "github.com/Azure/azure-service-operator/v2/api/network/v20250301/storage"
	asoworkspace "github.com/Azure/azure-service-operator/v2/api/operationalinsights/v20250701/storage"
	asoresources "github.com/Azure/azure-service-operator/v2/api/resources/v1api20200601/storage"
	asoservicebus "github.com/Azure/azure-service-operator/v2/api/servicebus/v20240101/storage"
	asosignalr "github.com/Azure/azure-service-operator/v2/api/signalrservice/v1api20240301/storage"
	asosql "github.com/Azure/azure-service-operator/v2/api/sql/v20250101/storage"
	asostorage "github.com/Azure/azure-service-operator/v2/api/storage/v20260601/storage"
	asoweb "github.com/Azure/azure-service-operator/v2/api/web/v20260715/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/registration"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	postgresqlv1 "github.com/highfivelabs/aso-extensions/apis/dbforpostgresql/v1"
	entrav1 "github.com/highfivelabs/aso-extensions/apis/entra/v1"
	keyvaultv1 "github.com/highfivelabs/aso-extensions/apis/keyvault/v1"
	recoveryservicesv20260701 "github.com/highfivelabs/aso-extensions/apis/recoveryservices/v20260701"
	sqlv1 "github.com/highfivelabs/aso-extensions/apis/sql/v1"
	webv1 "github.com/highfivelabs/aso-extensions/apis/web/v20260715"
	sqlreconciler "github.com/highfivelabs/aso-extensions/pkg/reconcilers/azuresql"
	entrareconciler "github.com/highfivelabs/aso-extensions/pkg/reconcilers/entra"
	kvreconciler "github.com/highfivelabs/aso-extensions/pkg/reconcilers/keyvault"
	pgreconciler "github.com/highfivelabs/aso-extensions/pkg/reconcilers/postgresql"
	rsreconciler "github.com/highfivelabs/aso-extensions/pkg/reconcilers/recoveryservices"
	webreconciler "github.com/highfivelabs/aso-extensions/pkg/reconcilers/web"
	"github.com/highfivelabs/aso-extensions/pkg/slot"
)

// CreateScheme holds our kinds (the generated ARM kinds, from
// arm_resources_gen.go, among them), the ASO owner kinds they resolve
// (storage versions only), and Kubernetes' own.
func CreateScheme() *runtime.Scheme {
	scheme := createScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = postgresqlv1.AddToScheme(scheme)
	_ = keyvaultv1.AddToScheme(scheme)
	_ = sqlv1.AddToScheme(scheme)
	_ = entrav1.AddToScheme(scheme)
	_ = webv1.AddToScheme(scheme)
	_ = recoveryservicesv20260701.AddToScheme(scheme)
	_ = asopostgresql.AddToScheme(scheme)
	_ = asokeyvault.AddToScheme(scheme)
	_ = asoapp.AddToScheme(scheme)
	_ = asoresources.AddToScheme(scheme)
	_ = asoweb.AddToScheme(scheme)
	_ = asoworkspace.AddToScheme(scheme)
	_ = asoaks.AddToScheme(scheme)
	_ = asofleet.AddToScheme(scheme)
	_ = asoregistry.AddToScheme(scheme)
	_ = asocompute.AddToScheme(scheme)
	_ = asosql.AddToScheme(scheme)
	_ = asodns.AddToScheme(scheme)
	_ = asoprivatedns.AddToScheme(scheme)
	_ = asocosmos.AddToScheme(scheme)
	_ = asocognitive.AddToScheme(scheme)
	_ = asostorage.AddToScheme(scheme)
	_ = asoinsights.AddToScheme(scheme)
	_ = asonetwork.AddToScheme(scheme)
	_ = asodnsresolver.AddToScheme(scheme)
	_ = asocache.AddToScheme(scheme)
	_ = asoredisenterprise.AddToScheme(scheme)
	_ = asoappconfig.AddToScheme(scheme)
	_ = asomongo.AddToScheme(scheme)
	_ = asodisk.AddToScheme(scheme)
	_ = asoasg.AddToScheme(scheme)
	_ = asowatcher.AddToScheme(scheme)
	_ = asoservicebus.AddToScheme(scheme)
	_ = asoeventhub.AddToScheme(scheme)
	_ = asoeventgrid.AddToScheme(scheme)
	_ = asoapim.AddToScheme(scheme)
	_ = asomysql.AddToScheme(scheme)
	_ = asosignalr.AddToScheme(scheme)
	_ = asokusto.AddToScheme(scheme)
	_ = asodataprotection.AddToScheme(scheme)
	return scheme
}

// OwnerTypes are the ASO kinds our kinds name as owners. They are indexed
// in the resolver so it can read them, but not reconciled here: ASO does.
func OwnerTypes() []*registration.StorageType {
	return []*registration.StorageType{
		{Obj: &asopostgresql.FlexibleServer{}, Name: "aso_postgresql_flexibleserver"},
		{Obj: &asokeyvault.Vault{}, Name: "aso_keyvault_vault"},
		{Obj: &asoapp.ManagedEnvironment{}, Name: "aso_app_managedenvironment"},
		{Obj: &asoapp.ContainerApp{}, Name: "aso_app_containerapp"},
		{Obj: &asoresources.ResourceGroup{}, Name: "aso_resources_resourcegroup"},
		{Obj: &asoweb.Site{}, Name: "aso_web_site"},
		{Obj: &asoworkspace.Workspace{}, Name: "aso_operationalinsights_workspace"},
		{Obj: &asoregistry.Registry{}, Name: "aso_containerregistry_registry"},
		{Obj: &asocompute.VirtualMachine{}, Name: "aso_compute_virtualmachine"},
		{Obj: &asosql.Server{}, Name: "aso_sql_server"},
		{Obj: &asosql.ServersDatabase{}, Name: "aso_sql_serversdatabase"},
		{Obj: &asoaks.ManagedCluster{}, Name: "aso_containerservice_managedcluster"},
		{Obj: &asofleet.Fleet{}, Name: "aso_containerservice_fleet"},
		{Obj: &asodns.DnsZone{}, Name: "aso_network_dnszone"},
		{Obj: &asoprivatedns.PrivateDnsZone{}, Name: "aso_network_privatednszone"},
		{Obj: &asocosmos.DatabaseAccount{}, Name: "aso_documentdb_databaseaccount"},
		{Obj: &asocosmos.SqlDatabase{}, Name: "aso_documentdb_sqldatabase"},
		{Obj: &asocognitive.Account{}, Name: "aso_cognitiveservices_account"},
		{Obj: &asocognitive.Project{}, Name: "aso_cognitiveservices_project"},
		{Obj: &asostorage.StorageAccount{}, Name: "aso_storage_storageaccount"},
		{Obj: &asoinsights.Component{}, Name: "aso_insights_component"},
		// The owners of the catalog's ManagementLocks (the Azure Verified
		// Modules' lock on each resource) beyond the kinds above: a generated
		// extension resource resolves an owner by group and kind only when the
		// kind is here (docs/plan.md, ManagementLock); any other is named by
		// armId.
		{Obj: &asonetwork.AzureFirewall{}, Name: "aso_network_azurefirewall"},
		{Obj: &asonetwork.BastionHost{}, Name: "aso_network_bastionhost"},
		{Obj: &asonetwork.DdosProtectionPlan{}, Name: "aso_network_ddosprotectionplan"},
		{Obj: &asonetwork.FirewallPolicy{}, Name: "aso_network_firewallpolicy"},
		{Obj: &asonetwork.FirewallPoliciesRuleCollectionGroup{}, Name: "aso_network_firewallpoliciesrulecollectiongroup"},
		{Obj: &asonetwork.LoadBalancer{}, Name: "aso_network_loadbalancer"},
		{Obj: &asonetwork.NatGateway{}, Name: "aso_network_natgateway"},
		{Obj: &asonetwork.NetworkInterface{}, Name: "aso_network_networkinterface"},
		{Obj: &asonetwork.NetworkSecurityGroup{}, Name: "aso_network_networksecuritygroup"},
		{Obj: &asonetwork.PrivateEndpoint{}, Name: "aso_network_privateendpoint"},
		{Obj: &asonetwork.PublicIPAddress{}, Name: "aso_network_publicipaddress"},
		{Obj: &asonetwork.RouteTable{}, Name: "aso_network_routetable"},
		{Obj: &asonetwork.VirtualNetwork{}, Name: "aso_network_virtualnetwork"},
		{Obj: &asodnsresolver.DnsResolver{}, Name: "aso_network_dnsresolver"},
		{Obj: &asodnsresolver.DnsForwardingRuleset{}, Name: "aso_network_dnsforwardingruleset"},
		{Obj: &asoweb.ServerFarm{}, Name: "aso_web_serverfarm"},
		{Obj: &asosql.ServersElasticPool{}, Name: "aso_sql_serverselasticpool"},
		{Obj: &asosql.ManagedInstance{}, Name: "aso_sql_managedinstance"},
		{Obj: &asosql.ManagedInstancesDatabase{}, Name: "aso_sql_managedinstancesdatabase"},
		{Obj: &asodisk.Disk{}, Name: "aso_compute_disk"},
		{Obj: &asodisk.DiskAccess{}, Name: "aso_compute_diskaccess"},
		{Obj: &asocache.Redis{}, Name: "aso_cache_redis"},
		{Obj: &asoredisenterprise.RedisEnterprise{}, Name: "aso_cache_redisenterprise"},
		{Obj: &asoappconfig.ConfigurationStore{}, Name: "aso_appconfiguration_configurationstore"},
		{Obj: &asomongo.MongoCluster{}, Name: "aso_documentdb_mongocluster"},
		{Obj: &asoasg.ApplicationSecurityGroup{}, Name: "aso_network_applicationsecuritygroup"},
		{Obj: &asowatcher.NetworkWatcher{}, Name: "aso_network_networkwatcher"},
		{Obj: &asoservicebus.Namespace{}, Name: "aso_servicebus_namespace"},
		{Obj: &asoeventhub.Namespace{}, Name: "aso_eventhub_namespace"},
		{Obj: &asoeventgrid.Namespace{}, Name: "aso_eventgrid_namespace"},
		{Obj: &asoeventgrid.NamespaceTopic{}, Name: "aso_eventgrid_namespacetopic"},
		{Obj: &asoapim.Service{}, Name: "aso_apimanagement_service"},
		{Obj: &asoapim.Api{}, Name: "aso_apimanagement_api"},
		{Obj: &asoapim.Product{}, Name: "aso_apimanagement_product"},
		{Obj: &asomysql.FlexibleServer{}, Name: "aso_dbformysql_flexibleserver"},
		{Obj: &asosignalr.SignalR{}, Name: "aso_signalrservice_signalr"},
		{Obj: &asosignalr.Replica{}, Name: "aso_signalrservice_replica"},
		{Obj: &asokusto.Cluster{}, Name: "aso_kusto_cluster"},
		{Obj: &asokusto.Database{}, Name: "aso_kusto_database"},
		{Obj: &asodataprotection.BackupVault{}, Name: "aso_dataprotection_backupvault"},
	}
}

// Reconcilers lets tests replace a reconciler's settings.
type Reconcilers struct {
	PostgreSQLUser   *pgreconciler.PostgreSQLUserReconciler
	VaultSecret      *kvreconciler.VaultSecretReconciler
	VaultCertificate *kvreconciler.VaultCertificateReconciler
	AzureSQLUser     *sqlreconciler.AzureSQLUserReconciler
	// DirectoryRoleAssignment reaches Microsoft Graph through its Clients.
	DirectoryRoleAssignment *entrareconciler.DirectoryRoleAssignmentReconciler
	// SiteConfig reconciles SitesConfigAppSettings and
	// SitesConfigConnectionStrings through Resource Manager.
	SiteConfig *webreconciler.SiteConfigReconciler
	// VaultsProtectedItem reaches Resource Manager through the generated
	// kinds' ARM connection factory.
	VaultsProtectedItem *rsreconciler.VaultsProtectedItemReconciler
}

// ARM is what the generated ARM kinds are reconciled with: ASO's generic ARM
// reconciler needs a connection to Resource Manager per credential and a CEL
// evaluator.
type ARM struct {
	ConnectionFactory   outoftree.ARMConnectionFactory
	ExpressionEvaluator outoftree.ExpressionEvaluator
}

// StorageTypes are our kinds with their reconcilers, as ASO's
// GetKnownStorageTypes builds its own.
func StorageTypes(
	scheme *runtime.Scheme,
	kubeClient outoftree.KubeClient,
	resolver *outoftree.Resolver,
	positiveConditions *conditions.PositiveConditionBuilder,
	credentialProvider outoftree.CredentialProvider,
	arm ARM,
	cfg outoftree.ConfigValues,
) ([]*registration.StorageType, *Reconcilers, error) {
	rs := &Reconcilers{
		PostgreSQLUser:          pgreconciler.NewPostgreSQLUserReconciler(kubeClient, resolver, positiveConditions, credentialProvider, cfg),
		VaultSecret:             kvreconciler.NewVaultSecretReconciler(kubeClient, resolver, positiveConditions, credentialProvider),
		VaultCertificate:        kvreconciler.NewVaultCertificateReconciler(kubeClient, resolver, positiveConditions, credentialProvider),
		AzureSQLUser:            sqlreconciler.NewAzureSQLUserReconciler(kubeClient, resolver, positiveConditions, credentialProvider, cfg),
		DirectoryRoleAssignment: entrareconciler.NewDirectoryRoleAssignmentReconciler(kubeClient, resolver, positiveConditions, credentialProvider),
		SiteConfig:              webreconciler.NewSiteConfigReconciler(kubeClient, resolver, positiveConditions, credentialProvider, cfg),
		VaultsProtectedItem:     rsreconciler.NewVaultsProtectedItemReconciler(kubeClient, resolver, positiveConditions, arm.ConnectionFactory),
	}
	generated, err := generatedStorageTypes(scheme, kubeClient, resolver, positiveConditions, arm, cfg)
	if err != nil {
		return nil, nil, err
	}
	return append(generated, []*registration.StorageType{
		{
			Obj:        &postgresqlv1.User{},
			Name:       "hfl_postgresql_user",
			Reconciler: rs.PostgreSQLUser,
			Predicate:  standardPredicate(),
			Indexes: []registration.Index{
				{Key: ".spec.localUser.password", Func: indexPostgreSQLUserPassword},
			},
			Watches: []registration.Watch{
				{Type: &corev1.Secret{}, MakeEventHandler: watchSecretsFactory([]string{".spec.localUser.password"}, &postgresqlv1.UserList{})},
			},
		},
		{
			Obj:        &keyvaultv1.VaultSecret{},
			Name:       "hfl_keyvault_vaultsecret",
			Reconciler: rs.VaultSecret,
			Predicate:  standardPredicate(),
			Indexes: []registration.Index{
				{Key: ".spec.value", Func: indexVaultSecretValue},
			},
			Watches: []registration.Watch{
				{Type: &corev1.Secret{}, MakeEventHandler: watchSecretsFactory([]string{".spec.value"}, &keyvaultv1.VaultSecretList{})},
			},
		},
		{
			Obj:        &keyvaultv1.VaultCertificate{},
			Name:       "hfl_keyvault_vaultcertificate",
			Reconciler: rs.VaultCertificate,
			Predicate:  standardPredicate(),
			Indexes: []registration.Index{
				{Key: ".spec.certificate", Func: indexVaultCertificateCertificate},
				{Key: ".spec.privateKey", Func: indexVaultCertificatePrivateKey},
			},
			Watches: []registration.Watch{
				{Type: &corev1.Secret{}, MakeEventHandler: watchSecretsFactory([]string{".spec.certificate", ".spec.privateKey"}, &keyvaultv1.VaultCertificateList{})},
			},
		},
		{
			Obj:        &sqlv1.User{},
			Name:       "hfl_sql_user",
			Reconciler: rs.AzureSQLUser,
			Predicate:  standardPredicate(),
			Indexes: []registration.Index{
				{Key: ".spec.localUser.password", Func: indexAzureSQLUserPassword},
			},
			Watches: []registration.Watch{
				{Type: &corev1.Secret{}, MakeEventHandler: watchSecretsFactory([]string{".spec.localUser.password"}, &sqlv1.UserList{})},
			},
		},
		{
			Obj:        &entrav1.DirectoryRoleAssignment{},
			Name:       "hfl_entra_directoryroleassignment",
			Reconciler: rs.DirectoryRoleAssignment,
			Predicate:  standardPredicate(),
		},
		{
			Obj:        &webv1.SitesConfigAppSettings{},
			Name:       "hfl_web_sitesconfigappsettings",
			Reconciler: rs.SiteConfig,
			Predicate:  standardPredicate(),
			Indexes: []registration.Index{
				{Key: ".spec.settings.valueFromSecret", Func: indexAppSettingsSecrets},
				{Key: ".spec.settings.valueFromConfig", Func: indexAppSettingsConfigMaps},
			},
			Watches: []registration.Watch{
				{Type: &corev1.Secret{}, MakeEventHandler: watchSecretsFactory([]string{".spec.settings.valueFromSecret"}, &webv1.SitesConfigAppSettingsList{})},
				{Type: &corev1.ConfigMap{}, MakeEventHandler: watchConfigMapsFactory([]string{".spec.settings.valueFromConfig"}, &webv1.SitesConfigAppSettingsList{})},
			},
		},
		{
			Obj:        &webv1.SitesConfigConnectionStrings{},
			Name:       "hfl_web_sitesconfigconnectionstrings",
			Reconciler: rs.SiteConfig,
			Predicate:  standardPredicate(),
			Indexes: []registration.Index{
				{Key: ".spec.connectionStrings.valueFromSecret", Func: indexConnectionStringsSecrets},
				{Key: ".spec.connectionStrings.valueFromConfig", Func: indexConnectionStringsConfigMaps},
			},
			Watches: []registration.Watch{
				{Type: &corev1.Secret{}, MakeEventHandler: watchSecretsFactory([]string{".spec.connectionStrings.valueFromSecret"}, &webv1.SitesConfigConnectionStringsList{})},
				{Type: &corev1.ConfigMap{}, MakeEventHandler: watchConfigMapsFactory([]string{".spec.connectionStrings.valueFromConfig"}, &webv1.SitesConfigConnectionStringsList{})},
			},
		},
		{
			Obj:        &recoveryservicesv20260701.VaultsProtectedItem{},
			Name:       "hfl_recoveryservices_vaultsprotecteditem",
			Reconciler: rs.VaultsProtectedItem,
			Predicate:  standardPredicate(),
		},
	}...), rs, nil
}

// generatedStorageTypes are the ARM kinds ASO's generator wrote, each
// reconciled by ASO's generic ARM reconciler as ASO's own are
// (getGeneratedStorageTypes, augmentWithARMReconciler).
func generatedStorageTypes(
	scheme *runtime.Scheme,
	kubeClient outoftree.KubeClient,
	resolver *outoftree.Resolver,
	positiveConditions *conditions.PositiveConditionBuilder,
	arm ARM,
	cfg outoftree.ConfigValues,
) ([]*registration.StorageType, error) {
	types := getKnownStorageTypes()
	extensions, err := resourceExtensions(scheme)
	if err != nil {
		return nil, err
	}
	for _, t := range types {
		gvk, err := apiutil.GVKForObject(t.Obj, scheme)
		if err != nil {
			return nil, fmt.Errorf("creating GVK for %T: %w", t.Obj, err)
		}
		t.Name = "hfl_" + controllerName(gvk)
		t.Predicate = standardPredicate()
		t.Reconciler = outoftree.NewAzureDeploymentReconciler(
			arm.ConnectionFactory,
			kubeClient,
			resolver,
			positiveConditions,
			arm.ExpressionEvaluator,
			cfg,
			extensions[gvk],
		)
	}
	return types, nil
}

// controllerName is ASO's getControllerName: the group without dots, an
// underscore and the lower-case kind.
func controllerName(gvk schema.GroupVersionKind) string {
	group := strings.TrimSuffix(gvk.Group, ".azure.hfl.dev")
	return fmt.Sprintf("%s_%s", strings.ReplaceAll(group, ".", ""), strings.ToLower(gvk.Kind))
}

// resourceExtensions is ASO's GetResourceExtensions: the extension of each
// generated resource version, by its GVK.
func resourceExtensions(scheme *runtime.Scheme) (map[schema.GroupVersionKind]genruntime.ResourceExtension, error) {
	result := make(map[schema.GroupVersionKind]genruntime.ResourceExtension)
	for _, extension := range getResourceExtensions() {
		for _, resource := range extension.GetExtendedResources() {
			obj, ok := resource.(runtime.Object)
			if !ok {
				return nil, fmt.Errorf("unexpected resource type for resource %q, found %T", resource.AzureName(), resource)
			}
			gvk, err := apiutil.GVKForObject(obj, scheme)
			if err != nil {
				return nil, err
			}
			result[gvk] = extension
		}
	}
	return result, nil
}

// standardPredicate is ASO's makeStandardPredicate.
func standardPredicate() predicate.Predicate {
	// Note: These predicates prevent status updates from triggering a reconcile.
	return predicate.Or(
		predicate.GenerationChangedPredicate{},
		outoftree.ARMReconcilerAnnotationChangedPredicate(),
		outoftree.ARMPerResourceSecretAnnotationChangedPredicate(),
		// Ours: a slot takeover added to a refused object (pkg/slot).
		slot.TakeoverChanged(),
	)
}

func indexPostgreSQLUserPassword(rawObj client.Object) []string {
	obj, ok := rawObj.(*postgresqlv1.User)
	if !ok || obj.Spec.LocalUser == nil || obj.Spec.LocalUser.Password == nil {
		return nil
	}
	return []string{obj.Spec.LocalUser.Password.Name}
}

func indexAzureSQLUserPassword(rawObj client.Object) []string {
	obj, ok := rawObj.(*sqlv1.User)
	if !ok || obj.Spec.LocalUser == nil || obj.Spec.LocalUser.Password == nil {
		return nil
	}
	return []string{obj.Spec.LocalUser.Password.Name}
}

func indexVaultSecretValue(rawObj client.Object) []string {
	obj, ok := rawObj.(*keyvaultv1.VaultSecret)
	if !ok || obj.Spec.Value == nil {
		return nil
	}
	return []string{obj.Spec.Value.Name}
}

func indexVaultCertificateCertificate(rawObj client.Object) []string {
	obj, ok := rawObj.(*keyvaultv1.VaultCertificate)
	if !ok || obj.Spec.Certificate == nil {
		return nil
	}
	return []string{obj.Spec.Certificate.Name}
}

func indexVaultCertificatePrivateKey(rawObj client.Object) []string {
	obj, ok := rawObj.(*keyvaultv1.VaultCertificate)
	if !ok || obj.Spec.PrivateKey == nil {
		return nil
	}
	return []string{obj.Spec.PrivateKey.Name}
}

// indexAppSettingsSecrets and the three below index the Secrets and
// ConfigMaps an App Service config section reads, so a change to one
// reconciles the section again.
func indexAppSettingsSecrets(rawObj client.Object) []string {
	obj, ok := rawObj.(*webv1.SitesConfigAppSettings)
	if !ok {
		return nil
	}
	var out []string
	for _, s := range obj.Spec.Settings {
		if s.ValueFromSecret != nil {
			out = append(out, s.ValueFromSecret.Name)
		}
	}
	return out
}

func indexAppSettingsConfigMaps(rawObj client.Object) []string {
	obj, ok := rawObj.(*webv1.SitesConfigAppSettings)
	if !ok {
		return nil
	}
	var out []string
	for _, s := range obj.Spec.Settings {
		if s.ValueFromConfig != nil {
			out = append(out, s.ValueFromConfig.Name)
		}
	}
	return out
}

func indexConnectionStringsSecrets(rawObj client.Object) []string {
	obj, ok := rawObj.(*webv1.SitesConfigConnectionStrings)
	if !ok {
		return nil
	}
	var out []string
	for _, s := range obj.Spec.ConnectionStrings {
		if s.ValueFromSecret != nil {
			out = append(out, s.ValueFromSecret.Name)
		}
	}
	return out
}

func indexConnectionStringsConfigMaps(rawObj client.Object) []string {
	obj, ok := rawObj.(*webv1.SitesConfigConnectionStrings)
	if !ok {
		return nil
	}
	var out []string
	for _, s := range obj.Spec.ConnectionStrings {
		if s.ValueFromConfig != nil {
			out = append(out, s.ValueFromConfig.Name)
		}
	}
	return out
}

// watchSecretsFactory is ASO's: enqueue the objects whose indexed fields
// name a Secret that changed.
func watchSecretsFactory(keys []string, objList client.ObjectList) registration.EventHandlerFactory {
	return func(c client.Client, log logr.Logger) handler.EventHandler {
		return handler.EnqueueRequestsFromMapFunc(watchEntity(c, log, keys, objList, &corev1.Secret{}))
	}
}

// watchConfigMapsFactory is ASO's: the same for ConfigMaps.
func watchConfigMapsFactory(keys []string, objList client.ObjectList) registration.EventHandlerFactory {
	return func(c client.Client, log logr.Logger) handler.EventHandler {
		return handler.EnqueueRequestsFromMapFunc(watchEntity(c, log, keys, objList, &corev1.ConfigMap{}))
	}
}

func watchEntity(c client.Client, log logr.Logger, keys []string, objList client.ObjectList, entity client.Object) handler.MapFunc {
	listType := reflect.TypeOf(objList).Elem()

	return func(ctx context.Context, o client.Object) []reconcile.Request {
		if reflect.TypeOf(o) != reflect.TypeOf(entity) {
			log.V(0).Info("Unexpected watch entity type", "namespace", o.GetNamespace(), "name", o.GetName(),
				"actual", fmt.Sprintf("%T", o), "expected", fmt.Sprintf("%T", entity))
			return nil
		}

		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		var result []reconcile.Request
		for _, key := range keys {
			list, ok := reflect.New(listType).Interface().(client.ObjectList)
			if !ok {
				continue
			}
			if err := c.List(ctx, list, client.MatchingFields{key: o.GetName()}, client.InNamespace(o.GetNamespace())); err != nil {
				log.Error(err, "couldn't list resources using secret", "field", key, "value", o.GetName())
				continue
			}
			items, err := apimeta.ExtractList(list)
			if err != nil {
				log.Error(err, "failed to get list items")
				continue
			}
			for _, item := range items {
				m, err := apimeta.Accessor(item)
				if err != nil {
					continue
				}
				result = append(result, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: m.GetNamespace(), Name: m.GetName()}})
			}
		}
		return result
	}
}
