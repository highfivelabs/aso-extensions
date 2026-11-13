// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	webv1 "github.com/highfivelabs/aso-extensions/apis/arm/web/v20260715"
)

// A site's fixed-name config sections (authsettingsV2, logs,
// slotConfigNames) and its basic publishing credentials policies (ftp, scm),
// each its own kind (generator patch 0014): PUT at the fixed name under ASO's
// Site, a log destination's SAS URL from a Secret (never in the status), a
// change PUT again; deleting an object leaves the section in Azure, which
// has no DELETE for it and deletes it with the site.
func TestSiteConfigSections(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "web-config")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	siteID, _ := appService(t, ns)
	sasURL := "https://logs.blob.core.windows.net/app?sv=2024-01-01&sig=c2lnbmF0dXJl"
	secret(t, ns, "logs", map[string]string{"sas": sasURL})
	owner := &genruntime.KnownResourceReference{Name: "site"}

	logs := &webv1.SitesConfigLogs{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "logs"},
		Spec: webv1.SitesConfigLogs_Spec{
			Owner: owner,
			ApplicationLogs: &webv1.ApplicationLogsConfig{AzureBlobStorage: &webv1.AzureBlobStorageApplicationLogsConfig{
				Level: ptr(webv1.LogLevel("Warning")), RetentionInDays: ptr(7),
				SasUrl: &genruntime.SecretReference{Name: "logs", Key: "sas"},
			}},
			DetailedErrorMessages: &webv1.EnabledConfig{Enabled: ptr(true)},
			FailedRequestsTracing: &webv1.EnabledConfig{Enabled: ptr(false)},
		},
	}
	sticky := &webv1.SitesConfigSlotConfigNames{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "sticky"},
		Spec: webv1.SitesConfigSlotConfigNames_Spec{
			Owner:                 owner,
			AppSettingNames:       []string{"SLOT_NAME"},
			ConnectionStringNames: []string{"Primary"},
		},
	}
	auth := &webv1.SitesConfigAuthSettingsV2{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "auth"},
		Spec: webv1.SitesConfigAuthSettingsV2_Spec{
			Owner:    owner,
			Platform: &webv1.AuthPlatform{Enabled: ptr(true), RuntimeVersion: ptr("~1")},
			GlobalValidation: &webv1.GlobalValidation{
				RequireAuthentication:       ptr(true),
				UnauthenticatedClientAction: ptr(webv1.UnauthenticatedClientActionV2("Return401")),
			},
		},
	}
	ftp := &webv1.SitesBasicPublishingCredentialsPolicyFtp{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "ftp"},
		Spec:       webv1.SitesBasicPublishingCredentialsPolicyFtp_Spec{Owner: owner, Allow: ptr(false)},
	}
	scm := &webv1.SitesBasicPublishingCredentialsPolicyScm{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "scm"},
		Spec:       webv1.SitesBasicPublishingCredentialsPolicyScm_Spec{Owner: owner, Allow: ptr(false)},
	}
	objs := []conditioned{logs, sticky, auth, ftp, scm}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}

	expectResource(t, siteID+"/config/logs", func(r map[string]any) error {
		p := properties(r)
		app, _ := p["applicationLogs"].(map[string]any)
		blob, _ := app["azureBlobStorage"].(map[string]any)
		if r["type"] != "Microsoft.Web/sites/config" || blob["sasUrl"] != sasURL || blob["level"] != "Warning" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, siteID+"/config/slotConfigNames", func(r map[string]any) error {
		if p := properties(r); !reflect.DeepEqual(p["appSettingNames"], []any{"SLOT_NAME"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, siteID+"/config/authsettingsV2", func(r map[string]any) error {
		gv, _ := properties(r)["globalValidation"].(map[string]any)
		if gv["unauthenticatedClientAction"] != "Return401" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	for _, name := range []string{"ftp", "scm"} {
		expectResource(t, siteID+"/basicPublishingCredentialsPolicies/"+name, func(r map[string]any) error {
			if r["type"] != "Microsoft.Web/sites/basicPublishingCredentialsPolicies" || properties(r)["allow"] != false {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})
	}

	// The status never holds the SAS URL.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(logs), logs); err != nil {
		t.Fatal(err)
	}
	if b, err := json.Marshal(logs.Status); err != nil || contains(string(b), "sig=") {
		t.Fatalf("status holds the SAS URL: %s %v", b, err)
	}

	// A change is PUT again.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(ftp), ftp); err != nil {
		t.Fatal(err)
	}
	ftp.Spec.Allow = ptr(true)
	if err := k8s.Update(ctx, ftp); err != nil {
		t.Fatal(err)
	}
	expectResource(t, siteID+"/basicPublishingCredentialsPolicies/ftp", func(r map[string]any) error {
		if properties(r)["allow"] != true {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// Deleting the objects leaves the sections with the site.
	for _, o := range objs {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, id := range []string{"/config/logs", "/config/slotConfigNames", "/config/authsettingsV2",
		"/basicPublishingCredentialsPolicies/ftp", "/basicPublishingCredentialsPolicies/scm"} {
		if !armFake.HasResource(siteID + id) {
			t.Fatalf("%s%s was deleted; it stays with the site", siteID, id)
		}
	}
}
