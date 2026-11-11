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

	v20180501 "github.com/highfivelabs/aso-extensions/apis/arm/network/v20180501"
	v20240601 "github.com/highfivelabs/aso-extensions/apis/arm/network/v20240601"
)

var (
	_ extensions.Deleter = &DnsZonesSOARecordExtension{}
	_ extensions.Deleter = &PrivateDnsZonesSOARecordExtension{}
)

// Delete leaves a public zone's SOA record set in Azure: Azure makes it
// with the zone, refuses to delete it alone, and deletes it with the zone.
// Deleting the Kubernetes object stops managing it; the values last PUT
// stay until the zone goes.
func (extension *DnsZonesSOARecordExtension) Delete(
	ctx context.Context,
	log logr.Logger,
	resolver *outoftree.Resolver,
	armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject,
	next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20180501.DnsZonesSOARecord); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveSOA(log, obj)
}

// Delete leaves a private zone's SOA record set in Azure, as a public
// zone's is left.
func (extension *PrivateDnsZonesSOARecordExtension) Delete(
	ctx context.Context,
	log logr.Logger,
	resolver *outoftree.Resolver,
	armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject,
	next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20240601.PrivateDnsZonesSOARecord); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveSOA(log, obj)
}

func leaveSOA(log logr.Logger, obj genruntime.ARMMetaObject) (extensions.DeleteResult, error) {
	id, _ := genruntime.GetResourceID(obj)
	log.V(1).Info("Leaving the SOA record set in Azure: it is deleted with its zone", "id", id)
	return extensions.DeleteCompleted(), nil
}
