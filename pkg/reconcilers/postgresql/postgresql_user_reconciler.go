/*
Copyright (c) Microsoft Corporation.
Licensed under the MIT license.
*/

// Modified by High Five Labs: from Azure Service Operator's
// v2/internal/reconcilers/postgresql/postgresql_user_reconciler.go. Our
// User type (dbforpostgresql.azure.hfl.dev); ASO's framework through
// pkg/outoftree (the patch in patches/azure-service-operator/); new: aadUser
// (connect as the server's Entra administrator with the credential ASO
// would use for this object, as ASO's MySQL aad_user.go does, and create
// the role with pgaadauth), databasePrivileges and their status; the user
// name falls back to metadata.name when azureName is empty (ASO's defaulting
// webhook does that; we run none); a missing roleOptions means ASO's
// default (LOGIN) instead of an error.

package postgresql

import (
	"context"
	"database/sql"
	"sort"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/go-logr/logr"
	"github.com/rotisserie/eris"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlconversion "sigs.k8s.io/controller-runtime/pkg/conversion"

	dbforpostgressql "github.com/Azure/azure-service-operator/v2/api/dbforpostgresql/v20250801/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/common/annotations"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/core"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	asopostgresql "github.com/highfivelabs/aso-extensions/apis/dbforpostgresql/v1"
	postgresqlutil "github.com/highfivelabs/aso-extensions/pkg/util/postgresql"
)

// Log levels, as ASO's internal/logging.
const (
	logStatus  = 0
	logVerbose = 2
)

var _ genruntime.Reconciler = &PostgreSQLUserReconciler{}

type PostgreSQLUserReconciler struct {
	outoftree.ARMOwnedResourceReconcilerCommon
	ResourceResolver   *outoftree.Resolver
	CredentialProvider outoftree.CredentialProvider
	Config             outoftree.ConfigValues
}

