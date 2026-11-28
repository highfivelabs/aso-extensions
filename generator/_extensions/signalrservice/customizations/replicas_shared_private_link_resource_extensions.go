// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

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

	v20240301 "github.com/highfivelabs/aso-extensions/apis/arm/signalrservice/v20240301"
)

var (
	_ extensions.Deleter = &WebPubSubReplicasSharedPrivateLinkResourceExtension{}
	_ extensions.Deleter = &SignalRReplicasSharedPrivateLinkResourceExtension{}
)

// A replica's shared private link resources have a PUT and a GET but no
// DELETE (docs/plan.md f68): they go with the replica. Deleting the
// Kubernetes object stops managing one; what was last PUT stays until the
// replica goes (as a Service Bus namespace's network rule set is left).

// Delete leaves a Web PubSub replica's shared private link resource.
func (extension *WebPubSubReplicasSharedPrivateLinkResourceExtension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20240301.WebPubSubReplicasSharedPrivateLinkResource); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithReplica(log, obj)
}

// Delete leaves a SignalR replica's shared private link resource.
func (extension *SignalRReplicasSharedPrivateLinkResourceExtension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20240301.SignalRReplicasSharedPrivateLinkResource); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithReplica(log, obj)
}

func leaveWithReplica(log logr.Logger, obj genruntime.ARMMetaObject) (extensions.DeleteResult, error) {
	id, _ := genruntime.GetResourceID(obj)
	log.V(1).Info("Leaving the replica's shared private link resource in Azure: it is deleted with its replica", "id", id)
	return extensions.DeleteCompleted(), nil
}
