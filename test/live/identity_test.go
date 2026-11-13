// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

//go:build live

package live

import (
	"fmt"
	"testing"

	identity "github.com/highfivelabs/aso-extensions/apis/arm/managedidentity/v20241130"
)

const identityAPI = "2024-11-30"

// Our UserAssignedIdentity (2024-11-30, beside ASO's 2023-01-31) with
// isolationScope (free: an identity costs nothing): made Regional, changed
// to None, deleted. The fake takes any value; Azure decides which it allows.
func TestLiveUserAssignedIdentityIsolationScope(t *testing.T) {
	t.Parallel()
	ns := namespace(t, "uai")
	uai := &identity.UserAssignedIdentity{}
	uai.Namespace, uai.Name = ns, "uai"
	uai.Spec = identity.UserAssignedIdentity_Spec{
		AzureName: azName("uai"), Owner: owner("rg"), Location: ptr(location),
		IsolationScope: ptr(identity.IsolationScope("Regional")),
		Tags:           map[string]string{"hfl-live-run": runID},
	}
	create(t, timeout, uai)
	id := rgID() + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/" + uai.Spec.AzureName
	expectResource(t, id, identityAPI, func(r map[string]any) error {
		p := properties(r)
		if p["isolationScope"] != "Regional" || p["principalId"] == nil || p["clientId"] == nil {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if uai.Status.PrincipalId == nil || uai.Status.ClientId == nil {
		t.Fatalf("status lacks the principal and client IDs: %+v", uai.Status)
	}

	update(t, uai, func(u *identity.UserAssignedIdentity) { u.Spec.IsolationScope = ptr(identity.IsolationScope("None")) })
	eventually(t, timeout, "UserAssignedIdentity Ready after the change", ready(uai))
	expectResource(t, id, identityAPI, func(r map[string]any) error {
		if properties(r)["isolationScope"] != "None" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	remove(t, timeout, uai)
	expectGone(t, id, identityAPI)
}
