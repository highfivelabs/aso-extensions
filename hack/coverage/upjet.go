// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// The Upjet facts (HFL-697): hack/upjet/provider-upjet-azure.json, the
// facts factory reads from Crossplane's provider-upjet-azure and folds onto
// this repository's API resources (factory's upjet/README.md). This file
// reads them against our kinds as they are now and writes what they say,
// with each fact's line in the slice, merged by the stricter rule
// (factory docs/design.md 5.4): where Upjet and we disagree it is listed,
// never overridden silently.
//
//   - references (HFL-699): each Upjet reference of a resource we serve,
//     against our kind's fields: a Kubernetes reference already, the owner,
//     a string field that holds an ARM ID and is not a reference (a gap,
//     labelled `$referenceType: arm` in generator/azure-arm/ or kept, with
//     the reason, in hack/coverage/upjet.yaml), or a field we lack;
//   - secrets: Upjet's Sensitive overrides against our fields;
//   - identifiers: Upjet's ID template against our ARM type;
//   - late-init ignores and list map keys, against what ASO does;
//   - examples (HFL-700): each hand-written example's values mapped onto
//     our kind's CRD, written to docs/coverage/upjet-examples.json, which
//     test/e2e/arm_upjet_examples_test.go runs on the fake cloud;
//   - the per-kind ledger (HFL-701): where each kind of ours is tested,
//     beside Upjet's tested flag;
//   - gaps: resources Upjet has and neither ASO nor we serve, ranked.

const (
	upjetFile      = "hack/upjet/provider-upjet-azure.json"
	upjetDecisions = "hack/coverage/upjet.yaml"
)

type upjetSlice struct {
	Provider struct {
		Repo   string `json:"repo"`
		Tag    string `json:"tag"`
		Commit string `json:"commit"`
	} `json:"provider"`
	Entries []*upjetEntry `json:"entries"`
	NoUpjet []string      `json:"noUpjet"`
}

type upjetEntry struct {
	Ours struct {
		Service  string `json:"service"`
		Resource string `json:"resource"`
	} `json:"ours"`
	Facts struct {
		Terraform    string `json:"terraform"`
		Tested       bool   `json:"tested"`
		ExternalName struct {
			Style    string `json:"style"`
			Template string `json:"template"`
			At       string `json:"at"`
		} `json:"externalName"`
		References      []*upjetRef `json:"references"`
		LateInitIgnored []struct {
			Field string `json:"field"`
			At    string `json:"at"`
		} `json:"lateInitIgnored"`
		SchemaOverrides []struct {
			Field    string `json:"field"`
			Property string `json:"property"`
			Value    string `json:"value"`
			At       string `json:"at"`
		} `json:"schemaOverrides"`
		ListMapKeys []struct {
			Field string   `json:"field"`
			Keys  []string `json:"keys"`
			At    string   `json:"at"`
		} `json:"listMapKeys"`
		Examples []*upjetExample `json:"examples"`
	} `json:"facts"`
	line int // of the entry in the slice
}

type upjetRef struct {
	Field     string `json:"field"`
	Target    string `json:"target"`
	Extractor string `json:"extractor"`
	Source    string `json:"source"`
	At        string `json:"at"`
	line      int
}

type upjetExample struct {
	ID                 string         `json:"id"`
	At                 string         `json:"at"`
	Origin             string         `json:"origin"`
	ManualIntervention string         `json:"manualIntervention"`
	Values             map[string]any `json:"values"`
	line               int
}

// readUpjet reads the slice and finds the line of each entry, reference
// and example in it, for provenance.
func readUpjet(root string) (*upjetSlice, error) {
	b, err := os.ReadFile(filepath.Join(root, upjetFile))
	if err != nil {
		return nil, err
	}
	var s upjetSlice
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", upjetFile, err)
	}
	// Each entry starts at a line `  {` inside "entries"; the slice is
	// written indented, one key per line.
	start := bytes.Index(b, []byte(`"entries": [`))
	if start < 0 {
		return nil, fmt.Errorf("%s: no entries", upjetFile)
	}
	lineAt := func(off int) int { return bytes.Count(b[:off], []byte("\n")) + 1 }
	pos := start
	for _, e := range s.Entries {
		i := bytes.Index(b[pos:], []byte(`"ours": {`))
		if i < 0 {
			return nil, fmt.Errorf("%s: cannot place entry %s", upjetFile, e.Ours.Resource)
		}
		pos += i
		e.line = lineAt(pos) - 1
		end := bytes.Index(b[pos+1:], []byte(`"ours": {`))
		if end < 0 {
			end = len(b)
		} else {
			end += pos + 1
		}
		// The references come before the examples in an entry.
		cur := pos
		place := func(at string) int {
			k := bytes.Index(b[cur:end], []byte(`"at": `+strconv.Quote(at)))
			if k < 0 {
				return e.line
			}
			cur += k + 1
			return lineAt(cur)
		}
		for _, r := range e.Facts.References {
			r.line = place(r.At)
		}
		for _, x := range e.Facts.Examples {
			x.line = place(x.At)
		}
		pos++
	}
	return &s, nil
}

func sliceAt(line int) string { return upjetFile + ":" + strconv.Itoa(line) }

