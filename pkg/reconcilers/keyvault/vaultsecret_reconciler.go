// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Package keyvault reconciles VaultSecret: a Key Vault secret whose value
// comes from a Kubernetes Secret. It runs on ASO's generic reconciler, so
// it honours ASO's annotations (reconcile-policy, credential-from) and
// conditions, and it signs in to the vault with the credential ASO's
// credential provider picks for the object. Its shape follows ASO's
// hand-written PostgreSQL user reconciler; its create and delete modes are
// ASO's VaultKey's.
package keyvault

import (
	"context"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/go-logr/logr"
	"github.com/rotisserie/eris"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlconversion "sigs.k8s.io/controller-runtime/pkg/conversion"

	asokeyvault "github.com/Azure/azure-service-operator/v2/api/keyvault/v1api20230701/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/common/annotations"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/core"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	kvv1 "github.com/highfivelabs/aso-extensions/apis/keyvault/v1"
	"github.com/highfivelabs/aso-extensions/pkg/keyvault"
)

const (
	CreateModeDefault         = "default"
	CreateModeRecover         = "recover"
	CreateModeCreateOrRecover = "createOrRecover"
	CreateModePurgeThenCreate = "purgeThenCreate"

	DeleteModeDetach  = "detach"
	DeleteModeDelete  = "delete"
	DeleteModeDisable = "disable"
)

// How long to wait for Key Vault's asynchronous recover and purge.
var softDeleteWait = 5 * time.Second

var _ genruntime.Reconciler = &VaultSecretReconciler{}

type VaultSecretReconciler struct {
	outoftree.ARMOwnedResourceReconcilerCommon
	ResourceResolver   *outoftree.Resolver
	CredentialProvider outoftree.CredentialProvider
	// ClientOptions for the data plane client; zero in production. Tests set
	// a transport that trusts the fake vault's certificate.
	ClientOptions azcore.ClientOptions
}

func NewVaultSecretReconciler(
	kubeClient outoftree.KubeClient,
	resourceResolver *outoftree.Resolver,
	positiveConditions *conditions.PositiveConditionBuilder,
	credentialProvider outoftree.CredentialProvider,
) *VaultSecretReconciler {
	return &VaultSecretReconciler{
		ResourceResolver:   resourceResolver,
		CredentialProvider: credentialProvider,
		ARMOwnedResourceReconcilerCommon: outoftree.ARMOwnedResourceReconcilerCommon{
			ResourceResolver: resourceResolver,
			ReconcilerCommon: outoftree.ReconcilerCommon{
				KubeClient:         kubeClient,
				PositiveConditions: positiveConditions,
			},
		},
	}
}

func asVaultSecret(obj genruntime.MetaObject) (*kvv1.VaultSecret, error) {
	s, ok := obj.(*kvv1.VaultSecret)
	if !ok {
		return nil, eris.Errorf("cannot modify resource that is not of type *VaultSecret. Type is %T", obj)
	}
	return s, nil
}

func (r *VaultSecretReconciler) CreateOrUpdate(
	ctx context.Context,
	log logr.Logger,
	_ record.EventRecorder,
	obj genruntime.MetaObject,
	_ annotations.ResolvedReconcilePolicies,
) (ctrl.Result, error) {
	s, err := asVaultSecret(obj)
	if err != nil {
		return ctrl.Result{}, err
	}
	log = log.WithValues("azureName", s.AzureName())

	secrets, err := r.ResourceResolver.ResolveResourceSecretReferences(ctx, s)
	if err != nil {
		return ctrl.Result{}, outoftree.ClassifyResolverError(err)
	}
	value, err := secrets.LookupFromPtr(s.Spec.Value)
	if err != nil {
		err = eris.Wrap(err, "failed to look up .spec.value")
		return ctrl.Result{}, conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityWarning, conditions.ReasonSecretNotFound)
	}

	client, err := r.client(ctx, s)
	if err != nil {
		return ctrl.Result{}, err
	}
	name := s.AzureName()
	desired := desiredSecret(s, value)

	current, err := client.Get(ctx, name)
	if keyvault.IsNotFound(err) {
		return r.create(ctx, log, client, s, desired)
	}
	if err != nil {
		return ctrl.Result{}, eris.Wrapf(err, "reading secret %s", name)
	}

	if current.Value == nil || *current.Value != value || stringOf(current.ContentType) != s.Spec.ContentType {
		log.V(0).Info("Setting a new version of the secret")
		if _, err := client.Set(ctx, name, desired); err != nil {
			return ctrl.Result{}, eris.Wrapf(err, "setting secret %s", name)
		}
	} else if !attributesMatch(current, desired) {
		log.V(0).Info("Updating the secret's attributes")
		if _, err := client.Update(ctx, name, current.Version(), desired); err != nil {
			return ctrl.Result{}, eris.Wrapf(err, "updating secret %s", name)
		}
	}
	// ASO's generic reconciler commits the object after CreateOrUpdate
	// (UpdateStatus runs only when the reconcile policy skips), so the
	// status is written here.
	return ctrl.Result{}, r.writeStatus(ctx, client, s)
}

