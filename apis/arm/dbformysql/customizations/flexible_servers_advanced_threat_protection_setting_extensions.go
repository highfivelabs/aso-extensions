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

	v20241230 "github.com/highfivelabs/aso-extensions/apis/arm/dbformysql/v20241230"
)

var _ extensions.Deleter = &FlexibleServersAdvancedThreatProtectionSettingExtension{}

// Delete leaves a server's advanced threat protection setting in Azure
// (docs/plan.md f66): Azure makes the one setting, Default, with the server,
// has no DELETE for it, and deletes it with the server. Deleting the
// Kubernetes object stops managing it; the state last PUT (Enabled or
// Disabled) stays until the server goes (as a Service Bus namespace's
// network rule set is left, servicebus's extensions).
func (extension *FlexibleServersAdvancedThreatProtectionSettingExtension) Delete(
	ctx context.Context,
	log logr.Logger,
	resolver *outoftree.Resolver,
	armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject,
	next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20241230.FlexibleServersAdvancedThreatProtectionSetting); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	id, _ := genruntime.GetResourceID(obj)
	log.V(1).Info("Leaving the advanced threat protection setting in Azure: it is deleted with its server", "id", id)
	return extensions.DeleteCompleted(), nil
}
