// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package v20260715

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/conversion"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
)

// ConnectionStringsName is the fixed name of the section in Azure.
const ConnectionStringsName = "connectionstrings"

// +kubebuilder:object:root=true
// +kubebuilder:resource:categories={azure,web,hfl}
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Severity",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].severity"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].reason"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].message"
// +kubebuilder:storageversion

// SitesConfigConnectionStrings is the whole set of a web app's (or
// deployment slot's) connection strings, Microsoft.Web
// sites/config/connectionstrings, each with its type and a value given in
// plain text or read from a key of a Kubernetes Secret or ConfigMap. The set
// is authoritative, as SitesConfigAppSettings's is: leave
// siteConfig.connectionStrings out of ASO's Site and give one
// SitesConfigConnectionStrings per site or slot. Deleting the object leaves
// the last connection strings on the app (Azure has no DELETE for them).
type SitesConfigConnectionStrings struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              SitesConfigConnectionStringsSpec   `json:"spec,omitempty"`
	Status            SitesConfigConnectionStringsStatus `json:"status,omitempty"`
}

var _ conditions.Conditioner = &SitesConfigConnectionStrings{}

// GetConditions returns the conditions of the resource
func (s *SitesConfigConnectionStrings) GetConditions() conditions.Conditions {
	return s.Status.Conditions
}

// SetConditions sets the conditions on the resource status
func (s *SitesConfigConnectionStrings) SetConditions(c conditions.Conditions) {
	s.Status.Conditions = c
}

var _ genruntime.ARMOwned = &SitesConfigConnectionStrings{}

// AzureName is the section's fixed name, connectionstrings.
func (s *SitesConfigConnectionStrings) AzureName() string { return ConnectionStringsName }

// Owner returns the ResourceReference of the owner (the Site or SitesSlot).
func (s *SitesConfigConnectionStrings) Owner() *genruntime.ResourceReference {
	return s.Spec.Owner.AsResourceReference()
}

var _ conversion.Hub = &SitesConfigConnectionStrings{}

// Hub marks that this is the hub type for conversion
func (s *SitesConfigConnectionStrings) Hub() {}

// +kubebuilder:object:root=true
type SitesConfigConnectionStringsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SitesConfigConnectionStrings `json:"items"`
}

// +kubebuilder:validation:XValidation:rule="self.owner == oldSelf.owner",message="owner is immutable"
type SitesConfigConnectionStringsSpec struct {
	// +kubebuilder:validation:Required
	// Owner is the app the connection strings belong to: a web.azure.com
	// Site (by name, the default), a web.azure.hfl.dev SitesSlot (name,
	// group and kind), or either by armId.
	Owner *SiteOwnerReference `json:"owner,omitempty"`

	// ConnectionStrings are the app's connection strings, the whole set. An
	// empty list removes every connection string.
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=1000
	ConnectionStrings []ConnectionString `json:"connectionStrings,omitempty"`
}

// ConnectionString is one connection string.
// +kubebuilder:validation:XValidation:rule="[has(self.value), has(self.valueFromSecret), has(self.valueFromConfig)].filter(x, x).size() == 1",message="set exactly one of value, valueFromSecret and valueFromConfig"
// +kubebuilder:validation:XValidation:rule="!has(self.template) || ((has(self.valueFromSecret) || has(self.valueFromConfig)) && self.template.contains('$(value)'))",message="template takes valueFromSecret or valueFromConfig and holds $(value) where the referenced value goes"
type ConnectionString struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	// Name is the connection string's name.
	Name string `json:"name"`

	// +kubebuilder:validation:Required
	// Type is the kind of database or service the connection string is for.
	// +kubebuilder:validation:Enum=MySql;SQLServer;SQLAzure;Custom;NotificationHub;ServiceBus;EventHub;ApiHub;DocDb;RedisCache;PostgreSQL
	Type string `json:"type"`

	// Value is the connection string in plain text. Use valueFromSecret
	// when it holds a password or a key.
	Value *string `json:"value,omitempty"`

	// ValueFromSecret reads the connection string from a key of a
	// Kubernetes Secret in this namespace. A change to the Secret is sent
	// to Azure. The value never appears in the status.
	ValueFromSecret *genruntime.SecretReference `json:"valueFromSecret,omitempty"`

	// ValueFromConfig reads the connection string from a key of a
	// Kubernetes ConfigMap in this namespace.
	ValueFromConfig *genruntime.ConfigMapReference `json:"valueFromConfig,omitempty"`

	// Template, with valueFromSecret or valueFromConfig, is text holding
	// $(value) where the referenced value goes (each occurrence), for a value that is only
	// partly secret: a storage connection string around an access key,
	// "DefaultEndpointsProtocol=https;AccountName=st1;AccountKey=$(value)".
	// +kubebuilder:validation:MaxLength=8192
	Template *string `json:"template,omitempty"`
}

// OriginalVersion returns the original API version used to create the resource.
func (spec *SitesConfigConnectionStringsSpec) OriginalVersion() string {
	return GroupVersion.Version
}

type SitesConfigConnectionStringsStatus struct {
	// Conditions: The observed state of the resource
	Conditions []conditions.Condition `json:"conditions,omitempty"`

	// Id is the section's ARM ID (.../config/connectionstrings).
	Id string `json:"id,omitempty"`

	// ConnectionStrings are the names and types of the connection strings
	// the app has, read from Azure after the last write, sorted by name.
	// Values are never shown.
	ConnectionStrings []ConnectionStringStatus `json:"connectionStrings,omitempty"`
}

// ConnectionStringStatus is a connection string's name and type, without
// its value.
type ConnectionStringStatus struct {
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"`
}

func init() {
	SchemeBuilder.Register(&SitesConfigConnectionStrings{}, &SitesConfigConnectionStringsList{})
}
