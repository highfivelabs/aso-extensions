// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// Source control configurations
// (Microsoft.KubernetesConfiguration/sourceControlConfigurations, API
// 2023-05-01, the legacy Flux v1 GitOps configuration, on a managed or
// Arc-enabled cluster) and Kubernetes configuration private link scopes
// (privateLinkScopes and their privateEndpointConnections,
// 2024-11-01-preview), as far as the fake models them:
//
//   - a source control configuration is on an AKS cluster
//     (Microsoft.ContainerService/managedClusters) or an Arc-enabled one
//     (Microsoft.Kubernetes/connectedClusters), 400 otherwise; it gives a
//     repositoryUrl (400) and, when given, an operatorType of Flux (400);
//   - its configurationProtectedSettings are kept aside, each under the
//     configuration's ID and "/<name>" (SharedKey), and never returned, as
//     Azure does not return them;
//   - a private link scope names, when it gives one, a connected cluster
//     that exists (properties.clusterResourceId, 404).
//
// Not modelled: Flux running in the cluster, the SSH key Azure generates
// (repositoryPublicKey), compliance status, private endpoint connections
// arriving from a consumer, and the error codes (this fake's own).
var (
	sourceControlConfig = regexp.MustCompile(`(?i)^(.*)/providers/microsoft\.kubernetesconfiguration/sourcecontrolconfigurations/[^/]+$`)
	gitopsCluster       = regexp.MustCompile(`(?i)/providers/(microsoft\.containerservice/managedclusters|microsoft\.kubernetes/connectedclusters)/[^/]+$`)
	k8sPrivateLinkScope = regexp.MustCompile(`(?i)/providers/microsoft\.kubernetesconfiguration/privatelinkscopes/[^/]+$`)
)

// checkKubernetesConfiguration refuses a configuration or scope the rules
// above do not allow; code is "" when it is allowed or id is neither.
func (a *ARM) checkKubernetesConfiguration(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	if m := sourceControlConfig.FindStringSubmatch(id); m != nil {
		if !gitopsCluster.MatchString(m[1]) {
			return http.StatusBadRequest, "InvalidClusterType", "A source control configuration is on a managed or Arc-enabled Kubernetes cluster, not " + m[1] + "."
		}
		if u, _ := props["repositoryUrl"].(string); u == "" {
			return http.StatusBadRequest, "RepositoryUrlMissing", "A source control configuration needs properties.repositoryUrl."
		}
		if t, ok := props["operatorType"].(string); ok && t != "Flux" {
			return http.StatusBadRequest, "InvalidOperatorType", "A source control configuration's operatorType is Flux."
		}
		if settings, ok := props["configurationProtectedSettings"].(map[string]any); ok {
			for name, v := range settings {
				s, _ := v.(string)
				a.sharedKeys[strings.ToLower(id+"/"+name)] = s
			}
			delete(props, "configurationProtectedSettings")
		}
		return 0, "", ""
	}
	if k8sPrivateLinkScope.MatchString(id) {
		if c, _ := props["clusterResourceId"].(string); c != "" && !a.exists(c) {
			return http.StatusNotFound, "ResourceNotFound", "The cluster " + c + " was not found."
		}
	}
	return 0, "", ""
}
