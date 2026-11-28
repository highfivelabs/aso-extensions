/*
Copyright (c) Microsoft Corporation.
Licensed under the MIT license.
*/

// Modified by High Five Labs: ASO's
// v2/api/documentdb/v1api20240815/webhook/sql_role_assignment_defaults.go
// (defaultAzureName), for the Cassandra, Gremlin, MongoDB (Entra) and Table
// API role definitions and assignments, whose names are GUIDs as a SQL role
// assignment's is. There is no webhook here, so the defaulting runs as a
// Claimer (as generator/_extensions/authorization/customizations does for
// PIM): the claim's commit records the name in spec.azureName. Changed: the
// owner-scoped string is ASO's current MakeUniqueOwnerScopedString, not the
// Legacy one ASO keeps for compatibility with names it made before. Kept in
// generator/_extensions and copied into apis/arm by hack/generate-arm.sh,
// which deletes apis/arm on every run.

package customizations

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/rotisserie/eris"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/extensions"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	v20260315 "github.com/highfivelabs/aso-extensions/apis/arm/documentdb/v20260315"
)

// defaultAzureName gives an object without an azureName the UUID ASO's
// webhook would: of its name, owner, group, kind and namespace.
func defaultAzureName(azureName *string, name, namespace string, owner *genruntime.ResourceReference, gk schema.GroupKind) {
	// If owner is not set we can't default AzureName, but the request will be rejected anyway for lack of owner.
	if owner == nil || *azureName != "" {
		return
	}
	*azureName = outoftree.MakeUUIDName(name, outoftree.MakeUniqueOwnerScopedString(owner, gk, namespace, name))
}

var _ extensions.Claimer = &DatabaseAccountsCassandraRoleDefinitionExtension{}

// Claim defaults the CassandraRoleDefinition's azureName, then claims it as ASO does.
func (extension *DatabaseAccountsCassandraRoleDefinitionExtension) Claim(ctx context.Context, log logr.Logger, obj genruntime.ARMOwnedMetaObject, next extensions.ClaimFunc) error {
	o, ok := obj.(*v20260315.DatabaseAccountsCassandraRoleDefinition)
	if !ok {
		return eris.Errorf("Cannot run DatabaseAccountsCassandraRoleDefinitionExtension.Claim() with unexpected resource type %T", obj)
	}
	if o.Spec.Owner != nil {
		defaultAzureName(&o.Spec.AzureName, o.Name, o.Namespace, o.Owner(), v20260315.GroupVersion.WithKind("DatabaseAccountsCassandraRoleDefinition").GroupKind())
	}
	log.V(1).Info("azureName", "azureName", o.Spec.AzureName)
	return next(ctx, log, obj)
}

var _ extensions.Claimer = &DatabaseAccountsCassandraRoleAssignmentExtension{}

// Claim defaults the CassandraRoleAssignment's azureName, then claims it as ASO does.
func (extension *DatabaseAccountsCassandraRoleAssignmentExtension) Claim(ctx context.Context, log logr.Logger, obj genruntime.ARMOwnedMetaObject, next extensions.ClaimFunc) error {
	o, ok := obj.(*v20260315.DatabaseAccountsCassandraRoleAssignment)
	if !ok {
		return eris.Errorf("Cannot run DatabaseAccountsCassandraRoleAssignmentExtension.Claim() with unexpected resource type %T", obj)
	}
	if o.Spec.Owner != nil {
		defaultAzureName(&o.Spec.AzureName, o.Name, o.Namespace, o.Owner(), v20260315.GroupVersion.WithKind("DatabaseAccountsCassandraRoleAssignment").GroupKind())
	}
	log.V(1).Info("azureName", "azureName", o.Spec.AzureName)
	return next(ctx, log, obj)
}

var _ extensions.Claimer = &DatabaseAccountsGremlinRoleDefinitionExtension{}

// Claim defaults the GremlinRoleDefinition's azureName, then claims it as ASO does.
func (extension *DatabaseAccountsGremlinRoleDefinitionExtension) Claim(ctx context.Context, log logr.Logger, obj genruntime.ARMOwnedMetaObject, next extensions.ClaimFunc) error {
	o, ok := obj.(*v20260315.DatabaseAccountsGremlinRoleDefinition)
	if !ok {
		return eris.Errorf("Cannot run DatabaseAccountsGremlinRoleDefinitionExtension.Claim() with unexpected resource type %T", obj)
	}
	if o.Spec.Owner != nil {
		defaultAzureName(&o.Spec.AzureName, o.Name, o.Namespace, o.Owner(), v20260315.GroupVersion.WithKind("DatabaseAccountsGremlinRoleDefinition").GroupKind())
	}
	log.V(1).Info("azureName", "azureName", o.Spec.AzureName)
	return next(ctx, log, obj)
}

var _ extensions.Claimer = &DatabaseAccountsGremlinRoleAssignmentExtension{}

