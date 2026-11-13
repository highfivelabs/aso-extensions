// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Package arm is a fake Azure Resource Manager, for tests and test builds
// only: a store of resources by ARM ID (PUT, GET, DELETE of any resource
// whose parent exists, the body kept as sent with id, name, type and a
// provisioningState of Succeeded added; no properties are checked), and
// management locks
// (Microsoft.Authorization/locks, API 2020-05-01) at any scope that
// exists. Locks behave as Azure documents them
// (https://learn.microsoft.com/azure/azure-resource-manager/management/lock-resources):
// a lock applies to its scope and everything under it; CanNotDelete
// refuses a DELETE, ReadOnly refuses a PUT and a DELETE, each with 409
// ScopeLocked; deleting a scope that holds a locked resource is refused
// too. Locks themselves can always be changed and removed.
//
// Tenant scope: the tenant root ("/") always exists, so a resource whose ID
// starts at /providers (a management group, a subscription alias) can be
// created. Management groups (Microsoft.Management/managementGroups) form a
// hierarchy: a group's properties.details.parent.id must name a group that
// exists, a group cannot become its own descendant's child, and a group that
// has child groups or subscriptions cannot be deleted. A subscription is in
// one group at a time: putting it in a group (…/managementGroups/g/subscriptions/s)
// takes it out of the group it was in. The error codes for those refusals are
// this fake's, not Azure's.
//
// Azure Policy definitions, set definitions, assignments and exemptions are
// checked for where they are kept and what they name (policy.go); so are
// policy remediations and attestations (policyinsights.go), and role
// definitions and Privileged Identity Management requests (pim.go: built-in
// roles listed by name, schedule requests that cannot be deleted), budgets
// and cost exports (cost.go: Cost Management scopes, notifications, delivery,
// an obfuscated SAS token), and site-to-site VPN connections and local
// network gateways (vpn.go: the gateways a connection names, a shared key
// kept but not returned), and ExpressRoute circuits, peerings,
// authorizations, Global Reach connections, ports, and gateways in a
// Virtual WAN hub with their connections (expressroute.go: keys Azure makes,
// children a parent's PUT keeps or deletes, authorization keys redeemed
// across subscriptions, one gateway per hub), and managed HSMs, their keys
// and Key Vault private endpoint connections (keyvault.go: what an HSM is
// created with, keys a PUT does not change and Resource Manager does not
// delete, connections a private endpoint makes).
//
// Log Analytics clusters, data sources and storage insight configs are
// checked too (loganalytics.go: commitment tiers, a storage key kept aside).
//
// Operations Management solutions are checked too (operationsmanagement.go:
// the workspace a solution is enabled on, a gallery solution's name and plan).
//
// AKS and fleet managed namespaces and node pool snapshots are checked too
// (containerservice.go: namespace names, the agent pool a snapshot is of).
//
// Container registry tasks and connected registries are checked too
// (containerregistry.go: a task's step and platform, tokens and credentials
// kept but not returned, a connected registry's mode and tokens).
//
// Source control configurations and Kubernetes configuration private link
// scopes are checked too (kubernetesconfiguration.go: the cluster, protected
// settings kept but not returned).
//
// Activity log profiles and workbook templates are checked too (insights.go:
// one log profile per subscription, its destination, a template's galleries).
//
// Properties Azure computes (an address, a principal ID, a login server, a
// vault's URI) are filled in, and children Azure makes with their parent (a
// storage account's default services, a DNS zone's SOA record set) are made
// (computed.go); Key Vault keys are served in the vault's data plane too
// (vaultkeys.go).
//
// It accepts a request only when Authorize accepts its bearer token, so a
// test decides which identity may use it.
package arm

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const locksType = "Microsoft.Authorization/locks"

// LockProperties are a lock's properties.
type LockProperties struct {
	Level  string      `json:"level"`
	Notes  string      `json:"notes,omitempty"`
	Owners []LockOwner `json:"owners,omitempty"`
}

// LockOwner is a lock owner, kept as sent; it changes nothing.
type LockOwner struct {
	ApplicationID string `json:"applicationId,omitempty"`
}

type lock struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Type       string         `json:"type"`
	Properties LockProperties `json:"properties"`
	scope      string
}

// resource is a resource as sent (location, tags, properties, whatever else)
// with id, name and type added.
type resource map[string]any

