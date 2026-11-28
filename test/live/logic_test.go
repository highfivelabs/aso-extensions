// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"strings"
	"testing"

	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	logic "github.com/highfivelabs/aso-extensions/apis/arm/logic/v20190501"
)

const logicWorkflowAPI = "2019-05-01"

// A Free integration account with a partner and a schema, and a disabled
// Consumption workflow on it (docs/plan.md f74): created, the workflow's
// parameter changed, deleted. Free: a Free integration account costs
// nothing (a subscription may have one per region, so a run fails if
// another exists there), and a Consumption workflow bills only runs, of
// which a disabled one has none.
func TestLiveLogicWorkflow(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.Logic")
	ns := namespace(t, "logic")
	accountName, workflowName := azName("b2b"), azName("greeter")

	account := &logic.IntegrationAccount{}
	account.Namespace, account.Name = ns, "b2b"
	account.Spec = logic.IntegrationAccount_Spec{AzureName: accountName, Owner: owner("rg"), Location: ptr(location),
		Sku: &logic.IntegrationAccountSku{Name: ptr(logic.IntegrationAccountSkuName_Free)}}
	create(t, timeout, account)
	partner := &logic.IntegrationAccountsPartner{}
	partner.Namespace, partner.Name = ns, "contoso"
	partner.Spec = logic.IntegrationAccountsPartner_Spec{AzureName: "contoso", Owner: owner("b2b"), PartnerType: ptr(logic.PartnerType_B2B),
		Content: &logic.PartnerContent{B2B: &logic.B2BPartnerContent{BusinessIdentities: []logic.BusinessIdentity{{Qualifier: ptr("ZZ"), Value: ptr("CONTOSO")}}}}}
	schema := &logic.IntegrationAccountsSchema{}
	schema.Namespace, schema.Name = ns, "order"
	schema.Spec = logic.IntegrationAccountsSchema_Spec{AzureName: "order", Owner: owner("b2b"), SchemaType: ptr(logic.SchemaType_Xml), ContentType: ptr("application/xml"),
		Content: ptr(`<?xml version="1.0" encoding="utf-8"?><xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="Order" type="xs:string"/></xs:schema>`)}
	workflow := &logic.Workflow{}
	workflow.Namespace, workflow.Name = ns, "greeter"
	workflow.Spec = logic.Workflow_Spec{AzureName: workflowName, Owner: owner("rg"), Location: ptr(location), State: ptr(logic.WorkflowState_Disabled),
		Definition: map[string]v1.JSON{
			"$schema":        {Raw: []byte(`"https://schema.management.azure.com/providers/Microsoft.Logic/schemas/2016-06-01/workflowdefinition.json#"`)},
			"contentVersion": {Raw: []byte(`"1.0.0.0"`)},
			"parameters":     {Raw: []byte(`{"greeting":{"type":"String","defaultValue":"hi"}}`)},
			"triggers":       {Raw: []byte(`{"manual":{"type":"Request","kind":"Http"}}`)},
			"actions":        {Raw: []byte(`{"respond":{"type":"Response","inputs":{"statusCode":200,"body":"@parameters('greeting')"}}}`)},
		},
		Parameters:         map[string]logic.WorkflowParameter{"greeting": {Value: v1.JSON{Raw: []byte(`"hello"`)}}},
		IntegrationAccount: &logic.ResourceReference{Reference: &genruntime.ResourceReference{Group: "logic.azure.hfl.dev", Kind: "IntegrationAccount", Name: "b2b"}},
	}
	create(t, timeout, partner, schema, workflow)

	wfID := rgID() + "/providers/Microsoft.Logic/workflows/" + workflowName
	expectResource(t, wfID, logicWorkflowAPI, func(r map[string]any) error {
		account, _ := properties(r)["integrationAccount"].(map[string]any)
		if !strings.HasSuffix(strings.ToLower(fmt.Sprint(account["id"])), strings.ToLower(accountName)) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, workflow, func(w *logic.Workflow) {
		w.Spec.Parameters = map[string]logic.WorkflowParameter{"greeting": {Value: v1.JSON{Raw: []byte(`"hello again"`)}}}
	})
	eventually(t, timeout, "the workflow Ready after the change", ready(workflow))
	expectResource(t, wfID, logicWorkflowAPI, func(r map[string]any) error {
		params, _ := properties(r)["parameters"].(map[string]any)
		if !strings.Contains(fmt.Sprint(params["greeting"]), "hello again") {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, workflow)
	expectGone(t, wfID, logicWorkflowAPI)
	remove(t, timeout, schema)
	remove(t, timeout, partner)
	remove(t, timeout, account)
	expectGone(t, rgID()+"/providers/Microsoft.Logic/integrationAccounts/"+accountName, logicWorkflowAPI)
}
