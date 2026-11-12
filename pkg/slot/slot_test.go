// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package slot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/Azure/azure-service-operator/v2/pkg/common/annotations"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"

	ipgroups "github.com/highfivelabs/aso-extensions/apis/arm/network/v20260301/storage"
	querypacks "github.com/highfivelabs/aso-extensions/apis/arm/operationalinsights/v20250701/storage"
	authorizationv1 "github.com/highfivelabs/aso-extensions/apis/authorization/v1"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/arm"
)

const (
	rg = "/subscriptions/00000000-0000-0000-0000-0000000000f1/resourceGroups/rg"
	id = rg + "/providers/Microsoft.Network/ipGroups/ips"
)

func TestResolve(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == EnvVar {
				return v
			}
			return ""
		}
	}
	for _, c := range []struct{ flag, env, want string }{
		{"", "", ""},
		{"", "blue", "blue"},
		{"green", "blue", "green"},
		{"green", "", "green"},
	} {
		if got := Resolve(c.flag, env(c.env)); got != c.want {
			t.Errorf("Resolve(%q, HFL_SLOT=%q) = %q, want %q", c.flag, c.env, got, c.want)
		}
	}
	if New("") != nil {
		t.Error("New(\"\") is a guard; no slot must mean no guard")
	}
}

// fakeARM is the fake Resource Manager behind a guard for slot.
func fakeARM(t *testing.T, slot string) (*arm.ARM, *http.Client) {
	t.Helper()
	a := arm.New(func(string) bool { return true })
	a.AddResource(rg)
	srv := httptest.NewServer(a)
	t.Cleanup(srv.Close)
	return a, &http.Client{Transport: New(slot).RoundTripper(rewrite(srv.URL))}
}

// rewrite sends every request to the test server.
func rewrite(base string) http.RoundTripper {
	return roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host = "http", strings.TrimPrefix(base, "http://")
		r.Host = r.URL.Host
		return http.DefaultTransport.RoundTrip(r)
	})
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func send(t *testing.T, ctx context.Context, c *http.Client, method, body string) (int, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://management.test"+id+"?api-version=2026-03-01", r)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer ok")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func storedTag(a *arm.ARM) string {
	r, ok := a.Resource(id)
	if !ok {
		return "<none>"
	}
	tags, _ := r["tags"].(map[string]any)
	s, _ := tags[Tag].(string)
	return s
}

func methods(a *arm.ARM, method string) int {
	n := 0
	for _, l := range a.Log {
		if strings.HasPrefix(l, method+" ") {
			n++
		}
	}
	return n
}

// The guard writes the tag on create and keeps it on update; the controller
// never sees it (so it is never a difference between spec and Azure).
func TestTagWrittenAndHidden(t *testing.T) {
	a, blue := fakeARM(t, "blue")
	ctx, w := WithWrite(context.Background(), true, "")
	code, body := send(t, ctx, blue, http.MethodPut, `{"location":"westus","tags":{"team":"a"},"properties":{}}`)
	if code != http.StatusCreated || w.Holder != "" {
		t.Fatalf("create: %d, refused by %q", code, w.Holder)
	}
	if got := storedTag(a); got != "blue" {
		t.Fatalf("the resource holds slot %q, want blue", got)
	}
	tags, _ := body["tags"].(map[string]any)
	if _, ok := tags[Tag]; ok || tags["team"] != "a" {
		t.Fatalf("the response shows tags %v; want the spec's tags only", tags)
	}

	ctx, _ = WithWrite(context.Background(), true, "")
	_, body = send(t, ctx, blue, http.MethodGet, "")
	if tags, _ := body["tags"].(map[string]any); tags[Tag] != nil {
		t.Fatalf("a GET shows the slot tag: %v", tags)
	}
	ctx, _ = WithWrite(context.Background(), true, "")
	if code, _ := send(t, ctx, blue, http.MethodPut, `{"location":"westus","properties":{}}`); code != http.StatusOK {
		t.Fatalf("update in the same slot: %d", code)
	}
	if got := storedTag(a); got != "blue" {
		t.Fatalf("after an update without tags the resource holds %q, want blue", got)
	}
}

