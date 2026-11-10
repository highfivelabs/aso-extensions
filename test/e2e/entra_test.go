// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	entrav1 "github.com/highfivelabs/aso-extensions/apis/entra/v1"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/graph"
)

// Microsoft Entra directory role assignments (c15) against the fake
// Microsoft Graph, with the namespace's identity: Global Reader for a
// principal over the whole directory (by object ID); User Administrator
// over an administrative unit for a principal whose ID is in a ConfigMap;
// an assignment made outside Kubernetes adopted (AdoptOrCreate, the
// default) rather than made twice; AdoptOnly with nothing to adopt, an
// unknown principal and an unknown administrative unit refused on the Ready
// condition; a change to the role refused by the API server (Graph does
// not update an assignment); deleted, and an AdoptOnly assignment left in
// Graph when its object goes.
func TestDirectoryRoleAssignment(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "entra")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)

	const (
		reader   = "6a2f0e1b-0000-4000-8000-0000000000a1"
		admin    = "6a2f0e1b-0000-4000-8000-0000000000a2"
		existing = "6a2f0e1b-0000-4000-8000-0000000000a3"
		unit     = "7b3c1f2a-0000-4000-8000-0000000000b1"
	)
	for _, p := range []string{reader, admin, existing} {
		graphFake.AddPrincipal(p)
	}
	graphFake.AddAdministrativeUnit(unit)
	preexisting := graphFake.Assign(existing, graph.SecurityReader, "/")

	if err := k8s.Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "admins"},
		Data:       map[string]string{"principalId": admin},
	}); err != nil {
		t.Fatal(err)
	}

	assignment := func(name string, spec entrav1.DirectoryRoleAssignmentSpec) *entrav1.DirectoryRoleAssignment {
		return &entrav1.DirectoryRoleAssignment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}, Spec: spec}
	}
	mode := func(m entrav1.CreationMode) *entrav1.DirectoryRoleAssignmentOperatorSpec {
		return &entrav1.DirectoryRoleAssignmentOperatorSpec{CreationMode: &m}
	}
	globalReader := assignment("global-reader", entrav1.DirectoryRoleAssignmentSpec{
		PrincipalId: ptr(reader), RoleDefinitionId: ptr(graph.GlobalReader), DirectoryScopeId: ptr("/"),
	})
	unitAdmin := assignment("unit-admin", entrav1.DirectoryRoleAssignmentSpec{
		PrincipalIdFromConfig: &genruntime.ConfigMapReference{Name: "admins", Key: "principalId"},
		RoleDefinitionId:      ptr(graph.UserAdministrator), DirectoryScopeId: ptr("/administrativeUnits/" + unit),
	})
	adopted := assignment("adopted", entrav1.DirectoryRoleAssignmentSpec{
		PrincipalId: ptr(existing), RoleDefinitionId: ptr(graph.SecurityReader), DirectoryScopeId: ptr("/"),
	})
	adoptOnly := assignment("adopt-only", entrav1.DirectoryRoleAssignmentSpec{
		PrincipalId: ptr(reader), RoleDefinitionId: ptr(graph.DirectoryReaders), DirectoryScopeId: ptr("/"),
		OperatorSpec: mode(entrav1.AdoptOnly),
	})
	unknownPrincipal := assignment("unknown-principal", entrav1.DirectoryRoleAssignmentSpec{
		PrincipalId: ptr("6a2f0e1b-0000-4000-8000-0000000000ff"), RoleDefinitionId: ptr(graph.GlobalReader), DirectoryScopeId: ptr("/"),
	})
	unknownUnit := assignment("unknown-unit", entrav1.DirectoryRoleAssignmentSpec{
		PrincipalId: ptr(reader), RoleDefinitionId: ptr(graph.UserAdministrator), DirectoryScopeId: ptr("/administrativeUnits/nowhere"),
	})
	all := []*entrav1.DirectoryRoleAssignment{globalReader, unitAdmin, adopted, adoptOnly, unknownPrincipal, unknownUnit}
	for _, a := range all {
		if err := k8s.Create(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range []*entrav1.DirectoryRoleAssignment{globalReader, unitAdmin, adopted} {
		eventually(t, timeout, a.Name+" Ready", ready(a))
	}
	eventually(t, timeout, "adopt-only on the Ready condition", notReadyWith(adoptOnly, "not found for adoption"))
	eventually(t, timeout, "the unknown principal on the Ready condition", notReadyWith(unknownPrincipal, "does not exist"))
	eventually(t, timeout, "the unknown administrative unit on the Ready condition", notReadyWith(unknownUnit, "administrative unit 'nowhere' does not exist"))

	for _, a := range []*entrav1.DirectoryRoleAssignment{globalReader, unitAdmin, adopted} {
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(a), a); err != nil {
			t.Fatal(err)
		}
		if a.Status.Id == nil || !graphFake.HasAssignment(*a.Status.Id) || a.Annotations[genruntime.ResourceIDAnnotation] != *a.Status.Id {
			t.Fatalf("%s: status %+v, annotations %v, Graph %v", a.Name, a.Status, a.Annotations, graphFake.Assignments())
		}
	}
	if *adopted.Status.Id != preexisting {
		t.Fatalf("adopted %s, want the assignment made outside Kubernetes, %s", *adopted.Status.Id, preexisting)
	}
	if *unitAdmin.Status.PrincipalId != admin || *unitAdmin.Status.DirectoryScopeId != "/administrativeUnits/"+unit {
		t.Fatalf("unit-admin status %+v", unitAdmin.Status)
	}
	want := []string{
		admin + " " + graph.UserAdministrator + " /administrativeUnits/" + unit,
		existing + " " + graph.SecurityReader + " /",
		reader + " " + graph.GlobalReader + " /",
	}
	if got := graphFake.Assignments(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Graph holds %v, want %v", got, want)
	}

	// Graph does not update an assignment; the API server refuses the change.
	globalReader.Spec.RoleDefinitionId = ptr(graph.SecurityReader)
	if err := k8s.Update(ctx, globalReader); err == nil || !strings.Contains(err.Error(), "roleDefinitionId cannot be changed") {
		t.Fatalf("changing the role: %v", err)
	}

	// An AdoptOnly assignment that found one is left in Graph when it goes.
	graphFake.Assign(reader, graph.DirectoryReaders, "/")
	eventually(t, 3*timeout, "adopt-only Ready once there is one to adopt", ready(adoptOnly))
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(adoptOnly), adoptOnly); err != nil {
		t.Fatal(err)
	}
	kept := *adoptOnly.Status.Id

	ids := map[string]string{}
	for _, a := range []*entrav1.DirectoryRoleAssignment{globalReader, unitAdmin, adopted} {
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(a), a); err != nil {
			t.Fatal(err)
		}
		ids[a.Name] = *a.Status.Id
	}
	for _, a := range all {
		if err := k8s.Delete(ctx, a); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, a.Name+" gone", gone(a))
	}
	for name, id := range ids {
		if graphFake.HasAssignment(id) {
			t.Fatalf("%s's assignment %s is still in Graph", name, id)
		}
	}
	if !graphFake.HasAssignment(kept) {
		t.Fatalf("the AdoptOnly assignment %s was deleted from Graph", kept)
	}
}
