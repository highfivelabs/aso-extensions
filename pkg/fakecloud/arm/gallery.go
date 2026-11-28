// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Azure Compute Gallery, dedicated hosts and restore point collections
// (Microsoft.Compute, APIs 2026-03-03 and 2025-04-01), as far as the fake
// models them. Every rule is one the specs' descriptions state:
//
//   - a gallery's name has letters, digits, dots and underscores only (400);
//   - an image version's, application version's or script version's name
//     is MajorVersion.MinorVersion.Patch, three integers (400);
//   - an image version names its source (a managed image, a virtual
//     machine, another image version, or OS disk snapshot or disk), which
//     must exist (404, as for any missing reference; a blob URI source is
//     not checked);
//   - an application version gives the blob its package is read from,
//     publishingProfile.source.mediaLink, and a script version its script's,
//     publishingProfile.source.scriptLink (400);
//   - a restore point collection's source (a virtual machine) exists (404;
//     docs/plan.md f45);
//   - a host group has 1 to 3 platform fault domains (400).
//
// Not modelled: replication to target regions (a version is Succeeded at
// once), image definitions matching their versions' OS state and
// generation, a script's parameters, in-VM access control rules being
// enforced, restore points being taken (a restore point is Succeeded at
// once, with no disk restore points), a host's SKU being available, quota,
// and the error codes (the fake's own).
var (
	galleryType        = regexp.MustCompile(`(?i)/providers/microsoft\.compute/galleries/([^/]+)$`)
	galleryVersionType = regexp.MustCompile(`(?i)/providers/microsoft\.compute/galleries/[^/]+/(images|applications|scripts)/[^/]+/versions/([^/]+)$`)
	restorePointColl   = regexp.MustCompile(`(?i)/providers/microsoft\.compute/restorepointcollections/[^/]+$`)
	galleryName        = regexp.MustCompile(`^[A-Za-z0-9._]+$`)
	semanticVersion    = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	hostGroupType      = regexp.MustCompile(`(?i)/providers/microsoft\.compute/hostgroups/[^/]+$`)
)

// checkGallery refuses a gallery, a version or a host group the rules
// above do not allow; code is "" when it is allowed or id is none of them.
func (a *ARM) checkGallery(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	if m := galleryType.FindStringSubmatch(id); m != nil {
		if !galleryName.MatchString(m[1]) {
			return http.StatusBadRequest, "InvalidGalleryName", "A gallery's name has letters, digits, dots and underscores only, not " + m[1] + "."
		}
		return 0, "", ""
	}
	if restorePointColl.MatchString(id) {
		source, _ := props["source"].(map[string]any)
		if vm, _ := source["id"].(string); vm != "" && !a.exists(vm) {
			return http.StatusNotFound, "ResourceNotFound", "The restore point collection's source " + vm + " was not found."
		}
		return 0, "", ""
	}
	if hostGroupType.MatchString(id) {
		if n, ok := props["platformFaultDomainCount"].(float64); !ok || n < 1 || n > 3 {
			return http.StatusBadRequest, "InvalidFaultDomainCount", "A host group has 1 to 3 platform fault domains."
		}
		return 0, "", ""
	}
	m := galleryVersionType.FindStringSubmatch(id)
	if m == nil {
		return 0, "", ""
	}
	if !semanticVersion.MatchString(m[2]) {
		return http.StatusBadRequest, "InvalidVersionName", "A gallery version's name is MajorVersion.MinorVersion.Patch, not " + m[2] + "."
	}
	if strings.EqualFold(m[1], "scripts") {
		profile, _ := props["publishingProfile"].(map[string]any)
		source, _ := profile["source"].(map[string]any)
		if link, _ := source["scriptLink"].(string); link == "" {
			return http.StatusBadRequest, "ScriptLinkRequired", "A script version gives its script's blob, publishingProfile.source.scriptLink."
		}
		return 0, "", ""
	}
	if m[1] == "applications" || m[1] == "Applications" {
		profile, _ := props["publishingProfile"].(map[string]any)
		source, _ := profile["source"].(map[string]any)
		if link, _ := source["mediaLink"].(string); link == "" {
			return http.StatusBadRequest, "MediaLinkRequired", "An application version gives its package's blob, publishingProfile.source.mediaLink."
		}
		return 0, "", ""
	}
	storage, _ := props["storageProfile"].(map[string]any)
	var sources []string
	if source, _ := storage["source"].(map[string]any); source != nil {
		for _, k := range []string{"id", "virtualMachineId"} {
			if s, _ := source[k].(string); s != "" {
				sources = append(sources, s)
			}
		}
	}
	if osDisk, _ := storage["osDiskImage"].(map[string]any); osDisk != nil {
		if source, _ := osDisk["source"].(map[string]any); source != nil {
			if s, _ := source["id"].(string); s != "" {
				sources = append(sources, s)
			}
		}
	}
	if len(sources) == 0 {
		return http.StatusBadRequest, "SourceRequired", "An image version names its source: an image, a virtual machine, an image version, or an OS disk or snapshot."
	}
	for _, s := range sources {
		if !a.exists(s) {
			return http.StatusNotFound, "ResourceNotFound", "The image version's source " + s + " was not found."
		}
	}
	return 0, "", ""
}
