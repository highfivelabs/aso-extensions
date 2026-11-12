// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Kept in generator/_extensions and copied into apis/arm by
// hack/generate-arm.sh, which deletes apis/arm on every run.

package customizations

import (
	"context"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	armruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/go-logr/logr"
	"github.com/rotisserie/eris"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/extensions"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	v20260701 "github.com/highfivelabs/aso-extensions/apis/arm/recoveryservices/v20260701"
)

var _ extensions.Deleter = &VaultsBackupResourceGuardProxyExtension{}

// Delete unlocks the proxy's delete before deleting it. Removing the proxy
// turns multi-user authorization off, an operation the resource guard
// protects: Azure refuses the DELETE (BMSUserErrorUnlockPreviligeAccessIsNeeded)
// until a POST to the proxy's unlockDelete names the proxy and the guard's
// deleteResourceGuardProxyRequests request (a live run of 2026-11-12). The
// identity needs the guard's permission for that request; without it the
// unlock is refused and the delete waits, as Azure means it to.
func (extension *VaultsBackupResourceGuardProxyExtension) Delete(
	ctx context.Context,
	log logr.Logger,
	resolver *outoftree.Resolver,
	armClient *outoftree.GenericClient,
	obj genruntime.ARMMetaObject,
	next extensions.DeleteFunc,
) (extensions.DeleteResult, error) {
	proxy, ok := obj.(*v20260701.VaultsBackupResourceGuardProxy)
	if !ok {
		return extensions.DeleteResult{}, eris.Errorf("cannot run on unknown resource type %T", obj)
	}
	id, hasID := genruntime.GetResourceID(obj)
	var guard string
	if p := proxy.Status.Properties; p != nil && p.ResourceGuardResourceId != nil {
		guard = *p.ResourceGuardResourceId
	}
	if !hasID || guard == "" {
		// Never made in Azure (or never read back): nothing to unlock.
		return next(ctx, log, resolver, armClient, obj)
	}
	if err := unlockDelete(ctx, armClient, id, guard, proxy.GetAPIVersion()); err != nil {
		return extensions.DeleteResult{}, eris.Wrapf(err, "unlocking the delete of %s", id)
	}
	log.V(1).Info("Unlocked the resource guard proxy's delete", "id", id, "guard", guard)
	return next(ctx, log, resolver, armClient, obj)
}

// unlockDelete POSTs the proxy's unlockDelete with ASO's own credential and
// client options (ASO's client has no POST with a body).
func unlockDelete(ctx context.Context, armClient *outoftree.GenericClient, proxyID, guardID, apiVersion string) error {
	opts := armClient.ClientOptions()
	endpoint := opts.Cloud.Services[cloud.ResourceManager].Endpoint
	pl, err := armruntime.NewPipeline("aso-extensions", "v0", armClient.Creds(), runtime.PipelineOptions{}, &arm.ClientOptions{ClientOptions: opts.ClientOptions})
	if err != nil {
		return err
	}
	req, err := runtime.NewRequest(ctx, http.MethodPost, runtime.JoinPaths(endpoint, proxyID, "unlockDelete"))
	if err != nil {
		return err
	}
	q := req.Raw().URL.Query()
	q.Set("api-version", apiVersion)
	req.Raw().URL.RawQuery = q.Encode()
	if err := runtime.MarshalAsJSON(req, map[string]any{
		"resourceGuardOperationRequests": []string{guardID + "/deleteResourceGuardProxyRequests/default"},
		"resourceToBeDeleted":            proxyID,
	}); err != nil {
		return err
	}
	resp, err := pl.Do(req)
	if err != nil {
		return err
	}
	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return runtime.NewResponseError(resp)
	}
	return nil
}
