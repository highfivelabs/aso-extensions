// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Kept in generator/_extensions and copied into apis/arm by
// hack/generate-arm.sh, which deletes apis/arm on every run.

package customizations

import (
	"context"

	"github.com/rotisserie/eris"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/secrets"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"
)

// exportKey reads a key Azure makes (an ExpressRoute circuit's service key,
// an authorization's key) from the resource's own properties and writes it to
// the Secret dest names, and returns it for the secret expressions that ask
// for it by name, as ASO's exporters do with keys they list
// (api/apimanagement/customizations/subscription_extensions.go). The key is
// read with a GET because it is not kept in the status (generator/azure-arm/
// network.yaml removes it there), so only those who may read the Secret read it.
func exportKey(
	ctx context.Context,
	obj genruntime.ARMMetaObject,
	additionalSecrets outoftree.Set[string],
	armClient *outoftree.GenericClient,
	apiVersion string,
	name string,
	dest *genruntime.SecretDestination,
) (*genruntime.KubernetesSecretExportResult, error) {
	if dest == nil && !additionalSecrets.Contains(name) {
		return nil, nil
	}

	resourceID, hasResourceID := genruntime.GetResourceID(obj)
	if !hasResourceID {
		return nil, eris.Errorf("%s has no resource ID yet, so its %s cannot be read", obj.GetName(), name)
	}

	raw := make(map[string]any)
	if _, err := armClient.GetByID(ctx, resourceID, apiVersion, &raw); err != nil {
		return nil, eris.Wrapf(err, "reading %s of %s", name, resourceID)
	}
	props, _ := raw["properties"].(map[string]any)
	value, _ := props[name].(string)
	if value == "" {
		return nil, eris.Errorf("Azure returned no %s for %s yet", name, resourceID)
	}

	collector := secrets.NewCollector(obj.GetNamespace())
	collector.AddValue(dest, value)
	secretSlice, err := collector.Values()
	if err != nil {
		return nil, err
	}

	return &genruntime.KubernetesSecretExportResult{
		Objs:       secrets.SliceToClientObjectSlice(secretSlice),
		RawSecrets: secrets.SelectSecrets(additionalSecrets, map[string]string{name: value}),
	}, nil
}