func (r *VaultSecretReconciler) create(ctx context.Context, log logr.Logger, client *keyvault.Client, s *kvv1.VaultSecret, desired keyvault.Secret) (ctrl.Result, error) {
	name := s.AzureName()
	mode := CreateModeDefault
	if s.Spec.CreateMode != nil {
		mode = *s.Spec.CreateMode
	}

	deleted := false
	if mode != CreateModeDefault {
		_, err := client.GetDeleted(ctx, name)
		switch {
		case err == nil:
			deleted = true
		case keyvault.IsNotFound(err):
		default:
			return ctrl.Result{}, eris.Wrapf(err, "reading deleted secret %s", name)
		}
	}

	switch {
	case deleted && (mode == CreateModeRecover || mode == CreateModeCreateOrRecover):
		log.V(0).Info("Recovering the soft-deleted secret")
		if _, err := client.Recover(ctx, name); err != nil {
			return ctrl.Result{}, eris.Wrapf(err, "recovering secret %s", name)
		}
		// Recovery is asynchronous; the next pass sets the value if it differs.
		return ctrl.Result{RequeueAfter: softDeleteWait}, nil
	case mode == CreateModeRecover:
		err := eris.Errorf("createMode is recover but no soft-deleted secret %s exists", name)
		return ctrl.Result{}, conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityError, conditions.ReasonFailed)
	case deleted && mode == CreateModePurgeThenCreate:
		log.V(0).Info("Purging the soft-deleted secret")
		if err := client.Purge(ctx, name); err != nil {
			return ctrl.Result{}, eris.Wrapf(err, "purging secret %s", name)
		}
	}

	log.V(0).Info("Creating the secret")
	if _, err := client.Set(ctx, name, desired); err != nil {
		if keyvault.IsDeletedButRecoverable(err) {
			err = eris.Wrapf(err, "a soft-deleted secret %s holds the name; set createMode to recover, createOrRecover or purgeThenCreate", name)
			return ctrl.Result{}, conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityError, conditions.ReasonFailed)
		}
		return ctrl.Result{}, eris.Wrapf(err, "creating secret %s", name)
	}
	return ctrl.Result{}, r.writeStatus(ctx, client, s)
}

func (r *VaultSecretReconciler) Delete(ctx context.Context, log logr.Logger, _ record.EventRecorder, obj genruntime.MetaObject) (ctrl.Result, error) {
	s, err := asVaultSecret(obj)
	if err != nil {
		return ctrl.Result{}, err
	}
	mode := DeleteModeDetach
	if s.Spec.DeleteMode != nil {
		mode = *s.Spec.DeleteMode
	}
	if mode == DeleteModeDetach {
		log.V(0).Info("deleteMode is detach; leaving the secret in the vault", "azureName", s.AzureName())
		return ctrl.Result{}, nil
	}

	_, err = r.ResourceResolver.ResolveOwner(ctx, s)
	if err != nil {
		var notFound *core.ReferenceNotFound
		if eris.As(err, &notFound) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	client, err := r.client(ctx, s)
	if err != nil {
		return ctrl.Result{}, err
	}
	name := s.AzureName()

	switch mode {
	case DeleteModeDelete:
		if err := client.Delete(ctx, name); err != nil && !keyvault.IsNotFound(err) {
			return ctrl.Result{}, eris.Wrapf(err, "deleting secret %s", name)
		}
	case DeleteModeDisable:
		current, err := client.Get(ctx, name)
		if keyvault.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		if err != nil {
			return ctrl.Result{}, eris.Wrapf(err, "reading secret %s", name)
		}
		disabled := false
		update := keyvault.Secret{ContentType: current.ContentType, Attributes: &keyvault.Attributes{Enabled: &disabled}, Tags: current.Tags}
		if _, err := client.Update(ctx, name, current.Version(), update); err != nil {
			return ctrl.Result{}, eris.Wrapf(err, "disabling secret %s", name)
		}
	}
	return ctrl.Result{}, nil
}

func (r *VaultSecretReconciler) Claim(ctx context.Context, log logr.Logger, _ record.EventRecorder, obj genruntime.MetaObject) error {
	s, err := asVaultSecret(obj)
	if err != nil {
		return err
	}
	return r.ClaimResource(ctx, log, s)
}

func (r *VaultSecretReconciler) UpdateStatus(ctx context.Context, _ logr.Logger, _ record.EventRecorder, obj genruntime.MetaObject, _ annotations.ResolvedReconcilePolicies) error {
	s, err := asVaultSecret(obj)
	if err != nil {
		return err
	}
	client, err := r.client(ctx, s)
	if err != nil {
		return err
	}
	return r.writeStatus(ctx, client, s)
}

// writeStatus reads the secret's current version into status.
func (r *VaultSecretReconciler) writeStatus(ctx context.Context, client *keyvault.Client, s *kvv1.VaultSecret) error {
	current, err := client.Get(ctx, s.AzureName())
	if keyvault.IsNotFound(err) {
		err = eris.Errorf("secret %s does not exist", s.AzureName())
		return conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityWarning, conditions.ReasonAzureResourceNotFound)
	}
	if err != nil {
		return err
	}

	s.Status.Id = current.ID
	s.Status.Version = current.Version()
	s.Status.ContentType = stringOf(current.ContentType)
	s.Status.Tags = current.Tags
	s.Status.Enabled, s.Status.NotBefore, s.Status.ExpiresOn, s.Status.RecoveryLevel = nil, nil, nil, ""
	if a := current.Attributes; a != nil {
		s.Status.Enabled = a.Enabled
		s.Status.NotBefore = timeOf(a.NotBefore)
		s.Status.ExpiresOn = timeOf(a.Expires)
		s.Status.RecoveryLevel = a.RecoveryLevel
	}
	return nil
}

