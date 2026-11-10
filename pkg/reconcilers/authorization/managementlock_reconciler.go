// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Package authorization reconciles ManagementLock: an Azure management lock
// (Microsoft.Authorization/locks) on whatever its owner is in Azure. It runs
// on ASO's generic reconciler, so it honours ASO's annotations
// (reconcile-policy, credential-from) and conditions, and it calls Resource
// Manager with the credential ASO's credential provider picks for the
// object. Claiming follows ASO's ARMOwnedResourceReconcilerCommon (wait for
// a Ready owner, then set the owner reference); the subscription check is
// ASO's checkSubscription. The owner is read as an unstructured object, so
// any ASO kind can own a lock without the manager knowing its Go type.
package authorization

import (
	"context"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/go-logr/logr"
	"github.com/rotisserie/eris"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/common/annotations"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	authv1 "github.com/highfivelabs/aso-extensions/apis/authorization/v1"
	"github.com/highfivelabs/aso-extensions/pkg/armlocks"
)

var _ genruntime.Reconciler = &ManagementLockReconciler{}

type ManagementLockReconciler struct {
	outoftree.ReconcilerCommon
	CredentialProvider outoftree.CredentialProvider
	// Cloud is the Resource Manager endpoint and audience from ASO's
	// settings, used when a credential carries none of its own.
	Cloud cloud.Configuration
	// OwnerReader reads owners straight from the API server (not the cache,
	// which has no informer for most ASO kinds); Mapper finds an owner kind's
	// version. Set by the manager.
	OwnerReader client.Reader
	Mapper      apimeta.RESTMapper
	// ClientOptions for the Resource Manager client; zero in production.
	// Tests set a transport that trusts the fake Resource Manager.
	ClientOptions azcore.ClientOptions
}

func NewManagementLockReconciler(
	kubeClient outoftree.KubeClient,
	positiveConditions *conditions.PositiveConditionBuilder,
	credentialProvider outoftree.CredentialProvider,
	cfg outoftree.ConfigValues,
) *ManagementLockReconciler {
	return &ManagementLockReconciler{
		ReconcilerCommon: outoftree.ReconcilerCommon{
			KubeClient:         kubeClient,
			PositiveConditions: positiveConditions,
		},
		CredentialProvider: credentialProvider,
		Cloud:              cfg.Cloud(),
	}
}

func asLock(obj genruntime.MetaObject) (*authv1.ManagementLock, error) {
	l, ok := obj.(*authv1.ManagementLock)
	if !ok {
		return nil, eris.Errorf("cannot modify resource that is not of type *ManagementLock. Type is %T", obj)
	}
	return l, nil
}

// owner is what a lock is on.
type owner struct {
	// armID is the scope: the owner's ARM ID.
	armID string
	// object is the Kubernetes owner, nil for an ARM ID owner.
	object *unstructured.Unstructured
}

// resolveOwner finds the lock's scope. An ARM ID owner is the scope as
// given; an ASO owner must exist in the namespace, be Ready, and carry the
// ARM ID ASO records on what it provisions (serviceoperator.azure.com/resource-id).
// Waiting for the owner is ASO's WaitingForOwner condition.
func (r *ManagementLockReconciler) resolveOwner(ctx context.Context, l *authv1.ManagementLock) (*owner, error) {
	ref := l.Spec.Owner
	if ref == nil {
		return nil, eris.New("spec.owner is required")
	}
	if ref.ARMID != "" {
		return &owner{armID: ref.ARMID}, nil
	}

	waiting := func(err error) error {
		return conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityWarning, conditions.ReasonWaitingForOwner)
	}
	mapping, err := r.Mapper.RESTMapping(schema.GroupKind{Group: ref.Group, Kind: ref.Kind})
	if err != nil {
		return nil, waiting(eris.Wrapf(err, "owner kind %s/%s is not installed", ref.Group, ref.Kind))
	}
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(mapping.GroupVersionKind)
	if err := r.OwnerReader.Get(ctx, types.NamespacedName{Namespace: l.Namespace, Name: ref.Name}, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, waiting(eris.Errorf("Owner %s/%s %q cannot be found. Progress is blocked until the owner is created.", ref.Group, ref.Kind, ref.Name))
		}
		return nil, eris.Wrapf(err, "reading owner %s/%s %q", ref.Group, ref.Kind, ref.Name)
	}
	if !isReady(obj) {
		return nil, waiting(eris.Errorf("Owner %s/%s %q is not Ready. Progress is blocked until it is.", ref.Group, ref.Kind, ref.Name))
	}
	id := obj.GetAnnotations()[genruntime.ResourceIDAnnotation]
	if id == "" {
		return nil, waiting(eris.Errorf("Owner %s/%s %q has no ARM ID (%s) yet", ref.Group, ref.Kind, ref.Name, genruntime.ResourceIDAnnotation))
	}
	return &owner{armID: id, object: obj}, nil
}

