// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"slices"
	"strings"
)

// Web PubSub services and their children, and a SignalR service's shared
// private link resources (Microsoft.SignalRService, 2024-03-01;
// docs/plan.md f68), as far as the fake models them. Each rule is one the
// spec or Azure's Web PubSub and SignalR documentation states:
//
//   - a Web PubSub service's SKU is Free_F1, Standard_S1, Premium_P1 or
//     Premium_P2 (400);
//   - a replica is of a Premium service, in a Premium SKU, in a region other
//     than the service's (400);
//   - a hub's name starts with a letter and has only letters, digits and
//     underscores (400);
//   - a custom certificate names its Key Vault and secret (400), and a
//     custom domain names a domain (400) and a custom certificate of the
//     same service that exists (404);
//   - a shared private link resource names a groupId (400) and a resource
//     that exists (404).
//
// The services' private endpoint connections are privateendpoints.go's. Not
// modelled: clients connecting, event handlers being called, the tiers that
// take custom domains and private links, and the error codes (the fake's
// own).
var (
	wpsService   = regexp.MustCompile(`(?i)/providers/microsoft\.signalrservice/webpubsub/[^/]+$`)
	wpsReplica   = regexp.MustCompile(`(?i)^(.*/providers/microsoft\.signalrservice/(?:webpubsub|signalr)/[^/]+)/replicas/[^/]+$`)
	wpsHub       = regexp.MustCompile(`(?i)/providers/microsoft\.signalrservice/webpubsub/[^/]+/hubs/([^/]+)$`)
	wpsHubName   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	wpsCert      = regexp.MustCompile(`(?i)/providers/microsoft\.signalrservice/(webpubsub|signalr)/[^/]+/customcertificates/[^/]+$`)
	wpsDomain    = regexp.MustCompile(`(?i)^(.*/providers/microsoft\.signalrservice/(?:webpubsub|signalr)/[^/]+)/customdomains/[^/]+$`)
	wpsSharedPLR = regexp.MustCompile(`(?i)/providers/microsoft\.signalrservice/(webpubsub|signalr)/[^/]+(/replicas/[^/]+)?/sharedprivatelinkresources/[^/]+$`)
	wpsSkus      = []string{"Free_F1", "Standard_S1", "Premium_P1", "Premium_P2"}
)

// checkSignalR refuses what the rules above do not allow; code is "" when it
// is allowed or id is none of these.
func (a *ARM) checkSignalR(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	bad := func(msg string) (int, string, string) {
		return http.StatusBadRequest, "BadRequest", msg
	}
	skuName := func(r map[string]any) string {
		sku, _ := r["sku"].(map[string]any)
		return stringOf(sku["name"])
	}
	switch {
	case wpsService.MatchString(id):
		if s := skuName(body); !slices.Contains(wpsSkus, s) {
			return bad("A Web PubSub service's SKU is one of " + strings.Join(wpsSkus, ", ") + ", not " + s + ".")
		}
	case wpsReplica.MatchString(id):
		parentID := wpsReplica.FindStringSubmatch(id)[1]
		parent, ok := a.resources[strings.ToLower(parentID)]
		if !ok {
			return 0, "", ""
		}
		if !strings.HasPrefix(skuName(*parent), "Premium") || !strings.HasPrefix(skuName(body), "Premium") {
			return bad("A replica is of a Premium service, in a Premium SKU.")
		}
		if loc, _ := (*parent)["location"].(string); strings.EqualFold(normalizeLocation(loc), normalizeLocation(stringOf(body["location"]))) {
			return bad("A replica is in a region other than its service's, " + loc + ".")
		}
	case wpsHub.MatchString(id):
		if name := wpsHub.FindStringSubmatch(id)[1]; !wpsHubName.MatchString(name) {
			return bad("A hub's name starts with a letter and has only letters, digits and underscores; " + name + " does not.")
		}
	case wpsCert.MatchString(id):
		if stringOf(props["keyVaultBaseUri"]) == "" || stringOf(props["keyVaultSecretName"]) == "" {
			return bad("A custom certificate names its Key Vault and secret.")
		}
	case wpsDomain.MatchString(id):
		if stringOf(props["domainName"]) == "" {
			return bad("A custom domain names its domain.")
		}
		cert := refID(props, "customCertificate")
		service := wpsDomain.FindStringSubmatch(id)[1]
		if cert == "" || !strings.HasPrefix(strings.ToLower(cert), strings.ToLower(service)+"/customcertificates/") || !a.exists(cert) {
			return http.StatusNotFound, "ResourceNotFound", "The custom certificate " + cert + " of " + service + " was not found."
		}
	case wpsSharedPLR.MatchString(id):
		if stringOf(props["groupId"]) == "" {
			return bad("A shared private link resource names its groupId.")
		}
		if target := stringOf(props["privateLinkResourceId"]); target == "" || !a.exists(target) {
			return http.StatusNotFound, "ResourceNotFound", "The private link resource " + target + " was not found."
		}
	}
	return 0, "", ""
}
