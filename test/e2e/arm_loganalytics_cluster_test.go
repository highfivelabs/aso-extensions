// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	oi "github.com/highfivelabs/aso-extensions/apis/arm/operationalinsights/v20250701"
)

// A dedicated Log Analytics cluster in a resource group, its commitment
// tier changed; a data source of ASO's Workspace (a syslog facility, kind
// and free-form properties); a storage insight config whose storage
// account key comes from a Secret and is kept out of the status; all
// deleted.
func TestLogAnalyticsClusterAndWorkspaceSources(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "la-cluster")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	workspaceID, rgID := logAnalytics(t, ns)

	cluster := &oi.Cluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "logs"},
		Spec: oi.Cluster_Spec{
			AzureName:                  ns + "-logs",
			Owner:                      &genruntime.KnownResourceReference{Name: "rg"},
			Location:                   ptr("westus"),
			Sku:                        &oi.ClusterSku{Name: ptr(oi.ClusterSkuNameEnum_CapacityReservation), Capacity: ptr(500)},
			IsAvailabilityZonesEnabled: ptr(true),
		},
	}
	if err := k8s.Create(ctx, cluster); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "Cluster Ready", ready(cluster))
	clusterID := rgID + "/providers/Microsoft.OperationalInsights/clusters/" + ns + "-logs"
	expectResource(t, clusterID, func(r map[string]any) error {
		sku, _ := r["sku"].(map[string]any)
		if r["type"] != "Microsoft.OperationalInsights/clusters" || sku["name"] != "CapacityReservation" || sku["capacity"] != float64(500) ||
			properties(r)["isAvailabilityZonesEnabled"] != true {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(cluster), cluster); err != nil {
		t.Fatal(err)
	}
	cluster.Spec.Sku.Capacity = ptr(1000)
	if err := k8s.Update(ctx, cluster); err != nil {
		t.Fatal(err)
	}
	expectResource(t, clusterID, func(r map[string]any) error {
		if sku, _ := r["sku"].(map[string]any); sku["capacity"] != float64(1000) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	syslog, err := json.Marshal(map[string]any{"syslogName": "auth", "syslogSeverities": []map[string]string{{"severity": "err"}, {"severity": "crit"}}})
	if err != nil {
		t.Fatal(err)
	}
	source := &oi.WorkspacesDataSource{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "auth"},
		Spec: oi.WorkspacesDataSource_Spec{
			AzureName:  "syslog-auth",
			Owner:      &genruntime.KnownResourceReference{Name: "law"},
			Kind:       ptr(oi.DataSourceKind_LinuxSyslog),
			Properties: &apiextensionsv1.JSON{Raw: syslog},
		},
	}
	accountID := rgID + "/providers/Microsoft.Storage/storageAccounts/" + strings.ReplaceAll(ns, "-", "") + "diag"
	armFake.AddResource(accountID)
	secret(t, ns, "diag-storage", map[string]string{"key": "diag-key-1"})
	insight := &oi.WorkspacesStorageInsightConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "diag"},
		Spec: oi.WorkspacesStorageInsightConfig_Spec{
			AzureName:  "diag",
			Owner:      &genruntime.KnownResourceReference{Name: "law"},
			Containers: []string{"wad-iis-logfiles"},
			Tables:     []string{"WADWindowsEventLogsTable"},
			StorageAccount: &oi.StorageAccount{
				Reference: &genruntime.ResourceReference{ARMID: accountID},
				Key:       &genruntime.SecretReference{Name: "diag-storage", Key: "key"},
			},
		},
	}
	for _, o := range []client.Object{source, insight} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []client.Object{source, insight} {
		eventually(t, timeout, fmt.Sprintf("%T Ready", o), ready(o.(conditioned)))
	}
	sourceID := workspaceID + "/dataSources/syslog-auth"
	expectResource(t, sourceID, func(r map[string]any) error {
		p := properties(r)
		if r["type"] != "Microsoft.OperationalInsights/workspaces/dataSources" || r["kind"] != "LinuxSyslog" || p["syslogName"] != "auth" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	insightID := workspaceID + "/storageInsightConfigs/diag"
	expectResource(t, insightID, func(r map[string]any) error {
		p := properties(r)
		account, _ := p["storageAccount"].(map[string]any)
		if r["type"] != "Microsoft.OperationalInsights/workspaces/storageInsightConfigs" || account["id"] != accountID ||
			!reflect.DeepEqual(p["containers"], []any{"wad-iis-logfiles"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if k, ok := armFake.SharedKey(insightID); !ok || k != "diag-key-1" {
		t.Fatalf("the storage key Resource Manager was given: %q %v", k, ok)
	}
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(insight), insight); err != nil {
		t.Fatal(err)
	}
	if status, _ := json.Marshal(insight.Status); strings.Contains(string(status), "diag-key-1") {
		t.Fatalf("the storage key is in the status: %s", status)
	}

	for _, o := range []client.Object{source, insight, cluster} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T gone", o), gone(o))
	}
	for _, id := range []string{sourceID, insightID, clusterID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
}

// A cluster the fake refuses: a capacity that is no commitment tier.
func TestLogAnalyticsClusterRefused(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "la-cluster-no")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	armFake.AddResource("/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg")
	resourceGroup(t, ns, "rg", ns+"-rg")
	cluster := &oi.Cluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "logs"},
		Spec: oi.Cluster_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
			Sku:      &oi.ClusterSku{Name: ptr(oi.ClusterSkuNameEnum_CapacityReservation), Capacity: ptr(150)},
		},
	}
	if err := k8s.Create(ctx, cluster); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the refusal on the Ready condition", notReadyWith(cluster, "CapacityReservation with a capacity of"))
}
