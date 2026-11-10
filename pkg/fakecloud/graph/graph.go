// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Package graph is a fake Microsoft Graph, for tests and test builds only:
// Microsoft Entra directory role assignments
// (/roleManagement/directory/roleAssignments, beta and v1.0): create, get,
// list with a $filter on principalId and roleDefinitionId, and delete. A
// directory of principals (users, groups, service principals by object
// ID), role definitions (the built-in ones below, and any a test adds) and
// administrative units decides what may be assigned. It accepts a request
// only when Authorize accepts its bearer token, so a test decides which
// identity may use Graph. Request bodies may be gzipped (Content-Encoding:
// gzip), as the Graph SDK's default middleware sends them. The shapes follow
// https://learn.microsoft.com/graph/api/rbacapplication-post-roleassignments.
package graph

import (
	"compress/gzip"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Built-in directory role template IDs the fake knows from the start (their
// IDs are the same in every tenant).
const (
	GlobalReader      = "f2ef992c-3afb-46b9-b7cf-a126ee74c451"
	UserAdministrator = "fe930be7-5e62-47db-91af-98c3a49a38b1"
	SecurityReader    = "5d6b6bb7-de71-4623-b4af-96380a352509"
	DirectoryReaders  = "88d8e3e3-8f55-4a1e-953a-9b9898b8876b"
)

type assignment struct {
	OdataType        string  `json:"@odata.type,omitempty"`
	ID               string  `json:"id"`
	PrincipalID      string  `json:"principalId"`
	RoleDefinitionID string  `json:"roleDefinitionId"`
	DirectoryScopeID *string `json:"directoryScopeId"`
	AppScopeID       *string `json:"appScopeId"`
	Condition        *string `json:"condition,omitempty"`
}

// Graph is the fake. Its zero value is not usable; use New.
type Graph struct {
	// Authorize decides whether a bearer token may use Graph.
	Authorize func(token string) bool

	mu              sync.Mutex
	principals      map[string]bool
	roleDefinitions map[string]bool
	adminUnits      map[string]bool
	assignments     map[string]*assignment
	// Log records every request: "METHOD path".
	Log []string
}

// New returns a fake Graph with the built-in roles above and no principals.
func New(authorize func(token string) bool) *Graph {
	g := &Graph{Authorize: authorize, principals: map[string]bool{}, roleDefinitions: map[string]bool{},
		adminUnits: map[string]bool{}, assignments: map[string]*assignment{}}
	for _, id := range []string{GlobalReader, UserAdministrator, SecurityReader, DirectoryReaders} {
		g.roleDefinitions[id] = true
	}
	return g
}

// AddPrincipal puts a user, group or service principal's object ID in the directory.
func (g *Graph) AddPrincipal(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.principals[strings.ToLower(id)] = true
}

// AddRoleDefinition adds a (custom) role definition's ID.
func (g *Graph) AddRoleDefinition(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.roleDefinitions[strings.ToLower(id)] = true
}

// AddAdministrativeUnit adds an administrative unit's ID.
func (g *Graph) AddAdministrativeUnit(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.adminUnits[strings.ToLower(id)] = true
}

// Assign records an assignment made outside Kubernetes (to adopt), returning its ID.
func (g *Graph) Assign(principalID, roleDefinitionID, directoryScopeID string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	a := &assignment{ID: newID(), PrincipalID: principalID, RoleDefinitionID: roleDefinitionID, DirectoryScopeID: &directoryScopeID}
	g.assignments[a.ID] = a
	return a.ID
}

// Assignments lists the assignments as "principal role scope", sorted, for tests.
func (g *Graph) Assignments() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, a := range g.assignments {
		out = append(out, a.PrincipalID+" "+a.RoleDefinitionID+" "+scopeOf(a))
	}
	sort.Strings(out)
	return out
}

// HasAssignment is whether an assignment with the ID exists, for tests.
func (g *Graph) HasAssignment(id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.assignments[id]
	return ok
}

var (
	collectionPath = regexp.MustCompile(`^/(beta|v1\.0)/roleManagement/directory/roleAssignments$`)
	itemPath       = regexp.MustCompile(`^/(beta|v1\.0)/roleManagement/directory/roleAssignments/([^/]+)$`)
	filterTerm     = regexp.MustCompile(`^\s*(principalId|roleDefinitionId|directoryScopeId)\s+eq\s+'((?:[^']|'')*)'\s*$`)
	adminUnitScope = regexp.MustCompile(`(?i)^/administrativeUnits/([^/]+)$`)
)

// ServeHTTP implements the role assignment API.
func (g *Graph) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.Log = append(g.Log, r.Method+" "+r.URL.Path)

	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || g.Authorize == nil || !g.Authorize(strings.TrimPrefix(auth, "Bearer ")) {
		writeError(w, http.StatusUnauthorized, "InvalidAuthenticationToken", "fake graph: this identity may not use Microsoft Graph")
		return
	}

	switch {
	case collectionPath.MatchString(r.URL.Path) && r.Method == http.MethodPost:
		g.create(w, r)
	case collectionPath.MatchString(r.URL.Path) && r.Method == http.MethodGet:
		g.list(w, r)
	case itemPath.MatchString(r.URL.Path):
		id := itemPath.FindStringSubmatch(r.URL.Path)[2]
		a, ok := g.assignments[id]
		if !ok {
			writeError(w, http.StatusNotFound, "Request_ResourceNotFound", "Resource '"+id+"' does not exist or one of its queried reference-property objects are not present.")
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, withType(a))
		case http.MethodDelete:
			delete(g.assignments, id)
			w.WriteHeader(http.StatusNoContent)
		default:
			// Graph does not update a role assignment.
			writeError(w, http.StatusMethodNotAllowed, "Request_BadRequest", "fake graph: "+r.Method+" is not supported on a role assignment")
		}
	default:
		writeError(w, http.StatusNotFound, "Request_ResourceNotFound", "fake graph: no such API: "+r.Method+" "+r.URL.Path)
	}
}

