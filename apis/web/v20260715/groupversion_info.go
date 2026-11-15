// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Package v20260715 contains the App Service kinds written by hand in
// web.azure.hfl.dev, at the Microsoft.Web API version of ASO's Site and of
// the generated kinds of the group (apis/arm/web/v20260715): a site's app
// settings and connection strings, which Azure writes with a PUT and reads
// back only with a POST .../list, so ASO's generic ARM reconciler (which
// reads every resource back with a GET) cannot reconcile them.
// +kubebuilder:object:generate=true
// +kubebuilder:validation:Optional
// +groupName=web.azure.hfl.dev
package v20260715

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is group version used to register these objects. The
	// generated kinds of the group register the same group version from
	// apis/arm/web/v20260715.
	GroupVersion = schema.GroupVersion{Group: "web.azure.hfl.dev", Version: "v20260715"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// APIVersion is the Microsoft.Web API version the kinds speak.
const APIVersion = "2026-07-15"