// upjetDecisionFile is hack/coverage/upjet.yaml, written by hand: what was
// done with each disagreement, with the reason.
type upjetDecisionFile struct {
	// References lists Upjet references to string fields of ours: labelled
	// (applied: the generator file that now makes it a reference) or kept
	// a string (keep: why).
	References []struct {
		Kind    string `yaml:"kind"`
		Field   string `yaml:"field"`
		Applied string `yaml:"applied"`
		Keep    string `yaml:"keep"`
		// Match is our field the reference is, when no name rule finds it
		// (a path of the CRD's spec, or "owner"), with Why.
		Match string `yaml:"match"`
		Why   string `yaml:"why"`
	} `yaml:"references"`
	// Examples lists hand examples that do not run on the fake, with why.
	Examples []struct {
		Kind    string `yaml:"kind"`
		Example string `yaml:"example"`
		Skip    string `yaml:"skip"`
	} `yaml:"examples"`
	// Secrets lists Sensitive overrides we do not follow, with why.
	Secrets []struct {
		Kind  string `yaml:"kind"`
		Field string `yaml:"field"`
		Keep  string `yaml:"keep"`
	} `yaml:"secrets"`
}

// upjetRefRow is one Upjet reference on one kind of ours.
type upjetRefRow struct {
	kind, resource, field, target, class, ours, note, at string
	extractor                                            string
	served                                               bool
}

// Reference classes.
const (
	refReference = "reference" // our field is a Kubernetes reference
	refOwner     = "owner"     // the target is the resource's owner (a parent or its resource group)
	refString    = "string"    // our field holds an ARM ID as a string: a gap, decided in upjet.yaml
	refKept      = "kept"      // a string on purpose (upjet.yaml says why)
	refName      = "name"      // Upjet names the target by name; ARM takes no ID there
	refMissing   = "missing"   // no field of ours matches
	refValue     = "value"     // Upjet copies another value of the target (a URI, an endpoint)
	refOther     = "other"     // Upjet's resource is another ARM type than ours (its ID template says so)
)

type upjetResult struct {
	refs     []*upjetRefRow
	secrets  [][]string
	ids      [][]string
	lateInit [][]string
	listMaps [][]string
	examples []*upjetFixture
	exRows   [][]string
	ledger   [][]string
	gaps     [][]string
	stats    map[string]int
}

// upjetFixture is one hand example as our kind's spec; the e2e test
// (test/e2e/arm_upjet_examples_test.go) runs each on the fake cloud.
type upjetFixture struct {
	Group   string         `json:"group"`
	Version string         `json:"version"`
	Kind    string         `json:"kind"`
	Example string         `json:"example"`
	At      string         `json:"at"`
	Slice   string         `json:"slice"`
	Spec    map[string]any `json:"spec"`
	Dropped []string       `json:"dropped,omitempty"`
}

var extractParam = regexp.MustCompile(`ExtractParamPath\("([a-z_]+)"`)

var idSuffix = regexp.MustCompile(`(?i)(resourceids|resourceid|references|reference|armids|armid|ids|id)$`)

