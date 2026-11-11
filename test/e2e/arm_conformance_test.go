// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	"github.com/highfivelabs/aso-extensions/pkg/controllers"
)

// The conformance test: every kind ASO's generator wrote here, with no
// test written for it by hand. For each generated CRD in config/crds it
// builds the smallest object the CRD accepts (its required fields filled
// from the schema: an enum's first value, a reference by ARM ID to a
// resource the fake holds, a Secret or ConfigMap key the test makes), owned
// by ARM ID through a parent chain the fake holds (derived from the kind's
// ARM type and scope), and checks what every generated kind must do against
// the fake Resource Manager: the object becomes Ready; the fake holds a
// resource of the kind's ARM type at the ID ASO wrote on the object; a
// change of tags is PUT again; deleting the object deletes the resource
// (or, for a kind Azure cannot delete, the object is detached, as ASO
// requires, and the resource stays).
//
// What it does not show is each kind's own behaviour (keys Azure makes,
// children kept on a parent's PUT, references the fake checks, refusals):
// that is what a kind's own test is for (arm_*_test.go), written when a kind
// has such behaviour. See docs/testing.md.
//
// conformanceFixtures says what a kind needs beyond the smallest object,
// when the fake's rules for that kind ask for more; a kind listed there with
// skip is not run, with the reason.
func TestGeneratedKindsConformance(t *testing.T) {
	scheme := controllers.CreateScheme()
	kinds := generatedCRDs(t)
	if len(kinds) < 90 {
		t.Fatalf("found %d generated CRDs in config/crds; expected the generated kinds (over 90)", len(kinds))
	}
	for _, crd := range kinds {
		version := crd.Spec.Versions[0]
		gvk := schema.GroupVersionKind{Group: crd.Spec.Group, Version: version.Name, Kind: crd.Spec.Names.Kind}
		t.Run(gvk.Kind, func(t *testing.T) {
			t.Parallel()
			fix := conformanceFixtures[gvk.Kind]
			if fix.skip != "" {
				t.Skip(fix.skip)
			}
			if fix.coveredBy != "" {
				if !testExists(t, fix.coveredBy) {
					t.Fatalf("%s is covered by %s, which is not a test in test/e2e", gvk.Kind, fix.coveredBy)
				}
				t.Skipf("run by %s (its smallest valid object needs other kinds)", fix.coveredBy)
			}
			typed, err := scheme.New(gvk)
			if err != nil {
				t.Fatalf("%s is not in the manager's scheme: %v", gvk, err)
			}
			meta, ok := typed.(genruntime.ARMMetaObject)
			if !ok {
				t.Fatalf("%s is not an ARM kind", gvk)
			}
			conformance(t, gvk, meta, version.Schema.OpenAPIV3Schema.Properties["spec"], fix)
		})
	}
}

// generatedCRDs are the CRDs in config/crds whose kinds ASO's generator
// wrote (their Go types are in apis/arm).
func generatedCRDs(t *testing.T) []*apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	scheme := controllers.CreateScheme()
	files, err := filepath.Glob("../../config/crds/*.azure.hfl.dev_*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	var out []*apiextensionsv1.CustomResourceDefinition
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		crd := &apiextensionsv1.CustomResourceDefinition{}
		if err := yaml.Unmarshal(raw, crd); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if len(crd.Spec.Versions) != 1 {
			t.Fatalf("%s: %d versions; the CRDs here have one", f, len(crd.Spec.Versions))
		}
		gvk := schema.GroupVersionKind{Group: crd.Spec.Group, Version: crd.Spec.Versions[0].Name, Kind: crd.Spec.Names.Kind}
		obj, err := scheme.New(gvk)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if strings.Contains(reflect.TypeOf(obj).Elem().PkgPath(), "/apis/arm/") {
			out = append(out, crd)
		}
	}
	return out
}

