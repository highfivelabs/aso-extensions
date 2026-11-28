// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
)

// Defender for Cloud custom assessments (Microsoft.Security, API
// 2025-05-04), as far as the fake models them. Every rule is one the spec's
// descriptions state:
//
//   - an assessment metadata's name is its key, a GUID (400);
//   - an assessment is named after the key of the assessment metadata it
//     reports on, which must exist in the resource's subscription (404; the
//     fake has no built-in assessments, so a test makes the metadata first);
//   - an assessment gives its status code (400).
//
// Not modelled: built-in assessment metadata, the secure score, an
// assessment's status showing in the portal or expiring, and the error
// codes (the fake's own).
var (
	assessmentMetadata = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/providers/microsoft\.security/assessmentmetadata/([^/]+)$`)
	assessment         = regexp.MustCompile(`(?i)^/subscriptions/([^/]+)(/.*)?/providers/microsoft\.security/assessments/([^/]+)$`)
)

// checkAssessments refuses an assessment metadata or an assessment the
// rules above do not allow; code is "" when it is allowed or id is neither.
func (a *ARM) checkAssessments(id string, body map[string]any) (status int, code, message string) {
	if m := assessmentMetadata.FindStringSubmatch(id); m != nil {
		if !guid.MatchString(m[1]) {
			return http.StatusBadRequest, "InvalidAssessmentKey", "An assessment metadata's name is its key, a GUID, not " + m[1] + "."
		}
		return 0, "", ""
	}
	m := assessment.FindStringSubmatch(id)
	if m == nil {
		return 0, "", ""
	}
	metadata := "/subscriptions/" + m[1] + "/providers/Microsoft.Security/assessmentMetadata/" + m[3]
	if !a.exists(metadata) {
		return http.StatusNotFound, "AssessmentMetadataNotFound", "No assessment metadata " + metadata + " for the assessment " + m[3] + "."
	}
	props, _ := body["properties"].(map[string]any)
	if st, _ := props["status"].(map[string]any); st["code"] == nil {
		return http.StatusBadRequest, "AssessmentStatusRequired", "An assessment gives its status code."
	}
	return 0, "", ""
}
