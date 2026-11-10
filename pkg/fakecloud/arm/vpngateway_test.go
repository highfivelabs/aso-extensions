// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// What a Virtual WAN VPN gateway, its connections and NAT rules, a VPN site,
// a VPN server configuration and a point-to-site gateway must give; the
// gateway's children kept or deleted by its PUT; keys kept but not returned.
func TestVPNGateway(t *testing.T) {
	a := New(func(string) bool { return true })
	put := func(what, path, body string, want int) (map[string]any, string) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
		out := map[string]any{}
		_ = json.Unmarshal([]byte(resp), &out)
		return out, resp
	}
	rg := "/subscriptions/" + sub1 + "/resourceGroups/net"
	put("resource group", rg, `{"location":"westus"}`, http.StatusCreated)
	nw := rg + "/providers/Microsoft.Network/"
	wan := nw + "virtualWans/wan"
	put("WAN", wan, `{"location":"westus","properties":{}}`, http.StatusCreated)
	hub := nw + "virtualHubs/hub"
	put("hub", hub, `{"location":"westus","properties":{"virtualWan":{"id":"`+wan+`"},"addressPrefix":"10.0.0.0/24"}}`, http.StatusCreated)

	// VPN sites.
	site := nw + "vpnSites/branch"
	siteBody := func(wanID, extra string) string {
		return `{"location":"westus","properties":{"virtualWan":{"id":"` + wanID + `"}` + extra + `}}`
	}
	put("site without a WAN", site, `{"location":"westus","properties":{"ipAddress":"203.0.113.1"}}`, http.StatusBadRequest)
	put("site in a missing WAN", site, siteBody(wan+"-nope", `,"ipAddress":"203.0.113.1"`), http.StatusNotFound)
	put("site without an address", site, siteBody(wan, ""), http.StatusBadRequest)
	put("site link without an address", site, siteBody(wan, `,"vpnSiteLinks":[{"name":"isp1","properties":{}}]`), http.StatusBadRequest)
	put("site", site, siteBody(wan, `,"vpnSiteLinks":[{"name":"isp1","properties":{"ipAddress":"203.0.113.1"}}]`), http.StatusCreated)

	// VPN gateways: one per hub, holding connections and NAT rules.
	gw := nw + "vpnGateways/gw"
	put("gateway without a hub", gw, `{"location":"westus","properties":{}}`, http.StatusBadRequest)
	put("gateway in a missing hub", gw, `{"location":"westus","properties":{"virtualHub":{"id":"`+hub+`-nope"}}}`, http.StatusNotFound)
	gwBody := `{"location":"westus","properties":{"virtualHub":{"id":"` + hub + `"}}}`
	put("gateway", gw, gwBody, http.StatusCreated)
	put("second gateway in the hub", nw+"vpnGateways/again", gwBody, http.StatusBadRequest)

	rule := gw + "/natRules/egress"
	put("NAT rule of an unknown mode", rule, `{"properties":{"mode":"Both","internalMappings":[{"addressSpace":"10.0.0.0/24"}],"externalMappings":[{"addressSpace":"100.64.0.0/24"}]}}`, http.StatusBadRequest)
	put("NAT rule without mappings", rule, `{"properties":{"mode":"EgressSnat"}}`, http.StatusBadRequest)
	if r, _ := put("NAT rule", rule, `{"properties":{"mode":"EgressSnat","internalMappings":[{"addressSpace":"10.0.0.0/24"}],"externalMappings":[{"addressSpace":"100.64.0.0/24"}]}}`, http.StatusCreated); r["properties"].(map[string]any)["type"] != "Static" {
		t.Fatalf("a NAT rule is Static when its type is left out: %v", r)
	}

	conn := gw + "/vpnConnections/branch"
	connBody := func(siteID, link string) string {
		return `{"properties":{"remoteVpnSite":{"id":"` + siteID + `"},"sharedKey":"psk-conn","vpnLinkConnections":[{"name":"isp1","properties":{"vpnSiteLink":{"id":"` + link + `"},"sharedKey":"psk-link"}}]}}`
	}
	put("connection without a site", conn, `{"properties":{}}`, http.StatusBadRequest)
	put("connection to a missing site", conn, connBody(site+"-nope", site+"-nope/vpnSiteLinks/isp1"), http.StatusNotFound)
	put("link connection to another site's link", conn, connBody(site, nw+"vpnSites/other/vpnSiteLinks/isp1"), http.StatusBadRequest)
	put("link connection to a missing link", conn, connBody(site, site+"/vpnSiteLinks/isp9"), http.StatusNotFound)
	_, resp := put("connection", conn, connBody(site, site+"/vpnSiteLinks/isp1"), http.StatusCreated)
	if strings.Contains(resp, "psk-") {
		t.Fatalf("a connection's keys are not returned: %s", resp)
	}
	if k, ok := a.SharedKey(conn); !ok || k != "psk-conn" {
		t.Fatalf("connection key %q %v", k, ok)
	}
	if k, ok := a.SharedKey(conn + "/vpnLinkConnections/isp1"); !ok || k != "psk-link" {
		t.Fatalf("link connection key %q %v", k, ok)
	}

	// The gateway lists its children; a PUT with them keeps them, one
	// without deletes them.
	code, got := do(t, a, http.MethodGet, gw, "")
	expect(t, "GET gateway", code, got, http.StatusOK)
	g := map[string]any{}
	_ = json.Unmarshal([]byte(got), &g)
	props := g["properties"].(map[string]any)
	if c, _ := props["connections"].([]any); len(c) != 1 {
		t.Fatalf("gateway connections: %v", props)
	}
	if n, _ := props["natRules"].([]any); len(n) != 1 {
		t.Fatalf("gateway NAT rules: %v", props)
	}
	keep, _ := json.Marshal(g)
	put("gateway with its children", gw, string(keep), http.StatusOK)
	if !a.HasResource(conn) || !a.HasResource(rule) {
		t.Fatal("a gateway PUT with its children removed one")
	}
	if _, ok := a.SharedKey(conn); !ok {
		t.Fatal("a gateway PUT with its connection lost the connection's key")
	}
	put("gateway without its children", gw, gwBody, http.StatusOK)
	if a.HasResource(conn) || a.HasResource(rule) {
		t.Fatal("a gateway PUT without its children kept one")
	}

	// VPN server configurations and point-to-site gateways.
	cfg := nw + "vpnServerConfigurations/users"
	put("configuration without authentication", cfg, `{"location":"westus","properties":{}}`, http.StatusBadRequest)
	put("certificate configuration without roots", cfg, `{"location":"westus","properties":{"vpnAuthenticationTypes":["Certificate"]}}`, http.StatusBadRequest)
	put("RADIUS configuration without a server", cfg, `{"location":"westus","properties":{"vpnAuthenticationTypes":["Radius"]}}`, http.StatusBadRequest)
	put("AAD configuration without a tenant", cfg, `{"location":"westus","properties":{"vpnAuthenticationTypes":["AAD"],"aadAuthenticationParameters":{}}}`, http.StatusBadRequest)
	_, resp = put("configuration", cfg, `{"location":"westus","properties":{"vpnAuthenticationTypes":["Radius","AAD"],"radiusServerAddress":"10.0.0.10","radiusServerSecret":"radius-1","aadAuthenticationParameters":{"aadTenant":"https://login.microsoftonline.com/t"}}}`, http.StatusCreated)
	if strings.Contains(resp, "radius-1") {
		t.Fatalf("a RADIUS secret is not returned: %s", resp)
	}
	if k, _ := a.SharedKey(cfg + "/radiusServerSecret"); k != "radius-1" {
		t.Fatalf("RADIUS secret %q", k)
	}

	p2s := nw + "p2svpnGateways/users"
	p2sBody := func(cfgID, pool string) string {
		return `{"location":"westus","properties":{"virtualHub":{"id":"` + hub + `"},"vpnServerConfiguration":{"id":"` + cfgID + `"},"p2SConnectionConfigurations":[{"name":"c1","properties":{"vpnClientAddressPool":{"addressPrefixes":[` + pool + `]}}}]}}`
	}
	put("point-to-site gateway without a configuration", p2s, `{"location":"westus","properties":{"virtualHub":{"id":"`+hub+`"}}}`, http.StatusBadRequest)
	put("point-to-site gateway with a missing configuration", p2s, p2sBody(cfg+"-nope", `"172.16.0.0/24"`), http.StatusNotFound)
	put("point-to-site gateway without a client pool", p2s, p2sBody(cfg, ""), http.StatusBadRequest)
	put("point-to-site gateway", p2s, p2sBody(cfg, `"172.16.0.0/24"`), http.StatusCreated)
	put("second point-to-site gateway in the hub", nw+"p2svpnGateways/again", p2sBody(cfg, `"172.16.1.0/24"`), http.StatusBadRequest)

	code, resp = do(t, a, http.MethodDelete, cfg, "")
	expect(t, "DELETE configuration", code, resp, http.StatusOK)
	if _, ok := a.SharedKey(cfg + "/radiusServerSecret"); ok {
		t.Fatal("a deleted configuration's RADIUS secret is kept")
	}
}
