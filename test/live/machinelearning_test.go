// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"os"
	"strings"
	"testing"

	asoml "github.com/Azure/azure-service-operator/v2/api/machinelearningservices/v1api20240401/storage"

	ml "github.com/highfivelabs/aso-extensions/apis/arm/machinelearningservices/v20260901"
)

const machineLearningAPI = "2026-09-01"

// A Machine Learning workspace's model and environment containers, an
// environment version naming a public image, a batch endpoint with no
// deployment (f83) and a disabled schedule of a serverless command job
// (f85: it never fires, so no job runs), on a workspace made as a
// fixture with its storage account and key vault (docs/plan.md f82):
// created, the model's description changed, deleted. Storage cents: the
// workspace bills nothing by itself, the version names a prebuilt image
// (no image build), and a batch endpoint bills only its deployments' jobs. Not here: datastores (a real account key or
// identity), outbound rules (a managed network, billed by the hour),
// connections (a private endpoint), registries (a registry's storage and
// registry per region), model and data versions (their URIs are checked
// against real storage), jobs (a job runs at once and bills its compute).
func TestLiveMachineLearningAssets(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.MachineLearningServices")
	ns := namespace(t, "ml")
	storageID, _ := storageAccount(t, "mlst")
	vaultName := strings.ReplaceAll(azName("mlkv"), "-", "")
	vaultID := rgID() + "/providers/Microsoft.KeyVault/vaults/" + vaultName
	fixture(t, vaultID, "2023-07-01", map[string]any{
		"location": location,
		"properties": map[string]any{
			"tenantId": os.Getenv("AZURE_TENANT_ID"), "sku": map[string]any{"family": "A", "name": "standard"},
			"enableRbacAuthorization": true, "enableSoftDelete": true, "softDeleteRetentionInDays": 7,
		},
	})
	name := azName("ml")
	wsID := rgID() + "/providers/Microsoft.MachineLearningServices/workspaces/" + name
	fixture(t, wsID, machineLearningAPI, map[string]any{
		"location": location, "identity": map[string]any{"type": "SystemAssigned"},
		"properties": map[string]any{"storageAccount": storageID, "keyVault": vaultID},
	})
	ws := &asoml.Workspace{}
	ws.Namespace, ws.Name = ns, "lab"
	ws.Spec.AzureName = name
	ws.Spec.Location = ptr(location)
	ws.Spec.Owner = owner("rg")
	standIn(t, ws, wsID, nil)

	model := &ml.WorkspacesModel{}
	model.Namespace, model.Name = ns, "churn"
	model.Spec = ml.WorkspacesModel_Spec{Owner: owner("lab"), Properties: &ml.ModelContainerProperties{Description: ptr("live test")}}
	env := &ml.WorkspacesEnvironment{}
	env.Namespace, env.Name = ns, "py"
	env.Spec = ml.WorkspacesEnvironment_Spec{Owner: owner("lab"), Properties: &ml.EnvironmentContainerProperties{Description: ptr("live test")}}
	batch := &ml.WorkspacesBatchEndpoint{}
	batch.Namespace, batch.Name = ns, "nightly"
	batch.Spec = ml.WorkspacesBatchEndpoint_Spec{Owner: owner("lab"), Location: ptr(location),
		Properties: &ml.BatchEndpointProperties{AuthMode: ptr(ml.EndpointAuthMode_AADToken), Description: ptr("live test")}}
	schedule := &ml.WorkspacesSchedule{}
	schedule.Namespace, schedule.Name = ns, "nightly-training"
	schedule.Spec = ml.WorkspacesSchedule_Spec{Owner: owner("lab"), Properties: &ml.ScheduleProperties{
		IsEnabled: ptr(false), Description: ptr("live test"),
		Action: &ml.ScheduleActionBase{CreateJob: &ml.JobScheduleAction{ActionType: ptr(ml.JobScheduleAction_ActionType_CreateJob),
			JobDefinition: &ml.JobBaseProperties{Command: &ml.CommandJob{JobType: ptr(ml.CommandJob_JobType_Command),
				Command:       ptr("echo live test"),
				EnvironmentId: ptr("azureml://registries/azureml/environments/sklearn-1.5/labels/latest"),
				Resources:     &ml.JobResourceConfiguration{InstanceType: ptr("Standard_DS3_v2"), InstanceCount: ptr(1)}}}}},
		Trigger: &ml.TriggerBase{Cron: &ml.CronTrigger{TriggerType: ptr(ml.CronTrigger_TriggerType_Cron), Expression: ptr("0 2 * * *")}}}}
	create(t, timeout, model, env, batch, schedule)
	version := &ml.WorkspacesEnvironmentsVersion{}
	version.Namespace, version.Name = ns, "py-1"
	version.Spec = ml.WorkspacesEnvironmentsVersion_Spec{AzureName: "1", Owner: owner("py"), Properties: &ml.EnvironmentVersionProperties{
		Image: ptr("mcr.microsoft.com/azureml/openmpi4.1.0-ubuntu22.04")}}
	create(t, timeout, version)

	expectResource(t, wsID+"/environments/py/versions/1", machineLearningAPI, func(r map[string]any) error {
		if !strings.Contains(fmt.Sprint(properties(r)["image"]), "openmpi4.1.0") {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, model, func(m *ml.WorkspacesModel) { m.Spec.Properties.Description = ptr("live test, changed") })
	eventually(t, timeout, "the model Ready after the change", ready(model))
	expectResource(t, wsID+"/models/churn", machineLearningAPI, func(r map[string]any) error {
		if properties(r)["description"] != "live test, changed" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, wsID+"/schedules/nightly-training", machineLearningAPI, func(r map[string]any) error {
		if properties(r)["isEnabled"] != false {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, schedule)
	remove(t, timeout, batch)
	remove(t, timeout, version)
	remove(t, timeout, env)
	remove(t, timeout, model)
	expectGone(t, wsID+"/models/churn", machineLearningAPI)
}
