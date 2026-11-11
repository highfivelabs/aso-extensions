// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// The reconciler of a DirectoryRoleAssignment, on the base reconciler of
// Azure Service Operator pull request #5821 (reconciler_base.go) and shaped
// as ASO's SecurityGroupReconciler is at that head (CreateOrUpdate: the ID
// from the resource-id annotation, else adoption, else creation; Delete
// unless creation is not allowed; UpdateStatus from a GET). A role
// assignment is never updated: Microsoft Graph has no PATCH for one, and
// the CRD refuses changes to what was sent.

package entra

import (
	"context"
	"strings"

	"github.com/go-logr/logr"
	msgraphsdk "github.com/microsoftgraph/msgraph-beta-sdk-go"
	msgraphmodels "github.com/microsoftgraph/msgraph-beta-sdk-go/models"
	"github.com/microsoftgraph/msgraph-beta-sdk-go/rolemanagement"
	"github.com/rotisserie/eris"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/Azure/azure-service-operator/v2/pkg/common/annotations"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	entrav1 "github.com/highfivelabs/aso-extensions/apis/entra/v1"
)

const logStatus = 0

// DirectoryRoleAssignmentReconciler reconciles a DirectoryRoleAssignment.
type DirectoryRoleAssignmentReconciler struct {
	reconcilerBase[*entrav1.DirectoryRoleAssignment, msgraphmodels.UnifiedRoleAssignmentable]
	ResourceResolver *outoftree.Resolver
	Clients          *GraphClientCache
}

var _ genruntime.Reconciler = &DirectoryRoleAssignmentReconciler{}

// NewDirectoryRoleAssignmentReconciler makes the reconciler.
func NewDirectoryRoleAssignmentReconciler(
	kubeClient outoftree.KubeClient,
	resourceResolver *outoftree.Resolver,
	positiveConditions *conditions.PositiveConditionBuilder,
	credentialProvider outoftree.CredentialProvider,
) *DirectoryRoleAssignmentReconciler {
	return &DirectoryRoleAssignmentReconciler{
		ResourceResolver: resourceResolver,
		Clients:          NewGraphClientCache(credentialProvider),
		reconcilerBase: reconcilerBase[*entrav1.DirectoryRoleAssignment, msgraphmodels.UnifiedRoleAssignmentable]{
			ReconcilerCommon: outoftree.ReconcilerCommon{
				KubeClient:         kubeClient,
				PositiveConditions: positiveConditions,
			},
		},
	}
}

// CreateOrUpdate creates or adopts the assignment; an assignment that
// already has its ID is only read back.
func (r *DirectoryRoleAssignmentReconciler) CreateOrUpdate(
	ctx context.Context,
	log logr.Logger,
	_ record.EventRecorder,
	obj genruntime.MetaObject,
	_ annotations.ResolvedReconcilePolicies,
) (ctrl.Result, error) {
	assignment, err := r.asResource(obj)
	if err != nil {
		return ctrl.Result{}, eris.Wrapf(err, "creating directory role assignment %s", obj.GetName())
	}

	client, err := r.Clients.GetClient(ctx, assignment)
	if err != nil {
		return ctrl.Result{}, eris.Wrap(err, "creating Microsoft Graph client")
	}

	// If we already know the Graph ID of the assignment (captured in an
	// annotation), it exists or was deleted outside Kubernetes.
	if id, ok := getEntraID(assignment); ok {
		found, err := r.loadAssignmentByID(ctx, client, id)
		if err != nil {
			return ctrl.Result{}, eris.Wrapf(err, "getting directory role assignment %s", id)
		}
		if found != nil {
			assignFromGraph(&assignment.Status, found)
			return ctrl.Result{}, nil
		}
		if !canCreate(assignment.Spec.OperatorSpec) {
			return ctrl.Result{}, eris.Errorf("directory role assignment %s no longer exists, and creationMode does not allow creating it", id)
		}
		log.V(logStatus).Info("Directory role assignment was deleted outside Kubernetes; creating it again", "id", id)
	}

	want, err := r.desired(ctx, assignment)
	if err != nil {
		return ctrl.Result{}, err
	}

	// If we're allowed to adopt the assignment, we can try to find it
	if canAdopt(assignment.Spec.OperatorSpec) {
		found, err := r.tryAdopt(ctx, client, want)
		if err != nil {
			return ctrl.Result{}, eris.Wrapf(err, "trying to adopt directory role assignment %s", assignment.Name)
		}
		if found != nil {
			log.V(logStatus).Info("Adopted existing directory role assignment", "id", deref(found.GetId()))
			setEntraID(assignment, deref(found.GetId()))
			assignFromGraph(&assignment.Status, found)
			return ctrl.Result{}, nil
		}
	}

	// If we can't adopt, or didn't find an assignment to adopt, we can create a new one
	if !canCreate(assignment.Spec.OperatorSpec) {
		return ctrl.Result{}, eris.Errorf("directory role assignment %s not found for adoption", assignment.Name)
	}

	log.V(logStatus).Info("Creating directory role assignment")
	created, err := client.RoleManagement().Directory().RoleAssignments().Post(ctx, want, nil)
	if err != nil {
		return ctrl.Result{}, eris.Wrapf(err, "failed to create directory role assignment %s", assignment.Name)
	}
	if created == nil || deref(created.GetId()) == "" {
		return ctrl.Result{}, eris.Errorf("Microsoft Graph returned no ID for directory role assignment %s", assignment.Name)
	}
	setEntraID(assignment, *created.GetId())
	assignFromGraph(&assignment.Status, created)
	return ctrl.Result{}, nil
}

