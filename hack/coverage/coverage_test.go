// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const swagger = `{
 "swagger": "2.0",
 "paths": {
  "/subscriptions/{s}/resourceGroups/{rg}/providers/Microsoft.Foo/widgets/{name}": {
   "put": {"parameters": [{"in":"body","name":"p","schema":{"$ref":"#/definitions/Widget"}}]}
  },
  "/subscriptions/{s}/resourceGroups/{rg}/providers/Microsoft.Foo/widgets/{name}/start": {
   "put": {"parameters": [{"in":"body","name":"p","schema":{"$ref":"#/definitions/Widget"}}]}
  },
  "/subscriptions/{s}/providers/Microsoft.Foo/zones/{z}/{recordType}/{n}": {
   "put": {"parameters": [
     {"in":"path","name":"recordType","type":"string","enum":["A","TXT"]},
     {"in":"body","name":"p","schema":{"$ref":"#/definitions/Part"}}]}
  },
  "/subscriptions/{s}/providers/Microsoft.Foo/links/{l}/targets/{t}": {"put": {"parameters": []}},
  "/subscriptions/{s}/resourceGroups/{rg}/providers/Microsoft.Foo/widgets/{name}/parts/default": {
   "put": {"parameters": [{"in":"body","name":"p","schema":{"$ref":"#/definitions/Part"}}]}
  }
 },
 "definitions": {
  "Resource": {"properties": {"id": {"type":"string"}, "name": {"type":"string","readOnly":true}, "type": {"type":"string"}}},
  "Widget": {
   "allOf": [{"$ref":"#/definitions/Resource"}],
   "required": ["properties"],
   "properties": {
    "location": {"type":"string"},
    "tags": {"type":"object","additionalProperties":{"type":"string"}},
    "etag": {"type":"string","readOnly":true},
    "properties": {"$ref":"#/definitions/WidgetProps"}
   }
  },
  "WidgetProps": {
   "required": ["size"],
   "properties": {
    "size": {"type":"integer"},
    "state": {"type":"string","readOnly":true},
    "created": {"type":"string","x-ms-mutability":["read"]},
    "immutable": {"type":"string","x-ms-mutability":["read","create"]},
    "labels": {"type":"array","items":{"type":"string"}},
    "ports": {"type":"array","items":{"$ref":"#/definitions/Port"}},
    "byName": {"type":"object","additionalProperties":{"$ref":"#/definitions/Port"}},
    "free": {"type":"object"},
    "ids": {"type":"object","additionalProperties":{"$ref":"#/definitions/RO"}},
    "tree": {"$ref":"#/definitions/Node"},
    "action": {"$ref":"#/definitions/Action"}
   }
  },
  "RO": {"properties": {"principalId": {"type":"string","readOnly":true}}},
  "Port": {"properties": {"number": {"type":"integer"}, "id": {"type":"string","readOnly":true}}},
  "Node": {"properties": {"value": {"type":"string"}, "children": {"type":"array","items":{"$ref":"#/definitions/Node"}}}},
  "Action": {"discriminator":"kind","required":["kind"],"properties":{"kind":{"type":"string"}}},
  "Email": {"allOf":[{"$ref":"#/definitions/Action"}],"properties":{"to":{"type":"string"}},"x-ms-discriminator-value":"Email"},
  "Hook": {"allOf":[{"$ref":"#/definitions/Action"}],"properties":{"url":{"type":"string"}},"x-ms-discriminator-value":"Hook"},
  "Part": {"properties": {"properties": {"properties": {"weight": {"type":"number"}}}}}
 }
}`

func readTestDoc(t *testing.T) map[string]*Resource {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "spec"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spec", "foo.json"), []byte(swagger), 0o644); err != nil {
		t.Fatal(err)
	}
	l := newLoader(&gitCache{dir: dir})
	d, err := l.load("spec/foo.json")
	if err != nil {
		t.Fatal(err)
	}
	w := &walker{l: l, unresolved: map[string]bool{}}
	res := map[string]*Resource{}
	w.readDoc(d, "2024-01-01", false, "foo/Microsoft.Foo", res)
	return res
}

func attrPaths(r *Resource) []string {
	var out []string
	for _, a := range r.Attrs {
		out = append(out, a.Path)
	}
	sort.Strings(out)
	return out
}

func TestWidgetAttributes(t *testing.T) {
	res := readTestDoc(t)
	if got := sortedKeys(res); !reflect.DeepEqual(got, []string{"microsoft.foo/links/targets", "microsoft.foo/widgets", "microsoft.foo/widgets/parts", "microsoft.foo/zones/a", "microsoft.foo/zones/txt"}) {
		t.Fatalf("resources %v", got)
	}
	want := []string{
		"location", "tags{}",
		"properties.size", "properties.immutable", "properties.labels[]",
		"properties.ports[].number", "properties.byName{}.number", "properties.free", "properties.ids{}",
		"properties.tree.value", "properties.tree.children[]",
		"properties.action.kind", "properties.action.to", "properties.action.url",
	}
	sort.Strings(want)
	if got := attrPaths(res["microsoft.foo/widgets"]); !reflect.DeepEqual(got, want) {
		t.Fatalf("attributes\n got %v\nwant %v", got, want)
	}
	w := res["microsoft.foo/widgets"].Attrs
	if !w["properties.size"].Required || w["properties.immutable"].Required || w["location"].Required {
		t.Errorf("required: size %v immutable %v location %v", w["properties.size"].Required, w["properties.immutable"].Required, w["location"].Required)
	}
	// A recursive reference stops at the first repeat and stays an object.
	if w["properties.tree.children[]"].Type != "object" {
		t.Errorf("recursive leaf type %q", w["properties.tree.children[]"].Type)
	}
	if got := attrPaths(res["microsoft.foo/widgets/parts"]); !reflect.DeepEqual(got, []string{"properties.weight"}) {
		t.Errorf("parts: %v", got)
	}
}

func TestArmType(t *testing.T) {
	for _, c := range []struct{ path, ns, typ string }{
		{"/subscriptions/{s}/resourceGroups/{rg}/providers/Microsoft.Network/virtualNetworks/{v}", "Microsoft.Network", "Microsoft.Network/virtualNetworks"},
		{"/subscriptions/{s}/resourceGroups/{rg}/providers/Microsoft.Network/virtualNetworks/{v}/subnets/{n}", "Microsoft.Network", "Microsoft.Network/virtualNetworks/subnets"},
		{"/subscriptions/{s}/resourceGroups/{rg}/providers/Microsoft.Storage/storageAccounts/{a}/blobServices/default", "Microsoft.Storage", "Microsoft.Storage/storageAccounts/blobServices"},
		{"/{scope}/providers/Microsoft.Authorization/locks/{n}", "Microsoft.Authorization", "Microsoft.Authorization/locks"},
		{"/subscriptions/{s}/resourceGroups/{rg}/providers/Microsoft.Foo/widgets/{n}/start", "", ""},
		{"/subscriptions/{s}/providers/Microsoft.Foo/widgets", "", ""},
		{"/subscriptions/{s}/resourcegroups/{rg}", "Microsoft.Resources", "Microsoft.Resources/resourceGroups"},
		{"/subscriptions/{s}/resourceGroups/{rg}/providers/Microsoft.Foo/widgets/{n}?api-version=1", "Microsoft.Foo", "Microsoft.Foo/widgets"},
	} {
		ns, typ := armType(c.path)
		if ns != c.ns || typ != c.typ {
			t.Errorf("%s: got %q %q, want %q %q", c.path, ns, typ, c.ns, c.typ)
		}
	}
}

func TestFileSelection(t *testing.T) {
	// A delta-style set: each version holds the files that changed in it.
	s := &specSet{
		Stable: map[string][]string{
			"2023-01-01": {"x/stable/2023-01-01/a.json", "x/stable/2023-01-01/b.json"},
			"2024-05-01": {"x/stable/2024-05-01/b.json"},
		},
		Preview: map[string][]string{
			"2025-01-01-preview": {"x/preview/2025-01-01-preview/b.json", "x/preview/2025-01-01-preview/c.json"},
		},
	}
	got := map[string]string{}
	for _, f := range s.selection() {
		got[f.Path] = f.Ver
	}
	want := map[string]string{
		"x/stable/2023-01-01/a.json":          "2023-01-01",
		"x/stable/2024-05-01/b.json":          "2024-05-01", // stable beats a later preview
		"x/preview/2025-01-01-preview/c.json": "2025-01-01-preview",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("selection\n got %v\nwant %v", got, want)
	}
	if !newer("2024-01-01", false, "2025-01-01-preview", true) || newer("2024-01-01", false, "2025-01-01", false) {
		t.Error("newer: stable before preview, then by date")
	}
	if !wireVersionPreferred("v1api20230101", "v1api20240101preview") || wireVersionPreferred("v1api20230101", "v1api20240101") {
		t.Error("ASO version order: stable before preview, then newer first")
	}
}

const armSrc = `package arm

