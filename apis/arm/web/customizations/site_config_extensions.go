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

	v20260715 "github.com/highfivelabs/aso-extensions/apis/arm/web/v20260715"
)

var (
	_ extensions.Deleter = &SitesConfigAuthSettingsV2Extension{}
	_ extensions.Deleter = &SitesConfigLogsExtension{}
	_ extensions.Deleter = &SitesConfigSlotConfigNamesExtension{}
	_ extensions.Deleter = &SitesBasicPublishingCredentialsPolicyFtpExtension{}
	_ extensions.Deleter = &SitesBasicPublishingCredentialsPolicyScmExtension{}
)

// A site's config sections (authsettingsV2, logs, slotConfigNames) and its
// publishing credentials policies (ftp, scm) exist as long as the site does:
// Azure makes them with it, has no DELETE for them, and deletes them with
// it. Deleting the Kubernetes object stops managing the section; the values
// last PUT stay until the site goes (as a zone's SOA record set is left,
// network's soa_record_extensions.go).

func (extension *SitesConfigAuthSettingsV2Extension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20260715.SitesConfigAuthSettingsV2); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithSite(log, obj)
}

func (extension *SitesConfigLogsExtension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20260715.SitesConfigLogs); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithSite(log, obj)
}

func (extension *SitesConfigSlotConfigNamesExtension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20260715.SitesConfigSlotConfigNames); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithSite(log, obj)
}

func (extension *SitesBasicPublishingCredentialsPolicyFtpExtension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20260715.SitesBasicPublishingCredentialsPolicyFtp); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithSite(log, obj)
}

func (extension *SitesBasicPublishingCredentialsPolicyScmExtension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20260715.SitesBasicPublishingCredentialsPolicyScm); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithSite(log, obj)
}

func leaveWithSite(log logr.Logger, obj genruntime.ARMMetaObject) (extensions.DeleteResult, error) {
	id, _ := genruntime.GetResourceID(obj)
	log.V(1).Info("Leaving the site's section in Azure: it is deleted with the site", "id", id)
	return extensions.DeleteCompleted(), nil
}
