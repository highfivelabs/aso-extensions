// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	"github.com/highfivelabs/aso-extensions/pkg/controllers"
)

// upjetExample is one hand-written example of Crossplane's
// provider-upjet-azure, its values mapped onto one of our kinds by
// hack/coverage/upjet.go (docs/coverage/README.md, "Upjet"; HFL-700).
type upjetExample struct {
	Group   string         `json:"group"`
	Version string         `json:"version"`
	Kind    string         `json:"kind"`
	Example string         `json:"example"`
	At      string         `json:"at"`
	Slice   string         `json:"slice"`
	Spec    map[string]any `json:"spec"`
}

// TestUpjetExamplesOnFake runs each Upjet hand example we can express
// (docs/coverage/upjet-examples.json) as the conformance test runs its
// kind (arm_conformance_test.go), with the example's values merged over
// the smallest object and the kind's conformance fixture: the object
// becomes Ready, the fake holds it at its ID with every string value of
// the example sent, a change of tags is PUT, and a delete deletes. An
// example the fake refuses, or our kind cannot take, is a finding: fix the
// kind or the fake, or list the example in hack/coverage/upjet.yaml with
// the reason.
func TestUpjetExamplesOnFake(t *testing.T) {
	raw, err := os.ReadFile("../../docs/coverage/upjet-examples.json")
	if err != nil {
		t.Fatal(err)
	}
	var examples []upjetExample
	if err := json.Unmarshal(raw, &examples); err != nil {
		t.Fatal(err)
	}
	if len(examples) == 0 {
		t.Fatal("docs/coverage/upjet-examples.json has no examples; run hack/coverage/run.sh")
	}
	crds := map[string]int{}
	kinds := generatedCRDs(t)
	for i, crd := range kinds {
		crds[crd.Spec.Group+"/"+crd.Spec.Names.Kind] = i
	}
	scheme := controllers.CreateScheme()
	n := map[string]int{}
	for _, ex := range examples {
		n[ex.Kind]++
		name := fmt.Sprintf("%s/%d", ex.Kind, n[ex.Kind])
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			t.Logf("Upjet example %s (%s; %s)", ex.Example, ex.At, ex.Slice)
			i, ok := crds[ex.Group+"/"+ex.Kind]
			if !ok {
				t.Fatalf("%s/%s is not a generated kind in config/crds; rerun hack/coverage/run.sh", ex.Group, ex.Kind)
			}
			crd := kinds[i]
			version := crd.Spec.Versions[0]
			gvk := schema.GroupVersionKind{Group: crd.Spec.Group, Version: version.Name, Kind: crd.Spec.Names.Kind}
			fix, ok := conformanceFixtures[gvk.Group+"/"+gvk.Kind]
			if !ok {
				fix = conformanceFixtures[gvk.Kind]
			}
			if fix.skip != "" {
				t.Skipf("the kind's conformance run is skipped: %s", fix.skip)
			}
			if fix.coveredBy != "" {
				t.Skipf("the kind's smallest object needs other kinds (%s)", fix.coveredBy)
			}
			typed, err := scheme.New(gvk)
			if err != nil {
				t.Fatal(err)
			}
			meta, ok := typed.(genruntime.ARMMetaObject)
			if !ok {
				t.Fatalf("%s is not an ARM kind", gvk)
			}
			base := fix.spec
			ns := upjetNamespace(gvk.Kind, name)
			specSchema := version.Schema.OpenAPIV3Schema.Properties["spec"]
			fix.spec = func(c *conformanceContext) map[string]any {
				out := map[string]any{}
				if meta.GetResourceScope() == genruntime.ResourceScopeTenant {
					// A tenant resource has no resource group to keep the
					// examples of a kind apart: each gets its own name.
					if _, ok := specSchema.Properties["azureName"]; ok {
						out["azureName"] = ns
					}
				}
				if base != nil {
					merge(out, base(c))
				}
				merge(out, deepCopyJSON(ex.Spec))
				return out
			}
			fix.namespace = ns
			fix.sent = stringsOf(ex.Spec)
			conformance(t, gvk, meta, specSchema, fix)
		})
	}
}

// upjetNamespace is "upj-", the kind and the example's number, cut to fit
// with a hash, as conformanceNamespace does.
func upjetNamespace(kind, name string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	ns := "upj-" + strings.ToLower(kind)
	if len(ns) > 44 {
		ns = strings.TrimRight(ns[:44], "-")
	}
	return fmt.Sprintf("%s-%08x", ns, h.Sum32())
}

// deepCopyJSON copies a JSON value, with numbers as the API server's
// unstructured objects want them (int64 for whole numbers).
func deepCopyJSON(m map[string]any) map[string]any {
	var cp func(v any) any
	cp = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			o := map[string]any{}
			for k, e := range x {
				o[k] = cp(e)
			}
			return o
		case []any:
			o := make([]any, len(x))
			for i, e := range x {
				o[i] = cp(e)
			}
			return o
		case float64:
			if x == float64(int64(x)) {
				return int64(x)
			}
			return x
		}
		return v
	}
	return cp(m).(map[string]any)
}

// stringsOf lists the string values in an example's spec, which the fake's
// resource must hold after the PUT.
func stringsOf(v any) []string {
	var out []string
	switch x := v.(type) {
	case map[string]any:
		for _, e := range x {
			out = append(out, stringsOf(e)...)
		}
	case []any:
		for _, e := range x {
			out = append(out, stringsOf(e)...)
		}
	case string:
		out = append(out, x)
	}
	return out
}
