/*
Copyright (c) Microsoft Corporation.
Licensed under the MIT license.
*/

// Modified by High Five Labs: generator/_extensions/authorization/
// customizations/role_eligibility_schedule_request_extensions.go, copied
// from ASO pull request #5638's RoleEligibilityScheduleRequest extension and
// webhook defaulting, for a Defender for Cloud custom assessment metadata (the pull
// request has none): only the Claimer that gives an empty azureName a UUID
// (a metadata's key must be a GUID), with the type changed. Kept in
// generator/_extensions and copied into apis/arm by hack/generate-arm.sh,
// which deletes apis/arm on every run.

package customizations

import (
	"context"
	"strings"

	"github.com/go-logr/logr"
	"github.com/rotisserie/eris"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/extensions"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	v20250504 "github.com/highfivelabs/aso-extensions/apis/arm/security/v20250504"
)

var _ extensions.Claimer = &AssessmentMetadataExtension{}

// Claim gives an assessment metadata without an azureName a UUID, as ASO's
// defaulting webhook does for a name that must be a GUID, then claims it as
// ASO does. The claim is committed to the object, so the name is recorded
// in spec.azureName and stays the same on every later reconcile.
func (extension *AssessmentMetadataExtension) Claim(
	ctx context.Context,
	log logr.Logger,
	obj genruntime.ARMOwnedMetaObject,
	next extensions.ClaimFunc,
) error {
	metadata, ok := obj.(*v20250504.AssessmentMetadata)
	if !ok {
		return eris.Errorf(
			"Cannot run AssessmentMetadataExtension.Claim() with unexpected resource type %T",
			obj,
		)
	}

	if metadata.Spec.Owner != nil && metadata.Spec.AzureName == "" {
		convention := "stable"
		if metadata.Spec.OperatorSpec != nil && metadata.Spec.OperatorSpec.NamingConvention != nil {
			convention = *metadata.Spec.OperatorSpec.NamingConvention
		}

		if strings.EqualFold(convention, "random") {
			metadata.Spec.AzureName = outoftree.MakeRandomUUID()
		} else if strings.EqualFold(convention, "stable") {
			metadata.Spec.AzureName = outoftree.MakeUUIDName(
				metadata.Name,
				outoftree.MakeUniqueOwnerScopedString(
					metadata.Owner(),
					v20250504.GroupVersion.WithKind("AssessmentMetadata").GroupKind(),
					metadata.Namespace,
					metadata.Name,
				),
			)
		}
		log.V(1).Info("Defaulted azureName", "azureName", metadata.Spec.AzureName, "namingConvention", convention)
	}

	return next(ctx, log, obj)
}
