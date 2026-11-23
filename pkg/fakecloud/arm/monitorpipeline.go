// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Azure Monitor pipeline groups (Microsoft.Monitor/pipelineGroups,
// 2026-04-01; docs/plan.md f54), as far as the fake models them. Each rule
// is one the Azure Monitor pipeline documentation states:
//
//   - a pipeline group runs on an Azure Arc-enabled cluster's custom
//     location (extendedLocation of type CustomLocation; 400) that exists
//     (404);
//   - its receivers, processors and exporters have names unique within
//     their kind (400), and a receiver or exporter carries the settings of
//     its type (syslog, otlp, azureMonitorWorkspaceLogs; 400);
//   - every pipeline names only receivers, processors and exporters the
//     group declares (400).
//
// Not modelled: the pipeline running on the cluster, data reaching the data
// collection rule, persistence, TLS certificates being read, and the error
// codes (the fake's own).
var monitorPipelineGroup = regexp.MustCompile(`(?i)/providers/microsoft\.monitor/pipelinegroups/[^/]+$`)

// receiverSettings and exporterSettings are the property each type of
// receiver or exporter carries its settings in.
var (
	receiverSettings = map[string]string{"syslog": "syslog", "otlp": "otlp"}
	exporterSettings = map[string]string{"azuremonitorworkspacelogs": "azureMonitorWorkspaceLogs"}
)

// checkMonitorPipeline refuses what the rules above do not allow; code is ""
// when it is allowed or id is not a pipeline group.
func (a *ARM) checkMonitorPipeline(id string, body map[string]any) (status int, code, message string) {
	if !monitorPipelineGroup.MatchString(id) {
		return 0, "", ""
	}
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	bad := func(msg string) (int, string, string) { return http.StatusBadRequest, "InvalidPipelineGroup", msg }
	ext, _ := body["extendedLocation"].(map[string]any)
	loc := str(ext, "name")
	if !strings.EqualFold(str(ext, "type"), "CustomLocation") || loc == "" {
		return bad("A pipeline group runs on a custom location (extendedLocation, type CustomLocation).")
	}
	if !a.exists(loc) {
		return http.StatusNotFound, "ResourceNotFound", "The custom location " + loc + " was not found."
	}
	props, _ := body["properties"].(map[string]any)
	declared := map[string]map[string]bool{}
	for _, kind := range []string{"receivers", "processors", "exporters"} {
		declared[kind] = map[string]bool{}
		items, _ := props[kind].([]any)
		for _, it := range items {
			m, _ := it.(map[string]any)
			name := str(m, "name")
			if declared[kind][name] {
				return bad("The " + kind + " name " + name + " is used twice.")
			}
			declared[kind][name] = true
			settings := map[string]map[string]string{"receivers": receiverSettings, "exporters": exporterSettings}[kind]
			if want, ok := settings[strings.ToLower(str(m, "type"))]; ok && m[want] == nil {
				return bad("The " + kind[:len(kind)-1] + " " + name + " of type " + str(m, "type") + " needs its " + want + " settings.")
			}
		}
	}
	service, _ := props["service"].(map[string]any)
	pipelines, _ := service["pipelines"].([]any)
	for _, p := range pipelines {
		pm, _ := p.(map[string]any)
		for _, kind := range []string{"receivers", "processors", "exporters"} {
			names, _ := pm[kind].([]any)
			for _, n := range names {
				if s, _ := n.(string); !declared[kind][s] {
					return bad("The pipeline " + str(pm, "name") + " names " + kind[:len(kind)-1] + " " + s + ", which the group does not declare.")
				}
			}
		}
	}
	return 0, "", ""
}
