// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"

	authv1 "github.com/highfivelabs/aso-extensions/apis/authorization/v1"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/arm"
	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/entra"
)

// asoResourceGroup stands in for an ASO ResourceGroup ASO has provisioned:
// the ARM ID annotation ASO writes and a Ready condition.
func asoResourceGroup(t *testing.T, ns, name, armID string) {
	t.Helper()
	ctx := context.Background()
	rg := &unstructured.Unstructured{}
	rg.SetAPIVersion("resources.azure.com/v1api20200601storage")
	rg.SetKind("ResourceGroup")
	rg.SetNamespace(ns)
	rg.SetName(name)
	rg.SetAnnotations(map[string]string{genruntime.ResourceIDAnnotation: armID})
	_ = unstructured.SetNestedField(rg.Object, name, "spec", "azureName")
	_ = unstructured.SetNestedField(rg.Object, "westus", "spec", "location")
	if err := k8s.Create(ctx, rg); err != nil {
		t.Fatal(err)
	}
	ready := asoReady(rg.GetGeneration())
	_ = unstructured.SetNestedSlice(rg.Object, []any{map[string]any{
		"type": string(ready.Type), "status": string(ready.Status), "severity": "", "reason": ready.Reason,
		"lastTransitionTime": ready.LastTransitionTime.UTC().Format("2006-01-02T15:04:05Z"), "observedGeneration": ready.ObservedGeneration,
	}}, "status", "conditions")
	if err := k8s.Status().Update(ctx, rg); err != nil {
		t.Fatal(err)
	}
}

// armDo sends a request to the fake Resource Manager as ns's aso-workload
// identity (client A), as anything else using Azure would, and returns the
// status and the error code.
func armDo(t *testing.T, ns, method, id string) (int, string) {
	t.Helper()
	token := entra.Token(entra.Claims{Tenant: tenantID, ClientID: clientA,
		Subject: entra.ServiceAccountSubject(ns, outoftree.DefaultWorkloadIdentityServiceAccount), Scope: armScope})
	var body io.Reader
	if method == http.MethodPut {
		body = strings.NewReader(`{"properties":{}}`)
	}
	req, err := http.NewRequest(method, armURL+id+"?api-version=2024-03-01", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := armHTTP.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	code := ""
	if i := strings.Index(string(b), `"code":"`); i >= 0 {
		code = strings.SplitN(string(b)[i+8:], `"`, 2)[0]
	}
	return resp.StatusCode, code
}

func expectARM(t *testing.T, ns, method, id string, wantStatus int, wantCode string) {
	t.Helper()
	status, code := armDo(t, ns, method, id)
	if status != wantStatus || code != wantCode {
		t.Fatalf("%s %s: %d %q, want %d %q", method, id, status, code, wantStatus, wantCode)
	}
}

func expectLock(t *testing.T, id string, want arm.LockProperties) {
	t.Helper()
	eventually(t, timeout, "lock "+id, func() error {
		got, ok := armFake.Lock(id)
		if !ok {
			return fmt.Errorf("no lock; locks: %v", armFake.Locks())
		}
		if got != want {
			return fmt.Errorf("got %+v, want %+v", got, want)
		}
		return nil
	})
}

func deleteLock(t *testing.T, l *authv1.ManagementLock) {
	t.Helper()
	ctx := context.Background()
	if err := k8s.Delete(ctx, l); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "ManagementLock "+l.Name+" gone", func() error {
		err := k8s.Get(ctx, client.ObjectKeyFromObject(l), &authv1.ManagementLock{})
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("still there: %v", err)
	})
}

