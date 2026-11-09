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

// priorityFile is docs/coverage/priority.csv. It is written by hand and read,
// never recomputed: a service missing from it is tier 3.
const priorityFile = "docs/coverage/priority.csv"

// Categories, in the order of importance to a platform team.
var categories = []string{"platform-foundation", "security-adjacent", "application", "other"}

// A row of priority.csv is a service ("Microsoft.Network") or a resource type
// or type prefix ("Microsoft.Network/ddosProtectionPlans"), which takes its
// own category and keeps its service's tier. The order of the rows is the
// order of importance, the foundation report's ranking.
type prioRow struct {
	tier     int
	category string
	order    int
}

// Priority is priority.csv, read.
type Priority struct {
	svc map[string]prioRow // lower-case service
	res map[string]prioRow // lower-case resource type or prefix
}

func (p Priority) tier(service string) int {
	if r, ok := p.svc[strings.ToLower(service)]; ok {
		return r.tier
	}
	return 3
}

func (p Priority) serviceCategory(service string) string {
	if r, ok := p.svc[strings.ToLower(service)]; ok {
		return r.category
	}
	return "other"
}

// order is where the service is in the file; services not in it follow.
func (p Priority) order(service string) int {
	if r, ok := p.svc[strings.ToLower(service)]; ok {
		return r.order
	}
	return 1 << 20
}

// category is the category of a resource: that of the longest row naming it
// or a prefix of it, else of its service.
func (p Priority) category(resource, service string) string {
	k := strings.ToLower(resource)
	for {
		if r, ok := p.res[k]; ok {
			return r.category
		}
		i := strings.LastIndex(k, "/")
		if i < 0 {
			break
		}
		k = k[:i]
	}
	return p.serviceCategory(service)
}

func validCategory(c string) bool {
	for _, x := range categories {
		if x == c {
			return true
		}
	}
	return false
}

func readPriority(root string, svcs []*SvcRow, rows []*ResRow) (Priority, error) {
	f, err := os.Open(filepath.Join(root, priorityFile))
	if err != nil {
		return Priority{}, err
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return Priority{}, fmt.Errorf("%s: %w", priorityFile, err)
	}
	if len(recs) == 0 || strings.Join(recs[0], ",") != "service,tier,rationale,source,category" {
		return Priority{}, fmt.Errorf("%s: the header must be service,tier,rationale,source,category", priorityFile)
	}
	known := map[string]bool{}
	for _, s := range svcs {
		known[strings.ToLower(s.Service)] = true
	}
	types := map[string]bool{}
	for _, r := range rows {
		if !r.Outside {
			types[strings.ToLower(r.Resource)] = true
		}
	}
	isTypeOrPrefix := func(k string) bool {
		for t := range types {
			if t == k || strings.HasPrefix(t, k+"/") {
				return true
			}
		}
		return false
	}
	p := Priority{svc: map[string]prioRow{}, res: map[string]prioRow{}}
	for i, r := range recs[1:] {
		line := i + 2
		if len(r) != 5 {
			return Priority{}, fmt.Errorf("%s: line %d has %d fields, want 5", priorityFile, line, len(r))
		}
		k := strings.ToLower(r[0])
		if !validCategory(r[4]) {
			return Priority{}, fmt.Errorf("%s: line %d: category %q is not one of %s", priorityFile, line, r[4], strings.Join(categories, ", "))
		}
		row := prioRow{category: r[4], order: i}
		if strings.Contains(k, "/") {
			if r[1] != "" {
				return Priority{}, fmt.Errorf("%s: line %d: a resource row takes its service's tier; leave tier empty", priorityFile, line)
			}
			if !isTypeOrPrefix(k) {
				return Priority{}, fmt.Errorf("%s: line %d: %q is not a resource type in resources.csv", priorityFile, line, r[0])
			}
			if _, dup := p.res[k]; dup {
				return Priority{}, fmt.Errorf("%s: line %d: %q is listed twice", priorityFile, line, r[0])
			}
			p.res[k] = row
			continue
		}
		t, err := strconv.Atoi(r[1])
		if err != nil || t < 1 || t > 3 {
			return Priority{}, fmt.Errorf("%s: line %d: tier %q is not 1, 2 or 3", priorityFile, line, r[1])
		}
		row.tier = t
		if !known[k] {
			return Priority{}, fmt.Errorf("%s: line %d: %q is not a service in services.csv", priorityFile, line, r[0])
		}
		if _, dup := p.svc[k]; dup {
			return Priority{}, fmt.Errorf("%s: line %d: %q is listed twice", priorityFile, line, r[0])
		}
		p.svc[k] = row
	}
	return p, nil
}

