// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

// Package recoveryservices reconciles VaultsProtectedItem: backup turned on
// for an Azure virtual machine in a Recovery Services vault
// (Microsoft.RecoveryServices/vaults/backupFabrics/protectionContainers/protectedItems,
// API 2026-07-01). ASO's generator cannot own it (the fixed backupFabrics/Azure
// segment is no resource, so ASO's owner chain cannot build its ARM ID), so it
// is written by hand on ASO's generic reconciler: it honours ASO's
// annotations (reconcile-policy, credential-from) and conditions, claims as
// ASO does (waits for a Ready vault, then sets the owner reference), and
// calls Resource Manager through ASO's own ARM client and connection factory
// with the credential ASO picks for the object. PUT and DELETE are
// long-running; a pending operation is kept in ASO's poller annotations and
// followed on the next reconcile, as ASO's ARM reconciler does.
//
// Azure's documented flow for a virtual machine is: refresh the vault's view
// of the subscription's machines (POST backupFabrics/Azure/refreshContainers),
// then PUT the protected item; Azure registers the machine's protection
// container itself. The reconciler refreshes once before it first PUTs an
// item Azure does not have.
package recoveryservices

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/go-logr/logr"
	"github.com/rotisserie/eris"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/Azure/azure-service-operator/v2/pkg/common/annotations"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/core"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	rsv "github.com/highfivelabs/aso-extensions/apis/recoveryservices/v20260701"
)

const (
	// ResourceType is the protected item's ARM type.
	ResourceType = "Microsoft.RecoveryServices/vaults/backupFabrics/protectionContainers/protectedItems"
	// virtualMachineType is the ARM type of what a protected item backs up.
	virtualMachineType = "Microsoft.Compute/virtualMachines"
	// fabric is the only backup fabric of Azure resources.
	fabric = "Azure"

	// ASO's poller annotations (internal/reconcilers), so a pending
	// operation reads the same on our objects as on ASO's.
	pollerResumeTokenAnnotation = "serviceoperator.azure.com/poller-resume-token"
	pollerResumeIDAnnotation    = "serviceoperator.azure.com/poller-resume-id"

	// What a pending operation is (the poller's ID annotation).
	pollerRefresh = "RecoveryServices.RefreshContainers"
	pollerCreate  = "GenericClient.CreateOrUpdateByID"
	pollerDelete  = "GenericClient.DeleteByID"
)

// PollInterval is how long to wait before following a pending operation
// when Azure does not say (Retry-After). Tests shorten it.
var PollInterval = 10 * time.Second

var _ genruntime.Reconciler = &VaultsProtectedItemReconciler{}

type VaultsProtectedItemReconciler struct {
	outoftree.ARMOwnedResourceReconcilerCommon
	ResourceResolver  *outoftree.Resolver
	ConnectionFactory outoftree.ARMConnectionFactory
}

func NewVaultsProtectedItemReconciler(
	kubeClient outoftree.KubeClient,
	resourceResolver *outoftree.Resolver,
	positiveConditions *conditions.PositiveConditionBuilder,
	connectionFactory outoftree.ARMConnectionFactory,
) *VaultsProtectedItemReconciler {
	return &VaultsProtectedItemReconciler{
		ResourceResolver:  resourceResolver,
		ConnectionFactory: connectionFactory,
		ARMOwnedResourceReconcilerCommon: outoftree.ARMOwnedResourceReconcilerCommon{
			ResourceResolver: resourceResolver,
			ReconcilerCommon: outoftree.ReconcilerCommon{
				KubeClient:         kubeClient,
				PositiveConditions: positiveConditions,
			},
		},
	}
}

func asItem(obj genruntime.MetaObject) (*rsv.VaultsProtectedItem, error) {
	item, ok := obj.(*rsv.VaultsProtectedItem)
	if !ok {
		return nil, eris.Errorf("cannot modify resource that is not of type *VaultsProtectedItem. Type is %T", obj)
	}
	return item, nil
}

