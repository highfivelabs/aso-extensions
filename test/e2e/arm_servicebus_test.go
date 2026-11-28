// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	asoservicebus "github.com/Azure/azure-service-operator/v2/api/servicebus/v20240101/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	servicebus "github.com/highfivelabs/aso-extensions/apis/arm/servicebus/v20260101"
)

// A Service Bus namespace's children of docs/plan.md f61, owned by ASO's
// Namespace by name (a new external owner): the network rule set (an IP
// range and a subnet by ARM ID), a geo-disaster recovery alias naming the
// partner, ASO's other Namespace, by its object, and the approval of a
// connection a private endpoint made. A rule set with an IPv6 mask, an
// alias naming a missing partner and a connection no private endpoint made
// are refused (the fake's rules, messaging.go and privateendpoints.go); a
// change to the rule set is PUT again; deleted, all go but the rule set,
// which Azure cannot delete and the kind leaves (its Deleter extension).
func TestServiceBusNamespaceChildren(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "servicebus")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	ordersID := rgID + "/providers/Microsoft.ServiceBus/namespaces/orders"
	westID := rgID + "/providers/Microsoft.ServiceBus/namespaces/orders-west"
	subnetID := rgID + "/providers/Microsoft.Network/virtualNetworks/vnet/subnets/apps"
	connID := ordersID + "/privateEndpointConnections/pe-orders"
	for _, id := range []string{rgID, ordersID, westID, subnetID, connID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	for name, id := range map[string]string{"orders": ordersID, "orders-west": westID} {
		owner := &asoservicebus.Namespace{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
		owner.Spec.AzureName, owner.Spec.Location = name, ptr("westus")
		owner.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
		asoStandIn(t, owner, id)
	}

	rules := &servicebus.NamespacesNetworkRuleSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "orders-network"},
		Spec: servicebus.NamespacesNetworkRuleSet_Spec{
			Owner:         &genruntime.KnownResourceReference{Name: "orders"},
			DefaultAction: ptr(servicebus.DefaultAction_Deny),
			IpRules:       []servicebus.NWRuleSetIpRules{{IpMask: ptr("203.0.113.0/24"), Action: ptr(servicebus.NWRuleSetIpRules_Action_Allow)}},
			VirtualNetworkRules: []servicebus.NWRuleSetVirtualNetworkRules{{
				Subnet: &servicebus.Subnet{Reference: &genruntime.ResourceReference{ARMID: subnetID}},
			}},
		},
	}
	ipv6 := &servicebus.NamespacesNetworkRuleSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "west-network"},
		Spec: servicebus.NamespacesNetworkRuleSet_Spec{
			Owner:         &genruntime.KnownResourceReference{Name: "orders-west"},
			DefaultAction: ptr(servicebus.DefaultAction_Deny),
			IpRules:       []servicebus.NWRuleSetIpRules{{IpMask: ptr("2001:db8::/32"), Action: ptr(servicebus.NWRuleSetIpRules_Action_Allow)}},
		},
	}
	alias := &servicebus.NamespacesDisasterRecoveryConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "orders-dr"},
		Spec: servicebus.NamespacesDisasterRecoveryConfig_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "orders"},
			PartnerNamespaceReference: &genruntime.ResourceReference{
				Group: "servicebus.azure.com", Kind: "Namespace", Name: "orders-west",
			},
		},
	}
	lonely := &servicebus.NamespacesDisasterRecoveryConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "lonely-dr"},
		Spec: servicebus.NamespacesDisasterRecoveryConfig_Spec{
			Owner:                     &genruntime.KnownResourceReference{Name: "orders-west"},
			PartnerNamespaceReference: &genruntime.ResourceReference{ARMID: rgID + "/providers/Microsoft.ServiceBus/namespaces/gone"},
		},
	}
	approve := func(name, azureName string) *servicebus.NamespacesPrivateEndpointConnection {
		return &servicebus.NamespacesPrivateEndpointConnection{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec: servicebus.NamespacesPrivateEndpointConnection_Spec{
				AzureName: azureName,
				Owner:     &genruntime.KnownResourceReference{Name: "orders"},
				PrivateLinkServiceConnectionState: &servicebus.ConnectionState{
					Status: ptr(servicebus.PrivateLinkConnectionStatus_Approved), Description: ptr("approved by the platform team"),
				},
			},
		}
	}
	conn, stray := approve("orders-pe", "pe-orders"), approve("orders-stray", "pe-stray")
	objs := []conditioned{rules, ipv6, alias, lonely, conn, stray}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "the rule set Ready", ready(rules))
	eventually(t, timeout, "an IPv6 mask refused", notReadyWith(ipv6, "IPv4"))
	eventually(t, timeout, "the alias Ready", ready(alias))
	eventually(t, timeout, "a missing partner refused", notReadyWith(lonely, "was not found"))
	eventually(t, timeout, "the connection approved", ready(conn))
	eventually(t, timeout, "a connection no private endpoint made refused", notReadyWith(stray, "a private endpoint makes it"))

	rulesID := ordersID + "/networkRuleSets/default"
	expectResource(t, rulesID, func(r map[string]any) error {
		p := properties(r)
		vnets, _ := p["virtualNetworkRules"].([]any)
		if p["defaultAction"] != "Deny" || len(vnets) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		if v, _ := vnets[0].(map[string]any); !strings.EqualFold(fmt.Sprint(v["subnet"].(map[string]any)["id"]), subnetID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, ordersID+"/disasterRecoveryConfigs/orders-dr", func(r map[string]any) error {
		if p := properties(r); p["partnerNamespace"] != westID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, connID, func(r map[string]any) error {
		state, _ := properties(r)["privateLinkServiceConnectionState"].(map[string]any)
		if state["status"] != "Approved" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again: a second address.
	if err := change(t, rules, func(rules *servicebus.NamespacesNetworkRuleSet) {
		rules.Spec.IpRules = append(rules.Spec.IpRules, servicebus.NWRuleSetIpRules{IpMask: ptr("198.51.100.7"), Action: ptr(servicebus.NWRuleSetIpRules_Action_Allow)})
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, rulesID, func(r map[string]any) error {
		var masks []string
		ipRules, _ := properties(r)["ipRules"].([]any)
		for _, rule := range ipRules {
			masks = append(masks, fmt.Sprint(rule.(map[string]any)["ipMask"]))
		}
		if !reflect.DeepEqual(masks, []string{"203.0.113.0/24", "198.51.100.7"}) {
			return fmt.Errorf("ipMasks: %v", masks)
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
	for _, id := range []string{ordersID + "/disasterRecoveryConfigs/orders-dr", connID} {
		if armFake.HasResource(id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
	// The rule set has no DELETE: it is left, and goes with its namespace.
	if !armFake.HasResource(rulesID) {
		t.Errorf("%s was deleted", rulesID)
	}
}
