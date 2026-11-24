// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	asoml "github.com/Azure/azure-service-operator/v2/api/machinelearningservices/v1api20240401/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	ml "github.com/highfivelabs/aso-extensions/apis/arm/machinelearningservices/v20260901"
)

// The Machine Learning kinds of docs/plan.md f82, on ASO's Workspace and
// Registry by name (new external owners): a model and its version naming
// a data version as its dataset, a data container and version, an
// environment and its version, a registry's code and its version, a blob
// datastore whose account key comes from a Secret, FQDN and private
// endpoint outbound rules, and the approval of a private endpoint's
// connection. A model version whose URI changes (asset versions are
// immutable), an environment version of neither image nor build and a
// private endpoint rule to a missing account are refused (the fake's
// rules, machinelearning.go); a container's description change is PUT
// again. Deleted, all go.
func TestMachineLearningAssets(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "ml")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	wsID := rgID + "/providers/Microsoft.MachineLearningServices/workspaces/lab"
	regID := rgID + "/providers/Microsoft.MachineLearningServices/registries/shared"
	storageID := rgID + "/providers/Microsoft.Storage/storageAccounts/lake"
	for _, id := range []string{rgID, wsID, regID, storageID, wsID + "/privateEndpointConnections/lab.pe"} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	ws := &asoml.Workspace{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "lab"}}
	ws.Spec.AzureName, ws.Spec.Location = "lab", ptr("westus")
	ws.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, ws, wsID)
	reg := &asoml.Registry{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "shared"}}
	reg.Spec.AzureName, reg.Spec.Location = "shared", ptr("westus")
	reg.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, reg, regID)
	if err := k8s.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "lake"},
		StringData: map[string]string{"key": "bGFrZS1rZXk="},
	}); err != nil {
		t.Fatal(err)
	}
	meta := func(name string) metav1.ObjectMeta { return metav1.ObjectMeta{Namespace: ns, Name: name} }
	onWS := &genruntime.KnownResourceReference{Name: "lab"}
	onReg := &genruntime.KnownResourceReference{Name: "shared"}
	create := func(objs ...conditioned) {
		t.Helper()
		for _, o := range objs {
			if err := k8s.Create(ctx, o); err != nil {
				t.Fatal(err)
			}
		}
	}

	// The containers first: their versions are their children.
	model := &ml.WorkspacesModel{ObjectMeta: meta("churn"), Spec: ml.WorkspacesModel_Spec{Owner: onWS,
		Properties: &ml.ModelContainerProperties{Description: ptr("churn"), Tags: map[string]string{"team": "data"}}}}
	data := &ml.WorkspacesData{ObjectMeta: meta("sales"), Spec: ml.WorkspacesData_Spec{Owner: onWS,
		Properties: &ml.DataContainerProperties{DataType: ptr(ml.DataType_Uri_Folder)}}}
	env := &ml.WorkspacesEnvironment{ObjectMeta: meta("py"), Spec: ml.WorkspacesEnvironment_Spec{Owner: onWS,
		Properties: &ml.EnvironmentContainerProperties{Description: ptr("python")}}}
	code := &ml.RegistriesCode{ObjectMeta: meta("scoring"), Spec: ml.RegistriesCode_Spec{Owner: onReg,
		Properties: &ml.CodeContainerProperties{Description: ptr("scoring code")}}}
	datastore := &ml.WorkspacesDatastore{ObjectMeta: meta("lake"), Spec: ml.WorkspacesDatastore_Spec{Owner: onWS,
		Properties: &ml.DatastoreProperties{AzureBlob: &ml.AzureBlobDatastore{
			DatastoreType: ptr(ml.AzureBlobDatastore_DatastoreType_AzureBlob), AccountName: ptr("lake"), ContainerName: ptr("raw"),
			Credentials: &ml.DatastoreCredentials{AccountKey: &ml.AccountKeyDatastoreCredentials{
				CredentialsType: ptr(ml.AccountKeyDatastoreCredentials_CredentialsType_AccountKey),
				Secrets: &ml.AccountKeyDatastoreSecrets{SecretsType: ptr(ml.AccountKeyDatastoreSecrets_SecretsType_AccountKey),
					Key: &genruntime.SecretReference{Name: "lake", Key: "key"}}}}}}}}
	fqdn := &ml.WorkspacesOutboundRule{ObjectMeta: meta("pypi"), Spec: ml.WorkspacesOutboundRule_Spec{Owner: onWS,
		Properties: &ml.OutboundRule{FQDN: &ml.FqdnOutboundRule{Type: ptr(ml.FqdnOutboundRule_Type_FQDN), Destination: ptr("pypi.org"),
			Category: ptr(ml.RuleCategory_UserDefined)}}}}
	endpointRule := func(name, target string) *ml.WorkspacesOutboundRule {
		return &ml.WorkspacesOutboundRule{ObjectMeta: meta(name), Spec: ml.WorkspacesOutboundRule_Spec{Owner: onWS,
			Properties: &ml.OutboundRule{PrivateEndpoint: &ml.PrivateEndpointOutboundRule{Type: ptr(ml.PrivateEndpointOutboundRule_Type_PrivateEndpoint),
				Destination: &ml.PrivateEndpointDestination{ServiceResourceReference: &genruntime.ResourceReference{ARMID: target}, SubresourceTarget: ptr("blob")}}}}}
	}
	lakeRule, goneRule := endpointRule("lake", storageID), endpointRule("gone", storageID+"-gone")
	conn := &ml.WorkspacesPrivateEndpointConnection{ObjectMeta: meta("lab-pe"), Spec: ml.WorkspacesPrivateEndpointConnection_Spec{
		AzureName: "lab.pe", Owner: onWS, PrivateLinkServiceConnectionState: &ml.PrivateLinkServiceConnectionState{
			Status: ptr(ml.EndpointServiceConnectionStatus_Approved), Description: ptr("approved")}}}
	first := []conditioned{model, data, env, code, datastore, fqdn, lakeRule, goneRule, conn}
	create(first...)
	for _, o := range []conditioned{model, data, env, code, datastore, fqdn, lakeRule, conn} {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	eventually(t, timeout, "an endpoint rule to a missing account refused", notReadyWith(goneRule, "was not found"))

	dataVersion := &ml.WorkspacesDataVersion{ObjectMeta: meta("sales-1"), Spec: ml.WorkspacesDataVersion_Spec{AzureName: "1",
		Owner: &genruntime.KnownResourceReference{Name: "sales"}, Properties: &ml.DataVersionBaseProperties{Uri_Folder: &ml.UriFolderDataVersion{
			DataType: ptr(ml.UriFolderDataVersion_DataType_Uri_Folder), DataUri: ptr("azureml://datastores/lake/paths/sales/")}}}}
	envVersion := &ml.WorkspacesEnvironmentsVersion{ObjectMeta: meta("py-1"), Spec: ml.WorkspacesEnvironmentsVersion_Spec{AzureName: "1",
		Owner: &genruntime.KnownResourceReference{Name: "py"}, Properties: &ml.EnvironmentVersionProperties{Image: ptr("mcr.microsoft.com/azureml/openmpi4.1.0-ubuntu22.04")}}}
	emptyEnv := &ml.WorkspacesEnvironmentsVersion{ObjectMeta: meta("py-2"), Spec: ml.WorkspacesEnvironmentsVersion_Spec{AzureName: "2",
		Owner: &genruntime.KnownResourceReference{Name: "py"}, Properties: &ml.EnvironmentVersionProperties{Description: ptr("nothing")}}}
	codeVersion := &ml.RegistriesCodesVersion{ObjectMeta: meta("scoring-1"), Spec: ml.RegistriesCodesVersion_Spec{AzureName: "1",
		Owner: &genruntime.KnownResourceReference{Name: "scoring"}, Properties: &ml.CodeVersionProperties{CodeUri: ptr("https://lake.blob.core.windows.net/code/scoring/")}}}
	second := []conditioned{dataVersion, envVersion, emptyEnv, codeVersion}
	create(second...)
	for _, o := range []conditioned{dataVersion, envVersion, codeVersion} {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	eventually(t, timeout, "an environment of neither image nor build refused", notReadyWith(emptyEnv, "image or a build"))

	modelVersion := &ml.WorkspacesModelsVersion{ObjectMeta: meta("churn-1"), Spec: ml.WorkspacesModelsVersion_Spec{AzureName: "1",
		Owner: &genruntime.KnownResourceReference{Name: "churn"}, Properties: &ml.ModelVersionProperties{
			ModelUri: ptr("azureml://datastores/lake/paths/models/churn/"), ModelType: ptr("mlflow_model"),
			Datasets: []ml.DatasetReference{{Name: ptr("sales"), Reference: &genruntime.ResourceReference{Group: "machinelearningservices.azure.hfl.dev", Kind: "WorkspacesDataVersion", Name: "sales-1"}}}}}}
	create(modelVersion)
	eventually(t, timeout, "the model version Ready", ready(modelVersion))
	expectResource(t, wsID+"/models/churn/versions/1", func(r map[string]any) error {
		ds, _ := properties(r)["datasets"].([]any)
		if len(ds) != 1 || ds[0].(map[string]any)["id"] != wsID+"/data/sales/versions/1" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, wsID+"/datastores/lake", func(r map[string]any) error {
		creds, _ := properties(r)["credentials"].(map[string]any)
		secrets, _ := creds["secrets"].(map[string]any)
		if secrets["key"] != "bGFrZS1rZXk=" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, wsID+"/outboundRules/lake", func(r map[string]any) error {
		dest, _ := properties(r)["destination"].(map[string]any)
		if dest["serviceResourceId"] != storageID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A version is immutable: its URI cannot change.
	if err := change(t, modelVersion, func(v *ml.WorkspacesModelsVersion) {
		v.Spec.Properties.ModelUri = ptr("azureml://datastores/lake/paths/models/churn-2/")
	}); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "a model version's new URI refused", notReadyWith(modelVersion, "immutable"))
	// A container's description is PUT again.
	if err := change(t, model, func(m *ml.WorkspacesModel) { m.Spec.Properties.Description = ptr("churn, retrained monthly") }); err != nil {
		t.Fatal(err)
	}
	expectResource(t, wsID+"/models/churn", func(r map[string]any) error {
		if properties(r)["description"] != "churn, retrained monthly" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, objs := range [][]conditioned{{modelVersion}, second, first} {
		for _, o := range objs {
			if err := k8s.Delete(ctx, o); err != nil {
				t.Fatal(err)
			}
		}
		for _, o := range objs {
			eventually(t, timeout, o.GetName()+" gone", gone(o))
		}
	}
	for _, id := range []string{wsID + "/models/churn", wsID + "/data/sales", wsID + "/datastores/lake", wsID + "/outboundRules/pypi",
		regID + "/codes/scoring", wsID + "/privateEndpointConnections/lab.pe"} {
		if armFake.HasResource(id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
}

// The Machine Learning endpoint kinds of docs/plan.md f83, on ASO's
// Workspace by name: an online endpoint with its keys from a Secret, a
// managed deployment on it, the endpoint's traffic then sent to the
// deployment; a batch endpoint, a deployment and the endpoint's default
// naming it. An endpoint sending traffic to a missing deployment and a
// managed deployment with no instance type are refused (the fake's rules,
// machinelearning.go); the endpoint is fixed by a change. Deleted, all go.
func TestMachineLearningEndpoints(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "mlep")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	wsID := rgID + "/providers/Microsoft.MachineLearningServices/workspaces/lab"
	for _, id := range []string{rgID, wsID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	ws := &asoml.Workspace{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "lab"}}
	ws.Spec.AzureName, ws.Spec.Location = "lab", ptr("westus")
	ws.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, ws, wsID)
	if err := k8s.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "score-keys"},
		StringData: map[string]string{"primary": "cHJpbWFyeQ==", "secondary": "c2Vjb25kYXJ5"},
	}); err != nil {
		t.Fatal(err)
	}
	meta := func(name string) metav1.ObjectMeta { return metav1.ObjectMeta{Namespace: ns, Name: name} }
	onWS := &genruntime.KnownResourceReference{Name: "lab"}
	create := func(objs ...conditioned) {
		t.Helper()
		for _, o := range objs {
			if err := k8s.Create(ctx, o); err != nil {
				t.Fatal(err)
			}
		}
	}

	online := &ml.WorkspacesOnlineEndpoint{ObjectMeta: meta("score"), Spec: ml.WorkspacesOnlineEndpoint_Spec{Owner: onWS, Location: ptr("westus"),
		Properties: &ml.OnlineEndpointProperties{AuthMode: ptr(ml.EndpointAuthMode_Key), Traffic: map[string]int{"blue": 100},
			Keys: &ml.EndpointAuthKeys{PrimaryKey: &genruntime.SecretReference{Name: "score-keys", Key: "primary"},
				SecondaryKey: &genruntime.SecretReference{Name: "score-keys", Key: "secondary"}}}}}
	batch := &ml.WorkspacesBatchEndpoint{ObjectMeta: meta("nightly"), Spec: ml.WorkspacesBatchEndpoint_Spec{Owner: onWS, Location: ptr("westus"),
		Properties: &ml.BatchEndpointProperties{AuthMode: ptr(ml.EndpointAuthMode_AADToken)}}}
	create(online, batch)
	eventually(t, timeout, "the batch endpoint Ready", ready(batch))
	eventually(t, timeout, "traffic to a missing deployment refused", notReadyWith(online, "does not exist"))
	if err := change(t, online, func(e *ml.WorkspacesOnlineEndpoint) { e.Spec.Properties.Traffic = nil }); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the online endpoint Ready", ready(online))

	deployment := func(name, instanceType string) *ml.WorkspacesOnlineEndpointsDeployment {
		d := &ml.WorkspacesOnlineEndpointsDeployment{ObjectMeta: meta(name), Spec: ml.WorkspacesOnlineEndpointsDeployment_Spec{
			AzureName: name, Owner: &genruntime.KnownResourceReference{Name: "score"}, Location: ptr("westus"), Sku: &ml.Sku{Name: ptr("Default"), Capacity: ptr(1)},
			Properties: &ml.OnlineDeploymentProperties{Managed: &ml.ManagedOnlineDeployment{
				EndpointComputeType: ptr(ml.ManagedOnlineDeployment_EndpointComputeType_Managed), Model: ptr("azureml:churn:1"),
				EnvironmentId:     ptr("azureml://registries/azureml/environments/sklearn-1.5/versions/1"),
				CodeConfiguration: &ml.CodeConfiguration{CodeId: ptr("azureml:scoring:1"), ScoringScript: ptr("score.py")}}}}}
		if instanceType != "" {
			d.Spec.Properties.Managed.InstanceType = ptr(instanceType)
		}
		return d
	}
	blue, green := deployment("blue", "Standard_DS3_v2"), deployment("green", "")
	batchDeployment := &ml.WorkspacesBatchEndpointsDeployment{ObjectMeta: meta("v1"), Spec: ml.WorkspacesBatchEndpointsDeployment_Spec{
		Owner: &genruntime.KnownResourceReference{Name: "nightly"}, Location: ptr("westus"),
		Properties: &ml.BatchDeploymentProperties{Compute: ptr("cpu-cluster"), MiniBatchSize: ptr(10),
			Model: &ml.AssetReferenceBase{Id: &ml.IdAssetReference{ReferenceType: ptr(ml.IdAssetReference_ReferenceType_Id), AssetId: ptr("azureml:churn:1")}}}}}
	create(blue, green, batchDeployment)
	eventually(t, timeout, "the blue deployment Ready", ready(blue))
	eventually(t, timeout, "the batch deployment Ready", ready(batchDeployment))
	eventually(t, timeout, "a managed deployment with no instance type refused", notReadyWith(green, "instanceType"))

	// The endpoints now name their deployments.
	if err := change(t, online, func(e *ml.WorkspacesOnlineEndpoint) { e.Spec.Properties.Traffic = map[string]int{"blue": 100} }); err != nil {
		t.Fatal(err)
	}
	if err := change(t, batch, func(e *ml.WorkspacesBatchEndpoint) {
		e.Spec.Properties.Defaults = &ml.BatchEndpointDefaults{DeploymentName: ptr("v1")}
	}); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the online endpoint Ready after the change", ready(online))
	eventually(t, timeout, "the batch endpoint Ready after the change", ready(batch))
	expectResource(t, wsID+"/onlineEndpoints/score", func(r map[string]any) error {
		p := properties(r)
		traffic, _ := p["traffic"].(map[string]any)
		keys, _ := p["keys"].(map[string]any)
		if fmt.Sprint(traffic["blue"]) != "100" || keys["primaryKey"] != "cHJpbWFyeQ==" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, wsID+"/onlineEndpoints/score/deployments/blue", func(r map[string]any) error {
		if p := properties(r); p["environmentId"] != "azureml://registries/azureml/environments/sklearn-1.5/versions/1" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// The endpoints stop naming the deployments before they go.
	if err := change(t, online, func(e *ml.WorkspacesOnlineEndpoint) { e.Spec.Properties.Traffic = nil }); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the online endpoint Ready with no traffic", ready(online))
	for _, objs := range [][]conditioned{{blue, green, batchDeployment}, {online, batch}} {
		for _, o := range objs {
			if err := k8s.Delete(ctx, o); err != nil {
				t.Fatal(err)
			}
		}
		for _, o := range objs {
			eventually(t, timeout, o.GetName()+" gone", gone(o))
		}
	}
	for _, id := range []string{wsID + "/onlineEndpoints/score", wsID + "/batchEndpoints/nightly", wsID + "/onlineEndpoints/score/deployments/blue"} {
		if armFake.HasResource(id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
}

// The Machine Learning jobs and schedules of docs/plan.md f85, on ASO's
// Workspace by name: a command job on a compute by ARM ID; its description
// changed and PUT again; its command changed, refused (a job runs once:
// the fake's rule, machinelearning.go), then changed back; a job on a
// missing compute refused. A schedule whose cron expression has four
// fields refused, then fixed by a change; a disabled daily recurrence
// creating a command job. Deleted, all go.
func TestMachineLearningJobs(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "mljob")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	wsID := rgID + "/providers/Microsoft.MachineLearningServices/workspaces/lab"
	computeID := wsID + "/computes/cpu"
	for _, id := range []string{rgID, wsID, computeID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	ws := &asoml.Workspace{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "lab"}}
	ws.Spec.AzureName, ws.Spec.Location = "lab", ptr("westus")
	ws.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, ws, wsID)
	meta := func(name string) metav1.ObjectMeta { return metav1.ObjectMeta{Namespace: ns, Name: name} }
	onWS := &genruntime.KnownResourceReference{Name: "lab"}

	command := func(compute string) *ml.JobBaseProperties {
		return &ml.JobBaseProperties{Command: &ml.CommandJob{JobType: ptr(ml.CommandJob_JobType_Command),
			Command:          ptr("python train.py"),
			EnvironmentId:    ptr("azureml://registries/azureml/environments/sklearn-1.5/versions/1"),
			ComputeReference: &genruntime.ResourceReference{ARMID: compute},
			ExperimentName:   ptr("churn"), Description: ptr("first")}}
	}
	train := &ml.WorkspacesJob{ObjectMeta: meta("train"), Spec: ml.WorkspacesJob_Spec{Owner: onWS, Properties: command(computeID)}}
	lost := &ml.WorkspacesJob{ObjectMeta: meta("lost"), Spec: ml.WorkspacesJob_Spec{Owner: onWS, Properties: command(computeID + "x")}}
	cron := &ml.WorkspacesSchedule{ObjectMeta: meta("hourly"), Spec: ml.WorkspacesSchedule_Spec{Owner: onWS, Properties: &ml.ScheduleProperties{
		Action: &ml.ScheduleActionBase{CreateJob: &ml.JobScheduleAction{ActionType: ptr(ml.JobScheduleAction_ActionType_CreateJob),
			JobDefinition: command(computeID)}},
		Trigger: &ml.TriggerBase{Cron: &ml.CronTrigger{TriggerType: ptr(ml.CronTrigger_TriggerType_Cron), Expression: ptr("0 * * *")}}}}}
	daily := &ml.WorkspacesSchedule{ObjectMeta: meta("daily"), Spec: ml.WorkspacesSchedule_Spec{Owner: onWS, Properties: &ml.ScheduleProperties{
		IsEnabled: ptr(false), DisplayName: ptr("nightly training"),
		Action: &ml.ScheduleActionBase{CreateJob: &ml.JobScheduleAction{ActionType: ptr(ml.JobScheduleAction_ActionType_CreateJob),
			JobDefinition: command(computeID)}},
		Trigger: &ml.TriggerBase{Recurrence: &ml.RecurrenceTrigger{TriggerType: ptr(ml.RecurrenceTrigger_TriggerType_Recurrence),
			Frequency: ptr(ml.RecurrenceFrequency_Day), Interval: ptr(1), TimeZone: ptr("UTC"),
			Schedule: &ml.RecurrenceSchedule{Hours: []int{2}, Minutes: []int{30}}}}}}}
	for _, o := range []conditioned{train, lost, cron, daily} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "the job Ready", ready(train))
	eventually(t, timeout, "the daily schedule Ready", ready(daily))
	eventually(t, timeout, "a job on a missing compute refused", notReadyWith(lost, "was not found"))
	eventually(t, timeout, "a cron of four fields refused", notReadyWith(cron, "five fields"))
	expectResource(t, wsID+"/jobs/train", func(r map[string]any) error {
		p := properties(r)
		if p["jobType"] != "Command" || p["command"] != "python train.py" || p["computeId"] != computeID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, wsID+"/schedules/daily", func(r map[string]any) error {
		p := properties(r)
		action, _ := p["action"].(map[string]any)
		job, _ := action["jobDefinition"].(map[string]any)
		trigger, _ := p["trigger"].(map[string]any)
		if p["isEnabled"] != false || job["command"] != "python train.py" || trigger["frequency"] != "Day" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// The cron is fixed; the job's description may change, its command may not.
	if err := change(t, cron, func(s *ml.WorkspacesSchedule) { s.Spec.Properties.Trigger.Cron.Expression = ptr("0 * * * *") }); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the cron schedule Ready after the change", ready(cron))
	if err := change(t, train, func(j *ml.WorkspacesJob) { j.Spec.Properties.Command.Description = ptr("first, kept") }); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the job's description PUT again", func() error {
		r, ok := armFake.Resource(wsID + "/jobs/train")
		if !ok || properties(r)["description"] != "first, kept" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if err := change(t, train, func(j *ml.WorkspacesJob) { j.Spec.Properties.Command.Command = ptr("python retrain.py") }); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "a job's command change refused", notReadyWith(train, "runs once"))
	if err := change(t, train, func(j *ml.WorkspacesJob) { j.Spec.Properties.Command.Command = ptr("python train.py") }); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the job Ready again", ready(train))

	for _, o := range []conditioned{train, lost, cron, daily} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []conditioned{train, lost, cron, daily} {
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, id := range []string{wsID + "/jobs/train", wsID + "/schedules/hourly", wsID + "/schedules/daily"} {
		if armFake.HasResource(id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
}
