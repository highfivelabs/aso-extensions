// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package slotmove

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	asokeyvault "github.com/Azure/azure-service-operator/v2/api/keyvault/v1api20230701/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/common/annotations"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"

	network "github.com/highfivelabs/aso-extensions/apis/arm/network/v20260301"
	insights "github.com/highfivelabs/aso-extensions/apis/arm/operationalinsights/v20250701"
	kvv1 "github.com/highfivelabs/aso-extensions/apis/keyvault/v1"
	"github.com/highfivelabs/aso-extensions/pkg/slot"
)

type conditioned interface {
	client.Object
	GetConditions() conditions.Conditions
}

// cloud is what Azure holds for a fixture's resource.
type cloud struct {
	exists bool
	// holder is the slot tag ("" if none).
	holder string
	// identity changes only when the resource is made again: Resource
	// Manager's systemData.createdAt, a secret's latest version.
	identity string
	// content is the rest of the resource, but the slot tag and the
	// modification times: what "no diff" compares.
	content string
}

// fixture is one kind moved between slots.
type fixture struct {
	name string
	// taggable: the resource carries the slot tag, so the old slot is
	// refused after the move. A kind that cannot is moved the same way but
	// is not guarded (docs/plan.md lists them).
	taggable bool
	// deletes: deleting the last object deletes the resource.
	deletes bool
	setup   func(t *testing.T, ns string)
	object  func(ns string) conditioned
	state   func(t *testing.T) cloud
	// writes counts the writes that reached Azure for the resource.
	writes func() int
}

func ptr[T any](v T) *T { return &v }

var queryPackID = rgID + "/providers/Microsoft.OperationalInsights/queryPacks/slot-pack-fixed"

func fixtures() []fixture {
	ipGroupID := rgID + "/providers/Microsoft.Network/ipGroups/slot-ips"
	queryPackMoved := rgID + "/providers/Microsoft.OperationalInsights/queryPacks/slot-pack"
	queryID := queryPackID + "/queries/7f8e1a2b-0c3d-4e5f-8a9b-0c1d2e3f4a5b"
	return []fixture{
		{
			name: "IpGroup", taggable: true, deletes: true,
			object: func(ns string) conditioned {
				return &network.IpGroup{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "ips"}, Spec: network.IpGroup_Spec{
					AzureName: "slot-ips", Location: ptr("westus"), Owner: &genruntime.KnownResourceReference{ARMID: rgID},
					IpAddresses: []string{"10.0.0.0/24"}, Tags: map[string]string{"team": "a"},
				}}
			},
			state:  armState(ipGroupID),
			writes: armWrites(ipGroupID),
		},
		{
			name: "QueryPack", taggable: true, deletes: true,
			object: func(ns string) conditioned {
				return &insights.QueryPack{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "pack"}, Spec: insights.QueryPack_Spec{
					AzureName: "slot-pack", Location: ptr("westus"), Owner: &genruntime.KnownResourceReference{ARMID: rgID},
				}}
			},
			state:  armState(queryPackMoved),
			writes: armWrites(queryPackMoved),
		},
		{
			// A query's "tags" are lists of labels: it cannot carry the slot
			// tag, so it moves unguarded.
			name: "QueryPacksQuery", taggable: false, deletes: true,
			setup: func(*testing.T, string) { armFake.AddResource(queryPackID) },
			object: func(ns string) conditioned {
				return &insights.QueryPacksQuery{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "query"}, Spec: insights.QueryPacksQuery_Spec{
					AzureName: "7f8e1a2b-0c3d-4e5f-8a9b-0c1d2e3f4a5b", Owner: &genruntime.KnownResourceReference{ARMID: queryPackID},
					DisplayName: ptr("errors"), Body: ptr("AzureDiagnostics | take 10"),
				}}
			},
			state:  armState(queryID),
			writes: armWrites(queryID),
		},
		{
			// Hand-written, Key Vault's data plane: a secret version carries
			// tags. Its default deleteMode, detach, leaves it in the vault.
			name: "VaultSecret", taggable: true, deletes: false,
			setup: func(t *testing.T, ns string) {
				ownerVault(t, ns)
				if err := k8s.Create(context.Background(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "db"},
					StringData: map[string]string{"url": "postgres://moved"}}); err != nil {
					t.Fatal(err)
				}
			},
			object: func(ns string) conditioned {
				return &kvv1.VaultSecret{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "db-url"}, Spec: kvv1.VaultSecretSpec{
					AzureName: "slot-db-url", Owner: &genruntime.KubernetesOwnerReference{Name: "vault"},
					Value: &genruntime.SecretReference{Name: "db", Key: "url"}, Tags: map[string]string{"team": "a"},
				}}
			},
			state:  vaultState("slot-db-url"),
			writes: vaultWrites("slot-db-url"),
		},
	}
}

