// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

//go:build live

package live

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	asoresources "github.com/Azure/azure-service-operator/v2/api/resources/v1api20200601/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
)

const (
	armEndpoint = "https://management.azure.com"
	// How long a real resource may take to become Ready or go away. Virtual
	// hubs and gateways take longer; their tests pass their own.
	timeout = 15 * time.Minute
	poll    = 5 * time.Second
	// How often a long wait says what it is waiting for.
	heartbeat = 4 * time.Minute
)

// armClient is the tests' own view of Resource Manager, with the same
// federated identity the manager uses: fixtures the kinds need (made and
// deleted by the test) and checks of what Azure holds.
type armClient struct {
	cred azcore.TokenCredential
	http *http.Client
}

func newARMClient() (*armClient, error) {
	// Reads AZURE_CLIENT_ID, AZURE_TENANT_ID and AZURE_FEDERATED_TOKEN_FILE.
	cred, err := azidentity.NewWorkloadIdentityCredential(nil)
	if err != nil {
		return nil, err
	}
	return &armClient{cred: cred, http: &http.Client{Timeout: 2 * time.Minute}}, nil
}

// do sends one request; a status of 400 or more is an error with Azure's message.
func (a *armClient) do(ctx context.Context, method, id, apiVersion string, body any) (int, map[string]any, error) {
	status, out, _, err := a.send(ctx, method, armEndpoint+id+"?api-version="+apiVersion, id, body)
	return status, out, err
}

// send is do for a full URL, with the response's headers.
func (a *armClient) send(ctx context.Context, method, url, id string, body any) (int, map[string]any, http.Header, error) {
	tok, err := a.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{armEndpoint + "/.default"}})
	if err != nil {
		return 0, nil, nil, err
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if resp.StatusCode >= 400 {
		// One line, so the run's filtered log shows Azure's message.
		return resp.StatusCode, out, resp.Header, fmt.Errorf("%s %s: %d %s", method, id, resp.StatusCode, strings.Join(strings.Fields(string(raw)), " "))
	}
	return resp.StatusCode, out, resp.Header, nil
}

func (a *armClient) get(ctx context.Context, id, apiVersion string) (int, map[string]any, error) {
	return a.do(ctx, http.MethodGet, id, apiVersion, nil)
}

func rgID() string { return "/subscriptions/" + subID + "/resourceGroups/" + rgName }

// azName is a name in Azure unique to this run: <base>-<run ID>, lower case.
func azName(base string) string { return strings.ToLower(base + "-" + runID) }

// fixture makes a resource the kind under test needs (with a PUT, waiting
// for provisioningState Succeeded) and deletes it when the test ends.
func fixture(t *testing.T, id, apiVersion string, body map[string]any) map[string]any {
	t.Helper()
	return fixtureWait(t, timeout, id, apiVersion, body)
}

// fixtureWait is fixture for resources that take longer than timeout.
func fixtureWait(t *testing.T, wait time.Duration, id, apiVersion string, body map[string]any) map[string]any {
	t.Helper()
	ctx := context.Background()
	_, _, hdr, err := azure.send(ctx, http.MethodPut, armEndpoint+id+"?api-version="+apiVersion, id, body)
	if err != nil {
		// A quota of zero is the subscription's (App Service plans had
		// none, 2026-11-12); raising it is the builder's request to Azure.
		if strings.Contains(err.Error(), "cannot be completed without additional quota") {
			t.Skipf("the subscription has no quota for fixture %s: the builder requests it: %v", id, err)
		}
		t.Fatalf("fixture: %v", err)
	}
	t.Cleanup(func() { deleteARM(t, id, apiVersion) })
	op := hdr.Get("Azure-AsyncOperation")
	var got, failed map[string]any
	eventually(t, wait, "fixture "+id+" Succeeded", func() error {
		// An accepted create can still fail; Azure then drops the
		// resource, and only the operation says why.
		if op != "" {
			_, o, _, err := azure.send(ctx, http.MethodGet, op, id, nil)
			if err == nil && o["status"] == "Failed" {
				failed, _ = o["error"].(map[string]any)
				return nil
			}
		}
		_, r, err := azure.get(ctx, id, apiVersion)
		if err != nil {
			return err
		}
		if s := state(r); s != "Succeeded" && s != "" {
			return fmt.Errorf("provisioningState %s", s)
		}
		got = r
		return nil
	})
	if failed != nil {
		// A region's limits are the subscription's (eastus2, 2026-11-12:
		// Azure SQL ProvisioningDisabled, PostgreSQL B1ms SkuNotAvailable);
		// lifting them is the builder's request to Azure.
		switch failed["code"] {
		case "ProvisioningDisabled", "SkuNotAvailable":
			t.Skipf("Azure refused fixture %s in %s for this subscription: %v: %v", id, location, failed["code"], failed["message"])
		}
		t.Fatalf("fixture %s failed: %v", id, failed)
	}
	return got
}

