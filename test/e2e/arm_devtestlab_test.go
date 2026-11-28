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

	dtl "github.com/highfivelabs/aso-extensions/apis/arm/devtestlab/v20180915"
)

// DevTest Labs (docs/plan.md f51): a lab in a resource group, its virtual
// network on a virtual network by ARM ID with one lab subnet, its nightly
// shutdown and a notification channel, a private artifact source (its token
// from a Secret), its cost target (no DELETE: detached), a user and the
// user's secret (from a Secret), and a lab VM on the lab network by object
// (its password from a Secret) with its own shutdown. No token, secret or
// password is in a status. A lab schedule with another name and a VM on a
// lab subnet the network does not have are refused (the fake's rules,
// devtestlab.go); a change is PUT again; deleted, all go but the cost
// target, which stays until the lab goes.
func TestDevTestLabs(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "dtl")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	vnetID := rgID + "/providers/Microsoft.Network/virtualNetworks/labs"
	for _, id := range []string{rgID, vnetID, vnetID + "/subnets/default"} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	secret(t, ns, "lab", map[string]string{
		"token": "ghp_conformanceToken0123456789", "password": "Lab-Passw0rd-1", "secret": "s3cret-value",
	})
	onRG := &genruntime.KnownResourceReference{Name: "rg"}
	onLab := &genruntime.KnownResourceReference{Name: "lab"}
	labID := rgID + "/providers/Microsoft.DevTestLab/labs/lab"

	lab := &dtl.Lab{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "lab"},
		Spec: dtl.Lab_Spec{
			Owner: onRG, Location: ptr("westus"), LabStorageType: ptr(dtl.LabProperties_LabStorageType_Premium),
			Announcement: &dtl.LabAnnouncementProperties{Enabled: ptr(dtl.EnableStatus_Enabled), Title: ptr("Maintenance"), Markdown: ptr("Saturday")},
		},
	}
	network := &dtl.LabsVirtualNetwork{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "net"},
		Spec: dtl.LabsVirtualNetwork_Spec{
			Owner: onLab, ExternalProviderResourceReference: &genruntime.ResourceReference{ARMID: vnetID},
			SubnetOverrides: []dtl.SubnetOverride{{
				ResourceReference: &genruntime.ResourceReference{ARMID: vnetID + "/subnets/default"}, LabSubnetName: ptr("default"),
				UseInVmCreationPermission: ptr(dtl.UsagePermissionType_Allow), UsePublicIpAddressPermission: ptr(dtl.UsagePermissionType_Deny),
			}},
		},
	}
	shutdown := &dtl.LabsSchedule{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "shutdown"},
		Spec: dtl.LabsSchedule_Spec{
			AzureName: "LabVmsShutdown", Owner: onLab, TaskType: ptr("LabVmsShutdownTask"),
			Status: ptr(dtl.ScheduleProperties_Status_Enabled), TimeZoneId: ptr("UTC"), DailyRecurrence: &dtl.DayDetails{Time: ptr("1900")},
		},
	}
	channel := &dtl.LabsNotificationChannel{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "ops"},
		Spec: dtl.LabsNotificationChannel_Spec{
			Owner: onLab, EmailRecipient: ptr("ops@example.com"),
			Events: []dtl.Event{{EventName: ptr(dtl.NotificationChannelEventType_AutoShutdown)}},
		},
	}
	artifacts := &dtl.LabsArtifactSource{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "team-artifacts"},
		Spec: dtl.LabsArtifactSource_Spec{
			Owner: onLab, Uri: ptr("https://github.com/contoso/lab-artifacts.git"), SourceType: ptr(dtl.SourceControlType_GitHub),
			FolderPath: ptr("/Artifacts"), BranchRef: ptr("main"), Status: ptr(dtl.EnableStatus_Enabled),
			SecurityToken: &genruntime.SecretReference{Name: "lab", Key: "token"},
		},
	}
	cost := &dtl.LabsCost{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cost", Annotations: map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}},
		Spec: dtl.LabsCost_Spec{
			AzureName: "targetCost", Owner: onLab, CurrencyCode: ptr("USD"),
			TargetCost: &dtl.TargetCostProperties{Status: ptr(dtl.TargetCostStatus_Enabled), Target: ptr(500), CycleType: ptr(dtl.ReportingCycleType_CalendarMonth)},
		},
	}
	user := &dtl.LabsUser{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "dev"},
		Spec:       dtl.LabsUser_Spec{AzureName: "00000000-0000-0000-0000-0000000000d1", Owner: onLab},
	}
	userSecret := &dtl.LabsUsersSecret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "db-password"},
		Spec: dtl.LabsUsersSecret_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "dev"}, Value: &genruntime.SecretReference{Name: "lab", Key: "secret"},
		},
	}
	vm := &dtl.LabsVirtualMachine{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "devbox"},
		Spec: dtl.LabsVirtualMachine_Spec{
			Owner: onLab, Location: ptr("westus"), Size: ptr("Standard_D2s_v3"), UserName: ptr("labadmin"),
			Password: &genruntime.SecretReference{Name: "lab", Key: "password"}, AllowClaim: ptr(false),
			LabVirtualNetworkReference: &genruntime.ResourceReference{Group: "devtestlab.azure.hfl.dev", Kind: "LabsVirtualNetwork", Name: "net"},
			LabSubnetName:              ptr("default"), DisallowPublicIpAddress: ptr(true),
			GalleryImageReference: &dtl.GalleryImageReference{
				Offer: ptr("ubuntu-24_04-lts"), Publisher: ptr("Canonical"), Sku: ptr("server"), OsType: ptr("Linux"), Version: ptr("latest"),
			},
		},
	}
	vmShutdown := &dtl.LabsVirtualmachinesSchedule{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "devbox-shutdown"},
		Spec: dtl.LabsVirtualmachinesSchedule_Spec{
			AzureName: "LabVmsShutdown", Owner: &genruntime.KnownResourceReference{Name: "devbox"}, TaskType: ptr("LabVmsShutdownTask"),
			Status: ptr(dtl.ScheduleProperties_Status_Enabled), TimeZoneId: ptr("UTC"), DailyRecurrence: &dtl.DayDetails{Time: ptr("2000")},
		},
	}
	objs := []conditioned{lab, network, shutdown, channel, artifacts, cost, user, userSecret, vm, vmShutdown}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}

	expectResource(t, labID+"/virtualnetworks/net", func(r map[string]any) error {
		p := properties(r)
		overrides, _ := p["subnetOverrides"].([]any)
		if p["externalProviderResourceId"] != vnetID || len(overrides) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, labID+"/schedules/LabVmsShutdown", func(r map[string]any) error {
		d, _ := properties(r)["dailyRecurrence"].(map[string]any)
		if d["time"] != "1900" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, labID+"/artifactsources/team-artifacts", func(r map[string]any) error {
		if properties(r)["securityToken"] != "ghp_conformanceToken0123456789" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, labID+"/users/00000000-0000-0000-0000-0000000000d1/secrets/db-password", func(r map[string]any) error {
		if properties(r)["value"] != "s3cret-value" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, labID+"/virtualmachines/devbox", func(r map[string]any) error {
		p := properties(r)
		if p["password"] != "Lab-Passw0rd-1" || p["labVirtualNetworkId"] != labID+"/virtualnetworks/net" || p["labSubnetName"] != "default" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, labID+"/costs/targetCost", func(r map[string]any) error {
		tc, _ := properties(r)["targetCost"].(map[string]any)
		if tc["target"] != float64(500) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	for _, o := range []conditioned{artifacts, userSecret, vm} {
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(o), o); err != nil {
			t.Fatal(err)
		}
	}
	for what, status := range map[string]any{"token": artifacts.Status, "secret": userSecret.Status, "password": vm.Status} {
		raw := fmt.Sprintf("%+v", status)
		for _, s := range []string{"ghp_conformanceToken", "s3cret-value", "Lab-Passw0rd-1"} {
			if contains(raw, s) {
				t.Fatalf("a %s is in a status: %s", what, raw)
			}
		}
	}

	// A change is PUT again.
	if err := change(t, shutdown, func(s *dtl.LabsSchedule) { s.Spec.DailyRecurrence.Time = ptr("2100") }); err != nil {
		t.Fatal(err)
	}
	expectResource(t, labID+"/schedules/LabVmsShutdown", func(r map[string]any) error {
		d, _ := properties(r)["dailyRecurrence"].(map[string]any)
		if d["time"] != "2100" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A lab schedule with another name, and a VM on a lab subnet the network
	// does not have, are refused.
	nightly := &dtl.LabsSchedule{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "nightly"},
		Spec:       dtl.LabsSchedule_Spec{Owner: onLab, TaskType: ptr("LabVmsShutdownTask"), DailyRecurrence: &dtl.DayDetails{Time: ptr("2200")}},
	}
	stray := &dtl.LabsVirtualMachine{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "stray"},
		Spec: dtl.LabsVirtualMachine_Spec{
			Owner: onLab, Size: ptr("Standard_D2s_v3"), UserName: ptr("labadmin"),
			Password:                   &genruntime.SecretReference{Name: "lab", Key: "password"},
			LabVirtualNetworkReference: &genruntime.ResourceReference{Group: "devtestlab.azure.hfl.dev", Kind: "LabsVirtualNetwork", Name: "net"},
			LabSubnetName:              ptr("apps"),
		},
	}
	for _, o := range []client.Object{nightly, stray} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "a lab schedule named nightly refused", notReadyWith(nightly, "LabVmsShutdown"))
	eventually(t, timeout, "a VM on a missing lab subnet refused", notReadyWith(stray, "no lab subnet apps"))

	del := func(objs ...client.Object) {
		t.Helper()
		for _, o := range objs {
			if err := k8s.Delete(ctx, o); err != nil {
				t.Fatal(err)
			}
			eventually(t, timeout, o.GetName()+" gone", gone(o))
		}
	}
	del(nightly, stray, vmShutdown, vm, userSecret, user, cost, artifacts, channel, shutdown, network)
	for _, id := range []string{labID + "/virtualmachines/devbox", labID + "/artifactsources/team-artifacts", labID + "/virtualnetworks/net"} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in the fake", id)
		}
	}
	if !armFake.HasResource(labID + "/costs/targetCost") {
		t.Fatal("the cost target was deleted; it has no DELETE and was detached")
	}
	// The cost target goes with the lab.
	del(lab)
	if armFake.HasResource(labID) {
		t.Fatal("the lab is still in the fake")
	}
}
