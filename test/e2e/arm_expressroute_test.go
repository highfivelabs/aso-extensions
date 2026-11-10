// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	network "github.com/highfivelabs/aso-extensions/apis/arm/network/v20260301"
)

func circuitSku() *network.ExpressRouteCircuitSku {
	return &network.ExpressRouteCircuitSku{
		Name:   ptr("Standard_MeteredData"),
		Tier:   ptr(network.ExpressRouteCircuitSkuTier_Standard),
		Family: ptr(network.ExpressRouteCircuitSkuFamily_MeteredData),
	}
}

func privatePeering(ns, name, circuit, prefix string, vlan int) *network.ExpressRouteCircuitsPeering {
	return &network.ExpressRouteCircuitsPeering{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: network.ExpressRouteCircuitsPeering_Spec{
			Owner:                      &genruntime.KnownResourceReference{Name: circuit},
			AzureName:                  "AzurePrivatePeering",
			PeeringType:                ptr(network.ExpressRoutePeeringType_AzurePrivatePeering),
			VlanId:                     ptr(vlan),
			PeerASN:                    ptr(65010),
			PrimaryPeerAddressPrefix:   ptr(prefix + ".0/30"),
			SecondaryPeerAddressPrefix: ptr(prefix + ".4/30"),
		},
	}
}

// secretValue is a key of a Secret, or "" while the Secret is not there.
func secretValue(ns, name, key string) string {
	s := &corev1.Secret{}
	if err := k8s.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: name}, s); err != nil {
		return ""
	}
	return string(s.Data[key])
}

