// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// maxAttributesCSV is the size above which attributes.csv is split by service.
const maxAttributesCSV = 25 << 20

func writeCSV(rows [][]string) []byte {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.WriteAll(rows) // a bytes.Buffer does not fail
	return b.Bytes()
}

func servicesCSV(svcs []*SvcRow) []byte {
	rows := [][]string{{"service", "resources_total", "resources_official", "resources_ours_beyond",
		"attributes_total", "attributes_official", "attributes_ours_beyond", "attributes_uncovered",
		"pct_official", "pct_ours_beyond", "pct_uncovered"}}
	for _, s := range svcs {
		rows = append(rows, []string{s.Service, itoa(s.ResTotal), itoa(s.ResOfficial), itoa(s.ResOursBeyond),
			itoa(s.AttrTotal), itoa(s.AttrOfficial), itoa(s.AttrOursBeyond), itoa(s.AttrUncovered),
			pct(s.AttrOfficial, s.AttrTotal), pct(s.AttrOursBeyond, s.AttrTotal), pct(s.AttrUncovered, s.AttrTotal)})
	}
	return writeCSV(rows)
}

func resourcesCSV(res []*ResRow) []byte {
	rows := [][]string{{"service", "resource", "attributes_total", "official_kinds", "attributes_official",
		"ours_kinds", "attributes_ours_beyond", "attributes_uncovered", "ours_from_upstream_pr", "notes"}}
	for _, r := range res {
		rows = append(rows, []string{r.Service, r.Resource, itoa(r.Total), strings.Join(r.OfficialKinds, ";"),
			itoa(r.Official), strings.Join(r.OursKinds, ";"), itoa(r.OursBeyond), itoa(r.Unc),
			strconv.FormatBool(r.FromPR), strings.Join(r.Notes, "; ")})
	}
	return writeCSV(rows)
}

var attrHeader = []string{"service", "resource", "attribute", "type", "required", "covered_by", "official_path", "ours_path"}

func attrRows(res []*ResRow) [][]string {
	var rows [][]string
	for _, r := range res {
		for _, a := range r.Attrs {
			rows = append(rows, []string{a.Service, a.Resource, a.Attribute, a.Type, strconv.FormatBool(a.Required),
				string(a.By), a.OfficialPath, a.OursPath})
		}
	}
	return rows
}

func itoa(n int) string { return strconv.Itoa(n) }

// fileName makes a service name safe as a file name.
func fileName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		}
		return '_'
	}, s)
}

func resSegs(s *SvcRow) segs {
	return segs{s.ResOfficial, s.ResOursBeyond, s.ResTotal - s.ResOfficial - s.ResOursBeyond}
}

func attrSegs(s *SvcRow) segs { return segs{s.AttrOfficial, s.AttrOursBeyond, s.AttrUncovered} }

// dominance names the service that is a large part of the whole, if one is.
func dominance(svcs []*SvcRow) string {
	var total int
	var top *SvcRow
	for _, s := range svcs {
		total += s.AttrTotal
		if top == nil || s.AttrTotal > top.AttrTotal {
			top = s
		}
	}
	if top == nil || total == 0 || top.AttrTotal*10 < total {
		return ""
	}
	return fmt.Sprintf("%s is %s%% of all attributes (%s%% of its own covered); the other %d services share the rest.",
		top.Service, pct(top.AttrTotal, total), pct(top.AttrOfficial+top.AttrOursBeyond, top.AttrTotal), len(svcs)-1)
}

func sumSvcs(svcs []*SvcRow) *SvcRow {
	t := &SvcRow{}
	for _, s := range svcs {
		t.ResTotal += s.ResTotal
		t.ResOfficial += s.ResOfficial
		t.ResOursBeyond += s.ResOursBeyond
		t.AttrTotal += s.AttrTotal
		t.AttrOfficial += s.AttrOfficial
		t.AttrOursBeyond += s.AttrOursBeyond
		t.AttrUncovered += s.AttrUncovered
	}
	return t
}

// outputs makes every generated file: path (relative to docs/coverage) to content.
func outputs(res []*ResRow, svcs []*SvcRow, tiers []*TierRow) map[string][]byte {
	out := map[string][]byte{}
	out["services.csv"] = servicesCSV(svcs)
	out["resources.csv"] = resourcesCSV(res)
	out["tiers.csv"] = tiersCSV(tiers)
	all := append([][]string{attrHeader}, attrRows(res)...)
	whole := writeCSV(all)
	if len(whole) <= maxAttributesCSV {
		out["attributes.csv"] = whole
	} else {
		bySvc := map[string][]*ResRow{}
		for _, r := range res {
			bySvc[r.Service] = append(bySvc[r.Service], r)
		}
		for svc, rs := range bySvc {
			out["attributes/"+fileName(svc)+".csv"] = writeCSV(append([][]string{attrHeader}, attrRows(rs)...))
		}
	}
	for _, s := range svcs {
		out["bars/"+fileName(s.Service)+".svg"] = []byte(serviceSVG(s.Service, attrSegs(s)))
	}
	t := sumSvcs(svcs)
	out["bars/_total.svg"] = []byte(totalSVG("Azure Resource Manager API coverage", attrSegs(t), resSegs(t), dominance(svcs)))
	out["bars/_tiers.svg"] = []byte(tiersSVG(tiers))
	return out
}

