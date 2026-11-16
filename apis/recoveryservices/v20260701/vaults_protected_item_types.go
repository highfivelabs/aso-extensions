// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package v20260701

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/conversion"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:categories={azure,recoveryservices,hfl}
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Protection",type="string",JSONPath=".status.protectionState"
// +kubebuilder:printcolumn:name="Severity",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].severity"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].reason"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].message"
// +kubebuilder:storageversion
// VaultsProtectedItem is an item a Recovery Services vault backs up: today an
// Azure virtual machine (backup turned on for the VM with a backup policy).
// Written by hand (docs/plan.md §3 c24; ASOQ Q14, builder, 2026-11-15):
// ARM keeps it under a fabric that is no resource,
// .../vaults/{vault}/backupFabrics/Azure/protectionContainers/{container}/protectedItems/{item},
// which ASO's generator cannot own through. Reconciled on ASO's generic
// reconciler with ASO's ARM client (PUT and DELETE are long-running), with
// the credential ASO picks for the object.
// - ARM URI: /subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.RecoveryServices/vaults/{vaultName}/backupFabrics/Azure/protectionContainers/{containerName}/protectedItems/{protectedItemName}
// - Spec: specification/recoveryservicesbackup, stable/2026-07-01, bms.json (ProtectedItems_CreateOrUpdate)
type VaultsProtectedItem struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              VaultsProtectedItem_Spec   `json:"spec,omitempty"`
	Status            VaultsProtectedItem_STATUS `json:"status,omitempty"`
}

var _ conditions.Conditioner = &VaultsProtectedItem{}

// GetConditions returns the conditions of the resource
func (item *VaultsProtectedItem) GetConditions() conditions.Conditions { return item.Status.Conditions }

// SetConditions sets the conditions on the resource status
func (item *VaultsProtectedItem) SetConditions(c conditions.Conditions) { item.Status.Conditions = c }

var _ genruntime.ARMOwned = &VaultsProtectedItem{}

// AzureName is the protected item's name: spec.azureName, which the
// reconciler derives from the virtual machine when it first claims the
// object (vm;iaasvmcontainerv2;<resource group>;<vm>) unless it is given.
func (item *VaultsProtectedItem) AzureName() string { return item.Spec.AzureName }

// Owner returns the ResourceReference of the owner (the Vault).
func (item *VaultsProtectedItem) Owner() *genruntime.ResourceReference {
	if item.Spec.Owner == nil {
		return nil
	}
	group, kind := genruntime.LookupOwnerGroupKind(item.Spec)
	return item.Spec.Owner.AsResourceReference(group, kind)
}

var _ conversion.Hub = &VaultsProtectedItem{}

// Hub marks that this is the hub type for conversion
func (item *VaultsProtectedItem) Hub() {}

// +kubebuilder:object:root=true
type VaultsProtectedItemList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VaultsProtectedItem `json:"items"`
}

// +kubebuilder:validation:XValidation:rule="!has(oldSelf.azureName) || self.azureName == oldSelf.azureName",message="azureName is immutable"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.containerName) || self.containerName == oldSelf.containerName",message="containerName is immutable"
// +kubebuilder:validation:XValidation:rule="self.owner == oldSelf.owner",message="owner is immutable"
// +kubebuilder:validation:XValidation:rule="self.sourceResourceReference == oldSelf.sourceResourceReference",message="sourceResourceReference is immutable: another virtual machine is another protected item"
// +kubebuilder:validation:XValidation:rule="has(self.policyReference) || (has(self.protectionState) && self.protectionState == 'ProtectionStopped')",message="policyReference is required unless protectionState is ProtectionStopped"
// +kubebuilder:validation:XValidation:rule="!has(self.diskExclusionProperties) || !has(self.diskExclusionProperties.diskLunList) || has(self.diskExclusionProperties.isInclusionList)",message="diskExclusionProperties.isInclusionList is required with a diskLunList"
type VaultsProtectedItem_Spec struct {
	// AzureName: the protected item's name in the container. Defaults to
	// vm;iaasvmcontainerv2;<resource group>;<vm>, from the virtual machine,
	// written here when the object is first claimed.
	// +kubebuilder:validation:MaxLength=260
	// +kubebuilder:validation:Pattern="^[^/?#]*$"
	AzureName string `json:"azureName,omitempty"`

	// ContainerName: the protection container the item is in. Defaults to
	// iaasvmcontainer;iaasvmcontainerv2;<resource group>;<vm>, from the
	// virtual machine, written here when the object is first claimed. Azure
	// registers a virtual machine's container itself when protection is
	// turned on.
	// +kubebuilder:validation:MaxLength=260
	// +kubebuilder:validation:Pattern="^[^/?#]*$"
	ContainerName string `json:"containerName,omitempty"`

	// +kubebuilder:validation:Required
	// Owner: the vault that backs the item up. Owner is expected to be a
	// reference to a recoveryservices.azure.hfl.dev/Vault resource, or an
	// ARM ID of a vault.
	Owner *genruntime.KnownResourceReference `group:"recoveryservices.azure.hfl.dev" json:"owner,omitempty" kind:"Vault"`

	// +kubebuilder:validation:Required
	// SourceResourceReference: the virtual machine to back up (ARM
	// properties.sourceResourceId): a compute.azure.com or
	// compute.azure.hfl.dev VirtualMachine by group, kind and name, or an
	// ARM ID.
	SourceResourceReference *genruntime.ResourceReference `armReference:"SourceResourceId" json:"sourceResourceReference,omitempty"`

	// PolicyReference: the backup policy (ARM properties.policyId), a
	// recoveryservices.azure.hfl.dev VaultsBackupPolicy of the same vault, or
	// an ARM ID. Required unless protectionState is ProtectionStopped.
	PolicyReference *genruntime.ResourceReference `armReference:"PolicyId" json:"policyReference,omitempty"`

	// ProtectionState: Protected (the default: back up on the policy's
	// schedule), BackupsSuspended (keep the recovery points, take no new
	// backups) or ProtectionStopped (stop protection and retain the data).
	// +kubebuilder:validation:Enum=Protected;BackupsSuspended;ProtectionStopped
	ProtectionState *string `json:"protectionState,omitempty"`

	// DiskExclusionProperties: which data disks (by LUN) are backed up
	// (ARM properties.extendedProperties.diskExclusionProperties).
	DiskExclusionProperties *DiskExclusionProperties `json:"diskExclusionProperties,omitempty"`
}

