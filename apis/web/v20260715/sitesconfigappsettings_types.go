// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package v20260715

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/conversion"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
)

// AppSettingsName is the fixed name of the section in Azure.
const AppSettingsName = "appsettings"

// +kubebuilder:object:root=true
// +kubebuilder:resource:categories={azure,web,hfl}
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Severity",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].severity"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].reason"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].message"
// +kubebuilder:storageversion

// SitesConfigAppSettings is the whole set of a web app's (or deployment
// slot's) application settings, Microsoft.Web sites/config/appsettings, each
// value given in plain text or read from a key of a Kubernetes Secret or
// ConfigMap. The set is authoritative: Azure replaces the app's settings
// with exactly this list, so a setting added elsewhere (the portal, the
// Site's siteConfig.appSettings) is removed. Use one writer per app: leave
// siteConfig.appSettings out of ASO's Site (it sends them only when set)
// and give one SitesConfigAppSettings per site or slot. Deleting the object
// leaves the last settings on the app: Azure has no DELETE for the section,
// which lives as long as the app.
type SitesConfigAppSettings struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              SitesConfigAppSettingsSpec   `json:"spec,omitempty"`
	Status            SitesConfigAppSettingsStatus `json:"status,omitempty"`
}

var _ conditions.Conditioner = &SitesConfigAppSettings{}

// GetConditions returns the conditions of the resource
func (s *SitesConfigAppSettings) GetConditions() conditions.Conditions { return s.Status.Conditions }

// SetConditions sets the conditions on the resource status
func (s *SitesConfigAppSettings) SetConditions(c conditions.Conditions) { s.Status.Conditions = c }

var _ genruntime.ARMOwned = &SitesConfigAppSettings{}

// AzureName is the section's fixed name, appsettings.
func (s *SitesConfigAppSettings) AzureName() string { return AppSettingsName }

// Owner returns the ResourceReference of the owner (the Site or SitesSlot).
func (s *SitesConfigAppSettings) Owner() *genruntime.ResourceReference {
	return s.Spec.Owner.AsResourceReference()
}

var _ conversion.Hub = &SitesConfigAppSettings{}

// Hub marks that this is the hub type for conversion
func (s *SitesConfigAppSettings) Hub() {}

// +kubebuilder:object:root=true
type SitesConfigAppSettingsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SitesConfigAppSettings `json:"items"`
}

// +kubebuilder:validation:XValidation:rule="self.owner == oldSelf.owner",message="owner is immutable"
type SitesConfigAppSettingsSpec struct {
	// +kubebuilder:validation:Required
	// Owner is the app the settings belong to: a web.azure.com Site (by
	// name, the default), a web.azure.hfl.dev SitesSlot (name, group and
	// kind), or either by armId.
	Owner *SiteOwnerReference `json:"owner,omitempty"`

	// Settings are the app's settings, the whole set. An empty list removes
	// every setting.
	// +listType=map
	// +listMapKey=name
	Settings []AppSetting `json:"settings,omitempty"`
}

// AppSetting is one application setting.
// +kubebuilder:validation:XValidation:rule="[has(self.value), has(self.valueFromSecret), has(self.valueFromConfig)].filter(x, x).size() == 1",message="set exactly one of value, valueFromSecret and valueFromConfig"
type AppSetting struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// Name is the setting's name, as the app reads it from its environment.
	Name string `json:"name"`

	// Value is the setting's value in plain text. Use valueFromSecret for a
	// key, a password or a connection string.
	Value *string `json:"value,omitempty"`

	// ValueFromSecret reads the value from a key of a Kubernetes Secret in
	// this namespace. A change to the Secret is sent to Azure. The value
	// never appears in the status.
	ValueFromSecret *genruntime.SecretReference `json:"valueFromSecret,omitempty"`

	// ValueFromConfig reads the value from a key of a Kubernetes ConfigMap
	// in this namespace, such as one an ASO Component's
	// operatorSpec.configMaps writes.
	ValueFromConfig *genruntime.ConfigMapReference `json:"valueFromConfig,omitempty"`
}

// OriginalVersion returns the original API version used to create the resource.
func (spec *SitesConfigAppSettingsSpec) OriginalVersion() string { return GroupVersion.Version }

type SitesConfigAppSettingsStatus struct {
	// Conditions: The observed state of the resource
	Conditions []conditions.Condition `json:"conditions,omitempty"`

	// Id is the section's ARM ID (.../config/appsettings).
	Id string `json:"id,omitempty"`

	// Names are the names of the settings the app has, read from Azure
	// after the last write, sorted. Values are never shown.
	Names []string `json:"names,omitempty"`
}

func init() {
	SchemeBuilder.Register(&SitesConfigAppSettings{}, &SitesConfigAppSettingsList{})
}