// DefaultNames are the container and item names Azure gives a virtual
// machine's backup (the spec's examples, and what Azure's own tools use):
// iaasvmcontainer;iaasvmcontainerv2;<resource group>;<vm> and
// vm;iaasvmcontainerv2;<resource group>;<vm>.
func DefaultNames(vmID string) (container, item string, err error) {
	id, err := arm.ParseResourceID(vmID)
	if err != nil {
		return "", "", eris.Wrapf(err, "parsing the virtual machine's ARM ID %q", vmID)
	}
	if !strings.EqualFold(id.ResourceType.String(), virtualMachineType) {
		return "", "", eris.Errorf("%q is a %s, not a virtual machine (%s): only virtual machines are backed up by this kind so far", vmID, id.ResourceType.String(), virtualMachineType)
	}
	suffix := "iaasvmcontainerv2;" + id.ResourceGroupName + ";" + id.Name
	return "iaasvmcontainer;" + suffix, "vm;" + suffix, nil
}

// ItemID is the protected item's ARM ID under a vault.
func ItemID(vaultID, container, item string) string {
	return strings.TrimRight(vaultID, "/") + "/backupFabrics/" + fabric + "/protectionContainers/" + container + "/protectedItems/" + item
}

// Claim waits for a Ready vault and sets the owner reference (ASO's
// ClaimResource), names the container and item from the virtual machine
// when the spec does not (the claim's commit records them in the spec, so
// they never move), and records the item's ARM ID.
func (r *VaultsProtectedItemReconciler) Claim(ctx context.Context, log logr.Logger, _ record.EventRecorder, obj genruntime.MetaObject) error {
	item, err := asItem(obj)
	if err != nil {
		return err
	}
	if err := r.ClaimResource(ctx, log, item); err != nil {
		return err
	}
	vaultID, err := r.vaultID(ctx, item)
	if err != nil {
		return err
	}
	if item.Spec.AzureName == "" || item.Spec.ContainerName == "" {
		vmID, err := r.resolve(ctx, item, item.Spec.SourceResourceReference, "sourceResourceReference")
		if err != nil {
			return err
		}
		container, name, err := DefaultNames(vmID)
		if err != nil {
			return conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityError, conditions.ReasonFailed)
		}
		if item.Spec.ContainerName == "" {
			item.Spec.ContainerName = container
		}
		if item.Spec.AzureName == "" {
			item.Spec.AzureName = name
		}
		log.V(1).Info("Named the protected item from its virtual machine", "container", item.Spec.ContainerName, "item", item.Spec.AzureName)
	}
	genruntime.AddAnnotation(item, genruntime.ResourceIDAnnotation, ItemID(vaultID, item.Spec.ContainerName, item.Spec.AzureName))
	return nil
}

// vaultID is the owning vault's ARM ID: the ID ASO records on the vault, or
// the owner's ARM ID.
func (r *VaultsProtectedItemReconciler) vaultID(ctx context.Context, item *rsv.VaultsProtectedItem) (string, error) {
	owner, err := r.ResourceResolver.ResolveOwner(ctx, item)
	if err != nil {
		return "", outoftree.ClassifyResolverError(err)
	}
	if !owner.FoundKubernetesOwner() {
		if owner.ARMID == "" {
			return "", eris.New("spec.owner is required")
		}
		return owner.ARMID, nil
	}
	id, ok := genruntime.GetResourceID(owner.Owner)
	if !ok || id == "" {
		err := eris.Errorf("owning vault %q has no ARM ID (%s) yet", owner.Owner.GetName(), genruntime.ResourceIDAnnotation)
		return "", conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityWarning, conditions.ReasonWaitingForOwner)
	}
	return id, nil
}

// resolve is the ARM ID a reference names, through ASO's resolver: an ARM ID
// as given, or the ID ASO records on the named object.
func (r *VaultsProtectedItemReconciler) resolve(ctx context.Context, item *rsv.VaultsProtectedItem, ref *genruntime.ResourceReference, field string) (string, error) {
	if ref == nil {
		return "", conditions.NewReadyConditionImpactingError(eris.Errorf("spec.%s is required", field), conditions.ConditionSeverityError, conditions.ReasonFailed)
	}
	id, err := r.ResourceResolver.ResolveReferenceToARMID(ctx, ref.AsNamespacedRef(item.Namespace))
	if err != nil {
		return "", outoftree.ClassifyResolverError(eris.Wrapf(err, "resolving spec.%s", field))
	}
	return id, nil
}

