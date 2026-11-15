// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Package web reconciles SitesConfigAppSettings and
// SitesConfigConnectionStrings: an App Service app's (or slot's) app settings
// and connection strings, whose values may come from Kubernetes Secrets.
// Azure writes them with a PUT of the whole set and reads them only with a
// POST .../list (no GET, no DELETE), which ASO's generic ARM reconciler cannot
// do (it reads every resource back with a GET), so they run on ASO's generic
// reconciler with this reconciler, as VaultSecret does: ASO's annotations
// (reconcile-policy, credential-from) and conditions, and the credential
// ASO's credential provider picks for the object.
//
// Each reconcile reads the set with the POST and PUTs the desired set only
// when it differs. Deleting the object makes no call: the section lives as
// long as the app, and Azure has no DELETE for it, so the last values stay
// (as for the generated config sections, site_config_extensions.go).
// Values are never logged or written to the status.
package web

import (
	"context"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/go-logr/logr"
	"github.com/rotisserie/eris"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/Azure/azure-service-operator/v2/pkg/common/annotations"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	webv1 "github.com/highfivelabs/aso-extensions/apis/web/v20260715"
	"github.com/highfivelabs/aso-extensions/pkg/webconfig"
)

var _ genruntime.Reconciler = &SiteConfigReconciler{}

// SiteConfigReconciler reconciles both kinds.
type SiteConfigReconciler struct {
	outoftree.ARMOwnedResourceReconcilerCommon
	ResourceResolver   *outoftree.Resolver
	CredentialProvider outoftree.CredentialProvider
	// Cloud is the Resource Manager endpoint and audience from ASO's
	// configuration; a credential's own cloud configuration wins.
	Cloud cloud.Configuration
	// ClientOptions for the Resource Manager client; zero in production.
	// Tests and the slot guard set the transport.
	ClientOptions azcore.ClientOptions
}

func NewSiteConfigReconciler(
	kubeClient outoftree.KubeClient,
	resourceResolver *outoftree.Resolver,
	positiveConditions *conditions.PositiveConditionBuilder,
	credentialProvider outoftree.CredentialProvider,
	cfg outoftree.ConfigValues,
) *SiteConfigReconciler {
	return &SiteConfigReconciler{
		ResourceResolver:   resourceResolver,
		CredentialProvider: credentialProvider,
		Cloud:              cfg.Cloud(),
		ARMOwnedResourceReconcilerCommon: outoftree.ARMOwnedResourceReconcilerCommon{
			ResourceResolver: resourceResolver,
			ReconcilerCommon: outoftree.ReconcilerCommon{
				KubeClient:         kubeClient,
				PositiveConditions: positiveConditions,
			},
		},
	}
}

// section is what differs between the two kinds.
type section interface {
	// object is the Kubernetes object.
	object() genruntime.ARMOwnedMetaObject
	AzureName() string
	// apply reads the section and PUTs the desired set when it differs.
	apply(ctx context.Context, log logr.Logger, c *webconfig.Client, id string, v values) error
	// read reads the section into the status.
	read(ctx context.Context, c *webconfig.Client, id string) error
	setID(id string)
}

func asSection(obj genruntime.MetaObject) (section, error) {
	switch o := obj.(type) {
	case *webv1.SitesConfigAppSettings:
		return appSettings{o}, nil
	case *webv1.SitesConfigConnectionStrings:
		return connectionStrings{o}, nil
	}
	return nil, eris.Errorf("cannot reconcile a %T as an App Service config section", obj)
}

func (r *SiteConfigReconciler) CreateOrUpdate(
	ctx context.Context,
	log logr.Logger,
	_ record.EventRecorder,
	obj genruntime.MetaObject,
	_ annotations.ResolvedReconcilePolicies,
) (ctrl.Result, error) {
	s, err := asSection(obj)
	if err != nil {
		return ctrl.Result{}, err
	}
	id, err := r.sectionID(ctx, s)
	if err != nil {
		return ctrl.Result{}, err
	}
	log = log.WithValues("id", id)

	v, err := r.values(ctx, s)
	if err != nil {
		return ctrl.Result{}, err
	}
	client, err := r.client(ctx, s.object(), id)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := s.apply(ctx, log, client, id, v); err != nil {
		return ctrl.Result{}, classify(err)
	}
	// ASO's generic reconciler commits the object after CreateOrUpdate
	// (UpdateStatus runs only when the reconcile policy skips), so the
	// status is written here.
	s.setID(id)
	return ctrl.Result{}, classify(s.read(ctx, client, id))
}

// Delete leaves the section as it is: Azure has no DELETE for it, and it is
// deleted with the app.
func (r *SiteConfigReconciler) Delete(_ context.Context, log logr.Logger, _ record.EventRecorder, obj genruntime.MetaObject) (ctrl.Result, error) {
	s, err := asSection(obj)
	if err != nil {
		return ctrl.Result{}, err
	}
	log.V(0).Info("Azure has no DELETE for an App Service config section; leaving its last values on the app", "section", s.AzureName())
	return ctrl.Result{}, nil
}

func (r *SiteConfigReconciler) Claim(ctx context.Context, log logr.Logger, _ record.EventRecorder, obj genruntime.MetaObject) error {
	s, err := asSection(obj)
	if err != nil {
		return err
	}
	return r.ClaimResource(ctx, log, s.object())
}

func (r *SiteConfigReconciler) UpdateStatus(ctx context.Context, _ logr.Logger, _ record.EventRecorder, obj genruntime.MetaObject, _ annotations.ResolvedReconcilePolicies) error {
	s, err := asSection(obj)
	if err != nil {
		return err
	}
	id, err := r.sectionID(ctx, s)
	if err != nil {
		return err
	}
	client, err := r.client(ctx, s.object(), id)
	if err != nil {
		return err
	}
	s.setID(id)
	return classify(s.read(ctx, client, id))
}