// TierRow is the rollup of the services of one tier.
type TierRow struct {
	Tier     int
	Services []string
	SvcRow
}

func tierRollup(svcs []*SvcRow, p Priority) []*TierRow {
	out := []*TierRow{{Tier: 1}, {Tier: 2}, {Tier: 3}}
	for _, s := range svcs {
		t := out[p.tier(s.Service)-1]
		t.Services = append(t.Services, s.Service)
		t.ResTotal += s.ResTotal
		t.ResOfficial += s.ResOfficial
		t.ResOursBeyond += s.ResOursBeyond
		t.AttrTotal += s.AttrTotal
		t.AttrOfficial += s.AttrOfficial
		t.AttrOursBeyond += s.AttrOursBeyond
		t.AttrUncovered += s.AttrUncovered
	}
	for _, t := range out {
		sort.Strings(t.Services)
	}
	return out
}

func tiersCSV(ts []*TierRow) []byte {
	rows := [][]string{{"tier", "services", "attributes_total", "attributes_official", "attributes_ours_beyond",
		"attributes_uncovered", "pct_official", "pct_ours_beyond", "pct_uncovered",
		"resources_total", "resources_official", "resources_ours_beyond"}}
	for _, t := range ts {
		rows = append(rows, []string{itoa(t.Tier), itoa(len(t.Services)), itoa(t.AttrTotal), itoa(t.AttrOfficial),
			itoa(t.AttrOursBeyond), itoa(t.AttrUncovered),
			pct(t.AttrOfficial, t.AttrTotal), pct(t.AttrOursBeyond, t.AttrTotal), pct(t.AttrUncovered, t.AttrTotal),
			itoa(t.ResTotal), itoa(t.ResOfficial), itoa(t.ResOursBeyond)})
	}
	return writeCSV(rows)
}

// CategoryRow is the rollup of the resources of one category.
type CategoryRow struct {
	Category string
	Services map[string]bool
	SvcRow
}

func categoryRollup(rows []*ResRow, p Priority) []*CategoryRow {
	var out []*CategoryRow
	for _, c := range categories {
		out = append(out, &CategoryRow{Category: c, Services: map[string]bool{}})
	}
	for _, r := range rows {
		if r.Outside {
			continue
		}
		c := out[indexOf(categories, p.category(r.Resource, r.Service))]
		c.Services[r.Service] = true
		c.ResTotal++
		if len(r.OfficialKinds) > 0 {
			c.ResOfficial++
		} else if len(r.OursKinds) > 0 {
			c.ResOursBeyond++
		}
		c.AttrTotal += r.Total
		c.AttrOfficial += r.Official
		c.AttrOursBeyond += r.OursBeyond
		c.AttrUncovered += r.Unc
	}
	return out
}

func indexOf(l []string, s string) int {
	for i, v := range l {
		if v == s {
			return i
		}
	}
	return len(l) - 1
}

func categoriesCSV(cs []*CategoryRow) []byte {
	rows := [][]string{{"category", "services", "resources_total", "resources_official", "resources_ours_beyond",
		"attributes_total", "attributes_official", "attributes_ours_beyond", "attributes_uncovered",
		"pct_official", "pct_ours_beyond", "pct_uncovered"}}
	for _, c := range cs {
		rows = append(rows, []string{c.Category, itoa(len(c.Services)), itoa(c.ResTotal), itoa(c.ResOfficial), itoa(c.ResOursBeyond),
			itoa(c.AttrTotal), itoa(c.AttrOfficial), itoa(c.AttrOursBeyond), itoa(c.AttrUncovered),
			pct(c.AttrOfficial, c.AttrTotal), pct(c.AttrOursBeyond, c.AttrTotal), pct(c.AttrUncovered, c.AttrTotal)})
	}
	return writeCSV(rows)
}
