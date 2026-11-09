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
	svg := serviceSVG("Microsoft.Foo", segs{50, 25, 25})
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
