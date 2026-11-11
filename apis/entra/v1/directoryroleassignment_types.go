// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// DirectoryRoleAssignment is Microsoft Graph's unifiedRoleAssignment
// (POST /roleManagement/directory/roleAssignments), shaped as Azure Service
// Operator's entra.azure.com kinds are (v2/api/entra/v1: the operatorSpec
// with creationMode, the Ready printer columns, the principal by object ID
// or from a ConfigMap as a SecurityGroup's members are). Property names are
// Graph's.

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/conversion"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
)

// +kubebuilder:rbac:groups=entra.azure.hfl.dev,resources=directoryroleassignments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=entra.azure.hfl.dev,resources={directoryroleassignments/status,directoryroleassignments/finalizers},verbs=get;update;patch

// +kubebuilder:object:root=true
// +kubebuilder:resource:categories={azure,entra,aad}
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Severity",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].severity"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].reason"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].message"
// +kubebuilder:storageversion

// DirectoryRoleAssignment assigns a Microsoft Entra directory role (a
// built-in role such as Global Reader, or a custom one) to a user, group or
// service principal, at the whole directory ("/"), an administrative unit
// or an application. Graph does not update an assignment: every property
// but operatorSpec is fixed once set; change one by replacing the object.
type DirectoryRoleAssignment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DirectoryRoleAssignmentSpec   `json:"spec,omitempty"`
	Status            DirectoryRoleAssignmentStatus `json:"status,omitempty"`
}

var _ conditions.Conditioner = &DirectoryRoleAssignment{}

// GetConditions returns the conditions of the resource
func (assignment *DirectoryRoleAssignment) GetConditions() conditions.Conditions {
	return assignment.Status.Conditions
}

// SetConditions sets the conditions on the resource status
func (assignment *DirectoryRoleAssignment) SetConditions(conditions conditions.Conditions) {
	assignment.Status.Conditions = conditions
}

var _ conversion.Hub = &DirectoryRoleAssignment{}

// Hub marks that this DirectoryRoleAssignment is the hub type for conversion
func (assignment *DirectoryRoleAssignment) Hub() {}

// +kubebuilder:object:root=true

// DirectoryRoleAssignmentList contains a list of DirectoryRoleAssignment
type DirectoryRoleAssignmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DirectoryRoleAssignment `json:"items"`
}

