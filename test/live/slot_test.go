// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

//go:build live

package live

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/Azure/azure-service-operator/v2/pkg/common/annotations"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	network "github.com/highfivelabs/aso-extensions/apis/arm/network/v20260301"
	"github.com/highfivelabs/aso-extensions/pkg/controllers"
	"github.com/highfivelabs/aso-extensions/pkg/manager"
	"github.com/highfivelabs/aso-extensions/pkg/slot"
)

// The slot move against real Azure (docs/plan.md, "Moving resources
// between slots"): two more managers, slots blue and green, each watching
// its own namespace; the run's main manager leaves those namespaces alone.
const blue, green = "blue", "green"

func slotNamespace(s string) string { return "slot-" + s + "-" + runID }

// notSlotNamespaces keeps the main manager out of the slots' namespaces.
func notSlotNamespaces() fields.Selector {
	return fields.AndSelectors(
		fields.OneTermNotEqualSelector("metadata.namespace", slotNamespace(blue)),
		fields.OneTermNotEqualSelector("metadata.namespace", slotNamespace(green)),
	)
}

// startSlotManagers runs the two slots' managers, as production wires them
// but with --slot and AZURE_TARGET_NAMESPACES set.
func startSlotManagers(ctx context.Context, restConfig *rest.Config) error {
	for _, s := range []string{blue, green} {
		// ASO's metrics may be registered once per registry.
		ctrlmetrics.Registry = prometheus.NewRegistry()
		skip := true
		mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
			Scheme:     controllers.CreateScheme(),
			Cache:      cache.Options{ReaderFailOnMissingInformer: true, DefaultNamespaces: map[string]cache.Config{slotNamespace(s): {}}},
			Metrics:    metricsserver.Options{BindAddress: "0"},
			Controller: ctrlconfig.Controller{SkipNameValidation: &skip},
		})
		if err != nil {
			return err
		}
		cfg, err := outoftree.ReadConfigUnvalidated()
		if err != nil {
			return err
		}
		cfg.PodNamespace = "aso-" + s
		cfg.TargetNamespaces = []string{slotNamespace(s)}
		cfg.MaxConcurrentReconciles = 2
		if _, err := manager.Setup(ctx, mgr, cfg, manager.Options{Slot: s}); err != nil {
			return fmt.Errorf("slot %s: %w", s, err)
		}
		go func() {
			if err := mgr.Start(ctx); err != nil {
				fmt.Println("live: slot manager", s+":", err)
			}
		}()
	}
	return nil
}