// deleteARM deletes id if it is still there and waits until it is gone.
func deleteARM(t *testing.T, id, apiVersion string) {
	t.Helper()
	ctx := context.Background()
	code, _, err := azure.do(ctx, http.MethodDelete, id, apiVersion, nil)
	if code == http.StatusNotFound || code == http.StatusNoContent {
		return
	}
	if err != nil {
		t.Errorf("cleanup: %v", err)
		return
	}
	expectGone(t, id, apiVersion)
}

func state(r map[string]any) string {
	s, _ := properties(r)["provisioningState"].(string)
	return s
}

func properties(r map[string]any) map[string]any {
	p, _ := r["properties"].(map[string]any)
	return p
}

// expectResource waits until Azure holds id and check passes on it.
func expectResource(t *testing.T, id, apiVersion string, check func(map[string]any) error) {
	t.Helper()
	eventually(t, timeout, "resource "+id+" in Azure", func() error {
		_, r, err := azure.get(context.Background(), id, apiVersion)
		if err != nil {
			return err
		}
		return check(r)
	})
}

// expectGone waits until Azure answers 404 for id.
func expectGone(t *testing.T, id, apiVersion string) {
	t.Helper()
	eventually(t, timeout, "resource "+id+" deleted in Azure", func() error {
		code, _, err := azure.get(context.Background(), id, apiVersion)
		if code == http.StatusNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("still there")
	})
}

type conditioned interface {
	client.Object
	GetConditions() conditions.Conditions
}

// ready is nil when obj's Ready condition is True for its current generation.
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
				err := fmt.Errorf("Ready=%s reason=%s: %s", c.Status, c.Reason, c.Message)
				if c.Severity == conditions.ConditionSeverityError {
					// ASO does not retry this one: Azure refused the spec.
					return permanent{err}
				}
				return err
			}
		}
		return fmt.Errorf("no Ready condition yet")
	}
}

// permanent is an error eventually does not wait out.
type permanent struct{ error }

func gone(obj client.Object) func() error {
	return func() error {
		err := k8s.Get(context.Background(), client.ObjectKeyFromObject(obj), obj)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("still there")
	}
}

// eventually retries f until it returns nil or the timeout passes.
func eventually(t *testing.T, timeout time.Duration, what string, f func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	// A line every few minutes: offload deletes a VM whose log is silent
	// for 45 minutes, and the condition so far is worth seeing.
	beat := time.Now().Add(heartbeat)
	var err error
	for time.Now().Before(deadline) {
		if err = f(); err == nil {
			return
		}
		if time.Now().After(beat) {
			t.Logf("%s: still waiting: %v", what, err)
			beat = time.Now().Add(heartbeat)
		}
		var p permanent
		if errors.As(err, &p) {
			break
		}
		time.Sleep(poll)
	}
	t.Fatalf("%s: %v", what, err)
}

// create creates objs in order, each Ready before the next, and deletes them
// in reverse order when the test ends (the manager deletes them in Azure),
// waiting for each to go.
func create(t *testing.T, wait time.Duration, objs ...conditioned) {
	t.Helper()
	for _, o := range objs {
		if err := k8s.Create(context.Background(), o); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { remove(t, wait, o) })
		eventually(t, wait, fmt.Sprintf("%T %s Ready", o, o.GetName()), ready(o))
	}
}

// remove deletes obj in Kubernetes and waits until the manager has deleted
// it in Azure and released it.
func remove(t *testing.T, wait time.Duration, obj client.Object) {
	t.Helper()
	if err := k8s.Delete(context.Background(), obj); err != nil && !apierrors.IsNotFound(err) {
		t.Errorf("deleting %T %s: %v", obj, obj.GetName(), err)
		return
	}
	deadline := time.Now().Add(wait)
	beat := time.Now().Add(heartbeat)
	for time.Now().Before(deadline) {
		if gone(obj)() == nil {
			return
		}
		if time.Now().After(beat) {
			t.Logf("%T %s: still deleting", obj, obj.GetName())
			beat = time.Now().Add(heartbeat)
		}
		time.Sleep(poll)
	}
	t.Errorf("%T %s not deleted after %s", obj, obj.GetName(), wait)
}

