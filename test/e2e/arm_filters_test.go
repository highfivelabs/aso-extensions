// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	network "github.com/highfivelabs/aso-extensions/apis/arm/network/v20260301"
)

// Foundation row 12, networking: an IP group owned by ASO's ResourceGroup;
// a route filter with its rule, the filter changed and PUT again keeping
// the rule (its extension, as a circuit keeps its peerings); a service
// endpoint policy with its definition, kept the same way. Refused by
// Resource Manager, the refusal on the Ready condition: an IP group address
// that is no IPv4 address, prefix or range, a second rule on a filter, a
// definition for another service than storage. Refused by the CRD: a rule
// whose access is no Access. Deleted, children first.
func TestNetworkFilters(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "filters")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	nw := rgID + "/providers/Microsoft.Network/"
	account := rgID + "/providers/Microsoft.Storage/storageAccounts/ours"

	ig := &network.IpGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "branches"},
		Spec: network.IpGroup_Spec{
			Owner:       &genruntime.KnownResourceReference{Name: "rg"},
			Location:    ptr("westus"),
			IpAddresses: []string{"10.10.0.0/16", "192.0.2.7", "10.20.0.1-10.20.0.9"},
		},
	}
	badIG := &network.IpGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "by-name"},
		Spec: network.IpGroup_Spec{
			Owner:       &genruntime.KnownResourceReference{Name: "rg"},
			Location:    ptr("westus"),
			IpAddresses: []string{"branch.example.com"},
		},
	}
	rf := &network.RouteFilter{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "m365"},
		Spec: network.RouteFilter_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
		},
	}
	rule := &network.RouteFiltersRouteFilterRule{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "exchange"},
		Spec: network.RouteFiltersRouteFilterRule_Spec{
			Owner:               &genruntime.KnownResourceReference{Name: "m365"},
			Location:            ptr("westus"),
			Access:              ptr(network.Access_Allow),
			RouteFilterRuleType: ptr(network.RouteFilterRuleType_Community),
			Communities:         []string{"12076:5010", "12076:5040"},
		},
	}
	sep := &network.ServiceEndpointPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "storage-only"},
		Spec: network.ServiceEndpointPolicy_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
		},
	}
	def := &network.ServiceEndpointPoliciesServiceEndpointPolicyDefinition{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "ours"},
		Spec: network.ServiceEndpointPoliciesServiceEndpointPolicyDefinition_Spec{
			Owner:            &genruntime.KnownResourceReference{Name: "storage-only"},
			Description:      ptr("only our storage account"),
			Service:          ptr("Microsoft.Storage"),
			ServiceResources: []string{account},
		},
	}
	for _, o := range []client.Object{ig, badIG, rf, rule, sep, def} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "IpGroup Ready", ready(ig))
	eventually(t, timeout, "the IP group's host name on the Ready condition", notReadyWith(badIG, "is not an IPv4 address, prefix or range"))
	igID := nw + "ipGroups/branches"
	expectResource(t, igID, func(r map[string]any) error {
		addrs, _ := properties(r)["ipAddresses"].([]any)
		if r["type"] != "Microsoft.Network/ipGroups" || len(addrs) != 3 || addrs[2] != "10.20.0.1-10.20.0.9" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	eventually(t, timeout, "RouteFilter Ready", ready(rf))
	eventually(t, timeout, "RouteFiltersRouteFilterRule Ready", ready(rule))
	rfID := nw + "routeFilters/m365"
	ruleID := rfID + "/routeFilterRules/exchange"
	expectResource(t, ruleID, func(r map[string]any) error {
		p := properties(r)
		c, _ := p["communities"].([]any)
		if p["access"] != "Allow" || p["routeFilterRuleType"] != "Community" || len(c) != 2 {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(rf), rf); err != nil {
		t.Fatal(err)
	}
	rf.Spec.Tags = map[string]string{"team": "network"}
	if err := k8s.Update(ctx, rf); err != nil {
		t.Fatal(err)
	}
	expectResource(t, rfID, func(r map[string]any) error {
		tags, _ := r["tags"].(map[string]any)
		if tags["team"] != "network" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if !armFake.HasResource(ruleID) {
		t.Fatal("the route filter's PUT removed its rule")
	}
	second := &network.RouteFiltersRouteFilterRule{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "second"},
		Spec: network.RouteFiltersRouteFilterRule_Spec{
			Owner:               &genruntime.KnownResourceReference{Name: "m365"},
			Location:            ptr("westus"),
			Access:              ptr(network.Access_Allow),
			RouteFilterRuleType: ptr(network.RouteFilterRuleType_Community),
			Communities:         []string{"12076:5020"},
		},
	}
	if err := k8s.Create(ctx, second); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the second rule on the Ready condition", notReadyWith(second, "at most one rule"))
	badAccess := rule.DeepCopy()
	badAccess.ObjectMeta = metav1.ObjectMeta{Namespace: ns, Name: "maybe"}
	badAccess.Spec.Access = ptr(network.Access("Maybe"))
	if err := k8s.Create(ctx, badAccess); err == nil || !contains(err.Error(), "Unsupported value") {
		t.Fatalf("want a refusal of the access, got %v", err)
	}

	eventually(t, timeout, "ServiceEndpointPolicy Ready", ready(sep))
	eventually(t, timeout, "ServiceEndpointPoliciesServiceEndpointPolicyDefinition Ready", ready(def))
	sepID := nw + "serviceEndpointPolicies/storage-only"
	defID := sepID + "/serviceEndpointPolicyDefinitions/ours"
	expectResource(t, defID, func(r map[string]any) error {
		p := properties(r)
		res, _ := p["serviceResources"].([]any)
		if p["service"] != "Microsoft.Storage" || len(res) != 1 || res[0] != account {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(sep), sep); err != nil {
		t.Fatal(err)
	}
	sep.Spec.Tags = map[string]string{"team": "network"}
	if err := k8s.Update(ctx, sep); err != nil {
		t.Fatal(err)
	}
	expectResource(t, sepID, func(r map[string]any) error {
		tags, _ := r["tags"].(map[string]any)
		if tags["team"] != "network" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if !armFake.HasResource(defID) {
		t.Fatal("the service endpoint policy's PUT removed its definition")
	}
	sql := &network.ServiceEndpointPoliciesServiceEndpointPolicyDefinition{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "sql"},
		Spec: network.ServiceEndpointPoliciesServiceEndpointPolicyDefinition_Spec{
			Owner:            &genruntime.KnownResourceReference{Name: "storage-only"},
			Service:          ptr("Microsoft.Sql"),
			ServiceResources: []string{rgID},
		},
	}
	if err := k8s.Create(ctx, sql); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the definition for another service on the Ready condition", notReadyWith(sql, "service is Microsoft.Storage"))

	for _, o := range []client.Object{sql, def, sep, second, rule, rf, badIG, ig} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T %s gone", o, o.GetName()), gone(o))
	}
	for _, id := range []string{defID, sepID, ruleID, rfID, igID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
}