// connection is ASO's ARM connection for the object (the credential ASO
// picks for it), after ASO's check that id is in the credential's
// subscription.
func (r *VaultsProtectedItemReconciler) connection(ctx context.Context, item *rsv.VaultsProtectedItem, id string) (*outoftree.GenericClient, error) {
	conn, err := r.ConnectionFactory(ctx, connectionObject{item})
	if err != nil {
		return nil, err
	}
	if err := checkSubscription(conn.SubscriptionID(), id); err != nil {
		return nil, err
	}
	return conn.Client(), nil
}

// checkSubscription is ASO's (internal/reconcilers/arm): an ID in another
// subscription than the credential's is refused, so a namespace's credential
// cannot be pointed elsewhere by an ARM ID owner.
func checkSubscription(subscriptionID, id string) error {
	parsed, err := arm.ParseResourceID(id)
	if err != nil {
		err = eris.Wrapf(err, "parsing resource ID %q", id)
		return conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityError, conditions.ReasonFailed)
	}
	if !genruntime.CheckARMIDMatchesSubscription(subscriptionID, parsed) {
		err = eris.Errorf("SubscriptionID %q for %q resource does not match with Client Credential: %q", parsed.SubscriptionID, id, subscriptionID)
		return conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityError, conditions.ReasonSubscriptionMismatch)
	}
	return nil
}

// protectedItem is the part of a protected item read here.
type protectedItem struct {
	ID         string                  `json:"id,omitempty"`
	Name       string                  `json:"name,omitempty"`
	Properties protectedItemProperties `json:"properties"`
}

type protectedItemProperties struct {
	ProtectedItemType    string              `json:"protectedItemType,omitempty"`
	SourceResourceID     string              `json:"sourceResourceId,omitempty"`
	PolicyID             string              `json:"policyId,omitempty"`
	ProtectionState      string              `json:"protectionState,omitempty"`
	ExtendedProperties   *extendedProperties `json:"extendedProperties,omitempty"`
	ContainerName        string              `json:"containerName,omitempty"`
	BackupManagementType string              `json:"backupManagementType,omitempty"`
	WorkloadType         string              `json:"workloadType,omitempty"`
	FriendlyName         string              `json:"friendlyName,omitempty"`
	ProtectionStatus     string              `json:"protectionStatus,omitempty"`
	HealthStatus         string              `json:"healthStatus,omitempty"`
	LastBackupStatus     string              `json:"lastBackupStatus,omitempty"`
	LastBackupTime       string              `json:"lastBackupTime,omitempty"`
}

type extendedProperties struct {
	DiskExclusionProperties *diskExclusion `json:"diskExclusionProperties,omitempty"`
}

type diskExclusion struct {
	DiskLunList     []int `json:"diskLunList,omitempty"`
	IsInclusionList *bool `json:"isInclusionList,omitempty"`
}

// putBody is what a PUT sends: the writable properties only.
type putBody struct {
	Properties putProperties `json:"properties"`
}

type putProperties struct {
	ProtectedItemType  string              `json:"protectedItemType"`
	SourceResourceID   string              `json:"sourceResourceId"`
	PolicyID           string              `json:"policyId,omitempty"`
	ProtectionState    string              `json:"protectionState,omitempty"`
	ExtendedProperties *extendedProperties `json:"extendedProperties,omitempty"`
}

