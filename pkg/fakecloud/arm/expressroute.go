// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"maps"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// ExpressRoute: circuits (Microsoft.Network/expressRouteCircuits), their
// peerings, authorizations and Global Reach connections
// (peerings/{p}/connections), ExpressRoute Direct ports
// (expressRoutePorts) and their authorizations, API 2026-03-01, as far as
// the fake models them:
//
//   - a circuit names its sku and either a connectivity provider
//     (serviceProviderProperties) or an ExpressRoute Direct port
//     (expressRoutePort, with bandwidthInGbps; 400 when neither), and the
//     port must exist (404); a circuit on a port in another subscription
//     gives one of the port's authorization keys (authorizationKey; 400 when
//     missing or not the port's), which is then in use;
//   - Azure makes a circuit's serviceKey and an authorization's
//     authorizationKey (a circuit's or a port's) on the first PUT and keeps
//     them; what a PUT sends for them is ignored;
//   - a circuit holds its peerings and authorizations, and a peering its
//     Global Reach connections, as child resources and as lists in its own
//     properties: a GET renders the lists from the children, and a PUT of the
//     parent makes the children what its lists say, deleting a child the list
//     leaves out (so a client that PUTs a circuit without its peerings
//     deletes them, which is why ASO's network kinds copy them in);
//   - a peering is named for its peeringType (AzurePrivatePeering,
//     MicrosoftPeering; 400 otherwise) and gives vlanId, peerASN and both
//     peer address prefixes (400);
//   - a Global Reach connection names its own peering
//     (expressRouteCircuitPeering, 400 when another), a peer peering that
//     exists (peerExpressRouteCircuitPeering, 404) and an addressPrefix
//     (400); when the peer circuit is in another subscription it gives one of
//     the peer circuit's authorization keys (400 when missing or not the
//     peer's), which is then in use; a key in use by another circuit or
//     connection is refused the same way (400);
//   - a port gives its peeringLocation and bandwidthInGbps (400).
//
// Not modelled: a provider provisioning the circuit
// (serviceProviderProvisioningState stays NotProvisioned), BGP sessions
// coming up, ARP and route tables, the SKU and bandwidth rules (Local,
// Standard, Premium; what a port's bandwidth allows), Microsoft peering's
// prefix validation and route filters, MACsec on port links, deleting an
// authorization that is in use, the hours a real circuit or port takes, and
// the error codes (this fake's own).
var (
	erCircuit          = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/expressroutecircuits/[^/]+$`)
	erAuthorization    = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/(expressroutecircuits|expressrouteports)/[^/]+/authorizations/[^/]+$`)
	erPeering          = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/expressroutecircuits/[^/]+/peerings/[^/]+$`)
	erGlobalReach      = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/expressroutecircuits/[^/]+/peerings/[^/]+/connections/[^/]+$`)
	erPort             = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.network/expressrouteports/[^/]+$`)
	erSubscriptionPart = regexp.MustCompile(`(?i)^/subscriptions/([^/]+)/`)
)

// erChildLists are the child collections a parent's properties list:
// the JSON name of the list and the type segment of the children.
var erChildLists = map[*regexp.Regexp][]string{
	erCircuit: {"peerings", "authorizations"},
	erPeering: {"connections"},
}

// checkExpressRoute refuses an ExpressRoute resource the rules above do not
// allow; otherwise it sets the keys Azure makes and returns what to do once
// the resource is stored (make the children what the lists say). code is ""
// when it is allowed or id is no ExpressRoute resource.
func (a *ARM) checkExpressRoute(id string, body map[string]any) (status int, code, message string, after func()) {
	props, _ := body["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		body["properties"] = props
	}
	old := map[string]any{}
	if r, ok := a.resources[strings.ToLower(id)]; ok {
		old, _ = (*r)["properties"].(map[string]any)
	}
	switch {
	case erCircuit.MatchString(id):
		if _, ok := body["sku"].(map[string]any); !ok {
			return http.StatusBadRequest, "ExpressRouteCircuitSkuMissing", "An ExpressRoute circuit needs its sku.", nil
		}
		port := refID(props, "expressRoutePort")
		_, provider := props["serviceProviderProperties"].(map[string]any)
		switch {
		case port != "":
			if !a.exists(port) {
				return http.StatusNotFound, "ResourceNotFound", "The ExpressRoute port " + port + " was not found.", nil
			}
			if _, ok := props["bandwidthInGbps"]; !ok {
				return http.StatusBadRequest, "ExpressRouteCircuitBandwidthMissing", "A circuit on an ExpressRoute port needs bandwidthInGbps.", nil
			}
			if !sameSubscription(id, port) || props["authorizationKey"] != nil {
				auth := a.redeem(port, props["authorizationKey"], id, "circuitResourceUri")
				if auth == "" {
					return http.StatusBadRequest, "ExpressRouteAuthorizationKeyInvalid", "A circuit on the ExpressRoute port " + port + " in another subscription needs one of the port's authorization keys.", nil
				}
			}
		case !provider:
			return http.StatusBadRequest, "ExpressRouteCircuitProviderMissing", "An ExpressRoute circuit needs serviceProviderProperties or an expressRoutePort.", nil
		}
		props["serviceKey"] = keep(old, "serviceKey")
		if _, ok := props["serviceProviderProvisioningState"]; !ok {
			props["serviceProviderProvisioningState"] = "NotProvisioned"
		}
		props["circuitProvisioningState"] = "Enabled"
	case erAuthorization.MatchString(id):
		props["authorizationKey"] = keep(old, "authorizationKey")
		props["authorizationUseStatus"] = "Available"
		if s, ok := old["authorizationUseStatus"]; ok {
			props["authorizationUseStatus"] = s
		}
	case erPeering.MatchString(id):
		kind, _ := props["peeringType"].(string)
		if kind != "AzurePrivatePeering" && kind != "MicrosoftPeering" {
			return http.StatusBadRequest, "ExpressRoutePeeringTypeInvalid", "A peering's peeringType is AzurePrivatePeering or MicrosoftPeering.", nil
		}
		if !strings.EqualFold(lastSegment(id), kind) {
			return http.StatusBadRequest, "ExpressRoutePeeringNameInvalid", "A peering of type " + kind + " is named " + kind + ".", nil
		}
		for _, p := range []string{"vlanId", "peerASN", "primaryPeerAddressPrefix", "secondaryPeerAddressPrefix"} {
			if _, ok := props[p]; !ok {
				return http.StatusBadRequest, "ExpressRoutePeeringIncomplete", "A peering needs " + p + ".", nil
			}
		}
		props["state"] = "Enabled"
	case erGlobalReach.MatchString(id):
		own := refID(props, "expressRouteCircuitPeering")
		if !strings.EqualFold(own, parentOf(id)) {
			return http.StatusBadRequest, "ExpressRouteCircuitConnectionPeeringInvalid", "A Global Reach connection names its own peering, " + parentOf(id) + ", as expressRouteCircuitPeering.", nil
		}
		peer := refID(props, "peerExpressRouteCircuitPeering")
		if peer == "" {
			return http.StatusBadRequest, "ExpressRouteCircuitConnectionPeerMissing", "A Global Reach connection needs peerExpressRouteCircuitPeering.", nil
		}
		if !a.exists(peer) {
			return http.StatusNotFound, "ResourceNotFound", "The peer peering " + peer + " was not found.", nil
		}
		if p, _ := props["addressPrefix"].(string); p == "" {
			return http.StatusBadRequest, "ExpressRouteCircuitConnectionPrefixMissing", "A Global Reach connection needs an addressPrefix (a /29).", nil
		}
		if !sameSubscription(id, peer) || props["authorizationKey"] != nil {
			if a.redeem(parentOf(peer), props["authorizationKey"], id, "connectionResourceUri") == "" {
				return http.StatusBadRequest, "ExpressRouteAuthorizationKeyInvalid", "A Global Reach connection to a circuit in another subscription needs one of that circuit's authorization keys.", nil
			}
		}
		props["circuitConnectionStatus"] = "Connected"
	case erPort.MatchString(id):
		if l, _ := props["peeringLocation"].(string); l == "" {
			return http.StatusBadRequest, "ExpressRoutePortIncomplete", "An ExpressRoute port needs its peeringLocation.", nil
		}
		if _, ok := props["bandwidthInGbps"]; !ok {
			return http.StatusBadRequest, "ExpressRoutePortIncomplete", "An ExpressRoute port needs its bandwidthInGbps.", nil
		}
	default:
		return 0, "", "", nil
	}
	for re, lists := range erChildLists {
		if !re.MatchString(id) {
			continue
		}
		wanted := map[string]map[string]any{}
		for _, list := range lists {
			items, _ := props[list].([]any)
			for _, item := range items {
				child, _ := item.(map[string]any)
				name, _ := child["name"].(string)
				if name != "" {
					wanted[strings.ToLower(id+"/"+list+"/"+name)] = child
				}
			}
			delete(props, list)
		}
		return 0, "", "", func() { a.setChildren(id, lists, wanted) }
	}
	return 0, "", "", nil
}

// setChildren makes the children of parent in lists the wanted ones: a child
// left out is deleted with what is under it, a new one is created as sent.
func (a *ARM) setChildren(parent string, lists []string, wanted map[string]map[string]any) {
	pkey := strings.ToLower(parent)
	for k := range a.resources {
		for _, list := range lists {
			prefix := pkey + "/" + strings.ToLower(list) + "/"
			if !strings.HasPrefix(k, prefix) || strings.Contains(k[len(prefix):], "/") {
				continue
			}
			if _, ok := wanted[k]; ok {
				continue
			}
			for k2 := range a.resources {
				if within(k2, k) {
					delete(a.resources, k2)
				}
			}
		}
	}
	for k, child := range wanted {
		if _, ok := a.resources[k]; ok {
			continue
		}
		name, _ := child["name"].(string)
		list := lastSegment(k[:len(k)-len(name)-1])
		id := parent + "/" + list + "/" + name
		r := resource(maps.Clone(child))
		r["id"], r["name"], r["type"] = id, name, typeOf(id)
		a.resources[k] = &r
	}
}

// render is a stored resource as Azure returns it: a circuit or a peering
// with its children's lists rendered from the children.
func (a *ARM) render(id string, r resource) resource {
	for re, lists := range erChildLists {
		if !re.MatchString(id) {
			continue
		}
		out := maps.Clone(r)
		props, _ := r["properties"].(map[string]any)
		props = maps.Clone(props)
		if props == nil {
			props = map[string]any{}
		}
		pkey := strings.ToLower(id)
		for _, list := range lists {
			prefix := pkey + "/" + strings.ToLower(list) + "/"
			var keys []string
			for k := range a.resources {
				if strings.HasPrefix(k, prefix) && !strings.Contains(k[len(prefix):], "/") {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			items := []any{}
			for _, k := range keys {
				child := *a.resources[k]
				items = append(items, map[string]any(a.render(child["id"].(string), child)))
			}
			props[list] = items
		}
		out["properties"] = props
		return out
	}
	return r
}

// redeem finds the authorization of parent (a circuit or a port) whose key
// is key, marks it in use by user (under uriProperty) and returns its ID; ""
// when key is no string, no authorization of parent has it, or another user
// has it in use already.
func (a *ARM) redeem(parent string, key any, user, uriProperty string) string {
	k, _ := key.(string)
	if k == "" {
		return ""
	}
	prefix := strings.ToLower(parent) + "/authorizations/"
	for id, r := range a.resources {
		if !strings.HasPrefix(id, prefix) {
			continue
		}
		props, _ := (*r)["properties"].(map[string]any)
		if props["authorizationKey"] != k {
			continue
		}
		if inUseBy, _ := props[uriProperty].(string); props["authorizationUseStatus"] == "InUse" && !strings.EqualFold(inUseBy, user) {
			return ""
		}
		props["authorizationUseStatus"] = "InUse"
		props[uriProperty] = user
		return id
	}
	return ""
}

// AuthorizationKey returns the key Azure made for an ExpressRoute circuit or
// port authorization, or a circuit's service key, for tests.
func (a *ARM) AuthorizationKey(id string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.resources[strings.ToLower(id)]
	if !ok {
		return ""
	}
	props, _ := (*r)["properties"].(map[string]any)
	name := "serviceKey"
	if erAuthorization.MatchString(id) {
		name = "authorizationKey"
	}
	k, _ := props[name].(string)
	return k
}

// keep is old's value of a key Azure makes, or a new one.
func keep(old map[string]any, name string) string {
	if k, ok := old[name].(string); ok && k != "" {
		return k
	}
	return uuid.NewString()
}

// refID is the id of the reference props[name] ({"id": ...}), or "".
func refID(props map[string]any, name string) string {
	ref, _ := props[name].(map[string]any)
	id, _ := ref["id"].(string)
	return id
}

func sameSubscription(a, b string) bool {
	ma, mb := erSubscriptionPart.FindStringSubmatch(a), erSubscriptionPart.FindStringSubmatch(b)
	return ma != nil && mb != nil && strings.EqualFold(ma[1], mb[1])
}
