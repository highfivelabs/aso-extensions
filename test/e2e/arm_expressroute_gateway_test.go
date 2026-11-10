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

// An ExpressRoute gateway in a Virtual WAN hub (by ARM ID: VirtualHub is not
// built yet), owned by ASO's ResourceGroup; its connection to our circuit's
// private peering, redeeming an authorization whose key Azure made and the
// manager wrote to a Secret; the gateway changed and PUT again, keeping the
// connection (its extension, as a circuit keeps its peerings); a second
// gateway in the same hub refused on its Ready condition. Deleted, children
// first.
func TestExpressRouteGateway(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "ergw")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	nw := rgID + "/providers/Microsoft.Network/"
	hubID := nw + "virtualHubs/hub"
	armFake.AddResource(hubID)

	er := &network.ExpressRouteCircuit{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "er"},
		Spec: network.ExpressRouteCircuit_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
			Sku:      circuitSku(),
			ServiceProviderProperties: &network.ExpressRouteCircuitServiceProviderProperties{
				ServiceProviderName: ptr("Equinix"), PeeringLocation: ptr("Silicon Valley"), BandwidthInMbps: ptr(50),
			},
		},
	}
	peering := privatePeering(ns, "private", "er", "192.168.60", 600)
	auth := &network.ExpressRouteCircuitsAuthorization{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "to-hub"},
		Spec: network.ExpressRouteCircuitsAuthorization_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "er"},
			OperatorSpec: &network.ExpressRouteCircuitsAuthorizationOperatorSpec{Secrets: &network.ExpressRouteCircuitsAuthorizationOperatorSecrets{
				AuthorizationKey: &genruntime.SecretDestination{Name: "hub-auth", Key: "key"},
			}},
		},
	}
	gw := &network.ExpressRouteGateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "gw"},
		Spec: network.ExpressRouteGateway_Spec{
			Owner:      &genruntime.KnownResourceReference{Name: "rg"},
			Location:   ptr("westus"),
			VirtualHub: &network.VirtualHubId{Reference: &genruntime.ResourceReference{ARMID: hubID}},
			AutoScaleConfiguration: &network.ExpressRouteGatewayPropertiesAutoScaleConfiguration{
				Bounds: &network.ExpressRouteGatewayPropertiesAutoScaleConfigurationBounds{Min: ptr(1), Max: ptr(2)},
			},
		},
	}
	for _, o := range []client.Object{er, peering, auth, gw} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "ExpressRouteCircuitsPeering Ready", ready(peering))
	eventually(t, timeout, "ExpressRouteCircuitsAuthorization Ready", ready(auth))
	eventually(t, timeout, "ExpressRouteGateway Ready", ready(gw))
	erID := nw + "expressRouteCircuits/er"
	peeringID := erID + "/peerings/AzurePrivatePeering"
	authID := erID + "/authorizations/to-hub"
	gwID := nw + "expressRouteGateways/gw"
	expectResource(t, gwID, func(r map[string]any) error {
		p := properties(r)
		hub, _ := p["virtualHub"].(map[string]any)
		scale, _ := p["autoScaleConfiguration"].(map[string]any)
		bounds, _ := scale["bounds"].(map[string]any)
		if r["type"] != "Microsoft.Network/expressRouteGateways" || hub["id"] != hubID || bounds["max"] != float64(2) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	authKey := armFake.AuthorizationKey(authID)
	eventually(t, timeout, "the authorization key in its Secret", func() error {
		if got := secretValue(ns, "hub-auth", "key"); authKey == "" || got != authKey {
			return fmt.Errorf("Secret hub-auth key %q, Azure made %q", got, authKey)
		}
		return nil
	})

	conn := &network.ExpressRouteGatewaysExpressRouteConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "to-er"},
		Spec: network.ExpressRouteGatewaysExpressRouteConnection_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "gw"},
			ExpressRouteCircuitPeering: &network.ExpressRouteCircuitPeeringId{Reference: &genruntime.ResourceReference{
				Group: "network.azure.hfl.dev", Kind: "ExpressRouteCircuitsPeering", Name: "private"}},
			AuthorizationKey:       &genruntime.SecretReference{Name: "hub-auth", Key: "key"},
			RoutingWeight:          ptr(10),
			EnableInternetSecurity: ptr(false),
		},
	}
	if err := k8s.Create(ctx, conn); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "ExpressRouteGatewaysExpressRouteConnection Ready", ready(conn))
	connID := gwID + "/expressRouteConnections/to-er"
	expectResource(t, connID, func(r map[string]any) error {
		p := properties(r)
		peer, _ := p["expressRouteCircuitPeering"].(map[string]any)
		if peer["id"] != peeringID || p["routingWeight"] != float64(10) || p["authorizationKey"] != authKey {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, authID, func(r map[string]any) error {
		p := properties(r)
		if p["authorizationUseStatus"] != "InUse" || !strings.EqualFold(fmt.Sprint(p["connectionResourceUri"]), connID) {
			return fmt.Errorf("the authorization is not in use by the gateway connection: %v", r)
		}
		return nil
	})
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(conn), conn); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprintf("%+v", conn.Status), authKey) {
		t.Fatalf("the authorization key is in the connection's status: %+v", conn.Status)
	}

	// The gateway changed: PUT again with its connection.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(gw), gw); err != nil {
		t.Fatal(err)
	}
	gw.Spec.Tags = map[string]string{"team": "network"}
	if err := k8s.Update(ctx, gw); err != nil {
		t.Fatal(err)
	}
	expectResource(t, gwID, func(r map[string]any) error {
		tags, _ := r["tags"].(map[string]any)
		if tags["team"] != "network" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if !armFake.HasResource(connID) {
		t.Fatal("the gateway's PUT removed its connection")
	}

	// A hub has one ExpressRoute gateway.
	second := &network.ExpressRouteGateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "gw2"},
		Spec: network.ExpressRouteGateway_Spec{
			Owner:      &genruntime.KnownResourceReference{Name: "rg"},
			Location:   ptr("westus"),
			VirtualHub: &network.VirtualHubId{Reference: &genruntime.ResourceReference{ARMID: hubID}},
		},
	}
	if err := k8s.Create(ctx, second); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the second gateway on the Ready condition", notReadyWith(second, "already has the ExpressRoute gateway"))

	for _, o := range []client.Object{second, conn, gw, auth, peering, er} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T %s gone", o, o.GetName()), gone(o))
	}
	for _, id := range []string{connID, gwID, authID, peeringID, erID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
}
