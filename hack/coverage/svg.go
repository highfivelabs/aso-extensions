// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"strings"
)

// The colours read on GitHub's light and dark pages alike: blue and green
// with white text, a light grey with dark text.
const (
	colOfficial = "#0969da"
	colOurs     = "#1a7f37"
	colNone     = "#d0d7de"
)

type segs struct{ official, ours, none int }

func (s segs) total() int { return s.official + s.ours + s.none }

// widths splits width into three parts in proportion to the segments; the
// parts add up to width.
func (s segs) widths(width float64) (o, u, n float64) {
	t := float64(s.total())
	if t == 0 {
		return 0, 0, width
	}
	o = width * float64(s.official) / t
	u = width * float64(s.ours) / t
	n = width - o - u
	return
}

func f1(v float64) string { return fmt.Sprintf("%.1f", v) }

func escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

func segLabel(x, w float64, text, fill string) string {
	if w < 36 {
		return ""
	}
	return fmt.Sprintf(`<text x="%s" y="%s" text-anchor="middle" fill="%s" class="s">%s</text>`, f1(x+w/2), "%Y%", fill, text)
}

func bar(x, y, width, h float64, s segs, id string) string {
	o, u, n := s.widths(width)
	var b strings.Builder
	fmt.Fprintf(&b, `<clipPath id="%s"><rect x="%s" y="%s" width="%s" height="%s" rx="3"/></clipPath>`, id, f1(x), f1(y), f1(width), f1(h))
	fmt.Fprintf(&b, `<g clip-path="url(#%s)">`, id)
	fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s" fill="%s"/>`, f1(x), f1(y), f1(o), f1(h), colOfficial)
	fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s" fill="%s"/>`, f1(x+o), f1(y), f1(u), f1(h), colOurs)
	fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s" fill="%s"/>`, f1(x+o+u), f1(y), f1(n), f1(h), colNone)
	b.WriteString(`</g>`)
	ty := f1(y + h/2 + 4)
	t := float64(s.total())
	if t > 0 {
		for _, l := range []string{
			segLabel(x, o, pct(s.official, s.total())+"%", "#fff"),
			segLabel(x+o, u, pct(s.ours, s.total())+"%", "#fff"),
			segLabel(x+o+u, n, pct(s.none, s.total())+"%", "#1f2328"),
		} {
			b.WriteString(strings.ReplaceAll(l, "%Y%", ty))
		}
	}
	return b.String()
}

// The text outside the bars is one mid-tone grey: an SVG shown as an image
// follows the system's colour scheme, not the GitHub theme chosen on the
// page, so a scheme-dependent colour can land on the wrong background.
const svgStyle = `<style>text{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Helvetica,Arial,sans-serif}.s{font-size:11px;font-weight:600}.t{font-size:11px;fill:#7d8590}.h{font-size:12px;font-weight:600;fill:#7d8590}</style>`