// readmeSection is the Coverage section of the root README, between its markers.
func readmeSection(svcs []*SvcRow, tiers []*TierRow, prio Priority) string {
	total := sumSvcs(svcs)
	var b strings.Builder
	b.WriteString("![Azure coverage by attributes and by resources: official, ours beyond official, uncovered](docs/coverage/bars/_total.svg)\n\n")
	fmt.Fprintf(&b, "Of the %s writable attributes of the %s ARM resources in the Azure REST API specs, "+
		"ASO v2 covers %s%% and our kinds cover a further %s%%; %s%% are in neither. "+
		"Blue is ASO, green is what only we cover, grey is what neither does. ",
		commas(total.AttrTotal), commas(total.ResTotal),
		pct(total.AttrOfficial, total.AttrTotal), pct(total.AttrOursBeyond, total.AttrTotal), pct(total.AttrUncovered, total.AttrTotal))
	b.WriteString("Not every service matters alike, so the services are in three tiers by how much the foundational modules people migrate first use them " +
		"([`priority.csv`](docs/coverage/priority.csv)):\n\n")
	b.WriteString("![Coverage by tier](docs/coverage/bars/_tiers.svg)\n\n")
	b.WriteString("| Tier | Services | Official | Ours beyond | Uncovered |\n| --- | ---: | ---: | ---: | ---: |\n")
	for _, t := range tiers {
		fmt.Fprintf(&b, "| %d | %d | %s%% | %s%% | %s%% |\n", t.Tier, len(t.Services),
			pct(t.AttrOfficial, t.AttrTotal), pct(t.AttrOursBeyond, t.AttrTotal), pct(t.AttrUncovered, t.AttrTotal))
	}
	b.WriteString("\nBy service, tier first and then attributes covered (every tier 1 and 2 service, and any other with coverage); " +
		"method, caveats and the data (service, resource and attribute level) are in [`docs/coverage/`](docs/coverage/README.md), " +
		"all services in [`services.csv`](docs/coverage/services.csv).\n\n")
	sorted := sortedByTier(svcs, prio)
	shown := 0
	table := func(rows []*SvcRow) {
		b.WriteString("| Tier | Service | Coverage | Official | Ours beyond | Uncovered |\n| ---: | --- | --- | ---: | ---: | ---: |\n")
		for _, s := range rows {
			fmt.Fprintf(&b, "| %d | `%s` | ![%s](docs/coverage/bars/%s.svg) | %s%% | %s%% | %s%% |\n",
				prio.tier(s.Service), s.Service, s.Service, fileName(s.Service),
				pct(s.AttrOfficial, s.AttrTotal), pct(s.AttrOursBeyond, s.AttrTotal), pct(s.AttrUncovered, s.AttrTotal))
		}
	}
	for tier := 1; tier <= 3; tier++ {
		var rows []*SvcRow
		for _, s := range sorted {
			if prio.tier(s.Service) == tier && (tier < 3 || s.AttrOfficial+s.AttrOursBeyond > 0) {
				rows = append(rows, s)
			}
		}
		shown += len(rows)
		if len(rows) == 0 {
			continue
		}
		if tier == 1 {
			table(rows)
			continue
		}
		fmt.Fprintf(&b, "\n<details><summary>Tier %d: %d services</summary>\n\n", tier, len(rows))
		table(rows)
		b.WriteString("\n</details>\n")
	}
	fmt.Fprintf(&b, "\n%d other services in the specs have no coverage from either and are in tier 3.\n", len(svcs)-shown)
	return b.String()
}

// sortedByTier orders services by tier, then attributes covered (most first).
func sortedByTier(svcs []*SvcRow, prio Priority) []*SvcRow {
	out := append([]*SvcRow(nil), svcs...)
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := prio.tier(out[i].Service), prio.tier(out[j].Service)
		if ti != tj {
			return ti < tj
		}
		ci, cj := out[i].AttrOfficial+out[i].AttrOursBeyond, out[j].AttrOfficial+out[j].AttrOursBeyond
		if ci != cj {
			return ci > cj
		}
		return strings.ToLower(out[i].Service) < strings.ToLower(out[j].Service)
	})
	return out
}

func commas(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