func (r *VaultsProtectedItemReconciler) desired(ctx context.Context, item *rsv.VaultsProtectedItem) (*putBody, error) {
	vmID, err := r.resolve(ctx, item, item.Spec.SourceResourceReference, "sourceResourceReference")
	if err != nil {
		return nil, err
	}
	body := &putBody{Properties: putProperties{ProtectedItemType: virtualMachineType, SourceResourceID: vmID}}
	if item.Spec.PolicyReference != nil {
		if body.Properties.PolicyID, err = r.resolve(ctx, item, item.Spec.PolicyReference, "policyReference"); err != nil {
			return nil, err
		}
	}
	if item.Spec.ProtectionState != nil {
		body.Properties.ProtectionState = *item.Spec.ProtectionState
	}
	if d := item.Spec.DiskExclusionProperties; d != nil {
		body.Properties.ExtendedProperties = &extendedProperties{DiskExclusionProperties: &diskExclusion{
			DiskLunList:     append([]int(nil), d.DiskLunList...),
			IsInclusionList: d.IsInclusionList,
		}}
	}
	return body, nil
}

// matches is whether Azure's item already is what body asks for. A state
// left out means protection on: Protected, or IRPending until the first
// backup, or ProtectionError (a failing backup, which a PUT would not
// mend). Disks are compared only when the spec chooses them.
func matches(current *protectedItem, body *putBody) bool {
	c, d := current.Properties, body.Properties
	if !strings.EqualFold(c.SourceResourceID, d.SourceResourceID) {
		return false
	}
	if d.PolicyID != "" && !strings.EqualFold(c.PolicyID, d.PolicyID) {
		return false
	}
	switch d.ProtectionState {
	case "", "Protected":
		switch c.ProtectionState {
		case "Protected", "IRPending", "ProtectionError":
		default:
			return false
		}
	default:
		if !strings.EqualFold(c.ProtectionState, d.ProtectionState) {
			return false
		}
	}
	if d.ExtendedProperties != nil {
		want := d.ExtendedProperties.DiskExclusionProperties
		var got *diskExclusion
		if c.ExtendedProperties != nil {
			got = c.ExtendedProperties.DiskExclusionProperties
		}
		if got == nil {
			got = &diskExclusion{}
		}
		if boolOf(want.IsInclusionList) != boolOf(got.IsInclusionList) || !sameInts(want.DiskLunList, got.DiskLunList) {
			return false
		}
	}
	return true
}

func (r *VaultsProtectedItemReconciler) CreateOrUpdate(
	ctx context.Context,
	log logr.Logger,
	_ record.EventRecorder,
	obj genruntime.MetaObject,
	_ annotations.ResolvedReconcilePolicies,
) (ctrl.Result, error) {
	item, err := asItem(obj)
	if err != nil {
		return ctrl.Result{}, err
	}
	id, ok := item.GetAnnotations()[genruntime.ResourceIDAnnotation]
	if !ok || id == "" {
		return ctrl.Result{}, eris.New("the protected item has no ARM ID; it was not claimed")
	}
	log = log.WithValues("azureName", item.AzureName())
	client, err := r.connection(ctx, item, id)
	if err != nil {
		return ctrl.Result{}, err
	}

	// A pending operation first: follow it until it is done.
	refreshed := false
	if pollerID, token, pending := pollerOf(item); pending {
		done, wait, err := resume(ctx, client, pollerID, token)
		if err != nil {
			if terminal(err) {
				clearPoller(item)
			}
			return ctrl.Result{}, classify(err, "following the "+pollerID+" operation")
		}
		if !done {
			return ctrl.Result{RequeueAfter: wait}, nil
		}
		clearPoller(item)
		refreshed = pollerID == pollerRefresh
	}

	body, err := r.desired(ctx, item)
	if err != nil {
		return ctrl.Result{}, err
	}
	current := &protectedItem{}
	_, err = client.GetByID(ctx, id, rsv.APIVersion, current)
	exists := err == nil
	if err != nil && !isNotFound(err) {
		return ctrl.Result{}, classify(err, "reading the protected item")
	}
	if exists && matches(current, body) {
		writeStatus(item, current)
		return ctrl.Result{}, nil
	}

	if !exists && !refreshed {
		// Azure's flow for a new item: refresh the vault's view of the
		// subscription's virtual machines first.
		fabricID := id[:strings.Index(strings.ToLower(id), "/backupfabrics/")] + "/backupFabrics/" + fabric
		log.V(0).Info("Refreshing the vault's protection containers", "fabric", fabricID)
		resp, err := client.BeginPostActionByID(ctx, fabricID, "refreshContainers", rsv.APIVersion)
		if err != nil {
			return ctrl.Result{}, classify(err, "refreshing the vault's protection containers")
		}
		if !resp.Poller.Done() {
			return r.wait(item, pollerRefresh, resp.Poller.ResumeToken, resp.RawResponse)
		}
	}

	log.V(0).Info("Setting the protected item", "id", id, "policy", body.Properties.PolicyID, "state", body.Properties.ProtectionState)
	resp, err := client.BeginCreateOrUpdateByID(ctx, id, rsv.APIVersion, body)
	if err != nil {
		return ctrl.Result{}, classify(err, "setting the protected item")
	}
	if !resp.Poller.Done() {
		return r.wait(item, pollerCreate, resp.Poller.ResumeToken, resp.RawResponse)
	}
	if _, err := resp.Poller.Result(ctx); err != nil {
		return ctrl.Result{}, classify(err, "setting the protected item")
	}
	return ctrl.Result{}, r.readStatus(ctx, client, item, id)
}

