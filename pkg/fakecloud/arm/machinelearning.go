// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// A Machine Learning workspace's and registry's assets, datastores and
// outbound rules (Microsoft.MachineLearningServices, 2026-09-01;
// docs/plan.md f82), as far as the fake models them. Each rule is one the
// spec or Machine Learning's documentation states:
//
//   - an asset version (code, component, data, environment, model) is
//     immutable once made: a PUT that changes what it points at (its URI,
//     image, build or component spec) is refused (409); its description,
//     tags, stage and archive flag may change;
//   - an environment version names an image or a build (400);
//   - a datastore's credentials carry what their type needs: an account
//     key its key, a SAS credential its token, a service principal its
//     client and tenant IDs and secret, a certificate its thumbprint,
//     client and tenant IDs and certificate (400); a blob datastore names
//     its account and container (400);
//   - an outbound rule of type FQDN names a destination, a ServiceTag rule
//     its service tag, a PrivateEndpoint rule its target and sub-resource
//     (400), and the target exists (404).
//
// Private endpoint connections follow the shared rule (privateendpoints.go).
// Not modelled: uploading or validating what an asset points at, image
// builds, the managed network's provisioning, and the error codes (the
// fake's own).
var (
	mlParent     = `(?i)/providers/microsoft\.machinelearningservices/(workspaces|registries)/[^/]+`
	mlVersion    = regexp.MustCompile(mlParent + `/(codes|components|data|environments|models)/[^/]+/versions/[^/]+$`)
	mlDatastore  = regexp.MustCompile(mlParent + `/datastores/[^/]+$`)
	mlOutbound   = regexp.MustCompile(mlParent + `/outboundrules/[^/]+$`)
	mlImmutables = []string{"codeUri", "dataUri", "modelUri", "image", "build", "componentSpec", "dataType", "modelType"}
)

// checkMachineLearning refuses what the rules above do not allow; code is
// "" when it is allowed or id is none of these.
func (a *ARM) checkMachineLearning(id string, body map[string]any) (status int, code, message string) {
	bad := func(msg string) (int, string, string) {
		return http.StatusBadRequest, "BadRequest", msg
	}
	props, _ := body["properties"].(map[string]any)
	switch {
	case mlVersion.MatchString(id):
		if strings.EqualFold(mlVersion.FindStringSubmatch(id)[2], "environments") && props["image"] == nil && props["build"] == nil {
			return bad("An environment version names an image or a build.")
		}
		if old, ok := a.resources[strings.ToLower(id)]; ok {
			oldProps, _ := (*old)["properties"].(map[string]any)
			for _, k := range mlImmutables {
				if !sameJSON(oldProps[k], props[k]) {
					return http.StatusConflict, "Conflict", "An asset version is immutable: " + k + " cannot change; make a new version."
				}
			}
		}
	case mlDatastore.MatchString(id):
		if stringOf(props["datastoreType"]) == "AzureBlob" && (stringOf(props["accountName"]) == "" || stringOf(props["containerName"]) == "") {
			return bad("A blob datastore names its accountName and containerName.")
		}
		creds, _ := props["credentials"].(map[string]any)
		secrets, _ := creds["secrets"].(map[string]any)
		has := func(m map[string]any, keys ...string) bool {
			for _, k := range keys {
				if stringOf(m[k]) == "" {
					return false
				}
			}
			return true
		}
		switch t := stringOf(creds["credentialsType"]); t {
		case "None":
		case "AccountKey":
			if !has(secrets, "key") {
				return bad("Account key credentials name the key.")
			}
		case "Sas":
			if !has(secrets, "sasToken") {
				return bad("SAS credentials name the token.")
			}
		case "ServicePrincipal":
			if !has(creds, "clientId", "tenantId") || !has(secrets, "clientSecret") {
				return bad("Service principal credentials name the clientId, tenantId and the client secret.")
			}
		case "Certificate":
			if !has(creds, "clientId", "tenantId", "thumbprint") || !has(secrets, "certificate") {
				return bad("Certificate credentials name the clientId, tenantId, thumbprint and the certificate.")
			}
		default:
			return bad("A datastore's credentialsType is None, AccountKey, Sas, ServicePrincipal or Certificate, not " + t + ".")
		}
	case mlOutbound.MatchString(id):
		switch stringOf(props["type"]) {
		case "FQDN":
			if stringOf(props["destination"]) == "" {
				return bad("An FQDN outbound rule names its destination.")
			}
		case "ServiceTag":
			dest, _ := props["destination"].(map[string]any)
			if stringOf(dest["serviceTag"]) == "" {
				return bad("A ServiceTag outbound rule names its service tag.")
			}
		case "PrivateEndpoint":
			dest, _ := props["destination"].(map[string]any)
			target := stringOf(dest["serviceResourceId"])
			if target == "" || stringOf(dest["subresourceTarget"]) == "" {
				return bad("A PrivateEndpoint outbound rule names its serviceResourceId and subresourceTarget.")
			}
			if !a.exists(target) {
				return http.StatusNotFound, "ResourceNotFound", "The resource " + target + " was not found."
			}
		default:
			return bad("An outbound rule's type is FQDN, PrivateEndpoint or ServiceTag.")
		}
	}
	return 0, "", ""
}

// sameJSON is whether two decoded JSON values are equal.
func sameJSON(x, y any) bool {
	bx, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return string(bx) == string(by)
}
