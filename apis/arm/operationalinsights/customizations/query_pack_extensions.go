// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Kept in generator/_extensions and copied into apis/arm by
// hack/generate-arm.sh, which deletes apis/arm on every run.

package customizations

import (
	"context"
	"encoding/json"

	"github.com/go-logr/logr"
	"github.com/rotisserie/eris"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/extensions"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	v20250701 "github.com/highfivelabs/aso-extensions/apis/arm/operationalinsights/v20250701"
)

var _ extensions.ARMResourceModifier = &QueryPackExtension{}

// ModifyARMResource sends a query pack's properties as an empty object. The
// spec marks properties required and every property in it read-only, so the
// generator leaves the field out of the spec, and Azure refuses a PUT
// without it ("Value cannot be null.", a live run of 2026-11-12).
func (extension *QueryPackExtension) ModifyARMResource(
	ctx context.Context,
	armClient *outoftree.GenericClient,
	armObj genruntime.ARMResource,
	obj genruntime.ARMMetaObject,
	kubeClient outoftree.KubeClient,
	resolver *outoftree.Resolver,
	log logr.Logger,
) (genruntime.ARMResource, error) {
	if _, ok := obj.(*v20250701.QueryPack); !ok {
		return nil, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return genruntime.NewARMResource(withEmptyProperties{armObj.Spec()}, armObj.Status(), armObj.GetID()), nil
}

// withEmptyProperties is an ARM spec sent with "properties": {} when it has
// no properties of its own.
type withEmptyProperties struct {
	genruntime.ARMResourceSpec
}

func (s withEmptyProperties) MarshalJSON() ([]byte, error) {
	raw, err := json.Marshal(s.ARMResourceSpec)
	if err != nil {
		return nil, err
	}
	body := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	if _, ok := body["properties"]; !ok {
		body["properties"] = json.RawMessage(`{}`)
	}
	return json.Marshal(body)
}