// ARM is the fake. Use New.
type ARM struct {
	// Authorize decides whether a bearer token may use Resource Manager.
	Authorize func(token string) bool

	mu        sync.Mutex
	resources map[string]*resource // by lower-case ID
	locks     map[string]*lock     // by lower-case lock ID
	// sharedKeys holds connections' shared keys, which are not returned
	// (vpn.go), by lower-case connection ID.
	sharedKeys map[string]string
	// unlocked holds the resource guard proxies an unlockDelete has opened
	// for their next DELETE (backup.go).
	unlocked map[string]bool
	// Log records every request: "METHOD path status".
	Log []string
	// Now is the clock systemData's times come from (time.Now when nil).
	Now func() time.Time
	// Tenant is the tenant ID written into identities and Key Vault's
	// challenge (DefaultTenant when empty; computed.go).
	Tenant string
	// VaultSuffix is the DNS suffix of vaults' data planes (vault.azure.net
	// when empty): a request whose Host is under it is a data-plane request
	// to that vault (vaultkeys.go).
	VaultSuffix string
	// AuthorizeVault decides whether a bearer token may use a vault's data
	// plane; nil refuses every token.
	AuthorizeVault func(token string) bool
	// rotationPolicies holds keys' rotation policies (vaultkeys.go), by
	// lower-case key ID.
	rotationPolicies map[string]map[string]any
}

// New returns an empty fake Resource Manager.
func New(authorize func(token string) bool) *ARM {
	return &ARM{Authorize: authorize, resources: map[string]*resource{}, locks: map[string]*lock{}, sharedKeys: map[string]string{}, unlocked: map[string]bool{}}
}

// AddResource puts a resource (a resource group, or any resource) in the
// store, as if it had been created, for tests.
func (a *ARM) AddResource(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.resources[strings.ToLower(id)] = &resource{"id": id, "name": lastSegment(id), "type": typeOf(id)}
}

// Resource returns the stored resource with the given ARM ID, as JSON, for
// tests.
func (a *ARM) Resource(id string) (map[string]any, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.resources[strings.ToLower(id)]
	if !ok {
		return nil, false
	}
	return *r, true
}

// HasResource is whether the store holds the resource, for tests.
func (a *ARM) HasResource(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.resources[strings.ToLower(id)]
	return ok
}

// Lock returns the lock with the given ARM ID, for tests.
func (a *ARM) Lock(id string) (LockProperties, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	l, ok := a.locks[strings.ToLower(id)]
	if !ok {
		return LockProperties{}, false
	}
	return l.Properties, true
}

// Locks lists the IDs of every lock, sorted, for tests.
func (a *ARM) Locks() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	ids := make([]string, 0, len(a.locks))
	for _, l := range a.locks {
		ids = append(ids, l.ID)
	}
	sort.Strings(ids)
	return ids
}

var (
	subscriptionScope  = regexp.MustCompile(`(?i)^/subscriptions/[^/]+$`)
	resourceGroupScope = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+$`)
	lockPath           = regexp.MustCompile(`(?i)^(.*)/providers/microsoft\.authorization/locks/([^/]+)$`)
	managementGroup    = regexp.MustCompile(`(?i)^/providers/microsoft\.management/managementgroups/[^/]+$`)
	mgSubscription     = regexp.MustCompile(`(?i)^(/providers/microsoft\.management/managementgroups/[^/]+)/subscriptions/([^/]+)$`)
)

type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// ServeHTTP implements Resource Manager.
func (a *ARM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rec := &recorder{ResponseWriter: w, status: http.StatusOK}
	defer func() { a.Log = append(a.Log, r.Method+" "+r.URL.Path+" "+http.StatusText(rec.status)) }()

	if vault := a.vaultOf(r); vault != "" {
		a.serveVault(rec, r, vault)
		return
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || a.Authorize == nil || !a.Authorize(strings.TrimPrefix(auth, "Bearer ")) {
		writeError(rec, http.StatusUnauthorized, "InvalidAuthenticationToken", "fake resource manager: this identity may not use Resource Manager")
		return
	}
	if r.URL.Query().Get("api-version") == "" {
		writeError(rec, http.StatusBadRequest, "MissingApiVersionParameter", "The api-version query parameter (?api-version=) is required for all requests.")
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	if m := lockPath.FindStringSubmatch(path); m != nil {
		a.serveLock(rec, r, m[1], m[2])
		return
	}
	if a.serveRoleDefinitions(rec, r, path) {
		return
	}
	if a.serveAgentRegistration(rec, r, path) {
		return
	}
	if a.serveUnlockDelete(rec, r, path) {
		return
	}
	a.serveResource(rec, r, path)
}

func (a *ARM) serveLock(w http.ResponseWriter, r *http.Request, scope, name string) {
	id := scope + "/providers/" + locksType + "/" + name
	key := strings.ToLower(id)
	switch r.Method {
	case http.MethodGet:
		l, ok := a.locks[key]
		if !ok {
			writeError(w, http.StatusNotFound, "LockNotFound", "The lock "+name+" could not be found.")
			return
		}
		writeJSON(w, http.StatusOK, l)
	case http.MethodPut:
		if !a.exists(scope) {
			writeError(w, http.StatusNotFound, "ResourceNotFound", "The resource "+scope+" was not found.")
			return
		}
		var body struct {
			Properties LockProperties `json:"properties"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
			return
		}
		if body.Properties.Level != "CanNotDelete" && body.Properties.Level != "ReadOnly" {
			writeError(w, http.StatusBadRequest, "LockLevelInvalid", "The lock level must be CanNotDelete or ReadOnly.")
			return
		}
		status := http.StatusOK
		if _, ok := a.locks[key]; !ok {
			status = http.StatusCreated
		}
		l := &lock{ID: id, Name: name, Type: locksType, Properties: body.Properties, scope: scope}
		a.locks[key] = l
		writeJSON(w, status, l)
	case http.MethodDelete:
		if _, ok := a.locks[key]; !ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		delete(a.locks, key)
		w.WriteHeader(http.StatusOK)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
	}
}