// conformanceFixture is what a kind needs beyond the smallest object.
type conformanceFixture struct {
	// spec is merged over the smallest spec (objects merged key by key).
	spec func(c *conformanceContext) map[string]any
	// owner, when set, is the owner's ARM ID instead of the one derived
	// from the kind's type; it must exist in the fake (setup adds it).
	owner func(c *conformanceContext) string
	// setup runs before the object is created (resources the fake should
	// hold first: what a reference names, or the resource itself when
	// something other than a PUT makes it).
	setup func(c *conformanceContext)
	// annotations are put on the object (reconcile-policy, for one).
	annotations map[string]string
	// stays: the resource stays in Azure after the object is deleted (a
	// request Azure cannot delete, so the object is detached).
	stays bool
	// keeps: a PUT on the resource once made changes nothing (Azure says
	// so), so the change of tags is not checked.
	keeps bool
	// coveredBy names the test in test/e2e that runs the kind instead,
	// for a kind whose smallest valid object is a graph of other kinds
	// (the test checks that the named test exists).
	coveredBy string
	// skip is why the kind is not run here.
	skip string
}

// conformanceContext is one kind's run: its namespace and the resource
// group made for it.
type conformanceContext struct {
	ns            string
	resourceGroup string
	// ref is a resource the fake holds that a required reference names
	// when the kind's fixture does not say otherwise.
	ref string
	// owner is the ARM ID of the kind's owner ("" for none).
	owner string
}

// add puts resources in the fake (as if made elsewhere) and returns the
// first.
func (c *conformanceContext) add(ids ...string) string {
	for _, id := range ids {
		armFake.AddResource(id)
	}
	return ids[0]
}

// in is an ID in the kind's resource group.
func (c *conformanceContext) in(rest string) string {
	return c.resourceGroup + "/providers/" + rest
}

// parent is the owner's parent's ARM ID (the owner without its last
// type and name).
func (c *conformanceContext) parent() string {
	return parentOf(c.owner)
}

func parentOf(id string) string {
	parts := strings.Split(id, "/")
	return strings.Join(parts[:len(parts)-2], "/")
}

func armRef(id string) map[string]any   { return map[string]any{"armId": id} }
func embedRef(id string) map[string]any { return map[string]any{"reference": armRef(id)} }

var conformanceFixtures = map[string]conformanceFixture{}

func conformance(t *testing.T, gvk schema.GroupVersionKind, meta genruntime.ARMMetaObject, specSchema apiextensionsv1.JSONSchemaProps, fix conformanceFixture) {
	ctx := context.Background()
	ns := namespace(t, conformanceNamespace(gvk))
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	c := &conformanceContext{ns: ns, resourceGroup: "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"}
	armFake.AddResource(c.resourceGroup)
	c.ref = c.resourceGroup + "/providers/Microsoft.Storage/storageAccounts/" + strings.ReplaceAll(ns, "-", "")
	armFake.AddResource(c.ref)
	for _, kind := range []client.Object{
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "conformance"}, StringData: map[string]string{"value": "conformance-secret-value"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "conformance"}, Data: map[string]string{"value": "conformance-value"}},
	} {
		if err := k8s.Create(ctx, kind); err != nil {
			t.Fatal(err)
		}
	}
	c.owner = conformanceOwner(t, c, meta)
	if fix.owner != nil {
		c.owner = fix.owner(c)
	}
	if fix.setup != nil {
		fix.setup(c)
	}

	spec := map[string]any{}
	for name, prop := range specSchema.Properties {
		if name == "owner" || !required(specSchema, name) {
			continue
		}
		spec[name] = conformanceValue(c, name, prop)
	}
	if _, ok := specSchema.Properties["location"]; ok {
		spec["location"] = "westus"
	}
	if hasTagMap(specSchema) {
		spec["tags"] = map[string]any{"conformance": "1"}
	}
	if c.owner != "" {
		spec["owner"] = map[string]any{"armId": c.owner}
	}
	if fix.spec != nil {
		merge(spec, fix.spec(c))
	}

	obj := &unstructured.Unstructured{Object: map[string]any{"spec": spec}}
	obj.SetGroupVersionKind(gvk)
	obj.SetNamespace(ns)
	obj.SetName("conformance")
	annotations := map[string]string{}
	for k, v := range fix.annotations {
		annotations[k] = v
	}
	stays := fix.stays
	if !deletable(meta) {
		// Azure has no DELETE for it: ASO refuses to delete the object
		// until it is detached (DeleteActionNotPossibleInAzure), as a
		// user must; the resource stays.
		annotations["serviceoperator.azure.com/reconcile-policy"] = "detach-on-delete"
		stays = true
	}
	obj.SetAnnotations(annotations)
	if err := k8s.Create(ctx, obj); err != nil {
		t.Fatalf("creating %s: %v\nspec: %v", gvk.Kind, err, spec)
	}
	eventually(t, timeout, gvk.Kind+" Ready", readyUnstructured(obj))

	id, _, _ := unstructured.NestedString(obj.Object, "metadata", "annotations", genruntime.ResourceIDAnnotation)
	if id == "" {
		t.Fatalf("%s has no %s annotation", gvk.Kind, genruntime.ResourceIDAnnotation)
	}
	got, ok := armFake.Resource(id)
	if !ok {
		t.Fatalf("%s is Ready but the fake has no resource %s", gvk.Kind, id)
	}
	if typ, _ := got["type"].(string); !strings.EqualFold(typ, meta.GetType()) {
		t.Fatalf("%s: the fake holds %s of type %q; the kind's ARM type is %q", gvk.Kind, id, typ, meta.GetType())
	}

	if hasTagMap(specSchema) && !fix.keeps {
		if err := unstructured.SetNestedField(obj.Object, map[string]any{"conformance": "2"}, "spec", "tags"); err != nil {
			t.Fatal(err)
		}
		if err := k8s.Update(ctx, obj); err != nil {
			t.Fatal(err)
		}
		expectResource(t, id, func(r map[string]any) error {
			if tags, _ := r["tags"].(map[string]any); tags["conformance"] != "2" {
				return fmt.Errorf("tags not changed: %v", r["tags"])
			}
			return nil
		})
		eventually(t, timeout, gvk.Kind+" Ready after the change", readyUnstructured(obj))
	}

	if err := k8s.Delete(ctx, obj); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, gvk.Kind+" gone", func() error {
		err := k8s.Get(ctx, client.ObjectKeyFromObject(obj), &unstructured.Unstructured{Object: map[string]any{"apiVersion": obj.GetAPIVersion(), "kind": obj.GetKind()}})
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("still there: %v", err)
	})
	if stays {
		if !armFake.HasResource(id) {
			t.Fatalf("%s detached, but %s is gone from the fake", gvk.Kind, id)
		}
	} else if armFake.HasResource(id) {
		t.Fatalf("%s deleted, but %s is still in the fake", gvk.Kind, id)
	}
}

