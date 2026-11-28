// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"encoding/json"
	"fmt"
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
//     (400), and the target exists (404);
//   - an online endpoint sends traffic only to its deployments that exist,
//     each share 0 to 100 and all of them 100 together (400) (f83);
//   - a batch endpoint's default deployment exists (400);
//   - a managed online deployment names its instance type (400);
//   - a job runs once: on a job that exists, a PUT may change its tags
//     (the spec: "For update case, the Tags in the definition passed in
//     will replace Tags in the existing job"), its display name,
//     description, properties and archive flag (what `az ml job update`
//     and archiving change); any other change is refused (409) (f85);
//   - a job's compute (computeId) exists (404);
//   - a schedule's cron expression has five fields (NCronTab, the spec's
//     format), and a recurrence's hours are 0 to 23 and its minutes 0 to
//     59 (400).
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
	mlOnline     = regexp.MustCompile(mlParent + `/onlineendpoints/[^/]+$`)
	mlBatch      = regexp.MustCompile(mlParent + `/batchendpoints/[^/]+$`)
	mlOnlineDep  = regexp.MustCompile(mlParent + `/onlineendpoints/[^/]+/deployments/[^/]+$`)
	mlImmutables = []string{"codeUri", "dataUri", "modelUri", "image", "build", "componentSpec", "dataType", "modelType"}
	mlJob        = regexp.MustCompile(mlParent + `/jobs/[^/]+$`)
	mlSchedule   = regexp.MustCompile(mlParent + `/schedules/[^/]+$`)
	// What a PUT may change on a job that exists.
	mlJobMutable = map[string]bool{"tags": true, "displayName": true, "description": true, "properties": true, "isArchived": true}
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
	case mlOnline.MatchString(id):
		traffic, _ := props["traffic"].(map[string]any)
		total := 0.0
		for name, v := range traffic {
			share, _ := v.(float64)
			if share < 0 || share > 100 {
				return bad("An endpoint's traffic to " + name + " is 0 to 100.")
			}
			total += share
			if share > 0 && !a.exists(id+"/deployments/"+name) {
				return bad("An endpoint's traffic names the deployment " + name + ", which does not exist.")
			}
		}
		if len(traffic) > 0 && total != 0 && total != 100 {
			return bad("An endpoint's traffic adds up to 100.")
		}
	case mlBatch.MatchString(id):
		defaults, _ := props["defaults"].(map[string]any)
		if name := stringOf(defaults["deploymentName"]); name != "" && !a.exists(id+"/deployments/"+name) {
			return bad("A batch endpoint's default deployment " + name + " does not exist.")
		}
	case mlOnlineDep.MatchString(id):
		if stringOf(props["endpointComputeType"]) == "Managed" && stringOf(props["instanceType"]) == "" {
			return bad("A managed online deployment names its instanceType.")
		}
	case mlJob.MatchString(id):
		if compute := stringOf(props["computeId"]); compute != "" && !a.exists(compute) {
			return http.StatusNotFound, "ResourceNotFound", "The compute " + compute + " was not found."
		}
		if old, ok := a.resources[strings.ToLower(id)]; ok {
			oldProps, _ := (*old)["properties"].(map[string]any)
			for _, m := range []map[string]any{oldProps, props} {
				for k := range m {
					if !mlJobMutable[k] && k != "provisioningState" && !sameJSON(oldProps[k], props[k]) {
						return http.StatusConflict, "Conflict", "A job runs once: only its tags, display name, description, properties and archive flag can change, not " + k + "; make a new job."
					}
				}
			}
		}
	case mlSchedule.MatchString(id):
		trigger, _ := props["trigger"].(map[string]any)
		switch stringOf(trigger["triggerType"]) {
		case "Cron":
			if len(strings.Fields(stringOf(trigger["expression"]))) != 5 {
				return bad("A cron trigger's expression has five fields (NCronTab): minutes, hours, days, months and days of the week.")
			}
		case "Recurrence":
			pattern, _ := trigger["schedule"].(map[string]any)
			for k, max := range map[string]float64{"hours": 23, "minutes": 59} {
				list, _ := pattern[k].([]any)
				for _, v := range list {
					if n, _ := v.(float64); n < 0 || n > max {
						return bad(fmt.Sprintf("A recurrence's %s are 0 to %v.", k, max))
					}
				}
			}
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
