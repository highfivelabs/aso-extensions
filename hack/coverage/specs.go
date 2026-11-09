// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Attr is one writable property of a resource, as the OpenAPI spec has it.
type Attr struct {
	Path     string // dotted, [] for arrays, {} for maps
	Type     string // the leaf's type: string, integer, number, boolean, object
	Required bool   // required all the way down from the request body
}

// Resource is one ARM resource type: a path with a PUT in a spec.
type Resource struct {
	Type    string // Microsoft.Network/virtualNetworks/subnets
	NS      string // Microsoft.Network
	Attrs   map[string]*Attr
	APIs    map[string]bool // spec API versions the attributes came from
	Sets    map[string]bool // spec sets (service/folder) they came from
	Ver     string          // the API version the attributes come from
	Preview bool            // that version is a preview
	Fallbck bool            // not in the latest version of its set; read from an older one
}

// specSet is one OpenAPI "project": the versions of one folder under
// specification/<service>/resource-manager/.
type specSet struct {
	Service string // the specification/<service> folder
	Key     string // the folder below resource-manager, without the version
	// Versions maps a version to its json files (paths in the repository).
	Stable, Preview map[string][]string
}

func (s *specSet) name() string { return s.Service + "/" + s.Key }

var setPattern = regexp.MustCompile(`^specification/([^/]+)/resource-manager/(?:(.*)/)?(stable|preview)/([^/]+)/([^/]+\.json)$`)

// scanSets groups the json files of every spec in the commit into sets.
func scanSets(files []string) []*specSet {
	byName := map[string]*specSet{}
	for _, f := range files {
		if strings.Contains(f, "/examples/") {
			continue
		}
		m := setPattern.FindStringSubmatch(f)
		if m == nil {
			continue
		}
		svc, key, channel, ver, _ := m[1], m[2], m[3], m[4], m[5]
		if strings.Contains(key, "/stable/") || strings.Contains(key, "/preview/") {
			continue // a version folder inside a version folder: not a spec
		}
		s := byName[svc+"/"+key]
		if s == nil {
			s = &specSet{Service: svc, Key: key, Stable: map[string][]string{}, Preview: map[string][]string{}}
			byName[svc+"/"+key] = s
		}
		if channel == "stable" {
			s.Stable[ver] = append(s.Stable[ver], f)
		} else {
			s.Preview[ver] = append(s.Preview[ver], f)
		}
	}
	var sets []*specSet
	for _, s := range byName {
		sets = append(sets, s)
	}
	sort.Slice(sets, func(i, j int) bool { return sets[i].name() < sets[j].name() })
	return sets
}

// versionLess orders Azure API versions: by date, then a stable version
// before a preview of the same date.
func versionLess(a, b string) bool {
	da, db := versionDate(a), versionDate(b)
	if da != db {
		return da < db
	}
	return a > b // "2024-01-01" > "2024-01-01-preview" in string order: stable is later
}

func versionDate(v string) string {
	if len(v) >= 10 {
		return v[:10]
	}
	return v
}

func sortedVersions(m map[string][]string) []string {
	var vs []string
	for v := range m {
		vs = append(vs, v)
	}
	sort.Slice(vs, func(i, j int) bool { return versionLess(vs[i], vs[j]) })
	return vs
}

// selFile is one spec file at the version it is read at.
type selFile struct {
	Path    string
	Ver     string
	Preview bool
}