func (a *ARM) serveResource(w http.ResponseWriter, r *http.Request, id string) {
	key := strings.ToLower(id)
	switch r.Method {
	case http.MethodGet:
		res, ok := a.resources[key]
		if !ok {
			writeError(w, http.StatusNotFound, "ResourceNotFound", "The resource "+id+" was not found.")
			return
		}
		writeJSON(w, http.StatusOK, a.render(id, *res))
	case http.MethodPut:
		if l := a.lockOver(id, "ReadOnly"); l != nil {
			writeScopeLocked(w, r.Method, id, l)
			return
		}
		parent := parentOf(id)
		if parent != "" && !a.exists(parent) {
			code := "ParentResourceNotFound"
			if resourceGroupScope.MatchString(parent) {
				code = "ResourceGroupNotFound"
			}
			writeError(w, http.StatusNotFound, code, "The parent resource "+parent+" of "+id+" was not found.")
			return
		}
		body := map[string]any{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
			return
		}
		body["id"], body["name"], body["type"] = id, lastSegment(id), typeOf(id)
		if managementGroup.MatchString(id) {
			if code, msg := a.checkGroupParent(id, body); code != "" {
				writeError(w, http.StatusBadRequest, code, msg)
				return
			}
		}
		if status, code, msg := a.checkPolicy(id, body); code != "" {
			writeError(w, status, code, msg)
			return
		}
		if status, code, msg := a.checkCost(id, body); code != "" {
			writeError(w, status, code, msg)
			return
		}
		status, code, msg, after := a.checkPIM(id, body)
		if code != "" {
			writeError(w, status, code, msg)
			return
		}
		if status, code, msg := a.checkMaintenance(id, body); code != "" {
			writeError(w, status, code, msg)
			return
		}
		if status, code, msg := a.checkLogAnalytics(id, body); code != "" {
			writeError(w, status, code, msg)
			return
		}
		if status, code, msg := a.checkOperationsManagement(id, body); code != "" {
			writeError(w, status, code, msg)
			return
		}
		if status, code, msg := a.checkContainerService(id, body); code != "" {
			writeError(w, status, code, msg)
			return
		}
		if status, code, msg := a.checkContainerRegistry(id, body); code != "" {
			writeError(w, status, code, msg)
			return
		}
		if status, code, msg := a.checkKubernetesConfiguration(id, body); code != "" {
			writeError(w, status, code, msg)
			return
		}
		if status, code, msg := a.checkDNS(id, body); code != "" {
			writeError(w, status, code, msg)
			return
		}
		if status, code, msg := a.checkFilters(id, body); code != "" {
			writeError(w, status, code, msg)
			return
		}
		if status, code, msg := a.checkInsights(id, body); code != "" {
			writeError(w, status, code, msg)
			return
		}
		if status, code, msg := a.checkVPN(id, body); code != "" {
			writeError(w, status, code, msg)
			return
		}
		if status, code, msg := a.checkVirtualWAN(id, body); code != "" {
			writeError(w, status, code, msg)
			return
		}
		if status, code, msg := a.checkVPNGateway(id, body); code != "" {
			writeError(w, status, code, msg)
			return
		}
		if status, code, msg := a.checkNetworkManager(id, body); code != "" {
			writeError(w, status, code, msg)
			return
		}
		status, code, msg, erAfter := a.checkExpressRoute(id, body)
		if code != "" {
			writeError(w, status, code, msg)
			return
		}
		status, code, msg, unchanged := a.checkKeyVault(id, body)
		if code != "" {
			writeError(w, status, code, msg)
			return
		}
		if unchanged != nil {
			writeJSON(w, http.StatusOK, a.render(id, *unchanged))
			return
		}
		if m := mgSubscription.FindStringSubmatch(id); m != nil {
			a.moveSubscription(m[2], key)
		}
		props, _ := body["properties"].(map[string]any)
		if props == nil {
			props = map[string]any{}
		}
		props["provisioningState"] = "Succeeded"
		body["properties"] = props
		a.compute(id, body, props)
		status = http.StatusOK
		if _, ok := a.resources[key]; !ok {
			status = http.StatusCreated
		}
		a.setSystemData(key, body)
		res := resource(body)
		a.resources[key] = &res
		a.makeChildren(id, body)
		if after != nil {
			after()
		}
		if erAfter != nil {
			erAfter()
		}
		writeJSON(w, status, a.render(id, res))
	case http.MethodDelete:
		if isScheduleRequest(id) {
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "A schedule request cannot be deleted; undo it with another request (AdminRemove, SelfDeactivate).")
			return
		}
		if vaultKey.MatchString(id) {
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "A key vault key cannot be deleted through Resource Manager; delete it in the vault's data plane.")
			return
		}
		if isManagedHSMKey(id) {
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "A managed HSM key cannot be deleted through Resource Manager; delete it in the HSM's data plane.")
			return
		}
		if _, ok := a.resources[key]; !ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if status, code, msg := a.checkGuardProxyDelete(id); code != "" {
			writeError(w, status, code, msg)
			return
		}
		if soaDeleteRefused(id) {
			writeError(w, http.StatusBadRequest, "BadRequest", "A zone's SOA record set cannot be deleted; it is deleted with its zone.")
			return
		}
		if managementGroup.MatchString(id) {
			if child := a.groupChild(key); child != "" {
				writeError(w, http.StatusConflict, "ManagementGroupNotEmpty", "The management group "+id+" has a child, "+child+", and cannot be deleted.")
				return
			}
		}
		if l := a.lockOver(id, ""); l != nil {
			writeScopeLocked(w, r.Method, id, l)
			return
		}
		if l := a.lockUnder(id); l != nil {
			writeScopeLocked(w, r.Method, id, l)
			return
		}
		for k := range a.resources {
			if within(k, key) {
				delete(a.resources, k)
			}
		}
		for k := range a.sharedKeys {
			if within(k, key) {
				delete(a.sharedKeys, k)
			}
		}
		w.WriteHeader(http.StatusOK)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
	}
}

