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

	management "github.com/highfivelabs/aso-extensions/apis/arm/management/v20230401"
	network "github.com/highfivelabs/aso-extensions/apis/arm/network/v20260301"
)

// Azure Virtual Network Manager (foundation row 11, first part): a network
// manager over a subscription and our management group, network manager
// connections at the subscription (by ARM ID) and at the management group
// (by object), both naming the manager by object; a scope connection to
// another tenant's subscription; a network group of virtual networks with a
// static member (a virtual network by ARM ID). The manager changed and PUT
// again; a subnet refused as a member of a group of virtual networks, on its
// Ready condition. Deleted, children first.
func TestNetworkManager(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "avnm")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	subscription := "/subscriptions/" + subID
	rgID := subscription + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	nw := rgID + "/providers/Microsoft.Network/"
	vnetID := nw + "virtualNetworks/spoke"
	armFake.AddResource(vnetID)
	armFake.AddResource(vnetID + "/subnets/app")
	owner := func(name string) *genruntime.KnownResourceReference {
		return &genruntime.KnownResourceReference{Name: name}
	}

	platform := &management.ManagementGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "platform"},
		Spec:       management.ManagementGroup_Spec{AzureName: ns + "-platform"},
	}
	if err := k8s.Create(ctx, platform); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "ManagementGroup Ready", ready(platform))
	mgID := "/providers/Microsoft.Management/managementGroups/" + ns + "-platform"

	nm := &network.NetworkManager{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "avnm"},
		Spec: network.NetworkManager_Spec{
			Owner: owner("rg"), Location: ptr("westus"), Description: ptr("hub and spoke"),
			NetworkManagerScopes: &network.NetworkManagerPropertiesNetworkManagerScopes{
				Subscriptions:    []string{subscription},
				ManagementGroups: []string{mgID},
			},
			NetworkManagerScopeAccesses: []network.ConfigurationType{network.ConfigurationType_Connectivity, network.ConfigurationType_SecurityAdmin},
		},
	}
	if err := k8s.Create(ctx, nm); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "NetworkManager Ready", ready(nm))
	nmID := nw + "networkManagers/avnm"
	expectResource(t, nmID, func(r map[string]any) error {
		scopes, _ := properties(r)["networkManagerScopes"].(map[string]any)
		groups, _ := scopes["managementGroups"].([]any)
		if r["type"] != "Microsoft.Network/networkManagers" || len(groups) != 1 || !strings.EqualFold(fmt.Sprint(groups[0]), mgID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	subConn := &network.NetworkManagerConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "at-subscription"},
		Spec: network.NetworkManagerConnection_Spec{
			AzureName:               ns + "-avnm",
			Owner:                   &genruntime.ArbitraryOwnerReference{ARMID: subscription},
			NetworkManagerReference: ours("NetworkManager", "avnm"),
		},
	}
	mgConn := &network.NetworkManagerConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "at-group"},
		Spec: network.NetworkManagerConnection_Spec{
			AzureName:               "avnm",
			Owner:                   &genruntime.ArbitraryOwnerReference{Group: "management.azure.hfl.dev", Kind: "ManagementGroup", Name: "platform"},
			NetworkManagerReference: ours("NetworkManager", "avnm"),
			Description:             ptr("the platform group joins"),
		},
	}
	scopeConn := &network.NetworkManagersScopeConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "partner"},
		Spec: network.NetworkManagersScopeConnection_Spec{
			Owner:             owner("avnm"),
			TenantId:          ptr("00000000-0000-0000-0000-0000000000e9"),
			ResourceReference: &genruntime.ResourceReference{ARMID: "/subscriptions/00000000-0000-0000-0000-0000000000f9"},
		},
	}
	group := &network.NetworkManagersNetworkGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "spokes"},
		Spec: network.NetworkManagersNetworkGroup_Spec{
			Owner:      owner("avnm"),
			MemberType: ptr(network.GroupMemberType_VirtualNetwork),
		},
	}
	member := &network.NetworkManagersNetworkGroupsStaticMember{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "spoke"},
		Spec: network.NetworkManagersNetworkGroupsStaticMember_Spec{
			Owner:             owner("spokes"),
			ResourceReference: &genruntime.ResourceReference{ARMID: vnetID},
		},
	}
	children := []client.Object{subConn, mgConn, scopeConn, group, member}
	for _, o := range children {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range children {
		eventually(t, timeout, fmt.Sprintf("%T %s Ready", o, o.GetName()), ready(o.(conditioned)))
	}
	subConnID := subscription + "/providers/Microsoft.Network/networkManagerConnections/" + ns + "-avnm"
	mgConnID := mgID + "/providers/Microsoft.Network/networkManagerConnections/avnm"
	scopeConnID := nmID + "/scopeConnections/partner"
	groupID := nmID + "/networkGroups/spokes"
	memberID := groupID + "/staticMembers/spoke"
	for _, id := range []string{subConnID, mgConnID} {
		expectResource(t, id, func(r map[string]any) error {
			if !strings.EqualFold(fmt.Sprint(properties(r)["networkManagerId"]), nmID) {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})
	}
	expectResource(t, scopeConnID, func(r map[string]any) error {
		p := properties(r)
		if p["tenantId"] != "00000000-0000-0000-0000-0000000000e9" || p["resourceId"] != "/subscriptions/00000000-0000-0000-0000-0000000000f9" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, memberID, func(r map[string]any) error {
		if !strings.EqualFold(fmt.Sprint(properties(r)["resourceId"]), vnetID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	eventually(t, timeout, "the scope connection's state in its status", func() error {
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(scopeConn), scopeConn); err != nil {
			return err
		}
		if s := scopeConn.Status.ConnectionState; s == nil || *s != network.ScopeConnectionState_STATUS_Pending {
			return fmt.Errorf("connection state %v", s)
		}
		return nil
	})

	// The manager changed: PUT again.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(nm), nm); err != nil {
		t.Fatal(err)
	}
	nm.Spec.Tags = map[string]string{"team": "network"}
	if err := k8s.Update(ctx, nm); err != nil {
		t.Fatal(err)
	}
	expectResource(t, nmID, func(r map[string]any) error {
		tags, _ := r["tags"].(map[string]any)
		if tags["team"] != "network" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A subnet is no member of a group of virtual networks.
	wrong := &network.NetworkManagersNetworkGroupsStaticMember{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "subnet"},
		Spec: network.NetworkManagersNetworkGroupsStaticMember_Spec{
			Owner:             owner("spokes"),
			ResourceReference: &genruntime.ResourceReference{ARMID: vnetID + "/subnets/app"},
		},
	}
	if err := k8s.Create(ctx, wrong); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the subnet member on the Ready condition", notReadyWith(wrong, "members are virtual networks"))

	for _, o := range []client.Object{wrong, member, group, scopeConn, mgConn, subConn, nm, platform} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T %s gone", o, o.GetName()), gone(o))
	}
	for _, id := range []string{memberID, groupID, scopeConnID, mgConnID, subConnID, nmID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
}