// wait records a pending operation in the poller annotations (the generic
// reconciler commits them) and asks to be called again.
func (r *VaultsProtectedItemReconciler) wait(item *rsv.VaultsProtectedItem, pollerID string, resumeToken func() (string, error), raw *http.Response) (ctrl.Result, error) {
	token, err := resumeToken()
	if err != nil {
		return ctrl.Result{}, eris.Wrap(err, "keeping the pending operation")
	}
	genruntime.AddAnnotation(item, pollerResumeTokenAnnotation, token)
	genruntime.AddAnnotation(item, pollerResumeIDAnnotation, pollerID)
	return ctrl.Result{RequeueAfter: retryAfter(raw)}, nil
}

func pollerOf(item genruntime.MetaObject) (id, token string, ok bool) {
	a := item.GetAnnotations()
	token, hasToken := a[pollerResumeTokenAnnotation]
	id, hasID := a[pollerResumeIDAnnotation]
	return id, token, hasToken && hasID
}

func clearPoller(item genruntime.MetaObject) {
	genruntime.RemoveAnnotation(item, pollerResumeTokenAnnotation)
	genruntime.RemoveAnnotation(item, pollerResumeIDAnnotation)
}

// resume follows a pending operation one step with ASO's poller (Resume
// polls once and, when done, reads the result).
func resume(ctx context.Context, client *outoftree.GenericClient, pollerID, token string) (done bool, wait time.Duration, err error) {
	switch pollerID {
	case pollerRefresh:
		p := client.ResumeActionPoller(pollerID)
		if err := p.Resume(ctx, client, token); err != nil {
			return false, 0, err
		}
		return p.Poller.Done(), retryAfter(p.RawResponse), nil
	case pollerCreate:
		p := client.ResumeCreatePoller(pollerID)
		if err := p.Resume(ctx, client, token); err != nil {
			return false, 0, err
		}
		return p.Poller.Done(), retryAfter(p.RawResponse), nil
	case pollerDelete:
		p := client.ResumeDeletePoller(pollerID)
		if err := p.Resume(ctx, client, token); err != nil {
			return false, 0, err
		}
		return p.Poller.Done(), retryAfter(p.RawResponse), nil
	default:
		return false, 0, eris.Errorf("unknown pending operation %q", pollerID)
	}
}

// retryAfter is Azure's Retry-After (seconds), within a minute, or
// PollInterval.
func retryAfter(resp *http.Response) time.Duration {
	if resp != nil {
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
			return min(time.Duration(s)*time.Second, time.Minute)
		}
	}
	return PollInterval
}

func (r *VaultsProtectedItemReconciler) readStatus(ctx context.Context, client *outoftree.GenericClient, item *rsv.VaultsProtectedItem, id string) error {
	current := &protectedItem{}
	if _, err := client.GetByID(ctx, id, rsv.APIVersion, current); err != nil {
		if isNotFound(err) {
			err = eris.Errorf("protected item %s does not exist", id)
			return conditions.NewReadyConditionImpactingError(err, conditions.ConditionSeverityWarning, conditions.ReasonAzureResourceNotFound)
		}
		return classify(err, "reading the protected item")
	}
	writeStatus(item, current)
	return nil
}