// isReady is whether an unstructured ASO object's Ready condition is True.
func isReady(obj *unstructured.Unstructured) bool {
	conds, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if ok && m["type"] == conditions.ConditionTypeReady {
			return m["status"] == string(metav1.ConditionTrue)
		}
	}
	return false
}

// Claim waits for a Ready owner, records the lock's ARM ID, and makes a
// Kubernetes owner the object's owner, as ASO's ClaimResource does.
func (r *ManagementLockReconciler) Claim(ctx context.Context, log logr.Logger, _ record.EventRecorder, obj genruntime.MetaObject) error {
	l, err := asLock(obj)
	if err != nil {
		return err
	}
	o, err := r.resolveOwner(ctx, l)
	if err != nil {
		return err
	}
	genruntime.AddAnnotation(l, genruntime.ResourceIDAnnotation, armlocks.ID(o.armID, l.AzureName()))
	if o.object != nil {
		ref := metav1.OwnerReference{
			APIVersion: o.object.GetAPIVersion(),
			Kind:       o.object.GetKind(),
			Name:       o.object.GetName(),
			UID:        o.object.GetUID(),
		}
		l.SetOwnerReferences(ensureOwnerRef(l.GetOwnerReferences(), ref))
		log.V(1).Info("Set owner reference", "owner", ref.APIVersion+"/"+ref.Kind, "ownerName", ref.Name)
	}
	return nil
}

func ensureOwnerRef(refs []metav1.OwnerReference, ref metav1.OwnerReference) []metav1.OwnerReference {
	for i, existing := range refs {
		if existing.UID == ref.UID {
			refs[i] = ref
			return refs
		}
	}
	return append(refs, ref)
}

// client returns a Resource Manager client with the credential ASO would
// use for the object, after ASO's check that the scope is in the
// credential's subscription.
func (r *ManagementLockReconciler) client(ctx context.Context, l *authv1.ManagementLock, scope string) (*armlocks.Client, error) {
	cred, err := r.CredentialProvider.GetCredential(ctx, l)
	if err != nil {
		return nil, eris.Wrap(err, "getting the credential")
	}
	if err := checkSubscription(cred.SubscriptionID(), scope); err != nil {
		return nil, err
	}
	cloudConfig := r.Cloud
	if c := cred.CloudConfig(); c != nil {
		cloudConfig = *c
	}
	rm, ok := cloudConfig.Services[cloud.ResourceManager]
	if !ok {
		return nil, eris.New("no Resource Manager endpoint is configured")
	}
	opts := r.ClientOptions
	return armlocks.New(rm.Endpoint, rm.Audience, cred.TokenCredential(), &opts)
}

// checkSubscription is ASO's (internal/reconcilers/arm): a scope in another
// subscription than the credential's is refused, so a namespace's
// credential cannot be pointed elsewhere by an ARM ID.
func checkSubscription(subscriptionID, scope string) error {
	parsed, err := arm.ParseResourceID(scope)
	if err != nil {
		err = eris.Wrapf(err, "parsing resource ID %q", scope)
		return conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityError, conditions.ReasonFailed)
	}
	if !genruntime.CheckARMIDMatchesSubscription(subscriptionID, parsed) {
		err = eris.Errorf("SubscriptionID %q for %q resource does not match with Client Credential: %q", parsed.SubscriptionID, scope, subscriptionID)
		return conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityError, conditions.ReasonSubscriptionMismatch)
	}
	return nil
}

