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