// An IP group (free) moved from slot blue to slot green: blue makes it and
// tags it; blue is paused; green takes it over without making it again or
// changing it; blue's object is deleted and the group stays; a blue object
// for it again is refused before it reaches Azure; green deletes it.
func TestLiveSlotMove(t *testing.T) {
	ctx := context.Background()
	blueNS, greenNS := namespace(t, "slot-"+blue), namespace(t, "slot-"+green)
	if blueNS != slotNamespace(blue) || greenNS != slotNamespace(green) {
		t.Fatalf("namespaces %s and %s are not the slots' (%s, %s)", blueNS, greenNS, slotNamespace(blue), slotNamespace(green))
	}
	name := azName("slot-ips")
	id := rgID() + "/providers/Microsoft.Network/ipGroups/" + name
	object := func(ns, addr string) *network.IpGroup {
		g := &network.IpGroup{}
		g.Namespace, g.Name = ns, "ips"
		g.Spec = network.IpGroup_Spec{AzureName: name, Owner: owner("rg"), Location: ptr(location),
			IpAddresses: []string{addr}, Tags: map[string]string{"run": runID}}
		return g
	}
	t.Cleanup(func() { deleteARM(t, id, networkAPI) })

	// What Azure holds: the slot tag, the addresses, and the createdTime
	// Resource Manager's resource list gives (IP groups have no systemData).
	read := func() (holder string, addrs []any, created string) {
		t.Helper()
		_, r, err := azure.get(ctx, id, networkAPI)
		if err != nil {
			t.Fatal(err)
		}
		tags, _ := r["tags"].(map[string]any)
		holder, _ = tags[slot.Tag].(string)
		addrs, _ = properties(r)["ipAddresses"].([]any)
		filter := "2021-04-01&%24expand=createdTime&%24filter=name%20eq%20%27" + name + "%27"
		_, list, err := azure.get(ctx, rgID()+"/resources", filter)
		if err != nil {
			t.Fatal(err)
		}
		items, _ := list["value"].([]any)
		if len(items) != 1 {
			t.Fatalf("the resource list has %d entries for %s", len(items), name)
		}
		created, _ = items[0].(map[string]any)["createdTime"].(string)
		return holder, addrs, created
	}

	old := object(blueNS, "10.0.0.0/24")
	create(t, timeout, old)
	holder, addrs, created := read()
	if holder != blue || created == "" {
		t.Fatalf("blue made it: holder %q, createdTime %q", holder, created)
	}
	if old.Labels[slot.Label] != blue {
		t.Fatalf("blue's object is labelled %q", old.Labels[slot.Label])
	}

	setReconcilePolicy(t, old, annotations.ReconcilePolicySkip)

	adopted := object(greenNS, "10.0.0.0/24")
	adopted.Annotations = map[string]string{slot.TakeoverAnnotation: blue}
	create(t, timeout, adopted)
	h, a, c := read()
	if h != green {
		t.Fatalf("after the takeover the group holds slot %q", h)
	}
	if c != created || !reflect.DeepEqual(a, addrs) {
		t.Fatalf("the takeover changed the group: createdTime %q then %q, addresses %v then %v", created, c, addrs, a)
	}

	remove(t, timeout, old)
	if h, _, c := read(); h != green || c != created {
		t.Fatalf("deleting blue's paused object changed the group: holder %q, createdTime %q", h, c)
	}

	// Blue again, wanting another address: refused, Azure unchanged.
	again := object(blueNS, "10.9.0.0/24")
	if err := k8s.Create(ctx, again); err != nil {
		t.Fatal(err)
	}
	want := "cloud resource is held by slot green; this controller is slot blue"
	eventually(t, timeout, "blue refused", func() error {
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(again), again); err != nil {
			return err
		}
		for _, cond := range again.Status.Conditions {
			if cond.Type == "Ready" && cond.Status == metav1.ConditionFalse && cond.Reason == slot.Reason && strings.Contains(cond.Message, want) {
				return nil
			}
		}
		return fmt.Errorf("conditions %v", again.Status.Conditions)
	})
	eventually(t, timeout, "blue's SlotMismatch event", func() error {
		var list corev1.EventList
		if err := k8s.List(ctx, &list, client.InNamespace(blueNS)); err != nil {
			return err
		}
		for _, e := range list.Items {
			if e.InvolvedObject.Name == again.Name && e.Type == corev1.EventTypeWarning && e.Reason == slot.Reason && e.Message == want {
				return nil
			}
		}
		return fmt.Errorf("no event among %d", len(list.Items))
	})
	if h, a, c := read(); h != green || c != created || !reflect.DeepEqual(a, addrs) {
		t.Fatalf("blue's refused write reached Azure: holder %q, addresses %v, createdTime %q", h, a, c)
	}
	setReconcilePolicy(t, again, annotations.ReconcilePolicySkip)
	remove(t, timeout, again)

	remove(t, timeout, adopted)
	code, _, _ := azure.get(ctx, id, networkAPI)
	if code != http.StatusNotFound {
		expectGone(t, id, networkAPI)
	}
}

func setReconcilePolicy(t *testing.T, obj client.Object, policy annotations.ReconcilePolicyValue) {
	t.Helper()
	eventually(t, timeout, "reconcile-policy "+string(policy), func() error {
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