func (r *ManagementLockReconciler) CreateOrUpdate(
	ctx context.Context,
	log logr.Logger,
	_ record.EventRecorder,
	obj genruntime.MetaObject,
	_ annotations.ResolvedReconcilePolicies,
) (ctrl.Result, error) {
	l, err := asLock(obj)
	if err != nil {
		return ctrl.Result{}, err
	}
	name := l.AzureName()
	log = log.WithValues("azureName", name)

	o, err := r.resolveOwner(ctx, l)
	if err != nil {
		return ctrl.Result{}, err
	}
	c, err := r.client(ctx, l, o.armID)
	if err != nil {
		return ctrl.Result{}, err
	}

	desired := armlocks.Properties{Level: l.Spec.Level, Notes: l.Spec.Notes}
	for _, o := range l.Spec.Owners {
		desired.Owners = append(desired.Owners, armlocks.Owner{ApplicationID: o.ApplicationId})
	}
	current, err := c.Get(ctx, o.armID, name)
	if err != nil && !armlocks.IsNotFound(err) {
		return ctrl.Result{}, eris.Wrapf(err, "reading lock %s on %s", name, o.armID)
	}
	if err != nil || !current.Properties.Equal(desired) {
		log.V(0).Info("Setting the lock", "scope", o.armID, "level", desired.Level)
		current, err = c.CreateOrUpdate(ctx, o.armID, name, desired)
		if err != nil {
			return ctrl.Result{}, eris.Wrapf(err, "setting lock %s on %s", name, o.armID)
		}
	}
	// ASO's generic reconciler commits the object after CreateOrUpdate, so
	// the status is written here.
	writeStatus(l, o.armID, current)
	return ctrl.Result{}, nil
}

func writeStatus(l *authv1.ManagementLock, scope string, lock *armlocks.Lock) {
	l.Status.Id = lock.ID
	l.Status.Scope = scope
	l.Status.Level = lock.Properties.Level
	l.Status.Notes = lock.Properties.Notes
	l.Status.Owners = nil
	for _, o := range lock.Properties.Owners {
		l.Status.Owners = append(l.Status.Owners, authv1.LockOwner{ApplicationId: o.ApplicationID})
	}
}

// scopeAndName splits a lock's ARM ID into its scope and name.
func scopeAndName(id string) (string, string, bool) {
	marker := "/providers/" + armlocks.ResourceType + "/"
	i := strings.LastIndex(strings.ToLower(id), strings.ToLower(marker))
	if i < 0 {
		return "", "", false
	}
	return id[:i], id[i+len(marker):], true
}

// Delete removes the lock, from the ARM ID recorded when it was claimed, so
// it works when the owner is gone from Kubernetes. A lock, or a scope,
// that Azure no longer has is deleted already.
func (r *ManagementLockReconciler) Delete(ctx context.Context, log logr.Logger, _ record.EventRecorder, obj genruntime.MetaObject) (ctrl.Result, error) {
	l, err := asLock(obj)
	if err != nil {
		return ctrl.Result{}, err
	}
	id, ok := l.GetAnnotations()[genruntime.ResourceIDAnnotation]
	if !ok {
		// Never claimed, so never created.
		return ctrl.Result{}, nil
	}
	scope, name, ok := scopeAndName(id)
	if !ok {
		return ctrl.Result{}, eris.Errorf("%s %q is not a lock's ARM ID", genruntime.ResourceIDAnnotation, id)
	}
	c, err := r.client(ctx, l, scope)
	if err != nil {
		return ctrl.Result{}, err
	}
	log.V(0).Info("Deleting the lock", "id", id)
	if err := c.Delete(ctx, scope, name); err != nil && !armlocks.IsNotFound(err) {
		return ctrl.Result{}, eris.Wrapf(err, "deleting lock %s", id)
	}
	return ctrl.Result{}, nil
}

func (r *ManagementLockReconciler) UpdateStatus(ctx context.Context, _ logr.Logger, _ record.EventRecorder, obj genruntime.MetaObject, _ annotations.ResolvedReconcilePolicies) error {
	l, err := asLock(obj)
	if err != nil {
		return err
	}
	o, err := r.resolveOwner(ctx, l)
	if err != nil {
		return err
	}
	c, err := r.client(ctx, l, o.armID)
	if err != nil {
		return err
	}
	lock, err := c.Get(ctx, o.armID, l.AzureName())
	if armlocks.IsNotFound(err) {
		err = eris.Errorf("lock %s on %s does not exist", l.AzureName(), o.armID)
		return conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityWarning, conditions.ReasonAzureResourceNotFound)
	}
	if err != nil {
		return err
	}
	writeStatus(l, o.armID, lock)
	return nil
}
