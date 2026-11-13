// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	compute2025 "github.com/highfivelabs/aso-extensions/apis/arm/compute/v20250401"
	network2026 "github.com/highfivelabs/aso-extensions/apis/arm/network/v20260301"
)

// Our VirtualMachine (2025-04-01), beside ASO's (2022-03-01), for the disk
// controller type and the platform patch schedule's safety-check bypass:
// PUT with both, the admin password from a Secret, a change PUT again,
// deleted.
func TestVirtualMachineNewerFields(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "vm-newer")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	nicID := rgID + "/providers/Microsoft.Network/networkInterfaces/vm-nic"
	armFake.AddResource(rgID)
	armFake.AddResource(nicID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	secret(t, ns, "vm-admin", map[string]string{"password": "Adm1n-Passw0rd!"})

	vm := &compute2025.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "app"},
		Spec: compute2025.VirtualMachine_Spec{
			Owner:           &genruntime.KnownResourceReference{Name: "rg"},
			Location:        ptr("westus"),
			HardwareProfile: &compute2025.HardwareProfile{VmSize: ptr("Standard_D2s_v5")},
			StorageProfile: &compute2025.StorageProfile{
				DiskControllerType: ptr(compute2025.DiskControllerTypes("NVMe")),
				ImageReference: &compute2025.ImageReference{
					Publisher: ptr("Canonical"), Offer: ptr("ubuntu-24_04-lts"), Sku: ptr("server"), Version: ptr("latest"),
				},
			},
			OsProfile: &compute2025.OSProfile{
				ComputerName:  ptr("app"),
				AdminUsername: ptr("azureuser"),
				AdminPassword: &genruntime.SecretReference{Name: "vm-admin", Key: "password"},
				LinuxConfiguration: &compute2025.LinuxConfiguration{
					PatchSettings: &compute2025.LinuxPatchSettings{
						PatchMode: ptr(compute2025.LinuxVMGuestPatchMode("AutomaticByPlatform")),
						AutomaticByPlatformSettings: &compute2025.LinuxVMGuestPatchAutomaticByPlatformSettings{
							BypassPlatformSafetyChecksOnUserSchedule: ptr(true),
						},
					},
				},
			},
			NetworkProfile: &compute2025.NetworkProfile{NetworkInterfaces: []compute2025.NetworkInterfaceReference{
				{Reference: &genruntime.ResourceReference{ARMID: nicID}, Primary: ptr(true)},
			}},
		},
	}
	if err := k8s.Create(ctx, vm); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "VirtualMachine Ready", ready(vm))

	id := rgID + "/providers/Microsoft.Compute/virtualMachines/app"
	expectResource(t, id, func(r map[string]any) error {
		p := properties(r)
		storage, _ := p["storageProfile"].(map[string]any)
		os, _ := p["osProfile"].(map[string]any)
		linux, _ := os["linuxConfiguration"].(map[string]any)
		patch, _ := linux["patchSettings"].(map[string]any)
		auto, _ := patch["automaticByPlatformSettings"].(map[string]any)
		switch {
		case r["type"] != "Microsoft.Compute/virtualMachines":
			return fmt.Errorf("type: %v", r["type"])
		case storage["diskControllerType"] != "NVMe":
			return fmt.Errorf("storageProfile: %v", storage)
		case auto["bypassPlatformSafetyChecksOnUserSchedule"] != true:
			return fmt.Errorf("patchSettings: %v", patch)
		case os["adminPassword"] != "Adm1n-Passw0rd!":
			return fmt.Errorf("osProfile: the admin password was not taken from the Secret")
		}
		return nil
	})

	if err := k8s.Get(ctx, client.ObjectKeyFromObject(vm), vm); err != nil {
		t.Fatal(err)
	}
	vm.Spec.StorageProfile.DiskControllerType = ptr(compute2025.DiskControllerTypes("SCSI"))
	if err := k8s.Update(ctx, vm); err != nil {
		t.Fatal(err)
	}
	expectResource(t, id, func(r map[string]any) error {
		storage, _ := properties(r)["storageProfile"].(map[string]any)
		if storage["diskControllerType"] != "SCSI" {
			return fmt.Errorf("storageProfile: %v", storage)
		}
		return nil
	})

	if err := k8s.Delete(ctx, vm); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "VirtualMachine gone", gone(vm))
	if armFake.HasResource(id) {
		t.Fatalf("%s is still in Resource Manager", id)
	}
}

// Our ApplicationGateway (2026-03-01), beside ASO's (2022-07-01), for Entra
// JWT validation: a configuration, and a routing rule that names it by the ID
// Azure gives it.
func TestApplicationGatewayEntraJWTValidation(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "appgw-jwt")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	subnetID := rgID + "/providers/Microsoft.Network/virtualNetworks/vnet/subnets/appgw"
	armFake.AddResource(rgID)
	armFake.AddResource(rgID + "/providers/Microsoft.Network/virtualNetworks/vnet")
	armFake.AddResource(subnetID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	gwID := rgID + "/providers/Microsoft.Network/applicationGateways/gw"
	jwtID := gwID + "/entraJWTValidationConfigs/api"

	gw := &network2026.ApplicationGateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "gw"},
		Spec: network2026.ApplicationGateway_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
			Sku: &network2026.ApplicationGatewaySku{
				Name: ptr(network2026.ApplicationGatewaySkuName("Standard_v2")), Tier: ptr(network2026.ApplicationGatewayTier("Standard_v2")), Capacity: ptr(1),
			},
			GatewayIPConfigurations: []network2026.ApplicationGatewayIPConfiguration{
				{Name: ptr("gwip"), Subnet: &network2026.SubResource{Reference: &genruntime.ResourceReference{ARMID: subnetID}}},
			},
			EntraJWTValidationConfigs: []network2026.ApplicationGatewayEntraJWTValidationConfig{{
				Name:                      ptr("api"),
				ClientId:                  ptr("00000000-0000-0000-0000-0000000000c1"),
				TenantId:                  ptr(tenantID),
				Audiences:                 []string{"api://orders"},
				UnAuthorizedRequestAction: ptr(network2026.ApplicationGatewayUnAuthorizedRequestAction_Deny),
			}},
			RequestRoutingRules: []network2026.ApplicationGatewayRequestRoutingRule{{
				Name:                     ptr("orders"),
				Priority:                 ptr(100),
				RuleType:                 ptr(network2026.ApplicationGatewayRequestRoutingRuleType("Basic")),
				EntraJWTValidationConfig: &network2026.SubResource{Reference: &genruntime.ResourceReference{ARMID: jwtID}},
			}},
		},
	}
	if err := k8s.Create(ctx, gw); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "ApplicationGateway Ready", ready(gw))

	expectResource(t, gwID, func(r map[string]any) error {
		p := properties(r)
		configs, _ := p["entraJWTValidationConfigs"].([]any)
		rules, _ := p["requestRoutingRules"].([]any)
		if len(configs) != 1 || len(rules) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		c := properties(configs[0].(map[string]any))
		rule := properties(rules[0].(map[string]any))
		ref, _ := rule["entraJWTValidationConfig"].(map[string]any)
		if c["clientId"] != "00000000-0000-0000-0000-0000000000c1" || c["unAuthorizedRequestAction"] != "Deny" || ref["id"] != jwtID {
			return fmt.Errorf("entraJWTValidationConfigs: %v; rule: %v", configs, rule)
		}
		return nil
	})

	if err := k8s.Delete(ctx, gw); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "ApplicationGateway gone", gone(gw))
	if armFake.HasResource(gwID) {
		t.Fatalf("%s is still in Resource Manager", gwID)
	}
}
