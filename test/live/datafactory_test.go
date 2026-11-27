// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"strings"
	"testing"

	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	asodatafactory "github.com/Azure/azure-service-operator/v2/api/datafactory/v20180601/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	datafactory "github.com/highfivelabs/aso-extensions/apis/arm/datafactory/v20180601"
)

const dataFactoryAPI = "2018-06-01"

// A factory's credential for a user-assigned identity and its global
// parameters, on a factory and an identity made as fixtures (docs/plan.md
// f78), and a self-hosted integration runtime and a schedule trigger of no
// pipeline (f86): created, a parameter changed, deleted. Free: Data Factory
// bills runs, and nothing runs (a self-hosted runtime with no node bills
// nothing; a trigger made by PUT is stopped); an identity costs nothing.
// Not here: the managed network and its private endpoints (approval on the
// target), private endpoint connections, data flows and change data
// capture (they name datasets and linked services, which no kind makes).
func TestLiveDataFactoryChildren(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.DataFactory")
	ns := namespace(t, "adf")
	name := azName("adf")
	factoryID := rgID() + "/providers/Microsoft.DataFactory/factories/" + name
	identityID := rgID() + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/" + azName("adfid")
	fixture(t, identityID, "2023-01-31", map[string]any{"location": location})
	fixture(t, factoryID, dataFactoryAPI, map[string]any{
		"location": location, "identity": map[string]any{"type": "UserAssigned", "userAssignedIdentities": map[string]any{identityID: map[string]any{}}},
		"properties": map[string]any{},
	})
	factory := &asodatafactory.Factory{}
	factory.Namespace, factory.Name = ns, "etl"
	factory.Spec.AzureName = name
	factory.Spec.Location = ptr(location)
	factory.Spec.Owner = owner("rg")
	standIn(t, factory, factoryID, nil)

	cred := &datafactory.FactoriesCredential{}
	cred.Namespace, cred.Name = ns, "etl-identity"
	cred.Spec = datafactory.FactoriesCredential_Spec{AzureName: "etl-identity", Owner: owner("etl"), Properties: &datafactory.Credential{
		ManagedIdentity: &datafactory.ManagedIdentityCredential{Type: ptr(datafactory.ManagedIdentityCredential_Type_ManagedIdentity),
			ResourceReference: &genruntime.ResourceReference{ARMID: identityID}}}}
	params := &datafactory.FactoriesGlobalParameter{}
	params.Namespace, params.Name = ns, "etl-parameters"
	params.Spec = datafactory.FactoriesGlobalParameter_Spec{AzureName: "default", Owner: owner("etl"), Properties: map[string]datafactory.GlobalParameterSpecification{
		"region": {Type: ptr(datafactory.GlobalParameterType_String), Value: &v1.JSON{Raw: []byte(`"` + location + `"`)}},
	}}
	runtime := &datafactory.FactoriesIntegrationRuntime{}
	runtime.Namespace, runtime.Name = ns, "onprem"
	runtime.Spec = datafactory.FactoriesIntegrationRuntime_Spec{Owner: owner("etl"), Properties: &datafactory.IntegrationRuntime{
		SelfHosted: &datafactory.SelfHostedIntegrationRuntime{Type: ptr(datafactory.SelfHostedIntegrationRuntime_Type_SelfHosted), Description: ptr("live test")}}}
	trigger := &datafactory.FactoriesTrigger{}
	trigger.Namespace, trigger.Name = ns, "nightly"
	trigger.Spec = datafactory.FactoriesTrigger_Spec{Owner: owner("etl"), Properties: &datafactory.Trigger{Schedule: &datafactory.ScheduleTrigger{
		Type: ptr(datafactory.ScheduleTrigger_Type_ScheduleTrigger), Description: ptr("live test"),
		Recurrence: &datafactory.ScheduleTriggerRecurrence{Frequency: ptr(datafactory.RecurrenceFrequency_Day), Interval: ptr(1)}}}}
	create(t, timeout, cred, params, runtime, trigger)
	expectResource(t, factoryID+"/integrationRuntimes/onprem", dataFactoryAPI, func(r map[string]any) error {
		if properties(r)["type"] != "SelfHosted" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	expectResource(t, factoryID+"/credentials/etl-identity", dataFactoryAPI, func(r map[string]any) error {
		if !strings.Contains(strings.ToLower(fmt.Sprint(properties(r))), strings.ToLower(identityID)) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, params, func(p *datafactory.FactoriesGlobalParameter) {
		p.Spec.Properties["retries"] = datafactory.GlobalParameterSpecification{Type: ptr(datafactory.GlobalParameterType_Int), Value: &v1.JSON{Raw: []byte(`3`)}}
	})
	eventually(t, timeout, "the parameters Ready after the change", ready(params))
	expectResource(t, factoryID+"/globalParameters/default", dataFactoryAPI, func(r map[string]any) error {
		if _, ok := properties(r)["retries"]; !ok {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, trigger)
	remove(t, timeout, runtime)
	remove(t, timeout, params)
	remove(t, timeout, cred)
	expectGone(t, factoryID+"/credentials/etl-identity", dataFactoryAPI)
}
