// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"encoding/json"
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
//     (404);
//   - a linked service names others of the factory by reference as the
//     kinds above do (its integration runtime, connectVia; a credential;
//     the Key Vault linked service of a secret's store), and each exists
//     ("invalid reference", 400) (one kind per linked service type);
//   - a secure string (a SecretBase of type SecureString) has a value
//     (400), and Data Factory never returns it: what a GET or PUT returns
//     of a factory's child holds each secure string's value masked
//     (adfMasked);
//   - a dataset names its linked service, which exists ("invalid
//     reference", 400), and is of a type the dataset can read: a table or
//     collection dataset the store it is a table of (an AzureSqlTable an
//     AzureSqlDatabase linked service), a file format dataset the store
//     its location is in (an AzureBlobFSLocation an AzureBlobFS linked
//     service) (400) (one kind per dataset type);
//   - a pipeline names datasets, linked services, data flows and other
//     pipelines by reference, in its activities and the activities they
//     hold, and each exists ("invalid reference", 400); its activities'
//     names are unique in it, and an activity depends only on activities
//     of the pipeline (400) (f89);
//   - an Azure Blob Storage linked service takes at most one of
//     connectionString, sasUri and serviceEndpoint, which the spec says are
//     mutually exclusive (400).
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
	adfAuthoring   = regexp.MustCompile(`(?i)^(.*` + adfFactory + `)/(triggers|dataflows|adfcdcs|integrationruntimes|linkedservices|datasets|pipelines)/[^/]+$`)
	adfChild       = regexp.MustCompile(adfFactory + `/[^/]+/[^/]+$`)
	// adfMasked is what Data Factory returns in place of a secure string's value.
	adfMasked = "**********"
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

// adfDatasetStores is the linked service type a dataset of a table or
// collection type reads; adfLocationStores the one a file format dataset's
// location is in. A dataset type in neither is not checked.
var (
	adfDatasetStores = map[string]string{
		"AzureSqlTable":                   "AzureSqlDatabase",
		"AzureSqlDWTable":                 "AzureSqlDW",
		"CosmosDbSqlApiCollection":        "CosmosDb",
		"RestResource":                    "RestService",
		"HttpFile":                        "HttpServer",
		"AzureDatabricksDeltaLakeDataset": "AzureDatabricksDeltaLake",
	}
	adfLocationStores = map[string]string{
		"AzureBlobStorageLocation": "AzureBlobStorage",
		"AzureBlobFSLocation":      "AzureBlobFS",
		"HttpServerLocation":       "HttpServer",
	}
)

// checkDatasetStore refuses a dataset whose linked service (which exists:
// the reference rule runs first) is of a type it cannot read.
func (a *ARM) checkDatasetStore(factory string, props map[string]any) (status int, code, message string) {
	ls, _ := props["linkedServiceName"].(map[string]any)
	name := stringOf(ls["referenceName"])
	if name == "" || strings.HasPrefix(name, "@") {
		return 0, "", ""
	}
	want := adfDatasetStores[stringOf(props["type"])]
	if want == "" {
		tp, _ := props["typeProperties"].(map[string]any)
		loc, _ := tp["location"].(map[string]any)
		want = adfLocationStores[stringOf(loc["type"])]
	}
	if want == "" {
		return 0, "", ""
	}
	key := strings.ToLower(factory + "/linkedservices/" + name)
	r, ok := a.resources[key]
	if !ok {
		return 0, "", ""
	}
	lp, _ := (*r)["properties"].(map[string]any)
	if got := stringOf(lp["type"]); got != want {
		return http.StatusBadRequest, "BadRequest", "The linked service " + name + " is of type " + got + "; a " +
			stringOf(props["type"]) + " dataset here needs a " + want + " linked service."
	}
	return 0, "", ""
}

