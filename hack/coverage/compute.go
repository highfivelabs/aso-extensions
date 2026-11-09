// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"sort"
	"strings"
)

// Kind is a Kubernetes kind that covers an ARM resource: one of ASO's
// ("official") or one of ours.
type Kind struct {
	Group, Kind string
	Version     string // the kind's API version, v1api20201101
	API         string // the ARM API version it speaks, 2020-11-01
	ARMType     string
	Wire        *Wire
	FromPR      string
	Outside     string // a resource the specs do not have
	Note        string
}

func (k *Kind) name() string { return k.Group + "/" + k.Kind }

// Covered is how an attribute is covered.
type Covered string

const (
	None     Covered = "none"
	Official Covered = "official"
	Ours     Covered = "ours"
	Both     Covered = "both"
)

type AttrRow struct {
	Service, Resource, Attribute, Type string
	Required                           bool
	By                                 Covered
	OfficialPath, OursPath             string
}

type ResRow struct {
	Service, Resource                string
	Total, Official, OursBeyond, Unc int
	OfficialKinds, OursKinds         []string
	FromPR                           bool
	Notes                            []string
	Outside                          bool // not in the denominator
	Unmapped                         bool // a kind with no spec match
	Attrs                            []*AttrRow
}

type SvcRow struct {
	Service                                                string
	ResTotal, ResOfficial, ResOursBeyond                   int
	AttrTotal, AttrOfficial, AttrOursBeyond, AttrUncovered int
}

func pct(n, d int) string {
	if d == 0 {
		return "0.0"
	}
	return fmt.Sprintf("%.1f", float64(n)*100/float64(d))
}

// join covers the denominator with the kinds.
func computeRows(res map[string]*Resource, nsName map[string]string, official, ours []*Kind) ([]*ResRow, []*SvcRow) {
	offBy := map[string][]*Kind{}
	for _, k := range official {
		offBy[strings.ToLower(k.ARMType)] = append(offBy[strings.ToLower(k.ARMType)], k)
	}
	oursBy := map[string][]*Kind{}
	for _, k := range ours {
		if k.Outside == "" {
			oursBy[strings.ToLower(k.ARMType)] = append(oursBy[strings.ToLower(k.ARMType)], k)
		}
	}
	var rows []*ResRow
	keys := sortedKeys(res)
	for _, key := range keys {
		r := res[key]
		svc := nsName[strings.ToLower(r.NS)]
		row := &ResRow{Service: svc, Resource: canonType(r.Type, svc)}
		offK, oursK := offBy[key], oursBy[key]
		row.OfficialKinds, row.OursKinds = kindNames(offK), kindNames(oursK)
		for _, k := range oursK {
			if k.FromPR != "" {
				row.FromPR = true
			}
		}
		var attrKeys []string
		for k := range r.Attrs {
			attrKeys = append(attrKeys, k)
		}
		sort.Strings(attrKeys)
		for _, ak := range attrKeys {
			a := r.Attrs[ak]
			ar := &AttrRow{Service: svc, Resource: row.Resource, Attribute: a.Path, Type: a.Type, Required: a.Required}
			op, inO := coverage(offK, a.Path)
			up, inU := coverage(oursK, a.Path)
			switch {
			case inO && inU:
				ar.By = Both
			case inO:
				ar.By = Official
			case inU:
				ar.By = Ours
			default:
				ar.By = None
			}
			ar.OfficialPath, ar.OursPath = op, up
			switch ar.By {
			case Official, Both:
				row.Official++
			case Ours:
				row.OursBeyond++
			default:
				row.Unc++
			}
			row.Attrs = append(row.Attrs, ar)
		}
		row.Total = len(row.Attrs)
		row.Notes = specNotes(r, offK, oursK)
		rows = append(rows, row)
	}
	// Kinds whose resource the specs do not have: listed, not counted.
	rows = append(rows, outsideRows(res, nsName, official, ours)...)
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if !strings.EqualFold(a.Service, b.Service) {
			return strings.ToLower(a.Service) < strings.ToLower(b.Service)
		}
		return strings.ToLower(a.Resource) < strings.ToLower(b.Resource)
	})
	svcs := map[string]*SvcRow{}
	for _, row := range rows {
		if row.Outside {
			continue
		}
		s := svcs[row.Service]
		if s == nil {
			s = &SvcRow{Service: row.Service}
			svcs[row.Service] = s
		}
		s.ResTotal++
		if len(row.OfficialKinds) > 0 {
			s.ResOfficial++
		} else if len(row.OursKinds) > 0 {
			s.ResOursBeyond++
		}
		s.AttrTotal += row.Total
		s.AttrOfficial += row.Official
		s.AttrOursBeyond += row.OursBeyond
		s.AttrUncovered += row.Unc
	}
	var out []*SvcRow
	for _, s := range svcs {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Service) < strings.ToLower(out[j].Service) })
	return rows, out
}