func (g *Graph) create(w http.ResponseWriter, r *http.Request) {
	var a assignment
	var body io.Reader = r.Body
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Request_BadRequest", "fake graph: the gzipped body is not gzip: "+err.Error())
			return
		}
		defer zr.Close()
		body = zr
	}
	if err := json.NewDecoder(body).Decode(&a); err != nil {
		writeError(w, http.StatusBadRequest, "Request_BadRequest", "fake graph: the body is not a role assignment: "+err.Error())
		return
	}
	if a.PrincipalID == "" || a.RoleDefinitionID == "" {
		writeError(w, http.StatusBadRequest, "Request_BadRequest", "principalId and roleDefinitionId are required.")
		return
	}
	if (a.DirectoryScopeID == nil) == (a.AppScopeID == nil) {
		writeError(w, http.StatusBadRequest, "Request_BadRequest", "Exactly one of directoryScopeId and appScopeId is required.")
		return
	}
	if !g.principals[strings.ToLower(a.PrincipalID)] {
		writeError(w, http.StatusNotFound, "Request_ResourceNotFound", "Resource '"+a.PrincipalID+"' does not exist or one of its queried reference-property objects are not present.")
		return
	}
	if !g.roleDefinitions[strings.ToLower(a.RoleDefinitionID)] {
		writeError(w, http.StatusNotFound, "Request_ResourceNotFound", "The role definition '"+a.RoleDefinitionID+"' does not exist.")
		return
	}
	if a.DirectoryScopeID != nil && *a.DirectoryScopeID != "/" {
		m := adminUnitScope.FindStringSubmatch(*a.DirectoryScopeID)
		if m == nil {
			writeError(w, http.StatusBadRequest, "Request_BadRequest", "directoryScopeId is / or /administrativeUnits/{id}, not "+*a.DirectoryScopeID+".")
			return
		}
		if !g.adminUnits[strings.ToLower(m[1])] {
			writeError(w, http.StatusNotFound, "Request_ResourceNotFound", "The administrative unit '"+m[1]+"' does not exist.")
			return
		}
	}
	for _, other := range g.assignments {
		if strings.EqualFold(other.PrincipalID, a.PrincipalID) && strings.EqualFold(other.RoleDefinitionID, a.RoleDefinitionID) &&
			strings.EqualFold(scopeOf(other), scopeOf(&a)) {
			writeError(w, http.StatusConflict, "Request_BadRequest", "A conflicting object with one or more of the specified property values is present in the directory.")
			return
		}
	}
	a.ID = newID()
	a.OdataType = ""
	g.assignments[a.ID] = &a
	writeJSON(w, http.StatusCreated, withType(&a))
}

func (g *Graph) list(w http.ResponseWriter, r *http.Request) {
	want := map[string]string{}
	if filter := r.URL.Query().Get("$filter"); filter != "" {
		for _, term := range strings.Split(filter, " and ") {
			m := filterTerm.FindStringSubmatch(term)
			if m == nil {
				writeError(w, http.StatusBadRequest, "Request_UnsupportedQuery", "fake graph: unsupported $filter term "+term)
				return
			}
			want[m[1]] = strings.ReplaceAll(m[2], "''", "'")
		}
	}
	values := []any{}
	ids := make([]string, 0, len(g.assignments))
	for id := range g.assignments {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a := g.assignments[id]
		have := map[string]string{"principalId": a.PrincipalID, "roleDefinitionId": a.RoleDefinitionID, "directoryScopeId": deref(a.DirectoryScopeID)}
		match := true
		for k, v := range want {
			if !strings.EqualFold(have[k], v) {
				match = false
			}
		}
		if match {
			values = append(values, withType(a))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"@odata.context": "https://graph.microsoft.com/beta/$metadata#roleManagement/directory/roleAssignments",
		"value":          values,
	})
}

func scopeOf(a *assignment) string {
	if a.AppScopeID != nil {
		return "app:" + *a.AppScopeID
	}
	return deref(a.DirectoryScopeID)
}

func withType(a *assignment) *assignment {
	c := *a
	c.OdataType = "#microsoft.graph.unifiedRoleAssignment"
	return &c
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// newID is an ID shaped as Graph's role assignment IDs are (URL-safe base64).
func newID() string {
	b := make([]byte, 33)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": message}})
}