// The move, per kind: blue makes the resource; blue is paused; green adopts
// it (taking it over from blue); Azure has not changed but for the slot tag
// and the resource was not made again; blue's object is deleted and the
// resource stays; a blue object for it again is refused, and so is its
// deletion; green deletes it at the end.
func TestSlotMove(t *testing.T) {
	ctx := context.Background()
	for _, f := range fixtures() {
		t.Run(f.name, func(t *testing.T) {
			if f.setup != nil {
				f.setup(t, blueNS)
				f.setup(t, greenNS)
			}

			// 1. Blue makes it and holds it.
			old := f.object(blueNS)
			create(t, old)
			eventually(t, "blue's object Ready", ready(old))
			made := f.state(t)
			if !made.exists {
				t.Fatal("blue's resource is not in Azure")
			}
			if f.taggable && made.holder != blue {
				t.Fatalf("blue made the resource; it holds slot %q", made.holder)
			}
			if !f.taggable && made.holder != "" {
				t.Fatalf("a kind without tags was tagged %q", made.holder)
			}
			if got := old.GetLabels()[slot.Label]; got != blue {
				t.Fatalf("blue's object is labelled %q", got)
			}

			// 2. Pause blue.
			setPolicy(t, old, annotations.ReconcilePolicySkip)

			// 3. Green adopts it, taking it over from blue.
			adopted := f.object(greenNS)
			adopted.SetAnnotations(map[string]string{slot.TakeoverAnnotation: blue})
			create(t, adopted)
			eventually(t, "green's object Ready", ready(adopted))
			after := f.state(t)
			if f.taggable && after.holder != green {
				t.Fatalf("after the adoption the resource holds slot %q, want green", after.holder)
			}
			if after.identity != made.identity {
				t.Fatalf("the resource was made again: %q, then %q", made.identity, after.identity)
			}
			if after.content != made.content {
				t.Fatalf("the adoption changed the resource:\nbefore %s\nafter  %s", made.content, after.content)
			}
			if got := adopted.GetLabels()[slot.Label]; got != green {
				t.Fatalf("green's object is labelled %q", got)
			}

			// 4. Delete blue's object: it goes, the resource stays.
			if err := k8s.Delete(ctx, old); err != nil {
				t.Fatal(err)
			}
			eventually(t, "blue's object gone", gone(old))
			if s := f.state(t); !s.exists || s.identity != made.identity {
				t.Fatalf("deleting blue's paused object changed Azure: %+v", s)
			}

			if f.taggable {
				// 5. Blue again: refused, without a write reaching Azure.
				writes := f.writes()
				again := f.object(blueNS)
				create(t, again)
				eventually(t, "blue refused", notReadyWith(again, slot.Reason, "cloud resource is held by slot green; this controller is slot blue"))
				eventually(t, "blue's SlotMismatch event", event(again, "cloud resource is held by slot green; this controller is slot blue"))
				if err := k8s.Delete(ctx, again); err != nil {
					t.Fatal(err)
				}
				if f.deletes {
					// Its deletion is refused too: the object stays. Let it go
					// (as Bundt would): skip deletes nothing.
					eventually(t, "blue's deletion refused", notReadyWith(again, slot.Reason, "held by slot green"))
					setPolicy(t, again, annotations.ReconcilePolicySkip)
				}
				// A kind whose deletion leaves the resource (a VaultSecret's
				// detach) makes no write to refuse, and goes.
				eventually(t, "blue's refused object gone", gone(again))
				if n := f.writes(); n != writes {
					t.Fatalf("%d of blue's writes reached Azure after the move", n-writes)
				}
				if s := f.state(t); !s.exists || s.holder != green {
					t.Fatalf("letting blue's object go changed Azure: %+v", s)
				}
			}

			// 6. Green deletes it.
			if err := k8s.Delete(ctx, adopted); err != nil {
				t.Fatal(err)
			}
			eventually(t, "green's object gone", gone(adopted))
			if f.deletes && f.state(t).exists {
				t.Fatal("green's deletion left the resource in Azure")
			}
		})
	}
}