// A lock on an ASO ResourceGroup: set in Resource Manager with the
// namespace's identity on the ARM ID ASO recorded; while it is
// CanNotDelete, the resource group and what is in it cannot be deleted but
// can be changed; ReadOnly stops changes too; deleting the lock lets
// deletes through again.
func TestManagementLockOnResourceGroup(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "lock-a")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/lock-a-rg"
	accountID := rgID + "/providers/Microsoft.Storage/storageAccounts/lockadata"
	armFake.AddResource(rgID)
	armFake.AddResource(accountID)
	asoResourceGroup(t, ns, "rg", rgID)

	l := &authv1.ManagementLock{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "keep-rg"},
		Spec: authv1.ManagementLockSpec{
			Owner: &genruntime.ArbitraryOwnerReference{Group: "resources.azure.com", Kind: "ResourceGroup", Name: "rg"},
			Level: "CanNotDelete",
			Notes: "production data",
		},
	}
	if err := k8s.Create(ctx, l); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "ManagementLock Ready", ready(l))
	lockID := rgID + "/providers/Microsoft.Authorization/locks/keep-rg"
	expectLock(t, lockID, arm.LockProperties{Level: "CanNotDelete", Notes: "production data"})
	if l.Status.Id != lockID || l.Status.Scope != rgID || l.Status.Level != "CanNotDelete" || l.Annotations[genruntime.ResourceIDAnnotation] != lockID {
		t.Fatalf("status %+v, resource-id %q", l.Status, l.Annotations[genruntime.ResourceIDAnnotation])
	}
	if refs := l.GetOwnerReferences(); len(refs) != 1 || refs[0].Kind != "ResourceGroup" || refs[0].Name != "rg" {
		t.Fatalf("owner references: %+v", refs)
	}

	// CanNotDelete: no deletes at or under the resource group; changes pass.
	expectARM(t, ns, http.MethodDelete, rgID, http.StatusConflict, "ScopeLocked")
	expectARM(t, ns, http.MethodDelete, accountID, http.StatusConflict, "ScopeLocked")
	expectARM(t, ns, http.MethodPut, accountID, http.StatusOK, "")

	// ReadOnly: changes are refused too.
	update := func(change func(*authv1.ManagementLock)) {
		t.Helper()
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(l), l); err != nil {
			t.Fatal(err)
		}
		change(l)
		if err := k8s.Update(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	update(func(l *authv1.ManagementLock) { l.Spec.Level = "ReadOnly"; l.Spec.Notes = "frozen" })
	eventually(t, timeout, "ManagementLock Ready as ReadOnly", ready(l))
	expectLock(t, lockID, arm.LockProperties{Level: "ReadOnly", Notes: "frozen"})
	expectARM(t, ns, http.MethodPut, accountID, http.StatusConflict, "ScopeLocked")

	// Deleting the lock removes it from Resource Manager; deletes pass again.
	deleteLock(t, l)
	if _, ok := armFake.Lock(lockID); ok {
		t.Fatalf("lock %s still in Resource Manager", lockID)
	}
	expectARM(t, ns, http.MethodDelete, accountID, http.StatusOK, "")
	expectARM(t, ns, http.MethodDelete, rgID, http.StatusOK, "")
}

