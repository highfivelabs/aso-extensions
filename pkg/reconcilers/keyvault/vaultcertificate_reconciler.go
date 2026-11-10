// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package keyvault

import (
	"context"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/go-logr/logr"
	"github.com/rotisserie/eris"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/Azure/azure-service-operator/v2/pkg/common/annotations"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/core"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	kvv1 "github.com/highfivelabs/aso-extensions/apis/keyvault/v1"
	"github.com/highfivelabs/aso-extensions/pkg/keyvault"
)

var _ genruntime.Reconciler = &VaultCertificateReconciler{}

// VaultCertificateReconciler imports a certificate and its private key from
// Kubernetes Secrets into a Key Vault, as VaultSecretReconciler sets a
// secret: the same owner, credential, create and delete modes.
type VaultCertificateReconciler struct {
	outoftree.ARMOwnedResourceReconcilerCommon
	ResourceResolver   *outoftree.Resolver
	CredentialProvider outoftree.CredentialProvider
	// ClientOptions for the data plane client; zero in production.
	ClientOptions azcore.ClientOptions
}

func NewVaultCertificateReconciler(
	kubeClient outoftree.KubeClient,
	resourceResolver *outoftree.Resolver,
	positiveConditions *conditions.PositiveConditionBuilder,
	credentialProvider outoftree.CredentialProvider,
) *VaultCertificateReconciler {
	return &VaultCertificateReconciler{
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

func asVaultCertificate(obj genruntime.MetaObject) (*kvv1.VaultCertificate, error) {
	c, ok := obj.(*kvv1.VaultCertificate)
	if !ok {
		return nil, eris.Errorf("cannot modify resource that is not of type *VaultCertificate. Type is %T", obj)
	}
	return c, nil
}

func (r *VaultCertificateReconciler) client(ctx context.Context, c *kvv1.VaultCertificate) (*keyvault.Client, error) {
	return vaultClient(ctx, r.ResourceResolver, r.CredentialProvider, c, r.ClientOptions)
}

// pem reads the certificate and key from their Secrets and checks they
// make a pair.
func (r *VaultCertificateReconciler) pem(ctx context.Context, c *kvv1.VaultCertificate) (*keyvault.ParsedPEM, error) {
	secrets, err := r.ResourceResolver.ResolveResourceSecretReferences(ctx, c)
	if err != nil {
		return nil, outoftree.ClassifyResolverError(err)
	}
	certPEM, err := secrets.LookupFromPtr(c.Spec.Certificate)
	if err != nil {
		err = eris.Wrap(err, "failed to look up .spec.certificate")
		return nil, conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityWarning, conditions.ReasonSecretNotFound)
	}
	keyPEM, err := secrets.LookupFromPtr(c.Spec.PrivateKey)
	if err != nil {
		err = eris.Wrap(err, "failed to look up .spec.privateKey")
		return nil, conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityWarning, conditions.ReasonSecretNotFound)
	}
	parsed, err := keyvault.ParsePEM(certPEM, keyPEM)
	if err != nil {
		return nil, conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityError, conditions.ReasonFailed)
	}
	return parsed, nil
}

func (r *VaultCertificateReconciler) CreateOrUpdate(
	ctx context.Context,
	log logr.Logger,
	_ record.EventRecorder,
	obj genruntime.MetaObject,
	_ annotations.ResolvedReconcilePolicies,
) (ctrl.Result, error) {
	c, err := asVaultCertificate(obj)
	if err != nil {
		return ctrl.Result{}, err
	}
	log = log.WithValues("azureName", c.AzureName())

	parsed, err := r.pem(ctx, c)
	if err != nil {
		return ctrl.Result{}, err
	}
	client, err := r.client(ctx, c)
	if err != nil {
		return ctrl.Result{}, err
	}
	name := c.AzureName()
	enabled := boolOf(c.Spec.Enabled, true)

	current, err := client.GetCertificate(ctx, name)
	if keyvault.IsNotFound(err) {
		return r.create(ctx, log, client, c, parsed)
	}
	if err != nil {
		return ctrl.Result{}, eris.Wrapf(err, "reading certificate %s", name)
	}

	if strings.TrimRight(current.X5T, "=") != parsed.Thumbprint {
		log.V(0).Info("Importing a new version of the certificate", "thumbprint", parsed.Thumbprint)
		if _, err := client.ImportCertificate(ctx, name, importRequest(c, parsed)); err != nil {
			return ctrl.Result{}, eris.Wrapf(err, "importing certificate %s", name)
		}
	} else if !certificateAttributesMatch(current, enabled, c.Spec.Tags) {
		log.V(0).Info("Updating the certificate's attributes")
		update := keyvault.CertificateUpdate{Attributes: &keyvault.Attributes{Enabled: &enabled}, Tags: tagsOrEmpty(c.Spec.Tags)}
		if _, err := client.UpdateCertificate(ctx, name, current.Version(), update); err != nil {
			return ctrl.Result{}, eris.Wrapf(err, "updating certificate %s", name)
		}
	}
	return ctrl.Result{}, r.writeStatus(ctx, client, c)
}

