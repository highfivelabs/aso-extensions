// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	asomysql "github.com/Azure/azure-service-operator/v2/api/dbformysql/v20241230/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	mysql "github.com/highfivelabs/aso-extensions/apis/arm/dbformysql/v20241230"
)

// A MySQL flexible server's children of docs/plan.md f66, owned by ASO's
// FlexibleServer by name (a new external owner): advanced threat
// protection turned on, then off (PUT again), and the approval of a
// connection a private endpoint made. A setting of another name and a
// connection no private endpoint made are refused (the fake's rules,
// mysql.go and privateendpoints.go). Deleted, the connection goes; the
// setting, which Azure cannot delete, is left (its Deleter extension).
func TestMySQLFlexibleServerChildren(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "mysql")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	serverID := rgID + "/providers/Microsoft.DBforMySQL/flexibleServers/orders"
	connID := serverID + "/privateEndpointConnections/pe-orders"
	for _, id := range []string{rgID, serverID, connID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	server := &asomysql.FlexibleServer{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "orders"}}
	server.Spec.AzureName, server.Spec.Location = "orders", ptr("westus")
	server.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, server, serverID)
	onServer := &genruntime.KnownResourceReference{Name: "orders"}

	atp := &mysql.FlexibleServersAdvancedThreatProtectionSetting{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "orders-defender"},
		Spec: mysql.FlexibleServersAdvancedThreatProtectionSetting_Spec{
			AzureName: "Default", Owner: onServer, State: ptr(mysql.AdvancedThreatProtectionState_Enabled),
		},
	}
	misnamed := &mysql.FlexibleServersAdvancedThreatProtectionSetting{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "orders-other"},
		Spec: mysql.FlexibleServersAdvancedThreatProtectionSetting_Spec{
			AzureName: "Other", Owner: onServer, State: ptr(mysql.AdvancedThreatProtectionState_Enabled),
		},
	}
	approve := func(name, azureName string) *mysql.FlexibleServersPrivateEndpointConnection {
		return &mysql.FlexibleServersPrivateEndpointConnection{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec: mysql.FlexibleServersPrivateEndpointConnection_Spec{
				AzureName: azureName, Owner: onServer,
				PrivateLinkServiceConnectionState: &mysql.PrivateLinkServiceConnectionState{
					Status: ptr(mysql.PrivateEndpointServiceConnectionStatus_Approved), Description: ptr("approved by the platform team"),
				},
			},
		}
	}
	conn, stray := approve("orders-pe", "pe-orders"), approve("orders-stray", "pe-stray")
	objs := []conditioned{atp, misnamed, conn, stray}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "the setting Ready", ready(atp))
	eventually(t, timeout, "the connection approved", ready(conn))
	eventually(t, timeout, "a setting of another name refused", notReadyWith(misnamed, "Default"))
	eventually(t, timeout, "a connection no private endpoint made refused", notReadyWith(stray, "a private endpoint makes it"))
	atpID := serverID + "/advancedThreatProtectionSettings/Default"
	expectResource(t, atpID, func(r map[string]any) error {
		if p := properties(r); p["state"] != "Enabled" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again: protection off.
	if err := change(t, atp, func(atp *mysql.FlexibleServersAdvancedThreatProtectionSetting) {
		atp.Spec.State = ptr(mysql.AdvancedThreatProtectionState_Disabled)
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, atpID, func(r map[string]any) error {
		if p := properties(r); p["state"] != "Disabled" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range objs {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	if armFake.HasResource(connID) {
		t.Errorf("%s is still in the fake", connID)
	}
	if !armFake.HasResource(atpID) {
		t.Errorf("the setting was deleted")
	}
}