// sectionID is the ARM ID of the section: the owning site's or slot's ID
// and /config/<name>.
func (r *SiteConfigReconciler) sectionID(ctx context.Context, s section) (string, error) {
	owner, err := r.ResourceResolver.ResolveOwner(ctx, s.object())
	if err != nil {
		return "", outoftree.ClassifyResolverError(err)
	}
	var appID string
	if owner.FoundKubernetesOwner() {
		id, ok := genruntime.GetResourceID(owner.Owner)
		if !ok || id == "" {
			err := eris.Errorf("owner %q has no ARM ID yet", owner.Owner.GetName())
			return "", conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityWarning, conditions.ReasonWaitingForOwner)
		}
		appID = id
	} else {
		appID = owner.ARMID
	}
	if !isApp(appID) {
		err := eris.Errorf("owner %q is not a Microsoft.Web site or slot", appID)
		return "", conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityError, conditions.ReasonFailed)
	}
	return strings.TrimSuffix(appID, "/") + "/config/" + s.AzureName(), nil
}

// isApp is whether id is a site's or a slot's ARM ID.
func isApp(id string) bool {
	parts := strings.Split(strings.Trim(id, "/"), "/")
	// subscriptions/s/resourceGroups/g/providers/Microsoft.Web/sites/n[/slots/x]
	if len(parts) != 8 && len(parts) != 10 {
		return false
	}
	if !strings.EqualFold(parts[0], "subscriptions") || !strings.EqualFold(parts[2], "resourceGroups") ||
		!strings.EqualFold(parts[4], "providers") || !strings.EqualFold(parts[5], "Microsoft.Web") ||
		!strings.EqualFold(parts[6], "sites") {
		return false
	}
	return len(parts) == 8 || strings.EqualFold(parts[8], "slots")
}

// values are the Secrets' and ConfigMaps' values the object names.
type values struct {
	secrets    genruntime.Resolved[genruntime.SecretReference, string]
	configMaps genruntime.Resolved[genruntime.ConfigMapReference, string]
}

func (r *SiteConfigReconciler) values(ctx context.Context, s section) (values, error) {
	secrets, err := r.ResourceResolver.ResolveResourceSecretReferences(ctx, s.object())
	if err != nil {
		return values{}, outoftree.ClassifyResolverError(err)
	}
	configMaps, err := r.ResourceResolver.ResolveResourceConfigMapReferences(ctx, s.object())
	if err != nil {
		return values{}, outoftree.ClassifyResolverError(err)
	}
	return values{secrets: secrets, configMaps: configMaps}, nil
}

// value is one setting's value: plain, or from a Secret or a ConfigMap and
// put into its template when it has one.
func (v values) value(name string, plain *string, secret *genruntime.SecretReference, configMap *genruntime.ConfigMapReference, template *string) (string, error) {
	out, err := v.referenced(name, plain, secret, configMap)
	if err != nil || template == nil || plain != nil {
		return out, err
	}
	return strings.Replace(*template, "$(value)", out, 1), nil
}

func (v values) referenced(name string, plain *string, secret *genruntime.SecretReference, configMap *genruntime.ConfigMapReference) (string, error) {
	switch {
	case plain != nil:
		return *plain, nil
	case secret != nil:
		out, err := v.secrets.Lookup(*secret)
		if err != nil {
			err = eris.Wrapf(err, "looking up %q's valueFromSecret", name)
			return "", conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityWarning, conditions.ReasonSecretNotFound)
		}
		return out, nil
	case configMap != nil:
		out, err := v.configMaps.Lookup(*configMap)
		if err != nil {
			err = eris.Wrapf(err, "looking up %q's valueFromConfig", name)
			return "", conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityWarning, conditions.ReasonConfigMapNotFound)
		}
		return out, nil
	}
	err := eris.Errorf("%q has no value: set one of value, valueFromSecret and valueFromConfig", name)
	return "", conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityError, conditions.ReasonFailed)
}

// client is a Resource Manager client with the credential ASO would use for
// obj, which must be for the subscription id is in.
func (r *SiteConfigReconciler) client(ctx context.Context, obj genruntime.MetaObject, id string) (*webconfig.Client, error) {
	cred, err := r.CredentialProvider.GetCredential(ctx, obj)
	if err != nil {
		return nil, eris.Wrap(err, "getting the credential")
	}
	if sub := subscriptionOf(id); cred.SubscriptionID() != "" && !strings.EqualFold(sub, cred.SubscriptionID()) {
		err := eris.Errorf("the app is in subscription %q, but the credential %s is for subscription %q",
			sub, cred.CredentialFrom(), cred.SubscriptionID())
		return nil, conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityError, conditions.ReasonFailed)
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
	return webconfig.New(rm.Endpoint, rm.Audience, cred.TokenCredential(), &opts)
}

func subscriptionOf(id string) string {
	parts := strings.Split(strings.Trim(id, "/"), "/")
	if len(parts) >= 2 && strings.EqualFold(parts[0], "subscriptions") {
		return parts[1]
	}
	return ""
}

// classify makes Resource Manager's refusals of the request itself (400,
// 403) errors a person must fix; anything else is retried as it is.
func classify(err error) error {
	if err == nil {
		return nil
	}
	switch webconfig.StatusCode(err) {
	case 400, 403:
		return conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityError, conditions.ReasonFailed)
	case 404:
		return conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityWarning, conditions.ReasonAzureResourceNotFound)
	}
	return err
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
