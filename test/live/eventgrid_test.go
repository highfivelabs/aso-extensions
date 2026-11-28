// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	eventgrid "github.com/highfivelabs/aso-extensions/apis/arm/eventgrid/v20250215"
)

const eventGridAPI = "2025-02-15"

// A system topic on a storage account and an event subscription sending
// the account's BlobCreated events to a queue in it (docs/plan.md f63):
// created, the filter changed, deleted. Cheap: a StorageV2 account and a
// queue (storage cents); Event Grid bills operations, and nothing is
// published. Not here: the MQTT broker's children and namespace topic
// subscriptions (an Event Grid namespace, billed by throughput unit and
// hour) and webhook destinations (an endpoint that answers Event Grid's
// validation handshake).
func TestLiveEventGridSystemTopic(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.EventGrid")
	ns := namespace(t, "egsys")
	accountID, _ := storageAccount(t, "egsrc")
	fixture(t, accountID+"/queueServices/default/queues/uploads", "2023-05-01", map[string]any{"properties": map[string]any{}})

	topic := &eventgrid.SystemTopic{}
	topic.Namespace, topic.Name = ns, "uploads"
	topic.Spec = eventgrid.SystemTopic_Spec{
		AzureName: azName("uploads"), Owner: owner("rg"), Location: ptr(location),
		SourceReference: &genruntime.ResourceReference{ARMID: accountID}, TopicType: ptr("Microsoft.Storage.StorageAccounts"),
	}
	create(t, timeout, topic)
	topicID := rgID() + "/providers/Microsoft.EventGrid/systemTopics/" + topic.Spec.AzureName

	sub := &eventgrid.SystemTopicsEventSubscription{}
	sub.Namespace, sub.Name = ns, "to-queue"
	sub.Spec = eventgrid.SystemTopicsEventSubscription_Spec{
		AzureName: "to-queue", Owner: owner("uploads"),
		Destination: &eventgrid.EventSubscriptionDestination{StorageQueue: &eventgrid.StorageQueueEventSubscriptionDestination{
			EndpointType:      ptr(eventgrid.StorageQueueEventSubscriptionDestination_EndpointType_StorageQueue),
			ResourceReference: &genruntime.ResourceReference{ARMID: accountID}, QueueName: ptr("uploads"),
		}},
		Filter: &eventgrid.EventSubscriptionFilter{IncludedEventTypes: []string{"Microsoft.Storage.BlobCreated"}},
	}
	create(t, timeout, sub)
	subID := topicID + "/eventSubscriptions/to-queue"
	expectResource(t, topicID, eventGridAPI, func(r map[string]any) error {
		if p := properties(r); !strings.EqualFold(fmt.Sprint(p["source"]), accountID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, sub, func(s *eventgrid.SystemTopicsEventSubscription) {
		s.Spec.Filter.IncludedEventTypes = []string{"Microsoft.Storage.BlobCreated", "Microsoft.Storage.BlobDeleted"}
	})
	eventually(t, timeout, "the subscription Ready after the change", ready(sub))
	expectResource(t, subID, eventGridAPI, func(r map[string]any) error {
		filter, _ := properties(r)["filter"].(map[string]any)
		if !reflect.DeepEqual(filter["includedEventTypes"], []any{"Microsoft.Storage.BlobCreated", "Microsoft.Storage.BlobDeleted"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, sub)
	expectGone(t, subID, eventGridAPI)
	remove(t, timeout, topic)
	expectGone(t, topicID, eventGridAPI)
}
