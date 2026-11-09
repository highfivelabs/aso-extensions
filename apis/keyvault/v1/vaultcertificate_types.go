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
// +kubebuilder:printcolumn:name="Expires",type="string",JSONPath=".status.expiresOn"
// +kubebuilder:printcolumn:name="Severity",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].severity"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].reason"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].message"
// +kubebuilder:storageversion

// VaultCertificate is a certificate in an Azure Key Vault, imported from a
// certificate and private key in Kubernetes Secrets, such as the
// kubernetes.io/tls Secret cert-manager issues. A new certificate in the
// Secret (a renewal) is imported as a new version. Key Vault keeps the
// certificate with its key and a secret of the same name holding both
// (PEM), which App Service, Application Gateway and Front Door read. The
// vault is an ASO keyvault.azure.com Vault in the same namespace; the
// credential is the one ASO uses for this object.
type VaultCertificate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              VaultCertificateSpec   `json:"spec,omitempty"`
	Status            VaultCertificateStatus `json:"status,omitempty"`
}

var _ conditions.Conditioner = &VaultCertificate{}

// GetConditions returns the conditions of the resource
func (c *VaultCertificate) GetConditions() conditions.Conditions { return c.Status.Conditions }

// SetConditions sets the conditions on the resource status
func (c *VaultCertificate) SetConditions(conds conditions.Conditions) { c.Status.Conditions = conds }

var _ genruntime.ARMOwned = &VaultCertificate{}

// AzureName is the certificate's name in the vault: spec.azureName, or the
// object's name when it is empty.
func (c *VaultCertificate) AzureName() string {
	if c.Spec.AzureName == "" {
		return c.Name
	}
	return c.Spec.AzureName
}

// Owner returns the ResourceReference of the owner (the Vault).
func (c *VaultCertificate) Owner() *genruntime.ResourceReference {
	group, kind := genruntime.LookupOwnerGroupKind(c.Spec)
	return c.Spec.Owner.AsResourceReference(group, kind)
}

var _ conversion.Hub = &VaultCertificate{}

// Hub marks that this is the hub type for conversion
func (c *VaultCertificate) Hub() {}

// +kubebuilder:object:root=true
type VaultCertificateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VaultCertificate `json:"items"`
}

// +kubebuilder:validation:XValidation:rule="!has(oldSelf.azureName) || self.azureName == oldSelf.azureName",message="azureName is immutable"
// +kubebuilder:validation:XValidation:rule="self.owner == oldSelf.owner",message="owner is immutable"
type VaultCertificateSpec struct {
	// AzureName is the certificate's name in the vault (1-127 letters,
	// digits and dashes); its secret has the same name. Defaults to the
	// object's name.
	// +kubebuilder:validation:Pattern="^[0-9a-zA-Z-]{1,127}$"
	AzureName string `json:"azureName,omitempty"`

	// +kubebuilder:validation:Required
	// Owner is the keyvault.azure.com Vault the certificate is in.
	Owner *genruntime.KubernetesOwnerReference `group:"keyvault.azure.com" json:"owner,omitempty" kind:"Vault"`

	// +kubebuilder:validation:Required
	// Certificate is a reference to the key of a Kubernetes Secret, in this
	// namespace, that holds the certificate as PEM: the leaf first, then any
	// intermediates (tls.crt of a kubernetes.io/tls Secret). A different
	// leaf is imported as a new version.
	Certificate *genruntime.SecretReference `json:"certificate,omitempty"`

	// +kubebuilder:validation:Required
	// PrivateKey is a reference to the key of a Kubernetes Secret, in this
	// namespace, that holds the leaf's private key as PEM (PKCS#1, PKCS#8 or
	// SEC 1; tls.key of a kubernetes.io/tls Secret). It must match the
	// certificate.
	PrivateKey *genruntime.SecretReference `json:"privateKey,omitempty"`

	// Enabled is whether the certificate can be used. Defaults to true.
	Enabled *bool `json:"enabled,omitempty"`

	// Tags are the certificate's tags in the vault.
	Tags map[string]string `json:"tags,omitempty"`

	// CreateMode is what to do when a soft-deleted certificate holds the
	// name at creation: 'default' imports and reports Key Vault's conflict;
	// 'recover' recovers the deleted certificate and fails when none exists;
	// 'createOrRecover' recovers one when it exists and imports otherwise;
	// 'purgeThenCreate' purges it for good and imports. As VaultSecret's,
	// which are ASO's VaultKey's. Defaults to 'default'.
	// +kubebuilder:validation:Enum=default;recover;createOrRecover;purgeThenCreate
	CreateMode *string `json:"createMode,omitempty"`

	// DeleteMode is what happens to the certificate in the vault when this
	// object is deleted: 'detach' leaves it; 'delete' soft-deletes it (with
	// its key and secret); 'disable' sets enabled to false. As VaultSecret's,
	// which are ASO's VaultKey's; the default is 'detach'.
	// +kubebuilder:validation:Enum=detach;delete;disable
	DeleteMode *string `json:"deleteMode,omitempty"`
}

// OriginalVersion returns the original API version used to create the resource.
func (spec *VaultCertificateSpec) OriginalVersion() string { return GroupVersion.Version }

// SetAzureName sets the Azure name of the resource
func (spec *VaultCertificateSpec) SetAzureName(azureName string) { spec.AzureName = azureName }

type VaultCertificateStatus struct {
	// Conditions: The observed state of the resource
	Conditions []conditions.Condition `json:"conditions,omitempty"`

	// Id is the identifier of the certificate's current version.
	Id string `json:"id,omitempty"`

	// Version is the certificate's current version.
	Version string `json:"version,omitempty"`

	// SecretId is the identifier of the secret holding the certificate and
	// its key (PEM), as App Service and Application Gateway reference it.
	SecretId string `json:"secretId,omitempty"`

	// Thumbprint is the leaf certificate's SHA-1 thumbprint, in hex.
	Thumbprint string `json:"thumbprint,omitempty"`

	// NotBefore and ExpiresOn are the certificate's validity, as the vault
	// reads them from it.
	NotBefore *metav1.Time `json:"notBefore,omitempty"`
	ExpiresOn *metav1.Time `json:"expiresOn,omitempty"`

	// Enabled and Tags as the vault holds them.
	Enabled *bool             `json:"enabled,omitempty"`
	Tags    map[string]string `json:"tags,omitempty"`

	// RecoveryLevel is the vault's deletion recovery level for the
	// certificate, such as Recoverable+Purgeable.
	RecoveryLevel string `json:"recoveryLevel,omitempty"`
}

func init() {
	SchemeBuilder.Register(&VaultCertificate{}, &VaultCertificateList{})
}