func coverage(ks []*Kind, p string) (string, bool) {
	for _, k := range ks {
		if v, ok := k.Wire.covers(p); ok {
			return v, true
		}
	}
	return "", false
}

func kindNames(ks []*Kind) []string {
	var out []string
	for _, k := range ks {
		out = append(out, k.name())
	}
	sort.Strings(out)
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func canonType(t, svc string) string {
	if i := strings.Index(t, "/"); i > 0 {
		return svc + t[i:]
	}
	return t
}

func specNotes(r *Resource, offK, oursK []*Kind) []string {
	var n []string
	apis := sortedKeys(r.APIs)
	n = append(n, "spec_api="+strings.Join(apis, ","))
	if r.Preview {
		n = append(n, "spec-preview-only")
	}
	if r.Fallbck {
		n = append(n, "not-in-latest-spec-version")
	}
	lag := func(role string, ks []*Kind) {
		if len(ks) == 0 {
			return
		}
		var vs []string
		behind := false
		for _, k := range ks {
			vs = append(vs, k.API)
			if !r.APIs[k.API] {
				behind = true
			}
		}
		sort.Strings(vs)
		n = append(n, role+"_api="+strings.Join(uniq(vs), ","))
		if behind {
			n = append(n, role+"-api-differs-from-spec")
		}
	}
	lag("official", offK)
	lag("ours", oursK)
	for _, k := range oursK {
		if k.FromPR != "" {
			n = append(n, "ours-copied-from="+k.FromPR)
		}
	}
	return n
}

func uniq(s []string) []string {
	var out []string
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// outsideRows lists kinds that cover something the specs do not have.
func outsideRows(res map[string]*Resource, nsName map[string]string, official, ours []*Kind) []*ResRow {
	var rows []*ResRow
	byRes := map[string]*ResRow{}
	add := func(k *Kind, role string) {
		resource := k.Outside
		unmapped := false
		if resource == "" {
			if _, ok := res[strings.ToLower(k.ARMType)]; ok {
				return
			}
			resource, unmapped = k.ARMType, true
		}
		ns := resource
		if i := strings.Index(ns, "/"); i > 0 {
			ns = ns[:i]
		}
		svc := nsName[strings.ToLower(ns)]
		if svc == "" {
			svc = ns
		}
		resource = canonType(resource, svc)
		key := strings.ToLower(resource)
		row := byRes[key]
		if row == nil {
			row = &ResRow{Service: svc, Resource: resource, Outside: true, Unmapped: unmapped}
			if unmapped {
				row.Notes = []string{"outside-denominator", "unmapped=no-spec-match"}
			} else {
				row.Notes = []string{"outside-denominator"}
			}
			byRes[key] = row
			rows = append(rows, row)
		}
		if role == "official" {
			row.OfficialKinds = append(row.OfficialKinds, k.name())
		} else {
			row.OursKinds = append(row.OursKinds, k.name())
			if k.FromPR != "" {
				row.FromPR = true
			}
		}
		if k.Note != "" {
			row.Notes = append(row.Notes, k.Note)
		}
		if k.API != "" {
			row.Notes = append(row.Notes, role+"_api="+k.API)
		}
		var leaves []string
		for l := range k.Wire.Leaves {
			leaves = append(leaves, l)
		}
		sort.Strings(leaves)
		for _, l := range leaves {
			ar := &AttrRow{Service: svc, Resource: resource, Attribute: k.Wire.Leaves[l], Type: "", By: Covered(role)}
			if role == "official" {
				ar.OfficialPath = k.Wire.Leaves[l]
			} else {
				ar.OursPath = k.Wire.Leaves[l]
			}
			row.Attrs = append(row.Attrs, ar)
		}
	}
	for _, k := range official {
		add(k, "official")
	}
	for _, k := range ours {
		add(k, "ours")
	}
	for _, row := range rows {
		sort.Strings(row.OfficialKinds)
		sort.Strings(row.OursKinds)
		row.Notes = uniq2(row.Notes)
		sort.SliceStable(row.Attrs, func(i, j int) bool {
			return strings.ToLower(row.Attrs[i].Attribute) < strings.ToLower(row.Attrs[j].Attribute)
		})
		row.Total = len(row.Attrs)
		for _, a := range row.Attrs {
			if a.By == Official {
				row.Official++
			} else {
				row.OursBeyond++
			}
		}
	}
	return rows
}

func uniq2(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
