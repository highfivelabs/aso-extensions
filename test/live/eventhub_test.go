// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"reflect"
	"testing"

	asoeventhub "github.com/Azure/azure-service-operator/v2/api/eventhub/v20240101/storage"

	eventhub "github.com/highfivelabs/aso-extensions/apis/arm/eventhub/v20260101"
)

const eventHubsAPI = "2026-01-01"

// A Standard Event Hubs namespace's network rule set (an IP range, public
// access kept), a schema group and an application group throttling a SAS
// client (docs/plan.md f62): created, the throttle changed, deleted (the
// rule set is left, and goes with the namespace). Cheap: a Standard
// namespace of one throughput unit bills by the hour (cents for a run).
// Not here: private endpoint connections (a private endpoint), the
// disaster recovery alias (a second namespace in another region, and the
// pairing broken before the alias goes) and the dedicated cluster (billed
// by the hour, with a minimum). Not known before a run: whether a Standard
// namespace takes application groups.
func TestLiveEventHubsNamespaceChildren(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.EventHub")
	ns := namespace(t, "ehkids")
	name := azName("eh")
	nsID := rgID() + "/providers/Microsoft.EventHub/namespaces/" + name
	fixture(t, nsID, "2024-01-01", map[string]any{"location": location, "sku": map[string]any{"name": "Standard", "tier": "Standard", "capacity": 1}})
	hub := &asoeventhub.Namespace{}
	hub.Namespace, hub.Name = ns, "hub"
	hub.Spec.AzureName = name
	hub.Spec.Location = ptr(location)
	hub.Spec.Owner = owner("rg")
	standIn(t, hub, nsID, nil)

	rules := &eventhub.NamespacesNetworkRuleSet{}
	rules.Namespace, rules.Name = ns, "rules"
	rules.Spec = eventhub.NamespacesNetworkRuleSet_Spec{
		Owner: owner("hub"), DefaultAction: ptr(eventhub.DefaultAction_Deny),
		PublicNetworkAccess: ptr(eventhub.NetworkRuleSetProperties_PublicNetworkAccess_Enabled),
		IpRules:             []eventhub.NWRuleSetIpRules{{IpMask: ptr("203.0.113.0/24"), Action: ptr(eventhub.NetworkRuleIPAction_Allow)}},
	}
	schemas := &eventhub.NamespacesSchemaGroup{}
	schemas.Namespace, schemas.Name = ns, "contracts"
	schemas.Spec = eventhub.NamespacesSchemaGroup_Spec{
		AzureName: "contracts", Owner: owner("hub"),
		SchemaType: ptr(eventhub.SchemaType_Avro), SchemaCompatibility: ptr(eventhub.SchemaCompatibility_Backward),
	}
	throttle := func(threshold int) []eventhub.ApplicationGroupPolicy {
		return []eventhub.ApplicationGroupPolicy{{ThrottlingPolicy: &eventhub.ThrottlingPolicy{
			Name: ptr("incoming"), Type: ptr(eventhub.ApplicationGroupPolicyType_ThrottlingPolicy),
			MetricId: ptr(eventhub.MetricId_IncomingMessages), RateLimitThreshold: ptr(threshold),
		}}}
	}
	group := &eventhub.NamespacesApplicationGroup{}
	group.Namespace, group.Name = ns, "ingest"
	group.Spec = eventhub.NamespacesApplicationGroup_Spec{
		AzureName: "ingest", Owner: owner("hub"),
		ClientAppGroupIdentifier: ptr("SASKeyName=RootManageSharedAccessKey"), IsEnabled: ptr(true), Policies: throttle(100),
	}
	create(t, timeout, rules, schemas, group)
	expectResource(t, nsID+"/networkRuleSets/default", eventHubsAPI, func(r map[string]any) error {
		p := properties(r)
		ipRules, _ := p["ipRules"].([]any)
		if p["defaultAction"] != "Deny" || len(ipRules) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, nsID+"/schemagroups/contracts", eventHubsAPI, func(r map[string]any) error {
		if p := properties(r); p["schemaType"] != "Avro" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	groupID := nsID + "/applicationGroups/ingest"
	update(t, group, func(g *eventhub.NamespacesApplicationGroup) { g.Spec.Policies = throttle(500) })
	eventually(t, timeout, "the application group Ready after the change", ready(group))
	expectResource(t, groupID, eventHubsAPI, func(r map[string]any) error {
		policies, _ := properties(r)["policies"].([]any)
		if len(policies) != 1 || !reflect.DeepEqual(policies[0].(map[string]any)["rateLimitThreshold"], float64(500)) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, group)
	expectGone(t, groupID, eventHubsAPI)
	remove(t, timeout, schemas)
	expectGone(t, nsID+"/schemagroups/contracts", eventHubsAPI)
	remove(t, timeout, rules)
}
