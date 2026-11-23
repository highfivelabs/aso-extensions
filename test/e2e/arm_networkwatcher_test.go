// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	asoasg "github.com/Azure/azure-service-operator/v2/api/network/v1api20240101/storage"
	asowatcher "github.com/Azure/azure-service-operator/v2/api/network/v1api20241001/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"

	network "github.com/highfivelabs/aso-extensions/apis/arm/network/v20260301"
)

// Network Watcher connection monitors and application security group address
// prefix sets, owned by ASO's NetworkWatcher and ApplicationSecurityGroup by
// name (new external owners: the manager reads them through its cache, so
// the scheme, the owner index and the role must have them): a monitor's
// endpoint names a virtual machine by ARM ID and its output a workspace; a
// test group naming an endpoint the monitor lacks is refused on the Ready
// condition, and fixing it makes the monitor Ready; a prefix set is sent with
// its CIDRs; both are deleted with their objects.
func TestNetworkWatcherAndAddressPrefixSets(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "netwatch")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	watcherID := rgID + "/providers/Microsoft.Network/networkWatchers/nw"
	asgID := rgID + "/providers/Microsoft.Network/applicationSecurityGroups/web"
	vmID := rgID + "/providers/Microsoft.Compute/virtualMachines/probe"
	workspaceID := rgID + "/providers/Microsoft.OperationalInsights/workspaces/law"
	armFake.AddResource(watcherID)
	armFake.AddResource(asgID)
	armFake.AddResource(vmID)
	armFake.AddResource(workspaceID)

	// ASO's owners, as ASO leaves them once provisioned.
	watcher := &asowatcher.NetworkWatcher{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "nw",
		Annotations: map[string]string{genruntime.ResourceIDAnnotation: watcherID}}}
	watcher.Spec.AzureName = "nw"
	watcher.Spec.Location = ptr("westus")
	watcher.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	asg := &asoasg.ApplicationSecurityGroup{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "web",
		Annotations: map[string]string{genruntime.ResourceIDAnnotation: asgID}}}
	asg.Spec.AzureName = "web"
	asg.Spec.Location = ptr("westus")
	asg.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	for _, owner := range []client.Object{watcher, asg} {
		if err := k8s.Create(ctx, owner); err != nil {
			t.Fatal(err)
		}
	}
	watcher.Status.Conditions = []conditions.Condition{asoReady(watcher.Generation)}
	if err := k8s.Status().Update(ctx, watcher); err != nil {
		t.Fatal(err)
	}
	asg.Status.Conditions = []conditions.Condition{asoReady(asg.Generation)}
	if err := k8s.Status().Update(ctx, asg); err != nil {
		t.Fatal(err)
	}

	// A monitor whose test group names an endpoint it does not define.
	monitor := &network.NetworkWatchersConnectionMonitor{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "web-reachability"},
		Spec: network.NetworkWatchersConnectionMonitor_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "nw"},
			Location: ptr("westus"),
			Endpoints: []network.ConnectionMonitorEndpoint{
				{Name: ptr("probe"), ResourceReference: &genruntime.ResourceReference{ARMID: vmID}},
				{Name: ptr("web"), Address: ptr("www.example.com")},
			},
			TestConfigurations: []network.ConnectionMonitorTestConfiguration{{
				Name:             ptr("https"),
				Protocol:         ptr(network.ConnectionMonitorTestConfigurationProtocol_Tcp),
				TcpConfiguration: &network.ConnectionMonitorTcpConfiguration{Port: ptr(443)},
			}},
			TestGroups: []network.ConnectionMonitorTestGroup{{
				Name: ptr("g"), Sources: []string{"elsewhere"}, Destinations: []string{"web"}, TestConfigurations: []string{"https"},
			}},
			Outputs: []network.ConnectionMonitorOutput{{
				Type:              ptr(network.OutputType_Workspace),
				WorkspaceSettings: &network.ConnectionMonitorWorkspaceSettings{WorkspaceResourceReference: &genruntime.ResourceReference{ARMID: workspaceID}},
			}},
		},
	}
	if err := k8s.Create(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the refusal on the monitor's Ready condition", notReadyWith(monitor, "elsewhere"))
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(monitor), monitor); err != nil {
		t.Fatal(err)
	}
	monitor.Spec.TestGroups[0].Sources = []string{"probe"}
	if err := k8s.Update(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "monitor Ready", ready(monitor))
	monitorID := watcherID + "/connectionMonitors/web-reachability"
	expectResource(t, monitorID, func(r map[string]any) error {
		p := properties(r)
		endpoints, _ := p["endpoints"].([]any)
		outputs, _ := p["outputs"].([]any)
		if len(endpoints) != 2 || len(outputs) != 1 {
			return fmt.Errorf("properties: %v", p)
		}
		probe, _ := endpoints[0].(map[string]any)
		output, _ := outputs[0].(map[string]any)
		settings, _ := output["workspaceSettings"].(map[string]any)
		if probe["resourceId"] != vmID || settings["workspaceResourceId"] != workspaceID {
			return fmt.Errorf("properties: %v", p)
		}
		return nil
	})

	// A prefix set under the ASG.
	prefixes := &network.ApplicationSecurityGroupsAddressPrefixSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "partners"},
		Spec: network.ApplicationSecurityGroupsAddressPrefixSet_Spec{
			Owner:      &genruntime.KnownResourceReference{Name: "web"},
			Properties: &network.AddressPrefixSetPropertiesFormat{AddressPrefixes: []string{"203.0.113.0/24", "2001:db8::/32"}},
		},
	}
	if err := k8s.Create(ctx, prefixes); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "prefix set Ready", ready(prefixes))
	prefixesID := asgID + "/addressPrefixSets/partners"
	expectResource(t, prefixesID, func(r map[string]any) error {
		if got := fmt.Sprint(properties(r)["addressPrefixes"]); got != "[203.0.113.0/24 2001:db8::/32]" {
			return fmt.Errorf("addressPrefixes: %s", got)
		}
		return nil
	})

	for _, obj := range []client.Object{monitor, prefixes} {
		if err := k8s.Delete(ctx, obj); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, obj.GetName()+" gone", gone(obj))
	}
	for _, id := range []string{monitorID, prefixesID} {
		if armFake.HasResource(id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
}