// selection returns the files a set is read at. A set is a folder of
// versions; in some every version holds every file, in others a version holds
// only the files that changed in it (Monitor's). So each file name is read at
// the latest stable version that has it, or if none is stable at its latest
// preview: every resource is then at its latest stable version, and no
// resource is lost because a newer version left its file out.
func (s *specSet) selection() []selFile {
	best := map[string]selFile{}
	consider := func(m map[string][]string, preview bool) {
		for _, v := range sortedVersions(m) { // oldest first, so the last wins
			for _, f := range m[v] {
				base := f[strings.LastIndex(f, "/")+1:]
				if ex, ok := best[base]; ok && !ex.Preview && preview {
					continue
				}
				best[base] = selFile{f, v, preview}
			}
		}
	}
	consider(s.Preview, true)
	consider(s.Stable, false)
	var out []selFile
	for _, f := range best {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// older lists the other versions of a set, newest first: stable ones, then
// previews (used only to find a resource that ASO or we cover and the latest
// version no longer has).
func (s *specSet) older() (vers []string, files map[string][]string, preview map[string]bool) {
	files = map[string][]string{}
	preview = map[string]bool{}
	for v, f := range s.Stable {
		files[v] = f
	}
	for v, f := range s.Preview {
		files[v] = f
		preview[v] = true
	}
	vers = sortedVersions(files)
	for i, j := 0, len(vers)-1; i < j; i, j = i+1, j-1 {
		vers[i], vers[j] = vers[j], vers[i]
	}
	return
}

// ---- reading swagger ----

type doc struct {
	path string // repo-relative, slash-separated
	raw  map[string]any
}

type loader struct {
	g     *gitCache
	cache map[string]*doc
	fails map[string]string
}

func newLoader(g *gitCache) *loader {
	return &loader{g: g, cache: map[string]*doc{}, fails: map[string]string{}}
}

func (l *loader) load(p string) (*doc, error) {
	if d, ok := l.cache[p]; ok {
		return d, nil
	}
	if msg, ok := l.fails[p]; ok {
		return nil, fmt.Errorf("%s", msg)
	}
	fail := func(err error) (*doc, error) {
		l.fails[p] = err.Error()
		return nil, err
	}
	if err := l.g.ensure([]string{p}); err != nil {
		return fail(err)
	}
	b, err := os.ReadFile(filepath.Join(l.g.dir, filepath.FromSlash(p)))
	if err != nil {
		return fail(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return fail(fmt.Errorf("%s: %w", p, err))
	}
	d := &doc{path: p, raw: raw}
	l.cache[p] = d
	return d, nil
}

// resolve follows a $ref. It returns the target, the document it is in, and
// a key naming it.
func (l *loader) resolve(d *doc, ref string) (*doc, map[string]any, string, error) {
	file, frag, _ := strings.Cut(ref, "#")
	td := d
	if file != "" {
		p := path.Clean(path.Join(path.Dir(d.path), file))
		var err error
		if td, err = l.load(p); err != nil {
			return nil, nil, "", err
		}
	}
	var cur any = td.raw
	for _, seg := range strings.Split(strings.TrimPrefix(frag, "/"), "/") {
		if seg == "" {
			continue
		}
		if u, err := url.PathUnescape(seg); err == nil {
			seg = u
		}
		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, nil, "", fmt.Errorf("%s: bad pointer %q", ref, frag)
		}
		if cur, ok = m[seg]; !ok {
			return nil, nil, "", fmt.Errorf("%s: no %q in %s", ref, seg, td.path)
		}
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return nil, nil, "", fmt.Errorf("%s: not an object", ref)
	}
	return td, m, td.path + "#" + frag, nil
}

// readOnly reports whether a property is not writable: readOnly, or an
// x-ms-mutability that allows neither create nor update.
func readOnly(s map[string]any) bool {
	if b, ok := s["readOnly"].(bool); ok && b {
		return true
	}
	if mu, ok := s["x-ms-mutability"].([]any); ok {
		for _, v := range mu {
			if v == "create" || v == "update" {
				return false
			}
		}
		return len(mu) > 0
	}
	return false
}

// flat is a schema with its allOf parts merged.
type flat struct {
	props    map[string]propEntry
	required map[string]bool
	typ      string
	items    *schemaRef
	addl     *schemaRef
	addlAny  bool
	disc     string
	discDoc  *doc
	discName string
}

type propEntry struct {
	d *doc
	s map[string]any
}

type schemaRef struct {
	d *doc
	s map[string]any
}

type walker struct {
	l          *loader
	out        map[string]*Attr
	unresolved map[string]bool
	n          int // leaves added, to tell whether a schema added any
}

func (w *walker) unresolvedRef(err error) {
	w.unresolved[err.Error()] = true
}

// flatten merges s and everything it extends. stack guards against
// recursive references.
func (w *walker) flatten(d *doc, s map[string]any, stack map[string]bool, f *flat, top bool) {
	if ref, ok := s["$ref"].(string); ok {
		td, t, key, err := w.l.resolve(d, ref)
		if err != nil {
			w.unresolvedRef(err)
			return
		}
		if stack[key] {
			return
		}
		stack[key] = true
		defer delete(stack, key)
		if top {
			if _, ok := t["discriminator"]; ok {
				f.discDoc = td
				f.discName = key[strings.LastIndex(key, "/")+1:]
			}
		}
		w.flatten(td, t, stack, f, top)
		return
	}
	if all, ok := s["allOf"].([]any); ok {
		for _, a := range all {
			if am, ok := a.(map[string]any); ok {
				w.flatten(d, am, stack, f, false)
			}
		}
	}
	if t, ok := s["type"].(string); ok && f.typ == "" {
		f.typ = t
	}
	if disc, ok := s["discriminator"].(string); ok && top {
		f.disc = disc
	}
	if props, ok := s["properties"].(map[string]any); ok {
		for name, pv := range props {
			if pm, ok := pv.(map[string]any); ok {
				f.props[name] = propEntry{d, pm}
			}
		}
	}
	if req, ok := s["required"].([]any); ok {
		for _, r := range req {
			if rs, ok := r.(string); ok {
				f.required[rs] = true
			}
		}
	}
	if it, ok := s["items"].(map[string]any); ok {
		f.items = &schemaRef{d, it}
		f.typ = "array"
	}
	switch ap := s["additionalProperties"].(type) {
	case map[string]any:
		f.addl = &schemaRef{d, ap}
	case bool:
		f.addlAny = f.addlAny || ap
	}
}

// subtypes finds the definitions of d that extend the definition name,
// directly or through another subtype.
func (w *walker) subtypes(d *doc, name string) []string {
	defs, _ := d.raw["definitions"].(map[string]any)
	extends := map[string][]string{}
	for dn, dv := range defs {
		dm, ok := dv.(map[string]any)
		if !ok {
			continue
		}
		all, _ := dm["allOf"].([]any)
		for _, a := range all {
			if am, ok := a.(map[string]any); ok {
				if ref, ok := am["$ref"].(string); ok && strings.HasPrefix(ref, "#/definitions/") {
					parent := strings.TrimPrefix(ref, "#/definitions/")
					extends[parent] = append(extends[parent], dn)
				}
			}
		}
	}
	var out []string
	seen := map[string]bool{name: true}
	queue := []string{name}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		kids := extends[cur]
		sort.Strings(kids)
		for _, k := range kids {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
				queue = append(queue, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

func join(p, name string) string {
	if p == "" {
		return name
	}
	return p + "." + name
}

// collect adds the writable leaves under a schema at the given path.
func (w *walker) collect(d *doc, s map[string]any, p string, req bool, stack map[string]bool) {
	// A recursive reference stops here and leaves the path as an object.
	flatStack := stack
	if ref, ok := s["$ref"].(string); ok {
		if _, _, key, err := w.l.resolve(d, ref); err == nil {
			if stack[key] || len(stack) > 60 {
				w.leaf(p, "object", req)
				return
			}
			stack = copyStack(stack)
			stack[key] = true
		}
	}
	f := &flat{props: map[string]propEntry{}, required: map[string]bool{}}
	w.flatten(d, s, copyStack(flatStack), f, true)
	// A discriminated union: every subtype's properties are writable at the
	// same path.
	if f.discDoc != nil {
		for _, sub := range w.subtypes(f.discDoc, f.discName) {
			sd, st, key, err := w.l.resolve(f.discDoc, "#/definitions/"+sub)
			if err != nil {
				w.unresolvedRef(err)
				continue
			}
			if stack[key] {
				continue
			}
			sf := &flat{props: map[string]propEntry{}, required: map[string]bool{}}
			w.flatten(sd, st, copyStack(stack), sf, false)
			for n, pe := range sf.props {
				if _, ok := f.props[n]; !ok {
					f.props[n] = pe
				}
			}
			for n := range sf.required {
				f.required[n] = true
			}
		}
	}
	n0 := w.n
	if f.items != nil {
		w.collectChild(f.items.d, f.items.s, p+"[]", req, stack)
	}
	if f.addl != nil {
		w.collectChild(f.addl.d, f.addl.s, p+"{}", req, stack)
	}
	if len(f.props) > 0 {
		names := make([]string, 0, len(f.props))
		for n := range f.props {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			pe := f.props[n]
			if readOnly(pe.s) {
				continue
			}
			if p == "" && isEnvelope(n) {
				continue
			}
			if ref, ok := pe.s["$ref"].(string); ok {
				if _, t, _, err := w.l.resolve(pe.d, ref); err == nil && readOnly(t) {
					continue
				}
			}
			w.collectChild(pe.d, pe.s, join(p, n), req && f.required[n], stack)
		}
	}
	// Nothing writable below: the object itself is the attribute (a free-form
	// object, or a map whose values have only read-only properties, as
	// identity.userAssignedIdentities does).
	if w.n == n0 && p != "" {
		typ := f.typ
		if typ == "" {
			typ = "object"
		}
		w.leaf(p, typ, req)
	}
}

// collectChild is collect for a property or element schema.
func (w *walker) collectChild(d *doc, s map[string]any, p string, req bool, stack map[string]bool) {
	w.collect(d, s, p, req, stack)
}

func (w *walker) leaf(p, typ string, req bool) {
	w.n++
	key := strings.ToLower(p)
	if a, ok := w.out[key]; ok {
		a.Required = a.Required && req
		return
	}
	w.out[key] = &Attr{Path: p, Type: typ, Required: req}
}

func isEnvelope(n string) bool {
	switch strings.ToLower(n) {
	case "id", "name", "type", "systemdata":
		return true
	}
	return false
}

func copyStack(s map[string]bool) map[string]bool {
	c := make(map[string]bool, len(s)+1)
	for k, v := range s {
		c[k] = v
	}
	return c
}

// ---- paths to resources ----

var providerSeg = regexp.MustCompile(`(?i)/providers/([^/{}]+)`)

// armType turns a path with a PUT into the resource type it creates, or ""
// when the path is not a resource (an action, a list). A type segment that is
// a path parameter ({recordType}) stays as it is; readDoc expands it.
func armType(p string) (ns, typ string) {
	if i := strings.Index(p, "?"); i >= 0 {
		p = p[:i]
	}
	idxs := providerSeg.FindAllStringSubmatchIndex(p, -1)
	if len(idxs) == 0 {
		low := strings.ToLower(strings.TrimRight(p, "/"))
		if strings.HasPrefix(low, "/subscriptions/{") && strings.Count(low, "/") == 4 &&
			strings.Contains(low, "/resourcegroups/") {
			return "Microsoft.Resources", "Microsoft.Resources/resourceGroups"
		}
		return "", ""
	}
	last := idxs[len(idxs)-1]
	ns = p[last[2]:last[3]]
	rest := strings.Trim(p[last[1]:], "/")
	if rest == "" {
		return "", ""
	}
	segs := strings.Split(rest, "/")
	if len(segs)%2 != 0 {
		return "", ""
	}
	typ = ns
	for i := 0; i < len(segs); i += 2 {
		typ += "/" + segs[i]
	}
	return ns, typ
}

// readDoc adds every resource a swagger file creates to res.
func (w *walker) readDoc(d *doc, api string, preview bool, set string, res map[string]*Resource) {
	paths := map[string]any{}
	for _, k := range []string{"paths", "x-ms-paths"} {
		if m, ok := d.raw[k].(map[string]any); ok {
			for pk, pv := range m {
				paths[pk] = pv
			}
		}
	}
	keys := make([]string, 0, len(paths))
	for k := range paths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, pk := range keys {
		item, ok := paths[pk].(map[string]any)
		if !ok {
			continue
		}
		put, ok := item["put"].(map[string]any)
		if !ok {
			continue
		}
		ns, typ := armType(pk)
		if typ == "" {
			continue
		}
		types := w.expandTypes(d, item, put, typ)
		if len(types) == 0 {
			continue
		}
		attrs := map[string]*Attr{}
		if body := w.bodySchema(d, item, put); body != nil {
			w2 := &walker{l: w.l, out: attrs, unresolved: w.unresolved}
			w2.collect(body.d, body.s, "", true, map[string]bool{})
		}
		for _, typ := range types {
			key := strings.ToLower(typ)
			r := res[key]
			if r == nil {
				r = &Resource{Type: typ, NS: ns, Attrs: map[string]*Attr{}, APIs: map[string]bool{}, Sets: map[string]bool{}, Ver: api, Preview: preview}
				res[key] = r
			}
			switch {
			case api == r.Ver && preview == r.Preview:
			case newer(api, preview, r.Ver, r.Preview):
				r.Attrs, r.APIs, r.Sets, r.Ver, r.Preview = map[string]*Attr{}, map[string]bool{}, map[string]bool{}, api, preview
			default:
				continue // an older version of a resource already read
			}
			for k, a := range attrs {
				if ex, ok := r.Attrs[k]; ok {
					ex.Required = ex.Required && a.Required
					continue
				}
				c := *a
				r.Attrs[k] = &c
			}
			r.APIs[api] = true
			r.Sets[set] = true
		}
	}
}

// newer reports whether version a is later than version b: a stable version
// is later than any preview, then the later date.
func newer(a string, aPreview bool, b string, bPreview bool) bool {
	if aPreview != bPreview {
		return !aPreview
	}
	return versionLess(b, a)
}

// expandTypes turns a type with a path-parameter segment ({recordType}) into
// one type per value of the parameter's enum: the DNS record sets are one
// path in the spec and one resource type per record type.
func (w *walker) expandTypes(d *doc, item, put map[string]any, typ string) []string {
	if !strings.Contains(typ, "{") {
		return []string{typ}
	}
	params := w.params(d, item, put)
	out := []string{typ}
	for {
		var next []string
		changed := false
		for _, t := range out {
			i := strings.Index(t, "{")
			if i < 0 {
				next = append(next, t)
				continue
			}
			changed = true
			j := strings.Index(t[i:], "}") + i
			name := t[i+1 : j]
			for _, p := range params {
				if p.m["name"] == name && p.m["in"] == "path" {
					if enum, ok := p.m["enum"].([]any); ok {
						for _, e := range enum {
							if es, ok := e.(string); ok {
								v := es
								if k := strings.LastIndex(v, "/"); k >= 0 {
									v = v[k+1:]
								}
								next = append(next, t[:i]+v+t[j+1:])
							}
						}
					}
					break
				}
			}
		}
		out = next
		if !changed {
			break
		}
	}
	sort.Strings(out)
	return out
}

type param struct {
	d *doc
	m map[string]any
}

// params lists the parameters of an operation and of its path, with
// references followed.
func (w *walker) params(d *doc, item, put map[string]any) []param {
	var raw []any
	if ps, ok := put["parameters"].([]any); ok {
		raw = append(raw, ps...)
	}
	if ps, ok := item["parameters"].([]any); ok {
		raw = append(raw, ps...)
	}
	var out []param
	for _, p := range raw {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		pd := d
		if ref, ok := pm["$ref"].(string); ok {
			td, t, _, err := w.l.resolve(d, ref)
			if err != nil {
				w.unresolvedRef(err)
				continue
			}
			pd, pm = td, t
		}
		out = append(out, param{pd, pm})
	}
	return out
}

func (w *walker) bodySchema(d *doc, item, put map[string]any) *schemaRef {
	for _, p := range w.params(d, item, put) {
		if p.m["in"] != "body" {
			continue
		}
		if sch, ok := p.m["schema"].(map[string]any); ok {
			return &schemaRef{p.d, sch}
		}
	}
	return nil
}
