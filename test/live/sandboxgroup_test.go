// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"testing"

	app "github.com/highfivelabs/aso-extensions/apis/arm/app/v20260701"
)

const sandboxGroupAPI = "2026-07-01"

// A Container Apps sandbox group with no environment linked (no sandbox
// runs, so nothing is billed): made, its tags changed, and deleted
// (docs/plan.md f49). Its network connection (a delegated subnet) and Azure
// SRE Agent agents (billed per agent-hour) are not made here.
func TestLiveSandboxGroup(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.App")
	ns := namespace(t, "sandbox")

	group := &app.SandboxGroup{}
	group.Namespace, group.Name = ns, "ci"
	group.Spec = app.SandboxGroup_Spec{AzureName: azName("sandbox"), Owner: owner("rg"), Location: ptr(location)}
	create(t, timeout, group)
	groupID := rgID() + "/providers/Microsoft.App/sandboxGroups/" + group.Spec.AzureName
	expectResource(t, groupID, sandboxGroupAPI, func(r map[string]any) error { return nil })

	update(t, group, func(g *app.SandboxGroup) { g.Spec.Tags = map[string]string{"team": "platform"} })
	eventually(t, timeout, "the sandbox group Ready after the change", ready(group))
	expectResource(t, groupID, sandboxGroupAPI, func(r map[string]any) error {
		if tags, _ := r["tags"].(map[string]any); tags["team"] != "platform" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, group)
	expectGone(t, groupID, sandboxGroupAPI)
}
