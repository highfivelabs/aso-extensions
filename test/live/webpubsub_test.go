// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"testing"

	wps "github.com/highfivelabs/aso-extensions/apis/arm/signalrservice/v20240301"
)

const webPubSubAPI = "2024-03-01"

// A Free Web PubSub service and a hub on it with an event handler
// (docs/plan.md f68): created, the hub's anonymous policy changed, deleted.
// Free: the Free_F1 tier bills nothing (a subscription holds a limited
// number of Free units; a run that hits the limit fails on the service).
// Not here: replicas, custom domains and shared private links (Premium or
// Standard services, and their targets).
func TestLiveWebPubSubHub(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.SignalRService")
	ns := namespace(t, "wpshub")
	service := &wps.WebPubSub{}
	service.Namespace, service.Name = ns, "events"
	service.Spec = wps.WebPubSub_Spec{
		AzureName: azName("events"), Owner: owner("rg"), Location: ptr(location),
		Sku: &wps.ResourceSku{Name: ptr("Free_F1"), Capacity: ptr(1)}, Kind: ptr(wps.WebpubsubServiceKind_WebPubSub),
	}
	create(t, timeout, service)
	serviceID := rgID() + "/providers/Microsoft.SignalRService/webPubSub/" + service.Spec.AzureName

	hub := &wps.WebPubSubHub{}
	hub.Namespace, hub.Name = ns, "notifications"
	hub.Spec = wps.WebPubSubHub_Spec{
		AzureName: "notifications", Owner: owner("events"),
		Properties: &wps.WebPubSubHubProperties{
			AnonymousConnectPolicy: ptr("deny"),
			EventHandlers:          []wps.EventHandler{{UrlTemplate: ptr("https://events.example.com/api/{hub}/{event}"), UserEventPattern: ptr("*")}},
		},
	}
	create(t, timeout, hub)
	hubID := serviceID + "/hubs/notifications"
	expectResource(t, hubID, webPubSubAPI, func(r map[string]any) error {
		handlers, _ := properties(r)["eventHandlers"].([]any)
		if len(handlers) != 1 || properties(r)["anonymousConnectPolicy"] != "deny" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, hub, func(h *wps.WebPubSubHub) { h.Spec.Properties.AnonymousConnectPolicy = ptr("allow") })
	eventually(t, timeout, "the hub Ready after the change", ready(hub))
	expectResource(t, hubID, webPubSubAPI, func(r map[string]any) error {
		if p := properties(r); p["anonymousConnectPolicy"] != "allow" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, hub)
	expectGone(t, hubID, webPubSubAPI)
	remove(t, timeout, service)
	expectGone(t, serviceID, webPubSubAPI)
}
