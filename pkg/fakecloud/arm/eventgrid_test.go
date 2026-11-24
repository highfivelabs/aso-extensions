// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// A system topic names a source that exists, once; an event subscription's
// destination exists or is an https webhook; a namespace topic's
// subscription delivers by Queue or Push; the MQTT broker's topic spaces,
// client groups, CA certificates and permission bindings carry what they
// need.
func TestEventGrid(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/events"
	storage := rg + "/providers/Microsoft.Storage/storageAccounts/uploads"
	hub := rg + "/providers/Microsoft.EventHub/namespaces/telemetry/eventhubs/blobs"
	ns := rg + "/providers/Microsoft.EventGrid/namespaces/fleet"
	nsTopic := ns + "/topics/telemetry"
	for _, id := range []string{rg, storage, hub, ns, nsTopic} {
		a.AddResource(id)
	}
	put := func(what, id, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, id, body)
		expect(t, what, code, resp, want)
	}
	topic := rg + "/providers/Microsoft.EventGrid/systemTopics/uploads"
	put("a system topic with no source", topic, `{"location":"westus","properties":{"topicType":"Microsoft.Storage.StorageAccounts"}}`, http.StatusBadRequest)
	put("a missing source", topic, `{"location":"westus","properties":{"source":"`+storage+`x","topicType":"Microsoft.Storage.StorageAccounts"}}`, http.StatusNotFound)
	put("a system topic with no type", topic, `{"location":"westus","properties":{"source":"`+storage+`"}}`, http.StatusBadRequest)
	put("a system topic", topic, `{"location":"westus","properties":{"source":"`+storage+`","topicType":"Microsoft.Storage.StorageAccounts"}}`, http.StatusCreated)
	put("a second system topic of the source", rg+"/providers/Microsoft.EventGrid/systemTopics/again", `{"location":"westus","properties":{"source":"`+storage+`","topicType":"Microsoft.Storage.StorageAccounts"}}`, http.StatusConflict)

	sub := topic + "/eventSubscriptions/to-hub"
	put("a subscription with no destination", sub, `{"properties":{}}`, http.StatusBadRequest)
	put("a missing event hub", sub, `{"properties":{"destination":{"endpointType":"EventHub","properties":{"resourceId":"`+hub+`x"}}}}`, http.StatusNotFound)
	put("an http webhook", sub, `{"properties":{"destination":{"endpointType":"WebHook","properties":{"endpointUrl":"http://example.com/hook"}}}}`, http.StatusBadRequest)
	put("a subscription to an event hub", sub, `{"properties":{"destination":{"endpointType":"EventHub","properties":{"resourceId":"`+hub+`"}},"filter":{"includedEventTypes":["Microsoft.Storage.BlobCreated"]}}}`, http.StatusCreated)
	put("a subscription to a webhook", topic+"/eventSubscriptions/hook", `{"properties":{"destination":{"endpointType":"WebHook","properties":{"endpointUrl":"https://example.com/hook"}}}}`, http.StatusCreated)

	nsSub := nsTopic + "/eventSubscriptions/reader"
	put("a namespace subscription of no mode", nsSub, `{"properties":{"deliveryConfiguration":{}}}`, http.StatusBadRequest)
	put("a push with no destination", nsSub, `{"properties":{"deliveryConfiguration":{"deliveryMode":"Push","push":{}}}}`, http.StatusBadRequest)
	put("a queue subscription", nsSub, `{"properties":{"deliveryConfiguration":{"deliveryMode":"Queue","queue":{"maxDeliveryCount":5}}}}`, http.StatusCreated)

	put("a topic space with no template", ns+"/topicSpaces/sensors", `{"properties":{"topicTemplates":[]}}`, http.StatusBadRequest)
	put("a topic space", ns+"/topicSpaces/sensors", `{"properties":{"topicTemplates":["sensors/#"]}}`, http.StatusCreated)
	put("a client group with no query", ns+"/clientGroups/devices", `{"properties":{}}`, http.StatusBadRequest)
	put("a client group", ns+"/clientGroups/devices", `{"properties":{"query":"attributes.type = 'sensor'"}}`, http.StatusCreated)
	put("a CA certificate with no certificate", ns+"/caCertificates/root", `{"properties":{}}`, http.StatusBadRequest)
	binding := ns + "/permissionBindings/publish"
	put("a binding of no permission", binding, `{"properties":{"topicSpaceName":"sensors","clientGroupName":"devices"}}`, http.StatusBadRequest)
	put("a binding to a missing topic space", binding, `{"properties":{"topicSpaceName":"other","clientGroupName":"devices","permission":"Publisher"}}`, http.StatusNotFound)
	put("a binding to a missing client group", binding, `{"properties":{"topicSpaceName":"sensors","clientGroupName":"other","permission":"Publisher"}}`, http.StatusNotFound)
	put("a binding", binding, `{"properties":{"topicSpaceName":"sensors","clientGroupName":"devices","permission":"Publisher"}}`, http.StatusCreated)
}