func importRequest(c *kvv1.VaultCertificate, parsed *keyvault.ParsedPEM) keyvault.ImportRequest {
	enabled := boolOf(c.Spec.Enabled, true)
	return keyvault.ImportRequest{
		Value:      parsed.PEM,
		Policy:     &keyvault.ImportPolicy{SecretProperties: &keyvault.SecretProperties{ContentType: keyvault.PEMContentType}},
		Attributes: &keyvault.Attributes{Enabled: &enabled},
		Tags:       c.Spec.Tags,
	}
}

// tagsOrEmpty sends an empty map rather than nothing, so removing every tag
// clears them.
func tagsOrEmpty(tags map[string]string) map[string]string {
	if tags == nil {
		return map[string]string{}
	}
	return tags
}

func certificateAttributesMatch(current *keyvault.Certificate, enabled bool, tags map[string]string) bool {
	a := current.Attributes
	if a == nil {
		a = &keyvault.Attributes{}
	}
	if boolOf(a.Enabled, true) != enabled || len(current.Tags) != len(tags) {
		return false
	}
	for k, v := range tags {
		if current.Tags[k] != v {
			return false
		}
	}
	return true
}

func (r *VaultCertificateReconciler) create(ctx context.Context, log logr.Logger, client *keyvault.Client, c *kvv1.VaultCertificate, parsed *keyvault.ParsedPEM) (ctrl.Result, error) {
	name := c.AzureName()
	mode := CreateModeDefault
	if c.Spec.CreateMode != nil {
		mode = *c.Spec.CreateMode
	}

	deleted := false
	if mode != CreateModeDefault {
		_, err := client.GetDeletedCertificate(ctx, name)
		switch {
		case err == nil:
			deleted = true
		case keyvault.IsNotFound(err):
		default:
			return ctrl.Result{}, eris.Wrapf(err, "reading deleted certificate %s", name)
		}
	}

	switch {
	case deleted && (mode == CreateModeRecover || mode == CreateModeCreateOrRecover):
		log.V(0).Info("Recovering the soft-deleted certificate")
		if _, err := client.RecoverCertificate(ctx, name); err != nil {
			return ctrl.Result{}, eris.Wrapf(err, "recovering certificate %s", name)
		}
		// Recovery is asynchronous; the next pass imports if the certificate differs.
		return ctrl.Result{RequeueAfter: softDeleteWait}, nil
	case mode == CreateModeRecover:
		err := eris.Errorf("createMode is recover but no soft-deleted certificate %s exists", name)
		return ctrl.Result{}, conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityError, conditions.ReasonFailed)
	case deleted && mode == CreateModePurgeThenCreate:
		log.V(0).Info("Purging the soft-deleted certificate")
		if err := client.PurgeCertificate(ctx, name); err != nil {
			return ctrl.Result{}, eris.Wrapf(err, "purging certificate %s", name)
		}
	}

	log.V(0).Info("Importing the certificate", "thumbprint", parsed.Thumbprint)
	if _, err := client.ImportCertificate(ctx, name, importRequest(c, parsed)); err != nil {
		if keyvault.IsDeletedButRecoverable(err) {
			err = eris.Wrapf(err, "a soft-deleted certificate %s holds the name; set createMode to recover, createOrRecover or purgeThenCreate", name)
			return ctrl.Result{}, conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityError, conditions.ReasonFailed)
		}
		return ctrl.Result{}, eris.Wrapf(err, "importing certificate %s", name)
	}
	return ctrl.Result{}, r.writeStatus(ctx, client, c)
}

