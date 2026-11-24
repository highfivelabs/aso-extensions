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

	v20210601 "github.com/highfivelabs/aso-extensions/apis/arm/synapse/v20210601"
)

var (
	_ extensions.Deleter = &WorkspacesAzureADOnlyAuthenticationExtension{}
	_ extensions.Deleter = &WorkspacesManagedIdentitySqlControlSettingExtension{}
	_ extensions.Deleter = &WorkspacesAuditingSettingExtension{}
	_ extensions.Deleter = &WorkspacesExtendedAuditingSettingExtension{}
	_ extensions.Deleter = &WorkspacesSecurityAlertPolicyExtension{}
	_ extensions.Deleter = &WorkspacesEncryptionProtectorExtension{}
	_ extensions.Deleter = &WorkspacesDedicatedSQLMinimalTlsSettingExtension{}
	_ extensions.Deleter = &WorkspacesSqlPoolsGeoBackupPolicyExtension{}
	_ extensions.Deleter = &WorkspacesSqlPoolsMaintenanceWindowExtension{}
	_ extensions.Deleter = &WorkspacesSqlPoolsTransparentDataEncryptionExtension{}
	_ extensions.Deleter = &WorkspacesSqlPoolsAuditingSettingExtension{}
	_ extensions.Deleter = &WorkspacesSqlPoolsExtendedAuditingSettingExtension{}
	_ extensions.Deleter = &WorkspacesSqlPoolsSecurityAlertPolicyExtension{}
	_ extensions.Deleter = &WorkspacesSqlPoolsDataMaskingPolicyExtension{}
	_ extensions.Deleter = &WorkspacesSqlPoolsDataMaskingPolicyRuleExtension{}
)

// A Synapse workspace's Entra-only authentication, managed identity SQL
// control setting, SQL auditing and extended auditing settings, security
// alert policy, encryption protector and dedicated SQL minimal TLS setting
// (docs/plan.md f80) exist as long as the workspace does: Azure makes each
// with it, has no DELETE for them, and deletes them with it. Deleting the
// Kubernetes object stops managing the setting; the values last PUT stay
// until the workspace goes (as a site's config sections are left, web's
// site_config_extensions.go). So do a dedicated SQL pool's geo-backup
// policy, maintenance window, transparent data encryption, auditing and
// extended auditing settings, security alert policy, data masking policy
// and data masking rules (f81), which go with the pool; a rule to stop
// masking is set to ruleState Disabled, not deleted.

func (extension *WorkspacesAzureADOnlyAuthenticationExtension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20210601.WorkspacesAzureADOnlyAuthentication); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithWorkspace(log, obj)
}

func (extension *WorkspacesManagedIdentitySqlControlSettingExtension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20210601.WorkspacesManagedIdentitySqlControlSetting); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithWorkspace(log, obj)
}

func (extension *WorkspacesAuditingSettingExtension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20210601.WorkspacesAuditingSetting); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithWorkspace(log, obj)
}

func (extension *WorkspacesExtendedAuditingSettingExtension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20210601.WorkspacesExtendedAuditingSetting); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithWorkspace(log, obj)
}

func (extension *WorkspacesSecurityAlertPolicyExtension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20210601.WorkspacesSecurityAlertPolicy); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithWorkspace(log, obj)
}

func (extension *WorkspacesEncryptionProtectorExtension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20210601.WorkspacesEncryptionProtector); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithWorkspace(log, obj)
}

func (extension *WorkspacesDedicatedSQLMinimalTlsSettingExtension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20210601.WorkspacesDedicatedSQLMinimalTlsSetting); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithWorkspace(log, obj)
}

func (extension *WorkspacesSqlPoolsGeoBackupPolicyExtension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20210601.WorkspacesSqlPoolsGeoBackupPolicy); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithWorkspace(log, obj)
}

func (extension *WorkspacesSqlPoolsMaintenanceWindowExtension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20210601.WorkspacesSqlPoolsMaintenanceWindow); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithWorkspace(log, obj)
}

func (extension *WorkspacesSqlPoolsTransparentDataEncryptionExtension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20210601.WorkspacesSqlPoolsTransparentDataEncryption); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithWorkspace(log, obj)
}

func (extension *WorkspacesSqlPoolsAuditingSettingExtension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20210601.WorkspacesSqlPoolsAuditingSetting); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithWorkspace(log, obj)
}

func (extension *WorkspacesSqlPoolsExtendedAuditingSettingExtension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20210601.WorkspacesSqlPoolsExtendedAuditingSetting); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithWorkspace(log, obj)
}

func (extension *WorkspacesSqlPoolsSecurityAlertPolicyExtension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20210601.WorkspacesSqlPoolsSecurityAlertPolicy); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithWorkspace(log, obj)
}

func (extension *WorkspacesSqlPoolsDataMaskingPolicyExtension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20210601.WorkspacesSqlPoolsDataMaskingPolicy); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithWorkspace(log, obj)
}

func (extension *WorkspacesSqlPoolsDataMaskingPolicyRuleExtension) Delete(
	ctx context.Context, log logr.Logger, resolver *outoftree.Resolver, armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject, next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	if _, ok := obj.(*v20210601.WorkspacesSqlPoolsDataMaskingPolicyRule); !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	return leaveWithWorkspace(log, obj)
}

func leaveWithWorkspace(log logr.Logger, obj genruntime.ARMMetaObject) (extensions.DeleteResult, error) {
	id, _ := genruntime.GetResourceID(obj)
	log.V(1).Info("Leaving the setting in Azure: it is deleted with its workspace or pool", "id", id)
	return extensions.DeleteCompleted(), nil
}