type Widget_Spec struct {
	Name string ` + "`json:\"name,omitempty\"`" + `
}

func (w Widget_Spec) GetAPIVersion() string { return "2024-01-01" }
func (w *Widget_Spec) GetType() string      { return "Microsoft.Foo/widgets" }
`

const apiSrc = `package v1

type Widget_Spec struct {
	AzureName string ` + "`json:\"azureName,omitempty\"`" + `
	Location *string ` + "`json:\"location,omitempty\"`" + `
	Owner *genruntime.KnownResourceReference ` + "`json:\"owner,omitempty\"`" + `
	Size   *int ` + "`json:\"size,omitempty\"`" + `
	Action *Action ` + "`json:\"action,omitempty\"`" + `
	Ports  []Port ` + "`json:\"ports,omitempty\"`" + `
	Raw    map[string]v1.JSON ` + "`json:\"raw,omitempty\"`" + `
	Mode   *Mode ` + "`json:\"mode,omitempty\"`" + `
	Subnet *SubResource ` + "`json:\"subnet,omitempty\"`" + `
	KeyReference *genruntime.ResourceReference ` + "`armReference:\"KeyId\" json:\"keyReference,omitempty\"`" + `
	Ids []genruntime.ResourceReference ` + "`armReference:\"ResourceIds\" json:\"idReferences,omitempty\"`" + `
	Identity *Identity ` + "`json:\"identity,omitempty\"`" + `
}

type Identity struct {
	Type *string ` + "`json:\"type,omitempty\"`" + `
	UserAssignedIdentities []UserAssigned ` + "`json:\"userAssignedIdentities,omitempty\"`" + `
}

type UserAssigned struct {
	Reference *genruntime.ResourceReference ` + "`armReference:\"Id\" json:\"reference,omitempty\"`" + `
}

type SubResource struct {
	Reference *genruntime.ResourceReference ` + "`armReference:\"Id\" json:\"reference,omitempty\"`" + `
}

type Mode string

type Port struct{ Number *int ` + "`json:\"number,omitempty\"`" + ` }

type Action struct {
	// Email: Mutually exclusive with all other properties
	Email *Email ` + "`json:\"email,omitempty\"`" + `
	// Hook: Mutually exclusive with all other properties
	Hook *Hook ` + "`json:\"hook,omitempty\"`" + `
}

type Email struct{ Kind *string ` + "`json:\"kind,omitempty\"`" + `; To *string ` + "`json:\"to,omitempty\"`" + ` }
type Hook struct{ Kind *string ` + "`json:\"kind,omitempty\"`" + `; Url *string ` + "`json:\"url,omitempty\"`" + ` }
`

func TestAPITypes(t *testing.T) {
	dir := t.TempDir()
	af, pf := filepath.Join(dir, "widget_spec_types_gen.go"), filepath.Join(dir, "widget_types_gen.go")
	for f, src := range map[string]string{af: armSrc, pf: apiSrc} {
		if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	arm, err := parsePackage([]string{af})
	if err != nil {
		t.Fatal(err)
	}
	api, err := parsePackage([]string{pf})
	if err != nil {
		t.Fatal(err)
	}
	ks := kindsOfPackage(arm, api, "foo.azure.com", "v1api20240101")
	if len(ks) != 1 || ks[0].Kind != "Widget" || ks[0].ARMType != "Microsoft.Foo/widgets" || ks[0].API != "2024-01-01" {
		t.Fatalf("kinds %+v", ks)
	}
	w := ks[0].Wire
	for p, ok := range map[string]bool{
		"location":                          true,
		"azureName":                         false, // the operator's
		"owner.name":                        false,
		"properties.size":                   true, // the properties bag is flattened
		"properties.ports[].number":         true,
		"ports[].number":                    true,
		"ports[]":                           true, // a deeper path covers a spec leaf that is an object
		"raw{}.anything":                    true, // any JSON below a free-form leaf
		"action.kind":                       true, // a union's variants are at its own path
		"action.to":                         true,
		"action.url":                        true,
		"properties.subnet.id":              true, // subnet.reference
		"properties.keyId":                  true, // armReference
		"resourceIds[]":                     true,
		"identity.userAssignedIdentities{}": true, // an array of {reference} for the spec's map
		"identity.type":                     true,
		"size.x":                            false,
		"missing":                           false,
	} {
		if _, got := w.covers(p); got != ok {
			t.Errorf("covers(%q) = %v, want %v", p, got, ok)
		}
	}
}

func TestBars(t *testing.T) {
	s := segs{official: 1, ours: 1, none: 1}
	o, u, n := s.widths(100)
	if o+u+n != 100 {
		t.Errorf("widths %v %v %v", o, u, n)
	}
	svg := serviceSVG("Microsoft.Foo", segs{50, 25, 25}, nil, "")
	for _, w := range []string{"50.0%", "25.0%", "75.0% covered", colOfficial, colOurs, colNone} {
		if !strings.Contains(svg, w) {
			t.Errorf("service svg lacks %q", w)
		}
	}
	if strings.Contains(svg, "http://") && !strings.Contains(svg, "http://www.w3.org/2000/svg") || strings.Contains(svg, "<script") || strings.Contains(svg, "href") {
		t.Error("svg has an external reference")
	}
	if pct(1, 3) != "33.3" || pct(0, 0) != "0.0" {
		t.Error("pct")
	}
	if commas(1234567) != "1,234,567" || commas(12) != "12" {
		t.Error("commas")
	}
}

func TestReplaceBetween(t *testing.T) {
	in := "a\n" + startMarker + "\nold\n" + endMarker + "\nb\n"
	out, err := replaceBetween([]byte(in), startMarker, endMarker, "new\n")
	if err != nil || string(out) != "a\n"+startMarker+"\nnew\n"+endMarker+"\nb\n" {
		t.Errorf("%q %v", out, err)
	}
}

func TestTerraform(t *testing.T) {
	in := "service,provider,category,resources_total,attributes_total,attributes_terraform_l1,attributes_terraform_l2\n" +
		"Microsoft.Foo,azurerm,platform-foundation,2,100,10,20\n" +
		"Microsoft.Foo,azurerm,other,1,50,5,5\n" +
		"Microsoft.Bar,azurerm,platform-foundation,1,50,0,10\n" +
		"Microsoft.Bar,azapi,platform-foundation,1,50,50,50\n"
	tf, err := parseTerraform([]byte(in), "azurerm")
	if err != nil {
		t.Fatal(err)
	}
	if tf.All != (tfAgg{200, 15, 35}) {
		t.Errorf("all %+v", tf.All)
	}
	if a, ok := tf.service("Microsoft.Foo"); !ok || a != (tfAgg{150, 15, 25}) {
		t.Errorf("Microsoft.Foo %+v %v", a, ok)
	}
	if a, ok := tf.category("platform-foundation"); !ok || a != (tfAgg{150, 10, 30}) {
		t.Errorf("platform-foundation %+v %v", a, ok)
	}
	if a, ok := tf.services([]string{"Microsoft.Bar", "Microsoft.Absent"}); !ok || a.cell(ok) != "~20.0%" {
		t.Errorf("services %+v %v", a, ok)
	}
	if a, ok := tf.service("Microsoft.Absent"); ok || a.cell(ok) != "—" {
		t.Error("an absent service has a figure")
	}
	if _, err := parseTerraform([]byte(in), "aws"); err == nil {
		t.Error("no rows for the provider, no error")
	}
	svg := serviceSVG("Microsoft.Foo", segs{50, 25, 25}, &tfAgg{100, 10, 20}, "azurerm")
	for _, w := range []string{colTerraform, colTerraformL, "Terraform ~20.0%", "10.0% by the strict rule"} {
		if !strings.Contains(svg, w) {
			t.Errorf("service svg lacks %q", w)
		}
	}
	total := totalSVG("T", segs{1, 1, 1}, segs{1, 1, 1}, "", &tfAgg{100, 10, 20}, "azurerm")
	if !strings.Contains(total, "azapi is not a bar") || !strings.Contains(total, "20.0%, 10.0% by the strict rule") {
		t.Error("total svg lacks the Terraform bar or the azapi note")
	}
}

func TestTerraformDigest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "terraform.csv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := &terraformSource{Commit: strings.Repeat("a", 40), Provider: "azurerm"}
	src.Files = append(src.Files, struct {
		From   string `yaml:"from"`
		Path   string `yaml:"path"`
		SHA256 string `yaml:"sha256"`
	}{"x", "terraform.csv", strings.Repeat("0", 64)})
	if _, err := readTerraform(root, src); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Errorf("a changed copy was accepted: %v", err)
	}
}

// TestTerraformPinned reads the vendored catalog files at the pin and checks
// them against the catalog's own summary.csv (azurerm: 8.2% L2, 7.2% L1 of
// everything; 18.7% L2 of platform foundation).
func TestTerraformPinned(t *testing.T) {
	root := filepath.Join("..", "..")
	b, err := os.ReadFile(filepath.Join(root, coverageDir, "sources.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var src sources
	if err := yaml.Unmarshal(b, &src); err != nil {
		t.Fatal(err)
	}
	tf, err := readTerraform(root, &src.Terraform)
	if err != nil {
		t.Fatal(err)
	}
	pf, _ := tf.category("platform-foundation")
	if got := []string{pct(tf.All.l2, tf.All.total), pct(tf.All.l1, tf.All.total), pct(pf.l2, pf.total)}; !reflect.DeepEqual(got, []string{"8.2", "7.2", "18.7"}) {
		t.Errorf("all L2, all L1, foundation L2 = %v, want the catalog's summary 8.2, 7.2, 18.7", got)
	}
}