// client resolves the owning Vault's URI and the credential ASO would use
// for this object.
func (r *VaultSecretReconciler) client(ctx context.Context, s *kvv1.VaultSecret) (*keyvault.Client, error) {
	return vaultClient(ctx, r.ResourceResolver, r.CredentialProvider, s, r.ClientOptions)
}

// vaultClient is a data plane client for the Vault that owns obj, with the
// credential ASO would use for obj.
func vaultClient(
	ctx context.Context,
	resolver *outoftree.Resolver,
	credentialProvider outoftree.CredentialProvider,
	obj genruntime.ARMOwnedMetaObject,
	opts azcore.ClientOptions,
) (*keyvault.Client, error) {
	ownerDetails, err := resolver.ResolveOwner(ctx, obj)
	if err != nil {
		return nil, eris.Wrapf(err, "resolving owner for %s", obj.GetName())
	}
	if !ownerDetails.FoundKubernetesOwner() {
		return nil, eris.Errorf("the owner must exist in Kubernetes for %s", obj.GetName())
	}
	vault, ok := ownerDetails.Owner.(*asokeyvault.Vault)
	if !ok {
		return nil, eris.Errorf("owner was not type Vault, instead: %T", ownerDetails.Owner)
	}
	// If this doesn't compile, update the version being imported to the new Hub version.
	var _ ctrlconversion.Hub = &asokeyvault.Vault{}

	if vault.Status.Properties == nil || vault.Status.Properties.VaultUri == nil {
		err := eris.Errorf("owning Vault %q '.status.properties.vaultUri' not set. Has the vault been provisioned successfully?", vault.Name)
		return nil, conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityWarning, conditions.ReasonWaitingForOwner)
	}

	credential, err := credentialProvider.GetCredential(ctx, obj)
	if err != nil {
		return nil, eris.Wrap(err, "failed to get credential")
	}
	return keyvault.New(*vault.Status.Properties.VaultUri, credential.TokenCredential(), &opts)
}

func desiredSecret(s *kvv1.VaultSecret, value string) keyvault.Secret {
	enabled := true
	if s.Spec.Enabled != nil {
		enabled = *s.Spec.Enabled
	}
	out := keyvault.Secret{
		Value:      &value,
		Attributes: &keyvault.Attributes{Enabled: &enabled},
		Tags:       s.Spec.Tags,
	}
	if s.Spec.ContentType != "" {
		out.ContentType = &s.Spec.ContentType
	}
	if s.Spec.NotBefore != nil {
		out.Attributes.NotBefore = keyvault.UnixTime(&s.Spec.NotBefore.Time)
	}
	if s.Spec.ExpiresOn != nil {
		out.Attributes.Expires = keyvault.UnixTime(&s.Spec.ExpiresOn.Time)
	}
	return out
}

func attributesMatch(current *keyvault.Secret, desired keyvault.Secret) bool {
	a := current.Attributes
	if a == nil {
		a = &keyvault.Attributes{}
	}
	d := desired.Attributes
	if boolOf(a.Enabled, true) != boolOf(d.Enabled, true) ||
		!int64PtrEqual(a.NotBefore, d.NotBefore) || !int64PtrEqual(a.Expires, d.Expires) {
		return false
	}
	if len(current.Tags) != len(desired.Tags) {
		return false
	}
	for k, v := range desired.Tags {
		if current.Tags[k] != v {
			return false
		}
	}
	return true
}

func int64PtrEqual(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func boolOf(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

func stringOf(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func timeOf(p *int64) *metav1.Time {
	if p == nil {
		return nil
	}
	t := metav1.NewTime(time.Unix(*p, 0).UTC())
	return &t
}