func writeStatus(item *rsv.VaultsProtectedItem, current *protectedItem) {
	p := current.Properties
	s := &item.Status
	s.Id, s.Name = current.ID, current.Name
	s.ContainerName = p.ContainerName
	s.ProtectedItemType = p.ProtectedItemType
	s.BackupManagementType = p.BackupManagementType
	s.WorkloadType = p.WorkloadType
	s.FriendlyName = p.FriendlyName
	s.SourceResourceId = p.SourceResourceID
	s.PolicyId = p.PolicyID
	s.ProtectionState = p.ProtectionState
	s.ProtectionStatus = p.ProtectionStatus
	s.HealthStatus = p.HealthStatus
	s.LastBackupStatus = p.LastBackupStatus
	s.LastBackupTime = p.LastBackupTime
	s.DiskExclusionProperties = nil
	if p.ExtendedProperties != nil && p.ExtendedProperties.DiskExclusionProperties != nil {
		d := p.ExtendedProperties.DiskExclusionProperties
		s.DiskExclusionProperties = &rsv.DiskExclusionProperties{DiskLunList: append([]int(nil), d.DiskLunList...), IsInclusionList: d.IsInclusionList}
	}
}

// Delete stops protection and deletes the backup data (Azure's DELETE;
// with the vault's soft delete on, Azure keeps the data in a soft-deleted
// state for its retention days), from the ARM ID recorded at the claim, so
// it works when the vault object is gone. An item Azure no longer has is
// deleted already.
func (r *VaultsProtectedItemReconciler) Delete(ctx context.Context, log logr.Logger, _ record.EventRecorder, obj genruntime.MetaObject) (ctrl.Result, error) {
	item, err := asItem(obj)
	if err != nil {
		return ctrl.Result{}, err
	}
	id, ok := item.GetAnnotations()[genruntime.ResourceIDAnnotation]
	if !ok || id == "" {
		// Never claimed, so never created.
		return ctrl.Result{}, nil
	}
	client, err := r.connection(ctx, item, id)
	if err != nil {
		return ctrl.Result{}, err
	}
	// A pending operation first: a delete is followed to its end; a refresh
	// or a PUT still running is followed until it ends too, since Azure
	// refuses a DELETE while an operation on the item runs.
	if pollerID, token, pending := pollerOf(item); pending {
		done, wait, err := resume(ctx, client, pollerID, token)
		if err != nil {
			if terminal(err) {
				clearPoller(item)
			}
			if pollerID == pollerDelete && isNotFound(err) {
				return ctrl.Result{}, nil
			}
			if pollerID == pollerDelete || !terminal(err) {
				return ctrl.Result{}, classify(err, "following the "+pollerID+" operation")
			}
			// A refresh or PUT that failed leaves nothing running: delete.
		} else {
			if !done {
				return ctrl.Result{RequeueAfter: wait}, nil
			}
			clearPoller(item)
			if pollerID == pollerDelete {
				return ctrl.Result{}, nil
			}
		}
	}
	log.V(0).Info("Deleting the protected item", "id", id)
	resp, err := client.BeginDeleteByID(ctx, id, rsv.APIVersion)
	if err != nil {
		if isNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, classify(err, "deleting the protected item")
	}
	if resp.Poller.Done() {
		return ctrl.Result{}, nil
	}
	return r.wait(item, pollerDelete, resp.Poller.ResumeToken, resp.RawResponse)
}

func (r *VaultsProtectedItemReconciler) UpdateStatus(ctx context.Context, _ logr.Logger, _ record.EventRecorder, obj genruntime.MetaObject, _ annotations.ResolvedReconcilePolicies) error {
	item, err := asItem(obj)
	if err != nil {
		return err
	}
	id, ok := item.GetAnnotations()[genruntime.ResourceIDAnnotation]
	if !ok || id == "" {
		return eris.New("the protected item has no ARM ID; it was not claimed")
	}
	client, err := r.connection(ctx, item, id)
	if err != nil {
		return err
	}
	return r.readStatus(ctx, client, item, id)
}

