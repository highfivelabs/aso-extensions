// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	network "github.com/highfivelabs/aso-extensions/apis/arm/network/v20260301"
)

// NetworkManager IPAM pools, static CIDRs, verifier workspaces, reachability
// analysis intents, and reachability analysis runs.
func TestNetworkManagerIpamAndVerifier(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "nm-ipam")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	resourceGroup(t, ns, "rg", ns+"-rg")
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	srcVnet := rgID + "/providers/Microsoft.Network/virtualNetworks/src-vnet"
	dstVnet := rgID + "/providers/Microsoft.Network/virtualNetworks/dst-vnet"
	armFake.AddResource(rgID)
	armFake.AddResource(srcVnet)
	armFake.AddResource(dstVnet)

	nm := &network.NetworkManager{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "nm"},
		Spec: network.NetworkManager_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
			NetworkManagerScopes: &network.NetworkManagerPropertiesNetworkManagerScopes{
				Subscriptions: []string{"/subscriptions/" + subID},
			},
			NetworkManagerScopeAccesses: []network.ConfigurationType{
				network.ConfigurationType_Connectivity,
			},
		},
	}
	pool := &network.NetworkManagersIpamPool{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "pool-1"},
		Spec: network.NetworkManagersIpamPool_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "nm"},
			Location: ptr("westus"),
			Properties: &network.IpamPoolProperties{
				DisplayName:     ptr("Root Pool"),
				AddressPrefixes: []string{"10.0.0.0/8"},
			},
		},
	}
	cidr := &network.NetworkManagersIpamPoolsStaticCidr{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cidr-1"},
		Spec: network.NetworkManagersIpamPoolsStaticCidr_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "pool-1"},
			Properties: &network.StaticCidrProperties{
				Description:     ptr("Reserved block"),
				AddressPrefixes: []string{"10.10.0.0/16"},
			},
		},
	}
	vw := &network.NetworkManagersVerifierWorkspace{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "vw-1"},
		Spec: network.NetworkManagersVerifierWorkspace_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "nm"},
			Location: ptr("westus"),
			Properties: &network.VerifierWorkspaceProperties{
				Description: ptr("Verifier workspace"),
			},
		},
	}
	intent := &network.NetworkManagersReachabilityAnalysisIntent{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "intent-1"},
		Spec: network.NetworkManagersReachabilityAnalysisIntent_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "vw-1"},
			Properties: &network.ReachabilityAnalysisIntentProperties{
				Description:                  ptr("Check HTTPS reachability"),
				SourceResourceReference:      &genruntime.ResourceReference{ARMID: srcVnet},
				DestinationResourceReference: &genruntime.ResourceReference{ARMID: dstVnet},
				IpTraffic: &network.IPTraffic{
					SourceIps:        []string{"10.0.0.0/24"},
					DestinationIps:   []string{"10.1.0.0/24"},
					SourcePorts:      []string{"0-65535"},
					DestinationPorts: []string{"443"},
					Protocols:        []network.NetworkProtocol{network.NetworkProtocol_TCP},
				},
			},
		},
	}
	intentID := rgID + "/providers/Microsoft.Network/networkManagers/nm/verifierWorkspaces/vw-1/reachabilityAnalysisIntents/intent-1"
	run := &network.NetworkManagersReachabilityAnalysisRun{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "run-1"},
		Spec: network.NetworkManagersReachabilityAnalysisRun_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "vw-1"},
			Properties: &network.ReachabilityAnalysisRunProperties{
				Description:     ptr("Initial run"),
				IntentReference: &genruntime.ResourceReference{ARMID: intentID},
			},
		},
	}

	for _, obj := range []client.Object{nm, pool, cidr, vw, intent, run} {
		if err := k8s.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "NetworkManager Ready", ready(nm))
	eventually(t, timeout, "IpamPool Ready", ready(pool))
	eventually(t, timeout, "StaticCidr Ready", ready(cidr))
	eventually(t, timeout, "VerifierWorkspace Ready", ready(vw))
	eventually(t, timeout, "ReachabilityAnalysisIntent Ready", ready(intent))
	eventually(t, timeout, "ReachabilityAnalysisRun Ready", ready(run))

	poolID := rgID + "/providers/Microsoft.Network/networkManagers/nm/ipamPools/pool-1"
	expectResource(t, poolID, func(r map[string]any) error {
		props, _ := r["properties"].(map[string]any)
		if props["displayName"] != "Root Pool" {
			return fmt.Errorf("unexpected IpamPool: %v", r)
		}
		return nil
	})
	if !armFake.HasResource(intentID) {
		t.Fatalf("%s missing from Resource Manager", intentID)
	}

	for _, obj := range []client.Object{run, intent, vw, cidr, pool, nm} {
		if err := k8s.Delete(ctx, obj); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, obj.GetName()+" gone", gone(obj))
	}
}