func NewPostgreSQLUserReconciler(
	kubeClient outoftree.KubeClient,
	resourceResolver *outoftree.Resolver,
	positiveConditions *conditions.PositiveConditionBuilder,
	credentialProvider outoftree.CredentialProvider,
	cfg outoftree.ConfigValues,
) *PostgreSQLUserReconciler {
	return &PostgreSQLUserReconciler{
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

func (r *PostgreSQLUserReconciler) asUser(obj genruntime.MetaObject) (*asopostgresql.User, error) {
	typedObj, ok := obj.(*asopostgresql.User)
	if !ok {
		return nil, eris.Errorf("cannot modify resource that is not of type *asopostgresql.User. Type is %T", obj)
	}

	return typedObj, nil
}

func (r *PostgreSQLUserReconciler) CreateOrUpdate(
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

	// Resolve the secrets
	secrets, err := r.ResourceResolver.ResolveResourceSecretReferences(ctx, user)
	if err != nil {
		return ctrl.Result{}, outoftree.ClassifyResolverError(err)
	}

	db, err := r.connectToDB(ctx, log, user, secrets)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer db.Close()

	username := user.AzureName()

	sqlUser, err := postgresqlutil.FindUserIfExist(ctx, db, username)
	if err != nil {
		return ctrl.Result{}, eris.Wrap(err, "failed to find user")
	}

	if user.Spec.AADUser != nil {
		log.V(logStatus).Info("Creating PostgreSql Entra user")
		if sqlUser == nil {
			sqlUser, err = postgresqlutil.CreateAADUser(ctx, db, username, user.Spec.AADUser.ObjectID, user.Spec.AADUser.PrincipalType)
			if err != nil {
				return ctrl.Result{}, eris.Wrap(err, "failed to create user")
			}
		}
	} else {
		log.V(logStatus).Info("Creating PostgreSql user")
		password, err := secrets.LookupFromPtr(user.Spec.LocalUser.Password)
		if err != nil {
			return ctrl.Result{}, eris.Wrap(err, "failed to look up .spec.localUser.Password")
		}

		// Create or update the user. Note that this updates password if it has changed
		if sqlUser == nil {
			sqlUser, err = postgresqlutil.CreateUser(ctx, db, username, password)
			if err != nil {
				return ctrl.Result{}, eris.Wrap(err, "failed to create user")
			}
		} else {
			err = postgresqlutil.UpdateUser(ctx, db, *sqlUser, password)
			if err != nil {
				return ctrl.Result{}, eris.Wrap(err, "failed to update user")
			}
		}
	}

	// ASO's CRD defaults roleOptions to {login: true}; treat a missing one the same.
	roleOptions := postgresqlutil.RoleOptions{Login: true}
	if user.Spec.RoleOptions != nil {
		roleOptions = postgresqlutil.RoleOptions(*user.Spec.RoleOptions)
	}
	// Ensure that the user role options are set
	err = postgresqlutil.ReconcileUserRoleOptions(ctx, db, *sqlUser, roleOptions)
	if err != nil {
		return ctrl.Result{}, eris.Wrap(err, "ensuring user role options")
	}

	// Ensure that the roles are set
	err = postgresqlutil.ReconcileUserServerRoles(ctx, db, *sqlUser, user.Spec.Roles)
	if err != nil {
		return ctrl.Result{}, eris.Wrap(err, "ensuring server roles")
	}

	// Ensure that the database privileges are exactly the listed ones
	err = postgresqlutil.ReconcileUserDatabasePrivileges(ctx, db, sqlUser.Name, databasePrivileges(user))
	if err != nil {
		return ctrl.Result{}, eris.Wrap(err, "ensuring database privileges")
	}

	// ASO's generic reconciler commits the object after CreateOrUpdate
	// (UpdateStatus runs only when the reconcile policy skips), so the
	// status is written here.
	if err := setAppliedDatabasePrivileges(ctx, db, user); err != nil {
		return ctrl.Result{}, err
	}

	log.V(logStatus).Info("Successfully reconciled PostgreSqlUser")

	return ctrl.Result{}, nil
}

func setAppliedDatabasePrivileges(ctx context.Context, db *sql.DB, user *asopostgresql.User) error {
	applied, err := postgresqlutil.DatabasePrivilegesOf(ctx, db, user.AzureName())
	if err != nil {
		return err
	}
	user.Status.AppliedDatabasePrivileges = nil
	if len(applied) > 0 {
		user.Status.AppliedDatabasePrivileges = make(map[string][]asopostgresql.DatabasePrivilege, len(applied))
		for database, privs := range applied {
			sort.Strings(privs)
			values := make([]asopostgresql.DatabasePrivilege, 0, len(privs))
			for _, p := range privs {
				values = append(values, asopostgresql.DatabasePrivilege(p))
			}
			user.Status.AppliedDatabasePrivileges[database] = values
		}
	}
	return nil
}

func databasePrivileges(user *asopostgresql.User) map[string][]string {
	result := make(map[string][]string, len(user.Spec.DatabasePrivileges))
	for database, privs := range user.Spec.DatabasePrivileges {
		values := make([]string, 0, len(privs))
		for _, p := range privs {
			values = append(values, string(p))
		}
		result[database] = values
	}
	return result
}

func (r *PostgreSQLUserReconciler) Delete(ctx context.Context, log logr.Logger, eventRecorder record.EventRecorder, obj genruntime.MetaObject) (ctrl.Result, error) {
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

	secrets, err := r.ResourceResolver.ResolveResourceSecretReferences(ctx, user)
	if err != nil {
		return ctrl.Result{}, err
	}

	db, err := r.connectToDB(ctx, log, user, secrets)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer db.Close()

	// TODO: There's still probably some ways that this user can be deleted but that we don't detect (and
	// TODO: so might cause an error triggering the resource to get stuck).
	// TODO: We check for owner not existing above, but cases where the server is in the process of being
	// TODO: deleted (or all system tables have been wiped?) might also exist...
	err = postgresqlutil.DropUser(ctx, db, user.AzureName())
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *PostgreSQLUserReconciler) Claim(ctx context.Context, log logr.Logger, eventRecorder record.EventRecorder, obj genruntime.MetaObject) error {
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

func (r *PostgreSQLUserReconciler) UpdateStatus(
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

	secrets, err := r.ResourceResolver.ResolveResourceSecretReferences(ctx, user)
	if err != nil {
		return err
	}

	db, err := r.connectToDB(ctx, log, user, secrets)
	if err != nil {
		return err
	}
	defer db.Close()

	exists, err := postgresqlutil.DoesUserExist(ctx, db, user.AzureName())
	if err != nil {
		return err
	}

	if !exists {
		err = eris.Errorf("user %s does not exist", user.AzureName())
		err = conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityWarning, conditions.ReasonAzureResourceNotFound)
		return err
	}

	return setAppliedDatabasePrivileges(ctx, db, user)
}

func (r *PostgreSQLUserReconciler) connectToDB(ctx context.Context, log logr.Logger, user *asopostgresql.User, secrets genruntime.Resolved[genruntime.SecretReference, string]) (*sql.DB, error) {
	// Get the owner - at this point it must exist
	ownerDetails, err := r.ResourceResolver.ResolveOwner(ctx, user)
	if err != nil {
		return nil, eris.Wrapf(err, "resolving owner for user %s", user.Name)
	}

	// Note that this is not actually possible for this type because we don't allow ARMID references for these owners,
	// but protecting against it here anyway.
	if !ownerDetails.FoundKubernetesOwner() {
		return nil, eris.Errorf("user owner must exist in Kubernetes for user %s", user.Name)
	}

	flexibleServer, ok := ownerDetails.Owner.(*dbforpostgressql.FlexibleServer)
	if !ok {
		return nil, eris.Errorf("owner was not type FlexibleServer, instead: %T", ownerDetails)
	}
	// Assertion to ensure that this is still the storage type
	// If this doesn't compile, update the version being imported to the new Hub version
	var _ ctrlconversion.Hub = &dbforpostgressql.FlexibleServer{}

	if flexibleServer.Status.FullyQualifiedDomainName == nil {
		// This possibly means that the server hasn't finished deploying yet
		err = eris.Errorf("owning Flexibleserver %q '.status.fullyQualifiedDomainName' not set. Has the server been provisioned successfully?", flexibleServer.Name)
		return nil, conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityWarning, conditions.ReasonWaitingForOwner)
	}
	serverFQDN := *flexibleServer.Status.FullyQualifiedDomainName

	if user.Spec.AADUser != nil {
		return r.connectToDBAAD(ctx, log, user, serverFQDN)
	}

	adminPassword, err := secrets.LookupFromPtr(user.Spec.LocalUser.ServerAdminPassword)
	if err != nil {
		err = eris.Wrap(err, "failed to look up .spec.localUser.ServerAdminPassword")
		err = conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityWarning, conditions.ReasonSecretNotFound)
		return nil, err
	}

	// Admin User
	adminUser := user.Spec.LocalUser.ServerAdminUsername

	// Connect to the DB
	db, err := postgresqlutil.ConnectToDB(ctx, serverFQDN, postgresqlutil.DefaultMaintanenceDatabase, postgresqlutil.ServerPort, adminUser, adminPassword)
	if err != nil {
		return nil, eris.Wrapf(
			err,
			"failed to connect database. Server: %s, Database: %s, Port: %d, AdminUser: %s",
			serverFQDN,
			postgresqlutil.DefaultMaintanenceDatabase,
			postgresqlutil.ServerPort,
			adminUser,
		)
	}

	return db, nil
}

