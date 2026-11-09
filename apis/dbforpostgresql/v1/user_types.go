// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

// Modified by High Five Labs: from Azure Service Operator's
// v2/api/dbforpostgresql/v1/user_types.go. Group dbforpostgresql.azure.hfl.dev;
// new spec.aadUser (a Microsoft Entra principal as the user, signed in with
// the operator's credential, as ASO's MySQL User has) and
// spec.databasePrivileges (privileges on databases, as ASO's MySQL User has);
// localUser is no longer required, and exactly one of localUser and aadUser
// is (a CEL rule here, where ASO uses a webhook); status.appliedDatabasePrivileges;
// spec.schemaPrivileges and spec.defaultPrivileges (schema and default
// privileges, which no ASO user has) and their status.

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/conversion"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:categories={azure,dbforpostgresql,hfl}
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Severity",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].severity"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].reason"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].message"
// +kubebuilder:storageversion
// User is a postgresql user.
type User struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              UserSpec   `json:"spec,omitempty"`
	Status            UserStatus `json:"status,omitempty"`
}

var _ conditions.Conditioner = &User{}

// GetConditions returns the conditions of the resource
func (user *User) GetConditions() conditions.Conditions {
	return user.Status.Conditions
}

// SetConditions sets the conditions on the resource status
func (user *User) SetConditions(conditions conditions.Conditions) {
	user.Status.Conditions = conditions
}

var _ genruntime.ARMOwned = &User{}

// AzureName returns the Azure name of the resource: spec.azureName, or the
// object's name when it is empty (ASO's defaulting webhook sets it so;
// no webhook runs here).
func (user *User) AzureName() string {
	if user.Spec.AzureName == "" {
		return user.Name
	}
	return user.Spec.AzureName
}

// Owner returns the ResourceReference of the owner, or nil if there is no owner
func (user *User) Owner() *genruntime.ResourceReference {
	group, kind := genruntime.LookupOwnerGroupKind(user.Spec)
	return user.Spec.Owner.AsResourceReference(group, kind)
}

var _ conversion.Hub = &User{}

// Hub marks that this userSpec is the hub type for conversion
func (user *User) Hub() {}

// +kubebuilder:object:root=true
type UserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []User `json:"items"`
}

// +kubebuilder:validation:XValidation:rule="has(self.localUser) != has(self.aadUser)",message="exactly one of localUser and aadUser must be set"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.aadUser) || has(self.aadUser)",message="a user cannot change from aadUser to localUser"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.localUser) || has(self.localUser)",message="a user cannot change from localUser to aadUser"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.azureName) || self.azureName == oldSelf.azureName",message="azureName is immutable"
type UserSpec struct {
	// AzureName: The name of the resource in Azure. This is often the same as the name of the resource in Kubernetes but it
	// doesn't have to be.
	AzureName string `json:"azureName,omitempty"`

	// +kubebuilder:validation:Required
	// Owner: The owner of the resource. The owner controls where the resource goes when it is deployed. The owner also
	// controls the resources lifecycle. When the owner is deleted the resource will also be deleted. Owner is expected to be a
	// reference to a dbforpostgresql.azure.com/FlexibleServer resource
	Owner *genruntime.KubernetesOwnerReference `group:"dbforpostgresql.azure.com" json:"owner,omitempty" kind:"FlexibleServer"`

	// Roles is the set of roles granted to the user upon creation.
	// The Azure Database for PostgreSQL server is created with 3 default roles defined: azure_pg_admin, azure_superuser,
	// and your server admin user (this last is a role w/ login permission, commonly called a User).
	Roles []string `json:"roles,omitempty"`

	// +kubebuilder:default={login: true}
	// RoleOptions defines additional attributes of the user role. You can read more about these attributes
	// at https://www.postgresql.org/docs/current/role-attributes.html.
	RoleOptions *RoleOptionsSpec `json:"roleOptions,omitempty"`

	// LocalUser contains details for creating a standard (non-aad) postgresql User.
	LocalUser *LocalUserSpec `json:"localUser,omitempty"`

	// AADUser contains details for creating a Microsoft Entra (AAD) user: a
	// role bound to an Entra principal (a managed identity, a service
	// principal, a user or a group), which then signs in with an Entra token
	// instead of a password. The operator signs in as the server's Entra
	// administrator with its own credential (the one ASO would use for this
	// object: per-resource, the namespace's aso-credential, or global).
	AADUser *AADUserSpec `json:"aadUser,omitempty"`

	// DatabasePrivileges are the user's privileges on databases of the
	// server, keyed by database name: CONNECT, CREATE, TEMPORARY, or ALL
	// (ALL PRIVILEGES). The user ends with exactly these on every database:
	// a privilege on a database not listed here is revoked, as ASO's MySQL
	// User does with its databasePrivileges.
	DatabasePrivileges map[string][]DatabasePrivilege `json:"databasePrivileges,omitempty"`

	// SchemaPrivileges are the user's privileges on schemas, keyed by
	// database and then by schema: USAGE, CREATE, or ALL (ALL PRIVILEGES).
	// Since PostgreSQL 15 only the database owner may create objects in the
	// public schema, so a user that creates tables needs CREATE there, and
	// on any other schema USAGE to reach its objects. In each database
	// listed here, or listed when the user was last reconciled, the user
	// ends with exactly these on every schema: a privilege on a schema not
	// listed is revoked. Databases never listed are left alone.
	SchemaPrivileges map[string]map[string][]SchemaPrivilege `json:"schemaPrivileges,omitempty"`

	// DefaultPrivileges are privileges the user gets on objects another
	// role creates later in a schema (ALTER DEFAULT PRIVILEGES FOR ROLE …
	// IN SCHEMA … GRANT … TO the user). In each database listed here, or
	// listed when the user was last reconciled, the user ends with exactly
	// these schema-level default privileges; others are revoked. Default
	// privileges set without a schema are left alone. Deleting the user
	// revokes these, and the privileges they gave it on objects created
	// meanwhile, before the role is dropped.
	// +listType=map
	// +listMapKey=database
	// +listMapKey=schema
	// +listMapKey=forRole
	// +listMapKey=objectType
	// +kubebuilder:validation:MaxItems=100
	DefaultPrivileges []DefaultPrivilegesSpec `json:"defaultPrivileges,omitempty"`
}

