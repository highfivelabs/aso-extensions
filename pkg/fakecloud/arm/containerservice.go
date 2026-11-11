// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// AKS managed namespaces (Microsoft.ContainerService/managedClusters/managedNamespaces,
// API 2026-05-01), fleet managed namespaces (fleets/managedNamespaces,
// 2026-06-01) and node pool snapshots (snapshots), as far as the fake models
// them:
//
//   - a managed namespace's name is a Kubernetes namespace name (a DNS-1123
//     label) and not one of Kubernetes' own (kube-system, kube-public,
//     kube-node-lease), 400 otherwise;
//   - a snapshot names the agent pool it is taken of
//     (properties.creationData.sourceResourceId, 400 when missing or not an
//     agent pool, 404 when the pool is not there).
//
// Not modelled: the namespace inside the cluster, its quota and network
// policies taking effect, a fleet propagating the namespace to its members,
// adoption of a namespace that already exists, the node image a snapshot
// records, and the error codes (this fake's own).
var (
	managedNamespace = regexp.MustCompile(`(?i)/providers/microsoft\.containerservice/(managedclusters|fleets)/[^/]+/managednamespaces/([^/]+)$`)
	aksSnapshot      = regexp.MustCompile(`(?i)/providers/microsoft\.containerservice/snapshots/[^/]+$`)
	agentPool        = regexp.MustCompile(`(?i)/providers/microsoft\.containerservice/managedclusters/[^/]+/agentpools/[^/]+$`)
	dns1123Label     = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
)

// checkContainerService refuses a managed namespace or snapshot the rules
// above do not allow; code is "" when it is allowed or id is neither.
func (a *ARM) checkContainerService(id string, body map[string]any) (status int, code, message string) {
	if m := managedNamespace.FindStringSubmatch(id); m != nil {
		name := m[2]
		if !dns1123Label.MatchString(name) {
			return http.StatusBadRequest, "InvalidNamespaceName", "A managed namespace's name is a Kubernetes namespace name (a DNS-1123 label), not " + name + "."
		}
		switch name {
		case "kube-system", "kube-public", "kube-node-lease":
			return http.StatusBadRequest, "ReservedNamespaceName", name + " is Kubernetes' own namespace and cannot be managed."
		}
		return 0, "", ""
	}
	if aksSnapshot.MatchString(id) {
		props, _ := body["properties"].(map[string]any)
		creation, _ := props["creationData"].(map[string]any)
		source, _ := creation["sourceResourceId"].(string)
		if source == "" || !agentPool.MatchString(source) {
			return http.StatusBadRequest, "InvalidSnapshotSource", "A snapshot names the agent pool it is taken of in properties.creationData.sourceResourceId."
		}
		if !a.exists(source) {
			return http.StatusNotFound, "ResourceNotFound", "The agent pool " + source + " was not found."
		}
		if t, _ := props["snapshotType"].(string); t != "" && !strings.EqualFold(t, "NodePool") {
			return http.StatusBadRequest, "InvalidSnapshotType", "A snapshot's type is NodePool."
		}
	}
	return 0, "", ""
}