// Delete removes the assignment from Microsoft Graph, unless creation is not
// allowed (AdoptOnly), as ASO's Entra kinds do.
func (r *DirectoryRoleAssignmentReconciler) Delete(
	ctx context.Context,
	log logr.Logger,
	_ record.EventRecorder,
	obj genruntime.MetaObject,
) (ctrl.Result, error) {
	assignment, err := r.asResource(obj)
	if err != nil {
		return ctrl.Result{}, eris.Wrapf(err, "deleting directory role assignment %s", obj.GetName())
	}
	if !canCreate(assignment.Spec.OperatorSpec) {
		return ctrl.Result{}, nil
	}

	// If we don't know the Graph ID of the assignment, there's nothing to do.
	id, ok := getEntraID(assignment)
	if !ok {
		return ctrl.Result{}, nil
	}

	client, err := r.Clients.GetClient(ctx, assignment)
	if err != nil {
		return ctrl.Result{}, eris.Wrap(err, "creating Microsoft Graph client")
	}

	log.V(logStatus).Info("Deleting directory role assignment", "id", id)
	err = client.RoleManagement().Directory().RoleAssignments().ByUnifiedRoleAssignmentId(id).Delete(ctx, nil)
	if err != nil && !isNotFound(err) {
		return ctrl.Result{}, eris.Wrapf(err, "failed to delete directory role assignment %s", id)
	}

	return ctrl.Result{}, nil
}

// Claim has nothing to do: the assignment's ID is known only once it exists.
func (r *DirectoryRoleAssignmentReconciler) Claim(
	_ context.Context,
	_ logr.Logger,
	_ record.EventRecorder,
	_ genruntime.MetaObject,
) error {
	return nil
}

// UpdateStatus reads the assignment back from Microsoft Graph.
func (r *DirectoryRoleAssignmentReconciler) UpdateStatus(
	ctx context.Context,
	log logr.Logger,
	_ record.EventRecorder,
	obj genruntime.MetaObject,
	_ annotations.ResolvedReconcilePolicies,
) error {
	assignment, err := r.asResource(obj)
	if err != nil {
		return eris.Wrapf(err, "updating status of directory role assignment %s", obj.GetName())
	}

	id, ok := getEntraID(assignment)
	if !ok {
		log.V(logStatus).Info("No Graph ID found for directory role assignment, skipping status update")
		return nil
	}

	client, err := r.Clients.GetClient(ctx, assignment)
	if err != nil {
		return eris.Wrap(err, "creating Microsoft Graph client")
	}

	found, err := r.loadAssignmentByID(ctx, client, id)
	if err != nil {
		return eris.Wrapf(err, "failed to update status of directory role assignment %s", id)
	}
	if found == nil {
		// Probably in the midst of deleting it
		log.V(logStatus).Info("Directory role assignment no longer exists, skipping status update")
		return nil
	}

	assignFromGraph(&assignment.Status, found)
	return nil
}

