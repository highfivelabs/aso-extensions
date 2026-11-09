// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Wire is what a generated kind lets a user set: the leaves of its API type's
// spec (the Go type the CRD is made from), as paths keyed in lower case.
// ASO's generator flattens the spec's "properties" bags, renames a property
// that holds a resource ID to xReference and wraps some in a struct, so each
// leaf is entered under the spec's own spelling too (see addRef).
type Wire struct {
	Leaves   map[string]string // lower-case key -> the path as the kind has it
	FreeForm map[string]bool   // lower-case keys that take any JSON
	pre      map[string]string // lower-case prefixes of the keys, with one leaf below each
}

func (w *Wire) add(p string, free bool) {
	k := strings.ToLower(p)
	if _, ok := w.Leaves[k]; !ok {
		w.Leaves[k] = p
	}
	if free {
		w.FreeForm[k] = true
	}
}

// addAs enters a leaf under another key, for the spelling the spec uses.
func (w *Wire) addAs(key, shown string) {
	k := strings.ToLower(key)
	if _, ok := w.Leaves[k]; !ok {
		w.Leaves[k] = shown
	}
}

// addRef records a leaf that holds a reference to another resource (a
// genruntime.ResourceReference): the spec calls it by its ARM name, which the
// armReference tag gives, or the field's name without Reference.
func (w *Wire) addRef(p, armName string) {
	w.add(p, false)
	suffix := ""
	base := p
	for strings.HasSuffix(base, "[]") || strings.HasSuffix(base, "{}") {
		suffix = base[len(base)-2:] + suffix
		base = base[:len(base)-2]
	}
	parent, name := "", base
	if i := strings.LastIndex(base, "."); i >= 0 {
		parent, name = base[:i], base[i+1:]
	}
	if armName != "" {
		w.addAs(join(parent, lowerFirst(armName))+suffix, p)
	}
	for _, s := range []string{"References", "Reference"} {
		if strings.HasSuffix(name, s) && len(name) > len(s) {
			b := strings.TrimSuffix(name, s)
			for _, x := range []string{"", "Id", "Ids", "ResourceId", "ResourceIds", "Uri", "Url"} {
				w.addAs(join(parent, b+x)+suffix, p)
			}
		}
	}
	// A map of references the spec keys by ID (identity.userAssignedIdentities)
	// is an array of {reference} here.
	if name == "reference" && strings.HasSuffix(parent, "[]") {
		w.addAs(strings.TrimSuffix(parent, "[]")+"{}", p)
	}
	// A reference to another resource stands for all of it: the spec has the
	// referenced resource's own properties below (the subnet of a private
	// endpoint has its address prefix) but they are set on that resource, not
	// here, so they are covered by the reference.
	if name == "reference" && parent != "" {
		w.addAs(parent, p)
		w.FreeForm[strings.ToLower(parent)] = true
	}
}

// variants lists a path with any subset of its "properties" segments
// removed: ASO's API types have the properties bag flattened, to different
// depths.
func variants(p string) []string {
	segs := strings.Split(p, ".")
	var idx []int
	for i, s := range segs {
		if s == "properties" && i < len(segs)-1 {
			idx = append(idx, i)
		}
	}
	if len(idx) > 6 {
		idx = idx[:6]
	}
	out := make([]string, 0, 1<<len(idx))
	for mask := 0; mask < 1<<len(idx); mask++ {
		skip := map[int]bool{}
		for b, i := range idx {
			if mask&(1<<b) != 0 {
				skip[i] = true
			}
		}
		var keep []string
		for i, s := range segs {
			if !skip[i] {
				keep = append(keep, s)
			}
		}
		out = append(out, strings.Join(keep, "."))
	}
	return out
}

// covers reports whether the kind can set the attribute at path p, and at
// which of its paths: the same path (with the properties bags flattened or
// not); a deeper path (the spec calls the property a free-form object, the
// kind models it); or a free-form leaf on the way (the kind takes any JSON
// there).
func (w *Wire) covers(p string) (string, bool) {
	for _, v := range variants(strings.ToLower(p)) {
		if s, ok := w.coversExact(v); ok {
			return s, true
		}
	}
	return "", false
}

