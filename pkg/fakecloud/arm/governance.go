// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Defender for Cloud governance rules (Microsoft.Security, API
// 2022-01-01-preview), as far as the fake models them. Every rule is one the
// spec states:
//
//   - a rule's name is its key, a GUID (400);
//   - its priority is from 0 to 1000, and two rules at one scope cannot
//     share a priority ("Rules with the same priority on the same scope will
//     not be allowed": 400);
//   - it gives at least one condition set (400).
//
// Not modelled: owners being assigned to recommendations or notified, the
// condition sets' content (the spec leaves them free-form), excluded and
// member scopes, the scopes Azure accepts (a subscription, a management
// group or a security connector; any is taken here), and the error codes
// (this fake's own).
var governanceRule = regexp.MustCompile(`(?i)^(.*)/providers/microsoft\.security/governancerules/([^/]+)$`)

// checkGovernance refuses a governance rule the rules above do not allow;
// code is "" when it is allowed or id is not one.
func (a *ARM) checkGovernance(id string, body map[string]any) (status int, code, message string) {
	m := governanceRule.FindStringSubmatch(id)
	if m == nil {
		return 0, "", ""
	}
	if !guid.MatchString(m[2]) {
		return http.StatusBadRequest, "InvalidGovernanceRuleKey", "A governance rule's key is a GUID, not " + m[2] + "."
	}
	props, _ := body["properties"].(map[string]any)
	priority, ok := props["rulePriority"].(float64)
	if !ok || priority < 0 || priority > 1000 {
		return http.StatusBadRequest, "InvalidRulePriority", "A governance rule's priority is from 0 to 1000."
	}
	if sets, _ := props["conditionSets"].([]any); len(sets) == 0 {
		return http.StatusBadRequest, "ConditionSetsRequired", "A governance rule gives at least one condition set."
	}
	prefix := strings.ToLower(m[1] + "/providers/microsoft.security/governancerules/")
	for key, res := range a.resources {
		if !strings.HasPrefix(key, prefix) || key == strings.ToLower(id) || strings.Contains(key[len(prefix):], "/") {
			continue
		}
		other, _ := (*res)["properties"].(map[string]any)
		if p, _ := other["rulePriority"].(float64); p == priority {
			return http.StatusBadRequest, "DuplicateRulePriority", "Another governance rule at this scope has priority " + strconv.FormatFloat(priority, 'f', -1, 64) + "."
		}
	}
	return 0, "", ""
}
