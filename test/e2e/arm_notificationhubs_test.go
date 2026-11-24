// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	asonotificationhubs "github.com/Azure/azure-service-operator/v2/api/notificationhubs/v1api20230901/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	notificationhubs "github.com/highfivelabs/aso-extensions/apis/arm/notificationhubs/v20230901"
)

// The Notification Hubs kind of docs/plan.md f76, on ASO's Namespace by name
// (a new external owner): a connection a private endpoint made is
// approved, then rejected; one no private endpoint made is refused (the
// fake's rule, privateendpoints.go); deleted, it goes.
func TestNotificationHubsPrivateEndpointConnection(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "nh")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	nsID := rgID + "/providers/Microsoft.NotificationHubs/namespaces/push"
	for _, id := range []string{rgID, nsID, nsID + "/privateEndpointConnections/pe-push.0d6f1a2b-0000-4000-8000-000000000001"} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	push := &asonotificationhubs.Namespace{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "push"}}
	push.Spec.AzureName, push.Spec.Location = "push", ptr("westus")
	push.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, push, nsID)

	connection := func(name, azureName string) *notificationhubs.NamespacesPrivateEndpointConnection {
		return &notificationhubs.NamespacesPrivateEndpointConnection{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec: notificationhubs.NamespacesPrivateEndpointConnection_Spec{AzureName: azureName, Owner: &genruntime.KnownResourceReference{Name: "push"},
				Properties: &notificationhubs.PrivateEndpointConnectionProperties{PrivateLinkServiceConnectionState: &notificationhubs.RemotePrivateLinkServiceConnectionState{
					Status: ptr(notificationhubs.PrivateLinkConnectionStatus_Approved)}}},
		}
	}
	conn, stray := connection("push-pe", "pe-push.0d6f1a2b-0000-4000-8000-000000000001"), connection("push-stray", "pe-stray.0d6f1a2b-0000-4000-8000-000000000002")
	for _, o := range []conditioned{conn, stray} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "the connection approved", ready(conn))
	eventually(t, timeout, "a connection no private endpoint made refused", notReadyWith(stray, "a private endpoint makes it"))

	if err := change(t, conn, func(c *notificationhubs.NamespacesPrivateEndpointConnection) {
		c.Spec.Properties.PrivateLinkServiceConnectionState.Status = ptr(notificationhubs.PrivateLinkConnectionStatus_Rejected)
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, nsID+"/privateEndpointConnections/pe-push.0d6f1a2b-0000-4000-8000-000000000001", func(r map[string]any) error {
		state, _ := properties(r)["privateLinkServiceConnectionState"].(map[string]any)
		if state["status"] != "Rejected" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	for _, o := range []conditioned{conn, stray} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []conditioned{conn, stray} {
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	if armFake.HasResource(nsID + "/privateEndpointConnections/pe-push.0d6f1a2b-0000-4000-8000-000000000001") {
		t.Error("the connection is still in the fake")
	}
}
