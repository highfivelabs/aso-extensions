// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	armwebv1 "github.com/highfivelabs/aso-extensions/apis/arm/web/v20260715"
	webv1 "github.com/highfivelabs/aso-extensions/apis/web/v20260715"
)

// A site's app settings and connection strings (hand-written kinds, PUT and
// POST .../list through Resource Manager), each value plain, from a Secret
// or from a ConfigMap (one Secret's key put into a template, a storage
// connection string around its access key): the whole set PUT under ASO's Site with the
// namespace's identity, no value in the status, a Secret's change sent
// again, a setting removed from the list removed in Azure, a missing Secret
// reported and then picked up, a deployment slot (our SitesSlot) as the
// owner, and deleting the objects leaving the last values on the app (Azure
// has no DELETE for the sections).
func TestSiteAppSettingsAndConnectionStrings(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "web-appsettings")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	siteID, rgID := appService(t, ns)
	aiSecret := "InstrumentationKey=00000000-0000-0000-0000-000000000000;IngestionEndpoint=https://x/;secret-1"
	dbSecret := "Server=tcp:db.database.windows.net;Password=db-password-1"
	secret(t, ns, "app", map[string]string{"ai": aiSecret, "db": dbSecret, "storagekey": "c3RvcmFnZS1rZXktMQ=="})
	storage := "DefaultEndpointsProtocol=https;AccountName=st1;AccountKey=c3RvcmFnZS1rZXktMQ=="
	if err := k8s.Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "app-config"},
		Data:       map[string]string{"region": "westus"},
	}); err != nil {
		t.Fatal(err)
	}

	settings := &webv1.SitesConfigAppSettings{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "settings"},
		Spec: webv1.SitesConfigAppSettingsSpec{
			Owner: &webv1.SiteOwnerReference{Name: "site"},
			Settings: []webv1.AppSetting{
				{Name: "PLAIN", Value: ptr("one")},
				{Name: "APPLICATIONINSIGHTS_CONNECTION_STRING", ValueFromSecret: &genruntime.SecretReference{Name: "app", Key: "ai"}},
				{Name: "REGION", ValueFromConfig: &genruntime.ConfigMapReference{Name: "app-config", Key: "region"}},
				{
					Name: "AzureWebJobsStorage", ValueFromSecret: &genruntime.SecretReference{Name: "app", Key: "storagekey"},
					Template: ptr("DefaultEndpointsProtocol=https;AccountName=st1;AccountKey=$(value)"),
				},
			},
		},
	}
	strs := &webv1.SitesConfigConnectionStrings{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "strings"},
		Spec: webv1.SitesConfigConnectionStringsSpec{
			Owner: &webv1.SiteOwnerReference{Name: "site", Group: "web.azure.com", Kind: "Site"},
			ConnectionStrings: []webv1.ConnectionString{
				{Name: "db", Type: "SQLAzure", ValueFromSecret: &genruntime.SecretReference{Name: "app", Key: "db"}},
				{Name: "cache", Type: "RedisCache", Value: ptr("cache.redis.cache.windows.net:6380")},
			},
		},
	}
	for _, o := range []conditioned{settings, strs} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []conditioned{settings, strs} {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}

	settingsID := siteID + "/config/appsettings"
	strsID := siteID + "/config/connectionstrings"
	expectProperties(t, settingsID, map[string]any{
		"PLAIN": "one", "APPLICATIONINSIGHTS_CONNECTION_STRING": aiSecret, "REGION": "westus",
		"AzureWebJobsStorage": storage,
	})
	expectProperties(t, strsID, map[string]any{
		"db":    map[string]any{"value": dbSecret, "type": "SQLAzure"},
		"cache": map[string]any{"value": "cache.redis.cache.windows.net:6380", "type": "RedisCache"},
	})

	// The status names the settings and never holds a value.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(settings), settings); err != nil {
		t.Fatal(err)
	}
	if settings.Status.Id != settingsID || !reflect.DeepEqual(settings.Status.Names, []string{"APPLICATIONINSIGHTS_CONNECTION_STRING", "AzureWebJobsStorage", "PLAIN", "REGION"}) {
		t.Fatalf("status: %+v", settings.Status)
	}
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(strs), strs); err != nil {
		t.Fatal(err)
	}
	if want := []webv1.ConnectionStringStatus{{Name: "cache", Type: "RedisCache"}, {Name: "db", Type: "SQLAzure"}}; !reflect.DeepEqual(strs.Status.ConnectionStrings, want) {
		t.Fatalf("status: %+v", strs.Status)
	}
	for _, s := range []any{settings.Status, strs.Status} {
		if b, err := json.Marshal(s); err != nil || contains(string(b), "secret-1") || contains(string(b), "db-password-1") || contains(string(b), "6380") || contains(string(b), "c3RvcmFnZS1rZXktMQ") {
			t.Fatalf("status holds a value: %s %v", b, err)
		}
	}

	// A change to the Secret is sent again.
	s := &corev1.Secret{}
	if err := k8s.Get(ctx, client.ObjectKey{Namespace: ns, Name: "app"}, s); err != nil {
		t.Fatal(err)
	}
	s.Data["ai"] = []byte("InstrumentationKey=rotated")
	if err := k8s.Update(ctx, s); err != nil {
		t.Fatal(err)
	}
	expectProperties(t, settingsID, map[string]any{
		"PLAIN": "one", "APPLICATIONINSIGHTS_CONNECTION_STRING": "InstrumentationKey=rotated", "REGION": "westus",
		"AzureWebJobsStorage": storage,
	})

	// The list is the whole set: a setting taken out of it goes in Azure.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(settings), settings); err != nil {
		t.Fatal(err)
	}
	settings.Spec.Settings = settings.Spec.Settings[1:]
	if err := k8s.Update(ctx, settings); err != nil {
		t.Fatal(err)
	}
	expectProperties(t, settingsID, map[string]any{
		"APPLICATIONINSIGHTS_CONNECTION_STRING": "InstrumentationKey=rotated", "REGION": "westus",
		"AzureWebJobsStorage": storage,
	})

	// A value is exactly one of value, valueFromSecret and valueFromConfig.
	bad := &webv1.SitesConfigAppSettings{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "bad"},
		Spec: webv1.SitesConfigAppSettingsSpec{
			Owner:    &webv1.SiteOwnerReference{Name: "site"},
			Settings: []webv1.AppSetting{{Name: "TWO", Value: ptr("x"), ValueFromSecret: &genruntime.SecretReference{Name: "app", Key: "ai"}}},
		},
	}
	if err := k8s.Create(ctx, bad); err == nil || !contains(err.Error(), "exactly one of value") {
		t.Fatalf("a setting with two values: %v", err)
	}
	bad.Spec.Settings = []webv1.AppSetting{{Name: "T", Value: ptr("x"), Template: ptr("a=$(value)")}}
	if err := k8s.Create(ctx, bad); err == nil || !contains(err.Error(), "template takes valueFromSecret") {
		t.Fatalf("a template on a plain value: %v", err)
	}

	// A deployment slot (our SitesSlot) owns its own settings; a Secret that
	// is not there yet is reported, then picked up.
	planID := rgID + "/providers/Microsoft.Web/serverfarms/plan"
	slot := &armwebv1.SitesSlot{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "staging"},
		Spec: armwebv1.SitesSlot_Spec{
			Owner:               &genruntime.KnownResourceReference{Name: "site"},
			Location:            ptr("westus"),
			ServerFarmReference: &genruntime.ResourceReference{ARMID: planID},
		},
	}
	if err := k8s.Create(ctx, slot); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "SitesSlot Ready", ready(slot))
	slotSettings := &webv1.SitesConfigAppSettings{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "staging-settings"},
		Spec: webv1.SitesConfigAppSettingsSpec{
			Owner:    &webv1.SiteOwnerReference{Name: "staging", Group: "web.azure.hfl.dev", Kind: "SitesSlot"},
			Settings: []webv1.AppSetting{{Name: "SLOT_KEY", ValueFromSecret: &genruntime.SecretReference{Name: "slot", Key: "key"}}},
		},
	}
	if err := k8s.Create(ctx, slotSettings); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "slot settings waiting for the Secret", notReadyWith(slotSettings, ns+"/slot"))
	secret(t, ns, "slot", map[string]string{"key": "slot-secret-1"})
	eventually(t, timeout, "slot settings Ready", ready(slotSettings))
	slotSettingsID := siteID + "/slots/staging/config/appsettings"
	expectProperties(t, slotSettingsID, map[string]any{"SLOT_KEY": "slot-secret-1"})
	expectResource(t, slotSettingsID, func(r map[string]any) error {
		if r["type"] != "Microsoft.Web/sites/slots/config" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// Deleting the objects leaves the last values on the apps.
	for _, o := range []client.Object{settings, strs, slotSettings} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	expectProperties(t, settingsID, map[string]any{
		"APPLICATIONINSIGHTS_CONNECTION_STRING": "InstrumentationKey=rotated", "REGION": "westus",
		"AzureWebJobsStorage": storage,
	})
	expectProperties(t, slotSettingsID, map[string]any{"SLOT_KEY": "slot-secret-1"})
	if !armFake.HasResource(strsID) {
		t.Fatalf("%s was deleted; it stays with the site", strsID)
	}
	if err := k8s.Delete(ctx, slot); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "SitesSlot gone", gone(slot))
}

// expectProperties waits until the fake holds exactly want as the
// resource's properties.
func expectProperties(t *testing.T, id string, want map[string]any) {
	t.Helper()
	expectResource(t, id, func(r map[string]any) error {
		if got := properties(r); !reflect.DeepEqual(got, want) {
			return fmt.Errorf("properties %v, want %v", got, want)
		}
		return nil
	})
}
