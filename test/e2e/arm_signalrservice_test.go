// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	asosignalr "github.com/Azure/azure-service-operator/v2/api/signalrservice/v1api20240301/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	wps "github.com/highfivelabs/aso-extensions/apis/arm/signalrservice/v20240301"
)

// The Web PubSub and SignalR kinds of docs/plan.md f68: a Premium Web PubSub
// service with a hub (an event handler), a replica in another region, a
// custom certificate and the custom domain naming it by object, and a shared
// private link to a function app (by ARM ID); and, on ASO's SignalR and
// Replica by name (new external owners), shared private links and an
// approved private endpoint connection. A hub named with a dash and a
// replica in the service's own region are refused (the fake's rules,
// signalr.go); a change is PUT again; deleted, all go but the replica's
// shared private link, which Azure cannot delete and the kind leaves.
func TestWebPubSubAndSignalRKinds(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "webpubsub")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	chatID := rgID + "/providers/Microsoft.SignalRService/signalR/chat"
	chatReplicaID := chatID + "/replicas/chat-east"
	fnID := rgID + "/providers/Microsoft.Web/sites/negotiate"
	connID := chatID + "/privateEndpointConnections/pe-chat"
	for _, id := range []string{rgID, chatID, chatReplicaID, fnID, connID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	chat := &asosignalr.SignalR{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "chat"}}
	chat.Spec.AzureName, chat.Spec.Location = "chat", ptr("westus")
	chat.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, chat, chatID)
	chatReplica := &asosignalr.Replica{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "chat-east"}}
	chatReplica.Spec.AzureName, chatReplica.Spec.Location = "chat-east", ptr("eastus")
	chatReplica.Spec.Owner = &genruntime.KnownResourceReference{Name: "chat"}
	asoStandIn(t, chatReplica, chatReplicaID)

	premium := func() *wps.ResourceSku {
		return &wps.ResourceSku{Name: ptr("Premium_P1"), Tier: ptr(wps.SignalRSkuTier_Premium), Capacity: ptr(1)}
	}
	service := &wps.WebPubSub{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "events"},
		Spec: wps.WebPubSub_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "rg"}, Location: ptr("westus"),
			Sku: premium(), Kind: ptr(wps.WebpubsubServiceKind_WebPubSub), DisableLocalAuth: ptr(true),
		},
	}
	if err := k8s.Create(ctx, service); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the Web PubSub service Ready", ready(service))

	onService := &genruntime.KnownResourceReference{Name: "events"}
	hub := &wps.WebPubSubHub{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "notifications"},
		Spec: wps.WebPubSubHub_Spec{Owner: onService, Properties: &wps.WebPubSubHubProperties{
			AnonymousConnectPolicy: ptr("deny"),
			EventHandlers:          []wps.EventHandler{{UrlTemplate: ptr("https://negotiate.example.com/api/{hub}/{event}"), UserEventPattern: ptr("*"), SystemEvents: []string{"connected"}}},
		}},
	}
	dashed := &wps.WebPubSubHub{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "chat-room"},
		Spec:       wps.WebPubSubHub_Spec{Owner: onService, Properties: &wps.WebPubSubHubProperties{}},
	}
	replica := &wps.WebPubSubReplica{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "events-east"},
		Spec:       wps.WebPubSubReplica_Spec{Owner: onService, Location: ptr("eastus"), Sku: premium()},
	}
	homebound := &wps.WebPubSubReplica{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "events-west"},
		Spec:       wps.WebPubSubReplica_Spec{Owner: onService, Location: ptr("westus"), Sku: premium()},
	}
	cert := &wps.WebPubSubCustomCertificate{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "events-tls"},
		Spec: wps.WebPubSubCustomCertificate_Spec{
			Owner: onService, KeyVaultBaseUri: ptr("https://events-kv.vault.azure.net/"), KeyVaultSecretName: ptr("events-tls"),
		},
	}
	link := &wps.WebPubSubSharedPrivateLinkResource{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "events-negotiate"},
		Spec: wps.WebPubSubSharedPrivateLinkResource_Spec{
			Owner: onService, GroupId: ptr("sites"), PrivateLinkResourceReference: &genruntime.ResourceReference{ARMID: fnID},
			RequestMessage: ptr("Web PubSub events"),
		},
	}
	chatLink := &wps.SignalRSharedPrivateLinkResource{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "chat-negotiate"},
		Spec: wps.SignalRSharedPrivateLinkResource_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "chat"}, GroupId: ptr("sites"),
			PrivateLinkResourceReference: &genruntime.ResourceReference{ARMID: fnID},
		},
	}
	replicaLink := &wps.SignalRReplicasSharedPrivateLinkResource{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "chat-east-negotiate"},
		Spec: wps.SignalRReplicasSharedPrivateLinkResource_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "chat-east"}, GroupId: ptr("sites"),
			PrivateLinkResourceReference: &genruntime.ResourceReference{ARMID: fnID},
		},
	}
	conn := &wps.SignalRPrivateEndpointConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "chat-pe"},
		Spec: wps.SignalRPrivateEndpointConnection_Spec{
			AzureName: "pe-chat", Owner: &genruntime.KnownResourceReference{Name: "chat"},
			PrivateLinkServiceConnectionState: &wps.PrivateLinkServiceConnectionState{Status: ptr(wps.PrivateLinkServiceConnectionStatus_Approved)},
		},
	}
	second := []conditioned{hub, dashed, replica, homebound, cert, link, chatLink, replicaLink, conn}
	for _, o := range second {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []conditioned{hub, replica, cert, link, chatLink, replicaLink, conn} {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	eventually(t, timeout, "a hub named with a dash refused", notReadyWith(dashed, "starts with a letter"))
	eventually(t, timeout, "a replica in the service's region refused", notReadyWith(homebound, "other than its service's"))

	// The certificate exists: the domain names it by object.
	domain := &wps.WebPubSubCustomDomain{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "events-www"},
		Spec: wps.WebPubSubCustomDomain_Spec{
			Owner: onService, DomainName: ptr("events.example.com"),
			CustomCertificate: &wps.ResourceReference{Reference: &genruntime.ResourceReference{
				Group: "signalrservice.azure.hfl.dev", Kind: "WebPubSubCustomCertificate", Name: "events-tls",
			}},
		},
	}
	if err := k8s.Create(ctx, domain); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the custom domain Ready", ready(domain))

	serviceID := rgID + "/providers/Microsoft.SignalRService/webPubSub/events"
	expectResource(t, serviceID+"/customDomains/events-www", func(r map[string]any) error {
		cc, _ := properties(r)["customCertificate"].(map[string]any)
		if cc["id"] != serviceID+"/customCertificates/events-tls" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	hubID := serviceID + "/hubs/notifications"
	expectResource(t, hubID, func(r map[string]any) error {
		handlers, _ := properties(r)["eventHandlers"].([]any)
		if len(handlers) != 1 || handlers[0].(map[string]any)["urlTemplate"] != "https://negotiate.example.com/api/{hub}/{event}" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again: anonymous connections allowed.
	if err := change(t, hub, func(h *wps.WebPubSubHub) { h.Spec.Properties.AnonymousConnectPolicy = ptr("allow") }); err != nil {
		t.Fatal(err)
	}
	expectResource(t, hubID, func(r map[string]any) error {
		if p := properties(r); p["anonymousConnectPolicy"] != "allow" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, objs := range [][]conditioned{{domain}, second, {service}} {
		for _, o := range objs {
			if err := k8s.Delete(ctx, o); err != nil {
				t.Fatal(err)
			}
		}
		for _, o := range objs {
			eventually(t, timeout, o.GetName()+" gone", gone(o))
		}
	}
	for _, id := range []string{serviceID, hubID, chatID + "/sharedPrivateLinkResources/chat-negotiate", connID} {
		if armFake.HasResource(id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
	if !armFake.HasResource(chatReplicaID + "/sharedPrivateLinkResources/chat-east-negotiate") {
		t.Errorf("the replica's shared private link was deleted")
	}
}
