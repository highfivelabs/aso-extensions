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
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/extensions"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	v20260301 "github.com/highfivelabs/aso-extensions/apis/arm/network/v20260301"
)

var _ extensions.ARMResourceModifier = &ExpressRouteCircuitExtension{}

// ModifyARMResource keeps a circuit's peerings and authorizations, which are
// their own kinds, on its PUT, as ASO's RouteTable keeps its routes.
func (extension *ExpressRouteCircuitExtension) ModifyARMResource(
	ctx context.Context,
	armClient *outoftree.GenericClient,
	armObj genruntime.ARMResource,
	obj genruntime.ARMMetaObject,
	kubeClient outoftree.KubeClient,
	resolver *outoftree.Resolver,
	log logr.Logger,
) (genruntime.ARMResource, error) {
	if _, ok := obj.(*v20260301.ExpressRouteCircuit); !ok {
		return nil, eris.Errorf("cannot run on unknown resource type %T", obj)
	}

	return keepChildCollections(ctx, armClient, armObj, obj, kubeClient, log,
		childCollection{jsonName: "peerings", fieldName: "Peerings"},
		childCollection{jsonName: "authorizations", fieldName: "Authorizations"},
	)
}

var _ genruntime.KubernetesSecretExporter = &ExpressRouteCircuitExtension{}

// ExportKubernetesSecrets writes the circuit's service key, which a
// connectivity provider provisions the circuit with, to
// operatorSpec.secrets.serviceKey.
func (extension *ExpressRouteCircuitExtension) ExportKubernetesSecrets(
	ctx context.Context,
	obj genruntime.MetaObject,
	additionalSecrets outoftree.Set[string],
	armClient *outoftree.GenericClient,
	log logr.Logger,
) (*genruntime.KubernetesSecretExportResult, error) {
	circuit, ok := obj.(*v20260301.ExpressRouteCircuit)
	if !ok {
		return nil, eris.Errorf("cannot run on unknown resource type %T, expected *v20260301.ExpressRouteCircuit", obj)
	}

	var dest *genruntime.SecretDestination
	if circuit.Spec.OperatorSpec != nil && circuit.Spec.OperatorSpec.Secrets != nil {
		dest = circuit.Spec.OperatorSpec.Secrets.ServiceKey
	}

	return exportKey(ctx, circuit, additionalSecrets, armClient, string(v20260301.APIVersion_Value), "serviceKey", dest)
}
