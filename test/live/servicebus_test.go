// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"testing"

	asoservicebus "github.com/Azure/azure-service-operator/v2/api/servicebus/v20240101/storage"

	servicebus "github.com/highfivelabs/aso-extensions/apis/arm/servicebus/v20260101"
)

const serviceBusAPI = "2026-01-01"

// A Standard Service Bus namespace's network rule set (docs/plan.md f61):
// the settings a Standard namespace takes (default action Allow, public
// access, trusted services), then trusted services turned off; deleting the
// object leaves the rule set, which goes with the namespace. Cheap: a
// Standard namespace bills a base rate by the hour (cents for a run). Not
// here: IP and virtual network rules (which tier takes them is to be seen),
// private endpoint connections and geo-disaster recovery, which need Premium
// namespaces (about a dollar an hour each).
func TestLiveServiceBusNetworkRuleSet(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.ServiceBus")
	ns := namespace(t, "sbrules")
	name := azName("sb")
	nsID := rgID() + "/providers/Microsoft.ServiceBus/namespaces/" + name
	fixture(t, nsID, "2024-01-01", map[string]any{"location": location, "sku": map[string]any{"name": "Standard", "tier": "Standard"}})
	bus := &asoservicebus.Namespace{}
	bus.Namespace, bus.Name = ns, "bus"
	bus.Spec.AzureName = name
	bus.Spec.Location = ptr(location)
	bus.Spec.Owner = owner("rg")
	standIn(t, bus, nsID, nil)

	rules := &servicebus.NamespacesNetworkRuleSet{}
	rules.Namespace, rules.Name = ns, "rules"
	rules.Spec = servicebus.NamespacesNetworkRuleSet_Spec{
		Owner:                       owner("bus"),
		DefaultAction:               ptr(servicebus.DefaultAction_Allow),
		PublicNetworkAccess:         ptr(servicebus.NetworkRuleSetProperties_PublicNetworkAccess_Enabled),
		TrustedServiceAccessEnabled: ptr(true),
	}
	create(t, timeout, rules)
	id := nsID + "/networkRuleSets/default"
	expectResource(t, id, serviceBusAPI, func(r map[string]any) error {
		if p := properties(r); p["trustedServiceAccessEnabled"] != true || p["defaultAction"] != "Allow" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, rules, func(r *servicebus.NamespacesNetworkRuleSet) { r.Spec.TrustedServiceAccessEnabled = ptr(false) })
	eventually(t, timeout, "the rule set Ready after the change", ready(rules))
	expectResource(t, id, serviceBusAPI, func(r map[string]any) error {
		if p := properties(r); p["trustedServiceAccessEnabled"] != false {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, rules)
}