// DiskExclusionProperties chooses the data disks of a virtual machine a
// backup takes.
type DiskExclusionProperties struct {
	// DiskLunList: the LUNs of the data disks listed.
	DiskLunList []int `json:"diskLunList,omitempty"`

	// IsInclusionList: true backs up only the listed disks; false backs up
	// all but them.
	IsInclusionList *bool `json:"isInclusionList,omitempty"`
}

// OriginalVersion returns the original API version used to create the resource.
func (spec *VaultsProtectedItem_Spec) OriginalVersion() string { return GroupVersion.Version }

// SetAzureName sets the Azure name of the resource
func (spec *VaultsProtectedItem_Spec) SetAzureName(azureName string) { spec.AzureName = azureName }

// VaultsProtectedItem_STATUS is the protected item as Azure reports it.
type VaultsProtectedItem_STATUS struct {
	// Conditions: The observed state of the resource
	Conditions []conditions.Condition `json:"conditions,omitempty"`

	// Id: the protected item's ARM ID.
	Id string `json:"id,omitempty"`

	// Name: the protected item's name.
	Name string `json:"name,omitempty"`

	// ContainerName: the container, as Azure names it in the item
	// (iaasvmcontainerv2;<resource group>;<vm>).
	ContainerName string `json:"containerName,omitempty"`

	// ProtectedItemType: Microsoft.Compute/virtualMachines for a virtual
	// machine.
	ProtectedItemType string `json:"protectedItemType,omitempty"`

	// BackupManagementType: AzureIaasVM for a virtual machine.
	BackupManagementType string `json:"backupManagementType,omitempty"`

	// WorkloadType: VM for a virtual machine.
	WorkloadType string `json:"workloadType,omitempty"`

	// FriendlyName: the virtual machine's name.
	FriendlyName string `json:"friendlyName,omitempty"`

	// SourceResourceId: the ARM ID of what is backed up.
	SourceResourceId string `json:"sourceResourceId,omitempty"`

	// PolicyId: the ARM ID of the backup policy.
	PolicyId string `json:"policyId,omitempty"`

	// ProtectionState: Protected, IRPending (before the first backup),
	// ProtectionStopped, BackupsSuspended, ProtectionError, ProtectionPaused.
	ProtectionState string `json:"protectionState,omitempty"`

	// ProtectionStatus: the item's protection health, such as Healthy.
	ProtectionStatus string `json:"protectionStatus,omitempty"`

	// HealthStatus: the health of the last backup, such as Passed.
	HealthStatus string `json:"healthStatus,omitempty"`

	// LastBackupStatus: the result of the last backup, such as Completed.
	LastBackupStatus string `json:"lastBackupStatus,omitempty"`

	// LastBackupTime: when the last backup ran.
	LastBackupTime string `json:"lastBackupTime,omitempty"`

	// DiskExclusionProperties: the data disks chosen, as Azure holds them.
	DiskExclusionProperties *DiskExclusionProperties `json:"diskExclusionProperties,omitempty"`
}

func init() {
	SchemeBuilder.Register(&VaultsProtectedItem{}, &VaultsProtectedItemList{})
}
