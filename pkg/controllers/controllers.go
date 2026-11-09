/*
Copyright (c) Microsoft Corporation.
Licensed under the MIT license.
*/

// Modified by High Five Labs: the storage-type registration,
// predicate, indexers and secret watches follow Azure Service Operator's
// v2/internal/controllers/controller_resources.go and indexers.go
// (GetKnownStorageTypes, makeStandardPredicate, watchSecretsFactory,
// watchEntity, indexPostgreSQLUserPassword), for our kinds only.
// watchEntity lists with apimeta.ExtractList instead of ASO's internal
// reflecthelpers.

// Package controllers registers our kinds on ASO's generic
// reconciler.
package controllers

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	asopostgresql "github.com/Azure/azure-service-operator/v2/api/dbforpostgresql/v20250801/storage"
	asokeyvault "github.com/Azure/azure-service-operator/v2/api/keyvault/v1api20230701/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/registration"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	authorizationv1 "github.com/highfivelabs/aso-extensions/apis/authorization/v1"
	postgresqlv1 "github.com/highfivelabs/aso-extensions/apis/dbforpostgresql/v1"
	keyvaultv1 "github.com/highfivelabs/aso-extensions/apis/keyvault/v1"
	authreconciler "github.com/highfivelabs/aso-extensions/pkg/reconcilers/authorization"
	kvreconciler "github.com/highfivelabs/aso-extensions/pkg/reconcilers/keyvault"
	pgreconciler "github.com/highfivelabs/aso-extensions/pkg/reconcilers/postgresql"
)

// CreateScheme holds our kinds, the ASO owner kinds they resolve
// (storage versions only), and Kubernetes' own. A ManagementLock's owner,
// any ASO kind, is read as an unstructured object instead.
func CreateScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = postgresqlv1.AddToScheme(scheme)
	_ = keyvaultv1.AddToScheme(scheme)
	_ = authorizationv1.AddToScheme(scheme)
	_ = asopostgresql.AddToScheme(scheme)
	_ = asokeyvault.AddToScheme(scheme)
	return scheme
}

// OwnerTypes are the ASO kinds our kinds name as owners. They are indexed
// in the resolver so it can read them, but not reconciled here: ASO does.
func OwnerTypes() []*registration.StorageType {
	return []*registration.StorageType{
		{Obj: &asopostgresql.FlexibleServer{}, Name: "aso_postgresql_flexibleserver"},
		{Obj: &asokeyvault.Vault{}, Name: "aso_keyvault_vault"},
	}
}

// Reconcilers lets tests replace a reconciler's settings.
type Reconcilers struct {
	PostgreSQLUser   *pgreconciler.PostgreSQLUserReconciler
	VaultSecret      *kvreconciler.VaultSecretReconciler
	VaultCertificate *kvreconciler.VaultCertificateReconciler
	ManagementLock   *authreconciler.ManagementLockReconciler
}

// StorageTypes are our kinds with their reconcilers, as ASO's
// GetKnownStorageTypes builds its own.
func StorageTypes(
	kubeClient outoftree.KubeClient,
	resolver *outoftree.Resolver,
	positiveConditions *conditions.PositiveConditionBuilder,
	credentialProvider outoftree.CredentialProvider,
	cfg outoftree.ConfigValues,
) ([]*registration.StorageType, *Reconcilers) {
	rs := &Reconcilers{
		PostgreSQLUser:   pgreconciler.NewPostgreSQLUserReconciler(kubeClient, resolver, positiveConditions, credentialProvider, cfg),
		VaultSecret:      kvreconciler.NewVaultSecretReconciler(kubeClient, resolver, positiveConditions, credentialProvider),
		VaultCertificate: kvreconciler.NewVaultCertificateReconciler(kubeClient, resolver, positiveConditions, credentialProvider),
		ManagementLock:   authreconciler.NewManagementLockReconciler(kubeClient, positiveConditions, credentialProvider, cfg),
	}
	return []*registration.StorageType{
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
			Obj:        &authorizationv1.ManagementLock{},
			Name:       "hfl_authorization_managementlock",
			Reconciler: rs.ManagementLock,
			Predicate:  standardPredicate(),
		},
	}, rs
}

// standardPredicate is ASO's makeStandardPredicate.
func standardPredicate() predicate.Predicate {
	// Note: These predicates prevent status updates from triggering a reconcile.
	return predicate.Or(
		predicate.GenerationChangedPredicate{},
		outoftree.ARMReconcilerAnnotationChangedPredicate(),
		outoftree.ARMPerResourceSecretAnnotationChangedPredicate(),
	)
}

func indexPostgreSQLUserPassword(rawObj client.Object) []string {
	obj, ok := rawObj.(*postgresqlv1.User)
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

// watchSecretsFactory is ASO's: enqueue the objects whose indexed fields
// name a Secret that changed.
func watchSecretsFactory(keys []string, objList client.ObjectList) registration.EventHandlerFactory {
	return func(c client.Client, log logr.Logger) handler.EventHandler {
		return handler.EnqueueRequestsFromMapFunc(watchEntity(c, log, keys, objList, &corev1.Secret{}))
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
