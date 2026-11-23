// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	logic "github.com/highfivelabs/aso-extensions/apis/arm/web/v20160601"
)

// The Logic Apps connections of docs/plan.md f56, in a resource group: a
// custom API (custom connector) from an OpenAPI URL, its OAuth client
// secret from a Secret; an API connection to a managed API in its region,
// its parameter values from a Secret (sent to Resource Manager, never in
// the status), and one to the custom API by object; an on-premises data
// gateway on its installation. A connection to a managed API in another
// region, a custom API with no definition and a gateway on a missing
// installation are refused (the fake's rules, logicconnections.go); a change
// is PUT again; deleted, all go.
func TestLogicAppsConnections(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "logic-conn")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	installID := "/subscriptions/" + subID + "/providers/Microsoft.Web/locations/westus/connectionGatewayInstallations/" + ns + "-gw"
	armFake.AddResource(installID)
	managedAPI := func(loc string) string {
		return "/subscriptions/" + subID + "/providers/Microsoft.Web/locations/" + loc + "/managedApis/azureblob"
	}
	secret(t, ns, "blob-conn", map[string]string{"accountName": "contosodata", "accessKey": "Blob-Acc3ss-Key"})
	secret(t, ns, "orders-oauth", map[string]string{"client-secret": "OAuth-Cl1ent-Secret"})
	onRG := &genruntime.KnownResourceReference{Name: "rg"}

	api := &logic.CustomApi{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "orders"},
		Spec: logic.CustomApi_Spec{
			Owner: onRG, Location: ptr("westus"),
			Properties: &logic.CustomApiPropertiesDefinition{
				DisplayName:    ptr("Orders"),
				ApiType:        ptr(logic.ApiType_Rest),
				ApiDefinitions: &logic.ApiResourceDefinitions{OriginalSwaggerUrl: ptr("https://contoso.example/orders/openapi.json")},
				BackendService: &logic.ApiResourceBackendService{ServiceUrl: ptr("https://orders.contoso.example")},
				ConnectionParameters: map[string]logic.ConnectionParameter{
					"token": {
						Type: ptr(logic.ConnectionParameter_Type_OauthSetting),
						OAuthSettings: &logic.ApiOAuthSettings{
							IdentityProvider: ptr("aad"), ClientId: ptr("00000000-0000-0000-0000-0000000000c1"),
							ClientSecret: &genruntime.SecretReference{Name: "orders-oauth", Key: "client-secret"},
							Scopes:       []string{"api://orders/.default"},
						},
					},
				},
			},
		},
	}
	blob := &logic.ApiConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "blob"},
		Spec: logic.ApiConnection_Spec{
			Owner: onRG, Location: ptr("westus"),
			Properties: &logic.Connection_Properties_Spec{
				DisplayName:              ptr("Contoso data"),
				Api:                      &logic.ApiReference{Reference: &genruntime.ResourceReference{ARMID: managedAPI("westus")}},
				ParameterValues:          &genruntime.SecretMapReference{Name: "blob-conn"},
				NonSecretParameterValues: map[string]string{"region": "westus"},
			},
		},
	}
	orders := &logic.ApiConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "orders-conn"},
		Spec: logic.ApiConnection_Spec{
			Owner: onRG, Location: ptr("westus"),
			Properties: &logic.Connection_Properties_Spec{
				Api: &logic.ApiReference{Reference: &genruntime.ResourceReference{Group: "web.azure.hfl.dev", Kind: "CustomApi", Name: "orders"}},
			},
		},
	}
	gateway := &logic.ConnectionGateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "onprem"},
		Spec: logic.ConnectionGateway_Spec{
			Owner: onRG, Location: ptr("westus"),
			Properties: &logic.ConnectionGateway_Properties_Spec{
				DisplayName:                   ptr("On-premises"),
				ConnectionGatewayInstallation: &logic.ConnectionGatewayReference{Reference: &genruntime.ResourceReference{ARMID: installID}},
				ContactInformation:            []string{"platform@contoso.example"},
			},
		},
	}
	// Refused: a managed API in another region, a custom API with no
	// definition, a gateway on an installation that is not there.
	elsewhere := &logic.ApiConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "elsewhere"},
		Spec: logic.ApiConnection_Spec{
			Owner: onRG, Location: ptr("westus"),
			Properties: &logic.Connection_Properties_Spec{
				Api: &logic.ApiReference{Reference: &genruntime.ResourceReference{ARMID: managedAPI("eastus")}},
			},
		},
	}
	undefined := &logic.CustomApi{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "undefined"},
		Spec: logic.CustomApi_Spec{
			Owner: onRG, Location: ptr("westus"),
			Properties: &logic.CustomApiPropertiesDefinition{DisplayName: ptr("No definition")},
		},
	}
	lost := &logic.ConnectionGateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "lost"},
		Spec: logic.ConnectionGateway_Spec{
			Owner: onRG, Location: ptr("westus"),
			Properties: &logic.ConnectionGateway_Properties_Spec{
				ConnectionGatewayInstallation: &logic.ConnectionGatewayReference{Reference: &genruntime.ResourceReference{ARMID: installID + "-gone"}},
			},
		},
	}
	objs := []conditioned{api, blob, orders, gateway, elsewhere, undefined, lost}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []conditioned{api, blob, orders, gateway} {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	eventually(t, timeout, "a managed API in another region refused", notReadyWith(elsewhere, "not in the connection's region"))
	eventually(t, timeout, "a custom API with no definition refused", notReadyWith(undefined, "carries its definition"))
	eventually(t, timeout, "a gateway on a missing installation refused", notReadyWith(lost, "was not found"))

	apiID := rgID + "/providers/Microsoft.Web/customApis/orders"
	blobID := rgID + "/providers/Microsoft.Web/connections/blob"
	expectResource(t, apiID, func(r map[string]any) error {
		p := properties(r)
		defs, _ := p["apiDefinitions"].(map[string]any)
		if defs["originalSwaggerUrl"] != "https://contoso.example/orders/openapi.json" || p["apiType"] != "Rest" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if k, ok := armFake.SharedKey(apiID + "/connectionParameters/token/clientSecret"); !ok || k != "OAuth-Cl1ent-Secret" {
		t.Fatalf("the client secret Resource Manager was given: %q %v", k, ok)
	}
	expectResource(t, blobID, func(r map[string]any) error {
		p := properties(r)
		ref, _ := p["api"].(map[string]any)
		if !strings.EqualFold(fmt.Sprint(ref["id"]), managedAPI("westus")) || p["displayName"] != "Contoso data" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	for name, want := range map[string]string{"accountName": "contosodata", "accessKey": "Blob-Acc3ss-Key"} {
		if k, ok := armFake.SharedKey(blobID + "/parameterValues/" + name); !ok || k != want {
			t.Fatalf("%s Resource Manager was given: %q %v", name, k, ok)
		}
	}
	expectResource(t, rgID+"/providers/Microsoft.Web/connections/orders-conn", func(r map[string]any) error {
		if ref, _ := properties(r)["api"].(map[string]any); !strings.EqualFold(fmt.Sprint(ref["id"]), apiID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, rgID+"/providers/Microsoft.Web/connectionGateways/onprem", func(r map[string]any) error {
		inst, _ := properties(r)["connectionGatewayInstallation"].(map[string]any)
		if !strings.EqualFold(fmt.Sprint(inst["id"]), installID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	// The status never holds a secret.
	for _, o := range []conditioned{api, blob} {
		eventually(t, timeout, o.GetName()+"'s status", func() error {
			if err := k8s.Get(ctx, client.ObjectKeyFromObject(o), o); err != nil {
				return err
			}
			b, err := json.Marshal(o)
			if err != nil {
				return err
			}
			if strings.Contains(string(b), "Blob-Acc3ss-Key") || strings.Contains(string(b), "OAuth-Cl1ent-Secret") {
				t.Fatalf("a secret is in %s: %s", o.GetName(), b)
			}
			return nil
		})
	}
	if blob.Status.Properties == nil || blob.Status.Properties.NonSecretParameterValues["region"] != "westus" {
		t.Fatalf("status: %+v", blob.Status.Properties)
	}

	// A change is PUT again.
	if err := change(t, blob, func(blob *logic.ApiConnection) {
		blob.Spec.Properties.DisplayName = ptr("Contoso data lake")
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, blobID, func(r map[string]any) error {
		if properties(r)["displayName"] != "Contoso data lake" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// Deleted: connections first, then the custom API and the gateway.
	for _, o := range objs {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, id := range []string{apiID, blobID, rgID + "/providers/Microsoft.Web/connectionGateways/onprem"} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in the fake", id)
		}
	}
}
