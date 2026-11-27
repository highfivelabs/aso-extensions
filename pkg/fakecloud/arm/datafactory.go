// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"slices"
	"strings"
)

// A Data Factory's managed virtual network, managed private endpoints,
// credentials and global parameters (Microsoft.DataFactory, 2018-06-01;
// docs/plan.md f78), as far as the fake models them. Each rule is one the
// spec or Data Factory's documentation states:
//
//   - a factory's managed virtual network and its global parameters are
//     named default (400);
//   - a managed private endpoint names what it connects to (400), which
//     exists (404), and a group ID (400);
//   - a managed identity credential names its user-assigned identity
//     (400), which exists (404); a service principal credential names the
//     principal, its key and its tenant (400);
//   - a global parameter has a type, Object, String, Int, Float, Bool or
//     Array (400);
//   - a trigger, data flow, change data capture or integration runtime
//     names others of the factory by reference (a pipeline, dataset,
//     linked service, data flow, integration runtime, credential or managed
//     virtual network by its referenceName), and each exists: Data Factory
//     refuses a document naming what is not there ("invalid reference",
//     400) (f86);
//   - a linked self-hosted integration runtime's shared runtime exists
//     (404).
//
// A factory's global parameters are kept with no provisioningState in their
// properties, which are a map of the parameters (propertiesAreAMap).
//
// Not modelled: the managed network's isolation, a managed private
// endpoint's approval by the target, credentials being used by linked
// services, a trigger's start and stop (a trigger made by PUT is stopped),
// runs, an integration runtime's nodes and state, references written as
// expressions, and the error codes (the fake's own).
var (
	adfFactory     = `(?i)/providers/microsoft\.datafactory/factories/[^/]+`
	adfManagedVNet = regexp.MustCompile(adfFactory + `/managedvirtualnetworks/([^/]+)$`)
	adfMPE         = regexp.MustCompile(adfFactory + `/managedvirtualnetworks/[^/]+/managedprivateendpoints/[^/]+$`)
	adfCredential  = regexp.MustCompile(adfFactory + `/credentials/[^/]+$`)
	adfGlobalParam = regexp.MustCompile(adfFactory + `/globalparameters/([^/]+)$`)
	adfParamTypes  = []string{"Object", "String", "Int", "Float", "Bool", "Array"}
	adfAuthoring   = regexp.MustCompile(`(?i)^(.*` + adfFactory + `)/(triggers|dataflows|adfcdcs|integrationruntimes)/[^/]+$`)
	// The collection a reference's type names.
	adfReferenceCollections = map[string]string{
		"PipelineReference":              "pipelines",
		"DatasetReference":               "datasets",
		"LinkedServiceReference":         "linkedservices",
		"DataFlowReference":              "dataflows",
		"IntegrationRuntimeReference":    "integrationRuntimes",
		"CredentialReference":            "credentials",
		"ManagedVirtualNetworkReference": "managedVirtualNetworks",
	}
)

// adfReferences returns the factory paths ("pipelines/train") a document
// names by reference, wherever they are in it.
func adfReferences(v any, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		if coll, ok := adfReferenceCollections[stringOf(t["type"])]; ok {
			if name := stringOf(t["referenceName"]); name != "" && !strings.HasPrefix(name, "@") {
				*out = append(*out, coll+"/"+name)
			}
		}
		for _, x := range t {
			adfReferences(x, out)
		}
	case []any:
		for _, x := range t {
			adfReferences(x, out)
		}
	}
}

// propertiesAreAMap is whether a resource's properties are a map of named
// entries rather than an object (a factory's global parameters), to which
// the fake adds no provisioningState, as Azure adds none.
func propertiesAreAMap(id string) bool {
	return adfGlobalParam.MatchString(id)
}

// checkDataFactory refuses what the rules above do not allow; code is ""
// when it is allowed or id is none of these.
func (a *ARM) checkDataFactory(id string, body map[string]any) (status int, code, message string) {
	bad := func(msg string) (int, string, string) {
		return http.StatusBadRequest, "BadRequest", msg
	}
	notFound := func(what, id string) (int, string, string) {
		return http.StatusNotFound, "ResourceNotFound", "The " + what + " " + id + " was not found."
	}
	props, _ := body["properties"].(map[string]any)
	if m := adfAuthoring.FindStringSubmatch(id); m != nil {
		var refs []string
		adfReferences(props, &refs)
		for _, ref := range refs {
			if !a.exists(m[1] + "/" + ref) {
				return bad("The document creation or update failed because of invalid reference '" + ref[strings.Index(ref, "/")+1:] + "'.")
			}
		}
		linked, _ := props["typeProperties"].(map[string]any)
		info, _ := linked["linkedInfo"].(map[string]any)
		if shared := stringOf(info["resourceId"]); shared != "" && !a.exists(shared) {
			return notFound("integration runtime", shared)
		}
	}
	switch {
	case adfMPE.MatchString(id):
		target := stringOf(props["privateLinkResourceId"])
		if target == "" || stringOf(props["groupId"]) == "" {
			return bad("A managed private endpoint names its privateLinkResourceId and groupId.")
		}
		if !a.exists(target) {
			return notFound("resource", target)
		}
	case adfManagedVNet.MatchString(id):
		if name := adfManagedVNet.FindStringSubmatch(id)[1]; name != "default" {
			return bad("A factory's managed virtual network is named default, not " + name + ".")
		}
	case adfCredential.MatchString(id):
		tp, _ := props["typeProperties"].(map[string]any)
		switch stringOf(props["type"]) {
		case "ManagedIdentity":
			r := stringOf(tp["resourceId"])
			if r == "" {
				return bad("A managed identity credential names its user-assigned identity.")
			}
			if !a.exists(r) {
				return notFound("identity", r)
			}
		case "ServicePrincipal":
			if tp["servicePrincipalId"] == nil || tp["servicePrincipalKey"] == nil || tp["tenant"] == nil {
				return bad("A service principal credential names its servicePrincipalId, servicePrincipalKey and tenant.")
			}
		default:
			return bad("A credential is ManagedIdentity or ServicePrincipal.")
		}
	case adfGlobalParam.MatchString(id):
		if name := adfGlobalParam.FindStringSubmatch(id)[1]; name != "default" {
			return bad("A factory's global parameters are named default, not " + name + ".")
		}
		for name, p := range props {
			pm, _ := p.(map[string]any)
			if !slices.Contains(adfParamTypes, stringOf(pm["type"])) {
				return bad("The global parameter " + name + " has no type of Object, String, Int, Float, Bool or Array.")
			}
		}
	}
	return 0, "", ""
}