// Another slot can read, but its writes and deletes are refused before they
// reach Azure.
func TestOtherSlotRefused(t *testing.T) {
	a, blue := fakeARM(t, "blue")
	ctx, _ := WithWrite(context.Background(), true, "")
	send(t, ctx, blue, http.MethodPut, `{"location":"westus","properties":{}}`)

	green := &http.Client{Transport: New("green").RoundTripper(blue.Transport.(*roundTripper).next)}

	puts, deletes := methods(a, "PUT"), methods(a, "DELETE")
	for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		ctx, w := WithWrite(context.Background(), true, "")
		body := ""
		if m != http.MethodDelete {
			body = `{"location":"westus","properties":{}}`
		}
		code, out := send(t, ctx, green, m, body)
		if code != http.StatusConflict || w.Holder != "blue" {
			t.Fatalf("%s from green: %d, holder %q; want 409 and blue", m, code, w.Holder)
		}
		if e, _ := out["error"].(map[string]any); e["code"] != Reason {
			t.Fatalf("%s from green: error %v, want code %s", m, out, Reason)
		}
	}
	if methods(a, "PUT") != puts || methods(a, "DELETE") != deletes || methods(a, "PATCH") != 0 {
		t.Fatalf("a refused write reached Azure: %v", a.Log)
	}
	if !a.HasResource(id) || storedTag(a) != "blue" {
		t.Fatal("the resource changed")
	}
	ctx, w := WithWrite(context.Background(), true, "")
	code, out := send(t, ctx, green, http.MethodGet, "")
	if code != http.StatusOK || w.Holder != "" {
		t.Fatalf("a read from green: %d, refused %q", code, w.Holder)
	}
	// Only a slot's own tag is hidden: a reconciler that writes only when
	// Azure differs from its spec sees another slot's tag as a difference,
	// so it writes (and, on a takeover, claims the resource).
	if tags, _ := out["tags"].(map[string]any); tags[Tag] != "blue" {
		t.Fatalf("green reads tags %v; want blue's tag shown", tags)
	}
}

// A slot takes a resource over only from the slot its object names; it then
// holds it, and the old slot is refused.
func TestTakeover(t *testing.T) {
	a, blue := fakeARM(t, "blue")
	ctx, _ := WithWrite(context.Background(), true, "")
	send(t, ctx, blue, http.MethodPut, `{"location":"westus","properties":{}}`)
	green := &http.Client{Transport: New("green").RoundTripper(blue.Transport.(*roundTripper).next)}

	ctx, w := WithWrite(context.Background(), true, "red")
	if code, _ := send(t, ctx, green, http.MethodPut, `{"location":"westus","properties":{}}`); code != http.StatusConflict || w.Holder != "blue" {
		t.Fatalf("takeover from the wrong slot: %d %q", code, w.Holder)
	}
	ctx, w = WithWrite(context.Background(), true, "blue")
	if code, _ := send(t, ctx, green, http.MethodPut, `{"location":"westus","properties":{}}`); code != http.StatusOK || w.Holder != "" {
		t.Fatalf("takeover from blue: %d %q", code, w.Holder)
	}
	if storedTag(a) != "green" {
		t.Fatalf("after the takeover the resource holds %q", storedTag(a))
	}
	ctx, w = WithWrite(context.Background(), true, "")
	if code, _ := send(t, ctx, blue, http.MethodPut, `{"location":"westus","properties":{}}`); code != http.StatusConflict || w.Holder != "green" {
		t.Fatalf("blue after the move: %d %q", code, w.Holder)
	}
}

// Requests outside a guarded write, and writes of kinds that cannot carry
// tags, pass unchanged.
func TestPassThrough(t *testing.T) {
	a, blue := fakeARM(t, "blue")
	if code, _ := send(t, context.Background(), blue, http.MethodPut, `{"location":"westus","properties":{}}`); code != http.StatusCreated {
		t.Fatalf("unguarded create: %d", code)
	}
	if storedTag(a) != "" {
		t.Fatal("an unguarded request was tagged")
	}
	ctx, _ := WithWrite(context.Background(), false, "")
	send(t, ctx, blue, http.MethodPut, `{"location":"westus","properties":{}}`)
	if storedTag(a) != "" {
		t.Fatal("a kind without tags was tagged")
	}
}

// fakeReconciler sends one request through client when asked to write.
type fakeReconciler struct {
	client *http.Client
	method string
	t      *testing.T
}

func (f *fakeReconciler) CreateOrUpdate(ctx context.Context, _ logr.Logger, _ record.EventRecorder, _ genruntime.MetaObject, _ annotations.ResolvedReconcilePolicies) (ctrl.Result, error) {
	send(f.t, ctx, f.client, f.method, `{"location":"westus","properties":{}}`)
	return ctrl.Result{}, nil
}

func (f *fakeReconciler) Delete(ctx context.Context, _ logr.Logger, _ record.EventRecorder, _ genruntime.MetaObject) (ctrl.Result, error) {
	send(f.t, ctx, f.client, http.MethodDelete, "")
	return ctrl.Result{}, nil
}

