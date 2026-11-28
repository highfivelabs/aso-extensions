// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"testing"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	webv1 "github.com/highfivelabs/aso-extensions/apis/arm/web/v20260715"
)

const staticSiteAPI = "2026-07-15"

// A Standard static web app with no content and password protection on its
// staging environments, then on all of them (docs/plan.md f53). The Standard
// plan bills per app-month, prorated (cents for a run); Static Web Apps
// runs in a few regions, so the app is in westus2 whatever the live
// region. Basic authentication has no DELETE: detached, it goes with the
// app. The password, from a Secret, is made per run.
func TestLiveStaticSiteBasicAuth(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.Web")
	ns := namespace(t, "swa-auth")
	secret(t, ns, "swa", map[string]string{"password": "Live-" + runID + "-Aa1!"})
	site := &webv1.StaticSite{}
	site.Namespace, site.Name = ns, "docs"
	site.Spec = webv1.StaticSite_Spec{
		AzureName: azName("docs"), Owner: owner("rg"), Location: ptr("westus2"),
		Sku: &webv1.SkuDescription{Name: ptr("Standard"), Tier: ptr("Standard")},
	}
	create(t, timeout, site)
	siteID := rgID() + "/providers/Microsoft.Web/staticSites/" + site.Spec.AzureName

	auth := &webv1.StaticSitesBasicAuth{}
	auth.Namespace, auth.Name = ns, "auth"
	auth.Annotations = map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}
	auth.Spec = webv1.StaticSitesBasicAuth_Spec{
		Owner: owner("docs"), ApplicableEnvironmentsMode: ptr("StagingEnvironments"),
		Password: &genruntime.SecretReference{Name: "swa", Key: "password"},
	}
	create(t, timeout, auth)
	expectResource(t, siteID+"/basicAuth/default", staticSiteAPI, func(r map[string]any) error {
		if properties(r)["applicableEnvironmentsMode"] != "StagingEnvironments" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	update(t, auth, func(a *webv1.StaticSitesBasicAuth) { a.Spec.ApplicableEnvironmentsMode = ptr("AllEnvironments") })
	eventually(t, timeout, "basic authentication Ready after the change", ready(auth))
	expectResource(t, siteID+"/basicAuth/default", staticSiteAPI, func(r map[string]any) error {
		if properties(r)["applicableEnvironmentsMode"] != "AllEnvironments" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, auth)
	remove(t, timeout, site)
	expectGone(t, siteID, staticSiteAPI)
}
