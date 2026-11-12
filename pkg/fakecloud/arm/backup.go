// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// A Recovery Services vault's resource guard proxy
// (Microsoft.RecoveryServices/vaults/backupResourceGuardProxies), as far as
// the fake models it: removing the proxy turns multi-user authorization
// off, an operation the guard protects, so Azure refuses its DELETE (400
// BMSUserErrorUnlockPreviligeAccessIsNeeded, real Azure, live run of
// 2026-11-12) until a POST to the proxy's unlockDelete names the proxy as
// resourceToBeDeleted and a deleteResourceGuardProxyRequests request of the
// guard the proxy names (its resourceGuardResourceId). One unlock opens one
// DELETE.
//
// Not modelled: the unlock's expiry, and the guard's own role checks (in
// Azure the caller needs a role on the guard; here anyone may unlock).
var (
	guardProxy        = regexp.MustCompile(`(?i)/providers/microsoft\.recoveryservices/vaults/[^/]+/backupresourceguardproxies/[^/]+$`)
	guardProxyUnlock  = regexp.MustCompile(`(?i)^(.*/providers/microsoft\.recoveryservices/vaults/[^/]+/backupresourceguardproxies/[^/]+)/unlockdelete$`)
	guardProxyRequest = regexp.MustCompile(`(?i)^(.*)/deleteresourceguardproxyrequests/[^/]+$`)
)

// serveUnlockDelete answers a POST to a proxy's unlockDelete; it reports
// whether path was one.
func (a *ARM) serveUnlockDelete(w http.ResponseWriter, r *http.Request, path string) bool {
	m := guardProxyUnlock.FindStringSubmatch(path)
	if m == nil || r.Method != http.MethodPost {
		return false
	}
	proxy := m[1]
	res, ok := a.resources[strings.ToLower(proxy)]
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "The resource guard proxy "+proxy+" was not found.")
		return true
	}
	var body struct {
		Requests []string `json:"resourceGuardOperationRequests"`
		Resource string   `json:"resourceToBeDeleted"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return true
	}
	props, _ := (*res)["properties"].(map[string]any)
	guard, _ := props["resourceGuardResourceId"].(string)
	named := false
	for _, req := range body.Requests {
		if g := guardProxyRequest.FindStringSubmatch(req); g != nil && guard != "" && strings.EqualFold(g[1], guard) {
			named = true
		}
	}
	if !strings.EqualFold(body.Resource, proxy) || !named {
		writeError(w, http.StatusBadRequest, "BMSUserErrorInvalidInput", "unlockDelete names the proxy as resourceToBeDeleted and a deleteResourceGuardProxyRequests request of its resource guard.")
		return true
	}
	a.unlocked[strings.ToLower(proxy)] = true
	writeJSON(w, http.StatusOK, map[string]any{"unlockDeleteExpiryTime": "2099-01-01T00:00:00Z"})
	return true
}

// checkGuardProxyDelete refuses the DELETE of a proxy no unlockDelete has
// opened, and uses up the unlock otherwise; code is "" when it is allowed or
// id is no proxy.
func (a *ARM) checkGuardProxyDelete(id string) (status int, code, message string) {
	if !guardProxy.MatchString(id) {
		return 0, "", ""
	}
	key := strings.ToLower(id)
	if !a.unlocked[key] {
		return http.StatusBadRequest, "BMSUserErrorUnlockPreviligeAccessIsNeeded", "Unlock privilege access is needed to delete the ResourceGuard proxy"
	}
	delete(a.unlocked, key)
	return 0, "", ""
}