// Claim defaults the GremlinRoleAssignment's azureName, then claims it as ASO does.
func (extension *DatabaseAccountsGremlinRoleAssignmentExtension) Claim(ctx context.Context, log logr.Logger, obj genruntime.ARMOwnedMetaObject, next extensions.ClaimFunc) error {
	o, ok := obj.(*v20260315.DatabaseAccountsGremlinRoleAssignment)
	if !ok {
		return eris.Errorf("Cannot run DatabaseAccountsGremlinRoleAssignmentExtension.Claim() with unexpected resource type %T", obj)
	}
	if o.Spec.Owner != nil {
		defaultAzureName(&o.Spec.AzureName, o.Name, o.Namespace, o.Owner(), v20260315.GroupVersion.WithKind("DatabaseAccountsGremlinRoleAssignment").GroupKind())
	}
	log.V(1).Info("azureName", "azureName", o.Spec.AzureName)
	return next(ctx, log, obj)
}

var _ extensions.Claimer = &DatabaseAccountsMongoMIRoleDefinitionExtension{}

// Claim defaults the MongoMIRoleDefinition's azureName, then claims it as ASO does.
func (extension *DatabaseAccountsMongoMIRoleDefinitionExtension) Claim(ctx context.Context, log logr.Logger, obj genruntime.ARMOwnedMetaObject, next extensions.ClaimFunc) error {
	o, ok := obj.(*v20260315.DatabaseAccountsMongoMIRoleDefinition)
	if !ok {
		return eris.Errorf("Cannot run DatabaseAccountsMongoMIRoleDefinitionExtension.Claim() with unexpected resource type %T", obj)
	}
	if o.Spec.Owner != nil {
		defaultAzureName(&o.Spec.AzureName, o.Name, o.Namespace, o.Owner(), v20260315.GroupVersion.WithKind("DatabaseAccountsMongoMIRoleDefinition").GroupKind())
	}
	log.V(1).Info("azureName", "azureName", o.Spec.AzureName)
	return next(ctx, log, obj)
}

var _ extensions.Claimer = &DatabaseAccountsMongoMIRoleAssignmentExtension{}

// Claim defaults the MongoMIRoleAssignment's azureName, then claims it as ASO does.
func (extension *DatabaseAccountsMongoMIRoleAssignmentExtension) Claim(ctx context.Context, log logr.Logger, obj genruntime.ARMOwnedMetaObject, next extensions.ClaimFunc) error {
	o, ok := obj.(*v20260315.DatabaseAccountsMongoMIRoleAssignment)
	if !ok {
		return eris.Errorf("Cannot run DatabaseAccountsMongoMIRoleAssignmentExtension.Claim() with unexpected resource type %T", obj)
	}
	if o.Spec.Owner != nil {
		defaultAzureName(&o.Spec.AzureName, o.Name, o.Namespace, o.Owner(), v20260315.GroupVersion.WithKind("DatabaseAccountsMongoMIRoleAssignment").GroupKind())
	}
	log.V(1).Info("azureName", "azureName", o.Spec.AzureName)
	return next(ctx, log, obj)
}

var _ extensions.Claimer = &DatabaseAccountsTableRoleDefinitionExtension{}

// Claim defaults the TableRoleDefinition's azureName, then claims it as ASO does.
func (extension *DatabaseAccountsTableRoleDefinitionExtension) Claim(ctx context.Context, log logr.Logger, obj genruntime.ARMOwnedMetaObject, next extensions.ClaimFunc) error {
	o, ok := obj.(*v20260315.DatabaseAccountsTableRoleDefinition)
	if !ok {
		return eris.Errorf("Cannot run DatabaseAccountsTableRoleDefinitionExtension.Claim() with unexpected resource type %T", obj)
	}
	if o.Spec.Owner != nil {
		defaultAzureName(&o.Spec.AzureName, o.Name, o.Namespace, o.Owner(), v20260315.GroupVersion.WithKind("DatabaseAccountsTableRoleDefinition").GroupKind())
	}
	log.V(1).Info("azureName", "azureName", o.Spec.AzureName)
	return next(ctx, log, obj)
}

var _ extensions.Claimer = &DatabaseAccountsTableRoleAssignmentExtension{}

// Claim defaults the TableRoleAssignment's azureName, then claims it as ASO does.
func (extension *DatabaseAccountsTableRoleAssignmentExtension) Claim(ctx context.Context, log logr.Logger, obj genruntime.ARMOwnedMetaObject, next extensions.ClaimFunc) error {
	o, ok := obj.(*v20260315.DatabaseAccountsTableRoleAssignment)
	if !ok {
		return eris.Errorf("Cannot run DatabaseAccountsTableRoleAssignmentExtension.Claim() with unexpected resource type %T", obj)
	}
	if o.Spec.Owner != nil {
		defaultAzureName(&o.Spec.AzureName, o.Name, o.Namespace, o.Owner(), v20260315.GroupVersion.WithKind("DatabaseAccountsTableRoleAssignment").GroupKind())
	}
	log.V(1).Info("azureName", "azureName", o.Spec.AzureName)
	return next(ctx, log, obj)
}
