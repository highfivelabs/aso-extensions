// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Azure SQL server and database leftovers (Microsoft.Sql, 2025-01-01;
// docs/plan.md f43), as far as the fake models them:
//
//   - Microsoft support operations auditing that is Enabled sends its events
//     somewhere: storageEndpoint or isAzureMonitorTargetEnabled (the spec:
//     "If state is Enabled, storageEndpoint or isAzureMonitorTargetEnabled
//     is required"; 400);
//   - a workload group's minimum share is no more than its cap, both 0 to
//     100 percent (CREATE WORKLOAD GROUP's MIN_PERCENTAGE_RESOURCE and
//     CAP_PERCENTAGE_RESOURCE, which the ARM properties set; 400).
//
// Not modelled: a workload group needing a dedicated SQL pool (any database
// takes one here), masking rules being applied to queries, maintenance
// windows being kept to, ledger digests being uploaded, baselines matching
// scan results, and the error codes (the fake's own).
var (
	sqlDevOpsAudit   = regexp.MustCompile(`(?i)/providers/microsoft\.sql/servers/[^/]+/devopsauditingsettings/default$`)
	sqlWorkloadGroup = regexp.MustCompile(`(?i)/providers/microsoft\.sql/servers/[^/]+/databases/[^/]+/workloadgroups/[^/]+$`)
)

// checkSQLServerExtras refuses what the rules above do not allow; code is ""
// when it is allowed or id is none of these.
func (a *ARM) checkSQLServerExtras(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	switch {
	case sqlDevOpsAudit.MatchString(id):
		state, _ := props["state"].(string)
		endpoint, _ := props["storageEndpoint"].(string)
		monitor, _ := props["isAzureMonitorTargetEnabled"].(bool)
		if strings.EqualFold(state, "Enabled") && endpoint == "" && !monitor {
			return http.StatusBadRequest, "DevOpsAuditTargetMissing", "Enabled DevOps auditing needs storageEndpoint or isAzureMonitorTargetEnabled."
		}
	case sqlWorkloadGroup.MatchString(id):
		lo, _ := props["minResourcePercent"].(float64)
		hi, _ := props["maxResourcePercent"].(float64)
		if lo < 0 || hi > 100 || lo > hi {
			return http.StatusBadRequest, "InvalidWorkloadGroupResourcePercent",
				"A workload group's minResourcePercent is no more than its maxResourcePercent, both 0 to 100."
		}
	}
	return 0, "", ""
}