func (f *fakeReconciler) Claim(context.Context, logr.Logger, record.EventRecorder, genruntime.MetaObject) error {
	return nil
}

func (f *fakeReconciler) UpdateStatus(context.Context, logr.Logger, record.EventRecorder, genruntime.MetaObject, annotations.ResolvedReconcilePolicies) error {
	return nil
}

// The reconciler wrapper labels the object with its slot, and on a refusal
// returns a SlotMismatch condition error and a Warning event.
func TestReconciler(t *testing.T) {
	a, blue := fakeARM(t, "blue")
	ctx, _ := WithWrite(context.Background(), true, "")
	send(t, ctx, blue, http.MethodPut, `{"location":"westus","properties":{}}`)
	green := &http.Client{Transport: New("green").RoundTripper(blue.Transport.(*roundTripper).next)}

	obj := &ipgroups.IpGroup{ObjectMeta: metav1.ObjectMeta{Name: "ips", Namespace: "ns"}}
	rec := record.NewFakeRecorder(10)
	r := New("green").Reconciler(&fakeReconciler{client: green, method: http.MethodPut, t: t})
	_, err := r.CreateOrUpdate(context.Background(), logr.Discard(), rec, obj, annotations.ResolvedReconcilePolicies{})
	ready, ok := conditions.AsReadyConditionImpactingError(err)
	if !ok || ready.Reason != Reason || ready.Severity != conditions.ConditionSeverityWarning {
		t.Fatalf("CreateOrUpdate in the wrong slot: %v", err)
	}
	want := "Warning SlotMismatch cloud resource is held by slot blue; this controller is slot green"
	if got := <-rec.Events; got != want {
		t.Fatalf("event %q, want %q", got, want)
	}
	if obj.Labels[Label] != "" {
		t.Fatalf("a refused object is labelled %q", obj.Labels[Label])
	}
	if _, err := r.Delete(context.Background(), logr.Discard(), rec, obj); err == nil {
		t.Fatal("Delete in the wrong slot succeeded")
	}
	<-rec.Events
	if !a.HasResource(id) {
		t.Fatal("the resource was deleted")
	}

	obj.Annotations = map[string]string{TakeoverAnnotation: "blue"}
	if _, err := r.CreateOrUpdate(context.Background(), logr.Discard(), rec, obj, annotations.ResolvedReconcilePolicies{}); err != nil {
		t.Fatalf("takeover: %v", err)
	}
	if obj.Labels[Label] != "green" {
		t.Fatalf("label %q after the takeover, want green", obj.Labels[Label])
	}
	if storedTag(a) != "green" {
		t.Fatalf("the resource holds %q", storedTag(a))
	}
}

// Taggable is whether a kind's spec has tags.
func TestTaggable(t *testing.T) {
	if !Taggable(&ipgroups.IpGroup{}) {
		t.Error("IpGroup has tags")
	}
	if Taggable(&authorizationv1.ManagementLock{}) {
		t.Error("ManagementLock has no tags")
	}
	// A query's tags are lists of strings, not the resource tags the slot
	// tag can be one of.
	if Taggable(&querypacks.QueryPacksQuery{}) {
		t.Error("QueryPacksQuery has no resource tags")
	}
}

// TakeoverChanged passes an update that adds, changes or removes the
// takeover annotation, and nothing else.
func TestTakeoverChanged(t *testing.T) {
	p := TakeoverChanged()
	obj := func(a map[string]string) *ipgroups.IpGroup {
		return &ipgroups.IpGroup{ObjectMeta: metav1.ObjectMeta{Name: "ips", Annotations: a}}
	}
	for _, c := range []struct {
		old, new map[string]string
		want     bool
	}{
		{nil, map[string]string{TakeoverAnnotation: "blue"}, true},
		{map[string]string{TakeoverAnnotation: "blue"}, map[string]string{TakeoverAnnotation: "red"}, true},
		{map[string]string{TakeoverAnnotation: "blue"}, nil, true},
		{map[string]string{TakeoverAnnotation: "blue"}, map[string]string{TakeoverAnnotation: "blue", "x": "y"}, false},
		{nil, map[string]string{"x": "y"}, false},
	} {
		if got := p.Update(event.UpdateEvent{ObjectOld: obj(c.old), ObjectNew: obj(c.new)}); got != c.want {
			t.Errorf("%v -> %v: %v, want %v", c.old, c.new, got, c.want)
		}
	}
	if p.Create(event.CreateEvent{Object: obj(map[string]string{TakeoverAnnotation: "blue"})}) {
		t.Error("a create passes; the standard predicate already does")
	}
}
