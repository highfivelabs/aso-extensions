// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DocsCheck compares the kinds read from ASO's wire types with ASO's own
// "supported resources" documentation at the same commit
// (docs/hugo/content/reference/<group>/_index.md).
type DocsCheck struct {
	DocKinds     int      // kinds the documentation lists
	Matched      int      // of those, found among the wire types
	SameVersion  int      // matched, and the same API version is the newest in both
	DocsOnly     []string // listed in the documentation, no wire type (a renamed kind, or a group without ARM types)
	WireOnly     []string // wire types the documentation does not list
	VersionDiffs []string
}

var (
	docIndexRe = regexp.MustCompile(`^docs/hugo/content/reference/([^/]+)/_index\.md$`)
	docRowRe   = regexp.MustCompile(`^\| \[([A-Za-z0-9_]+)\]\(\{\{< relref "/reference/([^/]+)/([^#"]+)#`)
)

// allKinds is every kind of every API version, as the wire types have them:
// asoKinds returns only the newest per ARM type, so the documentation check
// is made on the kind names it finds there.
func checkDocs(g *gitCache, official []*Kind) (*DocsCheck, error) {
	all, err := g.list("docs/hugo/content/reference")
	if err != nil {
		return nil, err
	}
	var idx []string
	for _, f := range all {
		if docIndexRe.MatchString(f) {
			idx = append(idx, f)
		}
	}
	if err := g.ensure(idx); err != nil {
		return nil, err
	}
	type key struct{ group, kind string }
	docLatest := map[key]string{} // newest API version the documentation lists for the kind
	for _, f := range idx {
		b, err := os.ReadFile(filepath.Join(g.dir, f))
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(b), "\n") {
			m := docRowRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			k := key{m[2], strings.ToLower(m[1])}
			if cur, ok := docLatest[k]; !ok || wireVersionPreferred(m[3], cur) {
				docLatest[k] = m[3]
			}
		}
	}
	mine := map[key]string{}
	for _, k := range official {
		mine[key{strings.TrimSuffix(k.Group, ".azure.com"), strings.ToLower(k.Kind)}] = k.Version
	}
	dc := &DocsCheck{DocKinds: len(docLatest)}
	for k, v := range docLatest {
		mv, ok := mine[k]
		if !ok {
			dc.DocsOnly = append(dc.DocsOnly, k.group+"/"+k.kind)
			continue
		}
		dc.Matched++
		if sameVersion(mv, v) {
			dc.SameVersion++
		} else {
			dc.VersionDiffs = append(dc.VersionDiffs, k.group+"/"+k.kind+" docs "+v+" wire "+mv)
		}
	}
	for k := range mine {
		if _, ok := docLatest[k]; !ok {
			dc.WireOnly = append(dc.WireOnly, k.group+"/"+k.kind)
		}
	}
	sort.Strings(dc.DocsOnly)
	sort.Strings(dc.WireOnly)
	sort.Strings(dc.VersionDiffs)
	return dc, nil
}

// sameVersion compares two names of one API version: the documentation says
// v20240501 where the package is v1api20240501.
func sameVersion(a, b string) bool {
	ma, mb := versionDirRe.FindStringSubmatch(a), versionDirRe.FindStringSubmatch(b)
	if ma == nil || mb == nil {
		return a == b
	}
	return ma[1] == mb[1] && ma[2] == mb[2]
}
