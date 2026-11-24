// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	asoeventhub "github.com/Azure/azure-service-operator/v2/api/eventhub/v20240101/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	eventhub "github.com/highfivelabs/aso-extensions/apis/arm/eventhub/v20260101"
)

// The Event Hubs kinds of docs/plan.md f62: on ASO's Namespace by name (a new
// external owner), the network rule set, an application group throttling a
// SAS client's incoming messages, a schema group, a geo-disaster recovery
// alias naming ASO's other Namespace by its object and an approved private
// endpoint connection; and a dedicated cluster in the resource group. An
// application group whose client identifier is of neither form and an alias
// naming a Service Bus namespace are refused (the fake's rules,
// messaging.go); a change to the application group is PUT again; deleted,
// all go but the rule set, which Azure cannot delete and the kind leaves.
func TestEventHubsKinds(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "eventhub")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	telemetryID := rgID + "/providers/Microsoft.EventHub/namespaces/telemetry"
	westID := rgID + "/providers/Microsoft.EventHub/namespaces/telemetry-west"
	busID := rgID + "/providers/Microsoft.ServiceBus/namespaces/orders"
	connID := telemetryID + "/privateEndpointConnections/pe-telemetry"
	for _, id := range []string{rgID, telemetryID, westID, busID, connID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	for name, id := range map[string]string{"telemetry": telemetryID, "telemetry-west": westID} {
		owner := &asoeventhub.Namespace{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
		owner.Spec.AzureName, owner.Spec.Location = name, ptr("westus")
		owner.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
		asoStandIn(t, owner, id)
	}
	telemetry := &genruntime.KnownResourceReference{Name: "telemetry"}

	rules := &eventhub.NamespacesNetworkRuleSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "telemetry-network"},
		Spec: eventhub.NamespacesNetworkRuleSet_Spec{
			Owner: telemetry, DefaultAction: ptr(eventhub.DefaultAction_Deny),
			IpRules: []eventhub.NWRuleSetIpRules{{IpMask: ptr("203.0.113.0/24"), Action: ptr(eventhub.NetworkRuleIPAction_Allow)}},
		},
	}
	throttle := func(threshold int) []eventhub.ApplicationGroupPolicy {
		return []eventhub.ApplicationGroupPolicy{{ThrottlingPolicy: &eventhub.ThrottlingPolicy{
			Name: ptr("incoming"), Type: ptr(eventhub.ApplicationGroupPolicyType_ThrottlingPolicy),
			MetricId: ptr(eventhub.MetricId_IncomingMessages), RateLimitThreshold: ptr(threshold),
		}}}
	}
	group := &eventhub.NamespacesApplicationGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "ingest"},
		Spec: eventhub.NamespacesApplicationGroup_Spec{
			Owner: telemetry, ClientAppGroupIdentifier: ptr("SASKeyName=ingest"), IsEnabled: ptr(true), Policies: throttle(100),
		},
	}
	nameless := &eventhub.NamespacesApplicationGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "nameless"},
		Spec:       eventhub.NamespacesApplicationGroup_Spec{Owner: telemetry, ClientAppGroupIdentifier: ptr("ingest")},
	}
	schemas := &eventhub.NamespacesSchemaGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "contracts"},
		Spec: eventhub.NamespacesSchemaGroup_Spec{
			Owner: telemetry, SchemaType: ptr(eventhub.SchemaType_Avro), SchemaCompatibility: ptr(eventhub.SchemaCompatibility_Backward),
		},
	}
	alias := &eventhub.NamespacesDisasterRecoveryConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "telemetry-dr"},
		Spec: eventhub.NamespacesDisasterRecoveryConfig_Spec{
			Owner: telemetry,
			PartnerNamespaceReference: &genruntime.ResourceReference{
				Group: "eventhub.azure.com", Kind: "Namespace", Name: "telemetry-west",
			},
		},
	}
	crossed := &eventhub.NamespacesDisasterRecoveryConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "crossed-dr"},
		Spec: eventhub.NamespacesDisasterRecoveryConfig_Spec{
			Owner:                     &genruntime.KnownResourceReference{Name: "telemetry-west"},
			PartnerNamespaceReference: &genruntime.ResourceReference{ARMID: busID},
		},
	}
	conn := &eventhub.NamespacesPrivateEndpointConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "telemetry-pe"},
		Spec: eventhub.NamespacesPrivateEndpointConnection_Spec{
			AzureName: "pe-telemetry", Owner: telemetry,
			PrivateLinkServiceConnectionState: &eventhub.ConnectionState{Status: ptr(eventhub.PrivateLinkConnectionStatus_Approved)},
		},
	}
	cluster := &eventhub.Cluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "dedicated"},
		Spec: eventhub.Cluster_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "rg"}, Location: ptr("westus"),
			Sku: &eventhub.ClusterSku{Name: ptr(eventhub.ClusterSkuName_Dedicated), Capacity: ptr(1)},
		},
	}
	objs := []conditioned{rules, group, nameless, schemas, alias, crossed, conn, cluster}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []conditioned{rules, group, schemas, alias, conn, cluster} {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	eventually(t, timeout, "a client identifier of neither form refused", notReadyWith(nameless, "SASKeyName="))
	eventually(t, timeout, "a Service Bus partner refused", notReadyWith(crossed, "same service"))

	groupID := telemetryID + "/applicationGroups/ingest"
	expectResource(t, groupID, func(r map[string]any) error {
		p := properties(r)
		policies, _ := p["policies"].([]any)
		if p["clientAppGroupIdentifier"] != "SASKeyName=ingest" || len(policies) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		if pol, _ := policies[0].(map[string]any); pol["type"] != "ThrottlingPolicy" || pol["rateLimitThreshold"] != float64(100) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, telemetryID+"/schemagroups/contracts", func(r map[string]any) error {
		if p := properties(r); p["schemaType"] != "Avro" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, telemetryID+"/disasterRecoveryConfigs/telemetry-dr", func(r map[string]any) error {
		if p := properties(r); p["partnerNamespace"] != westID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again: a higher threshold.
	if err := change(t, group, func(group *eventhub.NamespacesApplicationGroup) {
		group.Spec.Policies = throttle(500)
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, groupID, func(r map[string]any) error {
		policies, _ := properties(r)["policies"].([]any)
		if len(policies) != 1 || policies[0].(map[string]any)["rateLimitThreshold"] != float64(500) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range objs {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, id := range []string{groupID, telemetryID + "/schemagroups/contracts", telemetryID + "/disasterRecoveryConfigs/telemetry-dr", connID, rgID + "/providers/Microsoft.EventHub/clusters/dedicated"} {
		if armFake.HasResource(id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
	if !armFake.HasResource(telemetryID + "/networkRuleSets/default") {
		t.Errorf("the rule set was deleted")
	}
}