// +kubebuilder:validation:XValidation:rule="has(self.principalId) != has(self.principalIdFromConfig)",message="exactly one of principalId and principalIdFromConfig is required"
// +kubebuilder:validation:XValidation:rule="has(self.directoryScopeId) != has(self.appScopeId)",message="exactly one of directoryScopeId and appScopeId is required"
// +kubebuilder:validation:XValidation:rule="has(oldSelf.principalId) == has(self.principalId) && (!has(self.principalId) || self.principalId == oldSelf.principalId)",message="principalId cannot be changed: Microsoft Graph does not update a role assignment"
// +kubebuilder:validation:XValidation:rule="has(oldSelf.principalIdFromConfig) == has(self.principalIdFromConfig) && (!has(self.principalIdFromConfig) || self.principalIdFromConfig == oldSelf.principalIdFromConfig)",message="principalIdFromConfig cannot be changed: Microsoft Graph does not update a role assignment"
// +kubebuilder:validation:XValidation:rule="has(oldSelf.directoryScopeId) == has(self.directoryScopeId) && (!has(self.directoryScopeId) || self.directoryScopeId == oldSelf.directoryScopeId)",message="directoryScopeId cannot be changed: Microsoft Graph does not update a role assignment"
// +kubebuilder:validation:XValidation:rule="has(oldSelf.appScopeId) == has(self.appScopeId) && (!has(self.appScopeId) || self.appScopeId == oldSelf.appScopeId)",message="appScopeId cannot be changed: Microsoft Graph does not update a role assignment"
// +kubebuilder:validation:XValidation:rule="has(oldSelf.condition) == has(self.condition) && (!has(self.condition) || self.condition == oldSelf.condition)",message="condition cannot be changed: Microsoft Graph does not update a role assignment"
type DirectoryRoleAssignmentSpec struct {
	// PrincipalId: the object ID of the user, group or service principal
	// the role is assigned to. Exactly one of principalId and
	// principalIdFromConfig.
	PrincipalId *string `json:"principalId,omitempty" optionalConfigMapPair:"PrincipalId"`

	// PrincipalIdFromConfig: the principal's object ID from a ConfigMap
	// (an ASO SecurityGroup or ServicePrincipal can export its ID to one).
	PrincipalIdFromConfig *genruntime.ConfigMapReference `json:"principalIdFromConfig,omitempty" optionalConfigMapPair:"PrincipalId"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="roleDefinitionId cannot be changed: Microsoft Graph does not update a role assignment"
	// RoleDefinitionId: the role's ID; for a built-in role, its template ID
	// (Global Reader is f2ef992c-3afb-46b9-b7cf-a126ee74c451).
	RoleDefinitionId *string `json:"roleDefinitionId,omitempty"`

	// DirectoryScopeId: where the role applies in the directory: "/" for
	// the whole tenant, or /administrativeUnits/{id}. Exactly one of
	// directoryScopeId and appScopeId.
	DirectoryScopeId *string `json:"directoryScopeId,omitempty"`

	// AppScopeId: an application-specific scope, for roles that an
	// application defines. Exactly one of directoryScopeId and appScopeId.
	AppScopeId *string `json:"appScopeId,omitempty"`

	// Condition: a condition on the assignment (Graph's beta API; for
	// roles that support conditions).
	Condition *string `json:"condition,omitempty"`

	// OperatorSpec: settings for the operator, not sent to Microsoft Graph.
	OperatorSpec *DirectoryRoleAssignmentOperatorSpec `json:"operatorSpec,omitempty"`
}

// OriginalVersion returns the original API version used to create the resource.
func (spec *DirectoryRoleAssignmentSpec) OriginalVersion() string {
	return GroupVersion.Version
}

// DirectoryRoleAssignmentOperatorSpec: settings for the operator, as ASO's
// Entra kinds have them.
type DirectoryRoleAssignmentOperatorSpec struct {
	// CreationMode: whether an existing assignment of the same role to the
	// same principal at the same scope is adopted (AdoptOrCreate, the
	// default, or AdoptOnly) and whether one is created (AdoptOrCreate,
	// AlwaysCreate). An adopted assignment is deleted with the object
	// unless the mode is AdoptOnly.
	CreationMode *CreationMode `json:"creationMode,omitempty"`
}

// CreationAllowed checks if the creation mode allows ASO to create a new assignment.
func (spec *DirectoryRoleAssignmentOperatorSpec) CreationAllowed() bool {
	if spec == nil || spec.CreationMode == nil {
		// Default is AdoptOrCreate
		return true
	}

	return spec.CreationMode.AllowsCreation()
}

// AdoptionAllowed checks if the creation mode allows ASO to adopt an existing assignment.
func (spec *DirectoryRoleAssignmentOperatorSpec) AdoptionAllowed() bool {
	if spec == nil || spec.CreationMode == nil {
		// Default is AdoptOrCreate
		return true
	}

	return spec.CreationMode.AllowsAdoption()
}

// DirectoryRoleAssignmentStatus is the assignment as Microsoft Graph has it.
type DirectoryRoleAssignmentStatus struct {
	// Conditions: The observed state of the resource
	Conditions []conditions.Condition `json:"conditions,omitempty"`

	// Id: the assignment's ID in Microsoft Graph.
	Id *string `json:"id,omitempty"`

	PrincipalId      *string `json:"principalId,omitempty"`
	RoleDefinitionId *string `json:"roleDefinitionId,omitempty"`
	DirectoryScopeId *string `json:"directoryScopeId,omitempty"`
	AppScopeId       *string `json:"appScopeId,omitempty"`
	Condition        *string `json:"condition,omitempty"`
}

func init() {
	SchemeBuilder.Register(&DirectoryRoleAssignment{}, &DirectoryRoleAssignmentList{})
}
