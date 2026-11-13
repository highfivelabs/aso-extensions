// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package controllers

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// Every generated kind's owner reference names a kind the manager's
// resolver knows: one of our generated kinds or one of ASO's in
// OwnerTypes. A reference to anything else is never resolved ("failed to
// get owner ... was not in reconciledResourceLookup"), as happened to the
// children of an owner ASO exports under another name than its ARM name
// (Servers_Database, ServersDatabase) until generator/external-owners.txt
// mapped it.
func TestGeneratedOwnersAreKnown(t *testing.T) {
	scheme := CreateScheme()
	known := map[schema.GroupKind]bool{}
	for _, st := range getKnownStorageTypes() {
		known[gvkOf(t, st.Obj, scheme).GroupKind()] = true
	}
	for _, st := range OwnerTypes() {
		known[gvkOf(t, st.Obj, scheme).GroupKind()] = true
	}

	checked := 0
	for _, kt := range getKnownTypes() {
		gvk := gvkOf(t, kt.Obj, scheme)
		spec, ok := reflect.TypeOf(kt.Obj).Elem().FieldByName("Spec")
		if !ok {
			t.Errorf("%s has no Spec", gvk)
			continue
		}
		owner, ok := spec.Type.FieldByName("Owner")
		if !ok {
			continue // a tenant-scoped kind has no owner
		}
		group, hasGroup := owner.Tag.Lookup("group")
		kind, hasKind := owner.Tag.Lookup("kind")
		if !hasGroup && !hasKind {
			continue // an extension resource: any resource may own it
		}
		checked++
		if !known[schema.GroupKind{Group: group, Kind: kind}] {
			t.Errorf("%s: its owner %s/%s is not a kind the resolver knows (a generated kind or one of OwnerTypes)", gvk, group, kind)
		}
	}
	if checked < 100 {
		t.Fatalf("checked %d owner references; expected the generated kinds' (over 100)", checked)
	}
}

func gvkOf(t *testing.T, obj runtime.Object, scheme *runtime.Scheme) schema.GroupVersionKind {
	t.Helper()
	gvk, err := apiutil.GVKForObject(obj, scheme)
	if err != nil {
		t.Fatalf("%T: %v", obj, err)
	}
	return gvk
}
