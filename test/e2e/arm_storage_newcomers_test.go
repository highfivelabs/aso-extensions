// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	asostorage "github.com/Azure/azure-service-operator/v2/api/storage/v20260601/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	storage "github.com/highfivelabs/aso-extensions/apis/arm/storage/v20260601"
	storage2026 "github.com/highfivelabs/aso-extensions/apis/arm/storage/v20260901"
)

// A storage account's newer children (docs/plan.md f48) on ASO's
// StorageAccount stand-in: a data share of a folder to a managed identity,
// a connector reaching another account's share through a user-assigned
// identity by ARM ID, the container-level capacity metrics rule, a blob
// access point to an S3-compatible store with its secret key from a Secret
// (the status has no field for it), and a context cache with a container.
// The access point made inactive and the connector's description are PUT
// again; a move of the access point to another source type is refused by
// the fake (storagenewcomers.go); all are deleted, children first.
func TestStorageNewcomers(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "stnew")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	sfx := strings.ReplaceAll(ns, "-", "")
	accountID := rgID + "/providers/Microsoft.Storage/storageAccounts/lake" + sfx
	identityID := rgID + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/reader"
	for _, id := range []string{rgID, accountID, identityID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	account := &asostorage.StorageAccount{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "lake"}}
	account.Spec.AzureName, account.Spec.Owner = "lake"+sfx, &genruntime.KnownResourceReference{Name: "rg"}
	asoStandIn(t, account, accountID)
	secret(t, ns, "nas-key", map[string]string{"secret": "nas-secret-key-1"})
	onAccount := &genruntime.KnownResourceReference{Name: "lake"}

	share := &storage.StorageAccountsDataShare{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "reports"},
		Spec: storage.StorageAccountsDataShare_Spec{
			AzureName: "reports", Owner: onAccount, Location: ptr("westus"),
			Properties: &storage.StorageDataShareProperties{
				Description: ptr("monthly reports"),
				AccessPolicies: []storage.StorageDataShareAccessPolicy{{
					PrincipalId: ptr("00000000-0000-0000-0000-0000000000c1"), TenantId: ptr(tenantID),
					Permission: ptr(storage.StorageDataShareAccessPolicyPermission_Read),
				}},
				Assets: []storage.StorageDataShareAsset{{AssetPath: ptr("reports/2026"), DisplayName: ptr("reports")}},
			},
		},
	}
	connector := &storage.StorageAccountsConnector{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "partner"},
		Spec: storage.StorageAccountsConnector_Spec{
			AzureName: "partner", Owner: onAccount, Location: ptr("westus"),
			Properties: &storage.StorageConnectorProperties{
				DataSourceType: ptr(storage.StorageConnectorDataSourceType_Azure_DataShare),
				Source: &storage.StorageConnectorSource{DataShare: &storage.DataShareSource{
					Type: ptr(storage.StorageConnectorSourceType_DataShare),
					Connection: &storage.StorageConnectorConnection{DataShare: &storage.DataShareConnection{
						Type:         ptr(storage.StorageConnectorConnectionType_DataShare),
						DataShareUri: ptr("azds://westus:partnerlake:00000000-0000-0000-0000-0000000000c2"),
					}},
					AuthProperties: &storage.StorageConnectorAuthProperties{ManagedIdentity: &storage.ManagedIdentityAuthProperties{
						Type:                      ptr(storage.StorageConnectorAuthType_ManagedIdentity),
						IdentityResourceReference: &genruntime.ResourceReference{ARMID: identityID},
					}},
				}},
			},
		},
	}
	metrics := &storage.StorageAccountsAdvancedPlatformMetric{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "capacity"},
		Spec: storage.StorageAccountsAdvancedPlatformMetric_Spec{
			Owner: onAccount,
			Properties: &storage.AdvancedPlatformMetricsRuleProperties{
				Enabled: ptr(true),
				RuleConfig: &storage.AdvancedPlatformMetricsRuleConfig{
					FilterType: ptr(storage.AdvancedPlatformMetricsFilterType_ContainerPrefixFilter), FilterValues: []string{"logs-"},
				},
			},
		},
	}
	point := &storage2026.StorageAccountsBlobAccessPointConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "nas"},
		Spec: storage2026.StorageAccountsBlobAccessPointConfiguration_Spec{
			AzureName: "nas", Owner: onAccount, Location: ptr("westus"),
			Properties: &storage2026.BlobAccessPointConfigurationProperties{
				Source: &storage2026.BlobAccessPointSourceProperties{S3Compatible: &storage2026.BlobAccessPointGenericS3SourceProperties{
					SourceType: ptr(storage2026.BlobAccessPointGenericS3SourceProperties_SourceType_S3Compatible),
					Connection: &storage2026.BlobAccessPointConnectionProperties{Endpoint: &storage2026.BlobAccessPointEndpointConnectionProperties{
						ConnectionType:  ptr(storage2026.BlobAccessPointEndpointConnectionProperties_ConnectionType_Endpoint),
						Endpoint:        ptr("https://nas.example.com"),
						TlsVerification: ptr(storage2026.BlobAccessPointTlsVerification_Perform),
					}},
					Auth: &storage2026.BlobAccessPointRemoteAuthProperties{AccessKey: &storage2026.BlobAccessPointAccessKeyAuthProperties{
						AuthType:        ptr(storage2026.BlobAccessPointRemoteAuthType_AccessKey),
						AccessKeyId:     ptr("nas-reader"),
						SecretAccessKey: &genruntime.SecretReference{Name: "nas-key", Key: "secret"},
					}},
				}},
			},
		},
	}
	cache := &storage.ContextCache{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "prompts"},
		Spec: storage.ContextCache_Spec{
			AzureName: "prompts-" + sfx, Owner: &genruntime.KnownResourceReference{Name: "rg"}, Location: ptr("westus"),
			Properties: &storage.ContextCacheProperties{AccountKind: ptr(storage.ContextCacheAccountKind_Regional)},
		},
	}
	container := &storage.ContextCachesContextCacheContainer{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "gpt"},
		Spec: storage.ContextCachesContextCacheContainer_Spec{
			AzureName: "gpt", Owner: &genruntime.KnownResourceReference{Name: "prompts"},
			Properties: &storage.ContextCacheContainerProperties{
				ModelName: ptr("gpt-4o"), Provider: ptr(storage.AiProvider_OpenAI), TimeToLive: ptr(7),
			},
		},
	}
	objs := []conditioned{share, connector, metrics, point, cache, container}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}

	expectResource(t, accountID+"/dataShares/reports", func(r map[string]any) error {
		assets, _ := properties(r)["assets"].([]any)
		if r["type"] != "Microsoft.Storage/storageAccounts/dataShares" || len(assets) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, accountID+"/connectors/partner", func(r map[string]any) error {
		source, _ := properties(r)["source"].(map[string]any)
		auth, _ := source["authProperties"].(map[string]any)
		if source["type"] != "DataShare" || auth["identityResourceId"] != identityID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, accountID+"/advancedPlatformMetrics/ContainerLevelCapacityMetrics", func(r map[string]any) error {
		if properties(r)["enabled"] != true {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	pointID := accountID + "/blobAccessPointConfigurations/nas"
	expectResource(t, pointID, func(r map[string]any) error {
		source, _ := properties(r)["source"].(map[string]any)
		auth, _ := source["auth"].(map[string]any)
		if source["sourceType"] != "S3Compatible" || auth["secretAccessKey"] != "nas-secret-key-1" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	cacheID := rgID + "/providers/Microsoft.Storage/contextCaches/prompts-" + sfx
	expectResource(t, cacheID+"/contextCacheContainers/gpt", func(r map[string]any) error {
		if p := properties(r); p["modelName"] != "gpt-4o" || p["timeToLive"] != float64(7) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// The access point made inactive is PUT again.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(point), point); err != nil {
		t.Fatal(err)
	}
	point.Spec.Properties.State = ptr(storage2026.BlobAccessPointConfigurationState_Inactive)
	if err := k8s.Update(ctx, point); err != nil {
		t.Fatal(err)
	}
	expectResource(t, pointID, func(r map[string]any) error {
		if properties(r)["state"] != "Inactive" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A connector's description is PUT again.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(connector), connector); err != nil {
		t.Fatal(err)
	}
	connector.Spec.Properties.Description = ptr("the partner's lake")
	if err := k8s.Update(ctx, connector); err != nil {
		t.Fatal(err)
	}
	expectResource(t, accountID+"/connectors/partner", func(r map[string]any) error {
		if properties(r)["description"] != "the partner's lake" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// The access point cannot move to another source type.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(point), point); err != nil {
		t.Fatal(err)
	}
	s3 := point.Spec.Properties.Source.S3Compatible
	point.Spec.Properties.Source = &storage2026.BlobAccessPointSourceProperties{Qumulo: &storage2026.BlobAccessPointQumuloSourceProperties{
		SourceType: ptr(storage2026.BlobAccessPointQumuloSourceProperties_SourceType_Qumulo),
		Connection: s3.Connection, Auth: s3.Auth,
	}}
	if err := k8s.Update(ctx, point); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "a move to another source type refused", notReadyWith(point, "source type cannot change"))

	for _, o := range []client.Object{container, cache, point, metrics, connector, share} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	for _, id := range []string{cacheID, pointID, accountID + "/connectors/partner", accountID + "/dataShares/reports"} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in the fake", id)
		}
	}
}
