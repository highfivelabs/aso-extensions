// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// Elastic Jobs: an agent names a job database that exists; a credential has
// a username and password; a step names a target group, and its credentials,
// in its own agent, that exist.
func TestSQLJobs(t *testing.T) {
	a := New(func(string) bool { return true })
	server := "/subscriptions/" + sub1 + "/resourceGroups/jobs/providers/Microsoft.Sql/servers/sql"
	a.AddResource("/subscriptions/" + sub1 + "/resourceGroups/jobs")
	a.AddResource(server)
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	db := server + "/databases/jobs"
	agent := server + "/jobAgents/agent"
	put("an agent with no database", agent, `{"location":"westus","properties":{}}`, http.StatusBadRequest)
	put("an agent whose database is not there", agent, `{"location":"westus","properties":{"databaseId":"`+db+`"}}`, http.StatusNotFound)
	a.AddResource(db)
	put("an agent", agent, `{"location":"westus","properties":{"databaseId":"`+db+`"}}`, http.StatusCreated)
	put("a credential with no password", agent+"/credentials/c", `{"properties":{"username":"jobs"}}`, http.StatusBadRequest)
	put("a credential", agent+"/credentials/c", `{"properties":{"username":"jobs","password":"p"}}`, http.StatusCreated)
	put("a job", agent+"/jobs/nightly", `{"properties":{"description":"d"}}`, http.StatusCreated)
	step := agent + "/jobs/nightly/steps/s1"
	put("a step with no target group", step, `{"properties":{"action":{"value":"SELECT 1"}}}`, http.StatusBadRequest)
	put("a step whose target group is not there", step, `{"properties":{"targetGroup":"`+agent+`/targetGroups/all","action":{"value":"SELECT 1"}}}`, http.StatusNotFound)
	put("a target group", agent+"/targetGroups/all", `{"properties":{"members":[]}}`, http.StatusCreated)
	other := server + "/jobAgents/other/credentials/c"
	put("a step with another agent's credential", step, `{"properties":{"targetGroup":"`+agent+`/targetGroups/all","credential":"`+other+`","action":{"value":"SELECT 1"}}}`, http.StatusBadRequest)
	put("a step", step, `{"properties":{"targetGroup":"`+agent+`/targetGroups/all","credential":"`+agent+`/credentials/c","action":{"value":"SELECT 1"}}}`, http.StatusCreated)
}
