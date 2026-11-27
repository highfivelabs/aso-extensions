// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

// The Data Factory authoring kinds of docs/plan.md f86, on ASO's Factory by
// name: a schedule trigger running a pipeline; a mapping data flow from a
// dataset to a linked service; a managed integration runtime in the
// factory's managed network, with an SSIS catalog whose admin password
// comes from a Secret and stays out of the status; a self-hosted runtime;
// a change data capture from a linked service. The pipeline, dataset,
// linked service and managed network are in the fake (no kind makes the
// first three). A trigger naming a missing pipeline is refused (Data
// Factory's "invalid reference", the fake's rule, datafactory.go), then
// fixed by a change. Deleted, all go.
func TestDataFactoryAuthoring(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "adfa")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	factoryID := rgID + "/providers/Microsoft.DataFactory/factories/etl"
	for _, id := range []string{rgID, factoryID, factoryID + "/pipelines/copy", factoryID + "/datasets/raw",
		factoryID + "/linkedservices/lake", factoryID + "/managedVirtualNetworks/default"} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	factory := &asodatafactory.Factory{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "etl"}}
	factory.Spec.AzureName, factory.Spec.Location = "etl", ptr("westus")
	factory.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, factory, factoryID)
	onFactory := &genruntime.KnownResourceReference{Name: "etl"}
	meta := func(name string) metav1.ObjectMeta { return metav1.ObjectMeta{Namespace: ns, Name: name} }
	if err := k8s.Create(ctx, &corev1.Secret{ObjectMeta: meta("ssis"), StringData: map[string]string{"password": "c3Npcy1wYXNz"}}); err != nil {
		t.Fatal(err)
	}

	trigger := &datafactory.FactoriesTrigger{ObjectMeta: meta("nightly"), Spec: datafactory.FactoriesTrigger_Spec{Owner: onFactory,
		Properties: &datafactory.Trigger{Schedule: &datafactory.ScheduleTrigger{Type: ptr(datafactory.ScheduleTrigger_Type_ScheduleTrigger),
			Recurrence: &datafactory.ScheduleTriggerRecurrence{Frequency: ptr(datafactory.RecurrenceFrequency_Day), Interval: ptr(1)},
			Pipelines: []datafactory.TriggerPipelineReference{{PipelineReference: &datafactory.PipelineReference{
				Type: ptr(datafactory.PipelineReferenceType_PipelineReference), ReferenceName: ptr("train")}}}}}}}
	flow := &datafactory.FactoriesDataflow{ObjectMeta: meta("clean"), Spec: datafactory.FactoriesDataflow_Spec{Owner: onFactory,
		Properties: &datafactory.DataFlow{MappingDataFlow: &datafactory.MappingDataFlow{Type: ptr(datafactory.MappingDataFlow_Type_MappingDataFlow),
			Sources: []datafactory.DataFlowSource{{Name: ptr("src"), Dataset: &datafactory.DatasetReference{
				Type: ptr(datafactory.DatasetReferenceType_DatasetReference), ReferenceName: ptr("raw")}}},
			Sinks: []datafactory.DataFlowSink{{Name: ptr("out"), LinkedService: &datafactory.LinkedServiceReference{
				Type: ptr(datafactory.Type_LinkedServiceReference), ReferenceName: ptr("lake")}}},
			Script: ptr("source(output(id as integer)) ~> src\nsrc sink() ~> out")}}}}
	managed := &datafactory.FactoriesIntegrationRuntime{ObjectMeta: meta("ssis-runtime"), Spec: datafactory.FactoriesIntegrationRuntime_Spec{Owner: onFactory,
		Properties: &datafactory.IntegrationRuntime{Managed: &datafactory.ManagedIntegrationRuntime{Type: ptr(datafactory.ManagedIntegrationRuntime_Type_Managed),
			ManagedVirtualNetwork: &datafactory.ManagedVirtualNetworkReference{
				Type: ptr(datafactory.ManagedVirtualNetworkReferenceType_ManagedVirtualNetworkReference), ReferenceName: ptr("default")},
			ComputeProperties: &datafactory.IntegrationRuntimeComputeProperties{Location: ptr("AutoResolve")},
			SsisProperties: &datafactory.IntegrationRuntimeSsisProperties{CatalogInfo: &datafactory.IntegrationRuntimeSsisCatalogInfo{
				CatalogServerEndpoint: ptr("etl.database.windows.net"), CatalogAdminUserName: ptr("ssisadmin"),
				CatalogAdminPassword: &datafactory.SecureString{Type: ptr(datafactory.SecureString_Type_SecureString),
					Value: &genruntime.SecretReference{Name: "ssis", Key: "password"}}}}}}}}
	selfHosted := &datafactory.FactoriesIntegrationRuntime{ObjectMeta: meta("onprem"), Spec: datafactory.FactoriesIntegrationRuntime_Spec{Owner: onFactory,
		Properties: &datafactory.IntegrationRuntime{SelfHosted: &datafactory.SelfHostedIntegrationRuntime{
			Type: ptr(datafactory.SelfHostedIntegrationRuntime_Type_SelfHosted), Description: ptr("the datacentre")}}}}
	cdc := &datafactory.FactoriesAdfcdc{ObjectMeta: meta("orders"), Spec: datafactory.FactoriesAdfcdc_Spec{Owner: onFactory,
		Policy: &datafactory.MapperPolicy{Mode: ptr("Microbatch")},
		SourceConnectionsInfo: []datafactory.MapperSourceConnectionsInfo{{Connection: &datafactory.MapperConnection{
			Type:          ptr(datafactory.ConnectionType_Linkedservicetype),
			LinkedService: &datafactory.LinkedServiceReference{Type: ptr(datafactory.Type_LinkedServiceReference), ReferenceName: ptr("lake")}}}},
		TargetConnectionsInfo: []datafactory.MapperTargetConnectionsInfo{{Connection: &datafactory.MapperConnection{
			Type:          ptr(datafactory.ConnectionType_Linkedservicetype),
			LinkedService: &datafactory.LinkedServiceReference{Type: ptr(datafactory.Type_LinkedServiceReference), ReferenceName: ptr("lake")}}}}}}
	objs := []conditioned{trigger, flow, managed, selfHosted, cdc}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs[1:] {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}
	eventually(t, timeout, "a trigger of a missing pipeline refused", notReadyWith(trigger, "invalid reference"))
	if err := change(t, trigger, func(tr *datafactory.FactoriesTrigger) {
		tr.Spec.Properties.Schedule.Pipelines[0].PipelineReference.ReferenceName = ptr("copy")
	}); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the trigger Ready after the change", ready(trigger))

	expectResource(t, factoryID+"/integrationRuntimes/ssis-runtime", func(r map[string]any) error {
		tp, _ := properties(r)["typeProperties"].(map[string]any)
		ssis, _ := tp["ssisProperties"].(map[string]any)
		catalog, _ := ssis["catalogInfo"].(map[string]any)
		password, _ := catalog["catalogAdminPassword"].(map[string]any)
		if password["value"] != "c3Npcy1wYXNz" || password["type"] != "SecureString" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	eventually(t, timeout, "the password out of the status", func() error {
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(managed), managed); err != nil {
			return err
		}
		if strings.Contains(fmt.Sprintf("%+v", managed.Status), "c3Npcy1wYXNz") {
			return fmt.Errorf("the status holds the password: %+v", managed.Status)
		}
		return nil
	})
	expectResource(t, factoryID+"/triggers/nightly", func(r map[string]any) error {
		tp, _ := properties(r)["pipelines"].([]any)
		if len(tp) != 1 || !strings.Contains(fmt.Sprint(tp[0]), "copy") {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, factoryID+"/adfcdcs/orders", func(r map[string]any) error {
		if !strings.Contains(fmt.Sprint(properties(r)["sourceConnectionsInfo"]), "lake") {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range objs {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, id := range []string{"/triggers/nightly", "/dataflows/clean", "/integrationRuntimes/ssis-runtime", "/integrationRuntimes/onprem", "/adfcdcs/orders"} {
		if armFake.HasResource(factoryID + id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
}

// The linked service kinds, one kind per linked service type (docs/plan.md
// section 2, "One kind per member of a union"), on ASO's factory by name:
// Key Vault; Blob Storage with its connection string from a Secret; Data
// Lake Gen2 with its account key from a Secret; SQL Database with its
// connection string from the Key Vault linked service; Synapse (a dedicated
// pool, AzureSqlDW, and the workspace's artifacts); Cosmos DB with its key
// from a Secret; REST; HTTP with its password from a Secret; Databricks
// with its token from a Secret. Each secret reaches the fake as a secure
// string and is in no status (the fake masks what it returns, as Data
// Factory does). An HTTP service whose runtime is missing is refused (the
// fake's "invalid reference"), then fixed by a change. Deleted, all go.
func TestDataFactoryLinkedServices(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "adfls")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	factoryID := rgID + "/providers/Microsoft.DataFactory/factories/etl"
	for _, id := range []string{rgID, factoryID, factoryID + "/integrationRuntimes/onprem"} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	factory := &asodatafactory.Factory{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "etl"}}
	factory.Spec.AzureName, factory.Spec.Location = "etl", ptr("westus")
	factory.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, factory, factoryID)
	onFactory := &genruntime.KnownResourceReference{Name: "etl"}
	meta := func(name string) metav1.ObjectMeta { return metav1.ObjectMeta{Namespace: ns, Name: name} }
	secrets := map[string]string{
		"blob": "DefaultEndpointsProtocol=https;AccountName=lake;AccountKey=YmxvYi1rZXk=", "adls": "YWRscy1rZXk=",
		"dw": "Server=tcp:etl.sql.azuresynapse.net;Database=dw;Password=ZHctcGFzcw==", "cosmos": "Y29zbW9zLWtleQ==",
		"http": "aHR0cC1wYXNz", "databricks": "ZGF0YWJyaWNrcy10b2tlbg==",
	}
	if err := k8s.Create(ctx, &corev1.Secret{ObjectMeta: meta("adf"), StringData: secrets}); err != nil {
		t.Fatal(err)
	}
	js := func(s string) *v1.JSON { return &v1.JSON{Raw: []byte(s)} }
	secure := func(key string) *datafactory.SecretBase {
		return &datafactory.SecretBase{SecureString: &datafactory.SecureString{Type: ptr(datafactory.SecureString_Type_SecureString),
			Value: &genruntime.SecretReference{Name: "adf", Key: key}}}
	}

	vault := &datafactory.FactoriesLinkedServiceAzureKeyVault{ObjectMeta: meta("vault"), Spec: datafactory.FactoriesLinkedServiceAzureKeyVault_Spec{
		Owner: onFactory, Properties: &datafactory.AzureKeyVaultLinkedService{Type: ptr(datafactory.AzureKeyVaultLinkedService_Type_AzureKeyVault),
			BaseUrl: js(`"https://etl.vault.azure.net/"`)}}}
	blob := &datafactory.FactoriesLinkedServiceAzureBlobStorage{ObjectMeta: meta("blob"), Spec: datafactory.FactoriesLinkedServiceAzureBlobStorage_Spec{
		Owner: onFactory, Properties: &datafactory.AzureBlobStorageLinkedService{Type: ptr(datafactory.AzureBlobStorageLinkedService_Type_AzureBlobStorage),
			ConnectionString: secure("blob")}}}
	adls := &datafactory.FactoriesLinkedServiceAzureBlobFS{ObjectMeta: meta("adls"), Spec: datafactory.FactoriesLinkedServiceAzureBlobFS_Spec{
		Owner: onFactory, Properties: &datafactory.AzureBlobFSLinkedService{Type: ptr(datafactory.AzureBlobFSLinkedService_Type_AzureBlobFS),
			Url: js(`"https://lake.dfs.core.windows.net"`), AccountKey: secure("adls")}}}
	sql := &datafactory.FactoriesLinkedServiceAzureSqlDatabase{ObjectMeta: meta("sql"), Spec: datafactory.FactoriesLinkedServiceAzureSqlDatabase_Spec{
		Owner: onFactory, Properties: &datafactory.AzureSqlDatabaseLinkedService{Type: ptr(datafactory.AzureSqlDatabaseLinkedService_Type_AzureSqlDatabase),
			ConnectionString: &datafactory.SecretBase{AzureKeyVaultSecret: &datafactory.AzureKeyVaultSecretReference{
				Type:       ptr(datafactory.AzureKeyVaultSecretReference_Type_AzureKeyVaultSecret),
				Store:      &datafactory.LinkedServiceReference{Type: ptr(datafactory.Type_LinkedServiceReference), ReferenceName: ptr("vault")},
				SecretName: js(`"sql-connection"`)}}}}}
	dw := &datafactory.FactoriesLinkedServiceAzureSqlDW{ObjectMeta: meta("dw"), Spec: datafactory.FactoriesLinkedServiceAzureSqlDW_Spec{
		Owner: onFactory, Properties: &datafactory.AzureSqlDWLinkedService{Type: ptr(datafactory.AzureSqlDWLinkedService_Type_AzureSqlDW),
			ConnectionString: secure("dw")}}}
	synapse := &datafactory.FactoriesLinkedServiceAzureSynapseArtifacts{ObjectMeta: meta("synapse"), Spec: datafactory.FactoriesLinkedServiceAzureSynapseArtifacts_Spec{
		Owner: onFactory, Properties: &datafactory.AzureSynapseArtifactsLinkedService{Type: ptr(datafactory.AzureSynapseArtifactsLinkedService_Type_AzureSynapseArtifacts),
			Endpoint: js(`"https://etl.dev.azuresynapse.net"`), Authentication: js(`"MSI"`)}}}
	cosmos := &datafactory.FactoriesLinkedServiceCosmosDb{ObjectMeta: meta("cosmos"), Spec: datafactory.FactoriesLinkedServiceCosmosDb_Spec{
		Owner: onFactory, Properties: &datafactory.CosmosDbLinkedService{Type: ptr(datafactory.CosmosDbLinkedService_Type_CosmosDb),
			AccountEndpoint: js(`"https://etl.documents.azure.com:443/"`), Database: js(`"orders"`), AccountKey: secure("cosmos")}}}
	rest := &datafactory.FactoriesLinkedServiceRestService{ObjectMeta: meta("rest"), Spec: datafactory.FactoriesLinkedServiceRestService_Spec{
		Owner: onFactory, Properties: &datafactory.RestServiceLinkedService{Type: ptr(datafactory.RestServiceLinkedService_Type_RestService),
			Url: js(`"https://api.example.com/v1"`), AuthenticationType: ptr(datafactory.RestServiceAuthenticationType_Anonymous)}}}
	http := &datafactory.FactoriesLinkedServiceHttpServer{ObjectMeta: meta("files"), Spec: datafactory.FactoriesLinkedServiceHttpServer_Spec{
		Owner: onFactory, Properties: &datafactory.HttpLinkedService{Type: ptr(datafactory.HttpLinkedService_Type_HttpServer),
			Url: js(`"https://files.example.com"`), AuthenticationType: ptr(datafactory.HttpAuthenticationType_Basic),
			UserName: js(`"etl"`), Password: secure("http"),
			ConnectVia: &datafactory.IntegrationRuntimeReference{
				Type: ptr(datafactory.IntegrationRuntimeReferenceType_IntegrationRuntimeReference), ReferenceName: ptr("datacentre")}}}}
	databricks := &datafactory.FactoriesLinkedServiceAzureDatabricks{ObjectMeta: meta("databricks"), Spec: datafactory.FactoriesLinkedServiceAzureDatabricks_Spec{
		Owner: onFactory, Properties: &datafactory.AzureDatabricksLinkedService{Type: ptr(datafactory.AzureDatabricksLinkedService_Type_AzureDatabricks),
			Domain: js(`"https://adb-1.azuredatabricks.net"`), AccessToken: secure("databricks"), ExistingClusterId: js(`"0101-etl"`)}}}
	objs := []conditioned{vault, blob, adls, sql, dw, synapse, cosmos, rest, http, databricks}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		if o != http {
			eventually(t, timeout, o.GetName()+" Ready", ready(o))
		}
	}
	eventually(t, timeout, "an HTTP service of a missing runtime refused", notReadyWith(http, "invalid reference"))
	if err := change(t, http, func(l *datafactory.FactoriesLinkedServiceHttpServer) {
		l.Spec.Properties.ConnectVia.ReferenceName = ptr("onprem")
	}); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the HTTP service Ready after the change", ready(http))

	// Each secret is a secure string in Azure, and in no status.
	for name, field := range map[string][2]string{ // the linked service: its field and the Secret's key
		"blob": {"connectionString", "blob"}, "adls": {"accountKey", "adls"}, "dw": {"connectionString", "dw"},
		"cosmos": {"accountKey", "cosmos"}, "files": {"password", "http"}, "databricks": {"accessToken", "databricks"},
	} {
		expectResource(t, factoryID+"/linkedservices/"+name, func(r map[string]any) error {
			tp, _ := properties(r)["typeProperties"].(map[string]any)
			s, _ := tp[field[0]].(map[string]any)
			if s["type"] != "SecureString" || s["value"] != secrets[field[1]] {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})
	}
	expectResource(t, factoryID+"/linkedservices/sql", func(r map[string]any) error {
		tp, _ := properties(r)["typeProperties"].(map[string]any)
		cs := fmt.Sprint(tp["connectionString"])
		if !strings.Contains(cs, "AzureKeyVaultSecret") || !strings.Contains(cs, "vault") || !strings.Contains(cs, "sql-connection") {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, factoryID+"/linkedservices/files", func(r map[string]any) error {
		if !strings.Contains(fmt.Sprint(properties(r)["connectVia"]), "onprem") {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	eventually(t, timeout, "the secrets out of every status", func() error {
		for _, o := range objs {
			if err := k8s.Get(ctx, client.ObjectKeyFromObject(o), o); err != nil {
				return err
			}
			b, err := json.Marshal(o)
			if err != nil {
				return err
			}
			for _, v := range secrets {
				if strings.Contains(string(b), v) {
					return fmt.Errorf("%s holds a secret: %s", o.GetName(), b)
				}
			}
		}
		return nil
	})
	eventually(t, timeout, "the Key Vault URL in its status", func() error {
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(vault), vault); err != nil {
			return err
		}
		if p := vault.Status.Properties; p == nil || p.BaseUrl == nil || !strings.Contains(string(p.BaseUrl.Raw), "etl.vault") {
			return fmt.Errorf("status: %+v", vault.Status)
		}
		return nil
	})

	for _, o := range objs {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, name := range []string{"vault", "blob", "adls", "sql", "dw", "synapse", "cosmos", "rest", "files", "databricks"} {
		if armFake.HasResource(factoryID + "/linkedservices/" + name) {
			t.Errorf("linked service %s is still in the fake", name)
		}
	}
}

// The dataset kinds, one kind per dataset type (docs/plan.md f88), on ASO's
// factory by name, over the linked service kinds of f87: delimited text,
// Parquet, JSON, Avro, ORC, binary, Excel and XML files in a Data Lake Gen2
// file system; a SQL Database table, a Synapse dedicated pool table, a
// Cosmos DB collection, a REST resource and an HTTP file. A table dataset
// naming a linked service of another type is refused (the fake's rule),
// then fixed by a change; the location, the table and the delimiter reach
// the fake as Data Factory's JSON has them. Deleted, all go.
func TestDataFactoryDatasets(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "adfds")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	factoryID := rgID + "/providers/Microsoft.DataFactory/factories/etl"
	for _, id := range []string{rgID, factoryID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	factory := &asodatafactory.Factory{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "etl"}}
	factory.Spec.AzureName, factory.Spec.Location = "etl", ptr("westus")
	factory.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, factory, factoryID)
	onFactory := &genruntime.KnownResourceReference{Name: "etl"}
	meta := func(name string) metav1.ObjectMeta { return metav1.ObjectMeta{Namespace: ns, Name: name} }
	js := func(s string) *v1.JSON { return &v1.JSON{Raw: []byte(s)} }
	ls := func(name string) *datafactory.LinkedServiceReference {
		return &datafactory.LinkedServiceReference{Type: ptr(datafactory.Type_LinkedServiceReference), ReferenceName: ptr(name)}
	}

	// The linked services the datasets read (f87's kinds; no secrets needed here).
	stores := []conditioned{
		&datafactory.FactoriesLinkedServiceAzureBlobFS{ObjectMeta: meta("lake"), Spec: datafactory.FactoriesLinkedServiceAzureBlobFS_Spec{
			Owner: onFactory, Properties: &datafactory.AzureBlobFSLinkedService{Type: ptr(datafactory.AzureBlobFSLinkedService_Type_AzureBlobFS),
				Url: js(`"https://lake.dfs.core.windows.net"`)}}},
		&datafactory.FactoriesLinkedServiceAzureSqlDatabase{ObjectMeta: meta("sql"), Spec: datafactory.FactoriesLinkedServiceAzureSqlDatabase_Spec{
			Owner: onFactory, Properties: &datafactory.AzureSqlDatabaseLinkedService{Type: ptr(datafactory.AzureSqlDatabaseLinkedService_Type_AzureSqlDatabase),
				Server: js(`"etl.database.windows.net"`), Database: js(`"orders"`)}}},
		&datafactory.FactoriesLinkedServiceAzureSqlDW{ObjectMeta: meta("dw"), Spec: datafactory.FactoriesLinkedServiceAzureSqlDW_Spec{
			Owner: onFactory, Properties: &datafactory.AzureSqlDWLinkedService{Type: ptr(datafactory.AzureSqlDWLinkedService_Type_AzureSqlDW),
				Server: js(`"etl.sql.azuresynapse.net"`), Database: js(`"dw"`)}}},
		&datafactory.FactoriesLinkedServiceCosmosDb{ObjectMeta: meta("cosmos"), Spec: datafactory.FactoriesLinkedServiceCosmosDb_Spec{
			Owner: onFactory, Properties: &datafactory.CosmosDbLinkedService{Type: ptr(datafactory.CosmosDbLinkedService_Type_CosmosDb),
				AccountEndpoint: js(`"https://etl.documents.azure.com:443/"`), Database: js(`"orders"`)}}},
		&datafactory.FactoriesLinkedServiceRestService{ObjectMeta: meta("rest"), Spec: datafactory.FactoriesLinkedServiceRestService_Spec{
			Owner: onFactory, Properties: &datafactory.RestServiceLinkedService{Type: ptr(datafactory.RestServiceLinkedService_Type_RestService),
				Url: js(`"https://api.example.com/v1"`), AuthenticationType: ptr(datafactory.RestServiceAuthenticationType_Anonymous)}}},
		&datafactory.FactoriesLinkedServiceHttpServer{ObjectMeta: meta("files"), Spec: datafactory.FactoriesLinkedServiceHttpServer_Spec{
			Owner: onFactory, Properties: &datafactory.HttpLinkedService{Type: ptr(datafactory.HttpLinkedService_Type_HttpServer),
				Url: js(`"https://files.example.com"`)}}},
	}
	for _, o := range stores {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range stores {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}

	inLake := func(file string) *datafactory.DatasetLocation {
		return &datafactory.DatasetLocation{AzureBlobFS: &datafactory.AzureBlobFSLocation{
			Type:       ptr(datafactory.AzureBlobFSLocation_Type_AzureBlobFSLocation),
			FileSystem: js(`"raw"`), FolderPath: js(`"orders"`), FileName: js(`"` + file + `"`)}}
	}
	csv := &datafactory.FactoriesDatasetDelimitedText{ObjectMeta: meta("csv"), Spec: datafactory.FactoriesDatasetDelimitedText_Spec{
		Owner: onFactory, Properties: &datafactory.DelimitedTextDataset{Type: ptr(datafactory.DelimitedTextDataset_Type_DelimitedText),
			LinkedServiceName: ls("lake"), Location: inLake("orders.csv"), ColumnDelimiter: js(`","`), FirstRowAsHeader: js(`true`)}}}
	parquet := &datafactory.FactoriesDatasetParquet{ObjectMeta: meta("parquet"), Spec: datafactory.FactoriesDatasetParquet_Spec{
		Owner: onFactory, Properties: &datafactory.ParquetDataset{Type: ptr(datafactory.ParquetDataset_Type_Parquet),
			LinkedServiceName: ls("lake"), Location: inLake("orders.parquet"), CompressionCodec: js(`"snappy"`)}}}
	jsonFile := &datafactory.FactoriesDatasetJson{ObjectMeta: meta("json"), Spec: datafactory.FactoriesDatasetJson_Spec{
		Owner: onFactory, Properties: &datafactory.JsonDataset{Type: ptr(datafactory.JsonDataset_Type_Json),
			LinkedServiceName: ls("lake"), Location: inLake("orders.json")}}}
	avro := &datafactory.FactoriesDatasetAvro{ObjectMeta: meta("avro"), Spec: datafactory.FactoriesDatasetAvro_Spec{
		Owner: onFactory, Properties: &datafactory.AvroDataset{Type: ptr(datafactory.AvroDataset_Type_Avro),
			LinkedServiceName: ls("lake"), Location: inLake("orders.avro")}}}
	orc := &datafactory.FactoriesDatasetOrc{ObjectMeta: meta("orc"), Spec: datafactory.FactoriesDatasetOrc_Spec{
		Owner: onFactory, Properties: &datafactory.OrcDataset{Type: ptr(datafactory.OrcDataset_Type_Orc),
			LinkedServiceName: ls("lake"), Location: inLake("orders.orc")}}}
	binary := &datafactory.FactoriesDatasetBinary{ObjectMeta: meta("binary"), Spec: datafactory.FactoriesDatasetBinary_Spec{
		Owner: onFactory, Properties: &datafactory.BinaryDataset{Type: ptr(datafactory.BinaryDataset_Type_Binary),
			LinkedServiceName: ls("lake"), Location: inLake("orders.bin")}}}
	excel := &datafactory.FactoriesDatasetExcel{ObjectMeta: meta("excel"), Spec: datafactory.FactoriesDatasetExcel_Spec{
		Owner: onFactory, Properties: &datafactory.ExcelDataset{Type: ptr(datafactory.ExcelDataset_Type_Excel),
			LinkedServiceName: ls("lake"), Location: inLake("orders.xlsx"), SheetName: js(`"Sheet1"`)}}}
	xml := &datafactory.FactoriesDatasetXml{ObjectMeta: meta("xml"), Spec: datafactory.FactoriesDatasetXml_Spec{
		Owner: onFactory, Properties: &datafactory.XmlDataset{Type: ptr(datafactory.XmlDataset_Type_Xml),
			LinkedServiceName: ls("lake"), Location: inLake("orders.xml")}}}
	table := &datafactory.FactoriesDatasetAzureSqlTable{ObjectMeta: meta("table"), Spec: datafactory.FactoriesDatasetAzureSqlTable_Spec{
		Owner: onFactory, Properties: &datafactory.AzureSqlTableDataset{Type: ptr(datafactory.AzureSqlTableDataset_Type_AzureSqlTable),
			LinkedServiceName: ls("lake"), TypePropertiesSchema: js(`"dbo"`), Table: js(`"orders"`)}}}
	dwTable := &datafactory.FactoriesDatasetAzureSqlDWTable{ObjectMeta: meta("dwtable"), Spec: datafactory.FactoriesDatasetAzureSqlDWTable_Spec{
		Owner: onFactory, Properties: &datafactory.AzureSqlDWTableDataset{Type: ptr(datafactory.AzureSqlDWTableDataset_Type_AzureSqlDWTable),
			LinkedServiceName: ls("dw"), Table: js(`"facts"`)}}}
	collection := &datafactory.FactoriesDatasetCosmosDbSqlApiCollection{ObjectMeta: meta("collection"), Spec: datafactory.FactoriesDatasetCosmosDbSqlApiCollection_Spec{
		Owner: onFactory, Properties: &datafactory.CosmosDbSqlApiCollectionDataset{Type: ptr(datafactory.CosmosDbSqlApiCollectionDataset_Type_CosmosDbSqlApiCollection),
			LinkedServiceName: ls("cosmos"), CollectionName: js(`"orders"`)}}}
	resource := &datafactory.FactoriesDatasetRestResource{ObjectMeta: meta("resource"), Spec: datafactory.FactoriesDatasetRestResource_Spec{
		Owner: onFactory, Properties: &datafactory.RestResourceDataset{Type: ptr(datafactory.RestResourceDataset_Type_RestResource),
			LinkedServiceName: ls("rest"), RelativeUrl: js(`"orders"`), RequestMethod: js(`"GET"`)}}}
	httpFile := &datafactory.FactoriesDatasetHttpFile{ObjectMeta: meta("download"), Spec: datafactory.FactoriesDatasetHttpFile_Spec{
		Owner: onFactory, Properties: &datafactory.HttpDataset{Type: ptr(datafactory.HttpDataset_Type_HttpFile),
			LinkedServiceName: ls("files"), RelativeUrl: js(`"orders.csv"`)}}}
	objs := []conditioned{csv, parquet, jsonFile, avro, orc, binary, excel, xml, table, dwTable, collection, resource, httpFile}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		if o != table {
			eventually(t, timeout, o.GetName()+" Ready", ready(o))
		}
	}
	eventually(t, timeout, "a SQL table of a lake refused", notReadyWith(table, "needs a AzureSqlDatabase linked service"))
	if err := change(t, table, func(d *datafactory.FactoriesDatasetAzureSqlTable) {
		d.Spec.Properties.LinkedServiceName.ReferenceName = ptr("sql")
	}); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the SQL table Ready after the change", ready(table))

	expectResource(t, factoryID+"/datasets/csv", func(r map[string]any) error {
		p := properties(r)
		tp, _ := p["typeProperties"].(map[string]any)
		loc, _ := tp["location"].(map[string]any)
		if p["type"] != "DelimitedText" || tp["columnDelimiter"] != "," || loc["type"] != "AzureBlobFSLocation" || loc["fileSystem"] != "raw" ||
			!strings.Contains(fmt.Sprint(p["linkedServiceName"]), "lake") {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, factoryID+"/datasets/table", func(r map[string]any) error {
		p := properties(r)
		tp, _ := p["typeProperties"].(map[string]any)
		if p["type"] != "AzureSqlTable" || tp["table"] != "orders" || tp["schema"] != "dbo" || !strings.Contains(fmt.Sprint(p["linkedServiceName"]), "sql") {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range append(objs, stores...) {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range append(objs, stores...) {
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, o := range objs {
		if armFake.HasResource(factoryID + "/datasets/" + o.GetName()) {
			t.Errorf("dataset %s is still in the fake", o.GetName())
		}
	}
}

// The pipeline kind (docs/plan.md §8 Q18, (a) as recommended, with (b):
// f89), on ASO's factory by name: a copy from a delimited text dataset in
// Data Lake Gen2 to a SQL table, a ForEach whose nested activities are
// free-form JSON (a copy again), and an Execute Pipeline after the copy.
// Execute Pipeline naming a missing pipeline is refused (the fake's
// "invalid reference"), then fixed by a change; the activities, the copy's
// store settings and the nested copy reach the fake as Data Factory's JSON
// has them. Deleted, it goes.
func TestDataFactoryPipelines(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "adfpl")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	factoryID := rgID + "/providers/Microsoft.DataFactory/factories/etl"
	for _, id := range []string{rgID, factoryID, factoryID + "/datasets/raw", factoryID + "/datasets/table", factoryID + "/pipelines/archive"} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	factory := &asodatafactory.Factory{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "etl"}}
	factory.Spec.AzureName, factory.Spec.Location = "etl", ptr("westus")
	factory.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, factory, factoryID)
	js := func(s string) *v1.JSON { return &v1.JSON{Raw: []byte(s)} }
	ds := func(name string) datafactory.DatasetReference {
		return datafactory.DatasetReference{Type: ptr(datafactory.DatasetReferenceType_DatasetReference), ReferenceName: ptr(name)}
	}
	nested := `[{"name":"inner","type":"Copy","inputs":[{"type":"DatasetReference","referenceName":"raw"}],` +
		`"outputs":[{"type":"DatasetReference","referenceName":"table"}],"typeProperties":{"source":{"type":"DelimitedTextSource"},"sink":{"type":"AzureSqlSink"}}}]`
	pipeline := &datafactory.FactoriesPipeline{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "load"}, Spec: datafactory.FactoriesPipeline_Spec{
		Owner: &genruntime.KnownResourceReference{Name: "etl"}, Description: ptr("raw files into the orders table"),
		Parameters: map[string]datafactory.ParameterSpecification{"files": {Type: ptr(datafactory.ParameterType_Array)}},
		Activities: []datafactory.Activity{
			{Copy: &datafactory.CopyActivity{Name: ptr("copy"), Type: ptr(datafactory.CopyActivity_Type_Copy),
				Inputs: []datafactory.DatasetReference{ds("raw")}, Outputs: []datafactory.DatasetReference{ds("table")},
				Source: &datafactory.CopySource{DelimitedText: &datafactory.DelimitedTextSource{Type: ptr(datafactory.DelimitedTextSource_Type_DelimitedTextSource),
					StoreSettings: &datafactory.StoreReadSettings{AzureBlobFS: &datafactory.AzureBlobFSReadSettings{
						Type: ptr(datafactory.AzureBlobFSReadSettings_Type_AzureBlobFSReadSettings), Recursive: js(`true`)}}}},
				Sink: &datafactory.CopySink{AzureSql: &datafactory.AzureSqlSink{Type: ptr(datafactory.AzureSqlSink_Type_AzureSqlSink), WriteBehavior: js(`"insert"`)}}}},
			{ForEach: &datafactory.ForEachActivity{Name: ptr("each"), Type: ptr(datafactory.ForEachActivity_Type_ForEach),
				Items:      &datafactory.Expression{Type: ptr(datafactory.ExpressionType_Expression), Value: ptr("@pipeline().parameters.files")},
				Activities: js(nested)}},
			{ExecutePipeline: &datafactory.ExecutePipelineActivity{Name: ptr("next"), Type: ptr(datafactory.ExecutePipelineActivity_Type_ExecutePipeline),
				DependsOn: []datafactory.ActivityDependency{{Activity: ptr("copy"), DependencyConditions: []datafactory.DependencyCondition{datafactory.DependencyCondition_Succeeded}}},
				Pipeline:  &datafactory.PipelineReference{Type: ptr(datafactory.PipelineReferenceType_PipelineReference), ReferenceName: ptr("cleanup")}}},
		}}}
	if err := k8s.Create(ctx, pipeline); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "a pipeline running a missing pipeline refused", notReadyWith(pipeline, "invalid reference"))
	if err := change(t, pipeline, func(p *datafactory.FactoriesPipeline) {
		p.Spec.Activities[2].ExecutePipeline.Pipeline.ReferenceName = ptr("archive")
	}); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the pipeline Ready after the change", ready(pipeline))
	expectResource(t, factoryID+"/pipelines/load", func(r map[string]any) error {
		acts, _ := properties(r)["activities"].([]any)
		if len(acts) != 3 {
			return fmt.Errorf("resource: %v", r)
		}
		cp, _ := acts[0].(map[string]any)
		tp, _ := cp["typeProperties"].(map[string]any)
		src, _ := tp["source"].(map[string]any)
		store, _ := src["storeSettings"].(map[string]any)
		each, _ := acts[1].(map[string]any)
		etp, _ := each["typeProperties"].(map[string]any)
		inner, _ := etp["activities"].([]any)
		if cp["type"] != "Copy" || store["type"] != "AzureBlobFSReadSettings" || store["recursive"] != true || len(inner) != 1 ||
			!strings.Contains(fmt.Sprint(inner[0]), "inner") || !strings.Contains(fmt.Sprint(acts[2]), "archive") {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if err := k8s.Delete(ctx, pipeline); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the pipeline gone", gone(pipeline))
	if armFake.HasResource(factoryID + "/pipelines/load") {
		t.Error("the pipeline is still in the fake")
	}
}
