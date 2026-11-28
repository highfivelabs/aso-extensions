// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	asoeventgrid "github.com/Azure/azure-service-operator/v2/api/eventgrid/v20250215/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	eventgrid "github.com/highfivelabs/aso-extensions/apis/arm/eventgrid/v20250215"
)

// The Event Grid kinds of docs/plan.md f63: a system topic on a storage
// account (by ARM ID) with two event subscriptions, one to an event hub (by
// ARM ID) and one to a webhook whose URL is in a Secret; on ASO's Namespace
// and NamespaceTopic by name (new external owners), a queue subscription of
// the namespace topic and the MQTT broker's topic space, client group,
// client and the permission binding naming them. A second system topic of
// the same source, a subscription to a missing event hub and a binding to a
// missing topic space are refused (the fake's rules, eventgrid.go); a change
// is PUT again; deleted, all go.
func TestEventGridKinds(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "eventgrid")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	storageID := rgID + "/providers/Microsoft.Storage/storageAccounts/uploads"
	hubID := rgID + "/providers/Microsoft.EventHub/namespaces/telemetry/eventhubs/blobs"
	fleetID := rgID + "/providers/Microsoft.EventGrid/namespaces/fleet"
	readingsID := fleetID + "/topics/readings"
	for _, id := range []string{rgID, storageID, hubID, fleetID, readingsID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	fleet := &asoeventgrid.Namespace{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "fleet"}}
	fleet.Spec.AzureName, fleet.Spec.Location = "fleet", ptr("westus")
	fleet.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, fleet, fleetID)
	readings := &asoeventgrid.NamespaceTopic{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "readings"}}
	readings.Spec.AzureName = "readings"
	readings.Spec.Owner = &genruntime.KnownResourceReference{Name: "fleet"}
	asoStandIn(t, readings, readingsID)
	if err := k8s.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "hook"},
		StringData: map[string]string{"url": "https://hooks.example.com/uploads"},
	}); err != nil {
		t.Fatal(err)
	}

	// The system topic first: its subscriptions are owned by it.
	topic := &eventgrid.SystemTopic{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "uploads"},
		Spec: eventgrid.SystemTopic_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "rg"}, Location: ptr("westus"),
			SourceReference: &genruntime.ResourceReference{ARMID: storageID}, TopicType: ptr("Microsoft.Storage.StorageAccounts"),
		},
	}
	again := &eventgrid.SystemTopic{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "uploads-again"},
		Spec: eventgrid.SystemTopic_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "rg"}, Location: ptr("westus"),
			SourceReference: &genruntime.ResourceReference{ARMID: storageID}, TopicType: ptr("Microsoft.Storage.StorageAccounts"),
		},
	}
	space := &eventgrid.NamespacesTopicSpace{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "sensors"},
		Spec: eventgrid.NamespacesTopicSpace_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "fleet"}, TopicTemplates: []string{"sensors/${client.authenticationName}/#"},
		},
	}
	group := &eventgrid.NamespacesClientGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "devices"},
		Spec: eventgrid.NamespacesClientGroup_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "fleet"}, Query: ptr("attributes.type = 'sensor'"),
		},
	}
	for _, o := range []conditioned{topic, space, group} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []conditioned{topic, space, group} {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}

	toHub := &eventgrid.SystemTopicsEventSubscription{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "to-hub"},
		Spec: eventgrid.SystemTopicsEventSubscription_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "uploads"},
			Destination: &eventgrid.EventSubscriptionDestination{EventHub: &eventgrid.EventHubEventSubscriptionDestination{
				EndpointType:      ptr(eventgrid.EventHubEventSubscriptionDestination_EndpointType_EventHub),
				ResourceReference: &genruntime.ResourceReference{ARMID: hubID},
			}},
			Filter: &eventgrid.EventSubscriptionFilter{IncludedEventTypes: []string{"Microsoft.Storage.BlobCreated"}},
		},
	}
	toHook := &eventgrid.SystemTopicsEventSubscription{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "to-hook"},
		Spec: eventgrid.SystemTopicsEventSubscription_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "uploads"},
			Destination: &eventgrid.EventSubscriptionDestination{WebHook: &eventgrid.WebHookEventSubscriptionDestination{
				EndpointType: ptr(eventgrid.WebHookEventSubscriptionDestination_EndpointType_WebHook),
				EndpointUrl:  &genruntime.SecretReference{Name: "hook", Key: "url"},
			}},
		},
	}
	toNowhere := &eventgrid.SystemTopicsEventSubscription{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "to-nowhere"},
		Spec: eventgrid.SystemTopicsEventSubscription_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "uploads"},
			Destination: &eventgrid.EventSubscriptionDestination{EventHub: &eventgrid.EventHubEventSubscriptionDestination{
				EndpointType:      ptr(eventgrid.EventHubEventSubscriptionDestination_EndpointType_EventHub),
				ResourceReference: &genruntime.ResourceReference{ARMID: hubID + "-gone"},
			}},
		},
	}
	reader := &eventgrid.NamespacesTopicsEventSubscription{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "reader"},
		Spec: eventgrid.NamespacesTopicsEventSubscription_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "readings"},
			DeliveryConfiguration: &eventgrid.DeliveryConfiguration{
				DeliveryMode: ptr(eventgrid.DeliveryConfiguration_DeliveryMode_Queue),
				Queue:        &eventgrid.QueueInfo{MaxDeliveryCount: ptr(5), ReceiveLockDurationInSeconds: ptr(60)},
			},
		},
	}
	device := &eventgrid.NamespacesClient{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "sensor-1"},
		Spec: eventgrid.NamespacesClient_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "fleet"}, AuthenticationName: ptr("sensor-1"),
			State: ptr(eventgrid.ClientProperties_State_Enabled),
		},
	}
	binding := &eventgrid.NamespacesPermissionBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "publish"},
		Spec: eventgrid.NamespacesPermissionBinding_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "fleet"}, TopicSpaceName: ptr("sensors"), ClientGroupName: ptr("devices"),
			Permission: ptr(eventgrid.PermissionBindingProperties_Permission_Publisher),
		},
	}
	stray := &eventgrid.NamespacesPermissionBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "stray"},
		Spec: eventgrid.NamespacesPermissionBinding_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "fleet"}, TopicSpaceName: ptr("elsewhere"), ClientGroupName: ptr("devices"),
			Permission: ptr(eventgrid.PermissionBindingProperties_Permission_Publisher),
		},
	}
	later := []conditioned{again, toHub, toHook, toNowhere, reader, device, binding, stray}
	for _, o := range later {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []conditioned{toHub, toHook, reader, device, binding} {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	eventually(t, timeout, "a second system topic of the source refused", notReadyWith(again, "has a system topic already"))
	eventually(t, timeout, "a missing event hub refused", notReadyWith(toNowhere, "was not found"))
	eventually(t, timeout, "a missing topic space refused", notReadyWith(stray, "was not found"))

	topicID := rgID + "/providers/Microsoft.EventGrid/systemTopics/uploads"
	expectResource(t, topicID, func(r map[string]any) error {
		if p := properties(r); p["source"] != storageID || p["topicType"] != "Microsoft.Storage.StorageAccounts" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	hubSubID := topicID + "/eventSubscriptions/to-hub"
	expectResource(t, hubSubID, func(r map[string]any) error {
		dest, _ := properties(r)["destination"].(map[string]any)
		dp, _ := dest["properties"].(map[string]any)
		if dest["endpointType"] != "EventHub" || dp["resourceId"] != hubID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, topicID+"/eventSubscriptions/to-hook", func(r map[string]any) error {
		dest, _ := properties(r)["destination"].(map[string]any)
		dp, _ := dest["properties"].(map[string]any)
		if dp["endpointUrl"] != "https://hooks.example.com/uploads" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, fleetID+"/permissionBindings/publish", func(r map[string]any) error {
		if p := properties(r); p["topicSpaceName"] != "sensors" || p["clientGroupName"] != "devices" || p["permission"] != "Publisher" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again: a second event type.
	if err := change(t, toHub, func(s *eventgrid.SystemTopicsEventSubscription) {
		s.Spec.Filter.IncludedEventTypes = append(s.Spec.Filter.IncludedEventTypes, "Microsoft.Storage.BlobDeleted")
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, hubSubID, func(r map[string]any) error {
		filter, _ := properties(r)["filter"].(map[string]any)
		if !reflect.DeepEqual(filter["includedEventTypes"], []any{"Microsoft.Storage.BlobCreated", "Microsoft.Storage.BlobDeleted"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// The children first, then what they need.
	for _, objs := range [][]conditioned{later, {topic, space, group}} {
		for _, o := range objs {
			if err := k8s.Delete(ctx, o); err != nil {
				t.Fatal(err)
			}
		}
		for _, o := range objs {
			eventually(t, timeout, o.GetName()+" gone", gone(o))
		}
	}
	for _, id := range []string{topicID, hubSubID, readingsID + "/eventSubscriptions/reader", fleetID + "/permissionBindings/publish", fleetID + "/topicSpaces/sensors"} {
		if armFake.HasResource(id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
}
