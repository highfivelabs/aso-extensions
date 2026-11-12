// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Package slot is the slot guard (docs/plan.md, "Moving resources between
// slots"). Hosted Bundt runs one version of every provider per infra
// cluster, a slot, and moves an organisation's objects from one slot to
// another; the cloud resource carries the slot that holds it, as a tag, so
// two slots never write the same resource.
//
// The guard is off unless the manager is given a slot (--slot, or HFL_SLOT).
// When it is on, every write a reconciler makes to a resource that can carry
// tags (PUT, PATCH, DELETE, and a Key Vault certificate import) is checked
// first with a GET of the same resource:
//
//   - no slot tag, or this slot's: the write goes ahead, with this slot's
//     tag added to the body (it claims the resource);
//   - another slot's tag: the write is refused without reaching Azure; the
//     object's Ready condition is False with reason SlotMismatch and a
//     Warning event says which slot holds it;
//   - the slot named by the object's hfl.dev/slot-takeover annotation:
//     the write goes ahead and moves the resource to this slot (the
//     adopting half of a move).
//
// This slot's tag is removed from every response the reconciler reads, so
// the slot is never a difference between an object's spec and Azure. Reads are
// never refused. The object is labelled hfl.dev/slot with the slot after
// each successful create or update.
package slot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/go-logr/logr"
	"github.com/rotisserie/eris"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/Azure/azure-service-operator/v2/pkg/common/annotations"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/retry"
)

const (
	// Tag is the cloud resource's tag naming the slot that holds it. Azure
	// tag names cannot hold "/", so it is not the label's name.
	Tag = "hfl-dev-slot"
	// Label is the Kubernetes object's label naming its controller's slot.
	Label = "hfl.dev/slot"
	// TakeoverAnnotation, on an object, names the slot this controller may
	// take the object's cloud resource over from.
	TakeoverAnnotation = "hfl.dev/slot-takeover"
	// Reason is the Ready condition's reason, the event's reason and the
	// refused request's error code.
	Reason = "SlotMismatch"
	// EnvVar is the environment variable the slot is read from when the
	// --slot flag is not given.
	EnvVar = "HFL_SLOT"
)

// Resolve is the manager's slot: the flag's value, else HFL_SLOT's.
func Resolve(flagValue string, getenv func(string) string) string {
	if flagValue != "" {
		return flagValue
	}
	return getenv(EnvVar)
}

// Guard enforces one slot. A nil Guard is no guard.
type Guard struct {
	Slot string
}

// New is the guard for slot, or nil when slot is empty.
func New(slot string) *Guard {
	if slot == "" {
		return nil
	}
	return &Guard{Slot: slot}
}

// Write is one guarded call of a reconciler, carried in its context.
type Write struct {
	taggable     bool
	takeoverFrom string
	// Holder is the slot that holds the resource when a write was refused.
	Holder string
}

type writeKey struct{}

// WithWrite marks ctx as a reconciler's write: requests made with it are
// guarded when taggable.
func WithWrite(ctx context.Context, taggable bool, takeoverFrom string) (context.Context, *Write) {
	w := &Write{taggable: taggable, takeoverFrom: takeoverFrom}
	return context.WithValue(ctx, writeKey{}, w), w
}

func writeFrom(ctx context.Context) *Write {
	w, _ := ctx.Value(writeKey{}).(*Write)
	return w
}

// Message is the refusal's text.
func (g *Guard) Message(holder string) string {
	return fmt.Sprintf("cloud resource is held by slot %s; this controller is slot %s", holder, g.Slot)
}

// RoundTripper guards the requests sent through next (nil:
// http.DefaultTransport). A nil Guard returns next unchanged.
func (g *Guard) RoundTripper(next http.RoundTripper) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	if g == nil {
		return next
	}
	return &roundTripper{guard: g, next: next}
}

// Transporter guards an Azure SDK client's transport (nil: the SDK's
// default HTTP client). A nil Guard returns t unchanged.
func (g *Guard) Transporter(t policy.Transporter) policy.Transporter {
	if g == nil {
		return t
	}
	var next http.RoundTripper
	if t != nil {
		next = transporterFunc(t.Do)
	}
	return &http.Client{Transport: g.RoundTripper(next)}
}

type transporterFunc func(*http.Request) (*http.Response, error)

