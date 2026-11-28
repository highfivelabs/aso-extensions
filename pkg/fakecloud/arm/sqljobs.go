// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Azure SQL Elastic Jobs (Microsoft.Sql/servers/jobAgents and its children,
// 2025-01-01; docs/plan.md f39), as far as the fake models them. Each rule is
// one the spec's descriptions state:
//
//   - a job agent names its job database (databaseId), which must exist
//     (404);
//   - a credential is made with a username and a password (400);
//   - a job step names a target group (required) and may name a credential
//     and an output's credential; each must exist (404) and belong to the
//     step's own job agent (400).
//
// Not modelled: jobs running (schedules, executions, step outputs being
// written), target groups' members being resolved, the job database's
// schema, SKUs, managed identities, and the error codes (the fake's own).
var (
	sqlJobAgent      = regexp.MustCompile(`(?i)^(/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.sql/servers/[^/]+/jobagents/[^/]+)$`)
	sqlJobCredential = regexp.MustCompile(`(?i)/providers/microsoft\.sql/servers/[^/]+/jobagents/[^/]+/credentials/[^/]+$`)
	sqlJobStep       = regexp.MustCompile(`(?i)^(/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.sql/servers/[^/]+/jobagents/[^/]+)/jobs/[^/]+/steps/[^/]+$`)
)

// checkSQLJobs refuses what Elastic Jobs refuse; code is "" when it is
// allowed or id is none of these.
func (a *ARM) checkSQLJobs(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	switch {
	case sqlJobAgent.MatchString(id):
		db, _ := props["databaseId"].(string)
		if db == "" {
			return http.StatusBadRequest, "JobAgentDatabaseIdMissing", "A job agent needs its job database (databaseId)."
		}
		if !a.exists(db) {
			return http.StatusNotFound, "ResourceNotFound", "The job database " + db + " was not found."
		}
	case sqlJobCredential.MatchString(id):
		if u, _ := props["username"].(string); u == "" {
			return http.StatusBadRequest, "JobCredentialUsernameMissing", "A job credential needs a username."
		}
		if p, _ := props["password"].(string); p == "" {
			return http.StatusBadRequest, "JobCredentialPasswordMissing", "A job credential needs a password."
		}
	case sqlJobStep.MatchString(id):
		agent := sqlJobStep.FindStringSubmatch(id)[1]
		output, _ := props["output"].(map[string]any)
		refs := map[string]any{"targetGroup": props["targetGroup"], "credential": props["credential"], "output.credential": output["credential"]}
		if s, _ := props["targetGroup"].(string); s == "" {
			return http.StatusBadRequest, "JobStepTargetGroupMissing", "A job step needs its target group."
		}
		for field, v := range refs {
			ref, _ := v.(string)
			if ref == "" {
				continue
			}
			if !strings.HasPrefix(strings.ToLower(ref), strings.ToLower(agent)+"/") {
				return http.StatusBadRequest, "JobStepReferenceOtherAgent", "The step's " + field + " " + ref + " is not in its job agent " + agent + "."
			}
			if !a.exists(ref) {
				return http.StatusNotFound, "ResourceNotFound", "The step's " + field + " " + ref + " was not found."
			}
		}
	}
	return 0, "", ""
}
