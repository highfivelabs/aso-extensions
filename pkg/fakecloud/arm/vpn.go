// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Site-to-site VPN: virtual network gateway connections
// (Microsoft.Network/connections) and local network gateways
// (Microsoft.Network/localNetworkGateways), API 2026-03-01, as far as the
// fake models them:
//
//   - a connection names its connectionType and virtualNetworkGateway1 (the
//     spec's required properties; 400 when either is missing), and the other
//     side its type needs: an IPsec connection a localNetworkGateway2, a
//     Vnet2Vnet connection a virtualNetworkGateway2, an ExpressRoute
//     connection a peer (400 when missing); each gateway or peer it names
//     must exist (404, as for any missing reference);
//   - a connection's sharedKey is kept but never returned (the spec: "We
//     will no longer return sharedKey in VirtualNetworkGatewayConnection
//     Create/Update/Get/List/UpdateTags APIs response"); SharedKey reads it
//     for tests, as Azure's GetSharedKey does;
//   - an ExpressRoute connection to a circuit in another subscription gives
//     one of the circuit's authorization keys (authorizationKey; 400 when
//     missing, not the circuit's or in use by another), which is then in
//     use (expressroute.go);
//   - a local network gateway gives its gatewayIpAddress or its fqdn (400
//     when neither).
//
// Not modelled: a tunnel coming up (connectionStatus, bytes transferred),
// whether the gateway's SKU and VPN type allow the connection's settings
// (policy-based traffic selectors, custom IPsec policies, BGP), address
// spaces overlapping, the gateway and the connection being in one region,
// the long provisioning of a real gateway connection, and the error codes
// (this fake's own).
var (
	connectionType   = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/connections/[^/]+$`)
	localGatewayType = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/localnetworkgateways/[^/]+$`)
)

// checkVPN refuses a connection or a local network gateway the rules above
// do not allow, and otherwise takes a connection's shared key out of body,
// keeping it aside; code is "" when it is allowed or id is neither.
func (a *ARM) checkVPN(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	if localGatewayType.MatchString(id) {
		ip, _ := props["gatewayIpAddress"].(string)
		fqdn, _ := props["fqdn"].(string)
		if ip == "" && fqdn == "" {
			return http.StatusBadRequest, "LocalNetworkGatewayAddressMissing", "A local network gateway needs its gatewayIpAddress or its fqdn."
		}
		return 0, "", ""
	}
	if !connectionType.MatchString(id) {
		return 0, "", ""
	}
	kind, _ := props["connectionType"].(string)
	if kind == "" {
		return http.StatusBadRequest, "ConnectionTypeMissing", "A connection needs its connectionType."
	}
	needs := map[string]string{"IPsec": "localNetworkGateway2", "Vnet2Vnet": "virtualNetworkGateway2", "ExpressRoute": "peer"}
	for _, name := range []string{"virtualNetworkGateway1", needs[kind]} {
		if name == "" {
			continue
		}
		ref, _ := props[name].(map[string]any)
		target, _ := ref["id"].(string)
		if target == "" {
			return http.StatusBadRequest, "ConnectionEndMissing", "A connection of type " + kind + " needs " + name + "."
		}
		if !a.exists(target) {
			return http.StatusNotFound, "ResourceNotFound", "The resource " + target + " (" + name + ") was not found."
		}
	}
	if peer := refID(props, "peer"); kind == "ExpressRoute" && (!sameSubscription(id, peer) || props["authorizationKey"] != nil) {
		if a.redeem(peer, props["authorizationKey"], id, "connectionResourceUri") == "" {
			return http.StatusBadRequest, "ExpressRouteAuthorizationKeyInvalid", "An ExpressRoute connection to the circuit " + peer + " in another subscription needs one of the circuit's authorization keys."
		}
	}
	if k, ok := props["sharedKey"].(string); ok {
		a.sharedKeys[strings.ToLower(id)] = k
		delete(props, "sharedKey")
	}
	return 0, "", ""
}

// SharedKey returns a key kept aside (a connection's shared key, a VPN link
// connection's, a VPN server configuration's RADIUS secret under its ID and
// "/radiusServerSecret") while its resource is there, as Azure's
// GetSharedKey does, for tests.
func (a *ARM) SharedKey(id string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := strings.ToLower(id)
	k, ok := a.sharedKeys[key]
	return k, ok
}
