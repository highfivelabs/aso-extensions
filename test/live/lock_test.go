// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

//go:build live

package live

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	authorizationv1 "github.com/highfivelabs/aso-extensions/apis/authorization/v1"
)

const lockAPI = "2020-05-01"

// A CanNotDelete lock on an IP group fixture (both free): made, its notes
// changed, deleted. The live identity is Contributor on the group, which
// excludes Microsoft.Authorization/locks/write, so the test skips, naming
// the action, until a role grants it (docs/live-runs.md, "Rights
// Contributor does not have"). The lock is on a resource, never on the group,
// and is removed before its fixture, so a failed run cannot block the sweep.
func TestLiveManagementLock(t *testing.T) {
	t.Parallel()
	requireAction(t, "Microsoft.Authorization/locks/write")
	ns := namespace(t, "lock")
	groupID := rgID() + "/providers/Microsoft.Network/ipGroups/" + azName("locked")
	fixture(t, groupID, "2024-05-01", map[string]any{"location": location, "properties": map[string]any{"ipAddresses": []string{"10.0.0.0/24"}}})
	lockID := groupID + "/providers/Microsoft.Authorization/locks/live"
	// Registered after the fixture's cleanup, so it runs first.
	t.Cleanup(func() {
		_, _, _ = azure.do(context.Background(), http.MethodDelete, lockID, lockAPI, nil)
	})

	lock := &authorizationv1.ManagementLock{}
	lock.Namespace, lock.Name = ns, "live"
	lock.Spec = authorizationv1.ManagementLockSpec{
		Owner: &genruntime.ArbitraryOwnerReference{ARMID: groupID},
		Level: "CanNotDelete", Notes: "live run " + runID,
	}
	create(t, timeout, lock)
	expectResource(t, lockID, lockAPI, func(r map[string]any) error {
		if p := properties(r); p["level"] != "CanNotDelete" || p["notes"] != "live run "+runID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, lock, func(l *authorizationv1.ManagementLock) { l.Spec.Notes = "changed" })
	eventually(t, timeout, "ManagementLock Ready after the change", ready(lock))
	expectResource(t, lockID, lockAPI, func(r map[string]any) error {
		if properties(r)["notes"] != "changed" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, lock)
	expectGone(t, lockID, lockAPI)
}