// SchemaPrivilege is a privilege PostgreSQL grants on a schema.
// +kubebuilder:validation:Enum=USAGE;CREATE;ALL
type SchemaPrivilege string

const (
	SchemaPrivilegeUsage  = SchemaPrivilege("USAGE")
	SchemaPrivilegeCreate = SchemaPrivilege("CREATE")
	SchemaPrivilegeAll    = SchemaPrivilege("ALL")
)

// DefaultPrivilegesSpec is what the user is granted on objects of one type
// that ForRole creates in Schema of Database.
// +kubebuilder:validation:XValidation:rule="self.objectType != 'tables' || self.privileges.all(p, p in ['SELECT', 'INSERT', 'UPDATE', 'DELETE', 'TRUNCATE', 'REFERENCES', 'TRIGGER', 'MAINTAIN', 'ALL'])",message="privileges on tables are SELECT, INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER, MAINTAIN or ALL"
// +kubebuilder:validation:XValidation:rule="self.objectType != 'sequences' || self.privileges.all(p, p in ['USAGE', 'SELECT', 'UPDATE', 'ALL'])",message="privileges on sequences are USAGE, SELECT, UPDATE or ALL"
// +kubebuilder:validation:XValidation:rule="self.objectType != 'functions' || self.privileges.all(p, p in ['EXECUTE', 'ALL'])",message="privileges on functions are EXECUTE or ALL"
// +kubebuilder:validation:XValidation:rule="self.objectType != 'types' || self.privileges.all(p, p in ['USAGE', 'ALL'])",message="privileges on types are USAGE or ALL"
type DefaultPrivilegesSpec struct {
	// Database is the database the schema is in.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Database string `json:"database"`

	// Schema is the schema the objects are created in.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Schema string `json:"schema"`

	// ForRole is the role whose new objects these apply to: the role that
	// will create them, such as the user that runs an application's
	// migrations. The operator's administrator changes default privileges
	// for that role with its privileges: if it does not already hold them,
	// it grants itself the role for the one transaction (it needs ADMIN
	// OPTION on the role, which it has on the roles it created).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	ForRole string `json:"forRole"`

	// ObjectType is the kind of object: tables (which includes views),
	// sequences, functions (which includes procedures) or types.
	// +kubebuilder:validation:Required
	ObjectType DefaultPrivilegesObjectType `json:"objectType"`

	// Privileges are the privileges granted: for tables SELECT, INSERT,
	// UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER, MAINTAIN (PostgreSQL
	// 17 and later); for sequences USAGE, SELECT, UPDATE; for functions
	// EXECUTE; for types USAGE; ALL for all of them.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=11
	Privileges []DefaultPrivilege `json:"privileges"`
}

// DefaultPrivilegesObjectType is a kind of object default privileges apply to.
// +kubebuilder:validation:Enum=tables;sequences;functions;types
type DefaultPrivilegesObjectType string

const (
	DefaultPrivilegesObjectTypeTables    = DefaultPrivilegesObjectType("tables")
	DefaultPrivilegesObjectTypeSequences = DefaultPrivilegesObjectType("sequences")
	DefaultPrivilegesObjectTypeFunctions = DefaultPrivilegesObjectType("functions")
	DefaultPrivilegesObjectTypeTypes     = DefaultPrivilegesObjectType("types")
)

