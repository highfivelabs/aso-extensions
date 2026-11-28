// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Container group profiles and NGroups (Microsoft.ContainerInstance,
// 2026-07-01; docs/plan.md f67), as far as the fake models them, from the
// spec's required properties and Azure Container Instances' documentation:
//
//   - a profile has an osType and at least one container (400); each
//     container has a name, unique in the profile, an image and resource
//     requests of CPU and memory (400);
//   - an NGroup's container group profiles each name a profile (400) that
//     exists (404), and its elastic profile's desiredCount is not negative
//     (400).
//
// Not modelled: container groups being made from the profiles (an NGroup
// keeps its count here only as a property), images, regions' SKUs and
// quotas, the revision a profile's PUT makes, and the error codes (the
// fake's own).
var (
	aciProfile = regexp.MustCompile(`(?i)/providers/microsoft\.containerinstance/containergroupprofiles/[^/]+$`)
	aciNGroup  = regexp.MustCompile(`(?i)/providers/microsoft\.containerinstance/ngroups/[^/]+$`)
)

// checkContainerInstance refuses a profile or an NGroup the rules above do
// not allow; code is "" when it is allowed or id is neither.
func (a *ARM) checkContainerInstance(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	bad := func(msg string) (int, string, string) {
		return http.StatusBadRequest, "InvalidContainerGroupProfile", msg
	}
	switch {
	case aciProfile.MatchString(id):
		if stringOf(props["osType"]) == "" {
			return bad("A container group profile has an osType.")
		}
		containers, _ := props["containers"].([]any)
		if len(containers) == 0 {
			return bad("A container group profile has at least one container.")
		}
		seen := map[string]bool{}
		for _, c := range containers {
			cm, _ := c.(map[string]any)
			name := strings.ToLower(stringOf(cm["name"]))
			if name == "" || seen[name] {
				return bad("Each container has a name, unique in the profile.")
			}
			seen[name] = true
			cp, _ := cm["properties"].(map[string]any)
			res, _ := cp["resources"].(map[string]any)
			req, _ := res["requests"].(map[string]any)
			if stringOf(cp["image"]) == "" || req["cpu"] == nil || req["memoryInGB"] == nil {
				return bad("The container " + name + " has an image and requests CPU and memory.")
			}
		}
	case aciNGroup.MatchString(id):
		profiles, _ := props["containerGroupProfiles"].([]any)
		for _, p := range profiles {
			pm, _ := p.(map[string]any)
			ref := refID(pm, "resource")
			if ref == "" {
				return http.StatusBadRequest, "InvalidNGroup", "An NGroup's container group profile names a profile."
			}
			if !a.exists(ref) {
				return http.StatusNotFound, "ResourceNotFound", "The container group profile " + ref + " was not found."
			}
		}
		elastic, _ := props["elasticProfile"].(map[string]any)
		if n, ok := elastic["desiredCount"].(float64); ok && n < 0 {
			return http.StatusBadRequest, "InvalidNGroup", "An NGroup's desiredCount is not negative."
		}
	}
	return 0, "", ""
}
