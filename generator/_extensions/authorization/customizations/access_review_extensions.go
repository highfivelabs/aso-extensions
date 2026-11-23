/*
Copyright (c) Microsoft Corporation.
Licensed under the MIT license.
*/

// Modified by High Five Labs: role_eligibility_schedule_request_extensions.go
// (beside it), copied from ASO pull request #5638's
// RoleEligibilityScheduleRequest extension and webhook defaulting, for the
// access review schedule and history definitions (the pull request has
// neither): only the Claimer that gives an empty azureName a UUID, with the
// types changed; an access review has no role to resolve, so no
// ARMResourceModifier. Kept in generator/_extensions and copied into
// apis/arm by hack/generate-arm.sh, which deletes apis/arm on every run.

package customizations

import (
	"context"
	"strings"

	"github.com/go-logr/logr"
	"github.com/rotisserie/eris"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/extensions"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	v20211201preview "github.com/highfivelabs/aso-extensions/apis/arm/authorization/v20211201preview"
)

var _ extensions.Claimer = &AccessReviewScheduleDefinitionExtension{}

// Claim gives a schedule definition without an azureName a UUID, as ASO's
// defaulting webhook does for a name that must be a GUID, then claims it as
// ASO does. The claim is committed to the object, so the name is recorded
// in spec.azureName and stays the same on every later reconcile.
func (extension *AccessReviewScheduleDefinitionExtension) Claim(
	ctx context.Context,
	log logr.Logger,
	obj genruntime.ARMOwnedMetaObject,
	next extensions.ClaimFunc,
) error {
	definition, ok := obj.(*v20211201preview.AccessReviewScheduleDefinition)
	if !ok {
		return eris.Errorf(
			"Cannot run AccessReviewScheduleDefinitionExtension.Claim() with unexpected resource type %T",
			obj,
		)
	}

	if definition.Spec.Owner != nil && definition.Spec.AzureName == "" {
		convention := "stable"
		if definition.Spec.OperatorSpec != nil && definition.Spec.OperatorSpec.NamingConvention != nil {
			convention = *definition.Spec.OperatorSpec.NamingConvention
		}
		definition.Spec.AzureName = defaultUUIDName(
			convention,
			definition.Name,
			definition.Owner(),
			v20211201preview.GroupVersion.WithKind("AccessReviewScheduleDefinition").GroupKind(),
			definition.Namespace,
		)
		log.V(1).Info("Defaulted azureName", "azureName", definition.Spec.AzureName, "namingConvention", convention)
	}

	return next(ctx, log, obj)
}

var _ extensions.Claimer = &AccessReviewHistoryDefinitionExtension{}

// Claim gives a history definition without an azureName a UUID, as the
// schedule definition's Claim does.
func (extension *AccessReviewHistoryDefinitionExtension) Claim(
	ctx context.Context,
	log logr.Logger,
	obj genruntime.ARMOwnedMetaObject,
	next extensions.ClaimFunc,
) error {
	definition, ok := obj.(*v20211201preview.AccessReviewHistoryDefinition)
	if !ok {
		return eris.Errorf(
			"Cannot run AccessReviewHistoryDefinitionExtension.Claim() with unexpected resource type %T",
			obj,
		)
	}

	if definition.Spec.Owner != nil && definition.Spec.AzureName == "" {
		convention := "stable"
		if definition.Spec.OperatorSpec != nil && definition.Spec.OperatorSpec.NamingConvention != nil {
			convention = *definition.Spec.OperatorSpec.NamingConvention
		}
		definition.Spec.AzureName = defaultUUIDName(
			convention,
			definition.Name,
			definition.Owner(),
			v20211201preview.GroupVersion.WithKind("AccessReviewHistoryDefinition").GroupKind(),
			definition.Namespace,
		)
		log.V(1).Info("Defaulted azureName", "azureName", definition.Spec.AzureName, "namingConvention", convention)
	}

	return next(ctx, log, obj)
}

// defaultUUIDName is the webhook's choice of name: a random UUID, or
// ("stable") a UUID of the owner, group, kind, namespace and name. An
// unknown convention leaves the name empty, as the webhook does.
func defaultUUIDName(
	convention string,
	name string,
	owner *genruntime.ResourceReference,
	groupKind schema.GroupKind,
	namespace string,
) string {
	if strings.EqualFold(convention, "random") {
		return outoftree.MakeRandomUUID()
	}
	if strings.EqualFold(convention, "stable") {
		return outoftree.MakeUUIDName(
			name,
			outoftree.MakeUniqueOwnerScopedString(owner, groupKind, namespace, name),
		)
	}
	return ""
}
