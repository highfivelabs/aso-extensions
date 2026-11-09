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
// too. Locks themselves can always be changed and removed. It accepts a
// request only when Authorize accepts its bearer token, so a test decides
// which identity may use it.
package arm

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
)

const locksType = "Microsoft.Authorization/locks"

// LockProperties are a lock's properties.
type LockProperties struct {
	Level string `json:"level"`
	Notes string `json:"notes,omitempty"`
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
	// Log records every request: "METHOD path status".
	Log []string
}

// New returns an empty fake Resource Manager.
func New(authorize func(token string) bool) *ARM {
	return &ARM{Authorize: authorize, resources: map[string]*resource{}, locks: map[string]*lock{}}
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
		writeJSON(w, http.StatusOK, res)
	case http.MethodPut:
		if l := a.lockOver(id, "ReadOnly"); l != nil {
			writeScopeLocked(w, r.Method, id, l)
			return
		}
		parent := parentOf(id)
		if parent != "" && !subscriptionScope.MatchString(parent) && !a.exists(parent) {
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
		props, _ := body["properties"].(map[string]any)
		if props == nil {
			props = map[string]any{}
		}
		props["provisioningState"] = "Succeeded"
		body["properties"] = props
		status := http.StatusOK
		if _, ok := a.resources[key]; !ok {
			status = http.StatusCreated
		}
		res := resource(body)
		a.resources[key] = &res
		writeJSON(w, status, res)
	case http.MethodDelete:
		if _, ok := a.resources[key]; !ok {
			w.WriteHeader(http.StatusNoContent)
			return
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
		w.WriteHeader(http.StatusOK)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
	}
}

// exists is whether a lock's scope is there: a subscription always is.
func (a *ARM) exists(scope string) bool {
	if subscriptionScope.MatchString(scope) {
		return true
	}
	_, ok := a.resources[strings.ToLower(scope)]
	return ok
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
