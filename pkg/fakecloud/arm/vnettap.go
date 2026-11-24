// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"maps"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// Virtual network TAP (Microsoft.Network/virtualNetworkTaps and
// networkInterfaces/tapConfigurations, 2026-03-01; docs/plan.md f60), as far
// as the fake models it. Each rule is one the spec or Azure's documentation
// of virtual network TAP states:
//
//   - a TAP sends the mirrored traffic to one destination: a collector
//     NIC's IP configuration or an internal load balancer's frontend IP
//     configuration, not both and not neither (400), which exists (404);
//   - its destinationPort, the VXLAN port, is 1 to 65535 (400);
//   - a NIC's tap configuration names a TAP (400) that exists (404);
//   - a TAP lists, read-only, the tap configurations that name it
//     (networkInterfaceTapConfigurations, by ID), rendered from them.
//
// Not modelled: the traffic itself, a destination being in the TAP's or a
// peered virtual network, the VM sizes and NIC kinds that can be tapped,
// whether Azure refuses deleting a TAP that tap configurations still name,
// and the error codes (the fake's own).
var (
	vnetTap      = regexp.MustCompile(`(?i)/providers/microsoft\.network/virtualnetworktaps/[^/]+$`)
	nicTapConfig = regexp.MustCompile(`(?i)/providers/microsoft\.network/networkinterfaces/[^/]+/tapconfigurations/[^/]+$`)
)

// checkVirtualNetworkTap refuses a TAP or a tap configuration the rules
// above do not allow; code is "" when it is allowed or id is neither.
func (a *ARM) checkVirtualNetworkTap(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	switch {
	case vnetTap.MatchString(id):
		bad := func(msg string) (int, string, string) {
			return http.StatusBadRequest, "InvalidVirtualNetworkTap", msg
		}
		nic := refID(props, "destinationNetworkInterfaceIPConfiguration")
		lb := refID(props, "destinationLoadBalancerFrontEndIPConfiguration")
		if (nic == "") == (lb == "") {
			return bad("A virtual network TAP has one destination: a network interface IP configuration or a load balancer frontend IP configuration.")
		}
		dest := nic + lb
		if !a.exists(dest) {
			return http.StatusNotFound, "ResourceNotFound", "The destination " + dest + " was not found."
		}
		if p, ok := props["destinationPort"]; ok {
			if n, _ := p.(float64); n < 1 || n > 65535 || n != float64(int(n)) {
				return bad("A virtual network TAP's destinationPort is 1 to 65535.")
			}
		}
	case nicTapConfig.MatchString(id):
		tap := refID(props, "virtualNetworkTap")
		if tap == "" {
			return http.StatusBadRequest, "InvalidTapConfiguration", "A tap configuration names a virtual network TAP."
		}
		if !vnetTap.MatchString(tap) || !a.exists(tap) {
			return http.StatusNotFound, "ResourceNotFound", "The virtual network TAP " + tap + " was not found."
		}
	}
	return 0, "", ""
}

// renderVirtualNetworkTap is a TAP with networkInterfaceTapConfigurations
// listing the tap configurations that name it; ok is false when id is no
// TAP.
func (a *ARM) renderVirtualNetworkTap(id string, r resource) (resource, bool) {
	if !vnetTap.MatchString(id) {
		return nil, false
	}
	var ids []string
	for k, c := range a.resources {
		if !nicTapConfig.MatchString(k) {
			continue
		}
		props, _ := (*c)["properties"].(map[string]any)
		if strings.EqualFold(refID(props, "virtualNetworkTap"), id) {
			ids = append(ids, (*c)["id"].(string))
		}
	}
	sort.Strings(ids)
	items := []any{}
	for _, c := range ids {
		items = append(items, map[string]any{"id": c})
	}
	out := maps.Clone(r)
	props, _ := r["properties"].(map[string]any)
	props = maps.Clone(props)
	if props == nil {
		props = map[string]any{}
	}
	props["networkInterfaceTapConfigurations"] = items
	out["properties"] = props
	return out, true
}
