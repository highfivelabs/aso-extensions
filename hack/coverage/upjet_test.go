// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"reflect"
	"testing"
)

func TestTemplateType(t *testing.T) {
	for in, want := range map[string]string{
		"/subscriptions/{{ .s }}/resourceGroups/{{ .rg }}/providers/Microsoft.App/containerApps/{{ .external_name }}": "Microsoft.App/containerApps",
		"{{ .parameters.api_management_id }}/providers/Microsoft.ApiManagement/service/{{ .a }}/apis/{{ .b }}":       "Microsoft.ApiManagement/service/apis",
		"{{ .external_name }}": "",
	} {
		if got := templateType(in); got != want {
			t.Errorf("templateType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMapExample(t *testing.T) {
	str := func(extra map[string]any) map[string]any {
		m := map[string]any{"type": "string"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	spec := map[string]any{"type": "object", "properties": map[string]any{
		"revisionMode": str(map[string]any{"enum": []any{"Multiple", "Single"}}),
		"owner":        map[string]any{"type": "object", "properties": map[string]any{"armId": str(nil)}},
		"sku":          map[string]any{"type": "object", "properties": map[string]any{"name": str(nil)}},
		"containers": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
			"cpu":   map[string]any{"type": "number"},
			"image": str(map[string]any{"maxLength": int64(80)}),
		}}},
		"replicas": map[string]any{"type": "integer", "minimum": int64(1)},
	}}
	got, dropped := mapExample(map[string]any{
		"revision_mode": "single", // the enum's spelling wins
		"sku_name":      "Standard",
		"container":     []any{map[string]any{"cpu": 0.25, "image": "nginx"}},
		"replicas":      float64(0), // below the minimum
		"owner":         "x",        // never set from an example
		"unknown":       "y",
		"secret":        []any{map[string]any{}}, // empty: not listed
	}, spec)
	want := map[string]any{
		"revisionMode": "Single",
		"sku":          map[string]any{"name": "Standard"},
		"containers":   []any{map[string]any{"cpu": 0.25, "image": "nginx"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mapExample = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(dropped, []string{"owner", "replicas", "unknown"}) {
		t.Errorf("dropped = %v", dropped)
	}
}

func TestClassifyRef(t *testing.T) {
	w := &Wire{Leaves: map[string]string{}, FreeForm: map[string]bool{}}
	w.addRef("environmentReference", "environmentId")
	w.add("workspaceId", false)
	k := &Kind{Group: "app.azure.hfl.dev", Kind: "ContainerApp", Wire: w}
	e := &upjetEntry{}
	e.Ours.Resource = "Microsoft.App/containerApps"
	e.Facts.Terraform = "azurerm_container_app"
	for _, c := range []struct {
		field, target, class string
	}{
		{"resource_group_name", "azurerm_resource_group", refOwner},
		{"container_app_environment_id", "azurerm_container_app_environment", refReference},
		{"workspace_id", "azurerm_log_analytics_workspace", refString},
		{"storage_account_name", "azurerm_storage_account", refName},
		{"subnet_id", "azurerm_subnet", refMissing},
	} {
		r := classifyRef(e, &upjetRef{Field: c.field, Target: c.target}, k, map[string]string{})
		if r.class != c.class {
			t.Errorf("%s: class %q, want %q (ours %q)", c.field, r.class, c.class, r.ours)
		}
	}
}
