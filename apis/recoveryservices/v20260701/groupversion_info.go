// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Package v20260701 contains the Recovery Services kinds written by hand:
// the backup items ASO's generator cannot own (docs/plan.md §3 c24). They
// share recoveryservices.azure.hfl.dev/v20260701 with the generated Recovery
// Services kinds (apis/arm/recoveryservices/v20260701) and the backup API
// version those are generated from (2026-07-01), so they read alike.
// +kubebuilder:object:generate=true
// +kubebuilder:validation:Optional
// +groupName=recoveryservices.azure.hfl.dev
// +versionName=v20260701
package v20260701

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is group version used to register these objects
	GroupVersion = schema.GroupVersion{Group: "recoveryservices.azure.hfl.dev", Version: "v20260701"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// APIVersion is the Recovery Services backup API version the kinds here
// speak (specification/recoveryservicesbackup, stable/2026-07-01).
const APIVersion = "2026-07-01"
