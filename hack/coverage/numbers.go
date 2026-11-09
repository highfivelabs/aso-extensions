// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"sort"
	"strings"
)

// numbersBlock is the part of docs/coverage/README.md that the tool writes:
// the figures that go with the method (what was read, what could not be
// mapped, how far ASO lags), so they cannot drift from the data.
func numbersBlock(src *sources, st *specStats, dc *DocsCheck, res map[string]*Resource, rows []*ResRow, svcs []*SvcRow, tiers []*TierRow, official, ours []*Kind) string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	var attrs, off, oursOnly, unc, resOff, resOursBeyond, resTotal int
	for _, s := range svcs {
		attrs += s.AttrTotal
		off += s.AttrOfficial
		oursOnly += s.AttrOursBeyond
		unc += s.AttrUncovered
		resTotal += s.ResTotal
		resOff += s.ResOfficial
		resOursBeyond += s.ResOursBeyond
	}
	preview, zero := 0, 0
	for _, r := range res {
		if r.Preview {
			preview++
		}
		if len(r.Attrs) == 0 {
			zero++
		}
	}
	p("**Inputs.** Specs `%s` (%s, fetched %s); ASO `%s` (%s, nearest release %s, fetched %s).\n\n",
		short(src.Specs.Commit), src.Specs.CommitDate, src.Specs.Fetched,
		short(src.ASO.Commit), src.ASO.CommitDate, src.ASO.NearestRelease, src.ASO.Fetched)
	p("**Denominator.** %d spec sets (%d with a stable version, %d preview-only) were read through %d files: %s resources in %d services, %s attributes. "+
		"%d of the resources are at a preview version only (no stable version of that file exists); %d have no request body and so no attributes.\n\n",
		st.Sets, st.StableSets, st.PreviewOnlySets, st.Files, commas(resTotal), len(svcs), commas(attrs), preview, zero)
	p("**Result.** Official %s attributes (%s%%) in %d resources; ours beyond official %s (%s%%) in %d further resources; uncovered %s (%s%%).\n\n",
		commas(off), pct(off, attrs), resOff, commas(oursOnly), pct(oursOnly, attrs), resOursBeyond, commas(unc), pct(unc, attrs))

	// ASO's kinds.
	matched, unmapped, lag := 0, 0, 0
	var unmappedNames []string
	groups := map[string]bool{}
	for _, k := range official {
		groups[k.Group] = true
	}
	for _, r := range rows {
		if len(r.OfficialKinds) == 0 {
			continue
		}
		if r.Unmapped {
			unmapped += len(r.OfficialKinds)
			unmappedNames = append(unmappedNames, r.OfficialKinds...)
			continue
		}
		matched++
		for _, n := range r.Notes {
			if n == "official-api-differs-from-spec" {
				lag++
			}
		}
	}
	p("**ASO.** %d kinds in %d API groups, read from their API types (the Go types the CRDs are generated from) and ARM wire types, one per ARM type, at the newest stable version ASO has. "+
		"%d are resources in the denominator; **unmapped: %d** (an ASO kind whose ARM type no spec read here has%s). "+
		"In %d of the %d matched resources ASO's API version is not the version of the denominator (it lags, or in a few cases leads); "+
		"for those the attributes are compared with the ASO version's own, so what only the newer version added counts as uncovered.\n\n",
		len(official), len(groups), matched, unmapped, listOrNone(unmappedNames), lag, matched)
	fb := len(st.FallbackTypes)
	p("**Older spec versions.** %d resource types that ASO or we cover are not in the latest version of their spec file and were read from an older one (`not-in-latest-spec-version` in `notes`)%s.\n\n",
		fb, listOrNone(st.FallbackTypes))

	p("**Tiers.** ")
	for i, t := range tiers {
		if i > 0 {
			p("; ")
		}
		p("tier %d has %d services, %s attributes, %s%% covered by official and %s%% more by ours", t.Tier, len(t.Services), commas(t.AttrTotal),
			pct(t.AttrOfficial, t.AttrTotal), pct(t.AttrOursBeyond, t.AttrTotal))
	}
	p(".\n\n")

	// Ours.
	p("**Ours.**\n\n| Kind | Resource | How mapped |\n| --- | --- | --- |\n")
	for _, k := range ours {
		how := "ARM wire type of the generated kind (`apis/arm`)"
		res := k.ARMType
		if k.Outside != "" {
			res, how = k.Outside, "outside the denominator, not in any percentage"
		} else if k.Version == "v1" {
			how = "by hand, `hack/coverage/ours.yaml` (CRD spec path to ARM attribute)"
		}
		p("| `%s` | `%s` | %s |\n", k.name(), res, how)
	}
	p("\n")

	// Outside the denominator.
	outside := 0
	for _, r := range rows {
		if r.Outside {
			outside++
		}
	}
	p("**Outside the denominator.** %d rows in `resources.csv` carry `notes=outside-denominator` and are in none of the percentages: our data-plane and database kinds above, and the %d ASO kinds that are unmapped.\n\n",
		outside, unmapped)

	// Docs cross-check.
	p("**ASO's own supported-resources documentation** (`docs/hugo/content/reference/*/_index.md` at the pin) lists %d kinds; %d are among the kinds read here, of which %d have the same newest API version in both. "+
		"Listed there and not read here: %d%s. Read here and not listed there: %d%s.\n",
		dc.DocKinds, dc.Matched, dc.SameVersion, len(dc.DocsOnly), listOrNone(dc.DocsOnly), len(dc.WireOnly), listOrNone(dc.WireOnly))
	if len(dc.VersionDiffs) > 0 {
		p("\nVersion differences: %s.\n", strings.Join(dc.VersionDiffs, "; "))
	}
	return b.String()
}

func short(s string) string { return s[:12] }

func listOrNone(l []string) string {
	if len(l) == 0 {
		return ""
	}
	l = append([]string(nil), l...)
	sort.Strings(l)
	if len(l) > 40 {
		return ": " + strings.Join(l[:40], ", ") + fmt.Sprintf(", and %d more", len(l)-40)
	}
	return ": " + strings.Join(l, ", ")
}