// serviceSVG is the bar of one service: 360 by 22.
func serviceSVG(name string, s segs) string {
	label := fmt.Sprintf("%s: %s%% official, %s%% ours beyond official, %s%% uncovered, of %d attributes",
		name, pct(s.official, s.total()), pct(s.ours, s.total()), pct(s.none, s.total()), s.total())
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="360" height="22" viewBox="0 0 360 22" role="img" aria-label="%s">`, escape(label))
	fmt.Fprintf(&b, `<title>%s</title>`, escape(label))
	b.WriteString(svgStyle)
	b.WriteString(bar(0, 3, 250, 16, s, "c"))
	fmt.Fprintf(&b, `<text x="258" y="15" class="t">%s%% covered</text>`, pct(s.official+s.ours, s.total()))
	b.WriteString(`</svg>` + "\n")
	return b.String()
}

func legend(b *strings.Builder, y int, s segs) {
	items := []struct {
		col, text string
		x         int
	}{
		{colOfficial, "official " + pct(s.official, s.total()) + "%", 0},
		{colOurs, "ours beyond official " + pct(s.ours, s.total()) + "%", 190},
		{colNone, "uncovered " + pct(s.none, s.total()) + "%", 430},
	}
	for _, l := range items {
		fmt.Fprintf(b, `<rect x="%d" y="%d" width="10" height="10" rx="2" fill="%s"/>`, l.x, y, l.col)
		fmt.Fprintf(b, `<text x="%d" y="%d" class="t">%s</text>`, l.x+15, y+9, escape(l.text))
	}
}

// totalSVG is the bars over everything, by attributes and by resources, with
// a legend and, when one service is a large part of the whole, a note: 640 wide.
func totalSVG(title string, attrs, resources segs, note string) string {
	label := fmt.Sprintf("%s: by attributes %s%% official, %s%% ours beyond official, %s%% uncovered, of %d; by resources %s%% official, %s%% ours beyond official, %s%% uncovered, of %d",
		title, pct(attrs.official, attrs.total()), pct(attrs.ours, attrs.total()), pct(attrs.none, attrs.total()), attrs.total(),
		pct(resources.official, resources.total()), pct(resources.ours, resources.total()), pct(resources.none, resources.total()), resources.total())
	height := 108
	if note != "" {
		height = 126
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="640" height="%d" viewBox="0 0 640 %d" role="img" aria-label="%s">`, height, height, escape(label))
	fmt.Fprintf(&b, `<title>%s</title>`, escape(label))
	b.WriteString(svgStyle)
	fmt.Fprintf(&b, `<text x="0" y="12" class="h">%s</text>`, escape(title))
	fmt.Fprintf(&b, `<text x="640" y="12" text-anchor="end" class="t">%s%% of attributes, %s%% of resources covered</text>`,
		pct(attrs.official+attrs.ours, attrs.total()), pct(resources.official+resources.ours, resources.total()))
	fmt.Fprintf(&b, `<text x="0" y="31" class="t">attributes (%s)</text>`, commas(attrs.total()))
	b.WriteString(bar(0, 36, 640, 18, attrs, "a"))
	fmt.Fprintf(&b, `<text x="0" y="68" class="t">resources (%s)</text>`, commas(resources.total()))
	b.WriteString(bar(0, 73, 640, 18, resources, "r"))
	legend(&b, 98, attrs)
	if note != "" {
		fmt.Fprintf(&b, `<text x="0" y="120" class="t">%s</text>`, escape(note))
	}
	b.WriteString(`</svg>` + "\n")
	return b.String()
}

type groupBar struct {
	label, sub string
	s          segs
}

// groupSVG is one bar of attributes per group (a tier, a category), with a
// legend over the whole.
func groupSVG(title string, groups []groupBar) string {
	var all []string
	var whole segs
	for _, g := range groups {
		all = append(all, fmt.Sprintf("%s, %s: %s%% official, %s%% ours beyond official, %s%% uncovered, of %d attributes",
			g.label, g.sub, pct(g.s.official, g.s.total()), pct(g.s.ours, g.s.total()), pct(g.s.none, g.s.total()), g.s.total()))
		whole.official += g.s.official
		whole.ours += g.s.ours
		whole.none += g.s.none
	}
	label := title + ". " + strings.Join(all, "; ")
	height := len(groups)*30 + 28
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="640" height="%d" viewBox="0 0 640 %d" role="img" aria-label="%s">`, height, height, escape(label))
	fmt.Fprintf(&b, `<title>%s</title>`, escape(label))
	b.WriteString(svgStyle)
	for i, g := range groups {
		y := float64(i * 30)
		fmt.Fprintf(&b, `<text x="0" y="%s" class="t">%s</text>`, f1(y+16), escape(g.label))
		fmt.Fprintf(&b, `<text x="0" y="%s" class="t" opacity="0.7">%s</text>`, f1(y+27), escape(g.sub))
		b.WriteString(bar(190, y+4, 450, 18, g.s, fmt.Sprintf("g%d", i)))
	}
	legend(&b, len(groups)*30+10, whole)
	b.WriteString(`</svg>` + "\n")
	return b.String()
}

// tiersSVG is one bar of attributes per tier.
func tiersSVG(tiers []*TierRow) string {
	names := []string{"tier 1: foundation", "tier 2: security, tracked apps", "tier 3: the rest"}
	var gs []groupBar
	for i, t := range tiers {
		gs = append(gs, groupBar{names[i], fmt.Sprintf("%d services, %s attributes", len(t.Services), commas(t.AttrTotal)), attrSegs(&t.SvcRow)})
	}
	return groupSVG("Coverage by tier", gs)
}

// categoriesSVG is one bar of attributes per category.
func categoriesSVG(cs []*CategoryRow) string {
	var gs []groupBar
	for _, c := range cs {
		gs = append(gs, groupBar{c.Category, fmt.Sprintf("%d services, %s attributes", len(c.Services), commas(c.AttrTotal)), attrSegs(&c.SvcRow)})
	}
	return groupSVG("Coverage by category", gs)
}
