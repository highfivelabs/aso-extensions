// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Kept in generator/_extensions and copied into apis/arm by
// hack/generate-arm.sh, which deletes apis/arm on every run.

package customizations

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/rotisserie/eris"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/configmaps"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	v20241023 "github.com/highfivelabs/aso-extensions/apis/arm/automation/v20241023"
)

// agentRegistrationAPIVersion is the API version the account is generated
// from (generator/azure-arm/automation.yaml).
const agentRegistrationAPIVersion = "2024-10-23"

var _ genruntime.KubernetesConfigExporter = &AutomationAccountExtension{}

// ExportKubernetesConfigMaps writes the account's DSC server endpoint to the
// ConfigMap operatorSpec.configMaps.dscServerEndpoint names. Azure returns
// it from the account's agentRegistrationInformation, not from the
// account's GET, so it cannot be in the status (azurerm reads it there for
// its dsc_server_endpoint). Only the endpoint is read; the registration keys
// beside it are secrets and are not written.
func (extension *AutomationAccountExtension) ExportKubernetesConfigMaps(
	ctx context.Context,
	obj genruntime.MetaObject,
	armClient *outoftree.GenericClient,
	log logr.Logger,
) ([]client.Object, error) {
	account, ok := obj.(*v20241023.AutomationAccount)
	if !ok {
		return nil, eris.Errorf("cannot run on unknown resource type %T, expected *v20241023.AutomationAccount", obj)
	}
	spec := account.Spec.OperatorSpec
	if spec == nil || spec.ConfigMaps == nil || spec.ConfigMaps.DscServerEndpoint == nil {
		return nil, nil
	}

	resourceID, hasResourceID := genruntime.GetResourceID(account)
	if !hasResourceID {
		return nil, eris.Errorf("%s has no resource ID yet, so its DSC server endpoint cannot be read", account.GetName())
	}
	var registration struct {
		Endpoint string `json:"endpoint"`
	}
	if _, err := armClient.GetByID(ctx, resourceID+"/agentRegistrationInformation", agentRegistrationAPIVersion, &registration); err != nil {
		return nil, eris.Wrapf(err, "reading the agent registration information of %s", resourceID)
	}
	if registration.Endpoint == "" {
		return nil, eris.Errorf("Azure returned no DSC server endpoint for %s yet", resourceID)
	}

	collector := configmaps.NewCollector(account.Namespace)
	collector.AddValue(spec.ConfigMaps.DscServerEndpoint, registration.Endpoint)
	values, err := collector.Values()
	if err != nil {
		return nil, err
	}
	return configmaps.SliceToClientObjectSlice(values), nil
}
