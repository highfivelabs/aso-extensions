// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Package v1 contains API Schema definitions for Key Vault data plane objects
// that Azure Service Operator does not manage.
// +kubebuilder:object:generate=true
// +kubebuilder:validation:Optional
// +groupName=keyvault.azure.hfl.dev
package v1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is group version used to register these objects
	GroupVersion = schema.GroupVersion{Group: "keyvault.azure.hfl.dev", Version: "v1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)
