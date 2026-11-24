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

	v20260101 "github.com/highfivelabs/aso-extensions/apis/arm/eventhub/v20260101"
)

var _ extensions.Deleter = &NamespacesNetworkRuleSetExtension{}

// Delete leaves a namespace's network rule set in Azure (docs/plan.md f62):
// Azure makes the one rule set, default, with the namespace, has no DELETE
// for it, and deletes it with the namespace. Deleting the Kubernetes object
// stops managing the rules; the ones last PUT stay until the namespace goes
// (as a site's config sections are left, web's site_config_extensions.go).
func (extension *NamespacesNetworkRuleSetExtension) Delete(
	ctx context.Context,
	log logr.Logger,
	resolver *outoftree.Resolver,
	armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject,
	next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20260101.NamespacesNetworkRuleSet); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	id, _ := genruntime.GetResourceID(obj)
	log.V(1).Info("Leaving the network rule set in Azure: it is deleted with its namespace", "id", id)
	return extensions.DeleteCompleted(), nil
}
