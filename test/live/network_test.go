// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

//go:build live

package live

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	nspapi "github.com/highfivelabs/aso-extensions/apis/arm/network/v20250501"
	network "github.com/highfivelabs/aso-extensions/apis/arm/network/v20260301"
)

const networkAPI = "2026-03-01"

// A local network gateway (free): created, changed, deleted.
func TestLiveLocalNetworkGateway(t *testing.T) {
	ns := namespace(t, "lng")
	gw := &network.LocalNetworkGateway{}
	gw.Namespace, gw.Name = ns, "onprem"
	gw.Spec = network.LocalNetworkGateway_Spec{
		AzureName:                azName("onprem"),
		Owner:                    owner("rg"),
		Location:                 ptr(location),
		GatewayIpAddress:         ptr("203.0.113.10"),
		LocalNetworkAddressSpace: &network.AddressSpace{AddressPrefixes: []string{"10.20.0.0/16"}},
		BgpSettings:              &network.BgpSettings{Asn: ptr(uint32(65010)), BgpPeeringAddress: ptr("10.20.255.254")},
	}
	create(t, timeout, gw)
	id := rgID() + "/providers/Microsoft.Network/localNetworkGateways/" + gw.Spec.AzureName
	expectResource(t, id, networkAPI, func(r map[string]any) error {
		p := properties(r)
		space, _ := p["localNetworkAddressSpace"].(map[string]any)
		bgp, _ := p["bgpSettings"].(map[string]any)
		if p["gatewayIpAddress"] != "203.0.113.10" || !reflect.DeepEqual(space["addressPrefixes"], []any{"10.20.0.0/16"}) || bgp["asn"] != float64(65010) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	update(t, gw, func(g *network.LocalNetworkGateway) {
		g.Spec.LocalNetworkAddressSpace.AddressPrefixes = []string{"10.20.0.0/16", "10.21.0.0/16"}
	})
	eventually(t, timeout, "LocalNetworkGateway Ready after the change", ready(gw))
	expectResource(t, id, networkAPI, func(r map[string]any) error {
		space, _ := properties(r)["localNetworkAddressSpace"].(map[string]any)
		if !reflect.DeepEqual(space["addressPrefixes"], []any{"10.20.0.0/16", "10.21.0.0/16"}) {
			return fmt.Errorf("address space: %v", space)
		}
		return nil
	})

	remove(t, timeout, gw)
	expectGone(t, id, networkAPI)
}

// A Virtual WAN (Standard, free with no hub), a VPN site in it and a VPN
// server configuration (free until a point-to-site gateway uses it).
func TestLiveVirtualWanSiteServerConfiguration(t *testing.T) {
	ns := namespace(t, "vwan")
	if err := k8s.Create(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "vpn-keys"},
		StringData: map[string]string{"radius": "radius-secret-1"},
	}); err != nil {
		t.Fatal(err)
	}
	wan := &network.VirtualWan{}
	wan.Namespace, wan.Name = ns, "wan"
	wan.Spec = network.VirtualWan_Spec{
		AzureName: azName("wan"), Owner: owner("rg"), Location: ptr(location), Type: ptr("Standard"),
		AllowBranchToBranchTraffic: ptr(true),
	}
	site := &network.VpnSite{}
	site.Namespace, site.Name = ns, "branch"
	site.Spec = network.VpnSite_Spec{
		AzureName: azName("branch"), Owner: owner("rg"), Location: ptr(location),
		VirtualWan:       &network.SubResource{Reference: &genruntime.ResourceReference{Group: "network.azure.hfl.dev", Kind: "VirtualWan", Name: "wan"}},
		DeviceProperties: &network.DeviceProperties{DeviceVendor: ptr("Contoso")},
		// Azure needs the site's address space or BGP on its links.
		AddressSpace: &network.AddressSpace{AddressPrefixes: []string{"10.30.0.0/16"}},
		VpnSiteLinks: []network.VpnSiteLink{{
			Name: ptr("isp1"), IpAddress: ptr("203.0.113.10"),
			LinkProperties: &network.VpnLinkProviderProperties{LinkProviderName: ptr("ISP1"), LinkSpeedInMbps: ptr(100)},
		}},
	}
	cfg := &network.VpnServerConfiguration{}
	cfg.Namespace, cfg.Name = ns, "users"
	cfg.Spec = network.VpnServerConfiguration_Spec{
		AzureName: azName("users"), Owner: owner("rg"), Location: ptr(location),
		VpnProtocols:           []network.VpnGatewayTunnelingProtocol{network.VpnGatewayTunnelingProtocol_OpenVPN},
		VpnAuthenticationTypes: []network.VpnAuthenticationType{network.VpnAuthenticationType_Radius},
		RadiusServerAddress:    ptr("10.100.0.10"),
		RadiusServerSecret:     &genruntime.SecretReference{Name: "vpn-keys", Key: "radius"},
	}
	create(t, timeout, wan, site, cfg)

	wanID := rgID() + "/providers/Microsoft.Network/virtualWans/" + wan.Spec.AzureName
	siteID := rgID() + "/providers/Microsoft.Network/vpnSites/" + site.Spec.AzureName
	cfgID := rgID() + "/providers/Microsoft.Network/vpnServerConfigurations/" + cfg.Spec.AzureName
	expectResource(t, wanID, networkAPI, func(r map[string]any) error {
		if p := properties(r); p["type"] != "Standard" || p["allowBranchToBranchTraffic"] != true {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, siteID, networkAPI, func(r map[string]any) error {
		p := properties(r)
		w, _ := p["virtualWan"].(map[string]any)
		links, _ := p["vpnSiteLinks"].([]any)
		if !strings.EqualFold(fmt.Sprint(w["id"]), wanID) || len(links) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, cfgID, networkAPI, func(r map[string]any) error {
		if p := properties(r); p["radiusServerAddress"] != "10.100.0.10" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	update(t, site, func(s *network.VpnSite) { s.Spec.DeviceProperties.DeviceModel = ptr("edge-1") })
	eventually(t, timeout, "VpnSite Ready after the change", ready(site))
	expectResource(t, siteID, networkAPI, func(r map[string]any) error {
		d, _ := properties(r)["deviceProperties"].(map[string]any)
		if d["deviceModel"] != "edge-1" {
			return fmt.Errorf("device: %v", d)
		}
		return nil
	})

	for _, o := range []client.Object{cfg, site, wan} {
		remove(t, timeout, o)
	}
	for _, id := range []string{cfgID, siteID, wanID} {
		expectGone(t, id, networkAPI)
	}
}

// A network security perimeter (free) with a profile, an inbound access rule,
// a storage account associated in Learning mode then Enforced, its logging
// configuration, and a link to a second perimeter.
func TestLiveNetworkSecurityPerimeter(t *testing.T) {
	const nspAPI = "2025-05-01"
	ns := namespace(t, "nsp")
	storageName := strings.ReplaceAll(azName("nspst"), "-", "")
	storageID := rgID() + "/providers/Microsoft.Storage/storageAccounts/" + storageName
	fixture(t, storageID, "2023-05-01", map[string]any{
		"location": location, "kind": "StorageV2", "sku": map[string]any{"name": "Standard_LRS"},
		"properties": map[string]any{"minimumTlsVersion": "TLS1_2", "allowBlobPublicAccess": false},
	})

	nsp := &nspapi.NetworkSecurityPerimeter{}
	nsp.Namespace, nsp.Name = ns, "perimeter"
	nsp.Spec = nspapi.NetworkSecurityPerimeter_Spec{AzureName: azName("nsp"), Owner: owner("rg"), Location: ptr(location)}
	remote := &nspapi.NetworkSecurityPerimeter{}
	remote.Namespace, remote.Name = ns, "remote"
	remote.Spec = nspapi.NetworkSecurityPerimeter_Spec{AzureName: azName("nsp-remote"), Owner: owner("rg"), Location: ptr(location)}
	profile := &nspapi.NetworkSecurityPerimetersProfile{}
	profile.Namespace, profile.Name = ns, "default"
	profile.Spec = nspapi.NetworkSecurityPerimetersProfile_Spec{Owner: owner("perimeter")}
	rule := &nspapi.NetworkSecurityPerimetersProfilesAccessRule{}
	rule.Namespace, rule.Name = ns, "office"
	rule.Spec = nspapi.NetworkSecurityPerimetersProfilesAccessRule_Spec{
		Owner: owner("default"), Direction: ptr(nspapi.AccessRuleDirection("Inbound")), AddressPrefixes: []string{"203.0.113.0/24"},
	}
	nspID := rgID() + "/providers/Microsoft.Network/networkSecurityPerimeters/" + nsp.Spec.AzureName
	remoteID := rgID() + "/providers/Microsoft.Network/networkSecurityPerimeters/" + remote.Spec.AzureName
	profileID := nspID + "/profiles/default"
	assoc := &nspapi.NetworkSecurityPerimetersResourceAssociation{}
	assoc.Namespace, assoc.Name = ns, "store"
	assoc.Spec = nspapi.NetworkSecurityPerimetersResourceAssociation_Spec{
		Owner:               owner("perimeter"),
		AccessMode:          ptr(nspapi.AssociationAccessMode_Learning),
		PrivateLinkResource: &nspapi.SubResource{Reference: &genruntime.ResourceReference{ARMID: storageID}},
		Profile:             &nspapi.SubResource{Reference: &genruntime.ResourceReference{ARMID: profileID}},
	}
	logging := &nspapi.NetworkSecurityPerimetersLoggingConfiguration{}
	logging.Namespace, logging.Name = ns, "logging"
	logging.Spec = nspapi.NetworkSecurityPerimetersLoggingConfiguration_Spec{
		AzureName: "instance", Owner: owner("perimeter"), EnabledLogCategories: []string{"NspPublicInboundPerimeterRulesAllowed"},
	}
	link := &nspapi.NetworkSecurityPerimetersLink{}
	link.Namespace, link.Name = ns, "to-remote"
	link.Spec = nspapi.NetworkSecurityPerimetersLink_Spec{
		Owner: owner("perimeter"),
		AutoApprovedRemotePerimeterResourceReference: &genruntime.ResourceReference{ARMID: remoteID},
		LocalInboundProfiles:                         []string{"*"},
		RemoteInboundProfiles:                        []string{"*"},
	}
	create(t, timeout, nsp, remote, profile, rule, assoc, link)

	ruleID := profileID + "/accessRules/office"
	assocID := nspID + "/resourceAssociations/store"
	expectResource(t, ruleID, nspAPI, func(r map[string]any) error {
		p := properties(r)
		if p["direction"] != "Inbound" || !reflect.DeepEqual(p["addressPrefixes"], []any{"203.0.113.0/24"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, assocID, nspAPI, func(r map[string]any) error {
		p := properties(r)
		pl, _ := p["privateLinkResource"].(map[string]any)
		if p["accessMode"] != "Learning" || !strings.EqualFold(fmt.Sprint(pl["id"]), storageID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	t.Run("LoggingConfiguration", func(t *testing.T) {
		// Azure refused it on 2026-11-10: "tenant is not whitelisted and
		// EnableServiceTagsInNsp AFEC flag is not registered for the
		// subscription" (a subscription feature; docs/live-runs.md).
		if os.Getenv("LIVE_NSP_LOGGING") == "" {
			t.Skip("needs the subscription feature Microsoft.Network/EnableServiceTagsInNsp; set LIVE_NSP_LOGGING=1 once registered")
		}
		create(t, timeout, logging)
		expectResource(t, nspID+"/loggingConfigurations/instance", nspAPI, func(r map[string]any) error {
			if !reflect.DeepEqual(properties(r)["enabledLogCategories"], []any{"NspPublicInboundPerimeterRulesAllowed"}) {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})
		remove(t, timeout, logging)
	})
	expectResource(t, nspID+"/links/to-remote", nspAPI, func(r map[string]any) error {
		if !strings.EqualFold(fmt.Sprint(properties(r)["autoApprovedRemotePerimeterResourceId"]), remoteID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	update(t, assoc, func(a *nspapi.NetworkSecurityPerimetersResourceAssociation) {
		a.Spec.AccessMode = ptr(nspapi.AssociationAccessMode_Enforced)
	})
	eventually(t, timeout, "association Ready after the change", ready(assoc))
	expectResource(t, assocID, nspAPI, func(r map[string]any) error {
		if properties(r)["accessMode"] != "Enforced" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range []client.Object{link, assoc, rule, profile, remote, nsp} {
		remove(t, timeout, o)
	}
	expectGone(t, nspID, nspAPI)
	expectGone(t, remoteID, nspAPI)
}
