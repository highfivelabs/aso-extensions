// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"strings"
	"testing"
)

// A pipeline group runs on a custom location that exists, declares uniquely
// named receivers, processors and exporters with their settings, and its
// pipelines name only what it declares.
func TestMonitorPipelineGroup(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/edge"
	cl := rg + "/providers/Microsoft.ExtendedLocation/customLocations/plant"
	for _, id := range []string{rg, cl} {
		a.AddResource(id)
	}
	put := func(what, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, rg+"/providers/Microsoft.Monitor/pipelineGroups/edge", body)
		expect(t, what, code, resp, want)
	}
	group := func(ext, receivers, pipelineReceivers string) string {
		return `{"location":"westus",` + ext + `"properties":{"receivers":[` + receivers + `],"processors":[{"type":"Batch","name":"batch","batch":{}}],` +
			`"exporters":[{"type":"AzureMonitorWorkspaceLogs","name":"amw","azureMonitorWorkspaceLogs":{"api":{"dataCollectionEndpointUrl":"https://dce.westus.ingest.monitor.azure.com","dataCollectionRule":"dcr-1","stream":"Custom-Syslog","schema":{}}}}],` +
			`"service":{"pipelines":[{"name":"logs","type":"Logs","receivers":[` + pipelineReceivers + `],"processors":["batch"],"exporters":["amw"]}]}}}`
	}
	ext := `"extendedLocation":{"type":"CustomLocation","name":"` + cl + `"},`
	syslog := `{"type":"Syslog","name":"syslog","syslog":{"endpoint":"0.0.0.0:514"}}`
	put("no custom location", group("", syslog, `"syslog"`), http.StatusBadRequest)
	put("a missing custom location", group(strings.Replace(ext, "plant", "none", 1), syslog, `"syslog"`), http.StatusNotFound)
	put("a receiver twice", group(ext, syslog+","+syslog, `"syslog"`), http.StatusBadRequest)
	put("a syslog receiver without its settings", group(ext, `{"type":"Syslog","name":"syslog"}`, `"syslog"`), http.StatusBadRequest)
	put("a pipeline naming an undeclared receiver", group(ext, syslog, `"otlp"`), http.StatusBadRequest)
	put("a pipeline group", group(ext, syslog, `"syslog"`), http.StatusCreated)
}
