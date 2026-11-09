// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"io"
	"strings"
)

// specStats counts what reading the specs found, for the report.
type specStats struct {
	Sets, SetsRead, Files, FilesFailed int
	StableSets, PreviewOnlySets        int
	Unresolved                         []string
	FallbackTypes                      []string
	NotFound                           []string
	FailedFiles                        []string
}

func (s *specStats) print(w io.Writer, rows []*ResRow, svcs []*SvcRow, official, ours []*Kind) {
	fmt.Fprintf(w, "sets %d (stable %d, preview-only %d), files %d (failed %d), unresolved refs %d, fallback types %d, not found %d\n",
		s.Sets, s.StableSets, s.PreviewOnlySets, s.Files, s.FilesFailed, len(s.Unresolved), len(s.FallbackTypes), len(s.NotFound))
	var o, u, n, t int
	for _, v := range svcs {
		o += v.AttrOfficial
		u += v.AttrOursBeyond
		n += v.AttrUncovered
		t += v.AttrTotal
	}
	for _, l := range [][]string{s.FallbackTypes, s.NotFound, s.Unresolved, s.FailedFiles} {
		for _, x := range l {
			fmt.Fprintln(w, "  ", x)
		}
		fmt.Fprintln(w, "  --")
	}
	fmt.Fprintf(w, "services %d, resources %d, attributes %d: official %d (%s%%), ours %d, uncovered %d\n", len(svcs), len(rows), t, o, pct(o, t), u, n)
}

// readSpecs reads the latest version of every spec set (and, for ARM types a
// kind covers that the latest version lacks, older versions) and returns the
// resources by lower-case type, and the canonical spelling of each namespace.
func readSpecs(g *gitCache, kinds []*Kind) (map[string]*Resource, map[string]string, *specStats, error) {
	stats := &specStats{}
	all, err := g.list("specification")
	if err != nil {
		return nil, nil, nil, err
	}
	sets := scanSets(all)
	stats.Sets = len(sets)
	var picks []*specSet
	var want []string
	for _, f := range all {
		if strings.HasPrefix(f, "specification/common-types/") && strings.HasSuffix(f, ".json") && !strings.Contains(f, "/examples/") {
			want = append(want, f)
		}
	}
	selected := map[*specSet][]selFile{}
	for _, s := range sets {
		sel := s.selection()
		if len(sel) == 0 {
			continue
		}
		allPreview := true
		for _, f := range sel {
			want = append(want, f.Path)
			allPreview = allPreview && f.Preview
		}
		if allPreview {
			stats.PreviewOnlySets++
		} else {
			stats.StableSets++
		}
		selected[s] = sel
		picks = append(picks, s)
	}
	if err := g.ensure(want); err != nil {
		return nil, nil, nil, err
	}
	l := newLoader(g)
	unresolved := map[string]bool{}
	w := &walker{l: l, unresolved: unresolved}
	res := map[string]*Resource{}
	for _, s := range picks {
		for _, f := range selected[s] {
			readFile(w, s, f.Path, f.Ver, f.Preview, res, stats)
		}
	}

	// Kinds whose ARM type the latest versions lack: look in older versions
	// of the sets of the same provider.
	need := map[string]bool{}
	for _, k := range kinds {
		if k.Outside == "" && k.ARMType != "" {
			if _, ok := res[strings.ToLower(k.ARMType)]; !ok {
				need[strings.ToLower(k.ARMType)] = true
			}
		}
	}
	for _, key := range sortedKeys(need) {
		found := false
		ns := key[:strings.Index(key, "/")]
		for _, s := range sets {
			if !strings.EqualFold(firstSeg(s.Key), ns) {
				continue
			}
			vers, files, prev := s.older()
			for _, v := range vers {
				if found {
					break
				}
				if err := g.ensure(files[v]); err != nil {
					return nil, nil, nil, err
				}
				tmp := map[string]*Resource{}
				for _, f := range files[v] {
					readFile(w, s, f, v, prev[v], tmp, nil)
				}
				if r, ok := tmp[key]; ok {
					r.Fallbck = true
					res[key] = r
					stats.FallbackTypes = append(stats.FallbackTypes, r.Type+" ("+v+")")
					found = true
				}
			}
			if found {
				break
			}
		}
		if !found {
			stats.NotFound = append(stats.NotFound, key)
		}
	}
	stats.Unresolved = sortedKeys(unresolved)

	// Canonical spelling of each namespace: the most common one.
	count := map[string]map[string]int{}
	for _, r := range res {
		k := strings.ToLower(r.NS)
		if count[k] == nil {
			count[k] = map[string]int{}
		}
		count[k][r.NS]++
	}
	nsName := map[string]string{}
	for k, m := range count {
		best := ""
		for name, n := range m {
			if best == "" || n > m[best] || (n == m[best] && name < best) {
				best = name
			}
		}
		nsName[k] = best
	}
	return res, nsName, stats, nil
}

func firstSeg(key string) string {
	if i := strings.Index(key, "/"); i >= 0 {
		return key[:i]
	}
	return key
}

func readFile(w *walker, s *specSet, file, ver string, preview bool, res map[string]*Resource, stats *specStats) {
	d, err := w.l.load(file)
	if stats != nil {
		stats.Files++
	}
	if err != nil {
		if stats != nil {
			stats.FilesFailed++
			stats.FailedFiles = append(stats.FailedFiles, err.Error())
		}
		return
	}
	w.readDoc(d, ver, preview, s.name(), res)
}
