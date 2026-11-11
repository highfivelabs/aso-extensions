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

// Virtual Network Manager routing and commits (foundation row 11, third
// part): in a network manager with Connectivity and Routing access and a
// network group (by object), a routing configuration with a rule collection
// for the group and a rule sending 0.0.0.0/0 to a virtual appliance; a mesh
// connectivity configuration; a Routing commit of the routing configuration
// and a Connectivity commit of the connectivity configuration (both by
// object) to two regions, their active locations in the status. A
// SecurityAdmin commit refused (the manager has no such access) and a
// Routing commit naming the connectivity configuration refused, on their
// Ready conditions. Deleted, children first.
func TestNetworkManagerRouting(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "avnmrt")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	subscription := "/subscriptions/" + subID
	rgID := subscription + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	nw := rgID + "/providers/Microsoft.Network/"
	owner := func(name string) *genruntime.KnownResourceReference {
		return &genruntime.KnownResourceReference{Name: name}
	}

	nm := &network.NetworkManager{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "avnm"},
		Spec: network.NetworkManager_Spec{
			Owner: owner("rg"), Location: ptr("westus"),
			NetworkManagerScopes:        &network.NetworkManagerPropertiesNetworkManagerScopes{Subscriptions: []string{subscription}},
			NetworkManagerScopeAccesses: []network.ConfigurationType{network.ConfigurationType_Connectivity, network.ConfigurationType_Routing},
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

	routing := &network.NetworkManagersRoutingConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "egress"},
		Spec: network.NetworkManagersRoutingConfiguration_Spec{
			Owner: owner("avnm"), Description: ptr("default route through the firewall"),
			RouteTableUsageMode: ptr(network.NetworkManagerRoutingConfigurationPropertiesFormat_RouteTableUsageMode_ManagedOnly),
		},
	}
	coll := &network.NetworkManagersRoutingRuleCollection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "spokes"},
		Spec: network.NetworkManagersRoutingRuleCollection_Spec{
			Owner:                      owner("egress"),
			AppliesTo:                  []network.NetworkManagerRoutingGroupItem{{NetworkGroupReference: spokes}},
			DisableBgpRoutePropagation: ptr(network.DisableBgpRoutePropagation_True),
		},
	}
	rule := &network.NetworkManagersRoutingRule{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "default"},
		Spec: network.NetworkManagersRoutingRule_Spec{
			Owner:       owner("spokes"),
			Destination: &network.RoutingRuleRouteDestination{Type: ptr(network.RoutingRuleDestinationType_AddressPrefix), DestinationAddress: ptr("0.0.0.0/0")},
			NextHop:     &network.RoutingRuleNextHop{NextHopType: ptr(network.RoutingRuleNextHopType_VirtualAppliance), NextHopAddress: ptr("10.0.1.4")},
		},
	}
	mesh := &network.NetworkManagersConnectivityConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "mesh"},
		Spec: network.NetworkManagersConnectivityConfiguration_Spec{
			Owner:                owner("avnm"),
			ConnectivityTopology: ptr(network.ConnectivityTopology_Mesh),
			AppliesToGroups:      []network.ConnectivityGroupItem{{NetworkGroupReference: spokes, GroupConnectivity: ptr(network.GroupConnectivity_DirectlyConnected)}},
		},
	}
	children := []client.Object{routing, coll, rule, mesh}
	for _, o := range children {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range children {
		eventually(t, timeout, fmt.Sprintf("%T %s Ready", o, o.GetName()), ready(o.(conditioned)))
	}
	routingID := nmID + "/routingConfigurations/egress"
	collID := routingID + "/ruleCollections/spokes"
	ruleID := collID + "/rules/default"
	meshID := nmID + "/connectivityConfigurations/mesh"
	expectResource(t, collID, func(r map[string]any) error {
		items, _ := properties(r)["appliesTo"].([]any)
		if len(items) != 1 || !strings.EqualFold(fmt.Sprint(items[0].(map[string]any)["networkGroupId"]), groupID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, ruleID, func(r map[string]any) error {
		hop, _ := properties(r)["nextHop"].(map[string]any)
		if hop["nextHopType"] != "VirtualAppliance" || hop["nextHopAddress"] != "10.0.1.4" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	commit := func(name string, kind network.ConfigurationType, config string, configKind string) *network.NetworkManagersCommit {
		return &network.NetworkManagersCommit{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec: network.NetworkManagersCommit_Spec{
				Owner: owner("avnm"),
				Properties: &network.CommitProperties{
					CommitType:              ptr(kind),
					TargetLocations:         []string{"westus", "eastus"},
					ConfigurationReferences: []genruntime.ResourceReference{*ours(configKind, config)},
				},
			},
		}
	}
	routeCommit := commit("routing", network.ConfigurationType_Routing, "egress", "NetworkManagersRoutingConfiguration")
	meshCommit := commit("connectivity", network.ConfigurationType_Connectivity, "mesh", "NetworkManagersConnectivityConfiguration")
	for _, c := range []*network.NetworkManagersCommit{routeCommit, meshCommit} {
		if err := k8s.Create(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []*network.NetworkManagersCommit{routeCommit, meshCommit} {
		eventually(t, timeout, "commit "+c.Name+" Ready", ready(c))
	}
	for id, config := range map[string]string{nmID + "/commits/routing": routingID, nmID + "/commits/connectivity": meshID} {
		expectResource(t, id, func(r map[string]any) error {
			ids, _ := properties(r)["configurationIds"].([]any)
			if len(ids) != 1 || !strings.EqualFold(fmt.Sprint(ids[0]), config) {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})
	}
	eventually(t, timeout, "the routing commit's active locations in its status", func() error {
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(routeCommit), routeCommit); err != nil {
			return err
		}
		if p := routeCommit.Status.Properties; p == nil || len(p.ActiveLocations) != 2 {
			return fmt.Errorf("status properties %+v", p)
		}
		return nil
	})

	// The manager has no SecurityAdmin access; a Routing commit cannot name
	// a connectivity configuration.
	noAccess := commit("no-access", network.ConfigurationType_SecurityAdmin, "mesh", "NetworkManagersConnectivityConfiguration")
	wrongType := commit("wrong-type", network.ConfigurationType_Routing, "mesh", "NetworkManagersConnectivityConfiguration")
	for _, c := range []*network.NetworkManagersCommit{noAccess, wrongType} {
		if err := k8s.Create(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "the SecurityAdmin commit on the Ready condition", notReadyWith(noAccess, "has no SecurityAdmin"))
	eventually(t, timeout, "the mistyped commit on the Ready condition", notReadyWith(wrongType, "is no Routing configuration"))

	for _, o := range []client.Object{wrongType, noAccess, meshCommit, routeCommit, rule, coll, routing, mesh, group, nm} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T %s gone", o, o.GetName()), gone(o))
	}
	for _, id := range []string{nmID + "/commits/routing", nmID + "/commits/connectivity", ruleID, collID, routingID, meshID, groupID, nmID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
}