// upjet joins the slice with our kinds as they are now.
func upjet(root string, rows []*ResRow, ours []*Kind, prio Priority) (*upjetResult, error) {
	s, err := readUpjet(root)
	if err != nil {
		return nil, err
	}
	var dec upjetDecisionFile
	if b, err := os.ReadFile(filepath.Join(root, upjetDecisions)); err == nil {
		if err := yaml.Unmarshal(b, &dec); err != nil {
			return nil, fmt.Errorf("%s: %w", upjetDecisions, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	kindByName := map[string]*Kind{}
	for _, k := range ours {
		kindByName[k.name()] = k
	}
	rowByRes := map[string]*ResRow{}
	for _, r := range rows {
		rowByRes[strings.ToLower(r.Resource)] = r
	}
	tfRes := map[string]string{} // Terraform resource -> our API resource
	for _, e := range s.Entries {
		if _, ok := tfRes[e.Facts.Terraform]; !ok {
			tfRes[e.Facts.Terraform] = e.Ours.Resource
		}
	}
	crds, err := crdIndex(root)
	if err != nil {
		return nil, err
	}

	res := &upjetResult{stats: map[string]int{}}
	used := map[string]bool{} // decisions that matched something
	keyRef := func(kind, field string) string { return "ref|" + kind + "|" + field }
	keySec := func(kind, field string) string { return "sec|" + kind + "|" + field }
	keyEx := func(kind, ex string) string { return "ex|" + kind + "|" + ex }
	refDec := map[string]string{}
	refApplied := map[string]string{}
	refMatch := map[string][2]string{}
	for _, d := range dec.References {
		switch {
		case d.Match != "":
			refMatch[keyRef(d.Kind, d.Field)] = [2]string{d.Match, d.Why}
			if d.Keep != "" {
				refDec[keyRef(d.Kind, d.Field)] = d.Keep
			}
		case d.Applied != "":
			refApplied[keyRef(d.Kind, d.Field)] = d.Applied
		default:
			refDec[keyRef(d.Kind, d.Field)] = d.Keep
		}
	}
	secDec := map[string]string{}
	for _, d := range dec.Secrets {
		secDec[keySec(d.Kind, d.Field)] = d.Keep
	}
	exDec := map[string]string{}
	for _, d := range dec.Examples {
		exDec[keyEx(d.Kind, d.Example)] = d.Skip
	}

	// The per-kind ledger: what Upjet says of each kind of ours.
	type perKind struct {
		terraform        []string
		tested           []string
		refs             map[string]int
		hand, onFake     int
		idCheck          string
		kept, appliedRef int
	}
	kinds := map[string]*perKind{}
	pk := func(name string) *perKind {
		if kinds[name] == nil {
			kinds[name] = &perKind{refs: map[string]int{}}
		}
		return kinds[name]
	}
	inDegree := map[string]int{} // Terraform resource -> references to it from resources served here
	served := func(resource string) bool {
		r := rowByRes[strings.ToLower(resource)]
		return r != nil && (len(r.OfficialKinds) > 0 || len(r.OursKinds) > 0)
	}
	for _, e := range s.Entries {
		if served(e.Ours.Resource) {
			for _, ref := range e.Facts.References {
				inDegree[ref.Target]++
			}
		}
	}

	for _, e := range s.Entries {
		row := rowByRes[strings.ToLower(e.Ours.Resource)]
		var mine []*Kind
		if row != nil {
			for _, n := range row.OursKinds {
				if k := kindByName[n]; k != nil {
					mine = append(mine, k)
				}
			}
		}
		for _, k := range mine {
			p := pk(k.name())
			p.terraform = append(p.terraform, e.Facts.Terraform)
			p.tested = append(p.tested, strconv.FormatBool(e.Facts.Tested))
		}
		if len(mine) == 0 {
			continue
		}
		res.stats["entries"]++

		// Identifiers.
		check := "n/a (" + e.Facts.ExternalName.Style + ")"
		other := "" // the ARM type Upjet's resource is, when it is not ours
		if t := e.Facts.ExternalName.Template; t != "" {
			typ := templateType(t)
			if typ != "" && !strings.EqualFold(typ, e.Ours.Resource) {
				other = typ
			}
			switch {
			case typ == "":
				check = "n/a (no provider path)"
				res.stats["idNoPath"]++
			case typ == e.Ours.Resource:
				check = "agrees"
				res.stats["idAgree"]++
			case strings.EqualFold(typ, e.Ours.Resource):
				check = "agrees (case)"
				res.stats["idAgree"]++
			default:
				check = "differs"
				res.stats["idDiffer"]++
			}
			res.stats["idChecked"]++
			for _, k := range mine {
				res.ids = append(res.ids, []string{k.name(), e.Ours.Resource, e.Facts.Terraform, typ, check, e.Facts.ExternalName.At, sliceAt(e.line)})
			}
		}
		for _, k := range mine {
			pk(k.name()).idCheck = check
		}

		// Late-init ignores: ASO never fills a spec from Azure's values
		// (the spec is what the user asked; Azure's are in the status), so
		// every one holds here by construction.
		for _, li := range e.Facts.LateInitIgnored {
			res.stats["lateInit"]++
			for _, k := range mine {
				res.lateInit = append(res.lateInit, []string{k.name(), e.Facts.Terraform, li.Field, "holds: ASO never late-initializes a spec", li.At, sliceAt(e.line)})
			}
		}

		for _, k := range mine {
			c := crds[k.name()]
			spec := specSchema(c)
			// List map keys: our CRDs' lists are atomic unless marked.
			for _, lm := range e.Facts.ListMapKeys {
				res.stats["listMap"]++
				path, ps := findSchemaPath(spec, lm.Field)
				status := "missing: no such list here"
				if ps != nil {
					if lt, _ := ps["x-kubernetes-list-type"].(string); lt == "map" {
						status = "agrees: list-type map"
					} else {
						status = "differs: atomic list (a server-side apply replaces it whole); ASO's generator marks no list as a map"
						res.stats["listMapDiffer"]++
					}
				}
				res.listMaps = append(res.listMaps, []string{k.name(), e.Facts.Terraform, lm.Field, path, status, lm.At, sliceAt(e.line)})
			}
			// Secrets.
			for _, so := range e.Facts.SchemaOverrides {
				if so.Property != "Sensitive" {
					continue
				}
				res.stats["secretChecked"]++
				path, ps := findSchemaPath(spec, so.Field)
				status := "missing: no such field here"
				switch {
				case ps != nil && isSecretRef(ps) && so.Value == "true":
					status = "agrees: a Secret reference"
				case ps != nil && so.Value == "true":
					if why, ok := secDec[keySec(k.name(), so.Field)]; ok {
						used[keySec(k.name(), so.Field)] = true
						status = "kept: " + why
					} else {
						status = "differs: we take it in plain text"
						res.stats["secretDiffer"]++
					}
				case ps != nil:
					status = "agrees: Upjet shows it, as we do"
				}
				res.secrets = append(res.secrets, []string{k.name(), e.Facts.Terraform, so.Field, so.Value, path, status, so.At, sliceAt(e.line)})
			}
			// References.
			for _, ref := range e.Facts.References {
				r := classifyRef(e, ref, k, tfRes)
				r.served = served(tfRes[ref.Target])
				if m, ok := refMatch[keyRef(k.name(), ref.Field)]; ok && other == "" {
					used[keyRef(k.name(), ref.Field)] = true
					r.ours, r.note = m[0], "matched by hand ("+upjetDecisions+"): "+m[1]
					switch {
					case m[0] == "owner":
						r.class = refOwner
					case isRefPath(m[0]):
						r.class = refReference
					default:
						r.class = refString
					}
				}
				if other != "" {
					// The stricter rule: a fact of another resource is not ours.
					r.class, r.ours, r.note = refOther, "", "Upjet's resource is "+other+" (the catalog's mapping joins it here)"
				}
				if r.class == refString {
					if why, ok := refDec[keyRef(k.name(), ref.Field)]; ok {
						used[keyRef(k.name(), ref.Field)] = true
						r.class, r.note = refKept, strings.TrimPrefix(r.note+"; ", "; ")+"kept a string: "+why
					}
				}
				if r.class == refReference {
					if f, ok := refApplied[keyRef(k.name(), ref.Field)]; ok {
						used[keyRef(k.name(), ref.Field)] = true
						r.note = "labelled from Upjet in " + f
						res.stats["refApplied"]++
					}
				}
				res.refs = append(res.refs, r)
				res.stats["ref:"+r.class]++
				pk(k.name()).refs[r.class]++
			}
			// Examples.
			if k.Hand || c == nil {
				for _, x := range e.Facts.Examples {
					if x.Origin == "hand" {
						res.stats["exHand"]++
						pk(k.name()).hand++
						res.exRows = append(res.exRows, []string{k.name(), x.ID, x.At, "not run: a hand-written kind (its own test)", "", sliceAt(x.line)})
					}
				}
				continue
			}
			seen := map[string]bool{}
			for _, x := range e.Facts.Examples {
				if x.Origin != "hand" {
					continue
				}
				res.stats["exHand"]++
				pk(k.name()).hand++
				status := ""
				spec, dropped := mapExample(x.Values, spec)
				key := keyEx(k.name(), x.At)
				switch {
				case other != "":
					status = "not run: Upjet's resource is " + other + ", not ours (the catalog's mapping joins it here)"
				case exDec[key] != "":
					used[key] = true
					status = "not run: " + exDec[key]
				case len(spec) == 0:
					status = "not run: nothing in it maps to our fields"
				default:
					j, _ := json.Marshal(spec)
					if seen[string(j)] {
						status = "same as an example above"
						break
					}
					seen[string(j)] = true
					status = "runs on the fake"
					res.stats["exOnFake"]++
					pk(k.name()).onFake++
					res.examples = append(res.examples, &upjetFixture{Group: k.Group, Version: crdVersion(c), Kind: k.Kind,
						Example: x.ID, At: x.At, Slice: sliceAt(x.line), Spec: spec, Dropped: dropped})
				}
				res.exRows = append(res.exRows, []string{k.name(), x.ID, x.At, status, strings.Join(dropped, ";"), sliceAt(x.line)})
			}
		}
	}
	for _, k := range sortedKeys(map[string]bool(func() map[string]bool {
		m := map[string]bool{}
		for _, d := range dec.References {
			m[keyRef(d.Kind, d.Field)] = true
		}
		for _, d := range dec.Secrets {
			m[keySec(d.Kind, d.Field)] = true
		}
		for _, d := range dec.Examples {
			m[keyEx(d.Kind, d.Example)] = true
		}
		return m
	}())) {
		if !used[k] {
			return nil, fmt.Errorf("%s: %q matches nothing in the slice any more; remove it", upjetDecisions, k)
		}
	}
	for _, d := range dec.References {
		if d.Applied != "" {
			res.stats["decApplied"]++
		}
	}

	// The ledger: every kind of ours.
	for _, k := range ours {
		if k.Outside != "" {
			continue
		}
		p := kinds[k.name()]
		fake := "conformance"
		if k.Hand {
			fake = "kind test"
		}
		up := []string{"", "no Upjet resource", "", "", "", "", "", "", "", ""}
		if p != nil {
			up = []string{strings.Join(uniq(p.terraform), ";"), strings.Join(uniq(p.tested), ";"),
				itoa(p.refs[refReference]), itoa(p.refs[refOwner]), itoa(p.refs[refString]), itoa(p.refs[refKept]),
				itoa(p.refs[refName] + p.refs[refMissing] + p.refs[refValue] + p.refs[refOther]), itoa(p.hand), itoa(p.onFake), p.idCheck}
			res.stats["kindsWithUpjet"]++
			if p.onFake > 0 {
				res.stats["kindsOnFake"]++
			}
			if p.refs[refReference] > 0 {
				res.stats["kindsWithRefs"]++
			}
		} else {
			res.stats["kindsNoUpjet"]++
		}
		res.stats["kinds"]++
		res.ledger = append(res.ledger, append([]string{k.name(), k.ARMType, fake}, up...))
	}

	// Gaps: resources Upjet has that neither ASO nor we serve.
	type gap struct {
		e                *upjetEntry
		tier, order, deg int
		hand             int
	}
	var gs []gap
	seenRes := map[string]bool{}
	for _, e := range s.Entries {
		if served(e.Ours.Resource) || seenRes[e.Ours.Resource+"|"+e.Facts.Terraform] {
			continue
		}
		seenRes[e.Ours.Resource+"|"+e.Facts.Terraform] = true
		h := 0
		for _, x := range e.Facts.Examples {
			if x.Origin == "hand" {
				h++
			}
		}
		gs = append(gs, gap{e: e, tier: prio.tier(e.Ours.Service), order: prio.order(e.Ours.Service), deg: inDegree[e.Facts.Terraform], hand: h})
	}
	sort.SliceStable(gs, func(i, j int) bool {
		a, b := gs[i], gs[j]
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		if a.deg != b.deg {
			return a.deg > b.deg
		}
		if a.e.Facts.Tested != b.e.Facts.Tested {
			return a.e.Facts.Tested
		}
		if a.hand != b.hand {
			return a.hand > b.hand
		}
		if a.order != b.order {
			return a.order < b.order
		}
		return a.e.Ours.Resource+a.e.Facts.Terraform < b.e.Ours.Resource+b.e.Facts.Terraform
	})
	for i, g := range gs {
		e := g.e
		var targets []string
		for _, r := range e.Facts.References {
			targets = append(targets, r.Target)
		}
		res.gaps = append(res.gaps, []string{itoa(i + 1), e.Ours.Service, e.Ours.Resource, itoa(g.tier), e.Facts.Terraform,
			strconv.FormatBool(e.Facts.Tested), itoa(g.deg), itoa(len(e.Facts.References)), strings.Join(uniq(targets), ";"),
			itoa(g.hand), e.Facts.ExternalName.Style, sliceAt(e.line)})
	}
	res.stats["gaps"] = len(gs)
	res.stats["noUpjet"] = len(s.NoUpjet)
	sort.SliceStable(res.refs, func(i, j int) bool {
		a, b := res.refs[i], res.refs[j]
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		return a.field < b.field
	})
	return res, nil
}

// templateType is the ARM type an Upjet ID template names:
// .../providers/Microsoft.X/a/{{ .. }}/b/{{ .. }} is Microsoft.X/a/b.
func templateType(t string) string {
	i := strings.LastIndex(t, "/providers/")
	if i < 0 {
		return ""
	}
	segs := strings.Split(strings.Trim(t[i+len("/providers/"):], "/"), "/")
	if len(segs) < 2 {
		return ""
	}
	out := []string{segs[0]}
	for j := 1; j < len(segs); j += 2 {
		out = append(out, segs[j])
	}
	return strings.Join(out, "/")
}

// classifyRef places one Upjet reference on one kind of ours.
func classifyRef(e *upjetEntry, ref *upjetRef, k *Kind, tfRes map[string]string) *upjetRefRow {
	r := &upjetRefRow{kind: k.name(), resource: e.Ours.Resource, field: ref.Field, target: ref.Target, extractor: shortExtractor(ref.Extractor),
		at: ref.At + " (" + sliceAt(ref.line) + ")"}
	parts := strings.Split(ref.Field, ".")
	leaf := parts[len(parts)-1]
	targetRes := tfRes[ref.Target]
	// The owner: the resource group, a parent of the resource (by our
	// mapping of the target, or by Terraform's naming: a child is named
	// for its parent, azurerm_api_management_tag for azurerm_api_management),
	// or the management group or subscription an extension resource is at.
	base := strings.TrimSuffix(strings.TrimSuffix(leaf, "_id"), "_name")
	switch {
	case ref.Target == "azurerm_resource_group":
		r.class, r.ours = refOwner, "owner"
	case len(parts) == 1 && targetRes != "" && strings.HasPrefix(strings.ToLower(e.Ours.Resource), strings.ToLower(targetRes)+"/"):
		r.class, r.ours = refOwner, "owner"
	case len(parts) == 1 && base != leaf && strings.HasPrefix(e.Facts.Terraform, ref.Target+"_") && strings.HasSuffix(ref.Target, "_"+base) &&
		parentNamed(e.Ours.Resource, base):
		r.class, r.ours, r.note = refOwner, "owner", "Terraform names the resource for its parent"
	case len(parts) == 1 && (leaf == "management_group_id" || leaf == "subscription_id") &&
		(ref.Target == "azurerm_management_group" || ref.Target == "azurerm_subscription"):
		r.class, r.ours, r.note = refOwner, "owner", "the scope of an extension resource"
	case len(parts) == 1 && base != leaf && strings.HasSuffix(e.Facts.Terraform, "_"+base):
		// azurerm_maintenance_assignment_virtual_machine's virtual_machine_id.
		r.class, r.ours, r.note = refOwner, "owner", "the scope of an extension resource (Terraform names the resource for it)"
	case len(parts) == 1 && base != leaf && parentType(e.Ours.Resource, strings.ReplaceAll(base, "_", "")):
		r.class, r.ours, r.note = refOwner, "owner", "a parent of the resource, by its ARM type"
	}
	if r.class != "" {
		return r
	}
	// A value of the target other than its ID: an endpoint, a URI, a
	// client ID, a Key Vault secret's URL. ASO's references hold ARM IDs.
	if m := extractParam.FindStringSubmatch(ref.Extractor); m != nil && m[1] != "id" {
		r.class, r.note = refValue, "Upjet copies the target's "+m[1]
		return r
	}
	if strings.HasSuffix(leaf, "_uri") || strings.HasSuffix(leaf, "_url") || strings.HasSuffix(leaf, "secret_id") {
		r.class, r.note = refValue, "a URI of the target, not an ARM ID"
		return r
	}
	toks := strings.Split(leaf, "_")
	idLike := false
	for len(toks) > 1 && (toks[len(toks)-1] == "id" || toks[len(toks)-1] == "ids" || toks[len(toks)-1] == "resource") {
		toks = toks[:len(toks)-1]
		idLike = true
	}
	if !idLike && strings.Contains(ref.Extractor, "ExtractResourceID") {
		idLike = true
	}
	if !idLike {
		r.class = refName
		r.note = "Upjet names the target by " + leaf
		return r
	}
	norm := singular(strings.Join(toks, ""))
	parentToks := map[string]bool{}
	for _, p := range parts[:len(parts)-1] {
		for _, t := range strings.Split(p, "_") {
			parentToks[t] = true
		}
	}
	type cand struct {
		path         string
		score, bonus int
	}
	var best []cand
	for key, shown := range k.Wire.Leaves {
		base := strings.TrimRight(key, "[]{}")
		segs := strings.Split(base, ".")
		l := segs[len(segs)-1]
		if !idSuffix.MatchString(l) {
			continue
		}
		ln := idSuffix.ReplaceAllString(l, "")
		if ln == "" && len(segs) > 1 {
			ln = strings.TrimRight(segs[len(segs)-2], "[]{}")
			ln = idSuffix.ReplaceAllString(ln, "")
		}
		ln = singular(ln)
		bonus := 0
		for _, sg := range segs[:len(segs)-1] {
			for t := range parentToks {
				if len(t) >= 3 && strings.Contains(sg, t) {
					bonus++
				}
			}
		}
		score := 0
		short := min(len(ln), len(norm))
		switch {
		case ln == norm:
			score = 3
		case short >= 4 && (strings.HasSuffix(norm, ln) || strings.HasSuffix(ln, norm)):
			score = 2
		case len(toks[len(toks)-1]) >= 4 && strings.HasSuffix(ln, toks[len(toks)-1]):
			score = 1
		case len(ln) >= 4 && strings.HasPrefix(norm, ln):
			score = 1
		case (ln == "resource" || ln == "targetresource") && bonus > 0:
			// storage_queue_endpoint.storage_account_id is the storage
			// queue destination's resourceId.
			score = 1
		}
		if score == 0 {
			continue
		}
		c := cand{shown, score, bonus}
		if len(best) == 0 || c.score > best[0].score || (c.score == best[0].score && c.bonus > best[0].bonus) {
			best = []cand{c}
		} else if c.score == best[0].score && c.bonus == best[0].bonus {
			best = append(best, c)
		}
	}
	if len(best) == 0 {
		r.class = refMissing
		return r
	}
	paths := map[string]bool{}
	anyRef, anyStr := false, false
	for _, c := range best {
		paths[c.path] = true
		if isRefPath(c.path) {
			anyRef = true
		} else {
			anyStr = true
		}
	}
	r.ours = strings.Join(sortedKeys(paths), ";")
	switch {
	case anyRef && !anyStr:
		r.class = refReference
	case anyStr && !anyRef:
		r.class = refString
	default:
		// Some of the equally good matches are references, some not: the
		// references are the ones that hold an ARM ID.
		r.class = refReference
	}
	if best[0].score < 3 {
		r.note = "matched by name: " + norm
	}
	return r
}

// isRefPath is whether a path of our kind is a Kubernetes reference.
func isRefPath(p string) bool {
	p = strings.TrimRight(p, "[]{}")
	segs := strings.Split(p, ".")
	l := segs[len(segs)-1]
	if strings.HasSuffix(l, "Reference") || strings.HasSuffix(l, "References") || l == "reference" {
		return true
	}
	// A hand-written kind's leaf below a reference (spec.policyReference.armId).
	for _, sg := range segs[:len(segs)-1] {
		sg = strings.TrimRight(sg, "[]{}")
		if strings.HasSuffix(sg, "Reference") || strings.HasSuffix(sg, "References") {
			return true
		}
	}
	return false
}

// singular drops an English plural: identities is identity, ids is id.
func singular(s string) string {
	switch {
	case strings.HasSuffix(s, "ies"):
		return strings.TrimSuffix(s, "ies") + "y"
	case strings.HasSuffix(s, "ses"):
		return strings.TrimSuffix(s, "es")
	case strings.HasSuffix(s, "s"):
		return strings.TrimSuffix(s, "s")
	}
	return s
}

// crdIndex reads config/crds by group/Kind.
func crdIndex(root string) (map[string]*crd, error) {
	files, err := filepath.Glob(filepath.Join(root, "config", "crds", "*.yaml"))
	if err != nil {
		return nil, err
	}
	out := map[string]*crd{}
	for _, f := range files {
		if filepath.Base(f) == "kustomization.yaml" {
			continue
		}
		c, err := readCRD(f)
		if err != nil {
			return nil, err
		}
		out[c.Spec.Group+"/"+c.Spec.Names.Kind] = c
	}
	return out, nil
}

func crdVersion(c *crd) string { return c.Spec.Versions[0].Name }

func specSchema(c *crd) map[string]any {
	if c == nil {
		return nil
	}
	props, _ := c.Spec.Versions[0].Schema.OpenAPIV3Schema["properties"].(map[string]any)
	s, _ := props["spec"].(map[string]any)
	return s
}

func isSecretRef(s map[string]any) bool {
	ps, _ := s["properties"].(map[string]any)
	_, n := ps["name"]
	_, k := ps["key"]
	return len(ps) == 2 && n && k
}

// skipProps are the spec properties an example never sets: the operator's,
// the object's name and place, and references (an example names other
// resources by reference, which the conformance object makes).
var skipProps = map[string]bool{"owner": true, "azurename": true, "operatorspec": true, "location": true, "tags": true}

// findProp finds the property of an object schema that a Terraform name
// stands for: the name in camel case, or its plural, without case.
func findProp(s map[string]any, tf string) (string, map[string]any) {
	ps, _ := s["properties"].(map[string]any)
	want := strings.ToLower(strings.ReplaceAll(tf, "_", ""))
	for _, w := range []string{want, want + "s", want + "es", strings.TrimSuffix(want, "s")} {
		for name, p := range ps {
			ln := strings.ToLower(name)
			if skipProps[ln] || isRefPath(name) || strings.HasSuffix(ln, "fromconfig") {
				continue
			}
			if ln == w {
				m, _ := p.(map[string]any)
				return name, m
			}
		}
	}
	return "", nil
}

// findSchemaPath follows a Terraform field path (a.b_c) through a spec
// schema, through arrays.
func findSchemaPath(s map[string]any, field string) (string, map[string]any) {
	var path []string
	cur := s
	for _, seg := range strings.Split(field, ".") {
		for {
			items, ok := cur["items"].(map[string]any)
			if !ok {
				break
			}
			cur = items
		}
		name, p := findProp(cur, seg)
		if p == nil {
			return "", nil
		}
		path = append(path, name)
		cur = p
	}
	return strings.Join(path, "."), cur
}

// mapExample maps a hand example's Terraform values onto a spec schema:
// each value whose field and type our schema has, checked against its enum,
// pattern and bounds. What does not map is dropped and listed.
func mapExample(vals map[string]any, spec map[string]any) (map[string]any, []string) {
	var dropped []string
	out := mapObject(vals, spec, "", &dropped)
	sort.Strings(dropped)
	return out, dropped
}

func mapObject(vals map[string]any, s map[string]any, at string, dropped *[]string) map[string]any {
	out := map[string]any{}
	for _, k := range sortedKeys(vals) {
		v := vals[k]
		if empty(v) {
			continue
		}
		name, p := findProp(s, k)
		if p == nil {
			// sku_name is sku.name.
			toks := strings.Split(k, "_")
			for i := 1; i < len(toks) && p == nil; i++ {
				outer, op := findProp(s, strings.Join(toks[:i], "_"))
				if op == nil {
					continue
				}
				inner, ip := findProp(op, strings.Join(toks[i:], "_"))
				if ip == nil {
					continue
				}
				if mv, ok := mapValue(v, ip, join(at, k), dropped); ok {
					o, _ := out[outer].(map[string]any)
					if o == nil {
						o = map[string]any{}
					}
					o[inner] = mv
					out[outer] = o
					p = ip
				}
			}
			if p == nil {
				*dropped = append(*dropped, join(at, k))
			}
			continue
		}
		if mv, ok := mapValue(v, p, join(at, k), dropped); ok {
			if o, isObj := mv.(map[string]any); isObj {
				if prev, ok := out[name].(map[string]any); ok {
					merge2(prev, o)
					continue
				}
			}
			out[name] = mv
		} else {
			*dropped = append(*dropped, join(at, k))
		}
	}
	return out
}

func merge2(dst, src map[string]any) {
	for k, v := range src {
		dst[k] = v
	}
}

func empty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case map[string]any:
		return len(x) == 0
	case []any:
		for _, e := range x {
			if !empty(e) {
				return false
			}
		}
		return true
	}
	return false
}

func mapValue(v any, s map[string]any, at string, dropped *[]string) (any, bool) {
	typ, _ := s["type"].(string)
	switch typ {
	case "object":
		if ps, _ := s["properties"].(map[string]any); len(ps) > 0 {
			if l, ok := v.([]any); ok && len(l) == 1 {
				v = l[0]
			}
			m, ok := v.(map[string]any)
			if !ok {
				return nil, false
			}
			o := mapObject(m, s, at, dropped)
			return o, len(o) > 0
		}
		ap, _ := s["additionalProperties"].(map[string]any)
		m, ok := v.(map[string]any)
		if ap == nil || !ok {
			return nil, false
		}
		o := map[string]any{}
		for k, e := range m {
			mv, ok := mapValue(e, ap, at, dropped)
			if !ok {
				return nil, false
			}
			o[k] = mv
		}
		return o, len(o) > 0
	case "array":
		items, _ := s["items"].(map[string]any)
		if items == nil {
			return nil, false
		}
		l, ok := v.([]any)
		if !ok {
			l = []any{v}
		}
		var out []any
		for _, e := range l {
			if empty(e) {
				continue
			}
			mv, ok := mapValue(e, items, at, dropped)
			if !ok {
				return nil, false
			}
			out = append(out, mv)
		}
		return out, len(out) > 0
	case "string":
		str, ok := v.(string)
		if !ok || str == "" {
			return nil, false
		}
		if en, ok := s["enum"].([]any); ok {
			for _, x := range en {
				if xs, _ := x.(string); strings.EqualFold(xs, str) {
					return xs, true
				}
			}
			return nil, false
		}
		if pat, _ := s["pattern"].(string); pat != "" {
			re, err := regexp.Compile(pat)
			if err != nil || !re.MatchString(str) {
				return nil, false
			}
		}
		if n, ok := num(s["maxLength"]); ok && float64(len(str)) > n {
			return nil, false
		}
		if n, ok := num(s["minLength"]); ok && float64(len(str)) < n {
			return nil, false
		}
		if f, _ := s["format"].(string); f != "" && f != "date-time" {
			return nil, false
		}
		return str, true
	case "integer", "number":
		f, ok := v.(float64)
		if !ok {
			return nil, false
		}
		if typ == "integer" && f != float64(int64(f)) {
			return nil, false
		}
		if n, ok := num(s["minimum"]); ok && f < n {
			return nil, false
		}
		if n, ok := num(s["maximum"]); ok && f > n {
			return nil, false
		}
		if typ == "integer" {
			return int64(f), true
		}
		return f, true
	case "boolean":
		b, ok := v.(bool)
		return b, ok
	}
	return nil, false
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

// upjetOutputs are the files docs/coverage/upjet*.csv and
// docs/coverage/upjet-examples.json.
func upjetOutputs(u *upjetResult) map[string][]byte {
	files := map[string][]byte{}
	refs := [][]string{{"kind", "resource", "upjet_field", "upjet_target", "upjet_extractor", "target_served_here", "class", "ours_path", "note", "upjet_at"}}
	for _, r := range u.refs {
		refs = append(refs, []string{r.kind, r.resource, r.field, r.target, r.extractor, strconv.FormatBool(r.served), r.class, r.ours, r.note, r.at})
	}
	files["upjet-references.csv"] = writeCSV(refs)
	checks := [][]string{{"check", "kind", "terraform", "upjet_field", "upjet_says", "ours_path", "result", "upjet_at", "slice"}}
	for _, r := range u.ids {
		checks = append(checks, []string{"id", r[0], r[2], "", r[3], r[1], r[4], r[5], r[6]})
	}
	for _, r := range u.secrets {
		checks = append(checks, []string{"sensitive", r[0], r[1], r[2], "Sensitive=" + r[3], r[4], r[5], r[6], r[7]})
	}
	for _, r := range u.lateInit {
		checks = append(checks, []string{"late-init-ignored", r[0], r[1], r[2], "not filled from Azure", "", r[3], r[4], r[5]})
	}
	for _, r := range u.listMaps {
		checks = append(checks, []string{"list-map-keys", r[0], r[1], r[2], "merge as a map", r[3], r[4], r[5], r[6]})
	}
	files["upjet-checks.csv"] = writeCSV(checks)
	ex := [][]string{{"kind", "example", "upjet_file", "result", "dropped", "slice"}}
	files["upjet-examples.csv"] = writeCSV(append(ex, u.exRows...))
	ledger := [][]string{{"kind", "arm_type", "tested_here", "upjet_terraform", "upjet_tested", "refs_reference", "refs_owner", "refs_string", "refs_kept", "refs_other", "upjet_hand_examples", "upjet_examples_on_fake", "upjet_id_check"}}
	files["upjet-ledger.csv"] = writeCSV(append(ledger, u.ledger...))
	gaps := [][]string{{"rank", "service", "resource", "tier", "terraform", "upjet_tested", "referenced_by_served", "upjet_references", "upjet_targets", "hand_examples", "id_style", "slice"}}
	files["upjet-gaps.csv"] = writeCSV(append(gaps, u.gaps...))
	j, _ := json.MarshalIndent(u.examples, "", " ")
	files["upjet-examples.json"] = append(j, '\n')
	return files
}

// upjetNumbers is the Upjet section of docs/coverage/README.md.
func upjetNumbers(u *upjetResult) string {
	s := u.stats
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f, a...) }
	w("| What | Count |\n| --- | ---: |\n")
	w("| Kinds of ours (in the denominator) | %d |\n", s["kinds"])
	w("| ... with an Upjet resource | %d |\n", s["kindsWithUpjet"])
	w("| ... with none (`noUpjet` and unmatched) | %d |\n", s["kindsNoUpjet"])
	w("| Upjet references on our kinds | %d |\n", s["ref:reference"]+s["ref:owner"]+s["ref:string"]+s["ref:kept"]+s["ref:name"]+s["ref:missing"]+s["ref:value"]+s["ref:other"])
	w("| ... a Kubernetes reference here | %d |\n", s["ref:reference"])
	w("| ... of which labelled from Upjet (`%s`) | %d |\n", upjetDecisions, s["refApplied"])
	w("| ... the owner (a parent or resource group) | %d |\n", s["ref:owner"])
	w("| ... a string field here, undecided (a gap) | %d |\n", s["ref:string"])
	w("| ... a string here on purpose (`%s`, with the reason) | %d |\n", upjetDecisions, s["ref:kept"])
	w("| ... by name, where ARM takes a name | %d |\n", s["ref:name"])
	w("| ... another value of the target (a URI, an endpoint), which no ARM reference holds | %d |\n", s["ref:value"])
	w("| ... no field of ours matches | %d |\n", s["ref:missing"])
	w("| ... on an Upjet resource of another ARM type (its ID template differs; not applied) | %d |\n", s["ref:other"])
	w("| Kinds with at least one Upjet reference that is a reference here | %d |\n", s["kindsWithRefs"])
	w("| ID templates checked against our ARM type | %d (agree %d, differ %d; %d name no provider path) |\n", s["idChecked"], s["idAgree"], s["idDiffer"], s["idNoPath"])
	w("| Sensitive overrides checked | %d (we take in plain text, undecided: %d) |\n", s["secretChecked"], s["secretDiffer"])
	w("| Late-init ignores checked (hold: ASO never late-initializes) | %d |\n", s["lateInit"])
	w("| List map keys checked | %d (differ %d) |\n", s["listMap"], s["listMapDiffer"])
	w("| Hand examples of our kinds | %d |\n", s["exHand"])
	w("| ... run on the fake cloud (`TestUpjetExamplesOnFake`) | %d, of %d kinds |\n", s["exOnFake"], s["kindsOnFake"])
	w("| Resources Upjet has and neither ASO nor we serve (`upjet-gaps.csv`) | %d |\n", s["gaps"])
	return b.String()
}