// connectToDBAAD signs in as the server's Entra administrator with the
// credential ASO's credential provider picks for this object, as ASO's
// MySQL connectToDBAAD does.
func (r *PostgreSQLUserReconciler) connectToDBAAD(ctx context.Context, log logr.Logger, user *asopostgresql.User, fqdn string) (*sql.DB, error) {
	adminUser := user.Spec.AADUser.ServerAdminUsername
	if len(adminUser) == 0 {
		return nil, eris.Errorf("AAD User must specify $.spec.aadUser.serverAdminUsername")
	}

	credential, err := r.CredentialProvider.GetCredential(ctx, user)
	if err != nil {
		return nil, eris.Wrapf(err, "failed to get credential")
	}
	token, err := credential.TokenCredential().GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{postgresqlutil.EntraScope}})
	if err != nil {
		return nil, eris.Wrap(err, "failed to get token from credential")
	}
	log.V(logVerbose).Info("Retrieved token for PostgreSQL", "scope", postgresqlutil.EntraScope, "expires", token.ExpiresOn, "credentialFrom", credential.CredentialFrom())

	db, err := postgresqlutil.ConnectToDBAAD(ctx, fqdn, postgresqlutil.DefaultMaintanenceDatabase, postgresqlutil.ServerPort, adminUser, token.Token)
	if err != nil {
		return nil, eris.Wrapf(
			err,
			"failed to connect database. Server: %s, Database: %s, Port: %d, AdminUser: %s",
			fqdn,
			postgresqlutil.DefaultMaintanenceDatabase,
			postgresqlutil.ServerPort,
			adminUser,
		)
	}

	return db, nil
}