// An ExpressRoute circuit through a connectivity provider, owned by ASO's
// ResourceGroup, with its service key written to a Secret; its private
// peering (the BGP key from a Secret) and an authorization whose key, made by
// Azure, is written to a Secret; the circuit changed and PUT again, keeping
// the peering and the authorization (its extension, as ASO's RouteTable keeps
// its routes); a second circuit's peering joined to the first by Global Reach
// with that authorization key, and the second peering changed, keeping the
// connection; ExpressRoute Direct: a port, its authorization, and a circuit
// on the port redeeming the port authorization's key. Deleted, children
// first.
func TestExpressRouteCircuit(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "er")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	secret(t, ns, "bgp", map[string]string{"md5": "bgp-md5-key-1"})
	nw := rgID + "/providers/Microsoft.Network/"

	provider := &network.ExpressRouteCircuitServiceProviderProperties{
		ServiceProviderName: ptr("Equinix"), PeeringLocation: ptr("Silicon Valley"), BandwidthInMbps: ptr(50),
	}
	er := &network.ExpressRouteCircuit{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "er"},
		Spec: network.ExpressRouteCircuit_Spec{
			Owner:                     &genruntime.KnownResourceReference{Name: "rg"},
			Location:                  ptr("westus"),
			Sku:                       circuitSku(),
			ServiceProviderProperties: provider,
			AllowClassicOperations:    ptr(false),
			OperatorSpec: &network.ExpressRouteCircuitOperatorSpec{Secrets: &network.ExpressRouteCircuitOperatorSecrets{
				ServiceKey: &genruntime.SecretDestination{Name: "er-keys", Key: "serviceKey"},
			}},
		},
	}
	if err := k8s.Create(ctx, er); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "ExpressRouteCircuit Ready", ready(er))
	erID := nw + "expressRouteCircuits/er"
	expectResource(t, erID, func(r map[string]any) error {
		sku, _ := r["sku"].(map[string]any)
		sp, _ := properties(r)["serviceProviderProperties"].(map[string]any)
		if r["type"] != "Microsoft.Network/expressRouteCircuits" || sku["tier"] != "Standard" || sp["serviceProviderName"] != "Equinix" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	serviceKey := armFake.AuthorizationKey(erID)
	eventually(t, timeout, "the service key in its Secret", func() error {
		if got := secretValue(ns, "er-keys", "serviceKey"); serviceKey == "" || got != serviceKey {
			return fmt.Errorf("Secret er-keys serviceKey %q, Azure made %q", got, serviceKey)
		}
		return nil
	})

	peering := privatePeering(ns, "private", "er", "192.168.10", 100)
	peering.Spec.SharedKey = &genruntime.SecretReference{Name: "bgp", Key: "md5"}
	auth := &network.ExpressRouteCircuitsAuthorization{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "to-er2"},
		Spec: network.ExpressRouteCircuitsAuthorization_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "er"},
			OperatorSpec: &network.ExpressRouteCircuitsAuthorizationOperatorSpec{Secrets: &network.ExpressRouteCircuitsAuthorizationOperatorSecrets{
				AuthorizationKey: &genruntime.SecretDestination{Name: "er-auth", Key: "key"},
			}},
		},
	}
	for _, o := range []client.Object{peering, auth} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "ExpressRouteCircuitsPeering Ready", ready(peering))
	eventually(t, timeout, "ExpressRouteCircuitsAuthorization Ready", ready(auth))
	peeringID := erID + "/peerings/AzurePrivatePeering"
	authID := erID + "/authorizations/to-er2"
	expectResource(t, peeringID, func(r map[string]any) error {
		p := properties(r)
		if p["peeringType"] != "AzurePrivatePeering" || p["vlanId"] != float64(100) || p["sharedKey"] != "bgp-md5-key-1" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	authKey := armFake.AuthorizationKey(authID)
	eventually(t, timeout, "the authorization key in its Secret", func() error {
		if got := secretValue(ns, "er-auth", "key"); authKey == "" || got != authKey {
			return fmt.Errorf("Secret er-auth key %q, Azure made %q", got, authKey)
		}
		return nil
	})
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(auth), auth); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprintf("%+v", auth.Status), authKey) {
		t.Fatalf("the authorization key is in the status: %+v", auth.Status)
	}

	// The circuit changed: PUT again with its peering and authorization.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(er), er); err != nil {
		t.Fatal(err)
	}
	er.Spec.Tags = map[string]string{"team": "network"}
	if err := k8s.Update(ctx, er); err != nil {
		t.Fatal(err)
	}
	expectResource(t, erID, func(r map[string]any) error {
		tags, _ := r["tags"].(map[string]any)
		if tags["team"] != "network" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if !armFake.HasResource(peeringID) || !armFake.HasResource(authID) {
		t.Fatal("the circuit's PUT removed its peering or its authorization")
	}
	if armFake.AuthorizationKey(authID) != authKey || armFake.AuthorizationKey(erID) != serviceKey {
		t.Fatal("a key Azure made changed on the circuit's PUT")
	}

	// Global Reach: a second circuit's peering to the first's, with the
	// authorization key from the Secret.
	er2 := &network.ExpressRouteCircuit{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "er2"},
		Spec: network.ExpressRouteCircuit_Spec{
			Owner:                     &genruntime.KnownResourceReference{Name: "rg"},
			Location:                  ptr("eastus"),
			Sku:                       circuitSku(),
			ServiceProviderProperties: provider,
		},
	}
	peering2 := privatePeering(ns, "private2", "er2", "192.168.20", 200)
	for _, o := range []client.Object{er2, peering2} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "second circuit's peering Ready", ready(peering2))
	reach := &network.ExpressRouteCircuitsPeeringsConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "reach"},
		Spec: network.ExpressRouteCircuitsPeeringsConnection_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "private2"},
			ExpressRouteCircuitPeering: &network.SubResource{Reference: &genruntime.ResourceReference{
				Group: "network.azure.hfl.dev", Kind: "ExpressRouteCircuitsPeering", Name: "private2"}},
			PeerExpressRouteCircuitPeering: &network.SubResource{Reference: &genruntime.ResourceReference{
				Group: "network.azure.hfl.dev", Kind: "ExpressRouteCircuitsPeering", Name: "private"}},
			AddressPrefix:    ptr("10.99.0.0/29"),
			AuthorizationKey: &genruntime.SecretReference{Name: "er-auth", Key: "key"},
		},
	}
	if err := k8s.Create(ctx, reach); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "Global Reach ExpressRouteCircuitsPeeringsConnection Ready", ready(reach))
	peering2ID := nw + "expressRouteCircuits/er2/peerings/AzurePrivatePeering"
	reachID := peering2ID + "/connections/reach"
	expectResource(t, reachID, func(r map[string]any) error {
		p := properties(r)
		own, _ := p["expressRouteCircuitPeering"].(map[string]any)
		peer, _ := p["peerExpressRouteCircuitPeering"].(map[string]any)
		if own["id"] != peering2ID || peer["id"] != peeringID || p["authorizationKey"] != authKey {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, authID, func(r map[string]any) error {
		if properties(r)["authorizationUseStatus"] != "InUse" {
			return fmt.Errorf("the redeemed authorization is not in use: %v", r)
		}
		return nil
	})
	// The second peering changed: PUT again with its Global Reach connection.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(peering2), peering2); err != nil {
		t.Fatal(err)
	}
	peering2.Spec.PeerASN = ptr(65020)
	if err := k8s.Update(ctx, peering2); err != nil {
		t.Fatal(err)
	}
	expectResource(t, peering2ID, func(r map[string]any) error {
		if properties(r)["peerASN"] != float64(65020) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if !armFake.HasResource(reachID) {
		t.Fatal("the peering's PUT removed its Global Reach connection")
	}

	// ExpressRoute Direct.
	port := &network.ExpressRoutePort{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "direct"},
		Spec: network.ExpressRoutePort_Spec{
			Owner:           &genruntime.KnownResourceReference{Name: "rg"},
			Location:        ptr("westus"),
			PeeringLocation: ptr("Equinix-Silicon-Valley"),
			BandwidthInGbps: ptr(10),
			Encapsulation:   ptr(network.ExpressRoutePortsEncapsulation_Dot1Q),
		},
	}
	portAuth := &network.ExpressRoutePortsAuthorization{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "port-auth"},
		Spec: network.ExpressRoutePortsAuthorization_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "direct"},
			OperatorSpec: &network.ExpressRoutePortsAuthorizationOperatorSpec{Secrets: &network.ExpressRoutePortsAuthorizationOperatorSecrets{
				AuthorizationKey: &genruntime.SecretDestination{Name: "port-auth", Key: "key"},
			}},
		},
	}
	for _, o := range []client.Object{port, portAuth} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "ExpressRoutePortsAuthorization Ready", ready(portAuth))
	portID := nw + "expressRoutePorts/direct"
	portAuthID := portID + "/authorizations/port-auth"
	portKey := armFake.AuthorizationKey(portAuthID)
	eventually(t, timeout, "the port authorization key in its Secret", func() error {
		if got := secretValue(ns, "port-auth", "key"); portKey == "" || got != portKey {
			return fmt.Errorf("Secret port-auth key %q, Azure made %q", got, portKey)
		}
		return nil
	})
	onPort := &network.ExpressRouteCircuit{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "on-port"},
		Spec: network.ExpressRouteCircuit_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
			Sku:      circuitSku(),
			ExpressRoutePort: &network.SubResource{Reference: &genruntime.ResourceReference{
				Group: "network.azure.hfl.dev", Kind: "ExpressRoutePort", Name: "direct"}},
			BandwidthInGbps:  ptr(5.0),
			AuthorizationKey: &genruntime.SecretReference{Name: "port-auth", Key: "key"},
		},
	}
	if err := k8s.Create(ctx, onPort); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "circuit on the port Ready", ready(onPort))
	onPortID := nw + "expressRouteCircuits/on-port"
	expectResource(t, portAuthID, func(r map[string]any) error {
		p := properties(r)
		if p["authorizationUseStatus"] != "InUse" || !strings.EqualFold(fmt.Sprint(p["circuitResourceUri"]), onPortID) {
			return fmt.Errorf("the port authorization is not in use by the circuit: %v", r)
		}
		return nil
	})

	for _, o := range []client.Object{reach, onPort, portAuth, port, peering2, er2, auth, peering, er} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T %s gone", o, o.GetName()), gone(o))
	}
	for _, id := range []string{reachID, onPortID, portAuthID, portID, peering2ID, authID, peeringID, erID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
}