// namespace makes a namespace for one test with the resource group's
// stand-in, "rg": an ASO ResourceGroup as ASO leaves it once provisioned
// (Ready, with the ARM ID annotation), naming the real live-test group.
func namespace(t *testing.T, name string) string {
	t.Helper()
	ctx := context.Background()
	ns := name + "-" + runID
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil {
		t.Fatalf("creating namespace %s: %v", ns, err)
	}
	rg := &asoresources.ResourceGroup{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "rg",
		Annotations: map[string]string{genruntime.ResourceIDAnnotation: rgID()}}}
	rg.Spec.AzureName = rgName
	rg.Spec.Location = ptr(location)
	if err := k8s.Create(ctx, rg); err != nil {
		t.Fatal(err)
	}
	rg.Status.Conditions = []conditions.Condition{{
		Type: conditions.ConditionTypeReady, Status: metav1.ConditionTrue, Severity: conditions.ConditionSeverityNone,
		Reason: conditions.ReasonSucceeded, LastTransitionTime: metav1.Now(), ObservedGeneration: rg.Generation,
	}}
	if err := k8s.Status().Update(ctx, rg); err != nil {
		t.Fatal(err)
	}
	return ns
}

func owner(name string) *genruntime.KnownResourceReference {
	return &genruntime.KnownResourceReference{Name: name}
}

func ptr[T any](v T) *T { return &v }

// update reads obj, applies change and writes it back.
func update[T client.Object](t *testing.T, obj T, change func(T)) {
	t.Helper()
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(obj), obj); err != nil {
		t.Fatal(err)
	}
	change(obj)
	if err := k8s.Update(context.Background(), obj); err != nil {
		t.Fatal(err)
	}
}

// requireProvider skips the test when the subscription has not registered
// the resource provider ns: registering one is a subscription action the
// identity (Contributor on one group) cannot take; the builder does it
// (docs/live-runs.md, "What cannot run here").
func requireProvider(t *testing.T, ns string) {
	t.Helper()
	_, r, err := azure.get(context.Background(), "/subscriptions/"+subID+"/providers/"+ns, "2021-04-01")
	if err != nil {
		t.Skipf("cannot read the registration of %s: %v", ns, err)
	}
	if s, _ := r["registrationState"].(string); s != "Registered" {
		t.Skipf("the subscription has not registered %s (%s): the builder registers it", ns, s)
	}
}

// requireAction skips the test when the identity may not take action on the
// resource group: Contributor excludes Microsoft.Authorization/*/write, and
// the identity's roles are the builder's (docs/live-runs.md, "Rights
// Contributor does not have"). Read from the group's permissions, so the test
// runs once a role grants it.
func requireAction(t *testing.T, action string) {
	t.Helper()
	_, r, err := azure.get(context.Background(), rgID()+"/providers/Microsoft.Authorization/permissions", "2022-04-01")
	if err != nil {
		t.Skipf("cannot read the identity's permissions on the group: %v", err)
	}
	if !permitted(r, action) {
		t.Skipf("the identity may not %s on the group (Contributor excludes it): the builder grants a role that has it", action)
	}
}

// permitted is whether a permissions list (value: [{actions, notActions}])
// allows action, matching Azure's * wildcards without regard to case.
func permitted(r map[string]any, action string) bool {
	match := func(list any) bool {
		patterns, _ := list.([]any)
		for _, p := range patterns {
			if ok, _ := path.Match(strings.ToLower(fmt.Sprint(p)), strings.ToLower(action)); ok {
				return true
			}
			if strings.HasSuffix(fmt.Sprint(p), "*") && strings.HasPrefix(strings.ToLower(action), strings.ToLower(strings.TrimSuffix(fmt.Sprint(p), "*"))) {
				return true
			}
		}
		return false
	}
	entries, _ := r["value"].([]any)
	for _, e := range entries {
		m, _ := e.(map[string]any)
		if match(m["actions"]) && !match(m["notActions"]) {
			return true
		}
	}
	return false
}
