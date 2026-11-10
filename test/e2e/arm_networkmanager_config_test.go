// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	network "github.com/highfivelabs/aso-extensions/apis/arm/network/v20260301"
)

// Virtual Network Manager configurations (foundation row 11, second part):
// in a network manager with a network group (by object), a hub-and-spoke
// connectivity configuration (the hub, a virtual network, by ARM ID; the
// group by object); a security admin configuration with a rule collection
// for the group and two rules, a Custom one and a Default one (the two sides
// of the polymorphic admin rule); a security user configuration with a rule
// collection and a rule. A connectivity configuration naming a missing hub
// refused on its Ready condition. Deleted, children first.
func TestNetworkManagerConfigurations(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "avnmcfg")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	subscription := "/subscriptions/" + subID
	rgID := subscription + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	nw := rgID + "/providers/Microsoft.Network/"
	hubID := nw + "virtualNetworks/hub"
	armFake.AddResource(hubID)
	owner := func(name string) *genruntime.KnownResourceReference {
		return &genruntime.KnownResourceReference{Name: name}
	}

	nm := &network.NetworkManager{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "avnm"},
		Spec: network.NetworkManager_Spec{
			Owner: owner("rg"), Location: ptr("westus"),
			NetworkManagerScopes:        &network.NetworkManagerPropertiesNetworkManagerScopes{Subscriptions: []string{subscription}},
			NetworkManagerScopeAccesses: []network.ConfigurationType{network.ConfigurationType_Connectivity, network.ConfigurationType_SecurityAdmin, network.ConfigurationType_SecurityUser},
		},
	}
	group := &network.NetworkManagersNetworkGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "spokes"},
		Spec:       network.NetworkManagersNetworkGroup_Spec{Owner: owner("avnm"), MemberType: ptr(network.GroupMemberType_VirtualNetwork)},
	}
	for _, o := range []client.Object{nm, group} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []client.Object{nm, group} {
		eventually(t, timeout, fmt.Sprintf("%T Ready", o), ready(o.(conditioned)))
	}
	nmID := nw + "networkManagers/avnm"
	groupID := nmID + "/networkGroups/spokes"
	spokes := ours("NetworkManagersNetworkGroup", "spokes")

	cc := &network.NetworkManagersConnectivityConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "hub-spoke"},
		Spec: network.NetworkManagersConnectivityConfiguration_Spec{
			Owner:                owner("avnm"),
			ConnectivityTopology: ptr(network.ConnectivityTopology_HubAndSpoke),
			Hubs:                 []network.Hub{{ResourceReference: &genruntime.ResourceReference{ARMID: hubID}, ResourceType: ptr("Microsoft.Network/virtualNetworks")}},
			AppliesToGroups: []network.ConnectivityGroupItem{{
				NetworkGroupReference: spokes,
				GroupConnectivity:     ptr(network.GroupConnectivity_None),
				UseHubGateway:         ptr(network.UseHubGateway_True),
			}},
			DeleteExistingPeering: ptr(network.DeleteExistingPeering_True),
		},
	}
	admin := &network.NetworkManagersSecurityAdminConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "baseline"},
		Spec:       network.NetworkManagersSecurityAdminConfiguration_Spec{Owner: owner("avnm"), Description: ptr("organization baseline")},
	}
	user := &network.NetworkManagersSecurityUserConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "team"},
		Spec:       network.NetworkManagersSecurityUserConfiguration_Spec{Owner: owner("avnm")},
	}
	adminColl := &network.NetworkManagersSecurityAdminRuleCollection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "deny-mgmt"},
		Spec: network.NetworkManagersSecurityAdminRuleCollection_Spec{
			Owner:           owner("baseline"),
			AppliesToGroups: []network.NetworkManagerSecurityGroupItem{{NetworkGroupReference: spokes}},
		},
	}
	custom := &network.NetworkManagersSecurityAdminRule{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "deny-rdp"},
		Spec: network.NetworkManagersSecurityAdminRule_Spec{
			Owner: owner("deny-mgmt"),
			AdminRule: &network.AdminRule{
				Kind:                  ptr(network.AdminRule_Kind_Custom),
				Protocol:              ptr(network.SecurityConfigurationRuleProtocol_Tcp),
				Direction:             ptr(network.SecurityConfigurationRuleDirection_Inbound),
				Access:                ptr(network.SecurityConfigurationRuleAccess_Deny),
				Priority:              ptr(10),
				Sources:               []network.AddressPrefixItem{{AddressPrefix: ptr("Internet"), AddressPrefixType: ptr(network.AddressPrefixType_ServiceTag)}},
				DestinationPortRanges: []string{"3389"},
			},
		},
	}
	def := &network.NetworkManagersSecurityAdminRule{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "default"},
		Spec: network.NetworkManagersSecurityAdminRule_Spec{
			Owner:            owner("deny-mgmt"),
			DefaultAdminRule: &network.DefaultAdminRule{Kind: ptr(network.DefaultAdminRule_Kind_Default), Flag: ptr("AllowVnetInbound")},
		},
	}
	userColl := &network.NetworkManagersSecurityUserRuleCollection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "app"},
		Spec: network.NetworkManagersSecurityUserRuleCollection_Spec{
			Owner:           owner("team"),
			AppliesToGroups: []network.SecurityUserGroupItem{{NetworkGroupReference: spokes}},
		},
	}
	userRule := &network.NetworkManagersSecurityUserRule{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "https"},
		Spec: network.NetworkManagersSecurityUserRule_Spec{
			Owner:                 owner("app"),
			Protocol:              ptr(network.SecurityConfigurationRuleProtocol_Tcp),
			Direction:             ptr(network.SecurityConfigurationRuleDirection_Inbound),
			DestinationPortRanges: []string{"443"},
		},
	}
	children := []client.Object{cc, admin, user, adminColl, custom, def, userColl, userRule}
	for _, o := range children {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range children {
		eventually(t, timeout, fmt.Sprintf("%T %s Ready", o, o.GetName()), ready(o.(conditioned)))
	}
	ccID := nmID + "/connectivityConfigurations/hub-spoke"
	adminID := nmID + "/securityAdminConfigurations/baseline"
	adminCollID := adminID + "/ruleCollections/deny-mgmt"
	customID := adminCollID + "/rules/deny-rdp"
	defID := adminCollID + "/rules/default"
	userID := nmID + "/securityUserConfigurations/team"
	userCollID := userID + "/ruleCollections/app"
	userRuleID := userCollID + "/rules/https"
	groupOf := func(p map[string]any) string {
		items, _ := p["appliesToGroups"].([]any)
		if len(items) != 1 {
			return ""
		}
		return fmt.Sprint(items[0].(map[string]any)["networkGroupId"])
	}
	expectResource(t, ccID, func(r map[string]any) error {
		p := properties(r)
		hubs, _ := p["hubs"].([]any)
		if p["connectivityTopology"] != "HubAndSpoke" || len(hubs) != 1 || !strings.EqualFold(fmt.Sprint(hubs[0].(map[string]any)["resourceId"]), hubID) || !strings.EqualFold(groupOf(p), groupID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	for _, id := range []string{adminCollID, userCollID} {
		expectResource(t, id, func(r map[string]any) error {
			if !strings.EqualFold(groupOf(properties(r)), groupID) {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})
	}
	expectResource(t, customID, func(r map[string]any) error {
		p := properties(r)
		if r["kind"] != "Custom" || p["access"] != "Deny" || p["priority"] != 10.0 || p["direction"] != "Inbound" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, defID, func(r map[string]any) error {
		if r["kind"] != "Default" || properties(r)["flag"] != "AllowVnetInbound" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, userRuleID, func(r map[string]any) error {
		p := properties(r)
		if p["protocol"] != "Tcp" || p["direction"] != "Inbound" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// The custom rule changed: PUT again, still a Custom rule.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(custom), custom); err != nil {
		t.Fatal(err)
	}
	custom.Spec.AdminRule.Priority = ptr(20)
	if err := k8s.Update(ctx, custom); err != nil {
		t.Fatal(err)
	}
	expectResource(t, customID, func(r map[string]any) error {
		if r["kind"] != "Custom" || properties(r)["priority"] != 20.0 {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A hub that is not there.
	missing := &network.NetworkManagersConnectivityConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "no-hub"},
		Spec: network.NetworkManagersConnectivityConfiguration_Spec{
			Owner:                owner("avnm"),
			ConnectivityTopology: ptr(network.ConnectivityTopology_HubAndSpoke),
			Hubs:                 []network.Hub{{ResourceReference: &genruntime.ResourceReference{ARMID: hubID + "-nope"}}},
			AppliesToGroups:      []network.ConnectivityGroupItem{{NetworkGroupReference: spokes, GroupConnectivity: ptr(network.GroupConnectivity_None)}},
		},
	}
	if err := k8s.Create(ctx, missing); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the configuration with a missing hub on the Ready condition", notReadyWith(missing, "was not found"))

	for _, o := range []client.Object{missing, userRule, userColl, def, custom, adminColl, user, admin, cc, group, nm} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T %s gone", o, o.GetName()), gone(o))
	}
	for _, id := range []string{userRuleID, userCollID, defID, customID, adminCollID, userID, adminID, ccID, groupID, nmID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
}