func (w *Wire) coversExact(k string) (string, bool) {
	if v, ok := w.Leaves[k]; ok {
		return v, true
	}
	for i := 1; i < len(k); i++ {
		if k[i] == '.' || k[i] == '[' || k[i] == '{' {
			if w.FreeForm[k[:i]] {
				return w.Leaves[k[:i]], true
			}
		}
	}
	if w.pre == nil {
		w.pre = map[string]string{}
		keys := make([]string, 0, len(w.Leaves))
		for l := range w.Leaves {
			keys = append(keys, l)
		}
		sort.Strings(keys)
		for _, l := range keys {
			for i := 1; i < len(l); i++ {
				if l[i] == '.' || l[i] == '[' || l[i] == '{' {
					if _, ok := w.pre[l[:i]]; !ok {
						w.pre[l[:i]] = w.Leaves[l]
					}
				}
			}
		}
	}
	if v, ok := w.pre[k]; ok {
		return v, true
	}
	return "", false
}

// WireKind is one resource spec type found in a pair of packages.
type WireKind struct {
	Group   string // API group (network.azure.com)
	Kind    string // VirtualNetwork
	Version string // v1api20201101
	API     string // the ARM API version, 2020-11-01
	ARMType string // Microsoft.Network/virtualNetworks
	Wire    *Wire
}

var versionDirRe = regexp.MustCompile(`^v(?:1api|1beta|1alpha1api)?(\d{8})(preview)?$`)

// wireVersionPreferred reports whether ASO API version a is preferred over b:
// stable before preview, then the later date.
func wireVersionPreferred(a, b string) bool {
	ma, mb := versionDirRe.FindStringSubmatch(a), versionDirRe.FindStringSubmatch(b)
	if ma == nil || mb == nil {
		return a > b
	}
	pa, pb := ma[2] != "", mb[2] != ""
	if pa != pb {
		return !pa
	}
	return ma[1] > mb[1]
}

type pkgInfo struct {
	types  map[string]ast.Expr
	unions map[string]bool // structs whose fields are "mutually exclusive with all other properties": a discriminated union
	specs  map[string]*specMeta
}

type specMeta struct{ armType, api string }