// required is whether name is in the schema's required list.
func required(s apiextensionsv1.JSONSchemaProps, name string) bool {
	for _, r := range s.Required {
		if r == name {
			return true
		}
	}
	return false
}

// deletable is whether Azure deletes the kind's resource (the spec has a
// DELETE).
func deletable(meta genruntime.ARMMetaObject) bool {
	for _, op := range meta.GetSupportedOperations() {
		if op == genruntime.ResourceOperationDelete {
			return true
		}
	}
	return false
}

// conformanceNamespace is a namespace name for a kind: "conf-", the
// group's first label and the kind in lower case, cut to fit (a resource
// group name is made from it); two groups have a VaultsPrivateEndpointConnection.
func conformanceNamespace(gvk schema.GroupVersionKind) string {
	ns := "conf-" + strings.SplitN(gvk.Group, ".", 2)[0] + "-" + strings.ToLower(gvk.Kind)
	if len(ns) > 55 {
		ns = ns[:55]
	}
	return strings.TrimRight(ns, "-")
}

// hasTagMap is whether the spec has tags as a map of strings (Log
// Analytics saved searches have a list of name and value pairs instead).
func hasTagMap(s apiextensionsv1.JSONSchemaProps) bool {
	tags, ok := s.Properties["tags"]
	return ok && tags.Type == "object" && tags.AdditionalProperties != nil &&
		tags.AdditionalProperties.Schema != nil && tags.AdditionalProperties.Schema.Type == "string"
}

// testExists is whether test/e2e has a test function of that name.
func testExists(t *testing.T, name string) bool {
	t.Helper()
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "\nfunc "+name+"(t *testing.T)") {
			return true
		}
	}
	return false
}

