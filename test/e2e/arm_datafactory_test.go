// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	asodatafactory "github.com/Azure/azure-service-operator/v2/api/datafactory/v20180601/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	datafactory "github.com/highfivelabs/aso-extensions/apis/arm/datafactory/v20180601"
)

// The Data Factory kinds of docs/plan.md f78, on ASO's Factory by name (a new
// external owner): the managed virtual network, a managed private endpoint
// in it to a storage account, a credential for a user-assigned identity,
// the global parameters, and the approval of a private endpoint's
// connection. A managed network not named default and a credential naming
// a missing identity are refused (the fake's rules, datafactory.go); a
// change of a global parameter is PUT again; deleted, all go but the managed
// network, which Azure cannot delete (the object is detached).
func TestDataFactoryChildren(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "adf")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	factoryID := rgID + "/providers/Microsoft.DataFactory/factories/etl"
	storageID := rgID + "/providers/Microsoft.Storage/storageAccounts/lake"
	identityID := rgID + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/etl"
	for _, id := range []string{rgID, factoryID, storageID, identityID, factoryID + "/privateEndpointConnections/etl.pe"} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	factory := &asodatafactory.Factory{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "etl"}}
	factory.Spec.AzureName, factory.Spec.Location = "etl", ptr("westus")
	factory.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, factory, factoryID)
	onFactory := &genruntime.KnownResourceReference{Name: "etl"}

	// A managed network has no DELETE in the spec: the object is detached.
	detach := map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}
	vnet := &datafactory.FactoriesManagedVirtualNetwork{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "etl-network", Annotations: detach},
		Spec:       datafactory.FactoriesManagedVirtualNetwork_Spec{AzureName: "default", Owner: onFactory, Properties: &datafactory.ManagedVirtualNetwork{}},
	}
	isolated := &datafactory.FactoriesManagedVirtualNetwork{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "isolated", Annotations: detach},
		Spec:       datafactory.FactoriesManagedVirtualNetwork_Spec{Owner: onFactory, Properties: &datafactory.ManagedVirtualNetwork{}},
	}
	credential := func(name, identity string) *datafactory.FactoriesCredential {
		return &datafactory.FactoriesCredential{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec: datafactory.FactoriesCredential_Spec{Owner: onFactory, Properties: &datafactory.Credential{ManagedIdentity: &datafactory.ManagedIdentityCredential{
				Type: ptr(datafactory.ManagedIdentityCredential_Type_ManagedIdentity), Description: ptr("the pipelines' identity"),
				ResourceReference: &genruntime.ResourceReference{ARMID: identity}}}},
		}
	}
	cred, missing := credential("etl-identity", identityID), credential("gone-identity", identityID+"-gone")
	params := &datafactory.FactoriesGlobalParameter{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "etl-parameters"},
		Spec: datafactory.FactoriesGlobalParameter_Spec{AzureName: "default", Owner: onFactory, Properties: map[string]datafactory.GlobalParameterSpecification{
			"region":  {Type: ptr(datafactory.GlobalParameterType_String), Value: &v1.JSON{Raw: []byte(`"westus"`)}},
			"retries": {Type: ptr(datafactory.GlobalParameterType_Int), Value: &v1.JSON{Raw: []byte(`3`)}},
		}},
	}
	conn := &datafactory.FactoriesPrivateEndpointConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "etl-pe"},
		Spec: datafactory.FactoriesPrivateEndpointConnection_Spec{AzureName: "etl.pe", Owner: onFactory, Properties: &datafactory.PrivateLinkConnectionApprovalRequest{
			PrivateLinkServiceConnectionState: &datafactory.PrivateLinkConnectionState{Status: ptr("Approved"), Description: ptr("approved")}}},
	}
	first := []conditioned{vnet, isolated, cred, missing, params, conn}
	for _, o := range first {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []conditioned{vnet, cred, params, conn} {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	eventually(t, timeout, "a managed network not named default refused", notReadyWith(isolated, "named default"))
	eventually(t, timeout, "a missing identity refused", notReadyWith(missing, "was not found"))

	mpe := &datafactory.FactoriesManagedVirtualNetworksManagedPrivateEndpoint{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "lake-blob"},
		Spec: datafactory.FactoriesManagedVirtualNetworksManagedPrivateEndpoint_Spec{Owner: &genruntime.KnownResourceReference{Name: "etl-network"},
			Properties: &datafactory.ManagedPrivateEndpoint{GroupId: ptr("blob"), PrivateLinkResourceReference: &genruntime.ResourceReference{ARMID: storageID}}},
	}
	if err := k8s.Create(ctx, mpe); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the managed private endpoint Ready", ready(mpe))

	expectResource(t, factoryID+"/credentials/etl-identity", func(r map[string]any) error {
		p := properties(r)
		tp, _ := p["typeProperties"].(map[string]any)
		if p["type"] != "ManagedIdentity" || tp["resourceId"] != identityID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, factoryID+"/managedVirtualNetworks/default/managedPrivateEndpoints/lake-blob", func(r map[string]any) error {
		if p := properties(r); p["privateLinkResourceId"] != storageID || p["groupId"] != "blob" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again: a global parameter's value.
	if err := change(t, params, func(p *datafactory.FactoriesGlobalParameter) {
		p.Spec.Properties["retries"] = datafactory.GlobalParameterSpecification{Type: ptr(datafactory.GlobalParameterType_Int), Value: &v1.JSON{Raw: []byte(`5`)}}
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, factoryID+"/globalParameters/default", func(r map[string]any) error {
		retries, _ := properties(r)["retries"].(map[string]any)
		if fmt.Sprint(retries["value"]) != "5" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, objs := range [][]conditioned{{mpe}, first} {
		for _, o := range objs {
			if err := k8s.Delete(ctx, o); err != nil {
				t.Fatal(err)
			}
		}
		for _, o := range objs {
			eventually(t, timeout, o.GetName()+" gone", gone(o))
		}
	}
	for _, id := range []string{factoryID + "/managedVirtualNetworks/default/managedPrivateEndpoints/lake-blob", factoryID + "/credentials/etl-identity", factoryID + "/globalParameters/default"} {
		if armFake.HasResource(id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
	if !armFake.HasResource(factoryID + "/managedVirtualNetworks/default") {
		t.Error("the managed network, which Azure cannot delete, is gone from the fake")
	}
}
