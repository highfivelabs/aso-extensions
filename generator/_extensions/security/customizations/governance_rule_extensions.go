/*
Copyright (c) Microsoft Corporation.
Licensed under the MIT license.
*/

// Modified by High Five Labs: generator/_extensions/authorization/
// customizations/role_eligibility_schedule_request_extensions.go, copied
// from ASO pull request #5638's RoleEligibilityScheduleRequest extension and
// webhook defaulting, for a Defender for Cloud governance rule (the pull
// request has none): only the Claimer that gives an empty azureName a UUID
// (a rule's key must be a GUID), with the type changed. Kept in
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

	v20220101preview "github.com/highfivelabs/aso-extensions/apis/arm/security/v20220101preview"
)

var _ extensions.Claimer = &GovernanceRuleExtension{}

// Claim gives a governance rule without an azureName a UUID, as ASO's
// defaulting webhook does for a name that must be a GUID, then claims it as
// ASO does. The claim is committed to the object, so the name is recorded
// in spec.azureName and stays the same on every later reconcile.
func (extension *GovernanceRuleExtension) Claim(
	ctx context.Context,
	log logr.Logger,
	obj genruntime.ARMOwnedMetaObject,
	next extensions.ClaimFunc,
) error {
	rule, ok := obj.(*v20220101preview.GovernanceRule)
	if !ok {
		return eris.Errorf(
			"Cannot run GovernanceRuleExtension.Claim() with unexpected resource type %T",
			obj,
		)
	}

	if rule.Spec.Owner != nil && rule.Spec.AzureName == "" {
		convention := "stable"
		if rule.Spec.OperatorSpec != nil && rule.Spec.OperatorSpec.NamingConvention != nil {
			convention = *rule.Spec.OperatorSpec.NamingConvention
		}

		if strings.EqualFold(convention, "random") {
			rule.Spec.AzureName = outoftree.MakeRandomUUID()
		} else if strings.EqualFold(convention, "stable") {
			rule.Spec.AzureName = outoftree.MakeUUIDName(
				rule.Name,
				outoftree.MakeUniqueOwnerScopedString(
					rule.Owner(),
					v20220101preview.GroupVersion.WithKind("GovernanceRule").GroupKind(),
					rule.Namespace,
					rule.Name,
				),
			)
		}
		log.V(1).Info("Defaulted azureName", "azureName", rule.Spec.AzureName, "namingConvention", convention)
	}

	return next(ctx, log, obj)
}