// Without the takeover annotation, a slot does not adopt what another
// slot holds.
func TestNoTakeoverWithoutAnnotation(t *testing.T) {
	f := fixtures()[0]
	f.object = func(ns string) conditioned {
		return &network.IpGroup{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "held"}, Spec: network.IpGroup_Spec{
			AzureName: "slot-held", Location: ptr("westus"), Owner: &genruntime.KnownResourceReference{ARMID: rgID},
		}}
	}
	id := rgID + "/providers/Microsoft.Network/ipGroups/slot-held"
	held := f.object(blueNS)
	create(t, held)
	eventually(t, "blue's object Ready", ready(held))
	writes := armWrites(id)()
	other := f.object(greenNS)
	create(t, other)
	eventually(t, "green refused", notReadyWith(other, slot.Reason, "cloud resource is held by slot blue; this controller is slot green"))
	if n := armWrites(id)(); n != writes {
		t.Fatalf("%d of green's writes reached Azure", n-writes)
	}
	if s := armState(id)(t); s.holder != blue {
		t.Fatalf("the resource holds %q", s.holder)
	}
	setPolicy(t, other, annotations.ReconcilePolicySkip)
	if err := k8s.Delete(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	eventually(t, "green's object gone", gone(other))
	if err := k8s.Delete(context.Background(), held); err != nil {
		t.Fatal(err)
	}
	eventually(t, "blue's object gone", gone(held))
	if armFake.HasResource(id) {
		t.Fatal("blue's deletion left the resource")
	}
}

// armState reads a resource from the fake Resource Manager.
func armState(id string) func(t *testing.T) cloud {
	return func(t *testing.T) cloud {
		t.Helper()
		r, ok := armFake.Resource(id)
		if !ok {
			return cloud{}
		}
		out := cloud{exists: true}
		rest := map[string]any{}
		for k, v := range r {
			rest[k] = v
		}
		if tags, ok := r["tags"].(map[string]any); ok {
			out.holder, _ = tags[slot.Tag].(string)
			other := map[string]any{}
			for k, v := range tags {
				if k != slot.Tag {
					other[k] = v
				}
			}
			rest["tags"] = other
		}
		if sd, ok := r["systemData"].(map[string]any); ok {
			out.identity, _ = sd["createdAt"].(string)
		}
		delete(rest, "systemData")
		raw, err := json.Marshal(rest)
		if err != nil {
			t.Fatal(err)
		}
		out.content = string(raw)
		return out
	}
}

// armWrites counts PUTs, PATCHes and DELETEs of id the fake served.
func armWrites(id string) func() int {
	return func() int {
		n := 0
		for _, l := range armFake.Log {
			f := strings.SplitN(l, " ", 3)
			if len(f) == 3 && f[0] != "GET" && strings.EqualFold(f[1], id) {
				n++
			}
		}
		return n
	}
}

func vaultState(name string) func(t *testing.T) cloud {
	return func(t *testing.T) cloud {
		t.Helper()
		id, tags, ok := vault.Latest(name)
		if !ok {
			return cloud{}
		}
		holder := tags[slot.Tag]
		delete(tags, slot.Tag)
		value, _ := vault.Value(name)
		raw, _ := json.Marshal(map[string]any{"tags": tags, "value": value, "versions": vault.Versions(name)})
		return cloud{exists: true, holder: holder, identity: id, content: string(raw)}
	}
}

func vaultWrites(name string) func() int {
	return func() int {
		n := 0
		for _, l := range vault.Log {
			f := strings.SplitN(l, " ", 2)
			if len(f) == 2 && f[0] != "GET" && strings.HasPrefix(strings.ToLower(f[1]), "/secrets/"+strings.ToLower(name)) {
				n++
			}
		}
		return n
	}
}

