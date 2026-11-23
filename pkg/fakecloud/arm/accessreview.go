// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Access reviews of Azure role assignments (Microsoft.Authorization, API
// 2021-12-01-preview) and the tenant's subscription policy
// (Microsoft.Subscription, API 2021-10-01), as far as the fake models them.
// Every rule is one the specs state:
//
//   - the PUT body of each is the resource's properties themselves, not an
//     object with a "properties" member (the specs' PUT parameters are
//     AccessReviewScheduleDefinitionProperties, AccessReviewHistoryDefinitionProperties,
//     AccessReviewScheduleSettings and PutTenantPolicyRequestProperties),
//     while a GET returns them under "properties": the fake keeps what was
//     sent under "properties", as Azure returns it;
//   - an access review schedule or history definition's name is a GUID
//     (400), as the API's examples and the portal make them;
//   - access review default settings exist once per scope, named default,
//     and cannot be deleted (no DELETE in the spec: 405);
//   - the subscription policy exists once per tenant, at
//     /providers/Microsoft.Subscription/policies/default (400 at any other
//     scope or name), its exempted principals are object IDs (GUIDs, 400),
//     and it cannot be deleted (no DELETE in the spec: 405).
//
// Not modelled: an access review running (instances, decisions, reviewers
// being notified, a history definition's report being made: the fake never
// fills instances or a status beyond what was sent), which scopes and
// principals Azure accepts for a review (any is), the subscription policy
// being enforced on subscriptions moving between tenants, who may write it
// (a tenant's global administrator, or an exempted principal), and the error
// codes (this fake's own).
var (
	accessReviewDefinition = regexp.MustCompile(`(?i)/providers/microsoft\.authorization/accessreview(schedule|history)definitions/([^/]+)$`)
	accessReviewSettings   = regexp.MustCompile(`(?i)/providers/microsoft\.authorization/accessreviewschedulesettings/([^/]+)$`)
	subscriptionPolicy     = regexp.MustCompile(`(?i)^(.*)/providers/microsoft\.subscription/policies/([^/]+)$`)
)

// checkFlatBody refuses an access review or subscription policy the rules
// above do not allow, and otherwise moves its flat PUT body under
// "properties"; code is "" when it is allowed or id is neither.
func checkFlatBody(id string, body map[string]any) (status int, code, message string) {
	switch {
	case accessReviewDefinition.MatchString(id):
		name := accessReviewDefinition.FindStringSubmatch(id)[2]
		if !guid.MatchString(name) {
			return http.StatusBadRequest, "InvalidAccessReviewId", "The name of an access review " + accessReviewDefinition.FindStringSubmatch(id)[1] + " definition must be a GUID, not " + name + "."
		}
	case accessReviewSettings.MatchString(id):
		if name := accessReviewSettings.FindStringSubmatch(id)[1]; !strings.EqualFold(name, "default") {
			return http.StatusBadRequest, "InvalidAccessReviewSettings", "Access review settings are named default, not " + name + "."
		}
	case subscriptionPolicy.MatchString(id):
		m := subscriptionPolicy.FindStringSubmatch(id)
		if m[1] != "" || !strings.EqualFold(m[2], "default") {
			return http.StatusBadRequest, "InvalidTenantPolicy", "The subscription policy is the tenant's, /providers/Microsoft.Subscription/policies/default, not " + id + "."
		}
		if principals, ok := body["exemptedPrincipals"].([]any); ok {
			for _, p := range principals {
				if s, _ := p.(string); !guid.MatchString(s) {
					return http.StatusBadRequest, "InvalidExemptedPrincipal", "An exempted principal is an object ID (a GUID)."
				}
			}
		}
	default:
		return 0, "", ""
	}
	props := map[string]any{}
	for k, v := range body {
		switch k {
		case "id", "name", "type", "properties":
		default:
			props[k] = v
			delete(body, k)
		}
	}
	body["properties"] = props
	return 0, "", ""
}

// undeletableSingleton is whether id is access review settings or the
// subscription policy, which Azure has no DELETE for.
func undeletableSingleton(id string) bool {
	return accessReviewSettings.MatchString(id) || subscriptionPolicy.MatchString(id)
}