// DefaultPrivilege is a privilege granted by default privileges.
// +kubebuilder:validation:Enum=SELECT;INSERT;UPDATE;DELETE;TRUNCATE;REFERENCES;TRIGGER;MAINTAIN;USAGE;EXECUTE;ALL
type DefaultPrivilege string

// DatabasePrivilege is a privilege PostgreSQL grants on a database.
// +kubebuilder:validation:Enum=CONNECT;CREATE;TEMPORARY;ALL
type DatabasePrivilege string

const (
	DatabasePrivilegeConnect   = DatabasePrivilege("CONNECT")
	DatabasePrivilegeCreate    = DatabasePrivilege("CREATE")
	DatabasePrivilegeTemporary = DatabasePrivilege("TEMPORARY")
	DatabasePrivilegeAll       = DatabasePrivilege("ALL")
)

// +kubebuilder:validation:XValidation:rule="has(self.objectId) == has(self.principalType)",message="objectId and principalType are set together"
type AADUserSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// ServerAdminUsername is the name of the server's Microsoft Entra
	// administrator that the operator's credential belongs to (the
	// principal set as an administrator on the FlexibleServer).
	ServerAdminUsername string `json:"serverAdminUsername,omitempty"`

	// ObjectID is the Entra object ID of the principal. When set, the role
	// is created with pgaadauth_create_principal_with_oid, so azureName may
	// differ from the principal's display name; when not set,
	// pgaadauth_create_principal binds the role to the principal whose
	// display name is azureName. Immutable.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="objectId is immutable"
	ObjectID string `json:"objectId,omitempty"`

	// PrincipalType is the kind of principal ObjectID names: user, group or
	// service (a managed identity or service principal). Immutable.
	// +kubebuilder:validation:Enum=user;group;service
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="principalType is immutable"
	PrincipalType string `json:"principalType,omitempty"`
}

// OriginalVersion returns the original API version used to create the resource.
func (userSpec *UserSpec) OriginalVersion() string {
	return GroupVersion.Version
}

// SetAzureName sets the Azure name of the resource
func (userSpec *UserSpec) SetAzureName(azureName string) { userSpec.AzureName = azureName }

type LocalUserSpec struct {
	// +kubebuilder:validation:Required
	// ServerAdminUsername is the user name of the Server administrator
	ServerAdminUsername string `json:"serverAdminUsername,omitempty"`

	// +kubebuilder:validation:Required
	// ServerAdminPassword is a reference to a secret containing the servers administrator password
	ServerAdminPassword *genruntime.SecretReference `json:"serverAdminPassword,omitempty"`

	// +kubebuilder:validation:Required
	// Password is the password to use for the user
	Password *genruntime.SecretReference `json:"password,omitempty"`
}

type RoleOptionsSpec struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=true
	// WITH LOGIN or NOLOGIN
	Login bool `json:"login,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=false
	// WITH CREATEROLE or NOCREATEROLE
	CreateRole bool `json:"createRole,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=false
	// WITH CREATEDB or NOCREATEDB
	CreateDb bool `json:"createDb,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=false
	// WITH REPLICATION or NOREPLICATION
	Replication bool `json:"replication,omitempty"`
}

type UserStatus struct {
	// Conditions: The observed state of the resource
	Conditions []conditions.Condition `json:"conditions,omitempty"`

	// AppliedDatabasePrivileges are the database privileges the user had
	// when it was last reconciled.
	AppliedDatabasePrivileges map[string][]DatabasePrivilege `json:"appliedDatabasePrivileges,omitempty"`

	// AppliedSchemaPrivileges are the schema privileges the user had, in
	// the databases its schemaPrivileges named, when it was last
	// reconciled. A database here is visited again on the next reconcile, so
	// privileges on a database dropped from schemaPrivileges are revoked.
	AppliedSchemaPrivileges map[string]map[string][]string `json:"appliedSchemaPrivileges,omitempty"`

	// AppliedDefaultPrivileges are the schema-level default privileges the
	// user had, in the databases its defaultPrivileges named, when it was
	// last reconciled. As with appliedSchemaPrivileges, their databases are
	// visited again on the next reconcile.
	AppliedDefaultPrivileges []AppliedDefaultPrivileges `json:"appliedDefaultPrivileges,omitempty"`
}

// AppliedDefaultPrivileges are default privileges read back from a
// database's pg_default_acl.
type AppliedDefaultPrivileges struct {
	Database   string   `json:"database,omitempty"`
	Schema     string   `json:"schema,omitempty"`
	ForRole    string   `json:"forRole,omitempty"`
	ObjectType string   `json:"objectType,omitempty"`
	Privileges []string `json:"privileges,omitempty"`
}

func init() {
	SchemeBuilder.Register(&User{}, &UserList{})
}
