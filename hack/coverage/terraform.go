// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The Terraform comparison: how much of the same denominator the azurerm
// provider's writable attributes reach, an estimate made in the catalog
// repository (docs/terraform-leverage.md, "Method and error") and vendored
// here at a pinned commit, because that repository is private and a check
// must not need credentials. The files are byte-identical copies, verified
// against the digests in sources.yaml.

const (
	colTerraform  = "#7b42bc"
	colTerraformL = "#1f2328" // the L1 tick
	terraformDoc  = "https://github.com/highfivelabs/catalog/blob/%s/docs/terraform-leverage.md#method-and-error"
	azapiNote     = "azapi is not a bar: it takes any ARM type with a free-form body, so it covers everything by construction and no attribute as a typed schema."
)

// terraformSource is the terraform section of sources.yaml.
type terraformSource struct {
	Repo       string `yaml:"repo"`
	Commit     string `yaml:"commit"`
	CommitDate string `yaml:"commitDate"`
	Fetched    string `yaml:"fetched"`
	Provider   string `yaml:"provider"`
	Files      []struct {
		From   string `yaml:"from"`   // the path in the catalog repository
		Path   string `yaml:"path"`   // the vendored copy, relative to this repository's root
		SHA256 string `yaml:"sha256"` // of the vendored copy
	} `yaml:"files"`
}

// tfAgg is the sum of some rows of terraform.csv: attributes of the
// denominator, and those Terraform reaches by the strict (L1) and lenient
// (L2) rule.
type tfAgg struct{ total, l1, l2 int }

func (a *tfAgg) add(b tfAgg) { a.total, a.l1, a.l2 = a.total+b.total, a.l1+b.l1, a.l2+b.l2 }

// Terraform is terraform.csv for one provider, summed by service and by category.
type Terraform struct {
	Provider   string
	Commit     string
	ByService  map[string]tfAgg
	ByCategory map[string]tfAgg
	All        tfAgg
}

func (t *Terraform) doc() string { return fmt.Sprintf(terraformDoc, t.Commit) }

// readTerraform verifies the vendored files against their digests and reads
// terraform.csv.
func readTerraform(root string, src *terraformSource) (*Terraform, error) {
	if src.Commit == "" || src.Provider == "" || len(src.Files) == 0 {
		return nil, fmt.Errorf("docs/coverage/sources.yaml: the terraform section needs commit, provider and files")
	}
	var csvPath string
	for _, f := range src.Files {
		b, err := os.ReadFile(filepath.Join(root, f.Path))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != f.SHA256 {
			return nil, fmt.Errorf("%s: sha256 %s, docs/coverage/sources.yaml pins %s; copy it again from the catalog at %s (docs/coverage/README.md, \"Terraform\")",
				f.Path, got, f.SHA256, short(src.Commit))
		}
		if filepath.Base(f.Path) == "terraform.csv" {
			csvPath = f.Path
		}
	}
	if csvPath == "" {
		return nil, fmt.Errorf("docs/coverage/sources.yaml: the terraform section has no terraform.csv")
	}
	b, err := os.ReadFile(filepath.Join(root, csvPath))
	if err != nil {
		return nil, err
	}
	t, err := parseTerraform(b, src.Provider)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", csvPath, err)
	}
	t.Commit = src.Commit
	return t, nil
}

// parseTerraform sums the rows of one provider; a row is a (service,
// category) pair, so a service can have several.
func parseTerraform(b []byte, provider string) (*Terraform, error) {
	rows, err := csv.NewReader(bytes.NewReader(b)).ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("empty")
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[h] = i
	}
	for _, h := range []string{"service", "provider", "category", "attributes_total", "attributes_terraform_l1", "attributes_terraform_l2"} {
		if _, ok := col[h]; !ok {
			return nil, fmt.Errorf("no column %q", h)
		}
	}
	t := &Terraform{Provider: provider, ByService: map[string]tfAgg{}, ByCategory: map[string]tfAgg{}}
	for i, r := range rows[1:] {
		if r[col["provider"]] != provider {
			continue
		}
		var a tfAgg
		for _, f := range []struct {
			name string
			to   *int
		}{{"attributes_total", &a.total}, {"attributes_terraform_l1", &a.l1}, {"attributes_terraform_l2", &a.l2}} {
			n, err := strconv.Atoi(r[col[f.name]])
			if err != nil {
				return nil, fmt.Errorf("line %d: %s: %w", i+2, f.name, err)
			}
			*f.to = n
		}
		s := t.ByService[r[col["service"]]]
		s.add(a)
		t.ByService[r[col["service"]]] = s
		c := t.ByCategory[r[col["category"]]]
		c.add(a)
		t.ByCategory[r[col["category"]]] = c
		t.All.add(a)
	}
	if t.All.total == 0 {
		return nil, fmt.Errorf("no rows for provider %q", provider)
	}
	return t, nil
}

