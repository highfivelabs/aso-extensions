// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	armFileRe = regexp.MustCompile(`^v2/api/([^/]+)/([^/]+)/arm/[^/]+_types_gen\.go$`)
	apiFileRe = regexp.MustCompile(`^v2/api/([^/]+)/([^/]+)/[^/]+_types_gen\.go$`)
)

// asoKinds reads the resource kinds of ASO's API packages at the pinned
// commit: the ARM wire types of every API version, and for each ARM type
// the version ASO would be used at (the latest stable one).
func asoKinds(cache string, src *sources) ([]*Kind, error) {
	g := asoCache(cache, src)
	if err := g.open(); err != nil {
		return nil, err
	}
	all, err := g.list("v2/api")
	if err != nil {
		return nil, err
	}
	arms := map[[2]string][]string{}
	apis := map[[2]string][]string{}
	var want []string
	for _, f := range all {
		if m := armFileRe.FindStringSubmatch(f); m != nil {
			arms[[2]string{m[1], m[2]}] = append(arms[[2]string{m[1], m[2]}], f)
			want = append(want, f)
		} else if m := apiFileRe.FindStringSubmatch(f); m != nil {
			apis[[2]string{m[1], m[2]}] = append(apis[[2]string{m[1], m[2]}], f)
			want = append(want, f)
		}
	}
	if err := g.ensure(want); err != nil {
		return nil, err
	}
	best := map[string]*Kind{} // group + ARM type -> kind
	keys := make([][2]string, 0, len(arms))
	for k := range arms {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+"/"+keys[i][1] < keys[j][0]+"/"+keys[j][1] })
	for _, k := range keys {
		armPI, err := parseFiles(g.dir, arms[k])
		if err != nil {
			return nil, err
		}
		apiPI, err := parseFiles(g.dir, apis[k])
		if err != nil {
			return nil, err
		}
		for _, wk := range kindsOfPackage(armPI, apiPI, k[0]+".azure.com", k[1]) {
			kd := &Kind{Group: wk.Group, Kind: wk.Kind, Version: wk.Version, API: wk.API, ARMType: wk.ARMType, Wire: wk.Wire}
			id := kd.Group + "|" + strings.ToLower(kd.ARMType)
			if ex, ok := best[id]; !ok || wireVersionPreferred(kd.Version, ex.Version) {
				best[id] = kd
			}
		}
	}
	var out []*Kind
	for _, id := range sortedKeys(best) {
		out = append(out, best[id])
	}
	return out, nil
}

// oursKinds reads this repository's kinds: the generated ones from their
// ARM wire types (apis/arm), the hand-written ones from their CRDs and
// hack/coverage/ours.yaml.
func oursKinds(root string) ([]*Kind, error) {
	var out []*Kind
	generated := map[string]bool{} // API groups of generated kinds
	armDir := filepath.Join(root, "apis", "arm")
	groups, _ := os.ReadDir(armDir)
	for _, ge := range groups {
		if !ge.IsDir() {
			continue
		}
		vers, _ := os.ReadDir(filepath.Join(armDir, ge.Name()))
		best := map[string]*Kind{}
		for _, ve := range vers {
			if !ve.IsDir() {
				continue
			}
			armFiles, _ := filepath.Glob(filepath.Join(armDir, ge.Name(), ve.Name(), "arm", "*_types_gen.go"))
			if len(armFiles) == 0 {
				continue
			}
			armPI, err := parsePackage(armFiles)
			if err != nil {
				return nil, err
			}
			apiFiles, _ := filepath.Glob(filepath.Join(armDir, ge.Name(), ve.Name(), "*_types_gen.go"))
			apiPI, err := parsePackage(apiFiles)
			if err != nil {
				return nil, err
			}
			group := ge.Name() + ".azure.hfl.dev"
			generated[group] = true
			for _, wk := range kindsOfPackage(armPI, apiPI, group, ve.Name()) {
				kd := &Kind{Group: wk.Group, Kind: wk.Kind, Version: wk.Version, API: wk.API, ARMType: wk.ARMType, Wire: wk.Wire}
				id := strings.ToLower(kd.ARMType)
				if ex, ok := best[id]; !ok || wireVersionPreferred(kd.Version, ex.Version) {
					best[id] = kd
				}
			}
		}
		for _, id := range sortedKeys(best) {
			out = append(out, best[id])
		}
	}
	accounted := map[string]bool{}
	hand, err := readOurs(root, accounted)
	if err != nil {
		return nil, err
	}
	for _, h := range hand {
		w := &Wire{Leaves: map[string]string{}, FreeForm: map[string]bool{}}
		for _, l := range h.Leaves {
			attr := l
			if h.ARMType != "" {
				attr = h.Map[l]
			}
			w.add2(attr, "spec."+l)
		}
		out = append(out, &Kind{Group: h.Group, Kind: h.Kind, Version: "v1", API: h.API, ARMType: h.ARMType,
			Wire: w, FromPR: h.FromUpstreamPR, Outside: h.Outside, Note: h.Note})
	}
	// Every CRD must be explained: generated, or listed in ours.yaml.
	crds, _ := filepath.Glob(filepath.Join(root, "config", "crds", "*.yaml"))
	for _, f := range crds {
		base := filepath.Base(f)
		if base == "kustomization.yaml" || accounted[base] {
			continue
		}
		ok := false
		for g := range generated {
			if strings.HasPrefix(base, g+"_") {
				ok = true
			}
		}
		if !ok {
			return nil, fmt.Errorf("config/crds/%s is neither a generated kind (apis/arm) nor listed in hack/coverage/ours.yaml", base)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].name() < out[j].name() })
	return out, nil
}

func parseFiles(dir string, files []string) (*pkgInfo, error) {
	var abs []string
	for _, f := range files {
		abs = append(abs, filepath.Join(dir, f))
	}
	return parsePackage(abs)
}

// add2 records a leaf path (the key) with the path to show for it.
func (w *Wire) add2(key, shown string) {
	k := strings.ToLower(key)
	if _, ok := w.Leaves[k]; !ok {
		w.Leaves[k] = shown
	}
}

func asoCache(cache string, src *sources) *gitCache {
	return &gitCache{dir: filepath.Join(cache, "aso-"+src.ASO.Commit[:12]), remote: src.ASO.Repo, commit: src.ASO.Commit}
}
