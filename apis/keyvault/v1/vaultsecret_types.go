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
// +kubebuilder:resource:categories={azure,keyvault,hfl}
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Severity",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].severity"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].reason"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].message"
// +kubebuilder:storageversion

// VaultSecret is a secret in an Azure Key Vault, its value taken from a
// Kubernetes Secret. It is the secret counterpart of ASO's VaultKey, through
// the Key Vault data plane. The vault is an ASO keyvault.azure.com Vault in
// the same namespace, and the credential is the one ASO uses for this object
// (per-resource, the namespace's aso-credential, or global).
type VaultSecret struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              VaultSecretSpec   `json:"spec,omitempty"`
	Status            VaultSecretStatus `json:"status,omitempty"`
}

var _ conditions.Conditioner = &VaultSecret{}

// GetConditions returns the conditions of the resource
func (s *VaultSecret) GetConditions() conditions.Conditions { return s.Status.Conditions }

// SetConditions sets the conditions on the resource status
func (s *VaultSecret) SetConditions(c conditions.Conditions) { s.Status.Conditions = c }

var _ genruntime.ARMOwned = &VaultSecret{}

// AzureName is the secret's name in the vault: spec.azureName, or the
// object's name when it is empty.
func (s *VaultSecret) AzureName() string {
	if s.Spec.AzureName == "" {
		return s.Name
	}
	return s.Spec.AzureName
}

// Owner returns the ResourceReference of the owner (the Vault).
func (s *VaultSecret) Owner() *genruntime.ResourceReference {
	group, kind := genruntime.LookupOwnerGroupKind(s.Spec)
	return s.Spec.Owner.AsResourceReference(group, kind)
}

var _ conversion.Hub = &VaultSecret{}

// Hub marks that this is the hub type for conversion
func (s *VaultSecret) Hub() {}

// +kubebuilder:object:root=true
type VaultSecretList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VaultSecret `json:"items"`
}

// +kubebuilder:validation:XValidation:rule="!has(oldSelf.azureName) || self.azureName == oldSelf.azureName",message="azureName is immutable"
// +kubebuilder:validation:XValidation:rule="self.owner == oldSelf.owner",message="owner is immutable"
type VaultSecretSpec struct {
	// AzureName is the secret's name in the vault (1-127 letters, digits and
	// dashes). Defaults to the object's name.
	// +kubebuilder:validation:Pattern="^[0-9a-zA-Z-]{1,127}$"
	AzureName string `json:"azureName,omitempty"`

	// +kubebuilder:validation:Required
	// Owner is the keyvault.azure.com Vault the secret is in.
	Owner *genruntime.KubernetesOwnerReference `group:"keyvault.azure.com" json:"owner,omitempty" kind:"Vault"`

	// +kubebuilder:validation:Required
	// Value is a reference to the key of a Kubernetes Secret, in this
	// namespace, that holds the secret's value. A change to it makes a new
	// version in the vault.
	Value *genruntime.SecretReference `json:"value,omitempty"`

	// ContentType is a hint of the value's type, such as text/plain.
	ContentType string `json:"contentType,omitempty"`

	// Enabled is whether the secret can be read. Defaults to true.
	Enabled *bool `json:"enabled,omitempty"`

	// NotBefore is when the secret becomes usable.
	NotBefore *metav1.Time `json:"notBefore,omitempty"`

	// ExpiresOn is when the secret stops being usable.
	ExpiresOn *metav1.Time `json:"expiresOn,omitempty"`

	// Tags are the secret's tags in the vault.
	Tags map[string]string `json:"tags,omitempty"`

	// CreateMode is what to do when a soft-deleted secret holds the name at
	// creation: 'default' creates and reports Key Vault's conflict;
	// 'recover' recovers the deleted secret and fails when none exists;
	// 'createOrRecover' recovers one when it exists and creates otherwise;
	// 'purgeThenCreate' purges it for good and creates. The same modes as
	// ASO's VaultKey. Defaults to 'default'.
	// +kubebuilder:validation:Enum=default;recover;createOrRecover;purgeThenCreate
	CreateMode *string `json:"createMode,omitempty"`

	// DeleteMode is what happens to the secret in the vault when this object
	// is deleted: 'detach' leaves it; 'delete' soft-deletes it; 'disable'
	// sets enabled to false. The same modes and default as ASO's VaultKey:
	// 'detach'.
	// +kubebuilder:validation:Enum=detach;delete;disable
	DeleteMode *string `json:"deleteMode,omitempty"`
}

// OriginalVersion returns the original API version used to create the resource.
func (spec *VaultSecretSpec) OriginalVersion() string { return GroupVersion.Version }

// SetAzureName sets the Azure name of the resource
func (spec *VaultSecretSpec) SetAzureName(azureName string) { spec.AzureName = azureName }

type VaultSecretStatus struct {
	// Conditions: The observed state of the resource
	Conditions []conditions.Condition `json:"conditions,omitempty"`

	// Id is the identifier of the secret's current version.
	Id string `json:"id,omitempty"`

	// Version is the secret's current version.
	Version string `json:"version,omitempty"`

	// ContentType, Enabled, NotBefore, ExpiresOn and Tags as the vault holds them.
	ContentType string            `json:"contentType,omitempty"`
	Enabled     *bool             `json:"enabled,omitempty"`
	NotBefore   *metav1.Time      `json:"notBefore,omitempty"`
	ExpiresOn   *metav1.Time      `json:"expiresOn,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`

	// RecoveryLevel is the vault's deletion recovery level for the secret,
	// such as Recoverable+Purgeable.
	RecoveryLevel string `json:"recoveryLevel,omitempty"`
}

func init() {
	SchemeBuilder.Register(&VaultSecret{}, &VaultSecretList{})
}