// exists is whether a scope is there: the tenant root and a subscription
// always are.
func (a *ARM) exists(scope string) bool {
	if scope == "/" || subscriptionScope.MatchString(scope) {
		return true
	}
	_, ok := a.resources[strings.ToLower(scope)]
	return ok
}

// groupParent is the ID of the parent group a management group's body names
// (properties.details.parent.id), or "".
func groupParent(r resource) string {
	props, _ := r["properties"].(map[string]any)
	details, _ := props["details"].(map[string]any)
	parent, _ := details["parent"].(map[string]any)
	id, _ := parent["id"].(string)
	return id
}

// checkGroupParent refuses a management group whose named parent does not
// exist or would make a cycle.
func (a *ARM) checkGroupParent(id string, body map[string]any) (code, message string) {
	parent := groupParent(resource(body))
	if parent == "" {
		return "", ""
	}
	if !managementGroup.MatchString(parent) {
		return "InvalidParentGroup", "The parent " + parent + " is not a management group ID."
	}
	if _, ok := a.resources[strings.ToLower(parent)]; !ok {
		return "ParentGroupNotFound", "The parent management group " + parent + " was not found."
	}
	// Walk up from the parent: meeting id is a cycle.
	seen := map[string]bool{}
	for cur := strings.ToLower(parent); cur != "" && !seen[cur]; {
		if cur == strings.ToLower(id) {
			return "CycleInHierarchy", "The management group " + id + " cannot be placed under " + parent + ", its own descendant."
		}
		seen[cur] = true
		r, ok := a.resources[cur]
		if !ok {
			break
		}
		cur = strings.ToLower(groupParent(*r))
	}
	return "", ""
}