// parsePackage reads the files of one package: the API package of a version,
// or its arm package.
func parsePackage(files []string) (*pkgInfo, error) {
	fset := token.NewFileSet()
	pi := &pkgInfo{types: map[string]ast.Expr{}, unions: map[string]bool{}, specs: map[string]*specMeta{}}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		af, err := parser.ParseFile(fset, f, src, parser.SkipObjectResolution|parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for _, decl := range af.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				for _, sp := range d.Specs {
					ts, ok := sp.(*ast.TypeSpec)
					if !ok {
						continue
					}
					pi.types[ts.Name.Name] = ts.Type
					if st, ok := ts.Type.(*ast.StructType); ok {
						for _, fl := range st.Fields.List {
							if fl.Doc != nil && strings.Contains(fl.Doc.Text(), "Mutually exclusive with all other properties") {
								pi.unions[ts.Name.Name] = true
							}
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv == nil || len(d.Recv.List) != 1 {
					continue
				}
				switch d.Name.Name {
				case "GetType", "GetAPIVersion":
					recv := recvName(d.Recv.List[0].Type)
					if lit := returnedString(d); lit != "" {
						m := pi.specs[recv]
						if m == nil {
							m = &specMeta{}
							pi.specs[recv] = m
						}
						if d.Name.Name == "GetType" {
							m.armType = lit
						} else {
							m.api = lit
						}
					}
				}
			}
		}
	}
	return pi, nil
}

func recvName(e ast.Expr) string {
	if s, ok := e.(*ast.StarExpr); ok {
		e = s.X
	}
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func returnedString(fd *ast.FuncDecl) string {
	if fd.Body == nil {
		return ""
	}
	for _, st := range fd.Body.List {
		if r, ok := st.(*ast.ReturnStmt); ok && len(r.Results) == 1 {
			if bl, ok := r.Results[0].(*ast.BasicLit); ok && bl.Kind == token.STRING {
				if s, err := strconv.Unquote(bl.Value); err == nil {
					return s
				}
			}
		}
	}
	return ""
}

func fieldTag(tag *ast.BasicLit, key string) string {
	if tag == nil {
		return ""
	}
	raw, err := strconv.Unquote(tag.Value)
	if err != nil {
		return ""
	}
	return reflect.StructTag(raw).Get(key)
}

func jsonName(tag *ast.BasicLit) (string, bool) {
	name, _, _ := strings.Cut(fieldTag(tag, "json"), ",")
	if name == "" || name == "-" {
		return "", false
	}
	return name, true
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

type apiWalker struct {
	pi    *pkgInfo
	w     *Wire
	stack map[string]bool
}

var builtinTypes = map[string]bool{"string": true, "bool": true, "int": true, "int32": true, "int64": true,
	"float32": true, "float64": true, "byte": true, "uint": true, "uint32": true, "uint64": true}

func (ww *apiWalker) walk(e ast.Expr, p, armRef string) {
	switch t := e.(type) {
	case *ast.StarExpr:
		ww.walk(t.X, p, armRef)
	case *ast.ArrayType:
		ww.walk(t.Elt, p+"[]", armRef)
	case *ast.MapType:
		ww.walk(t.Value, p+"{}", armRef)
	case *ast.InterfaceType:
		ww.w.add(p, true)
	case *ast.SelectorExpr:
		switch t.Sel.Name {
		case "ResourceReference", "KnownResourceReference", "ArbitraryOwnerReference", "WellKnownResourceReference":
			ww.w.addRef(p, armRef)
		case "JSON":
			ww.w.add(p, true)
		case "SecretReference", "ConfigMapReference", "SecretMapReference":
			ww.w.add(p, false)
			if i := strings.LastIndex(p, "FromConfig"); i > 0 && i == len(strings.TrimRight(p, "[]{}"))-len("FromConfig") {
				ww.w.addAs(p[:i]+p[i+len("FromConfig"):], p)
			}
		default:
			ww.w.add(p, false)
		}
	case *ast.StructType:
		ww.structFields(t, p, false)
	case *ast.Ident:
		if builtinTypes[t.Name] {
			ww.w.add(p, false)
			return
		}
		under, ok := ww.pi.types[t.Name]
		if !ok {
			ww.w.add(p, false)
			return
		}
		if ww.stack[t.Name] {
			ww.w.add(p, true) // a recursive type: anything goes below
			return
		}
		ww.stack[t.Name] = true
		defer delete(ww.stack, t.Name)
		if st, ok := under.(*ast.StructType); ok {
			ww.structFields(st, p, ww.pi.unions[t.Name])
			return
		}
		ww.walk(under, p, armRef)
	default:
		ww.w.add(p, false)
	}
}

func (ww *apiWalker) structFields(st *ast.StructType, p string, union bool) {
	n := 0
	for _, f := range st.Fields.List {
		name, ok := jsonName(f.Tag)
		if !ok {
			continue
		}
		if p == "" && (name == "azureName" || name == "owner" || name == "operatorSpec") {
			continue // the operator's, not Azure's
		}
		n++
		if union {
			// A discriminated union: the variant's own fields are at this path.
			ww.walk(f.Type, p, "")
			continue
		}
		ww.walk(f.Type, join(p, name), fieldTag(f.Tag, "armReference"))
	}
	if n == 0 && p != "" {
		ww.w.add(p, false)
	}
}

// kindsOfPackage returns the resource kinds of a version: the *_Spec types of
// the arm package that name an ARM type, with the leaves of the API package's
// type of the same name.
func kindsOfPackage(arm, api *pkgInfo, group, version string) []*WireKind {
	var out []*WireKind
	var names []string
	for n, m := range arm.specs {
		if m.armType != "" && strings.HasSuffix(n, "_Spec") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		m := arm.specs[n]
		st, ok := api.types[n].(*ast.StructType)
		if !ok {
			continue
		}
		w := &Wire{Leaves: map[string]string{}, FreeForm: map[string]bool{}}
		ww := &apiWalker{pi: api, w: w, stack: map[string]bool{n: true}}
		ww.structFields(st, "", false)
		out = append(out, &WireKind{
			Group: group, Kind: strings.TrimSuffix(n, "_Spec"), Version: version,
			API: m.api, ARMType: m.armType, Wire: w,
		})
	}
	return out
}
