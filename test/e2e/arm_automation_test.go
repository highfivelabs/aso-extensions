// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	automation "github.com/highfivelabs/aso-extensions/apis/arm/automation/v20241023"
)

// An Automation account exports its DSC server endpoint, which Azure returns
// from the account's agent registration information and not from its GET,
// to the ConfigMap operatorSpec.configMaps.dscServerEndpoint names (a
// hand-written KubernetesConfigExporter, generator/_extensions/automation),
// beside the status's hybrid service URL; the ConfigMap is owned by the
// account.
func TestAutomationAccountDscServerEndpoint(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "automation")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")

	account := &automation.AutomationAccount{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "aa"},
		Spec: automation.AutomationAccount_Spec{
			AzureName: ns + "-aa",
			Owner:     &genruntime.KnownResourceReference{Name: "rg"},
			Location:  ptr("westus"),
			Sku:       &automation.Sku{Name: ptr(automation.SkuNameEnum_Basic)},
			OperatorSpec: &automation.AutomationAccountOperatorSpec{
				ConfigMaps: &automation.AutomationAccountOperatorConfigMaps{
					DscServerEndpoint: &genruntime.ConfigMapDestination{Name: "aa-dsc", Key: "endpoint"},
				},
			},
		},
	}
	if err := k8s.Create(ctx, account); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "AutomationAccount Ready", ready(account))
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(account), account); err != nil {
		t.Fatal(err)
	}
	hybrid := ""
	if account.Status.AutomationHybridServiceUrl != nil {
		hybrid = *account.Status.AutomationHybridServiceUrl
	}
	guid := strings.TrimPrefix(strings.SplitN(hybrid, ".", 2)[0], "https://")
	if guid == "" || !strings.Contains(hybrid, ".jrds.westus.azure-automation.net/") {
		t.Fatalf("status.automationHybridServiceUrl %q", hybrid)
	}

	want := "https://" + guid + ".agentsvc.westus.azure-automation.net/accounts/" + guid
	cm := &corev1.ConfigMap{}
	eventually(t, timeout, "the DSC server endpoint in its ConfigMap", func() error {
		if err := k8s.Get(ctx, client.ObjectKey{Namespace: ns, Name: "aa-dsc"}, cm); err != nil {
			return err
		}
		if cm.Data["endpoint"] != want {
			return fmt.Errorf("endpoint %q, want %q", cm.Data["endpoint"], want)
		}
		return nil
	})
	if refs := cm.GetOwnerReferences(); len(refs) != 1 || refs[0].Kind != "AutomationAccount" || refs[0].Name != "aa" {
		t.Fatalf("the ConfigMap's owners: %v", refs)
	}

	if err := k8s.Delete(ctx, account); err != nil {
		t.Fatal(err)
	}
	eventually(t, 2*timeout, "AutomationAccount gone", gone(account))
}