func (r *VaultCertificateReconciler) Delete(ctx context.Context, log logr.Logger, _ record.EventRecorder, obj genruntime.MetaObject) (ctrl.Result, error) {
	c, err := asVaultCertificate(obj)
	if err != nil {
		return ctrl.Result{}, err
	}
	mode := DeleteModeDetach
	if c.Spec.DeleteMode != nil {
		mode = *c.Spec.DeleteMode
	}
	if mode == DeleteModeDetach {
		log.V(0).Info("deleteMode is detach; leaving the certificate in the vault", "azureName", c.AzureName())
		return ctrl.Result{}, nil
	}

	if _, err := r.ResourceResolver.ResolveOwner(ctx, c); err != nil {
		var notFound *core.ReferenceNotFound
		if eris.As(err, &notFound) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	client, err := r.client(ctx, c)
	if err != nil {
		return ctrl.Result{}, err
	}
	name := c.AzureName()

	switch mode {
	case DeleteModeDelete:
		if err := client.DeleteCertificate(ctx, name); err != nil && !keyvault.IsNotFound(err) {
			return ctrl.Result{}, eris.Wrapf(err, "deleting certificate %s", name)
		}
	case DeleteModeDisable:
		current, err := client.GetCertificate(ctx, name)
		if keyvault.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		if err != nil {
			return ctrl.Result{}, eris.Wrapf(err, "reading certificate %s", name)
		}
		disabled := false
		update := keyvault.CertificateUpdate{Attributes: &keyvault.Attributes{Enabled: &disabled}, Tags: current.Tags}
		if _, err := client.UpdateCertificate(ctx, name, current.Version(), update); err != nil {
			return ctrl.Result{}, eris.Wrapf(err, "disabling certificate %s", name)
		}
	}
	return ctrl.Result{}, nil
}

func (r *VaultCertificateReconciler) Claim(ctx context.Context, log logr.Logger, _ record.EventRecorder, obj genruntime.MetaObject) error {
	c, err := asVaultCertificate(obj)
	if err != nil {
		return err
	}
	return r.ClaimResource(ctx, log, c)
}

func (r *VaultCertificateReconciler) UpdateStatus(ctx context.Context, _ logr.Logger, _ record.EventRecorder, obj genruntime.MetaObject, _ annotations.ResolvedReconcilePolicies) error {
	c, err := asVaultCertificate(obj)
	if err != nil {
		return err
	}
	client, err := r.client(ctx, c)
	if err != nil {
		return err
	}
	return r.writeStatus(ctx, client, c)
}

// writeStatus reads the certificate's current version into status.
func (r *VaultCertificateReconciler) writeStatus(ctx context.Context, client *keyvault.Client, c *kvv1.VaultCertificate) error {
	current, err := client.GetCertificate(ctx, c.AzureName())
	if keyvault.IsNotFound(err) {
		err = eris.Errorf("certificate %s does not exist", c.AzureName())
		return conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityWarning, conditions.ReasonAzureResourceNotFound)
	}
	if err != nil {
		return err
	}
	c.Status.Id = current.ID
	c.Status.Version = current.Version()
	c.Status.SecretId = current.SID
	c.Status.Thumbprint = current.ThumbprintHex()
	c.Status.Tags = current.Tags
	c.Status.Enabled, c.Status.NotBefore, c.Status.ExpiresOn, c.Status.RecoveryLevel = nil, nil, nil, ""
	if a := current.Attributes; a != nil {
		c.Status.Enabled = a.Enabled
		c.Status.NotBefore = timeOf(a.NotBefore)
		c.Status.ExpiresOn = timeOf(a.Expires)
		c.Status.RecoveryLevel = a.RecoveryLevel
	}
	return nil
}

// ci-selective-demo: a throwaway change to show the selective path (not to merge).
