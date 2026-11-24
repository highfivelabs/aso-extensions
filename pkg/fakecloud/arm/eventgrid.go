// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Event Grid system topics, their event subscriptions, a namespace topic's
// event subscriptions and the MQTT broker's children of a namespace
// (Microsoft.EventGrid, 2025-02-15; docs/plan.md f63), as far as the fake
// models them. Each rule is one the spec or Azure's Event Grid
// documentation states:
//
//   - a system topic names its source (400), a resource that exists (404),
//     and a topicType (400); a source has one system topic (409);
//   - an event subscription has a destination (400); a destination named
//     by resourceId (an event hub, a storage queue, a Service Bus queue or
//     topic, a function, a hybrid connection, a namespace topic) exists
//     (404); a webhook's endpointUrl is an https URL (400);
//   - a namespace topic's subscription delivers by Queue or by Push, and a
//     Push names its destination (400);
//   - a topic space has one to ten topic templates (400); a client group
//     has a query (400); a CA certificate has its encodedCertificate (400);
//   - a permission binding names a topic space and a client group of its
//     namespace that exist (404), and a permission, Publisher or
//     Subscriber (400).
//
// Not modelled: events (nothing publishes here), the source's region a
// system topic must share, filter semantics, the endpoint validation
// handshake of a webhook, MQTT clients connecting, a namespace's MQTT
// broker being enabled, and the error codes (the fake's own).
var (
	egSystemTopic  = regexp.MustCompile(`(?i)/providers/microsoft\.eventgrid/systemtopics/[^/]+$`)
	egSubscription = regexp.MustCompile(`(?i)/providers/microsoft\.eventgrid/(systemtopics/[^/]+|namespaces/[^/]+/topics/[^/]+)/eventsubscriptions/[^/]+$`)
	egNSSub        = regexp.MustCompile(`(?i)/providers/microsoft\.eventgrid/namespaces/[^/]+/topics/[^/]+/eventsubscriptions/[^/]+$`)
	egNSChild      = regexp.MustCompile(`(?i)^(.*/providers/microsoft\.eventgrid/namespaces/[^/]+)/(topicspaces|clientgroups|cacertificates|permissionbindings|clients)/[^/]+$`)
)

// checkEventGrid refuses what the rules above do not allow; code is "" when
// it is allowed or id is none of these.
func (a *ARM) checkEventGrid(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	bad := func(msg string) (int, string, string) {
		return http.StatusBadRequest, "InvalidRequest", msg
	}
	switch {
	case egSystemTopic.MatchString(id):
		source, _ := props["source"].(string)
		if source == "" {
			return bad("A system topic names its source.")
		}
		if !a.exists(source) {
			return http.StatusNotFound, "ResourceNotFound", "The source " + source + " was not found."
		}
		if t, _ := props["topicType"].(string); t == "" {
			return bad("A system topic names its topicType.")
		}
		for k, r := range a.resources {
			if strings.EqualFold(k, id) || !egSystemTopic.MatchString(k) {
				continue
			}
			p, _ := (*r)["properties"].(map[string]any)
			if other, _ := p["source"].(string); strings.EqualFold(other, source) {
				return http.StatusConflict, "Conflict", "The source " + source + " has a system topic already: " + (*r)["id"].(string) + "."
			}
		}
	case egNSSub.MatchString(id):
		dc, _ := props["deliveryConfiguration"].(map[string]any)
		switch mode, _ := dc["deliveryMode"].(string); mode {
		case "Queue":
		case "Push":
			push, _ := dc["push"].(map[string]any)
			dest, _ := push["destination"].(map[string]any)
			if dest == nil {
				return bad("A Push subscription names its destination.")
			}
			return a.checkEventGridDestination(dest)
		default:
			return bad("A namespace topic's subscription delivers by Queue or Push.")
		}
	case egSubscription.MatchString(id):
		dest, _ := props["destination"].(map[string]any)
		if dest == nil {
			return bad("An event subscription has a destination.")
		}
		return a.checkEventGridDestination(dest)
	}
	m := egNSChild.FindStringSubmatch(id)
	if m == nil {
		return 0, "", ""
	}
	switch strings.ToLower(m[2]) {
	case "topicspaces":
		if t, _ := props["topicTemplates"].([]any); len(t) == 0 || len(t) > 10 {
			return bad("A topic space has one to ten topic templates.")
		}
	case "clientgroups":
		if q, _ := props["query"].(string); q == "" {
			return bad("A client group has a query.")
		}
	case "cacertificates":
		if c, _ := props["encodedCertificate"].(string); c == "" {
			return bad("A CA certificate has its encodedCertificate.")
		}
	case "permissionbindings":
		if p, _ := props["permission"].(string); p != "Publisher" && p != "Subscriber" {
			return bad("A permission binding's permission is Publisher or Subscriber.")
		}
		for _, ref := range []struct{ prop, segment string }{{"topicSpaceName", "topicSpaces"}, {"clientGroupName", "clientGroups"}} {
			name, _ := props[ref.prop].(string)
			if name == "" || !a.exists(m[1]+"/"+ref.segment+"/"+name) {
				return http.StatusNotFound, "ResourceNotFound", "The " + ref.segment + " " + name + " of " + m[1] + " was not found."
			}
		}
	}
	return 0, "", ""
}

// checkEventGridDestination refuses a destination that names a resource
// that does not exist or a webhook that is not https.
func (a *ARM) checkEventGridDestination(dest map[string]any) (status int, code, message string) {
	p, _ := dest["properties"].(map[string]any)
	if dest["endpointType"] == "WebHook" {
		u, err := url.Parse(stringOf(p["endpointUrl"]))
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return http.StatusBadRequest, "InvalidRequest", "A webhook's endpointUrl is an https URL."
		}
		return 0, "", ""
	}
	if id := stringOf(p["resourceId"]); id != "" && !a.exists(id) {
		return http.StatusNotFound, "ResourceNotFound", "The destination " + id + " was not found."
	}
	return 0, "", ""
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}