// adfActivityLists are the properties of an activity that hold
// activities (a ForEach's, an If's, a Switch's and its cases', an Until's).
var adfActivityLists = []string{"activities", "ifTrueActivities", "ifFalseActivities", "defaultActivities"}

// adfActivities returns every activity of a pipeline, those that control
// activities hold among them.
func adfActivities(list any, out *[]map[string]any) {
	items, _ := list.([]any)
	for _, it := range items {
		act, ok := it.(map[string]any)
		if !ok {
			continue
		}
		*out = append(*out, act)
		tp, _ := act["typeProperties"].(map[string]any)
		for _, k := range adfActivityLists {
			adfActivities(tp[k], out)
		}
		cases, _ := tp["cases"].([]any)
		for _, c := range cases {
			cm, _ := c.(map[string]any)
			adfActivities(cm["activities"], out)
		}
	}
}

// checkPipelineActivities refuses a pipeline whose activity names repeat or
// whose activity depends on one the pipeline does not have.
func checkPipelineActivities(props map[string]any) (status int, code, message string) {
	var acts []map[string]any
	adfActivities(props["activities"], &acts)
	names := map[string]bool{}
	for _, a := range acts {
		n := stringOf(a["name"])
		if names[n] {
			return http.StatusBadRequest, "BadRequest", "The pipeline has two activities named " + n + "."
		}
		names[n] = true
	}
	for _, a := range acts {
		deps, _ := a["dependsOn"].([]any)
		for _, d := range deps {
			dm, _ := d.(map[string]any)
			if on := stringOf(dm["activity"]); !names[on] {
				return http.StatusBadRequest, "BadRequest", "Activity '" + stringOf(a["name"]) + "' depends on activity '" + on + "', which the pipeline does not have."
			}
		}
	}
	return 0, "", ""
}

// adfSecureStrings returns the secure strings of a document (objects of
// type SecureString), wherever they are in it.
func adfSecureStrings(v any, out *[]map[string]any) {
	switch t := v.(type) {
	case map[string]any:
		if stringOf(t["type"]) == "SecureString" {
			*out = append(*out, t)
		}
		for _, x := range t {
			adfSecureStrings(x, out)
		}
	case []any:
		for _, x := range t {
			adfSecureStrings(x, out)
		}
	}
}

// renderDataFactory returns a factory child as Data Factory shows it: each
// secure string's value masked. ok is false when id is no factory child or
// it holds no secure string.
func renderDataFactory(id string, r resource) (resource, bool) {
	if !adfChild.MatchString(id) {
		return nil, false
	}
	var secure []map[string]any
	adfSecureStrings(map[string]any(r), &secure)
	if len(secure) == 0 {
		return nil, false
	}
	b, err := json.Marshal(r)
	if err != nil {
		return nil, false
	}
	var out resource
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, false
	}
	secure = secure[:0]
	adfSecureStrings(map[string]any(out), &secure)
	for _, s := range secure {
		s["value"] = adfMasked
	}
	return out, true
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
		if strings.EqualFold(m[2], "pipelines") {
			if status, code, msg := checkPipelineActivities(props); code != "" {
				return status, code, msg
			}
		}
		if strings.EqualFold(m[2], "datasets") {
			if status, code, msg := a.checkDatasetStore(m[1], props); code != "" {
				return status, code, msg
			}
		}
		var secure []map[string]any
		adfSecureStrings(props, &secure)
		for _, s := range secure {
			if stringOf(s["value"]) == "" {
				return bad("A secure string has a value.")
			}
		}
		linked, _ := props["typeProperties"].(map[string]any)
		if strings.EqualFold(m[2], "linkedservices") && stringOf(props["type"]) == "AzureBlobStorage" {
			n := 0
			for _, k := range []string{"connectionString", "sasUri", "serviceEndpoint"} {
				if linked[k] != nil {
					n++
				}
			}
			if n > 1 {
				return bad("An Azure Blob Storage linked service takes one of connectionString, sasUri and serviceEndpoint.")
			}
		}
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
