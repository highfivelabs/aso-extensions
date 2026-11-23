// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	compute "github.com/highfivelabs/aso-extensions/apis/arm/compute/v20260401"
)

// A diagnostic run command (docs/plan.md f55) on ASO's VirtualMachine
// stand-in: a gallery script version by ARM ID, with a parameter and the
// password of the account it runs as from a Secret (never in the status).
// A change is PUT again; one with two sources is refused (the fake's rule,
// diagnosticruncommands.go); deleted, it goes.
func TestVirtualMachineDiagnosticRunCommand(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "vmdiag")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	vmID, rgID := vmFootprint(t, ns)
	scriptID := rgID + "/providers/Microsoft.Compute/galleries/ops/scripts/collect-logs/versions/1.0.0"
	armFake.AddResource(scriptID)
	secret(t, ns, "runas", map[string]string{"password": "diag-password-1"})
	onVM := &genruntime.KnownResourceReference{Name: "vm"}

	diag := &compute.VirtualMachinesDiagnosticRunCommand{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "collect-logs"},
		Spec: compute.VirtualMachinesDiagnosticRunCommand_Spec{
			Owner: onVM, Location: ptr("westus"),
			Properties: &compute.VirtualMachineRunCommandProperties{
				Source:                  &compute.VirtualMachineRunCommandScriptSource{GalleryScriptReferenceReference: &genruntime.ResourceReference{ARMID: scriptID}},
				Parameters:              []compute.RunCommandInputParameter{{Name: ptr("since"), Value: ptr("1h")}},
				RunAsUser:               ptr("diag"),
				RunAsPasswordFromSecret: &genruntime.SecretReference{Name: "runas", Key: "password"},
				TimeoutInSeconds:        ptr(600),
			},
		},
	}
	if err := k8s.Create(ctx, diag); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "diagnostic run command Ready", ready(diag))
	diagID := vmID + "/diagnosticRunCommands/collect-logs"
	expectResource(t, diagID, func(r map[string]any) error {
		p := properties(r)
		src, _ := p["source"].(map[string]any)
		if src["galleryScriptReferenceId"] != scriptID || p["runAsPassword"] != "diag-password-1" || p["timeoutInSeconds"] != float64(600) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(diag), diag); err != nil {
		t.Fatal(err)
	}
	if raw := fmt.Sprintf("%+v", diag.Status); contains(raw, "diag-password-1") {
		t.Fatalf("the password is in the status: %s", raw)
	}

	// A change is PUT again.
	if err := change(t, diag, func(d *compute.VirtualMachinesDiagnosticRunCommand) { d.Spec.Properties.TimeoutInSeconds = ptr(900) }); err != nil {
		t.Fatal(err)
	}
	expectResource(t, diagID, func(r map[string]any) error {
		if properties(r)["timeoutInSeconds"] != float64(900) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// Two sources are refused.
	both := &compute.VirtualMachinesDiagnosticRunCommand{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "both"},
		Spec: compute.VirtualMachinesDiagnosticRunCommand_Spec{
			Owner: onVM, Location: ptr("westus"),
			Properties: &compute.VirtualMachineRunCommandProperties{
				Source: &compute.VirtualMachineRunCommandScriptSource{Script: ptr("uptime"), CommandId: ptr("RunShellScript")},
			},
		},
	}
	if err := k8s.Create(ctx, both); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "two sources refused", notReadyWith(both, "source is one of"))

	for _, o := range []client.Object{both, diag} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	if armFake.HasResource(diagID) {
		t.Fatal("the diagnostic run command is still in the fake")
	}
}
