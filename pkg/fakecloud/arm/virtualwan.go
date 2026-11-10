// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net"
	"net/http"
	"regexp"
	"strings"
)

// Virtual WAN: virtual WANs (Microsoft.Network/virtualWans), their hubs
// (virtualHubs) and a hub's children: virtual network connections
// (hubVirtualNetworkConnections), route tables (hubRouteTables, and the
// older routeTables), routing intent (routingIntent), BGP connections
// (bgpConnections) and IP configurations (ipConfigurations), API 2026-03-01,
// as far as the fake models them:
//
//   - a virtual WAN's type, when given, is Basic or Standard (400), and is
//     Standard when left out;
//   - a hub names a virtual WAN that exists (virtualWan, 400 when missing,
//     404 when not there) and gives an addressPrefix in CIDR form (400);
//   - a hub holds its routeTables as a circuit holds its peerings: the list
//     virtualHubRouteTableV2s is rendered from them on a GET, and a hub PUT
//     without them deletes them (assumed, as for circuits);
//   - a virtual network connection names a virtual network that exists
//     (remoteVirtualNetwork, 400 when missing, 404 when not there), which
//     has one hub connection (400 for a second, in any hub); its
//     routingConfiguration's associatedRouteTable, when given, is a route
//     table of the same hub (400) that exists (404);
//   - a hub route table's routes each give a name, destinationType,
//     destinations, nextHopType and nextHop (400), and the next hop exists
//     (404);
//   - a hub has one routing intent (400 for a second); each of its routing
//     policies gives a name, destinations of Internet or PrivateTraffic only
//     (400) and a nextHop that exists (404);
//   - a BGP connection gives peerAsn and peerIp (400); its
//     hubVirtualNetworkConnection, when given, is a connection of the same
//     hub (400) that exists (404);
//   - an IP configuration names a subnet that exists (subnet, 400 when
//     missing, 404 when not there).
//
// Not modelled: the hub's router coming up and the routes it learns and
// propagates (associatedConnections, propagatingConnections, effective
// routes), the default and none route tables Azure makes in every hub, hub
// address space overlapping a connected network, Basic WANs allowing only
// site-to-site VPN, the hours a real hub takes, and the error codes (this
// fake's own).
var (
	vwanWAN       = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/virtualwans/[^/]+$`)
	vwanHub       = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/virtualhubs/[^/]+$`)
	vwanHubChild  = regexp.MustCompile(`(?i)^(/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/virtualhubs/[^/]+)/([^/]+)/[^/]+$`)
	vwanIntentTo  = map[string]bool{"internet": true, "privatetraffic": true}
)