// service is the sum for one service, and whether terraform.csv has it.
func (t *Terraform) service(name string) (tfAgg, bool) {
	if t == nil {
		return tfAgg{}, false
	}
	a, ok := t.ByService[name]
	return a, ok
}

// category is the sum for one category (of resources, as the dashboard's).
func (t *Terraform) category(name string) (tfAgg, bool) {
	if t == nil {
		return tfAgg{}, false
	}
	a, ok := t.ByCategory[name]
	return a, ok
}

// all is the sum over everything, or nil without the Terraform input.
func (t *Terraform) all() *tfAgg {
	if t == nil {
		return nil
	}
	a := t.All
	return &a
}

func (t *Terraform) provider() string {
	if t == nil {
		return ""
	}
	return t.Provider
}

// services sums several services; those terraform.csv lacks are left out.
func (t *Terraform) services(names []string) (tfAgg, bool) {
	var a tfAgg
	found := false
	for _, n := range names {
		if s, ok := t.service(n); ok {
			a.add(s)
			found = true
		}
	}
	return a, found
}

// cell is a table cell: the L2 estimate, or a dash.
func (a tfAgg) cell(ok bool) string {
	if !ok || a.total == 0 {
		return "—"
	}
	return "~" + pct(a.l2, a.total) + "%"
}

// tfBar is the Terraform bar: a grey track, the L2 share in purple, and a
// dark tick at the L1 share.
func tfBar(x, y, width, h float64, a tfAgg, id string) string {
	var l1, l2 float64
	if a.total > 0 {
		l1 = width * float64(a.l1) / float64(a.total)
		l2 = width * float64(a.l2) / float64(a.total)
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<clipPath id="%s"><rect x="%s" y="%s" width="%s" height="%s" rx="2"/></clipPath>`, id, f1(x), f1(y), f1(width), f1(h))
	fmt.Fprintf(&b, `<g clip-path="url(#%s)">`, id)
	fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s" fill="%s"/>`, f1(x), f1(y), f1(width), f1(h), colNone)
	fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s" fill="%s"/>`, f1(x), f1(y), f1(l2), f1(h), colTerraform)
	b.WriteString(`</g>`)
	fmt.Fprintf(&b, `<rect x="%s" y="%s" width="2" height="%s" fill="%s"/>`, f1(x+l1-1), f1(y-1), f1(h+2), colTerraformL)
	return b.String()
}

// tfLabel describes the Terraform bar for a title or aria-label.
func tfLabel(provider string, a tfAgg) string {
	return fmt.Sprintf("Terraform %s, an estimate: %s%% (L2), %s%% by the strict rule (L1)", provider, pct(a.l2, a.total), pct(a.l1, a.total))
}

// tfLegend is the legend line of the Terraform bar, at y, 640 wide.
func tfLegend(b *strings.Builder, y int, provider string) {
	fmt.Fprintf(b, `<rect x="0" y="%d" width="10" height="10" rx="2" fill="%s"/>`, y, colTerraform)
	fmt.Fprintf(b, `<rect x="4" y="%d" width="2" height="12" fill="%s"/>`, y-1, colTerraformL)
	fmt.Fprintf(b, `<text x="15" y="%d" class="t">%s</text>`, y+9,
		escape("Terraform "+provider+", a separate bar: an estimate (L2), the tick the strict rule (L1); see the catalog's \"Method and error\""))
}
