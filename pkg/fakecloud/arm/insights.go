// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Activity log profiles (Microsoft.Insights/logprofiles, API 2016-03-01,
// one subscription's activity log archived or streamed) and workbook
// templates (Microsoft.Insights/workbooktemplates, 2020-11-20), as far as
// the fake models them:
//
//   - a log profile is at a subscription (400 otherwise), one per
//     subscription (409 for a second name), names categories among Write,
//     Delete and Action and at least one location (400), archives to a
//     storage account or streams to an Event Hubs authorization rule (400
//     without either; 404 when the one it names is not there), and keeps 0
//     to 365 days (400);
//   - a workbook template gives its templateData and at least one gallery
//     (400).
//
// Not modelled: the activity log flowing, the global location, the
// template's content being a valid workbook, and the error codes (this
// fake's own).
var (
	logProfile       = regexp.MustCompile(`(?i)^(/subscriptions/[^/]+)/providers/microsoft\.insights/logprofiles/[^/]+$`)
	anyLogProfile    = regexp.MustCompile(`(?i)/providers/microsoft\.insights/logprofiles/[^/]+$`)
	workbookTemplate = regexp.MustCompile(`(?i)/providers/microsoft\.insights/workbooktemplates/[^/]+$`)
)

// checkInsights refuses a log profile or workbook template the rules above
// do not allow; code is "" when it is allowed or id is neither.
func (a *ARM) checkInsights(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	switch {
	case anyLogProfile.MatchString(id):
		m := logProfile.FindStringSubmatch(id)
		if m == nil {
			return http.StatusBadRequest, "InvalidLogProfileScope", "A log profile is at a subscription, not " + id + "."
		}
		for key := range a.resources {
			if logProfile.MatchString(key) && within(key, strings.ToLower(m[1])) && key != strings.ToLower(id) {
				return http.StatusConflict, "LogProfileExists", "The subscription already has a log profile, " + key + "."
			}
		}
		categories, _ := props["categories"].([]any)
		for _, c := range categories {
			switch c {
			case "Write", "Delete", "Action":
			default:
				return http.StatusBadRequest, "InvalidLogProfileCategory", "A log profile's categories are Write, Delete and Action."
			}
		}
		if locations, _ := props["locations"].([]any); len(locations) == 0 {
			return http.StatusBadRequest, "LogProfileLocationsMissing", "A log profile names at least one location (global for global events)."
		}
		storage, _ := props["storageAccountId"].(string)
		rule, _ := props["serviceBusRuleId"].(string)
		if storage == "" && rule == "" {
			return http.StatusBadRequest, "LogProfileDestinationMissing", "A log profile archives to a storage account or streams to an Event Hubs authorization rule."
		}
		for _, d := range []string{storage, rule} {
			if d != "" && !a.exists(d) {
				return http.StatusNotFound, "ResourceNotFound", "The resource " + d + " was not found."
			}
		}
		retention, _ := props["retentionPolicy"].(map[string]any)
		if days, ok := retention["days"].(float64); ok && (days < 0 || days > 365) {
			return http.StatusBadRequest, "InvalidRetention", "A log profile keeps 0 to 365 days."
		}
	case workbookTemplate.MatchString(id):
		if props["templateData"] == nil {
			return http.StatusBadRequest, "TemplateDataMissing", "A workbook template needs properties.templateData."
		}
		if galleries, _ := props["galleries"].([]any); len(galleries) == 0 {
			return http.StatusBadRequest, "GalleriesMissing", "A workbook template is in at least one gallery."
		}
	}
	return 0, "", ""
}
