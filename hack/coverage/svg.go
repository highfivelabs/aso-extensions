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

// serviceSVG is the bar of one service: 360 by 22, or by 34 with the
// Terraform bar under it when tf is not nil.
func serviceSVG(name string, s segs, tf *tfAgg, provider string) string {
	label := fmt.Sprintf("%s: %s%% official, %s%% ours beyond official, %s%% uncovered, of %d attributes",
		name, pct(s.official, s.total()), pct(s.ours, s.total()), pct(s.none, s.total()), s.total())
	height := 22
	if tf != nil {
		label += "; " + tfLabel(provider, *tf)
		height = 34
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="360" height="%d" viewBox="0 0 360 %d" role="img" aria-label="%s">`, height, height, escape(label))
	fmt.Fprintf(&b, `<title>%s</title>`, escape(label))
	b.WriteString(svgStyle)
	b.WriteString(bar(0, 3, 250, 16, s, "c"))
	fmt.Fprintf(&b, `<text x="258" y="15" class="t">%s%% covered</text>`, pct(s.official+s.ours, s.total()))
	if tf != nil {
		b.WriteString(tfBar(0, 23, 250, 8, *tf, "tf"))
		fmt.Fprintf(&b, `<text x="258" y="31" class="t">Terraform ~%s%%</text>`, pct(tf.l2, tf.total))
	}
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
// a legend and, when one service is a large part of the whole, a note: 640
// wide. With tf, a Terraform bar of attributes follows the two.
func totalSVG(title string, attrs, resources segs, note string, tf *tfAgg, provider string) string {
	label := fmt.Sprintf("%s: by attributes %s%% official, %s%% ours beyond official, %s%% uncovered, of %d; by resources %s%% official, %s%% ours beyond official, %s%% uncovered, of %d",
		title, pct(attrs.official, attrs.total()), pct(attrs.ours, attrs.total()), pct(attrs.none, attrs.total()), attrs.total(),
		pct(resources.official, resources.total()), pct(resources.ours, resources.total()), pct(resources.none, resources.total()), resources.total())
	extra := 0
	if tf != nil {
		label += "; " + tfLabel(provider, *tf) + ". " + azapiNote
		extra = 72
	}
	height := 108 + extra
	if note != "" {
		height += 18
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
	y := 98
	if tf != nil {
		fmt.Fprintf(&b, `<text x="0" y="105" class="t">%s</text>`, escape(fmt.Sprintf("Terraform %s, attributes (%s), an estimate: %s%%, %s%% by the strict rule",
			provider, commas(tf.total), pct(tf.l2, tf.total), pct(tf.l1, tf.total))))
		b.WriteString(tfBar(0, 110, 640, 12, *tf, "tf"))
		legend(&b, 134, attrs)
		tfLegend(&b, 150, provider)
		fmt.Fprintf(&b, `<text x="0" y="175" class="t">%s</text>`, escape(azapiNote))
		y = 170
	} else {
		legend(&b, y, attrs)
	}
	if note != "" {
		fmt.Fprintf(&b, `<text x="0" y="%d" class="t">%s</text>`, y+22, escape(note))
	}
	b.WriteString(`</svg>` + "\n")
	return b.String()
}

type groupBar struct {
	label, sub string
	s          segs
	tf         *tfAgg // the Terraform bar under it, or none
}

// groupSVG is one bar of attributes per group (a tier, a category), with a
// legend over the whole. A group with a Terraform figure has its bar under
// the coverage bar.
func groupSVG(title, provider string, groups []groupBar) string {
	var all []string
	var whole segs
	rowH, withTF := 30, false
	for _, g := range groups {
		l := fmt.Sprintf("%s, %s: %s%% official, %s%% ours beyond official, %s%% uncovered, of %d attributes",
			g.label, g.sub, pct(g.s.official, g.s.total()), pct(g.s.ours, g.s.total()), pct(g.s.none, g.s.total()), g.s.total())
		if g.tf != nil {
			l += "; " + tfLabel(provider, *g.tf)
			rowH, withTF = 40, true
		}
		all = append(all, l)
		whole.official += g.s.official
		whole.ours += g.s.ours
		whole.none += g.s.none
	}
	label := title + ". " + strings.Join(all, "; ")
	height := len(groups)*rowH + 28
	if withTF {
		height += 16
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="640" height="%d" viewBox="0 0 640 %d" role="img" aria-label="%s">`, height, height, escape(label))
	fmt.Fprintf(&b, `<title>%s</title>`, escape(label))
	b.WriteString(svgStyle)
	for i, g := range groups {
		y := float64(i * rowH)
		fmt.Fprintf(&b, `<text x="0" y="%s" class="t">%s</text>`, f1(y+16), escape(g.label))
		fmt.Fprintf(&b, `<text x="0" y="%s" class="t" opacity="0.7">%s</text>`, f1(y+27), escape(g.sub))
		b.WriteString(bar(190, y+4, 450, 18, g.s, fmt.Sprintf("g%d", i)))
		if g.tf != nil {
			fmt.Fprintf(&b, `<text x="0" y="%s" class="t" opacity="0.7">%s</text>`, f1(y+38), escape("Terraform ~"+pct(g.tf.l2, g.tf.total)+"%"))
			b.WriteString(tfBar(190, y+26, 450, 8, *g.tf, fmt.Sprintf("t%d", i)))
		}
	}
	legend(&b, len(groups)*rowH+10, whole)
	if withTF {
		tfLegend(&b, len(groups)*rowH+26, provider)
	}
	b.WriteString(`</svg>` + "\n")
	return b.String()
}

// tiersSVG is one bar of attributes per tier.
func tiersSVG(tiers []*TierRow, tf *Terraform) string {
	names := []string{"tier 1: foundation", "tier 2: security, tracked apps", "tier 3: the rest"}
	var gs []groupBar
	for i, t := range tiers {
		g := groupBar{names[i], fmt.Sprintf("%d services, %s attributes", len(t.Services), commas(t.AttrTotal)), attrSegs(&t.SvcRow), nil}
		if a, ok := tf.services(t.Services); ok {
			g.tf = &a
		}
		gs = append(gs, g)
	}
	return groupSVG("Coverage by tier", tf.provider(), gs)
}

// categoriesSVG is one bar of attributes per category.
func categoriesSVG(cs []*CategoryRow, tf *Terraform) string {
	var gs []groupBar
	for _, c := range cs {
		g := groupBar{c.Category, fmt.Sprintf("%d services, %s attributes", len(c.Services), commas(c.AttrTotal)), attrSegs(&c.SvcRow), nil}
		if a, ok := tf.category(c.Category); ok {
			g.tf = &a
		}
		gs = append(gs, g)
	}
	return groupSVG("Coverage by category", tf.provider(), gs)
}