// terminal is whether following an operation failed for good (Azure
// answered with the operation's failure or a refusal), so the pending
// operation is forgotten; a transport error, a throttle (429) or a server
// error (5xx) keeps it, to be followed again.
func terminal(err error) bool {
	var re *azcore.ResponseError
	if !errors.As(err, &re) {
		return false
	}
	return re.StatusCode < 500 && re.StatusCode != http.StatusTooManyRequests
}

func isNotFound(err error) bool {
	var re *azcore.ResponseError
	return errors.As(err, &re) && re.StatusCode == http.StatusNotFound
}

// retryableCodes are ARM error codes that a later attempt may get past, as
// ASO's error classification (internal/reconcilers/arm) treats them: an
// operation already running, a conflict, a right not granted yet.
var retryableCodes = []string{"Conflict", "AnotherOperationInProgress", "OperationInProgress", "AuthorizationFailed", "ScopeLocked", "InProgress", "RetryableError"}

// classify puts an ARM error on the Ready condition as ASO's reconciler
// would: a bad request (400) whose code is not one a later attempt may get
// past is an error that waits for a change of the spec; a missing
// reference (404), a conflict or refusal that may pass (409, 403, an
// operation in progress, a lock) and anything else are warnings, retried.
func classify(err error, what string) error {
	wrapped := eris.Wrap(err, what)
	var re *azcore.ResponseError
	if errors.As(err, &re) {
		retryable := false
		for _, c := range retryableCodes {
			if strings.Contains(strings.ToLower(re.ErrorCode), strings.ToLower(c)) {
				retryable = true
			}
		}
		switch {
		case re.StatusCode == http.StatusNotFound:
			return conditions.NewReadyConditionImpactingError(wrapped, conditions.ConditionSeverityWarning, conditions.ReasonAzureResourceNotFound)
		case re.StatusCode == http.StatusBadRequest && !retryable:
			return conditions.NewReadyConditionImpactingError(wrapped, conditions.ConditionSeverityError, conditions.ReasonFailed)
		case re.StatusCode < 500:
			return conditions.NewReadyConditionImpactingError(wrapped, conditions.ConditionSeverityWarning, conditions.ReasonFailed)
		}
	}
	var notFound *core.ReferenceNotFound
	if eris.As(err, &notFound) {
		return outoftree.ClassifyResolverError(err)
	}
	return wrapped
}

func boolOf(p *bool) bool { return p != nil && *p }

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := append([]int(nil), a...), append([]int(nil), b...)
	sort.Ints(x)
	sort.Ints(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// connectionObject lets ASO's ARM connection factory take the object. The
// factory's signature asks for an ARMMetaObject, and it reads only the
// object's metadata (its namespace and credential annotations); the ARM
// parts are given here for completeness.
type connectionObject struct{ *rsv.VaultsProtectedItem }

var _ genruntime.ARMMetaObject = connectionObject{}

func (connectionObject) GetType() string { return ResourceType }

func (connectionObject) GetResourceScope() genruntime.ResourceScope {
	return genruntime.ResourceScopeResourceGroup
}

func (connectionObject) GetAPIVersion() string { return rsv.APIVersion }

func (connectionObject) GetSpec() genruntime.ConvertibleSpec { return nil }

func (connectionObject) GetStatus() genruntime.ConvertibleStatus { return nil }

func (connectionObject) NewEmptyStatus() genruntime.ConvertibleStatus { return nil }

func (connectionObject) SetStatus(genruntime.ConvertibleStatus) error {
	return eris.New("VaultsProtectedItem has no ARM status conversion")
}

func (connectionObject) GetSupportedOperations() []genruntime.ResourceOperation {
	return []genruntime.ResourceOperation{genruntime.ResourceOperationGet, genruntime.ResourceOperationPut, genruntime.ResourceOperationDelete}
}
