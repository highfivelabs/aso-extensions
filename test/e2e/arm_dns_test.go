// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	asodns "github.com/Azure/azure-service-operator/v2/api/network/v1api20180501/storage"
	asoprivatedns "github.com/Azure/azure-service-operator/v2/api/network/v1api20240601/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"

	dns "github.com/highfivelabs/aso-extensions/apis/arm/network/v20180501"
	privatedns "github.com/highfivelabs/aso-extensions/apis/arm/network/v20240601"
	resolver "github.com/highfivelabs/aso-extensions/apis/arm/network/v20250501"
)

// dnsZone stands in for an ASO network.azure.com DnsZone ASO has
// provisioned, in the resource group rg; Azure makes its SOA record set,
// which the fake is given too.
func dnsZone(t *testing.T, ns, name, rg, rgAzureName, azureName string) string {
	t.Helper()
	ctx := context.Background()
	id := "/subscriptions/" + subID + "/resourceGroups/" + rgAzureName + "/providers/Microsoft.Network/dnsZones/" + azureName
	armFake.AddResource(id)
	armFake.AddResource(id + "/SOA/@")
	z := &asodns.DnsZone{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name,
		Annotations: map[string]string{genruntime.ResourceIDAnnotation: id}}}
	z.Spec.AzureName = azureName
	z.Spec.Location = ptr("global")
	z.Spec.Owner = &genruntime.KnownResourceReference{Name: rg}
	if err := k8s.Create(ctx, z); err != nil {
		t.Fatal(err)
	}
	z.Status.Conditions = []conditions.Condition{asoReady(z.Generation)}
	if err := k8s.Status().Update(ctx, z); err != nil {
		t.Fatal(err)
	}
	return id
}

// privateDnsZone stands in for an ASO network.azure.com PrivateDnsZone, as
// dnsZone does for a public one.
func privateDnsZone(t *testing.T, ns, name, rg, rgAzureName, azureName string) string {
	t.Helper()
	ctx := context.Background()
	id := "/subscriptions/" + subID + "/resourceGroups/" + rgAzureName + "/providers/Microsoft.Network/privateDnsZones/" + azureName
	armFake.AddResource(id)
	armFake.AddResource(id + "/SOA/@")
	z := &asoprivatedns.PrivateDnsZone{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name,
		Annotations: map[string]string{genruntime.ResourceIDAnnotation: id}}}
	z.Spec.AzureName = azureName
	z.Spec.Location = ptr("global")
	z.Spec.Owner = &genruntime.KnownResourceReference{Name: rg}
	if err := k8s.Create(ctx, z); err != nil {
		t.Fatal(err)
	}
	z.Status.Conditions = []conditions.Condition{asoReady(z.Generation)}
	if err := k8s.Status().Update(ctx, z); err != nil {
		t.Fatal(err)
	}
	return id
}

