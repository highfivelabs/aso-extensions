// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// spotCheck compares, for some resources, the coverage this tool computed
// from ASO's ARM wire types with what ASO's real CRDs (a release's
// customresourcedefinitions manifest) say: an attribute is covered by the
// CRD when the spec has a property for it, after ASO's renames are undone.
// It is run by hand (hack/coverage/run.sh --spotcheck <file>), not by
// regeneration, and its results are recorded in docs/coverage/README.md.
func spotCheck(crdFile string, types []string, rows []*ResRow, official []*Kind, w io.Writer) error {
	byType := map[string]*ResRow{}
	for _, r := range rows {
		byType[strings.ToLower(r.Resource)] = r
	}
	kindOf := map[string]*Kind{} // group/kind -> kind
	for _, k := range official {
		kindOf[k.name()] = k
	}
	want := map[string]*Kind{} // group/kind -> kind, for the resources asked for
	for _, t := range types {
		r := byType[strings.ToLower(t)]
		if r == nil || len(r.OfficialKinds) == 0 {
			fmt.Fprintf(w, "%s: no ASO kind\n", t)
			continue
		}
		want[r.OfficialKinds[0]] = kindOf[r.OfficialKinds[0]]
	}
	f, err := os.Open(crdFile)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	found := map[string][]string{} // group/kind -> CRD leaf paths
	for {
		var d struct {
			Spec struct {
				Group string `yaml:"group"`
				Names struct {
					Kind string `yaml:"kind"`
				} `yaml:"names"`
				Versions []struct {
					Name   string `yaml:"name"`
					Schema struct {
						OpenAPIV3Schema map[string]any `yaml:"openAPIV3Schema"`
					} `yaml:"schema"`
				} `yaml:"versions"`
			} `yaml:"spec"`
		}
		if err := dec.Decode(&d); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		name := d.Spec.Group + "/" + d.Spec.Names.Kind
		k, ok := want[name]
		if !ok {
			continue
		}
		for _, v := range d.Spec.Versions {
			if v.Name == k.Version {
				props, _ := v.Schema.OpenAPIV3Schema["properties"].(map[string]any)
				spec, _ := props["spec"].(map[string]any)
				found[name] = specLeaves(spec, "")
			}
		}
	}
	var totalAgree, totalAttrs int
	for _, t := range types {
		r := byType[strings.ToLower(t)]
		if r == nil || len(r.OfficialKinds) == 0 {
			continue
		}
		name := r.OfficialKinds[0]
		leaves, ok := found[name]
		if !ok {
			fmt.Fprintf(w, "%s: %s (%s) is not in the CRD file\n", t, name, want[name].Version)
			continue
		}
		set := normalizedSet(leaves)
		agree, n := 0, 0
		var dis []string
		for _, a := range r.Attrs {
			n++
			mine := a.By == Official || a.By == Both
			theirs := crdHas(set, a.Attribute)
			if mine == theirs {
				agree++
			} else if mine {
				dis = append(dis, "tool says covered, CRD has no property: "+a.Attribute)
			} else {
				dis = append(dis, "CRD has a property, tool says uncovered: "+a.Attribute)
			}
		}
		totalAgree += agree
		totalAttrs += n
		fmt.Fprintf(w, "%s  %s@%s  %d attributes, %d agree (%s%%)\n", t, name, want[name].Version, n, agree, pct(agree, n))
		sort.Strings(dis)
		for i, d := range dis {
			if i >= 12 {
				fmt.Fprintf(w, "    ... and %d more\n", len(dis)-i)
				break
			}
			fmt.Fprintf(w, "    %s\n", d)
		}
	}
	fmt.Fprintf(w, "total: %d of %d attributes agree (%s%%)\n", totalAgree, totalAttrs, pct(totalAgree, totalAttrs))
	return nil
}

// specLeaves lists the leaf paths of a CRD spec schema. A ResourceReference
// (armId, group, kind, name) and a secret reference (name, key) are leaves.
func specLeaves(s map[string]any, p string) []string {
	var out []string
	if items, ok := s["items"].(map[string]any); ok {
		out = append(out, specLeaves(items, p+"[]")...)
	}
	if ap, ok := s["additionalProperties"].(map[string]any); ok {
		out = append(out, specLeaves(ap, p+"{}")...)
	}
	if ps, ok := s["properties"].(map[string]any); ok && len(ps) > 0 {
		names := make([]string, 0, len(ps))
		for n := range ps {
			names = append(names, n)
		}
		sort.Strings(names)
		has := func(names ...string) bool {
			for _, n := range names {
				if _, ok := ps[n]; !ok {
					return false
				}
			}
			return true
		}
		// A ResourceReference has armId; a secret or config map reference
		// has exactly name and key.
		isRef := func() bool {
			return has("armId") && len(ps) <= 4 || has("name", "key") && len(ps) == 2
		}
		if p != "" && isRef() {
			return []string{p}
		}
		for _, n := range names {
			if c, ok := ps[n].(map[string]any); ok {
				out = append(out, specLeaves(c, join(p, n))...)
			}
		}
		return out
	}
	if len(out) == 0 && p != "" {
		out = append(out, p)
	}
	return out
}

// normalizedSet reduces CRD paths to the spec's: ASO flattens the "properties"
// bag, names a reference property xReference (or x.reference for an object
// that holds only an id), and a secret xSecretRef.
func normalizedSet(leaves []string) map[string]bool {
	set := map[string]bool{}
	add := func(p string) {
		p = strings.ToLower(p)
		set[p] = true
		for i := 1; i < len(p); i++ {
			if p[i] == '.' || p[i] == '[' || p[i] == '{' {
				set[p[:i]] = true
			}
		}
	}
	for _, l := range leaves {
		add(l)
		if strings.HasSuffix(l, ".reference") {
			add(strings.TrimSuffix(l, ".reference") + ".id")
			set["\x00ref:"+strings.ToLower(strings.TrimSuffix(l, ".reference"))] = true
		}
		last := l[strings.LastIndexAny(l, ".]}")+1:]
		pre := l[:len(l)-len(last)]
		for _, suf := range []string{"Reference", "References", "SecretRef", "SecretReference", "Ref"} {
			if strings.HasSuffix(last, suf) && len(last) > len(suf) {
				base := strings.TrimSuffix(last, suf)
				add(pre + base)
				add(pre + base + "Id")
				add(pre + base + "ID")
				add(pre + base + "Ids")
				add(pre + base + "Uri")
			}
		}
	}
	return set
}

// crdHas reports whether the CRD has a property for the spec attribute
// path, ignoring the "properties" segments ASO flattens.
func crdHas(set map[string]bool, attr string) bool {
	p := strings.ToLower(attr)
	if set[p] {
		return true
	}
	// below a reference to another resource: covered by the reference
	for _, v := range variantsForSpot(p) {
		for i := 1; i < len(v); i++ {
			if v[i] == '.' && set["\x00ref:"+v[:i]] {
				return true
			}
		}
	}
	var segs []string
	for _, s := range splitPath(p) {
		if s != "properties" {
			segs = append(segs, s)
		}
	}
	return set[strings.Join(segs, ".")]
}

// splitPath splits a path into names, keeping [] and {} with their name.
func splitPath(p string) []string {
	var out []string
	cur := ""
	for i := 0; i < len(p); i++ {
		if p[i] == '.' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(p[i])
	}
	return append(out, cur)
}

func variantsForSpot(p string) []string { return variants(p) }
