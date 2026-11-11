/*
Copyright (c) Microsoft Corporation.
Licensed under the MIT license.
*/

// Modified by High Five Labs: from Azure Service Operator pull request
// #5821 ("chore: Extract base reconciler for Entra resources", head
// 6c93419b, read 2026-11-10, open), v2/internal/reconcilers/entra/
// reconciler_base.go. Changes: the methods that take their own type
// parameter (canAdopt, canCreate, query) are package functions, since this
// module's Go version (1.26) has no generic methods; E is any Graph model
// (a role assignment is an entity, not a directory object); ReconcilerCommon
// comes through pkg/outoftree; isNotFound, getEntraID and setEntraID (from
// errors.go and annotations.go at the pin) are here too.

package entra

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/microsoftgraph/msgraph-beta-sdk-go/models/odataerrors"
	"github.com/rotisserie/eris"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"
)

// reconcilerBase is a reusable foundation for Entra resource reconcilers.
// R is the resource type and E is the corresponding Microsoft Graph type.
type reconcilerBase[R genruntime.MetaObject, E any] struct {
	outoftree.ReconcilerCommon
}

// asResource asserts that obj is of resource type R.
// If it isn't, the error identifies both the expected and actual types.
func (r *reconcilerBase[R, E]) asResource(obj genruntime.MetaObject) (R, error) {
	typedObj, ok := obj.(R)
	if !ok {
		var zero R
		return zero, eris.Errorf("cannot modify resource that is not of type %T. Type is %T", zero, obj)
	}

	return typedObj, nil
}

type adoptionAllower interface {
	comparable
	AdoptionAllowed() bool
}

// canAdopt checks whether spec allows adoption. A nil spec allows adoption by default.
func canAdopt[P adoptionAllower](spec P) bool {
	var nilSpec P
	if spec == nilSpec {
		return true
	}

	return spec.AdoptionAllowed()
}

type creationAllower interface {
	comparable
	CreationAllowed() bool
}

// canCreate checks whether spec allows creation. A nil spec allows creation by default.
func canCreate[P creationAllower](spec P) bool {
	var nilSpec P
	if spec == nilSpec {
		return true
	}

	return spec.CreationAllowed()
}

// loadByID loads an Entra resource by ID using fetch.
// A 404 returns the zero value of E without an error; other results and errors are returned unchanged.
func (r *reconcilerBase[R, E]) loadByID(
	ctx context.Context,
	id string,
	fetch func(context.Context, string) (E, error),
) (E, error) {
	resource, err := fetch(ctx, id)
	if isNotFound(err) {
		var zero E
		return zero, nil
	}

	return resource, err
}

// query finds Entra resources matching filter, collecting results from every page.
// fetchPage receives the filter and next-link URL, which is empty for the first page.
// readPage extracts the resources and next link from a response. Any failure discards partial results.
// (#5821's query builds "field eq 'value'" itself; a role assignment is
// found by two fields, so the caller builds the filter with eqFilter.)
func query[R any, E any, C comparable](
	ctx context.Context,
	filter string,
	fetchPage func(context.Context, string, string) (C, error),
	readPage func(C) ([]E, *string),
) ([]E, error) {
	var resource R
	var nilPage C
	var results []E
	nextLink := ""
	for {
		page, err := fetchPage(ctx, filter, nextLink)
		if err != nil {
			return nil, eris.Wrapf(err, "searching %T by %s", resource, filter)
		}

		if page == nilPage {
			return nil, eris.Errorf("searching %T by %s returned no response", resource, filter)
		}

		values, next := readPage(page)
		results = append(results, values...)

		if next == nil || *next == "" {
			return results, nil
		}
		nextLink = *next
	}
}

// eqFilter is an OData filter that field equals value.
func eqFilter(field, value string) string {
	return fmt.Sprintf("%s eq '%s'", field, escapeODataString(value))
}

// escapeODataString escapes a string value for use in an OData filter expression.
// Single quotes in OData are escaped by doubling them.
func escapeODataString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func isNotFound(err error) bool {
	var odataError *odataerrors.ODataError
	if eris.As(err, &odataError) {
		if odataError.ResponseStatusCode == http.StatusNotFound {
			return true
		}
	}

	return false
}

func getEntraID(obj v1.Object) (string, bool) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return "", false
	}

	result, ok := annotations[genruntime.ResourceIDAnnotation]
	return result, ok
}

func setEntraID(obj v1.Object, id string) {
	genruntime.AddAnnotation(obj, genruntime.ResourceIDAnnotation, id)
}