// groupChild is the ID of a child of the management group with the given
// lower-case ID (a group naming it as its parent, or a subscription placed
// in it), or "".
func (a *ARM) groupChild(key string) string {
	for k, r := range a.resources {
		if managementGroup.MatchString(k) && strings.ToLower(groupParent(*r)) == key {
			return (*r)["id"].(string)
		}
		if m := mgSubscription.FindStringSubmatch(k); m != nil && strings.ToLower(m[1]) == key {
			return (*r)["id"].(string)
		}
	}
	return ""
}

// moveSubscription takes a subscription out of every group but the one whose
// placement key is given (a subscription is in one group at a time).
func (a *ARM) moveSubscription(subscription, keep string) {
	for k := range a.resources {
		if m := mgSubscription.FindStringSubmatch(k); m != nil && strings.EqualFold(m[2], subscription) && k != keep {
			delete(a.resources, k)
		}
	}
}

// lockOver is a lock on id or a scope above it, of the given level (any
// level when empty).
func (a *ARM) lockOver(id, level string) *lock {
	for _, l := range a.sortedLocks() {
		if within(strings.ToLower(id), strings.ToLower(l.scope)) && (level == "" || l.Properties.Level == level) {
			return l
		}
	}
	return nil
}

// lockUnder is a lock on a scope below id.
func (a *ARM) lockUnder(id string) *lock {
	for _, l := range a.sortedLocks() {
		if within(strings.ToLower(l.scope), strings.ToLower(id)) {
			return l
		}
	}
	return nil
}

func (a *ARM) sortedLocks() []*lock {
	out := make([]*lock, 0, len(a.locks))
	for _, l := range a.locks {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// within is whether id is scope or under it (lower-case IDs).
func within(id, scope string) bool {
	return id == scope || strings.HasPrefix(id, scope+"/")
}

func lastSegment(id string) string {
	return id[strings.LastIndex(id, "/")+1:]
}

// parentOf is the ID without its last two segments (a type and a name): the
// resource group of a resource, the subscription of a resource group.
func parentOf(id string) string {
	parts := strings.Split(strings.Trim(id, "/"), "/")
	if len(parts) < 3 {
		return ""
	}
	// A resource under providers/: .../providers/NS/type/name has the
	// resource group (everything before /providers/) as its parent.
	for i, p := range parts {
		if strings.EqualFold(p, "providers") && i+4 == len(parts) {
			return "/" + strings.Join(parts[:i], "/")
		}
	}
	return "/" + strings.Join(parts[:len(parts)-2], "/")
}

// typeOf is the ARM type of a resource ID: Microsoft.App/managedEnvironments
// for .../providers/Microsoft.App/managedEnvironments/env, with every type
// segment of a child (…/daprComponents/name) appended; resourceGroups for a
// resource group.
func typeOf(id string) string {
	parts := strings.Split(strings.Trim(id, "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if strings.EqualFold(parts[i], "providers") {
			rest := parts[i+1:]
			if len(rest) < 3 {
				break
			}
			types := []string{rest[0]}
			for j := 1; j < len(rest); j += 2 {
				types = append(types, rest[j])
			}
			return strings.Join(types, "/")
		}
	}
	if len(parts) >= 4 && strings.EqualFold(parts[2], "resourcegroups") {
		return "Microsoft.Resources/resourceGroups"
	}
	return ""
}

func writeScopeLocked(w http.ResponseWriter, method, id string, l *lock) {
	writeError(w, http.StatusConflict, "ScopeLocked",
		"The scope '"+id+"' cannot perform "+strings.ToLower(method)+" operation because following scope(s) are locked: '"+l.scope+"'. Please remove the lock and try again.")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// setSystemData sets body's systemData as Resource Manager does: createdAt
// when the resource is made, kept by every later PUT (whatever the body
// says), and lastModifiedAt on each PUT.
func (a *ARM) setSystemData(key string, body map[string]any) {
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	stamp := now().UTC().Format(time.RFC3339)
	created := stamp
	if old, ok := a.resources[key]; ok {
		if sd, ok := (*old)["systemData"].(map[string]any); ok {
			if c, ok := sd["createdAt"].(string); ok {
				created = c
			}
		}
	}
	body["systemData"] = map[string]any{"createdAt": created, "lastModifiedAt": stamp}
}
