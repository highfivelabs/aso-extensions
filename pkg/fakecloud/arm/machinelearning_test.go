// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// Machine Learning: an asset version is immutable but for its description
// and tags; an environment names an image or a build; a datastore's
// credentials carry what their type needs; an outbound rule names its
// destination, and a private endpoint rule's target exists.
func TestMachineLearning(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/ml"
	ws := rg + "/providers/Microsoft.MachineLearningServices/workspaces/lab"
	reg := rg + "/providers/Microsoft.MachineLearningServices/registries/shared"
	storage := rg + "/providers/Microsoft.Storage/storageAccounts/lake"
	for _, id := range []string{rg, ws, reg, storage} {
		a.AddResource(id)
	}
	put := func(what, id, props string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, id, `{"properties":`+props+`}`)
		expect(t, what, code, resp, want)
	}
	put("a model", ws+"/models/churn", `{"description":"churn"}`, http.StatusCreated)
	v1 := ws + "/models/churn/versions/1"
	put("a model version", v1, `{"modelUri":"azureml://jobs/train/outputs/model","modelType":"mlflow_model","description":"first"}`, http.StatusCreated)
	put("a model version's description changed", v1, `{"modelUri":"azureml://jobs/train/outputs/model","modelType":"mlflow_model","description":"first, kept"}`, http.StatusOK)
	put("a model version's URI changed", v1, `{"modelUri":"azureml://jobs/retrain/outputs/model","modelType":"mlflow_model"}`, http.StatusConflict)
	put("a registry's data container", reg+"/data/sales", `{"dataType":"uri_folder"}`, http.StatusCreated)
	put("an environment container", ws+"/environments/py", `{}`, http.StatusCreated)
	put("a registry's data version", reg+"/data/sales/versions/1", `{"dataType":"uri_folder","dataUri":"https://lake.blob.core.windows.net/sales/"}`, http.StatusCreated)
	put("an environment of nothing", ws+"/environments/py/versions/1", `{"description":"empty"}`, http.StatusBadRequest)
	put("an environment", ws+"/environments/py/versions/1", `{"image":"mcr.microsoft.com/azureml/openmpi4.1.0-ubuntu22.04"}`, http.StatusCreated)

	put("a datastore of no credentials type", ws+"/datastores/lake", `{"datastoreType":"AzureBlob","accountName":"lake","containerName":"raw","credentials":{}}`, http.StatusBadRequest)
	put("a key datastore with no key", ws+"/datastores/lake", `{"datastoreType":"AzureBlob","accountName":"lake","containerName":"raw","credentials":{"credentialsType":"AccountKey","secrets":{"secretsType":"AccountKey"}}}`, http.StatusBadRequest)
	put("a blob datastore with no container", ws+"/datastores/lake", `{"datastoreType":"AzureBlob","accountName":"lake","credentials":{"credentialsType":"None"}}`, http.StatusBadRequest)
	put("a key datastore", ws+"/datastores/lake", `{"datastoreType":"AzureBlob","accountName":"lake","containerName":"raw","credentials":{"credentialsType":"AccountKey","secrets":{"secretsType":"AccountKey","key":"a2V5"}}}`, http.StatusCreated)
	put("a principal datastore with no tenant", ws+"/datastores/gen2", `{"datastoreType":"AzureDataLakeGen2","accountName":"lake","filesystem":"raw","credentials":{"credentialsType":"ServicePrincipal","clientId":"app","secrets":{"secretsType":"ServicePrincipal","clientSecret":"s"}}}`, http.StatusBadRequest)

	put("an FQDN rule with no destination", ws+"/outboundRules/pypi", `{"type":"FQDN"}`, http.StatusBadRequest)
	put("an FQDN rule", ws+"/outboundRules/pypi", `{"type":"FQDN","destination":"pypi.org","category":"UserDefined"}`, http.StatusCreated)
	put("an endpoint rule to nothing", ws+"/outboundRules/lake", `{"type":"PrivateEndpoint","destination":{"serviceResourceId":"`+storage+`x","subresourceTarget":"blob"}}`, http.StatusNotFound)
	put("an endpoint rule", ws+"/outboundRules/lake", `{"type":"PrivateEndpoint","destination":{"serviceResourceId":"`+storage+`","subresourceTarget":"blob"}}`, http.StatusCreated)
	put("a service tag rule", ws+"/outboundRules/monitor", `{"type":"ServiceTag","destination":{"serviceTag":"AzureMonitor","protocol":"TCP","portRanges":"443"}}`, http.StatusCreated)

	ep := ws + "/onlineEndpoints/score"
	put("an endpoint with traffic to nothing", ep, `{"authMode":"Key","traffic":{"blue":100}}`, http.StatusBadRequest)
	put("an endpoint", ep, `{"authMode":"Key"}`, http.StatusCreated)
	put("a managed deployment with no instance type", ep+"/deployments/blue", `{"endpointComputeType":"Managed","model":"azureml:churn:1"}`, http.StatusBadRequest)
	put("a managed deployment", ep+"/deployments/blue", `{"endpointComputeType":"Managed","model":"azureml:churn:1","instanceType":"Standard_DS3_v2"}`, http.StatusCreated)
	put("traffic of 90", ep, `{"authMode":"Key","traffic":{"blue":90}}`, http.StatusBadRequest)
	put("traffic to the deployment", ep, `{"authMode":"Key","traffic":{"blue":100}}`, http.StatusOK)
	be := ws + "/batchEndpoints/nightly"
	put("a batch endpoint defaulting to nothing", be, `{"authMode":"AADToken","defaults":{"deploymentName":"v1"}}`, http.StatusBadRequest)
	put("a batch endpoint", be, `{"authMode":"AADToken"}`, http.StatusCreated)
	put("a batch deployment", be+"/deployments/v1", `{"compute":"cpu-cluster","model":{"referenceType":"Id","assetId":"azureml:churn:1"}}`, http.StatusCreated)
	put("a batch endpoint defaulting to it", be, `{"authMode":"AADToken","defaults":{"deploymentName":"v1"}}`, http.StatusOK)
}
