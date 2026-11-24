// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"slices"
	"strings"
)

// Logic Apps workflows and integration accounts (Microsoft.Logic,
// 2019-05-01; docs/plan.md f74), as far as the fake models them. Each rule
// is one the spec or Logic Apps' documentation states:
//
//   - a workflow's definition, when given, names its $schema and
//     contentVersion (400); its integration account, when given, exists
//     (404);
//   - an integration account has a SKU, Free, Basic or Standard (400);
//   - a schema has a schemaType and a map a mapType (400), and each its
//     content (400);
//   - a partner's B2B business identities each have a qualifier and a value
//     (400);
//   - an agreement names a host and a guest partner of its account that
//     exist (404), host and guest identities that are business identities
//     of those partners (400), and carries the content of its type, aS2,
//     x12 or edifact (400);
//   - a certificate has a public certificate or a Key Vault key (400), the
//     vault existing (404);
//   - a batch configuration's release criteria name a message count, a
//     batch size or a recurrence (400);
//   - an assembly has its name and content (400).
//
// Not modelled: workflows running (triggers, actions, runs, the definition
// language beyond its two fields), the SKUs' artifact limits, AS2, X12 and
// EDIFACT messages, and the error codes (the fake's own).
var (
	logicWorkflow    = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.logic/workflows/[^/]+$`)
	logicAccount     = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.logic/integrationaccounts/[^/]+$`)
	logicAccountKid  = regexp.MustCompile(`(?i)^(/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.logic/integrationaccounts/[^/]+)/([^/]+)/[^/]+$`)
	logicAccountSkus = []string{"Free", "Basic", "Standard"}
	logicAgreements  = map[string]string{"as2": "aS2", "x12": "x12", "edifact": "edifact"}
)

// checkLogic refuses what the rules above do not allow; code is "" when it
// is allowed or id is none of these.
func (a *ARM) checkLogic(id string, body map[string]any) (status int, code, message string) {
	bad := func(msg string) (int, string, string) {
		return http.StatusBadRequest, "InvalidRequestContent", msg
	}
	notFound := func(what, id string) (int, string, string) {
		return http.StatusNotFound, "ResourceNotFound", "The " + what + " " + id + " was not found."
	}
	props, _ := body["properties"].(map[string]any)
	switch {
	case logicWorkflow.MatchString(id):
		if def, ok := props["definition"].(map[string]any); ok {
			if stringOf(def["$schema"]) == "" || stringOf(def["contentVersion"]) == "" {
				return bad("A workflow definition names its $schema and contentVersion.")
			}
		}
		account, _ := props["integrationAccount"].(map[string]any)
		if r := stringOf(account["id"]); r != "" && !a.exists(r) {
			return notFound("integration account", r)
		}
		return 0, "", ""
	case logicAccount.MatchString(id):
		sku, _ := body["sku"].(map[string]any)
		if name := stringOf(sku["name"]); !slices.Contains(logicAccountSkus, name) {
			return bad("An integration account's SKU is " + strings.Join(logicAccountSkus, ", ") + "; not '" + name + "'.")
		}
		return 0, "", ""
	}
	m := logicAccountKid.FindStringSubmatch(id)
	if m == nil {
		return 0, "", ""
	}
	account, kind := m[1], strings.ToLower(m[2])
	switch kind {
	case "schemas", "maps":
		field := map[string]string{"schemas": "schemaType", "maps": "mapType"}[kind]
		if stringOf(props[field]) == "" {
			return bad("'" + field + "' is required.")
		}
		if stringOf(props["content"]) == "" {
			return bad("'content' is required.")
		}
	case "assemblies":
		if stringOf(props["assemblyName"]) == "" || stringOf(props["content"]) == "" {
			return bad("An assembly has its assemblyName and content.")
		}
	case "partners":
		for _, ident := range logicIdentities(props) {
			if stringOf(ident["qualifier"]) == "" || stringOf(ident["value"]) == "" {
				return bad("A business identity has a qualifier and a value.")
			}
		}
	case "agreements":
		var partners [2]map[string]any
		for i, side := range []string{"host", "guest"} {
			name := stringOf(props[side+"Partner"])
			pid := account + "/partners/" + name
			r, ok := a.resources[strings.ToLower(pid)]
			if name == "" || !ok {
				return notFound(side+" partner", pid)
			}
			partners[i], _ = (*r)["properties"].(map[string]any)
			ident, _ := props[side+"Identity"].(map[string]any)
			if !slices.ContainsFunc(logicIdentities(partners[i]), func(p map[string]any) bool {
				return strings.EqualFold(stringOf(p["qualifier"]), stringOf(ident["qualifier"])) && stringOf(p["value"]) == stringOf(ident["value"])
			}) {
				return bad("The " + side + " identity is not a business identity of the partner " + name + ".")
			}
		}
		content, _ := props["content"].(map[string]any)
		key, ok := logicAgreements[strings.ToLower(stringOf(props["agreementType"]))]
		if !ok {
			return bad("An agreement's type is AS2, X12 or Edifact.")
		}
		if _, ok := content[key].(map[string]any); !ok {
			return bad("An agreement of type " + stringOf(props["agreementType"]) + " carries content." + key + ".")
		}
	case "certificates":
		key, _ := props["key"].(map[string]any)
		if stringOf(props["publicCertificate"]) == "" && key == nil {
			return bad("A certificate has a publicCertificate or a key.")
		}
		vault, _ := key["keyVault"].(map[string]any)
		if r := stringOf(vault["id"]); r != "" && !a.exists(r) {
			return notFound("key vault", r)
		}
	case "batchconfigurations":
		criteria, _ := props["releaseCriteria"].(map[string]any)
		if criteria["messageCount"] == nil && criteria["batchSize"] == nil && criteria["recurrence"] == nil {
			return bad("A batch configuration's release criteria name a messageCount, a batchSize or a recurrence.")
		}
	}
	return 0, "", ""
}

// logicIdentities are a partner's B2B business identities.
func logicIdentities(props map[string]any) []map[string]any {
	content, _ := props["content"].(map[string]any)
	b2b, _ := content["b2b"].(map[string]any)
	list, _ := b2b["businessIdentities"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, i := range list {
		m, _ := i.(map[string]any)
		out = append(out, m)
	}
	return out
}