// ownerVault stands in for an ASO Vault ASO has provisioned in ns: its
// status names the fake vault.
func ownerVault(t *testing.T, ns string) {
	t.Helper()
	ctx := context.Background()
	v := &asokeyvault.Vault{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "vault"}}
	v.Spec.AzureName = "vault"
	v.Spec.Owner = &genruntime.KnownResourceReference{ARMID: rgID}
	if err := k8s.Create(ctx, v); err != nil {
		t.Fatal(err)
	}
	uri := vaultURL + "/"
	v.Status.Properties = &asokeyvault.VaultProperties_STATUS{VaultUri: &uri}
	v.Status.Conditions = []conditions.Condition{{
		Type: conditions.ConditionTypeReady, Status: metav1.ConditionTrue, Severity: conditions.ConditionSeverityNone,
		Reason: conditions.ReasonSucceeded, LastTransitionTime: metav1.Now(), ObservedGeneration: v.Generation,
	}}
	if err := k8s.Status().Update(ctx, v); err != nil {
		t.Fatal(err)
	}
}

func create(t *testing.T, obj client.Object) {
	t.Helper()
	if err := k8s.Create(context.Background(), obj); err != nil {
		t.Fatal(err)
	}
}

// setPolicy sets obj's reconcile-policy annotation.
func setPolicy(t *testing.T, obj client.Object, policy annotations.ReconcilePolicyValue) {
	t.Helper()
	eventually(t, "setting reconcile-policy "+string(policy), func() error {
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(obj), obj); err != nil {
			return err
		}
		a := obj.GetAnnotations()
		if a == nil {
			a = map[string]string{}
		}
		a[annotations.ReconcilePolicy] = string(policy)
		obj.SetAnnotations(a)
		return k8s.Update(context.Background(), obj)
	})
}

func ready(obj conditioned) func() error {
	return func() error {
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(obj), obj); err != nil {
			return err
		}
		for _, c := range obj.GetConditions() {
			if c.Type == conditions.ConditionTypeReady {
				if c.Status == metav1.ConditionTrue && c.ObservedGeneration == obj.GetGeneration() {
					return nil
				}
				return fmt.Errorf("Ready=%s reason=%s: %s", c.Status, c.Reason, c.Message)
			}
		}
		return fmt.Errorf("no Ready condition yet")
	}
}

// notReadyWith is nil when obj's Ready condition is False with reason and
// a message containing text.
func notReadyWith(obj conditioned, reason, text string) func() error {
	return func() error {
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(obj), obj); err != nil {
			return err
		}
		for _, c := range obj.GetConditions() {
			if c.Type == conditions.ConditionTypeReady {
				if c.Status == metav1.ConditionFalse && c.Reason == reason && strings.Contains(c.Message, text) {
					return nil
				}
				return fmt.Errorf("Ready=%s reason=%s: %s", c.Status, c.Reason, c.Message)
			}
		}
		return fmt.Errorf("no Ready condition yet")
	}
}

// event is nil when obj has a Warning SlotMismatch event with message.
func event(obj client.Object, message string) func() error {
	return func() error {
		var list corev1.EventList
		if err := k8s.List(context.Background(), &list, client.InNamespace(obj.GetNamespace())); err != nil {
			return err
		}
		for _, e := range list.Items {
			if e.InvolvedObject.Name == obj.GetName() && e.Type == corev1.EventTypeWarning && e.Reason == slot.Reason && e.Message == message {
				return nil
			}
		}
		return fmt.Errorf("no Warning %s event %q on %s among %d events", slot.Reason, message, obj.GetName(), len(list.Items))
	}
}

func gone(obj client.Object) func() error {
	return func() error {
		err := k8s.Get(context.Background(), client.ObjectKeyFromObject(obj), obj)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("still there (finalizers %v)", obj.GetFinalizers())
	}
}

func eventually(t *testing.T, what string, f func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var err error
	for time.Now().Before(deadline) {
		if err = f(); err == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("%s: %v", what, err)
}