// Refused by Resource Manager, and the refusal reaches the Ready condition: a
// circuit with neither a provider nor a port, a peering named for another
// type, and a Global Reach connection with a key that is no authorization's.
// Refused by the CRD: a peering type that does not exist.
func TestExpressRouteRefusals(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "er-refused")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	secret(t, ns, "wrong", map[string]string{"key": "not-an-authorization-key"})

	nowhere := &network.ExpressRouteCircuit{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "nowhere"},
		Spec: network.ExpressRouteCircuit_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
			Sku:      circuitSku(),
		},
	}
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
	for _, o := range []client.Object{nowhere, er} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "the missing provider on the Ready condition", notReadyWith(nowhere, "serviceProviderProperties or an expressRoutePort"))
	eventually(t, timeout, "ExpressRouteCircuit Ready", ready(er))

	misnamed := privatePeering(ns, "misnamed", "er", "192.168.30", 300)
	misnamed.Spec.AzureName = "MicrosoftPeering"
	private := privatePeering(ns, "private", "er", "192.168.40", 400)
	for _, o := range []client.Object{misnamed, private} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "the misnamed peering on the Ready condition", notReadyWith(misnamed, "is named AzurePrivatePeering"))
	eventually(t, timeout, "ExpressRouteCircuitsPeering Ready", ready(private))

	wrongKey := &network.ExpressRouteCircuitsPeeringsConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "wrong-key"},
		Spec: network.ExpressRouteCircuitsPeeringsConnection_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "private"},
			ExpressRouteCircuitPeering: &network.SubResource{Reference: &genruntime.ResourceReference{
				Group: "network.azure.hfl.dev", Kind: "ExpressRouteCircuitsPeering", Name: "private"}},
			PeerExpressRouteCircuitPeering: &network.SubResource{Reference: &genruntime.ResourceReference{
				ARMID: rgID + "/providers/Microsoft.Network/expressRouteCircuits/er/peerings/AzurePrivatePeering"}},
			AddressPrefix:    ptr("10.98.0.0/29"),
			AuthorizationKey: &genruntime.SecretReference{Name: "wrong", Key: "key"},
		},
	}
	if err := k8s.Create(ctx, wrongKey); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the wrong authorization key on the Ready condition", notReadyWith(wrongKey, "authorization keys"))

	badType := privatePeering(ns, "bad", "er", "192.168.50", 500)
	badType.Spec.PeeringType = ptr(network.ExpressRoutePeeringType("CarrierPigeonPeering"))
	if err := k8s.Create(ctx, badType); err == nil || !contains(err.Error(), "Unsupported value") {
		t.Fatalf("want a refusal of the peering type, got %v", err)
	}
}
