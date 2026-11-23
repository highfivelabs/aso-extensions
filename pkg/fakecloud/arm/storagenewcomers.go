// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// A storage account's connectors, data shares and blob access point
// configurations (Microsoft.Storage, 2026-06-01 and 2026-09-01; docs/plan.md
// f48), as far as the fake models them. Each rule is one the spec states:
//
//   - a connector's source type is "Not mutable once the Storage Connector
//     is created" (400);
//   - a new data share has its access policies and assets ("For Create -
//     This property is required and cannot be null"; an empty list is
//     allowed; 400);
//   - a blob access point configuration's source type "determines the
//     remaining shape of the source object": a configuration keeps the
//     type it was made with (400; the spec does not let a PATCH change it
//     either).
//
// Not modelled: the connection being tested (testConnection), the data
// share's consumers reading through a connector, an access point reaching
// its source, advanced platform metrics being emitted, context caches
// caching anything, and the error codes (the fake's own).
var (
	storageConnector   = regexp.MustCompile(`(?i)/providers/microsoft\.storage/storageaccounts/[^/]+/connectors/[^/]+$`)
	storageDataShare   = regexp.MustCompile(`(?i)/providers/microsoft\.storage/storageaccounts/[^/]+/datashares/[^/]+$`)
	storageAccessPoint = regexp.MustCompile(`(?i)/providers/microsoft\.storage/storageaccounts/[^/]+/blobaccesspointconfigurations/[^/]+$`)
)

// checkStorageNewcomers refuses what the rules above do not allow; code is
// "" when it is allowed or id is none of these.
func (a *ARM) checkStorageNewcomers(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	old := map[string]any{}
	if res, ok := a.resources[strings.ToLower(id)]; ok {
		old, _ = (*res)["properties"].(map[string]any)
	}
	sourceField := func(p map[string]any, field string) string {
		source, _ := p["source"].(map[string]any)
		s, _ := source[field].(string)
		return s
	}
	switch {
	case storageConnector.MatchString(id):
		if was, now := sourceField(old, "type"), sourceField(props, "type"); was != "" && !strings.EqualFold(was, now) {
			return http.StatusBadRequest, "InvalidConnectorUpdate", "A connector's source type cannot change once it is created (" + was + ", not " + now + ")."
		}
	case storageDataShare.MatchString(id):
		if len(old) > 0 {
			return 0, "", ""
		}
		for _, p := range []string{"accessPolicies", "assets"} {
			if _, ok := props[p].([]any); !ok {
				return http.StatusBadRequest, "MissingRequiredProperty", "A new data share needs properties." + p + " (an empty list if none)."
			}
		}
	case storageAccessPoint.MatchString(id):
		if was, now := sourceField(old, "sourceType"), sourceField(props, "sourceType"); was != "" && !strings.EqualFold(was, now) {
			return http.StatusBadRequest, "InvalidSourceTypeUpdate", "A blob access point configuration's source type cannot change (" + was + ", not " + now + ")."
		}
	}
	return 0, "", ""
}