// Locks by ARM ID: on the subscription (everything in it is protected) and
// on one resource; a scope in another subscription than the credential's
// is refused, as ASO refuses it.
func TestManagementLockByARMID(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "lock-b")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	subscription := "/subscriptions/" + subID
	vaultID := subscription + "/resourceGroups/lock-b-rg/providers/Microsoft.KeyVault/vaults/lockbvault"
	armFake.AddResource(subscription + "/resourceGroups/lock-b-rg")
	armFake.AddResource(vaultID)

	onResource := &authv1.ManagementLock{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "keep-vault"},
		Spec: authv1.ManagementLockSpec{
			AzureName: "keep the vault",
			Owner:     &genruntime.ArbitraryOwnerReference{ARMID: vaultID},
			Level:     "CanNotDelete",
		},
	}
	if err := k8s.Create(ctx, onResource); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "lock on a resource Ready", ready(onResource))
	expectLock(t, vaultID+"/providers/Microsoft.Authorization/locks/keep the vault", arm.LockProperties{Level: "CanNotDelete"})
	expectARM(t, ns, http.MethodDelete, vaultID, http.StatusConflict, "ScopeLocked")
	deleteLock(t, onResource)
	expectARM(t, ns, http.MethodDelete, vaultID, http.StatusOK, "")

	onSubscription := &authv1.ManagementLock{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "keep-subscription"},
		Spec: authv1.ManagementLockSpec{
			Owner: &genruntime.ArbitraryOwnerReference{ARMID: subscription},
			Level: "CanNotDelete",
		},
	}
	if err := k8s.Create(ctx, onSubscription); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "lock on the subscription Ready", ready(onSubscription))
	expectARM(t, ns, http.MethodDelete, subscription+"/resourceGroups/lock-b-rg", http.StatusConflict, "ScopeLocked")
	deleteLock(t, onSubscription)
	expectARM(t, ns, http.MethodDelete, subscription+"/resourceGroups/lock-b-rg", http.StatusOK, "")

	elsewhere := &authv1.ManagementLock{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "elsewhere"},
		Spec: authv1.ManagementLockSpec{
			Owner: &genruntime.ArbitraryOwnerReference{ARMID: "/subscriptions/00000000-0000-0000-0000-0000000000f2/resourceGroups/other"},
			Level: "CanNotDelete",
		},
	}
	if err := k8s.Create(ctx, elsewhere); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "lock in another subscription refused", notReadyWith(elsewhere, "does not match with Client Credential"))
}

// An owner that does not exist yet holds the lock back (ASO's
// WaitingForOwner); an identity Resource Manager does not trust is
// refused; the CRD refuses an owner that is neither or both of an ARM ID
// and a named resource, and a level that is not a lock level.
func TestManagementLockRefused(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "lock-c")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	waiting := &authv1.ManagementLock{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "waiting"},
		Spec: authv1.ManagementLockSpec{
			Owner: &genruntime.ArbitraryOwnerReference{Group: "resources.azure.com", Kind: "ResourceGroup", Name: "not-yet"},
			Level: "CanNotDelete",
		},
	}
	if err := k8s.Create(ctx, waiting); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "lock waits for its owner", notReadyWith(waiting, `"not-yet" cannot be found`))

	other := namespace(t, "lock-d")
	strictCredential(t, other, clientB)
	untrusted := &authv1.ManagementLock{
		ObjectMeta: metav1.ObjectMeta{Namespace: other, Name: "untrusted"},
		Spec: authv1.ManagementLockSpec{
			Owner: &genruntime.ArbitraryOwnerReference{ARMID: "/subscriptions/" + subID},
			Level: "CanNotDelete",
		},
	}
	if err := k8s.Create(ctx, untrusted); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "untrusted identity refused", notReadyWith(untrusted, "401"))

	for _, c := range []struct {
		owner genruntime.ArbitraryOwnerReference
		level string
		want  string
	}{
		{genruntime.ArbitraryOwnerReference{}, "CanNotDelete", "owner names either an ARM ID"},
		{genruntime.ArbitraryOwnerReference{ARMID: "/subscriptions/" + subID, Name: "rg", Group: "resources.azure.com", Kind: "ResourceGroup"}, "CanNotDelete", "owner names either an ARM ID"},
		{genruntime.ArbitraryOwnerReference{Name: "rg"}, "CanNotDelete", "needs its group and kind"},
		{genruntime.ArbitraryOwnerReference{ARMID: "/subscriptions/" + subID}, "NoDelete", "Unsupported value"},
	} {
		l := &authv1.ManagementLock{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "invalid"},
			Spec:       authv1.ManagementLockSpec{Owner: &c.owner, Level: c.level},
		}
		if err := k8s.Create(ctx, l); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("owner %+v level %s: want a refusal with %q, got %v", c.owner, c.level, c.want, err)
		}
	}
}
