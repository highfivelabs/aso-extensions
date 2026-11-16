// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package v20260715

import (
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
)

const (
	// SiteGroup and SiteKind name ASO's Site, the default owner.
	SiteGroup = "web.azure.com"
	SiteKind  = "Site"
	// SlotGroup and SlotKind name this repository's deployment slot.
	SlotGroup = "web.azure.hfl.dev"
	SlotKind  = "SitesSlot"
)

// SiteOwnerReference is the app a config section belongs to: ASO's
// web.azure.com Site (the default when kind is empty), this repository's
// web.azure.hfl.dev SitesSlot (a deployment slot), or either by its ARM ID.
// +kubebuilder:validation:XValidation:rule="has(self.name) != has(self.armId)",message="set exactly one of name and armId"
// +kubebuilder:validation:XValidation:rule="!has(self.group) || has(self.kind)",message="a group needs its kind (Site or SitesSlot)"
// +kubebuilder:validation:XValidation:rule="!has(self.kind) || (self.kind == 'Site' && (!has(self.group) || self.group == 'web.azure.com')) || (self.kind == 'SitesSlot' && has(self.group) && self.group == 'web.azure.hfl.dev')",message="the owner is a web.azure.com Site or a web.azure.hfl.dev SitesSlot"
// +kubebuilder:validation:XValidation:rule="!has(self.armId) || self.armId.matches('(?i)^/subscriptions/[-0-9a-f]+/resourceGroups/[-._()a-z0-9]+/providers/Microsoft[.]Web/sites/[-a-z0-9]+(/slots/[-a-z0-9]+)?$')",message="armId is the ID of a Microsoft.Web site or of one of its slots"
type SiteOwnerReference struct {
	// Name is the name of the Site or SitesSlot object, in this namespace.
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name,omitempty"`

	// Group is the owner's API group: web.azure.com (the default) for a
	// Site, web.azure.hfl.dev for a SitesSlot.
	// +kubebuilder:validation:MaxLength=253
	Group string `json:"group,omitempty"`

	// Kind is the owner's kind: Site (the default) or SitesSlot.
	// +kubebuilder:validation:MaxLength=63
	Kind string `json:"kind,omitempty"`

	// ARMID is the ARM ID of a site or slot that is not in Kubernetes.
	// +kubebuilder:validation:MaxLength=1024
	ARMID string `json:"armId,omitempty"`
}

// AsResourceReference is the owner as ASO's resolver reads it.
func (o *SiteOwnerReference) AsResourceReference() *genruntime.ResourceReference {
	if o == nil {
		return nil
	}
	if o.ARMID != "" {
		return &genruntime.ResourceReference{ARMID: o.ARMID}
	}
	group, kind := o.Group, o.Kind
	if kind == "" {
		group, kind = SiteGroup, SiteKind
	} else if group == "" && kind == SiteKind {
		group = SiteGroup
	}
	return &genruntime.ResourceReference{Group: group, Kind: kind, Name: o.Name}
}
