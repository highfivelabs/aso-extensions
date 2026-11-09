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

// Priority maps a lower-case service name to its tier (1, 2 or 3).
type Priority map[string]int

func (p Priority) tier(service string) int {
	if t, ok := p[strings.ToLower(service)]; ok {
		return t
	}
	return 3
}

func readPriority(root string, svcs []*SvcRow) (Priority, error) {
	f, err := os.Open(filepath.Join(root, priorityFile))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", priorityFile, err)
	}
	if len(rows) == 0 || strings.Join(rows[0], ",") != "service,tier,rationale,source" {
		return nil, fmt.Errorf("%s: the header must be service,tier,rationale,source", priorityFile)
	}
	known := map[string]bool{}
	for _, s := range svcs {
		known[strings.ToLower(s.Service)] = true
	}
	p := Priority{}
	for i, r := range rows[1:] {
		if len(r) != 4 {
			return nil, fmt.Errorf("%s: line %d has %d fields, want 4", priorityFile, i+2, len(r))
		}
		t, err := strconv.Atoi(r[1])
		if err != nil || t < 1 || t > 3 {
			return nil, fmt.Errorf("%s: line %d: tier %q is not 1, 2 or 3", priorityFile, i+2, r[1])
		}
		k := strings.ToLower(r[0])
		if !known[k] {
			return nil, fmt.Errorf("%s: line %d: %q is not a service in services.csv", priorityFile, i+2, r[0])
		}
		if _, dup := p[k]; dup {
			return nil, fmt.Errorf("%s: line %d: %q is listed twice", priorityFile, i+2, r[0])
		}
		p[k] = t
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
