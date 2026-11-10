// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Kept in generator/_extensions and copied into apis/arm by
// hack/generate-arm.sh, which deletes apis/arm on every run.

package customizations

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/rotisserie/eris"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	v20260301 "github.com/highfivelabs/aso-extensions/apis/arm/network/v20260301"
)

var _ genruntime.KubernetesSecretExporter = &ExpressRoutePortsAuthorizationExtension{}

// ExportKubernetesSecrets writes an ExpressRoute Direct port authorization's key, which a circuit on the port in another subscription redeems, to
// operatorSpec.secrets.authorizationKey.
func (extension *ExpressRoutePortsAuthorizationExtension) ExportKubernetesSecrets(
	ctx context.Context,
	obj genruntime.MetaObject,
	additionalSecrets outoftree.Set[string],
	armClient *outoftree.GenericClient,
	log logr.Logger,
) (*genruntime.KubernetesSecretExportResult, error) {
	authorization, ok := obj.(*v20260301.ExpressRoutePortsAuthorization)
	if !ok {
		return nil, eris.Errorf("cannot run on unknown resource type %T, expected *v20260301.ExpressRoutePortsAuthorization", obj)
	}

	var dest *genruntime.SecretDestination
	if authorization.Spec.OperatorSpec != nil && authorization.Spec.OperatorSpec.Secrets != nil {
		dest = authorization.Spec.OperatorSpec.Secrets.AuthorizationKey
	}

	return exportKey(ctx, authorization, additionalSecrets, armClient, string(v20260301.APIVersion_Value), "authorizationKey", dest)
}
