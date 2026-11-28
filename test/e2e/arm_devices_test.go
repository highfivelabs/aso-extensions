// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	asodevices "github.com/Azure/azure-service-operator/v2/api/devices/v20210702/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	iothub "github.com/highfivelabs/aso-extensions/apis/arm/devices/v20230630"
	dps "github.com/highfivelabs/aso-extensions/apis/arm/devices/v20261101"
)

// The IoT kinds of docs/plan.md f75: on ASO's IotHub by name (a new external
// owner) a CA certificate and the approval of a private endpoint's
// connection; and a Device Provisioning Service in a resource group that
// links the hub by its connection string (from a Secret, kept out of the
// status) with a shared access policy, its own certificate and connection.
// A hub certificate with no certificate and a provisioning service linking a
// hub by key with no connection string are refused (the fake's rules, devices.go); a change of the
// allocation policy is PUT again; deleted, all go.
func TestIoTHubChildrenAndProvisioningService(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "iot")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	hubID := rgID + "/providers/Microsoft.Devices/IotHubs/plant"
	dpsID := rgID + "/providers/Microsoft.Devices/provisioningServices/enroll"
	for _, id := range []string{rgID, hubID, hubID + "/privateEndpointConnections/pe-plant"} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	hub := &asodevices.IotHub{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "plant"}}
	hub.Spec.AzureName, hub.Spec.Location = "plant", ptr("westus")
	hub.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, hub, hubID)
	connection := "HostName=plant.azure-devices.net;SharedAccessKeyName=iothubowner;SharedAccessKey=c2VjcmV0"
	if err := k8s.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "plant-hub"},
		StringData: map[string]string{"connectionString": connection},
	}); err != nil {
		t.Fatal(err)
	}
	onHub := &genruntime.KnownResourceReference{Name: "plant"}
	onRG := &genruntime.KnownResourceReference{Name: "rg"}

	hubCert := &iothub.IotHubsCertificate{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "plant-root"},
		Spec:       iothub.IotHubsCertificate_Spec{AzureName: "root", Owner: onHub, Properties: &iothub.CertificateProperties{Certificate: ptr("MIIBplantroot")}},
	}
	emptyCert := &iothub.IotHubsCertificate{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "plant-empty"},
		Spec:       iothub.IotHubsCertificate_Spec{AzureName: "empty", Owner: onHub, Properties: &iothub.CertificateProperties{IsVerified: ptr(false)}},
	}
	hubConn := &iothub.IotHubsPrivateEndpointConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "plant-pe"},
		Spec: iothub.IotHubsPrivateEndpointConnection_Spec{AzureName: "pe-plant", Owner: onHub, Properties: &iothub.PrivateEndpointConnectionProperties{
			PrivateLinkServiceConnectionState: &iothub.PrivateLinkServiceConnectionState{Status: ptr(iothub.PrivateLinkServiceConnectionState_Status_Approved), Description: ptr("approved")}}},
	}
	service := func(name string, sku dps.IotDpsSku) *dps.ProvisioningService {
		return &dps.ProvisioningService{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec: dps.ProvisioningService_Spec{Owner: onRG, Location: ptr("westus"), Sku: &dps.IotDpsSkuInfo{Name: ptr(sku), Capacity: ptr(1)},
				Properties: &dps.IotDpsPropertiesDescription{
					AllocationPolicy: ptr(dps.AllocationPolicy_Hashed),
					IotHubs: []dps.IotHubDefinitionDescription{{Location: ptr("westus"), AuthenticationType: ptr(dps.IotHubAuthenticationType_KeyBased),
						ConnectionString: &genruntime.SecretReference{Name: "plant-hub", Key: "connectionString"}}},
					AuthorizationPolicies: []dps.SharedAccessSignatureAuthorizationRuleAccessRightsDescription{{
						KeyName: ptr("enrollment-writer"), Rights: ptr(dps.AccessRightsDescription_EnrollmentWrite)}},
				}},
		}
	}
	enroll, keyless := service("enroll", "S1"), service("keyless", "S1")
	keyless.Spec.Properties.IotHubs[0].ConnectionString = nil
	first := []conditioned{hubCert, emptyCert, hubConn, enroll, keyless}
	for _, o := range first {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []conditioned{hubCert, hubConn, enroll} {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	eventually(t, timeout, "a certificate with no certificate refused", notReadyWith(emptyCert, "'certificate' is required"))
	eventually(t, timeout, "a key-based linked hub with no connection string refused", notReadyWith(keyless, "connection string"))

	armFake.AddResource(dpsID + "/privateEndpointConnections/pe-enroll")
	onDPS := &genruntime.KnownResourceReference{Name: "enroll"}
	dpsCert := &dps.ProvisioningServicesCertificate{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "enroll-root"},
		Spec:       dps.ProvisioningServicesCertificate_Spec{AzureName: "root", Owner: onDPS, Properties: &dps.CertificateProperties{Certificate: ptr("MIIBenrollroot")}},
	}
	dpsConn := &dps.ProvisioningServicesPrivateEndpointConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "enroll-pe"},
		Spec: dps.ProvisioningServicesPrivateEndpointConnection_Spec{AzureName: "pe-enroll", Owner: onDPS, Properties: &dps.PrivateEndpointConnectionProperties{
			PrivateLinkServiceConnectionState: &dps.PrivateLinkServiceConnectionState{Status: ptr(dps.PrivateLinkServiceConnectionStatus_Approved), Description: ptr("approved")}}},
	}
	second := []conditioned{dpsCert, dpsConn}
	for _, o := range second {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range second {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}

	expectResource(t, dpsID, func(r map[string]any) error {
		hubs, _ := properties(r)["iotHubs"].([]any)
		if len(hubs) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		if h, _ := hubs[0].(map[string]any); h["connectionString"] != connection {
			return fmt.Errorf("hub: %v", hubs[0])
		}
		return nil
	})
	// The linked hub's connection string, with its key, is not in the status.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(enroll), enroll); err != nil {
		t.Fatal(err)
	}
	status, err := json.Marshal(enroll.Status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(status), "SharedAccessKey=") || !strings.Contains(string(status), "Hashed") {
		t.Errorf("the status shows the hub's connection string, or not the service: %s", status)
	}
	expectResource(t, hubID+"/certificates/root", func(r map[string]any) error {
		if p := properties(r); p["certificate"] != "MIIBplantroot" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again: the allocation policy.
	if err := change(t, enroll, func(d *dps.ProvisioningService) {
		d.Spec.Properties.AllocationPolicy = ptr(dps.AllocationPolicy_GeoLatency)
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, dpsID, func(r map[string]any) error {
		if p := properties(r); p["allocationPolicy"] != "GeoLatency" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, objs := range [][]conditioned{second, first} {
		for _, o := range objs {
			if err := k8s.Delete(ctx, o); err != nil {
				t.Fatal(err)
			}
		}
		for _, o := range objs {
			eventually(t, timeout, o.GetName()+" gone", gone(o))
		}
	}
	for _, id := range []string{dpsID, dpsID + "/certificates/root", hubID + "/certificates/root"} {
		if armFake.HasResource(id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
}
