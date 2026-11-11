// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	si "github.com/highfivelabs/aso-extensions/apis/arm/securityinsights/v20250901"
)

// A child of an extension resource owned through Kubernetes (generator
// patch 0012): a Sentinel watchlist, an extension resource on a Log Analytics
// workspace (by ARM ID), and an item owned by the watchlist object. The
// item's ARM ID is the watchlist's (with the workspace's scope) and its own
// type and name; ASO's resolver refused it as an extension resource with
// two types, and would have left the workspace's scope out. The conformance
// test owns every kind by ARM ID, so this is the one that walks the
// Kubernetes owner chain.
func TestSentinelWatchlistItem(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "sentinel-watchlist")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	workspaceID := rgID + "/providers/Microsoft.OperationalInsights/workspaces/law"
	armFake.AddResource(rgID)
	armFake.AddResource(workspaceID)

	watchlist := &si.Watchlist{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "allowed-ips"},
		Spec: si.Watchlist_Spec{
			Owner:          &genruntime.ArbitraryOwnerReference{ARMID: workspaceID},
			DisplayName:    ptr("Allowed IPs"),
			ItemsSearchKey: ptr("ip"),
			Provider:       ptr("Microsoft"),
		},
	}
	if err := k8s.Create(ctx, watchlist); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "Watchlist Ready", ready(watchlist))
	watchlistID := workspaceID + "/providers/Microsoft.SecurityInsights/watchlists/allowed-ips"
	if !armFake.HasResource(watchlistID) {
		t.Fatalf("%s is not in Resource Manager", watchlistID)
	}

	item := &si.WatchlistsWatchlistItem{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "office"},
		Spec: si.WatchlistsWatchlistItem_Spec{
			Owner:         &genruntime.KnownResourceReference{Name: "allowed-ips"},
			ItemsKeyValue: &apiextensionsv1.JSON{Raw: []byte(`{"ip":"203.0.113.7"}`)},
		},
	}
	if err := k8s.Create(ctx, item); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "WatchlistsWatchlistItem Ready", ready(item))
	itemID := watchlistID + "/watchlistItems/office"
	expectResource(t, itemID, func(r map[string]any) error {
		if r["type"] != "Microsoft.SecurityInsights/watchlists/watchlistItems" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if got := item.GetAnnotations()[genruntime.ResourceIDAnnotation]; got != itemID {
		t.Fatalf("the item's ARM ID annotation is %q, not %q", got, itemID)
	}

	if err := k8s.Delete(ctx, item); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "WatchlistsWatchlistItem gone", gone(item))
	if armFake.HasResource(itemID) {
		t.Fatalf("%s is still in Resource Manager", itemID)
	}
	if err := k8s.Delete(ctx, watchlist); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "Watchlist gone", gone(watchlist))
}