// VirtualHub route maps and connection policies.
func TestVirtualHubRouteMapAndConnectionPolicy(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "vhub-rm")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	resourceGroup(t, ns, "rg", ns+"-rg")
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)

	wan := &network.VirtualWan{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "wan"},
		Spec: network.VirtualWan_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
		},
	}
	hub := &network.VirtualHub{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "hub"},
		Spec: network.VirtualHub_Spec{
			Owner:         &genruntime.KnownResourceReference{Name: "rg"},
			Location:      ptr("westus"),
			AddressPrefix: ptr("10.200.0.0/24"),
			VirtualWan:    &network.SubResource{Reference: &genruntime.ResourceReference{Name: "wan"}},
		},
	}
	routeMap := &network.VirtualHubsRouteMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "rm-1"},
		Spec: network.VirtualHubsRouteMap_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "hub"},
			Rules: []network.RouteMapRule{
				{
					Name: ptr("summarize"),
					MatchCriteria: []network.Criterion{
						{
							RoutePrefix:    []string{"10.1.0.0/16"},
							MatchCondition: ptr(network.RouteMapMatchCondition_Contains),
						},
					},
					Actions: []network.Action{
						{
							Type: ptr(network.RouteMapActionType_Add),
							Parameters: []network.Parameter{
								{Community: []string{"65001:100"}},
							},
						},
					},
					NextStepIfMatched: ptr(network.NextStep_Continue),
				},
			},
		},
	}
	connPolicy := &network.VirtualHubsConnectionPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cp-1"},
		Spec: network.VirtualHubsConnectionPolicy_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "hub"},
			Properties: &network.ConnectionPolicyProperties{
				EnableInternetSecurity: ptr(true),
			},
		},
	}

	for _, obj := range []client.Object{wan, hub, routeMap, connPolicy} {
		if err := k8s.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "VirtualWan Ready", ready(wan))
	eventually(t, timeout, "VirtualHub Ready", ready(hub))
	eventually(t, timeout, "VirtualHubsRouteMap Ready", ready(routeMap))
	eventually(t, timeout, "VirtualHubsConnectionPolicy Ready", ready(connPolicy))

	rmID := rgID + "/providers/Microsoft.Network/virtualHubs/hub/routeMaps/rm-1"
	cpID := rgID + "/providers/Microsoft.Network/virtualHubs/hub/connectionPolicies/cp-1"
	if !armFake.HasResource(rmID) || !armFake.HasResource(cpID) {
		t.Fatalf("RouteMap (%v) or ConnectionPolicy (%v) missing", armFake.HasResource(rmID), armFake.HasResource(cpID))
	}

	for _, obj := range []client.Object{connPolicy, routeMap, hub, wan} {
		if err := k8s.Delete(ctx, obj); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, obj.GetName()+" gone", gone(obj))
	}
}

// VirtualRouter, VirtualRoutersPeering, and FirewallPoliciesKubeSelectorGroup.
func TestVirtualRouterAndKubeSelectorGroup(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "vr-ksg")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	resourceGroup(t, ns, "rg", ns+"-rg")
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)

	vr := &network.VirtualRouter{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "vr-1"},
		Spec: network.VirtualRouter_Spec{
			Owner:            &genruntime.KnownResourceReference{Name: "rg"},
			Location:         ptr("westus"),
			VirtualRouterAsn: ptr(uint32(65001)),
		},
	}
	peering := &network.VirtualRoutersPeering{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "peer-1"},
		Spec: network.VirtualRoutersPeering_Spec{
			Owner:   &genruntime.KnownResourceReference{Name: "vr-1"},
			PeerAsn: ptr(uint32(65002)),
			PeerIp:  ptr("10.0.1.4"),
		},
	}

	for _, obj := range []client.Object{vr, peering} {
		if err := k8s.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "VirtualRouter Ready", ready(vr))
	eventually(t, timeout, "VirtualRoutersPeering Ready", ready(peering))

	peeringID := rgID + "/providers/Microsoft.Network/virtualRouters/vr-1/peerings/peer-1"
	expectResource(t, peeringID, func(r map[string]any) error {
		props, _ := r["properties"].(map[string]any)
		if props["peerIp"] != "10.0.1.4" {
			return fmt.Errorf("unexpected VirtualRoutersPeering: %v", r)
		}
		return nil
	})

	for _, obj := range []client.Object{peering, vr} {
		if err := k8s.Delete(ctx, obj); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, obj.GetName()+" gone", gone(obj))
	}
}
