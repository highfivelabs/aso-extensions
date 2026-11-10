/*
Copyright (c) Microsoft Corporation.
Licensed under the MIT license.
*/

// Modified by High Five Labs: copied from ASO pull request #5638 (head
// 5d0e686e), v2/api/authorization/customizations/role_eligibility_schedule_request_extensions.go
// (ModifyARMResource), with the defaulting of its webhook,
// v2/api/authorization/v20201001/webhook/role_eligibility_schedule_request_defaults.go
// (Claim). Changes: the object is our API version, which the controller is
// registered on, not ASO's storage hub; ASO's internal types and
// resolveBuiltInRoleDefinition come through pkg/outoftree and the exports
// patch 0003 adds; and, with no webhook here, the name is defaulted when the
// reconciler first claims the object, not on admission (docs/plan.md,
// "Names that must be GUIDs"). Kept in generator/_extensions and copied into
// apis/arm by hack/generate-arm.sh, which deletes apis/arm on every run.

package customizations

import (
	"context"
	"strings"

	"github.com/go-logr/logr"
	"github.com/rotisserie/eris"

	asoauthorization "github.com/Azure/azure-service-operator/v2/api/authorization/customizations"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/extensions"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	v20201001 "github.com/highfivelabs/aso-extensions/apis/arm/authorization/v20201001"
)

var _ extensions.ARMResourceModifier = &RoleEligibilityScheduleRequestExtension{}

func (extension *RoleEligibilityScheduleRequestExtension) ModifyARMResource(
	ctx context.Context,
	armClient *outoftree.GenericClient,
	armObj genruntime.ARMResource,
	obj genruntime.ARMMetaObject,
	kubeClient outoftree.KubeClient,
	resolver *outoftree.Resolver,
	log logr.Logger,
) (genruntime.ARMResource, error) {
	request, ok := obj.(*v20201001.RoleEligibilityScheduleRequest)
	if !ok {
		return nil, eris.Errorf(
			"Cannot run RoleEligibilityScheduleRequestExtension.ModifyARMResource() with unexpected resource type %T",
			obj,
		)
	}

	if request.Spec.RoleDefinitionReference == nil {
		return armObj, nil
	}
	roleDefinitionName := request.Spec.RoleDefinitionReference.WellKnownName
	if roleDefinitionName == "" {
		return armObj, nil
	}

	roleDefinitionId, err := asoauthorization.ResolveBuiltInRoleDefinition(ctx, roleDefinitionName, armObj, armClient)
	if err != nil {
		return nil, eris.Wrapf(err, "resolving built in role definition %q", roleDefinitionName)
	}

	log.V(1).Info("Resolved built-in role", "roleName", roleDefinitionName, "roleId", roleDefinitionId)

	err = outoftree.SetProperty(armObj.Spec(), "Properties.RoleDefinitionId", &roleDefinitionId)
	if err != nil {
		return nil, eris.Wrapf(err, "error setting RoleDefinitionId to %s", roleDefinitionId)
	}

	return armObj, nil
}

var _ extensions.Claimer = &RoleEligibilityScheduleRequestExtension{}

// Claim gives a request without an azureName a UUID, as ASO's defaulting
// webhook does (a request's name must be a GUID), then claims it as ASO
// does. The claim is committed to the object, so the name is recorded in
// spec.azureName and stays the same on every later reconcile.
func (extension *RoleEligibilityScheduleRequestExtension) Claim(
	ctx context.Context,
	log logr.Logger,
	obj genruntime.ARMOwnedMetaObject,
	next extensions.ClaimFunc,
) error {
	request, ok := obj.(*v20201001.RoleEligibilityScheduleRequest)
	if !ok {
		return eris.Errorf(
			"Cannot run RoleEligibilityScheduleRequestExtension.Claim() with unexpected resource type %T",
			obj,
		)
	}

	if request.Spec.Owner != nil && request.Spec.AzureName == "" {
		convention := "stable"
		if request.Spec.OperatorSpec != nil && request.Spec.OperatorSpec.NamingConvention != nil {
			convention = *request.Spec.OperatorSpec.NamingConvention
		}

		if strings.EqualFold(convention, "random") {
			request.Spec.AzureName = outoftree.MakeRandomUUID()
		} else if strings.EqualFold(convention, "stable") {
			request.Spec.AzureName = outoftree.MakeUUIDName(
				request.Name,
				outoftree.MakeUniqueOwnerScopedString(
					request.Owner(),
					v20201001.GroupVersion.WithKind("RoleEligibilityScheduleRequest").GroupKind(),
					request.Namespace,
					request.Name,
				),
			)
		}
		log.V(1).Info("Defaulted azureName", "azureName", request.Spec.AzureName, "namingConvention", convention)
	}

	return next(ctx, log, obj)
}