// upjetGapTable is the top of the ranked gap list, for foundation.md.
func upjetGapTable(u *upjetResult, n int) string {
	var b strings.Builder
	b.WriteString("| # | Resource | Tier | Upjet resource | Referenced by what we serve | Tested by Upjet | Hand examples |\n| ---: | --- | ---: | --- | ---: | --- | ---: |\n")
	for i, g := range u.gaps {
		if i == n {
			break
		}
		fmt.Fprintf(&b, "| %s | `%s` | %s | `%s` | %s | %s | %s |\n", g[0], g[2], g[3], g[4], g[6], g[5], g[9])
	}
	fmt.Fprintf(&b, "\n%d in all: [`upjet-gaps.csv`](upjet-gaps.csv).\n", len(u.gaps))
	return b.String()
}

// shortExtractor drops the package path of an extractor.
func shortExtractor(x string) string {
	if i := strings.LastIndex(x, "/"); i >= 0 {
		return x[i+1:]
	}
	return x
}

// parentType is whether one of the parent types of an ARM type is named
// name (expressroutegateway for Microsoft.Network/expressRouteGateways/...).
func parentType(resource, name string) bool {
	segs := strings.Split(resource, "/")
	for _, sg := range segs[1 : len(segs)-1] {
		if singular(strings.ToLower(sg)) == name {
			return true
		}
	}
	return false
}

// tfParentAlias names the ARM type of a Terraform resource whose name says
// nothing of it.
var tfParentAlias = map[string]string{"api_management": "service"}

// parentNamed is whether a parent type of an ARM type ends in the last word
// of a Terraform name (environment for managedEnvironments), or is the one
// tfParentAlias gives.
func parentNamed(resource, tfBase string) bool {
	segs := strings.Split(resource, "/")
	toks := strings.Split(tfBase, "_")
	last := toks[len(toks)-1]
	for _, sg := range segs[1 : len(segs)-1] {
		sg = singular(strings.ToLower(sg))
		if strings.HasSuffix(sg, last) || tfParentAlias[tfBase] == sg {
			return true
		}
	}
	return false
}
