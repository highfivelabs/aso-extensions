// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Container registry tasks (Microsoft.ContainerRegistry/registries/tasks,
// API 2019-04-01) and connected registries (registries/connectedRegistries,
// 2025-11-01), as far as the fake models them:
//
//   - a task gives a platform with an os, and one step whose type is
//     Docker, EncodedTask or FileTask (400); a Docker step gives its
//     dockerFilePath (400); a timeout, when given, is 300 to 28,800 seconds
//     (400);
//   - a task's tokens (a step's contextAccessToken, a source trigger's
//     token and refreshToken) and its custom registries' Opaque user names
//     and passwords are kept aside under the task's ID and a path
//     (SharedKey: "/step/contextAccessToken", "/trigger/<name>/token",
//     "/credentials/<registry>/password") and never returned, as Azure does
//     not return them;
//   - a connected registry gives a mode (ReadWrite, ReadOnly, Registry or
//     Mirror, 400) and a parent whose sync token, and each client token,
//     exists (404).
//
// Not modelled: runs (a task never runs here), base image and timer
// triggers firing, the registry's SKU (Azure wants Premium for connected
// registries), activation of a connected registry, and the error codes
// (this fake's own).
var (
	registryTask      = regexp.MustCompile(`(?i)/providers/microsoft\.containerregistry/registries/[^/]+/tasks/[^/]+$`)
	connectedRegistry = regexp.MustCompile(`(?i)/providers/microsoft\.containerregistry/registries/[^/]+/connectedregistries/[^/]+$`)
)

// checkContainerRegistry refuses a task or connected registry the rules
// above do not allow; code is "" when it is allowed or id is neither.
func (a *ARM) checkContainerRegistry(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	switch {
	case registryTask.MatchString(id):
		platform, _ := props["platform"].(map[string]any)
		if os, _ := platform["os"].(string); os == "" {
			return http.StatusBadRequest, "TaskPlatformMissing", "A task needs properties.platform.os."
		}
		step, _ := props["step"].(map[string]any)
		switch t, _ := step["type"].(string); t {
		case "Docker":
			if p, _ := step["dockerFilePath"].(string); p == "" {
				return http.StatusBadRequest, "TaskStepInvalid", "A Docker step needs its dockerFilePath."
			}
		case "EncodedTask", "FileTask":
		default:
			return http.StatusBadRequest, "TaskStepInvalid", "A task needs one step, of type Docker, EncodedTask or FileTask."
		}
		if t, ok := props["timeout"].(float64); ok && (t < 300 || t > 28800) {
			return http.StatusBadRequest, "TaskTimeoutInvalid", "A task's timeout is 300 to 28800 seconds."
		}
		a.keep(id+"/step/contextAccessToken", step, "contextAccessToken")
		trigger, _ := props["trigger"].(map[string]any)
		sources, _ := trigger["sourceTriggers"].([]any)
		for _, s := range sources {
			st, _ := s.(map[string]any)
			name, _ := st["name"].(string)
			repo, _ := st["sourceRepository"].(map[string]any)
			auth, _ := repo["sourceControlAuthProperties"].(map[string]any)
			a.keep(id+"/trigger/"+name+"/token", auth, "token")
			a.keep(id+"/trigger/"+name+"/refreshToken", auth, "refreshToken")
		}
		creds, _ := props["credentials"].(map[string]any)
		custom, _ := creds["customRegistries"].(map[string]any)
		for registry, c := range custom {
			cc, _ := c.(map[string]any)
			for _, field := range []string{"userName", "password"} {
				if so, _ := cc[field].(map[string]any); so != nil {
					if t, _ := so["type"].(string); t == "" || strings.EqualFold(t, "Opaque") {
						a.keep(id+"/credentials/"+registry+"/"+field, so, "value")
					}
				}
			}
		}
	case connectedRegistry.MatchString(id):
		switch m, _ := props["mode"].(string); m {
		case "ReadWrite", "ReadOnly", "Registry", "Mirror":
		default:
			return http.StatusBadRequest, "ConnectedRegistryModeInvalid", "A connected registry's mode is ReadWrite, ReadOnly, Registry or Mirror."
		}
		parent, _ := props["parent"].(map[string]any)
		sync, _ := parent["syncProperties"].(map[string]any)
		token, _ := sync["tokenId"].(string)
		if token == "" {
			return http.StatusBadRequest, "ConnectedRegistrySyncTokenMissing", "A connected registry needs properties.parent.syncProperties.tokenId."
		}
		ids := []string{token}
		clients, _ := props["clientTokenIds"].([]any)
		for _, c := range clients {
			s, _ := c.(string)
			ids = append(ids, s)
		}
		for _, t := range ids {
			if !a.exists(t) {
				return http.StatusNotFound, "ResourceNotFound", "The token " + t + " was not found."
			}
		}
	}
	return 0, "", ""
}

// keep moves m[field], a string, aside under key (SharedKey), out of what is
// stored and returned.
func (a *ARM) keep(key string, m map[string]any, field string) {
	if m == nil {
		return
	}
	if s, ok := m[field].(string); ok {
		a.sharedKeys[strings.ToLower(key)] = s
		delete(m, field)
	}
}
