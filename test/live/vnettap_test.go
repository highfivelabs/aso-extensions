// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	asonetwork "github.com/Azure/azure-service-operator/v2/api/network/v20250301/storage"

	network "github.com/highfivelabs/aso-extensions/apis/arm/network/v20260301"
)

// A virtual network TAP mirroring to a collector NIC's IP configuration, and
// a tap configuration on a second NIC, a fixture standing in for ASO's
// NetworkInterface (docs/plan.md f60): created, the TAP's port changed, both
// deleted. Cheap: two NICs in a new virtual network with no VM behind them
// (no traffic is mirrored) and a TAP for the minutes of the run. Not known
// before a run: whether Azure takes a tap configuration on a NIC no VM uses;
// if it refuses, the test needs the small VM fixture (linuxVM) instead.
func TestLiveVirtualNetworkTap(t *testing.T) {
	t.Parallel()
	ns := namespace(t, "vtap")
	vnetID := vnet(t, azName("tapnet"), "10.42.0.0/16", map[string]string{"default": "10.42.0.0/24"})
	nic := func(base string) string {
		id := rgID() + "/providers/Microsoft.Network/networkInterfaces/" + azName(base)
		fixture(t, id, fixtureNetworkAPI, map[string]any{
			"location": location,
			"properties": map[string]any{"ipConfigurations": []any{map[string]any{"name": "ipconfig1", "properties": map[string]any{
				"subnet": map[string]any{"id": vnetID + "/subnets/default"}, "privateIPAllocationMethod": "Dynamic",
			}}}},
		})
		return id
	}
	collectorID := nic("collector") + "/ipConfigurations/ipconfig1"
	sourceID := nic("tapped")
	source := &asonetwork.NetworkInterface{}
	source.Namespace, source.Name = ns, "tapped"
	source.Spec.AzureName = azName("tapped")
	source.Spec.Location = ptr(location)
	source.Spec.Owner = owner("rg")
	standIn(t, source, sourceID, nil)

	tap := &network.VirtualNetworkTap{}
	tap.Namespace, tap.Name = ns, "mirror"
	tap.Spec = network.VirtualNetworkTap_Spec{
		AzureName: azName("mirror"), Owner: owner("rg"), Location: ptr(location),
		DestinationNetworkInterfaceIPConfiguration: &network.NetworkInterfaceIPConfiguration_VirtualNetworkTap_SubResourceEmbedded{
			Reference: &genruntime.ResourceReference{ARMID: collectorID},
		},
		DestinationPort: ptr(4789),
	}
	create(t, timeout, tap)
	tapID := rgID() + "/providers/Microsoft.Network/virtualNetworkTaps/" + tap.Spec.AzureName

	conf := &network.NetworkInterfacesTapConfiguration{}
	conf.Namespace, conf.Name = ns, "mirror"
	conf.Spec = network.NetworkInterfacesTapConfiguration_Spec{
		AzureName: "mirror", Owner: owner("tapped"),
		VirtualNetworkTap: &network.VirtualNetworkTapSpec{Reference: ours("VirtualNetworkTap", "mirror")},
	}
	create(t, timeout, conf)
	confID := sourceID + "/tapConfigurations/mirror"
	expectResource(t, confID, networkAPI, func(r map[string]any) error {
		if v, _ := properties(r)["virtualNetworkTap"].(map[string]any); !strings.EqualFold(fmt.Sprint(v["id"]), tapID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, tapID, networkAPI, func(r map[string]any) error {
		p := properties(r)
		dest, _ := p["destinationNetworkInterfaceIPConfiguration"].(map[string]any)
		confs, _ := p["networkInterfaceTapConfigurations"].([]any)
		if !strings.EqualFold(fmt.Sprint(dest["id"]), collectorID) || len(confs) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, tap, func(v *network.VirtualNetworkTap) { v.Spec.DestinationPort = ptr(4790) })
	eventually(t, timeout, "the TAP Ready after the change", ready(tap))
	expectResource(t, tapID, networkAPI, func(r map[string]any) error {
		if p := properties(r); p["destinationPort"] != float64(4790) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, conf)
	expectGone(t, confID, networkAPI)
	remove(t, timeout, tap)
	expectGone(t, tapID, networkAPI)
}
