// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	eventgrid "github.com/highfivelabs/aso-extensions/apis/arm/eventgrid/v20250215"
)

// The Event Grid partner kinds of docs/plan.md f77, in a resource group:
// the partner's registration, a partner namespace naming it by object and a
// channel for a subscriber's partner topic; the subscriber's partner
// configuration authorizing the partner, the partner topic and a webhook
// subscription on it (its URL from a Secret). A partner namespace naming a missing registration
// and a partner topic with no source are refused (the fake's rules,
// eventgrid.go); a change of the configuration is PUT again; deleted, all
// go.
func TestEventGridPartners(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "egpartner")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	if err := k8s.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "saas-hook"},
		StringData: map[string]string{"url": "https://hooks.example.com/events?code=hook-key"},
	}); err != nil {
		t.Fatal(err)
	}
	onRG := &genruntime.KnownResourceReference{Name: "rg"}
	egID := rgID + "/providers/Microsoft.EventGrid/"

	registration := &eventgrid.PartnerRegistration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "saas"},
		Spec:       eventgrid.PartnerRegistration_Spec{Owner: onRG, Location: ptr("global")},
	}
	partnerNS := &eventgrid.PartnerNamespace{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "saas-ns"},
		Spec: eventgrid.PartnerNamespace_Spec{Owner: onRG, Location: ptr("westus"),
			PartnerRegistrationFullyQualifiedReference: &genruntime.ResourceReference{Group: "eventgrid.azure.hfl.dev", Kind: "PartnerRegistration", Name: "saas"},
			PartnerTopicRoutingMode:                    ptr(eventgrid.PartnerNamespaceProperties_PartnerTopicRoutingMode_ChannelNameHeader)},
	}
	orphan := &eventgrid.PartnerNamespace{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "orphan-ns"},
		Spec: eventgrid.PartnerNamespace_Spec{Owner: onRG, Location: ptr("westus"),
			PartnerRegistrationFullyQualifiedReference: &genruntime.ResourceReference{ARMID: egID + "partnerRegistrations/gone"}},
	}
	config := &eventgrid.PartnerConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "partners"},
		Spec: eventgrid.PartnerConfiguration_Spec{Owner: onRG, Location: ptr("global"), PartnerAuthorization: &eventgrid.PartnerAuthorization{
			DefaultMaximumExpirationTimeInDays: ptr(7), AuthorizedPartnersList: []eventgrid.Partner{{PartnerName: ptr("saas")}}}},
	}
	topic := &eventgrid.PartnerTopic{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "saas-events"},
		Spec: eventgrid.PartnerTopic_Spec{Owner: onRG, Location: ptr("westus"), Source: ptr("tenant-1"),
			ActivationState: ptr(eventgrid.PartnerTopicProperties_ActivationState_Activated), PartnerTopicFriendlyDescription: ptr("The SaaS tenant's events")},
	}
	sourceless := &eventgrid.PartnerTopic{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "sourceless"},
		Spec:       eventgrid.PartnerTopic_Spec{Owner: onRG, Location: ptr("westus")},
	}
	first := []conditioned{registration, partnerNS, orphan, config, topic, sourceless}
	for _, o := range first {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []conditioned{registration, partnerNS, config, topic} {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	eventually(t, timeout, "a missing registration refused", notReadyWith(orphan, "was not found"))
	eventually(t, timeout, "a partner topic with no source refused", notReadyWith(sourceless, "source"))

	channel := &eventgrid.PartnerNamespacesChannel{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "contoso"},
		Spec: eventgrid.PartnerNamespacesChannel_Spec{Owner: &genruntime.KnownResourceReference{Name: "saas-ns"},
			ChannelType: ptr(eventgrid.ChannelProperties_ChannelType_PartnerTopic), MessageForActivation: ptr("Activate to receive the tenant's events"),
			PartnerTopicInfo: &eventgrid.PartnerTopicInfo{AzureSubscriptionId: ptr(subID), ResourceGroupName: ptr(ns + "-rg"), Name: ptr("saas-events"), Source: ptr("tenant-1")}},
	}
	sub := &eventgrid.PartnerTopicsEventSubscription{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "all-events"},
		Spec: eventgrid.PartnerTopicsEventSubscription_Spec{Owner: &genruntime.KnownResourceReference{Name: "saas-events"},
			Destination: &eventgrid.EventSubscriptionDestination{WebHook: &eventgrid.WebHookEventSubscriptionDestination{
				EndpointType: ptr(eventgrid.WebHookEventSubscriptionDestination_EndpointType_WebHook),
				EndpointUrl:  &genruntime.SecretReference{Name: "saas-hook", Key: "url"}}}},
	}
	second := []conditioned{channel, sub}
	for _, o := range second {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range second {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	expectResource(t, egID+"partnerNamespaces/saas-ns", func(r map[string]any) error {
		if p := properties(r); p["partnerRegistrationFullyQualifiedId"] != egID+"partnerRegistrations/saas" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if !armFake.HasResource(egID + "partnerTopics/saas-events/eventSubscriptions/all-events") {
		t.Error("the partner topic's subscription is not in the fake")
	}

	// A change is PUT again: the configuration's default expiry.
	if err := change(t, config, func(c *eventgrid.PartnerConfiguration) {
		c.Spec.PartnerAuthorization.DefaultMaximumExpirationTimeInDays = ptr(30)
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, egID+"partnerConfigurations/default", func(r map[string]any) error {
		auth, _ := properties(r)["partnerAuthorization"].(map[string]any)
		if fmt.Sprint(auth["defaultMaximumExpirationTimeInDays"]) != "30" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, objs := range [][]conditioned{second, first} {
		for _, o := range objs {
			if err := k8s.Delete(ctx, o); err != nil {
				t.Fatal(err)
			}
		}
		for _, o := range objs {
			eventually(t, timeout, o.GetName()+" gone", gone(o))
		}
	}
	for _, id := range []string{egID + "partnerNamespaces/saas-ns", egID + "partnerConfigurations/default", egID + "partnerTopics/saas-events"} {
		if armFake.HasResource(id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
}
