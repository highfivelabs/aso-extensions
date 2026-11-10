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

var _ extensions.ARMResourceModifier = &VirtualHubExtension{}

// ModifyARMResource keeps a hub's route tables (routeTables), which are their
// own kind, on its PUT, as a circuit keeps its peerings.
func (extension *VirtualHubExtension) ModifyARMResource(
	ctx context.Context,
	armClient *outoftree.GenericClient,
	armObj genruntime.ARMResource,
	obj genruntime.ARMMetaObject,
	kubeClient outoftree.KubeClient,
	resolver *outoftree.Resolver,
	log logr.Logger,
) (genruntime.ARMResource, error) {
	if _, ok := obj.(*v20260301.VirtualHub); !ok {
		return nil, eris.Errorf("cannot run on unknown resource type %T", obj)
	}

	return keepChildCollections(ctx, armClient, armObj, obj, kubeClient, log,
		childCollection{jsonName: "virtualHubRouteTableV2s", fieldName: "VirtualHubRouteTableV2S"},
	)
}
