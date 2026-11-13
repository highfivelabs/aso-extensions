// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Kept in generator/_extensions and copied into apis/arm by
// hack/generate-arm.sh, which deletes apis/arm on every run.

package customizations

import (
	"strings"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/core"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/extensions"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/retry"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"
)

var _ extensions.ErrorClassifier = &ManagementGroupExtension{}

// ClassifyError retries a management group whose parent is not there yet at
// the usual pace. A group names its parent by reference
// (details.parent), which resolves to the parent's ID as soon as the parent
// object is claimed, before Azure has made it; Azure then refuses the
// child's PUT. ASO classifies an unknown 400 as retryable only at its very
// slow pace (the sync period), so a hierarchy applied at once (the catalog's
// avm-ptn-alz, levels 0 to 6) waited that long for every level. As
// MongodbDatabaseExtension makes a 400 retryable while its account is made,
// this makes a missing parent retryable, fast.
func (extension *ManagementGroupExtension) ClassifyError(
	cloudError *outoftree.CloudError,
	_ string,
	_ logr.Logger,
	next extensions.ErrorClassifierFunc,
) (core.CloudErrorDetails, error) {
	details, err := next(cloudError)
	if err != nil {
		return core.CloudErrorDetails{}, err
	}
	if isMissingParent(details.Code, details.Message) {
		details.Classification = core.ErrorRetryable
		details.Retry = retry.Fast
	}
	return details, nil
}

// isMissingParent is whether an error says a group's parent was not found:
// the fake's ParentGroupNotFound, or any code or message that says a parent
// is not found (Azure's code for it is not documented).
func isMissingParent(code, message string) bool {
	c, m := strings.ToLower(code), strings.ToLower(message)
	if strings.Contains(c, "parent") && strings.Contains(c, "notfound") {
		return true
	}
	return strings.Contains(m, "parent") && (strings.Contains(m, "not found") || strings.Contains(m, "does not exist"))
}
