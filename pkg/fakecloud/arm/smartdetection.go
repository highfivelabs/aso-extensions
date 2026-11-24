// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Application Insights smart detection rules
// (Microsoft.Insights/components/ProactiveDetectionConfigs, 2015-05-01;
// docs/plan.md f57), as far as the fake models them:
//
//   - a rule is one Application Insights defines, named as its ARM template
//     documentation and the spec's examples name them (slowpageloadtime,
//     slowserverresponsetime, longdependencyduration,
//     degradationinserverresponsetime, degradationindependencyduration and
//     the extension_ rules); any other name is refused (400);
//   - a rule cannot be deleted: the API has no DELETE (405); it lives as
//     long as its component.
//
// Not modelled: the rules running, mail being sent, a rule's definition
// (ruleDefinitions, Azure's own), and the error codes (the fake's own).
var (
	smartDetectionRule  = regexp.MustCompile(`(?i)/providers/microsoft\.insights/components/[^/]+/proactivedetectionconfigs/([^/]+)$`)
	smartDetectionRules = map[string]bool{
		"slowpageloadtime": true, "slowserverresponsetime": true, "longdependencyduration": true,
		"degradationinserverresponsetime": true, "degradationindependencyduration": true,
		"extension_traceseveritydetector": true, "extension_exceptionchangeextension": true,
		"extension_memoryleakextension": true, "extension_securityextensionspackage": true,
		"extension_billingdatavolumedailyspikeextension": true,
	}
)

// checkSmartDetection refuses a smart detection rule the rules above do not
// allow; code is "" when it is allowed or id is not one.
func (a *ARM) checkSmartDetection(id string, _ map[string]any) (status int, code, message string) {
	m := smartDetectionRule.FindStringSubmatch(id)
	if m == nil {
		return 0, "", ""
	}
	if !smartDetectionRules[strings.ToLower(m[1])] {
		return http.StatusBadRequest, "InvalidSmartDetectionRule", m[1] + " is not a smart detection rule of Application Insights."
	}
	return 0, "", ""
}

// isSmartDetectionRule says whether id is a smart detection rule, which
// cannot be deleted.
func isSmartDetectionRule(id string) bool { return smartDetectionRule.MatchString(id) }
