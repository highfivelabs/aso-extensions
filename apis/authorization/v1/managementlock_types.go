// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/conversion"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:categories={azure,authorization,hfl}
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Level",type="string",JSONPath=".spec.level"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Severity",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].severity"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].reason"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].message"
// +kubebuilder:storageversion

// ManagementLock is an Azure management lock (Microsoft.Authorization/locks)
// on a subscription, a resource group or a resource: CanNotDelete stops the
// owner and everything under it from being deleted, ReadOnly also stops
// them being changed, whoever asks, until the lock is removed. The owner is
// any ASO resource in the same namespace, or an ARM ID, as ASO's own
// extension resources (RoleAssignment) take; the credential is the one ASO
// uses for this object (per-resource, the namespace's aso-credential, or
// global).
type ManagementLock struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ManagementLockSpec   `json:"spec,omitempty"`
	Status            ManagementLockStatus `json:"status,omitempty"`
}

var _ conditions.Conditioner = &ManagementLock{}

// GetConditions returns the conditions of the resource
func (l *ManagementLock) GetConditions() conditions.Conditions { return l.Status.Conditions }

// SetConditions sets the conditions on the resource status
func (l *ManagementLock) SetConditions(c conditions.Conditions) { l.Status.Conditions = c }

var _ genruntime.ARMOwned = &ManagementLock{}

// AzureName is the lock's name: spec.azureName, or the object's name when
// it is empty.
func (l *ManagementLock) AzureName() string {
	if l.Spec.AzureName == "" {
		return l.Name
	}
	return l.Spec.AzureName
}

// Owner returns the ResourceReference of the owner.
func (l *ManagementLock) Owner() *genruntime.ResourceReference {
	if l.Spec.Owner == nil {
		return nil
	}
	return l.Spec.Owner.AsResourceReference()
}

var _ conversion.Hub = &ManagementLock{}

// Hub marks that this is the hub type for conversion
func (l *ManagementLock) Hub() {}

// +kubebuilder:object:root=true
type ManagementLockList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ManagementLock `json:"items"`
}

// +kubebuilder:validation:XValidation:rule="!has(oldSelf.azureName) || self.azureName == oldSelf.azureName",message="azureName is immutable"
// +kubebuilder:validation:XValidation:rule="self.owner == oldSelf.owner",message="owner is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.owner.armId) != has(self.owner.name)",message="owner names either an ARM ID (armId) or an ASO resource (group, kind and name)"
// +kubebuilder:validation:XValidation:rule="!has(self.owner.name) || (has(self.owner.group) && has(self.owner.kind))",message="an owner by name needs its group and kind"
type ManagementLockSpec struct {
	// AzureName is the lock's name (up to 260 characters, none of
	// < > % & : \ ? / # or control characters). Defaults to the object's
	// name.
	// +kubebuilder:validation:MaxLength=260
	// +kubebuilder:validation:Pattern=`^[^<>%&:\\?/#\x00-\x1f]+$`
	AzureName string `json:"azureName,omitempty"`

	// +kubebuilder:validation:Required
	// Owner is what the lock is on: an ASO resource in this namespace by
	// group, kind and name (its ARM ID is read from the resource once ASO
	// has provisioned it, and it must be Ready), or an ARM ID: a
	// subscription (/subscriptions/<id>), a resource group or a resource.
	Owner *genruntime.ArbitraryOwnerReference `json:"owner,omitempty"`

	// +kubebuilder:validation:Required
	// Level is CanNotDelete (authorized users can read and change the
	// resource but not delete it) or ReadOnly (they can only read it). A
	// lock applies to everything under its scope. ReadOnly also stops ASO
	// itself from updating the owner and its children until it is removed.
	// +kubebuilder:validation:Enum=CanNotDelete;ReadOnly
	Level string `json:"level,omitempty"`

	// Notes about the lock, up to 512 characters.
	// +kubebuilder:validation:MaxLength=512
	Notes string `json:"notes,omitempty"`

	// Owners of the lock, by application ID (Azure's properties.owners).
	// Azure keeps the list with the lock; it grants nothing and does not
	// change who may remove the lock.
	Owners []LockOwner `json:"owners,omitempty"`
}

// LockOwner is an owner of a lock.
type LockOwner struct {
	// ApplicationId is the application ID of the lock owner.
	ApplicationId string `json:"applicationId,omitempty"`
}

// OriginalVersion returns the original API version used to create the resource.
func (spec *ManagementLockSpec) OriginalVersion() string { return GroupVersion.Version }

// SetAzureName sets the Azure name of the resource
func (spec *ManagementLockSpec) SetAzureName(azureName string) { spec.AzureName = azureName }

type ManagementLockStatus struct {
	// Conditions: The observed state of the resource
	Conditions []conditions.Condition `json:"conditions,omitempty"`

	// Id is the lock's ARM ID.
	Id string `json:"id,omitempty"`

	// Scope is the ARM ID of what the lock is on.
	Scope string `json:"scope,omitempty"`

	// Level, Notes and Owners as Azure holds them.
	Level  string      `json:"level,omitempty"`
	Notes  string      `json:"notes,omitempty"`
	Owners []LockOwner `json:"owners,omitempty"`
}

func init() {
	SchemeBuilder.Register(&ManagementLock{}, &ManagementLockList{})
}
