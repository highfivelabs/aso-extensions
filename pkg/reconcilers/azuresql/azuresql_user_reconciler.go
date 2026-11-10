/*
Copyright (c) Microsoft Corporation.
Licensed under the MIT license.
*/

// Modified by High Five Labs: from Azure Service Operator's
// v2/internal/reconcilers/azuresql/azuresql_user_reconciler.go, with the
// aadUser connector of ASO pull request #5104 (head 1219643c). Our User type
// (sql.azure.hfl.dev); ASO's framework through pkg/outoftree (the patch in
// patches/azure-service-operator/); the user name is User.AzureName(), which
// falls back to metadata.name (ASO's defaulting webhook does that; we run
// none).

package sql

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/rotisserie/eris"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/Azure/azure-service-operator/v2/pkg/common/annotations"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/core"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	asosql "github.com/highfivelabs/aso-extensions/apis/sql/v1"
)

// Here's some useful reading:
// https://azure.microsoft.com/en-us/blog/adding-users-to-your-sql-azure-database/
// https://learn.microsoft.com/en-us/azure/azure-sql/database/logins-create-manage?view=azuresql

// Test logging in as an AAD User:
// https://learn.microsoft.com/en-us/azure/azure-sql/database/authentication-aad-configure?view=azuresql&tabs=azure-powershell#azure-ad-token

// There's LOGIN's and USERs. We only support USERS. LOGINs are server-wide whereas USERs are DB scoped.
// Here are least some built in DB roles:
// # db_owner, db_securityadmin, db_accessadmin, db_backupoperator, db_ddladmin, db_datawriter, db_datareader, db_denydatawriter, db_denydatareader

// Log levels, as ASO's internal/logging.
const (
	logStatus  = 0
	logVerbose = 2
)

var _ genruntime.Reconciler = &AzureSQLUserReconciler{}

type AzureSQLUserReconciler struct {
	outoftree.ARMOwnedResourceReconcilerCommon
	ResourceResolver   *outoftree.Resolver
	CredentialProvider outoftree.CredentialProvider
	Config             outoftree.ConfigValues
}

func NewAzureSQLUserReconciler(
	kubeClient outoftree.KubeClient,
	resourceResolver *outoftree.Resolver,
	positiveConditions *conditions.PositiveConditionBuilder,
	credentialProvider outoftree.CredentialProvider,
	cfg outoftree.ConfigValues,
) *AzureSQLUserReconciler {
	return &AzureSQLUserReconciler{
		ResourceResolver:   resourceResolver,
		CredentialProvider: credentialProvider,
		Config:             cfg,
		ARMOwnedResourceReconcilerCommon: outoftree.ARMOwnedResourceReconcilerCommon{
			ResourceResolver: resourceResolver,
			ReconcilerCommon: outoftree.ReconcilerCommon{
				KubeClient:         kubeClient,
				PositiveConditions: positiveConditions,
			},
		},
	}
}

func (r *AzureSQLUserReconciler) asUser(obj genruntime.MetaObject) (*asosql.User, error) {
	typedObj, ok := obj.(*asosql.User)
	if !ok {
		return nil, eris.Errorf("cannot modify resource that is not of type *asosql.User. Type is %T", obj)
	}

	return typedObj, nil
}

func (r *AzureSQLUserReconciler) CreateOrUpdate(
	ctx context.Context,
	log logr.Logger,
	eventRecorder record.EventRecorder,
	obj genruntime.MetaObject,
	_ annotations.ResolvedReconcilePolicies,
) (ctrl.Result, error) {
	user, err := r.asUser(obj)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Augment Log
	log = log.WithValues("azureName", user.AzureName())
	connector, err := r.newDBConnector(log, user)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = connector.CreateOrUpdate(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *AzureSQLUserReconciler) Delete(ctx context.Context, log logr.Logger, eventRecorder record.EventRecorder, obj genruntime.MetaObject) (ctrl.Result, error) {
	user, err := r.asUser(obj)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Augment Log
	log = log.WithValues("azureName", user.AzureName())

	log.V(logStatus).Info("Starting delete of resource")

	// Check that this objects owner still exists
	// This is an optimization to avoid excess requests to Azure.
	_, err = r.ResourceResolver.ResolveOwner(ctx, user)
	if err != nil {
		var typedErr *core.ReferenceNotFound
		if eris.As(err, &typedErr) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	connector, err := r.newDBConnector(log, user)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = connector.Delete(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *AzureSQLUserReconciler) Claim(ctx context.Context, log logr.Logger, eventRecorder record.EventRecorder, obj genruntime.MetaObject) error {
	user, err := r.asUser(obj)
	if err != nil {
		return err
	}

	err = r.ClaimResource(ctx, log, user)
	if err != nil {
		return err
	}

	return nil
}

func (r *AzureSQLUserReconciler) UpdateStatus(
	ctx context.Context,
	log logr.Logger,
	eventRecorder record.EventRecorder,
	obj genruntime.MetaObject,
	_ annotations.ResolvedReconcilePolicies,
) error {
	user, err := r.asUser(obj)
	if err != nil {
		return err
	}

	connector, err := r.newDBConnector(log, user)
	if err != nil {
		return err
	}

	exists, err := connector.Exists(ctx)
	if err != nil {
		return err
	}

	if !exists {
		err = eris.Errorf("user %s does not exist", user.AzureName())
		err = conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityWarning, conditions.ReasonAzureResourceNotFound)
		return err
	}

	return nil
}

func (r *AzureSQLUserReconciler) newDBConnector(log logr.Logger, user *asosql.User) (Connector, error) {
	if user.Spec.LocalUser != nil {
		return &localUser{
			user:               user,
			resourceResolver:   r.ResourceResolver,
			credentialProvider: r.CredentialProvider,
			log:                log,
		}, nil
	}

	if user.Spec.AADUser != nil {
		return &aadUser{
			user:               user,
			resourceResolver:   r.ResourceResolver,
			credentialProvider: r.CredentialProvider,
			log:                log,
		}, nil
	}

	// This is also enforced with a CEL rule
	err := eris.Errorf("unknown user type, user must be LocalUser or AADUser")
	return nil, conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityError, conditions.ReasonFailed)
}