func (f transporterFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type roundTripper struct {
	guard *Guard
	next  http.RoundTripper
}

func (rt *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	w := writeFrom(req.Context())
	if w == nil || !w.taggable {
		return rt.next.RoundTrip(req)
	}
	target, ok := guardedTarget(req)
	if ok {
		holder, resp, err := rt.holder(req, target)
		if err != nil || resp != nil {
			return resp, err
		}
		if holder != "" && holder != rt.guard.Slot && holder != w.takeoverFrom {
			w.Holder = holder
			return refusal(req, rt.guard.Message(holder)), nil
		}
		if req.Method != http.MethodDelete && req.Body != nil {
			if req, err = rt.tagBody(req); err != nil {
				return nil, err
			}
		}
	}
	resp, err := rt.next.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	return hideTag(resp, rt.guard.Slot)
}

// guardedTarget is the URL of the resource a write changes, and whether
// the request is a write.
func guardedTarget(req *http.Request) (string, bool) {
	switch req.Method {
	case http.MethodPut, http.MethodPatch, http.MethodDelete:
		return req.URL.String(), true
	case http.MethodPost:
		// A Key Vault certificate import makes a new version of
		// /certificates/<name>. Other POSTs (listKeys and other actions)
		// are not guarded.
		if strings.HasSuffix(req.URL.Path, "/import") {
			u := *req.URL
			u.Path = strings.TrimSuffix(u.Path, "/import")
			u.RawPath = ""
			return u.String(), true
		}
	}
	return "", false
}

// holder reads the slot tag of the resource at target. A response is
// returned (and no holder) when the read failed with anything but not
// found: the write is not sent without knowing who holds the resource.
func (rt *roundTripper) holder(req *http.Request, target string) (string, *http.Response, error) {
	get, err := http.NewRequestWithContext(req.Context(), http.MethodGet, target, nil)
	if err != nil {
		return "", nil, err
	}
	for k, v := range req.Header {
		switch http.CanonicalHeaderKey(k) {
		case "Content-Type", "Content-Length", "If-Match", "If-None-Match":
			continue
		}
		get.Header[k] = v
	}
	resp, err := rt.next.RoundTrip(get)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return "", nil, nil
	case resp.StatusCode >= 300:
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		return "", resp, nil
	}
	var body struct {
		Tags map[string]string `json:"tags"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", nil, eris.Wrapf(err, "reading the slot tag of %s", target)
	}
	return body.Tags[Tag], nil, nil
}

// tagBody adds this slot's tag to a write's JSON body.
func (rt *roundTripper) tagBody(req *http.Request) (*http.Request, error) {
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	body := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, eris.Wrap(err, "adding the slot tag to a request body that is not a JSON object")
		}
	}
	tags, _ := body["tags"].(map[string]any)
	if tags == nil {
		tags = map[string]any{}
	}
	tags[Tag] = rt.guard.Slot
	body["tags"] = tags
	out, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req = req.Clone(req.Context())
	req.Body = io.NopCloser(bytes.NewReader(out))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(out)), nil }
	req.ContentLength = int64(len(out))
	req.Header.Set("Content-Length", strconv.Itoa(len(out)))
	return req, nil
}

// hideTag removes this slot's tag from a JSON response's tags. Another
// slot's tag is left: to a reconciler that writes only when Azure differs
// from its spec it is a difference, so a takeover writes (and claims).
func hideTag(resp *http.Response, slot string) (*http.Response, error) {
	if resp.Body == nil || !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		return resp, nil
	}
	raw, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	out := raw
	if bytes.Contains(raw, []byte(Tag)) {
		body := map[string]any{}
		if json.Unmarshal(raw, &body) == nil {
			if tags, ok := body["tags"].(map[string]any); ok && tags[Tag] == slot {
				delete(tags, Tag)
				if b, err := json.Marshal(body); err == nil {
					out = b
				}
			}
		}
	}
	resp.Body = io.NopCloser(bytes.NewReader(out))
	resp.ContentLength = int64(len(out))
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	return resp, nil
}

// refusal is the response to a refused write: a Resource Manager (and Key
// Vault) style error, 409 Conflict with code SlotMismatch.
func refusal(req *http.Request, message string) *http.Response {
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"code": Reason, "message": message}})
	return &http.Response{
		StatusCode:    http.StatusConflict,
		Status:        "409 Conflict",
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": {"application/json"}, "X-Ms-Error-Code": {Reason}},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

// Taggable is whether obj's cloud resource can carry tags: whether its spec
// has a tags map of strings to strings (a few kinds' "tags" are something
// else, such as a query's lists of labels).
func Taggable(obj genruntime.MetaObject) bool {
	v := reflect.ValueOf(obj)
	for v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return false
	}
	spec := v.FieldByName("Spec")
	if !spec.IsValid() || spec.Kind() != reflect.Struct {
		return false
	}
	tags := spec.FieldByName("Tags")
	return tags.IsValid() && tags.Kind() == reflect.Map &&
		tags.Type().Key().Kind() == reflect.String && tags.Type().Elem().Kind() == reflect.String
}

// Reconciler wraps a kind's reconciler so its writes are guarded. A nil
// Guard returns inner unchanged.
func (g *Guard) Reconciler(inner genruntime.Reconciler) genruntime.Reconciler {
	if g == nil {
		return inner
	}
	return &reconciler{guard: g, inner: inner}
}

type reconciler struct {
	guard *Guard
	inner genruntime.Reconciler
}

var _ genruntime.Reconciler = &reconciler{}

func (r *reconciler) write(ctx context.Context, obj genruntime.MetaObject) (context.Context, *Write) {
	return WithWrite(ctx, Taggable(obj), obj.GetAnnotations()[TakeoverAnnotation])
}

// refused turns a refused write into the object's condition and an event.
func (r *reconciler) refused(eventRecorder record.EventRecorder, obj genruntime.MetaObject, w *Write) error {
	message := r.guard.Message(w.Holder)
	eventRecorder.Event(obj, corev1.EventTypeWarning, Reason, message)
	return conditions.NewReadyConditionImpactingError(
		eris.New(message),
		conditions.ConditionSeverityWarning,
		conditions.Reason{Name: Reason, RetryClassification: retry.Slow},
	)
}

func (r *reconciler) CreateOrUpdate(
	ctx context.Context,
	log logr.Logger,
	eventRecorder record.EventRecorder,
	obj genruntime.MetaObject,
	reconcilePolicies annotations.ResolvedReconcilePolicies,
) (ctrl.Result, error) {
	ctx, w := r.write(ctx, obj)
	result, err := r.inner.CreateOrUpdate(ctx, log, eventRecorder, obj, reconcilePolicies)
	if w.Holder != "" {
		return ctrl.Result{}, r.refused(eventRecorder, obj, w)
	}
	if err == nil {
		genruntime.AddLabel(obj, Label, r.guard.Slot)
	}
	return result, err
}

func (r *reconciler) Delete(
	ctx context.Context,
	log logr.Logger,
	eventRecorder record.EventRecorder,
	obj genruntime.MetaObject,
) (ctrl.Result, error) {
	ctx, w := r.write(ctx, obj)
	result, err := r.inner.Delete(ctx, log, eventRecorder, obj)
	if w.Holder != "" {
		return ctrl.Result{}, r.refused(eventRecorder, obj, w)
	}
	return result, err
}

func (r *reconciler) Claim(ctx context.Context, log logr.Logger, eventRecorder record.EventRecorder, obj genruntime.MetaObject) error {
	return r.inner.Claim(ctx, log, eventRecorder, obj)
}

// UpdateStatus only reads; its context is marked so the tag is hidden.
func (r *reconciler) UpdateStatus(
	ctx context.Context,
	log logr.Logger,
	eventRecorder record.EventRecorder,
	obj genruntime.MetaObject,
	reconcilePolicies annotations.ResolvedReconcilePolicies,
) error {
	ctx, _ = r.write(ctx, obj)
	return r.inner.UpdateStatus(ctx, log, eventRecorder, obj, reconcilePolicies)
}

// TakeoverChanged is a predicate that passes an update whose takeover
// annotation changed, so an object refused for another slot's tag is
// reconciled as soon as it is given the annotation, not at its next slow
// retry. It is part of every kind's predicate; with no guard it only causes
// a reconcile that changes nothing.
func TakeoverChanged() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return false },
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			if e.ObjectOld == nil || e.ObjectNew == nil {
				return false
			}
			return e.ObjectOld.GetAnnotations()[TakeoverAnnotation] != e.ObjectNew.GetAnnotations()[TakeoverAnnotation]
		},
	}
}
