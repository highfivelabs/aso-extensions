// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Virtual WAN VPN: site-to-site VPN gateways in a hub
// (Microsoft.Network/vpnGateways) with their connections (vpnConnections)
// and NAT rules (natRules), VPN sites (vpnSites), VPN server configurations
// (vpnServerConfigurations) and point-to-site gateways (p2svpnGateways), API
// 2026-03-01, as far as the fake models them:
//
//   - a VPN gateway names a virtual hub that exists (virtualHub, 400 when
//     missing, 404 when not there), which has one VPN gateway (400 for a
//     second); it holds its connections and NAT rules as a circuit holds its
//     peerings: the lists connections and natRules are rendered from them on
//     a GET, and a PUT without them deletes them (assumed, as for circuits);
//   - a VPN connection names a VPN site that exists (remoteVpnSite, 400,
//     404); each of its vpnLinkConnections names a link of that site
//     (vpnSiteLink, 400 when missing or another site's, 404 when the site has
//     no such link); its sharedKey and its link connections' sharedKey are
//     kept but never returned (a link connection's is x-ms-secret in the
//     spec; the connection's is assumed the same), as for site-to-site
//     connections (vpn.go); SharedKey reads them for tests;
//   - a NAT rule's type is Static or Dynamic (Static when left out), its mode
//     EgressSnat or IngressSnat (400), and it gives internalMappings and
//     externalMappings (400);
//   - a VPN site names a virtual WAN that exists (virtualWan, 400, 404) and
//     gives an ipAddress or vpnSiteLinks (400); each link gives a name and an
//     ipAddress or an fqdn (400);
//   - a VPN server configuration gives vpnAuthenticationTypes (400), and
//     what each needs: Certificate vpnClientRootCertificates, Radius a
//     radiusServerAddress or radiusServers, AAD aadAuthenticationParameters
//     with an aadTenant (400); its radiusServerSecret (x-ms-secret) is kept
//     but never returned;
//   - a point-to-site gateway names a virtual hub that exists (400, 404),
//     which has one point-to-site gateway (400 for a second), and a VPN
//     server configuration that exists (vpnServerConfiguration, 400, 404);
//     each p2SConnectionConfigurations entry gives a vpnClientAddressPool
//     with addressPrefixes (400).
//
// Not modelled: tunnels coming up (connectionStatus, bytes transferred, BGP
// sessions), the gateway's public IP addresses (ipConfigurations), scale
// units and their throughput, NAT rules applied to link connections, a hub
// refusing deletion while it holds a gateway, client VPN profiles, the hour
// a real gateway takes, and the error codes (this fake's own).
var (
	vpnGatewayType = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/vpngateways/[^/]+$`)
	vpnGatewayKid  = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/vpngateways/[^/]+/(vpnconnections|natrules)/[^/]+$`)
	vpnSiteType    = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/vpnsites/[^/]+$`)
	vpnServerType  = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/vpnserverconfigurations/[^/]+$`)
	p2sGatewayType = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/p2svpngateways/[^/]+$`)
)

// checkVPNGateway refuses a Virtual WAN VPN resource the rules above do not
// allow, and otherwise takes its secrets out of body, keeping them aside;
// code is "" when it is allowed or id is no such resource.
func (a *ARM) checkVPNGateway(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		body["properties"] = props
	}
	switch {
	case vpnGatewayType.MatchString(id):
		return a.checkInHub(id, props, "VPN gateway", vpnGatewayType)
	case p2sGatewayType.MatchString(id):
		if status, code, msg := a.checkInHub(id, props, "point-to-site gateway", p2sGatewayType); code != "" {
			return status, code, msg
		}
		config := refID(props, "vpnServerConfiguration")
		if config == "" {
			return http.StatusBadRequest, "P2SVpnGatewayConfigurationMissing", "A point-to-site gateway needs its vpnServerConfiguration."
		}
		if !a.exists(config) {
			return http.StatusNotFound, "ResourceNotFound", "The VPN server configuration " + config + " was not found."
		}
		configs, _ := props["p2SConnectionConfigurations"].([]any)
		for _, item := range configs {
			c, _ := item.(map[string]any)
			cp, _ := c["properties"].(map[string]any)
			pool, _ := cp["vpnClientAddressPool"].(map[string]any)
			if prefixes, _ := pool["addressPrefixes"].([]any); len(prefixes) == 0 {
				return http.StatusBadRequest, "P2SConnectionConfigurationPoolMissing", "A point-to-site connection configuration needs a vpnClientAddressPool with addressPrefixes."
			}
		}
	case vpnSiteType.MatchString(id):
		wan := refID(props, "virtualWan")
		if wan == "" {
			return http.StatusBadRequest, "VpnSiteWanMissing", "A VPN site needs its virtualWan."
		}
		if !a.exists(wan) {
			return http.StatusNotFound, "ResourceNotFound", "The virtual WAN " + wan + " was not found."
		}
		links, _ := props["vpnSiteLinks"].([]any)
		if ip, _ := props["ipAddress"].(string); ip == "" && len(links) == 0 {
			return http.StatusBadRequest, "VpnSiteAddressMissing", "A VPN site needs its ipAddress or vpnSiteLinks."
		}
		for _, item := range links {
			link, _ := item.(map[string]any)
			lp, _ := link["properties"].(map[string]any)
			ip, _ := lp["ipAddress"].(string)
			fqdn, _ := lp["fqdn"].(string)
			if name, _ := link["name"].(string); name == "" || (ip == "" && fqdn == "") {
				return http.StatusBadRequest, "VpnSiteLinkIncomplete", "A VPN site link needs its name and an ipAddress or an fqdn."
			}
		}
	case vpnServerType.MatchString(id):
		types, _ := props["vpnAuthenticationTypes"].([]any)
		if len(types) == 0 {
			return http.StatusBadRequest, "VpnServerConfigurationAuthenticationMissing", "A VPN server configuration needs its vpnAuthenticationTypes."
		}
		for _, item := range types {
			switch t, _ := item.(string); t {
			case "Certificate":
				if roots, _ := props["vpnClientRootCertificates"].([]any); len(roots) == 0 {
					return http.StatusBadRequest, "VpnServerConfigurationIncomplete", "Certificate authentication needs vpnClientRootCertificates."
				}
			case "Radius":
				servers, _ := props["radiusServers"].([]any)
				if address, _ := props["radiusServerAddress"].(string); address == "" && len(servers) == 0 {
					return http.StatusBadRequest, "VpnServerConfigurationIncomplete", "RADIUS authentication needs a radiusServerAddress or radiusServers."
				}
			case "AAD":
				aad, _ := props["aadAuthenticationParameters"].(map[string]any)
				if tenant, _ := aad["aadTenant"].(string); tenant == "" {
					return http.StatusBadRequest, "VpnServerConfigurationIncomplete", "AAD authentication needs aadAuthenticationParameters with an aadTenant."
				}
			default:
				return http.StatusBadRequest, "VpnServerConfigurationAuthenticationInvalid", "A VPN authentication type is Certificate, Radius or AAD, not " + t + "."
			}
		}
		a.keepSecret(id+"/radiusServerSecret", props, "radiusServerSecret")
	default:
		m := vpnGatewayKid.FindStringSubmatch(id)
		if m == nil {
			return 0, "", ""
		}
		if strings.EqualFold(m[1], "natRules") {
			return checkNATRule(props)
		}
		return a.checkVPNConnection(id, props)
	}
	return 0, "", ""
}

// checkInHub is the check of a gateway in a virtual hub, one of its type
// (re) per hub.
func (a *ARM) checkInHub(id string, props map[string]any, what string, re *regexp.Regexp) (status int, code, message string) {
	hub := refID(props, "virtualHub")
	if hub == "" {
		return http.StatusBadRequest, "GatewayHubMissing", "A " + what + " needs its virtualHub."
	}
	if !a.exists(hub) {
		return http.StatusNotFound, "ResourceNotFound", "The virtual hub " + hub + " was not found."
	}
	for k, r := range a.resources {
		if re.MatchString(k) && !strings.EqualFold(k, id) {
			kp, _ := (*r)["properties"].(map[string]any)
			if strings.EqualFold(refID(kp, "virtualHub"), hub) {
				return http.StatusBadRequest, "VirtualHubHasGateway", "The virtual hub " + hub + " already has the " + what + " " + (*r)["id"].(string) + "."
			}
		}
	}
	return 0, "", ""
}

func (a *ARM) checkVPNConnection(id string, props map[string]any) (status int, code, message string) {
	site := refID(props, "remoteVpnSite")
	if site == "" {
		return http.StatusBadRequest, "VpnConnectionSiteMissing", "A VPN connection needs its remoteVpnSite."
	}
	r, ok := a.resources[strings.ToLower(site)]
	if !ok {
		return http.StatusNotFound, "ResourceNotFound", "The VPN site " + site + " was not found."
	}
	siteProps, _ := (*r)["properties"].(map[string]any)
	siteLinks := map[string]bool{}
	items, _ := siteProps["vpnSiteLinks"].([]any)
	for _, item := range items {
		link, _ := item.(map[string]any)
		name, _ := link["name"].(string)
		siteLinks[strings.ToLower(site+"/vpnSiteLinks/"+name)] = true
	}
	links, _ := props["vpnLinkConnections"].([]any)
	for _, item := range links {
		lc, _ := item.(map[string]any)
		lp, _ := lc["properties"].(map[string]any)
		link := refID(lp, "vpnSiteLink")
		if link == "" || !strings.HasPrefix(strings.ToLower(link), strings.ToLower(site)+"/vpnsitelinks/") {
			return http.StatusBadRequest, "VpnLinkConnectionSiteLinkInvalid", "A VPN link connection names a link of the connection's site " + site + "."
		}
		if !siteLinks[strings.ToLower(link)] {
			return http.StatusNotFound, "ResourceNotFound", "The VPN site link " + link + " was not found."
		}
	}
	a.keepSecret(id, props, "sharedKey")
	for _, item := range links {
		lc, _ := item.(map[string]any)
		lp, _ := lc["properties"].(map[string]any)
		if name, _ := lc["name"].(string); name != "" && lp != nil {
			a.keepSecret(id+"/vpnLinkConnections/"+name, lp, "sharedKey")
		}
	}
	return 0, "", ""
}

func checkNATRule(props map[string]any) (status int, code, message string) {
	switch t, _ := props["type"].(string); t {
	case "":
		props["type"] = "Static"
	case "Static", "Dynamic":
	default:
		return http.StatusBadRequest, "VpnNatRuleTypeInvalid", "A NAT rule's type is Static or Dynamic, not " + t + "."
	}
	if mode, _ := props["mode"].(string); mode != "EgressSnat" && mode != "IngressSnat" {
		return http.StatusBadRequest, "VpnNatRuleModeInvalid", "A NAT rule's mode is EgressSnat or IngressSnat."
	}
	for _, field := range []string{"internalMappings", "externalMappings"} {
		if m, _ := props[field].([]any); len(m) == 0 {
			return http.StatusBadRequest, "VpnNatRuleIncomplete", "A NAT rule needs its " + field + "."
		}
	}
	return 0, "", ""
}

// keepSecret takes field out of props and keeps it aside under key, as
// Azure keeps a key it does not return.
func (a *ARM) keepSecret(key string, props map[string]any, field string) {
	if k, ok := props[field].(string); ok {
		a.sharedKeys[strings.ToLower(key)] = k
		delete(props, field)
	}
}