// checkVirtualWAN refuses a Virtual WAN resource the rules above do not
// allow. code is "" when it is allowed or id is no Virtual WAN resource.
func (a *ARM) checkVirtualWAN(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		body["properties"] = props
	}
	switch {
	case vwanWAN.MatchString(id):
		switch t, _ := props["type"].(string); t {
		case "":
			props["type"] = "Standard"
		case "Basic", "Standard":
		default:
			return http.StatusBadRequest, "VirtualWanTypeInvalid", "A virtual WAN's type is Basic or Standard, not " + t + "."
		}
		return 0, "", ""
	case vwanHub.MatchString(id):
		wan := refID(props, "virtualWan")
		if wan == "" {
			return http.StatusBadRequest, "VirtualHubWanMissing", "A virtual hub needs its virtualWan."
		}
		if !a.exists(wan) {
			return http.StatusNotFound, "ResourceNotFound", "The virtual WAN " + wan + " was not found."
		}
		prefix, _ := props["addressPrefix"].(string)
		if _, _, err := net.ParseCIDR(prefix); err != nil {
			return http.StatusBadRequest, "VirtualHubAddressPrefixInvalid", "A virtual hub needs an addressPrefix in CIDR form."
		}
		return 0, "", ""
	}
	m := vwanHubChild.FindStringSubmatch(id)
	if m == nil {
		return 0, "", ""
	}
	hub := m[1]
	switch strings.ToLower(m[2]) {
	case "hubvirtualnetworkconnections":
		vnet := refID(props, "remoteVirtualNetwork")
		if vnet == "" {
			return http.StatusBadRequest, "HubVirtualNetworkConnectionRemoteMissing", "A hub virtual network connection needs its remoteVirtualNetwork."
		}
		if !a.exists(vnet) {
			return http.StatusNotFound, "ResourceNotFound", "The virtual network " + vnet + " was not found."
		}
		if other := a.hubConnectionOf(vnet, id); other != "" {
			return http.StatusBadRequest, "VirtualNetworkAlreadyConnectedToHub", "The virtual network " + vnet + " is already connected to a hub by " + other + "."
		}
		if routing, ok := props["routingConfiguration"].(map[string]any); ok {
			if table := refID(routing, "associatedRouteTable"); table != "" {
				if !strings.HasPrefix(strings.ToLower(table), strings.ToLower(hub)+"/hubroutetables/") {
					return http.StatusBadRequest, "HubRouteTableNotInHub", "The associated route table " + table + " is not a route table of " + hub + "."
				}
				if !a.exists(table) {
					return http.StatusNotFound, "ResourceNotFound", "The route table " + table + " was not found."
				}
			}
		}
	case "hubroutetables":
		routes, _ := props["routes"].([]any)
		for _, item := range routes {
			route, _ := item.(map[string]any)
			destinations, _ := route["destinations"].([]any)
			for _, field := range []string{"name", "destinationType", "nextHopType", "nextHop"} {
				if s, _ := route[field].(string); s == "" {
					return http.StatusBadRequest, "HubRouteIncomplete", "A hub route needs its " + field + "."
				}
			}
			if len(destinations) == 0 {
				return http.StatusBadRequest, "HubRouteIncomplete", "A hub route needs its destinations."
			}
			if next := route["nextHop"].(string); !a.exists(next) {
				return http.StatusNotFound, "ResourceNotFound", "The next hop " + next + " was not found."
			}
		}
	case "routingintent":
		if other := a.childOf(hub, "routingIntent", id); other != "" {
			return http.StatusBadRequest, "VirtualHubHasRoutingIntent", "The virtual hub " + hub + " already has the routing intent " + other + "."
		}
		policies, _ := props["routingPolicies"].([]any)
		for _, item := range policies {
			policy, _ := item.(map[string]any)
			if name, _ := policy["name"].(string); name == "" {
				return http.StatusBadRequest, "RoutingPolicyIncomplete", "A routing policy needs its name."
			}
			destinations, _ := policy["destinations"].([]any)
			if len(destinations) == 0 {
				return http.StatusBadRequest, "RoutingPolicyIncomplete", "A routing policy needs its destinations."
			}
			for _, d := range destinations {
				if s, _ := d.(string); !vwanIntentTo[strings.ToLower(s)] {
					return http.StatusBadRequest, "RoutingPolicyDestinationInvalid", "A routing policy's destinations are Internet or PrivateTraffic."
				}
			}
			next, _ := policy["nextHop"].(string)
			if next == "" {
				return http.StatusBadRequest, "RoutingPolicyIncomplete", "A routing policy needs its nextHop."
			}
			if !a.exists(next) {
				return http.StatusNotFound, "ResourceNotFound", "The next hop " + next + " was not found."
			}
		}
	case "bgpconnections":
		if _, ok := props["peerAsn"].(float64); !ok {
			return http.StatusBadRequest, "BgpConnectionIncomplete", "A BGP connection needs its peerAsn."
		}
		if ip, _ := props["peerIp"].(string); net.ParseIP(ip) == nil {
			return http.StatusBadRequest, "BgpConnectionIncomplete", "A BGP connection needs its peerIp."
		}
		if conn := refID(props, "hubVirtualNetworkConnection"); conn != "" {
			if !strings.HasPrefix(strings.ToLower(conn), strings.ToLower(hub)+"/hubvirtualnetworkconnections/") {
				return http.StatusBadRequest, "HubVirtualNetworkConnectionNotInHub", "The connection " + conn + " is not a connection of " + hub + "."
			}
			if !a.exists(conn) {
				return http.StatusNotFound, "ResourceNotFound", "The connection " + conn + " was not found."
			}
		}
	case "ipconfigurations":
		subnet := refID(props, "subnet")
		if subnet == "" {
			return http.StatusBadRequest, "HubIpConfigurationSubnetMissing", "A hub IP configuration needs its subnet."
		}
		if !a.exists(subnet) {
			return http.StatusNotFound, "ResourceNotFound", "The subnet " + subnet + " was not found."
		}
	}
	return 0, "", ""
}

// hubConnectionOf is the hub virtual network connection other than id that
// connects vnet, or "".
func (a *ARM) hubConnectionOf(vnet, id string) string {
	for k, r := range a.resources {
		m := vwanHubChild.FindStringSubmatch(k)
		if m == nil || !strings.EqualFold(m[2], "hubVirtualNetworkConnections") || strings.EqualFold(k, id) {
			continue
		}
		props, _ := (*r)["properties"].(map[string]any)
		if strings.EqualFold(refID(props, "remoteVirtualNetwork"), vnet) {
			return (*r)["id"].(string)
		}
	}
	return ""
}

// childOf is a child of parent of type segment other than id, or "".
func (a *ARM) childOf(parent, segment, id string) string {
	prefix := strings.ToLower(parent + "/" + segment + "/")
	for k, r := range a.resources {
		if strings.HasPrefix(k, prefix) && !strings.Contains(k[len(prefix):], "/") && !strings.EqualFold(k, id) {
			return (*r)["id"].(string)
		}
	}
	return ""
}