// A public and a private zone's SOA record sets, owned by ASO's DnsZone and
// PrivateDnsZone: set, changed, and on delete left in Azure (Azure deletes
// a SOA record set only with its zone), so the Kubernetes object goes and
// the record set stays. A SOA record set not named @ is refused by
// Resource Manager, the refusal on the Ready condition.
func TestDNSZoneSOARecords(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "soa")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	zoneID := dnsZone(t, ns, "public", "rg", ns+"-rg", "example.com")
	privateID := privateDnsZone(t, ns, "private", "rg", ns+"-rg", "internal.example")

	soa := &dns.DnsZonesSOARecord{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "public-soa"},
		Spec: dns.DnsZonesSOARecord_Spec{
			Owner:     &genruntime.KnownResourceReference{Name: "public"},
			AzureName: "@",
			TTL:       ptr(3600),
			SOARecord: &dns.SoaRecord{Email: ptr("hostmaster.example.com"), RefreshTime: ptr(3600), RetryTime: ptr(300), ExpireTime: ptr(2419200), MinimumTTL: ptr(300)},
		},
	}
	psoa := &privatedns.PrivateDnsZonesSOARecord{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "private-soa"},
		Spec: privatedns.PrivateDnsZonesSOARecord_Spec{
			Owner:     &genruntime.KnownResourceReference{Name: "private"},
			AzureName: "@",
			Ttl:       ptr(3600),
			SoaRecord: &privatedns.SoaRecord{Email: ptr("hostmaster.internal.example"), MinimumTtl: ptr(10)},
		},
	}
	misnamed := &dns.DnsZonesSOARecord{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "misnamed"},
		Spec: dns.DnsZonesSOARecord_Spec{
			Owner:     &genruntime.KnownResourceReference{Name: "public"},
			SOARecord: &dns.SoaRecord{Email: ptr("hostmaster.example.com")},
		},
	}
	for _, o := range []client.Object{soa, psoa, misnamed} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "DnsZonesSOARecord Ready", ready(soa))
	eventually(t, timeout, "PrivateDnsZonesSOARecord Ready", ready(psoa))
	eventually(t, timeout, "the SOA not named @ on the Ready condition", notReadyWith(misnamed, "is named @"))
	soaID, psoaID := zoneID+"/SOA/@", privateID+"/SOA/@"
	expectResource(t, soaID, func(r map[string]any) error {
		rec, _ := properties(r)["SOARecord"].(map[string]any)
		if properties(r)["TTL"] != float64(3600) || rec["email"] != "hostmaster.example.com" || rec["minimumTTL"] != float64(300) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, psoaID, func(r map[string]any) error {
		rec, _ := properties(r)["soaRecord"].(map[string]any)
		if rec["email"] != "hostmaster.internal.example" || rec["minimumTtl"] != float64(10) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	if err := k8s.Get(ctx, client.ObjectKeyFromObject(soa), soa); err != nil {
		t.Fatal(err)
	}
	soa.Spec.SOARecord.RefreshTime = ptr(7200)
	if err := k8s.Update(ctx, soa); err != nil {
		t.Fatal(err)
	}
	expectResource(t, soaID, func(r map[string]any) error {
		rec, _ := properties(r)["SOARecord"].(map[string]any)
		if rec["refreshTime"] != float64(7200) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range []client.Object{misnamed, soa, psoa} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T %s gone", o, o.GetName()), gone(o))
	}
	for _, id := range []string{soaID, psoaID} {
		if !armFake.HasResource(id) {
			t.Fatalf("%s was deleted with its Kubernetes object; Azure keeps it until the zone goes", id)
		}
	}
}

// A DNS security policy owned by ASO's ResourceGroup, a domain list, a rule
// blocking the list (by object), and a link to a virtual network (by ARM
// ID). Refused by Resource Manager, on the Ready condition: a rule with a
// priority under 100, and a second policy's link to the same virtual
// network. Refused by the CRD: an action that does not exist. Deleted,
// children first.
func TestDNSSecurityPolicy(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "dnspolicy")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	nw := rgID + "/providers/Microsoft.Network/"
	vnetID := nw + "virtualNetworks/spoke"
	armFake.AddResource(vnetID)

	policy := &resolver.DnsResolverPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "egress"},
		Spec: resolver.DnsResolverPolicy_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
		},
	}
	other := &resolver.DnsResolverPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "other"},
		Spec: resolver.DnsResolverPolicy_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
		},
	}
	list := &resolver.DnsResolverDomainList{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "blocked"},
		Spec: resolver.DnsResolverDomainList_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
			Domains:  []string{"malware.example.", "phish.example."},
		},
	}
	listRef := resolver.OpenapiSubResource{Reference: &genruntime.ResourceReference{
		Group: "network.azure.hfl.dev", Kind: "DnsResolverDomainList", Name: "blocked"}}
	rule := &resolver.DnsResolverPoliciesDnsSecurityRule{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "block"},
		Spec: resolver.DnsResolverPoliciesDnsSecurityRule_Spec{
			Owner:                  &genruntime.KnownResourceReference{Name: "egress"},
			Location:               ptr("westus"),
			Priority:               ptr(100),
			Action:                 &resolver.DnsSecurityRuleAction{ActionType: ptr(resolver.OpenapiActionType_Block)},
			DnsResolverDomainLists: []resolver.OpenapiSubResource{listRef},
			DnsSecurityRuleState:   ptr(resolver.DnsSecurityRuleState_Enabled),
		},
	}
	low := rule.DeepCopy()
	low.ObjectMeta = metav1.ObjectMeta{Namespace: ns, Name: "low"}
	low.Spec.Priority = ptr(50)
	link := &resolver.DnsResolverPoliciesVirtualNetworkLink{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "spoke"},
		Spec: resolver.DnsResolverPoliciesVirtualNetworkLink_Spec{
			Owner:          &genruntime.KnownResourceReference{Name: "egress"},
			Location:       ptr("westus"),
			VirtualNetwork: &resolver.OpenapiSubResource{Reference: &genruntime.ResourceReference{ARMID: vnetID}},
		},
	}
	for _, o := range []client.Object{policy, list, rule, low, link} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "DnsResolverPolicy Ready", ready(policy))
	eventually(t, timeout, "DnsResolverDomainList Ready", ready(list))
	eventually(t, timeout, "DnsResolverPoliciesDnsSecurityRule Ready", ready(rule))
	eventually(t, timeout, "DnsResolverPoliciesVirtualNetworkLink Ready", ready(link))
	eventually(t, timeout, "the low priority on the Ready condition", notReadyWith(low, "from 100 to 65000"))
	policyID := nw + "dnsResolverPolicies/egress"
	listID := nw + "dnsResolverDomainLists/blocked"
	ruleID := policyID + "/dnsSecurityRules/block"
	linkID := policyID + "/virtualNetworkLinks/spoke"
	expectResource(t, ruleID, func(r map[string]any) error {
		p := properties(r)
		action, _ := p["action"].(map[string]any)
		lists, _ := p["dnsResolverDomainLists"].([]any)
		first, _ := lists[0].(map[string]any)
		if p["priority"] != float64(100) || action["actionType"] != "Block" || len(lists) != 1 || first["id"] != listID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, linkID, func(r map[string]any) error {
		if refOf(properties(r), "virtualNetwork") != vnetID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	otherLink := link.DeepCopy()
	otherLink.ObjectMeta = metav1.ObjectMeta{Namespace: ns, Name: "spoke-again"}
	otherLink.Spec.Owner = &genruntime.KnownResourceReference{Name: "other"}
	for _, o := range []client.Object{other, otherLink} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "the second link to the network on the Ready condition", notReadyWith(otherLink, "is linked to another DNS security policy"))

	badAction := rule.DeepCopy()
	badAction.ObjectMeta = metav1.ObjectMeta{Namespace: ns, Name: "drop"}
	badAction.Spec.Action = &resolver.DnsSecurityRuleAction{ActionType: ptr(resolver.OpenapiActionType("Drop"))}
	if err := k8s.Create(ctx, badAction); err == nil || !contains(err.Error(), "Unsupported value") {
		t.Fatalf("want a refusal of the action, got %v", err)
	}

	for _, o := range []client.Object{otherLink, other, link, low, rule, list, policy} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T %s gone", o, o.GetName()), gone(o))
	}
	for _, id := range []string{linkID, ruleID, listID, policyID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
}

func refOf(props map[string]any, name string) string {
	ref, _ := props[name].(map[string]any)
	id, _ := ref["id"].(string)
	return id
}
