// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	logic "github.com/highfivelabs/aso-extensions/apis/arm/logic/v20190501"
)

// The Logic Apps kinds of docs/plan.md f74, in a resource group: a Free
// integration account with a schema, a map, two partners and an X12
// agreement between them (its protocol settings the smallest the CRD
// takes), a certificate whose key is in a Key Vault, a batch
// configuration, a session and an assembly; and a Consumption workflow on
// the account, by object, with a parameter. A workflow whose definition
// names no $schema and an agreement whose guest identity is not the guest
// partner's are refused (the fake's rules, logic.go); disabling the
// workflow is PUT again; deleted, all go.
func TestLogicWorkflowAndIntegrationAccount(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "logic")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	vaultID := rgID + "/providers/Microsoft.KeyVault/vaults/b2b-keys"
	for _, id := range []string{rgID, vaultID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	// The X12 content's secret fields, as the smallest content names them.
	if err := k8s.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "conformance"},
		StringData: map[string]string{"value": "x12-security"},
	}); err != nil {
		t.Fatal(err)
	}
	onRG := &genruntime.KnownResourceReference{Name: "rg"}
	acctID := rgID + "/providers/Microsoft.Logic/integrationAccounts/b2b"

	account := &logic.IntegrationAccount{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "b2b"},
		Spec: logic.IntegrationAccount_Spec{Owner: onRG, Location: ptr("westus"),
			Sku: &logic.IntegrationAccountSku{Name: ptr(logic.IntegrationAccountSkuName_Free)}, State: ptr(logic.WorkflowState_Enabled)},
	}
	definition := map[string]v1.JSON{
		"$schema":        {Raw: []byte(`"https://schema.management.azure.com/providers/Microsoft.Logic/schemas/2016-06-01/workflowdefinition.json#"`)},
		"contentVersion": {Raw: []byte(`"1.0.0.0"`)},
		"parameters":     {Raw: []byte(`{"greeting":{"type":"String"}}`)},
		"triggers":       {Raw: []byte(`{"manual":{"type":"Request","kind":"Http"}}`)},
		"actions":        {Raw: []byte(`{"respond":{"type":"Response","inputs":{"statusCode":200,"body":"@parameters('greeting')"}}}`)},
	}
	workflow := &logic.Workflow{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "greeter"},
		Spec: logic.Workflow_Spec{Owner: onRG, Location: ptr("westus"), State: ptr(logic.WorkflowState_Enabled), Definition: definition,
			Parameters: map[string]logic.WorkflowParameter{"greeting": {Type: ptr(logic.ParameterType_String), Value: v1.JSON{Raw: []byte(`"hello"`)}}},
			IntegrationAccount: &logic.ResourceReference{Reference: &genruntime.ResourceReference{
				Group: "logic.azure.hfl.dev", Kind: "IntegrationAccount", Name: "b2b"}}},
	}
	schemaless := &logic.Workflow{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "schemaless"},
		Spec: logic.Workflow_Spec{Owner: onRG, Location: ptr("westus"),
			Definition: map[string]v1.JSON{"triggers": {Raw: []byte(`{}`)}}},
	}
	first := []conditioned{account, workflow, schemaless}
	for _, o := range first {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "the integration account Ready", ready(account))
	eventually(t, timeout, "the workflow Ready", ready(workflow))
	eventually(t, timeout, "a definition with no $schema refused", notReadyWith(schemaless, "$schema"))

	onAccount := &genruntime.KnownResourceReference{Name: "b2b"}
	partner := func(name, value string) *logic.IntegrationAccountsPartner {
		return &logic.IntegrationAccountsPartner{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec: logic.IntegrationAccountsPartner_Spec{Owner: onAccount, PartnerType: ptr(logic.PartnerType_B2B),
				Content: &logic.PartnerContent{B2B: &logic.B2BPartnerContent{BusinessIdentities: []logic.BusinessIdentity{{Qualifier: ptr("ZZ"), Value: ptr(value)}}}}},
		}
	}
	contoso, fabrikam := partner("contoso", "CONTOSO"), partner("fabrikam", "FABRIKAM")
	schema := &logic.IntegrationAccountsSchema{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "order"},
		Spec: logic.IntegrationAccountsSchema_Spec{Owner: onAccount, SchemaType: ptr(logic.SchemaType_Xml), ContentType: ptr("application/xml"),
			Content: ptr(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="Order" type="xs:string"/></xs:schema>`)},
	}
	liquid := &logic.IntegrationAccountsMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "order-to-json"},
		Spec: logic.IntegrationAccountsMap_Spec{Owner: onAccount, MapType: ptr(logic.MapType_Liquid), ContentType: ptr("text/plain"),
			Content: ptr(`{"order": "{{ content.Order }}"}`)},
	}
	cert := &logic.IntegrationAccountsCertificate{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "signing"},
		Spec: logic.IntegrationAccountsCertificate_Spec{Owner: onAccount, Key: &logic.KeyVaultKeyReference{KeyName: ptr("signing"),
			KeyVault: &logic.KeyVaultKeyReference_KeyVault{Reference: &genruntime.ResourceReference{ARMID: vaultID}}}},
	}
	batchConfig := &logic.IntegrationAccountsBatchConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "daily"},
		Spec: logic.IntegrationAccountsBatchConfiguration_Spec{Owner: onAccount, Properties: &logic.BatchConfigurationProperties{
			BatchGroupName: ptr("daily"), ReleaseCriteria: &logic.BatchReleaseCriteria{MessageCount: ptr(10)}}},
	}
	session := &logic.IntegrationAccountsSession{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "control-numbers"},
		Spec:       logic.IntegrationAccountsSession_Spec{Owner: onAccount, Content: map[string]v1.JSON{"controlNumber": {Raw: []byte(`"1"`)}}},
	}
	assembly := &logic.IntegrationAccountsAssembly{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "codec"},
		Spec: logic.IntegrationAccountsAssembly_Spec{Owner: onAccount, Properties: &logic.AssemblyProperties{
			AssemblyName: ptr("Codec"), AssemblyVersion: ptr("1.0.0.0"), ContentType: ptr("application/octet-stream"),
			Content: &v1.JSON{Raw: []byte(`"TVqQAAMAAAAEAAAA"`)}}},
	}
	second := []conditioned{contoso, fabrikam, schema, liquid, cert, batchConfig, session, assembly}
	for _, o := range second {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range second {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}

	// The X12 agreement's content: the smallest the CRD takes (its X12
	// protocol settings have dozens of required fields).
	var x12 logic.X12AgreementContent
	for _, crd := range generatedCRDs(t) {
		if crd.Name != "integrationaccountsagreements.logic.azure.hfl.dev" {
			continue
		}
		c := &conformanceContext{ns: ns, resourceGroup: rgID, ref: vaultID}
		raw, err := json.Marshal(conformanceValue(c, "x12", crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties["content"].Properties["x12"]))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &x12); err != nil {
			t.Fatal(err)
		}
	}
	if x12.SendAgreement == nil {
		t.Fatal("no X12 content made from the agreement's CRD")
	}
	agreement := func(name, guestValue string) *logic.IntegrationAccountsAgreement {
		return &logic.IntegrationAccountsAgreement{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec: logic.IntegrationAccountsAgreement_Spec{Owner: onAccount, AgreementType: ptr(logic.AgreementType_X12),
				HostPartner: ptr("contoso"), GuestPartner: ptr("fabrikam"),
				HostIdentity:  &logic.BusinessIdentity{Qualifier: ptr("ZZ"), Value: ptr("CONTOSO")},
				GuestIdentity: &logic.BusinessIdentity{Qualifier: ptr("ZZ"), Value: ptr(guestValue)},
				Content:       &logic.AgreementContent{X12: x12.DeepCopy()}},
		}
	}
	x12Agreement, stranger := agreement("contoso-fabrikam", "FABRIKAM"), agreement("contoso-stranger", "STRANGER")
	third := []conditioned{x12Agreement, stranger}
	for _, o := range third {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "the agreement Ready", ready(x12Agreement))
	eventually(t, timeout, "an identity not the guest partner's refused", notReadyWith(stranger, "not a business identity"))

	wfID := rgID + "/providers/Microsoft.Logic/workflows/greeter"
	expectResource(t, wfID, func(r map[string]any) error {
		p := properties(r)
		account, _ := p["integrationAccount"].(map[string]any)
		params, _ := p["parameters"].(map[string]any)
		greeting, _ := params["greeting"].(map[string]any)
		if account["id"] != acctID || greeting["value"] != "hello" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, acctID+"/certificates/signing", func(r map[string]any) error {
		key, _ := properties(r)["key"].(map[string]any)
		vault, _ := key["keyVault"].(map[string]any)
		if vault["id"] != vaultID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again: the workflow disabled.
	if err := change(t, workflow, func(w *logic.Workflow) { w.Spec.State = ptr(logic.WorkflowState_Disabled) }); err != nil {
		t.Fatal(err)
	}
	expectResource(t, wfID, func(r map[string]any) error {
		if p := properties(r); p["state"] != "Disabled" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, objs := range [][]conditioned{third, second, first} {
		for _, o := range objs {
			if err := k8s.Delete(ctx, o); err != nil {
				t.Fatal(err)
			}
		}
		for _, o := range objs {
			eventually(t, timeout, o.GetName()+" gone", gone(o))
		}
	}
	for _, id := range []string{wfID, acctID, acctID + "/agreements/contoso-fabrikam", acctID + "/partners/contoso"} {
		if armFake.HasResource(id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
}