// conformanceOwner is the ARM ID of an owner for meta, added to the fake
// with its parents: the resource group for a resource in a resource group
// or an extension resource; for a child resource, the parent chain its ARM
// type names (Microsoft.Network/virtualHubs/routeTables is owned by
// .../providers/Microsoft.Network/virtualHubs/p1); none for a tenant
// resource at the top.
func conformanceOwner(t *testing.T, c *conformanceContext, meta genruntime.ARMMetaObject) string {
	t.Helper()
	parts := strings.Split(meta.GetType(), "/")
	namespace, types := parts[0], parts[1:]
	var root string
	switch meta.GetResourceScope() {
	case genruntime.ResourceScopeResourceGroup:
		root = c.resourceGroup
	case genruntime.ResourceScopeExtension:
		return c.resourceGroup
	case genruntime.ResourceScopeTenant:
		root = ""
	default:
		t.Fatalf("%s: scope %q is not handled here", meta.GetType(), meta.GetResourceScope())
	}
	if len(types) == 1 {
		return root
	}
	id := root + "/providers/" + namespace
	for i, typ := range types[:len(types)-1] {
		id += fmt.Sprintf("/%s/p%d", typ, i+1)
		armFake.AddResource(id)
	}
	return id
}

// conformanceValue is the smallest value the schema accepts for a
// required property.
func conformanceValue(c *conformanceContext, name string, s apiextensionsv1.JSONSchemaProps) any {
	switch s.Type {
	case "string":
		if len(s.Enum) > 0 {
			var v any
			_ = yaml.Unmarshal(s.Enum[0].Raw, &v)
			return v
		}
		lower := strings.ToLower(name)
		if s.Format == "date-time" || strings.Contains(lower, "date") || strings.Contains(lower, "time") {
			return "2030-01-01T00:00:00Z"
		}
		return conformanceString(s)
	case "integer", "number":
		if s.Minimum != nil && *s.Minimum > 1 {
			return int64(*s.Minimum)
		}
		return int64(1)
	case "boolean":
		return false
	case "array":
		if s.Items == nil || s.Items.Schema == nil {
			return []any{}
		}
		return []any{conformanceValue(c, name, *s.Items.Schema)}
	}
	// Free-form JSON (no type): a scalar, which survives the typed round
	// trip ({} came back empty on the reconciler's update and a required
	// field was refused).
	if s.Type == "" && len(s.Properties) == 0 {
		return "conformance"
	}
	// A map: one entry (an empty map is dropped as omitempty).
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil && len(s.Properties) == 0 {
		return map[string]any{"conformance": conformanceValue(c, name, *s.AdditionalProperties.Schema)}
	}
	// An object.
	if _, ok := s.Properties["armId"]; ok {
		return armRef(c.ref)
	}
	if r, ok := s.Properties["reference"]; ok && len(s.Properties) == 1 {
		if _, ok := r.Properties["armId"]; ok {
			return embedRef(c.ref)
		}
	}
	if _, ok := s.Properties["key"]; ok && len(s.Properties) == 2 {
		if _, ok := s.Properties["name"]; ok {
			return map[string]any{"name": "conformance", "key": "value"}
		}
	}
	out := map[string]any{}
	for _, r := range s.Required {
		if p, ok := s.Properties[r]; ok {
			out[r] = conformanceValue(c, r, p)
		}
	}
	return out
}

// conformanceString is a string the schema's length and pattern accept.
func conformanceString(s apiextensionsv1.JSONSchemaProps) string {
	candidates := []string{"conformance", "c1", "00000000-0000-0000-0000-000000000001", "1", "10.0.0.0/24", "x"}
	for _, v := range candidates {
		if s.MinLength != nil && int64(len(v)) < *s.MinLength {
			continue
		}
		if s.MaxLength != nil && int64(len(v)) > *s.MaxLength {
			continue
		}
		if s.Pattern != "" {
			re, err := regexp.Compile(s.Pattern)
			if err != nil || !re.MatchString(v) {
				continue
			}
		}
		return v
	}
	if s.MinLength != nil {
		return strings.Repeat("c", int(*s.MinLength))
	}
	return "conformance"
}

// merge copies src into dst, merging objects key by key.
func merge(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				merge(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
}

// readyUnstructured is nil when obj's Ready condition is True for its
// current generation.
func readyUnstructured(obj *unstructured.Unstructured) func() error {
	return func() error {
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(obj), obj); err != nil {
			return err
		}
		conds, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
		for _, raw := range conds {
			cond, _ := raw.(map[string]any)
			if cond["type"] != "Ready" {
				continue
			}
			gen, _ := cond["observedGeneration"].(int64)
			if cond["status"] == "True" && gen == obj.GetGeneration() {
				return nil
			}
			return fmt.Errorf("Ready=%v reason=%v: %v", cond["status"], cond["reason"], cond["message"])
		}
		return fmt.Errorf("no Ready condition yet")
	}
}
