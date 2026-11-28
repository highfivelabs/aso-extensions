// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	monitor "github.com/highfivelabs/aso-extensions/apis/arm/monitor/v20260401"
)

// An Azure Monitor pipeline group (docs/plan.md f54) in a resource group, on
// an Arc cluster's custom location: a syslog receiver, a batch processor and
// an exporter to a data collection rule, joined by one logs pipeline. A
// change of the batch size is PUT again; a pipeline naming an exporter the
// group does not declare and a group on a missing custom location are
// refused (the fake's rules, monitorpipeline.go); deleted, it goes.
func TestMonitorPipelineGroup(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "pipeline")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	locationID := rgID + "/providers/Microsoft.ExtendedLocation/customLocations/plant"
	for _, id := range []string{rgID, locationID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	onRG := &genruntime.KnownResourceReference{Name: "rg"}

	group := func(name, location, exporter string) *monitor.PipelineGroup {
		return &monitor.PipelineGroup{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec: monitor.PipelineGroup_Spec{
				Owner: onRG, Location: ptr("westus"),
				ExtendedLocation: &monitor.AzureResourceManagerCommonTypesExtendedLocation{
					Name: ptr(location), Type: ptr(monitor.AzureResourceManagerCommonTypesExtendedLocationType_CustomLocation),
				},
				Properties: &monitor.PipelineGroupProperties{
					Receivers: []monitor.Receiver{{
						Type: ptr(monitor.ReceiverType_Syslog), Name: ptr("syslog"),
						Syslog: &monitor.SyslogReceiver{Endpoint: ptr("0.0.0.0:514")},
					}},
					Processors: []monitor.Processor{{
						Type: ptr(monitor.ProcessorType_Batch), Name: ptr("batch"), Batch: &monitor.BatchProcessor{BatchSize: ptr(1000), Timeout: ptr(1000)},
					}},
					Exporters: []monitor.Exporter{{
						Type: ptr(monitor.ExporterType_AzureMonitorWorkspaceLogs), Name: ptr("amw"),
						AzureMonitorWorkspaceLogs: &monitor.AzureMonitorWorkspaceLogsExporter{Api: &monitor.AzureMonitorWorkspaceLogsApiConfig{
							DataCollectionEndpointUrl: ptr("https://dce-plant.westus-1.ingest.monitor.azure.com"),
							DataCollectionRule:        ptr("dcr-00000000000000000000000000000001"), Stream: ptr("Custom-Syslog"),
							Schema: &monitor.SchemaMap{RecordMap: []monitor.RecordMap{{From: ptr("body"), To: ptr("Body")}}},
						}},
					}},
					Service: &monitor.Service{Pipelines: []monitor.Pipeline{{
						Name: ptr("logs"), Type: ptr(monitor.PipelineType_Logs),
						Receivers: []string{"syslog"}, Processors: []string{"batch"}, Exporters: []string{exporter},
					}}},
				},
			},
		}
	}
	edge := group("edge", locationID, "amw")
	if err := k8s.Create(ctx, edge); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "pipeline group Ready", ready(edge))
	groupID := rgID + "/providers/Microsoft.Monitor/pipelineGroups/edge"
	expectResource(t, groupID, func(r map[string]any) error {
		ext, _ := r["extendedLocation"].(map[string]any)
		svc, _ := properties(r)["service"].(map[string]any)
		pipes, _ := svc["pipelines"].([]any)
		if ext["name"] != locationID || len(pipes) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again.
	if err := change(t, edge, func(g *monitor.PipelineGroup) { g.Spec.Properties.Processors[0].Batch.BatchSize = ptr(5000) }); err != nil {
		t.Fatal(err)
	}
	expectResource(t, groupID, func(r map[string]any) error {
		procs, _ := properties(r)["processors"].([]any)
		p, _ := procs[0].(map[string]any)
		b, _ := p["batch"].(map[string]any)
		if b["batchSize"] != float64(5000) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// Refused: a pipeline naming an undeclared exporter, a group on a
	// missing custom location.
	stray := group("stray", locationID, "elsewhere")
	lost := group("lost", rgID+"/providers/Microsoft.ExtendedLocation/customLocations/none", "amw")
	for _, o := range []client.Object{stray, lost} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "a pipeline naming an undeclared exporter refused", notReadyWith(stray, "does not declare"))
	eventually(t, timeout, "a missing custom location refused", notReadyWith(lost, "was not found"))

	for _, o := range []client.Object{stray, lost, edge} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	if armFake.HasResource(groupID) {
		t.Fatal("the pipeline group is still in the fake")
	}
}
