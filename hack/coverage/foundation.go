// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// avmFile is docs/coverage/avm-need.csv: how many of the Azure Verified
// Modules read for the catalog (highfivelabs/catalog docs/azure.md) need a
// resource type. Written by hand; a resource type missing from it is 0.
const avmFile = "docs/coverage/avm-need.csv"

type avmNeed struct {
	modules int
	named   string
}

func readAVM(root string, rows []*ResRow) (map[string]avmNeed, error) {
	f, err := os.Open(filepath.Join(root, avmFile))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", avmFile, err)
	}
	if len(recs) == 0 || strings.Join(recs[0], ",") != "resource,modules,named" {
		return nil, fmt.Errorf("%s: the header must be resource,modules,named", avmFile)
	}
	types := map[string]bool{}
	for _, r := range rows {
		types[strings.ToLower(r.Resource)] = true
	}
	out := map[string]avmNeed{}
	for i, r := range recs[1:] {
		if len(r) != 3 {
			return nil, fmt.Errorf("%s: line %d has %d fields, want 3", avmFile, i+2, len(r))
		}
		n, err := strconv.Atoi(r[1])
		if err != nil || n < 0 {
			return nil, fmt.Errorf("%s: line %d: modules %q is not a count", avmFile, i+2, r[1])
		}
		k := strings.ToLower(r[0])
		if !types[k] {
			return nil, fmt.Errorf("%s: line %d: %q is not a resource in resources.csv", avmFile, i+2, r[0])
		}
		out[k] = avmNeed{n, r[2]}
	}
	return out, nil
}

// buildRoute says how a missing resource could come, from where it can be
// created. ASO's generator handles a resource group's resources and their
// children (the owner is a resource group or a parent kind); a tenant- or
// subscription-scoped resource has precedent in ASO's own (the subscription
// Alias, the ResourceGroup); an extension resource, created on whatever its
// path names, has precedent in ASO's DiagnosticSetting and RoleAssignment
// (owner: any resource or an ARM ID). A resource that can be created at
// several scopes, or at a management group, is one type at several paths,
// which ASO's generator needs a design decision on (issue #3756): hand-written,
// as ManagementLock was.
func buildRoute(r *Resource) string {
	if r == nil {
		return "?"
	}
	var sc []string
	for k := range r.Scopes {
		sc = append(sc, k)
	}
	switch {
	case len(sc) > 1 || r.Scopes["managementGroup"]:
		return "hand-written (one type at several scopes, as ManagementLock)"
	case r.Scopes["extension"]:
		return "generator, arbitrary owner (as ASO's DiagnosticSetting)"
	case r.Scopes["tenant"] || r.Scopes["subscription"]:
		return "generator, tenant or subscription owner (as ASO's Alias)"
	}
	return "generator"
}

func scopeList(r *Resource) string {
	if r == nil {
		return ""
	}
	return strings.Join(sortedKeys(r.Scopes), ",")
}

func sizeOf(n int) string {
	switch {
	case n <= 25:
		return fmt.Sprintf("S (%d)", n)
	case n <= 80:
		return fmt.Sprintf("M (%d)", n)
	}
	return fmt.Sprintf("L (%d)", n)
}

func shortKinds(ks []string) string {
	var out []string
	for _, k := range ks {
		out = append(out, "`"+strings.Replace(strings.Replace(k, ".azure.hfl.dev", "", 1), ".azure.com", "", 1)+"`")
	}
	return strings.Join(out, ", ")
}

// foundationTable lists every resource of the platform-foundation and
// security-adjacent categories, most important first: the order of the
// services in priority.csv, then what is missing before what is covered, then
// the modules that need it, then the smaller first.
func foundationTable(rows []*ResRow, res map[string]*Resource, prio Priority, avm map[string]avmNeed, cats []*CategoryRow) string {
	var b strings.Builder
	b.WriteString("\n| Category | Services | Resources | Attributes | Official | Ours beyond | Uncovered |\n| --- | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, c := range cats {
		fmt.Fprintf(&b, "| %s | %d | %s | %s | %s%% | %s%% | %s%% |\n", c.Category, len(c.Services), commas(c.ResTotal), commas(c.AttrTotal),
			pct(c.AttrOfficial, c.AttrTotal), pct(c.AttrOursBeyond, c.AttrTotal), pct(c.AttrUncovered, c.AttrTotal))
	}
	for _, cat := range categories[:2] {
		var sel []*ResRow
		for _, r := range rows {
			if !r.Outside && prio.category(r.Resource, r.Service) == cat {
				sel = append(sel, r)
			}
		}
		status := func(r *ResRow) int {
			switch {
			case len(r.OfficialKinds)+len(r.OursKinds) == 0:
				return 0
			case r.Unc > 0:
				return 1
			}
			return 2
		}
		sort.SliceStable(sel, func(i, j int) bool {
			a, c := sel[i], sel[j]
			if oa, oc := prio.order(a.Service), prio.order(c.Service); oa != oc {
				return oa < oc
			}
			if sa, sc := status(a), status(c); sa != sc {
				return sa < sc
			}
			ma, mc := avm[strings.ToLower(a.Resource)].modules, avm[strings.ToLower(c.Resource)].modules
			if ma != mc {
				return ma > mc
			}
			if a.Total != c.Total {
				return a.Total < c.Total
			}
			return strings.ToLower(a.Resource) < strings.ToLower(c.Resource)
		})
		var gaps, partial, covered int
		for _, r := range sel {
			switch status(r) {
			case 0:
				gaps++
			case 1:
				partial++
			default:
				covered++
			}
		}
		fmt.Fprintf(&b, "\n### %s: %d resources (%d with no kind, %d partly covered, %d covered)\n\n", cat, len(sel), gaps, partial, covered)
		b.WriteString("| # | Resource | Status | ASO | Ours | Attributes: official / ours / uncovered | Scope | Route | Size | AVMs |\n| ---: | --- | --- | --- | --- | --- | --- | --- | --- | ---: |\n")
		for i, r := range sel {
			st := "covered"
			route := "-"
			size := "-"
			rr := res[strings.ToLower(r.Resource)]
			switch status(r) {
			case 0:
				st = "no kind"
				route = buildRoute(rr)
				size = sizeOf(r.Total)
			case 1:
				st = fmt.Sprintf("partial, %d of %d missing", r.Unc, r.Total)
				route = "extend: newer API version or property config"
				size = sizeOf(r.Unc)
				for _, n := range r.Notes {
					if n == "official-api-differs-from-spec" {
						route = "extend: ASO's API version is older than the spec's"
					}
				}
			}
			fmt.Fprintf(&b, "| %d | `%s` | %s | %s | %s | %d / %d / %d | %s | %s | %s | %d |\n", i+1, r.Resource, st,
				shortKinds(r.OfficialKinds), shortKinds(r.OursKinds), r.Official, r.OursBeyond, r.Unc, scopeList(rr), route, size,
				avm[strings.ToLower(r.Resource)].modules)
		}
	}
	return b.String()
}