// desired is the assignment the spec asks for, the principal resolved.
func (r *DirectoryRoleAssignmentReconciler) desired(
	ctx context.Context,
	assignment *entrav1.DirectoryRoleAssignment,
) (msgraphmodels.UnifiedRoleAssignmentable, error) {
	spec := &assignment.Spec
	principalID := deref(spec.PrincipalId)
	if spec.PrincipalIdFromConfig != nil {
		resolved, err := r.ResourceResolver.ResolveResourceConfigMapReferences(ctx, assignment)
		if err != nil {
			return nil, eris.Wrapf(err, "failed resolving principalIdFromConfig for directory role assignment %s", assignment.Name)
		}
		principalID, err = resolved.Lookup(*spec.PrincipalIdFromConfig)
		if err != nil {
			return nil, eris.Wrap(err, "failed resolving principalIdFromConfig")
		}
	}
	if principalID == "" {
		return nil, eris.New("missing principalId or principalIdFromConfig")
	}

	want := msgraphmodels.NewUnifiedRoleAssignment()
	want.SetPrincipalId(&principalID)
	want.SetRoleDefinitionId(spec.RoleDefinitionId)
	want.SetDirectoryScopeId(spec.DirectoryScopeId)
	want.SetAppScopeId(spec.AppScopeId)
	want.SetCondition(spec.Condition)
	return want, nil
}

// tryAdopt finds an existing assignment of the same role to the same
// principal at the same scope. Graph filters by principal and role; the
// scope and condition are compared here.
func (r *DirectoryRoleAssignmentReconciler) tryAdopt(
	ctx context.Context,
	client *msgraphsdk.GraphServiceClient,
	want msgraphmodels.UnifiedRoleAssignmentable,
) (msgraphmodels.UnifiedRoleAssignmentable, error) {
	filter := eqFilter("principalId", deref(want.GetPrincipalId())) + " and " + eqFilter("roleDefinitionId", deref(want.GetRoleDefinitionId()))
	assignments := client.RoleManagement().Directory().RoleAssignments()
	fetchPage := func(ctx context.Context, filter string, nextLink string) (msgraphmodels.UnifiedRoleAssignmentCollectionResponseable, error) {
		if nextLink != "" {
			return assignments.WithUrl(nextLink).Get(ctx, nil)
		}
		return assignments.Get(ctx, &rolemanagement.DirectoryRoleAssignmentsRequestBuilderGetRequestConfiguration{
			QueryParameters: &rolemanagement.DirectoryRoleAssignmentsRequestBuilderGetQueryParameters{Filter: &filter},
		})
	}
	readPage := func(page msgraphmodels.UnifiedRoleAssignmentCollectionResponseable) ([]msgraphmodels.UnifiedRoleAssignmentable, *string) {
		return page.GetValue(), page.GetOdataNextLink()
	}
	found, err := query[*entrav1.DirectoryRoleAssignment](ctx, filter, fetchPage, readPage)
	if err != nil {
		return nil, err
	}

	var matches []msgraphmodels.UnifiedRoleAssignmentable
	for _, a := range found {
		if strings.EqualFold(deref(a.GetDirectoryScopeId()), deref(want.GetDirectoryScopeId())) &&
			strings.EqualFold(deref(a.GetAppScopeId()), deref(want.GetAppScopeId())) &&
			deref(a.GetCondition()) == deref(want.GetCondition()) {
			matches = append(matches, a)
		}
	}
	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		return matches[0], nil
	default:
		return nil, eris.Errorf("%d existing directory role assignments match", len(matches))
	}
}

func (r *DirectoryRoleAssignmentReconciler) loadAssignmentByID(
	ctx context.Context,
	client *msgraphsdk.GraphServiceClient,
	id string,
) (msgraphmodels.UnifiedRoleAssignmentable, error) {
	fetch := func(ctx context.Context, id string) (msgraphmodels.UnifiedRoleAssignmentable, error) {
		return client.RoleManagement().Directory().RoleAssignments().ByUnifiedRoleAssignmentId(id).Get(ctx, nil)
	}
	return r.loadByID(ctx, id, fetch)
}

func assignFromGraph(status *entrav1.DirectoryRoleAssignmentStatus, a msgraphmodels.UnifiedRoleAssignmentable) {
	status.Id = a.GetId()
	status.PrincipalId = a.GetPrincipalId()
	status.RoleDefinitionId = a.GetRoleDefinitionId()
	status.DirectoryScopeId = a.GetDirectoryScopeId()
	status.AppScopeId = a.GetAppScopeId()
	status.Condition = a.GetCondition()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
