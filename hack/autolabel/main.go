// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Command autolabel reads a log of ASO's generator and writes the labels it
// asks for into generator/azure-arm/<group>.yaml, so one generator run on a
// machine can be followed by the next without a person in between
// (hack/generate-iterate.sh). It handles one kind of error, the commonest:
//
//	<package>/<Type>.<Property> looks like a resource reference but was not labelled as one
//
// and
//
//	visiting type "<package>/<Type>": ... property <Property> might be a secret and must be configured with $importSecretMode
//
// A property whose name says it holds an ARM ID (Id, ...ResourceId,
// ...ResourceIds, ...ArmId, ...Ids; the generator's own name rule plus the
// plural) is labelled `$referenceType: arm`, and becomes a Kubernetes
// reference; any other (the generator matched only its description, as
// for a type name or a free-form scope) is labelled `$referenceType:
// simple` and stays a string. A property that might be a secret is
// labelled `$importSecretMode: required` (a Secret reference) when its name
// ends in a secret's word (Password, Secret, Key, Token, ...) and `never`
// otherwise (a TokenEndpoint, an AccessTokenPrepend); a _STATUS type's
// label goes on the type without the suffix, where the generator looks.
// Each label it writes carries the comment
// "autolabel: review"; a person reviews the generated structure.txt and
// removes the comment, as for any label (docs/throughput.md).
//
// Usage: go run ./hack/autolabel <generator log>
//
// It prints one line per label written and exits 0; it exits 3 when the
// log asks for nothing it can write (so a loop knows to stop).
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

var (
	unlabelled = regexp.MustCompile(`(\S+)/([A-Za-z0-9_]+)\.([A-Za-z0-9_]+) looks like a resource reference but was not labelled as one`)
	// A package path ends in <group>/<version>, version v20250601 or
	// v20231201preview.
	packagePath = regexp.MustCompile(`/([a-z0-9]+)/v(\d{4})(\d{2})(\d{2})(preview)?$`)
	armName     = regexp.MustCompile(`(?i)(^Id$|ResourceIds?$|ARMIDs?$|Ids$|ResourceId)`)
	maybeSecret = regexp.MustCompile(`visiting type \\?"(\S+)/([A-Za-z0-9_]+)\\?": [^"]*?property ([A-Za-z0-9_]+) might be a secret and must be configured with \$importSecretMode`)
	secretName  = regexp.MustCompile(`(?i)(password|secret|key|keys|token|credential|credentials|connectionstring|sas)$`)
)

type label struct {
	group, version, typ, prop, key, value string
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: autolabel <generator log>")
		os.Exit(2)
	}
	labels, err := read(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(labels) == 0 {
		fmt.Fprintln(os.Stderr, "autolabel: no unlabelled references in the log")
		os.Exit(3)
	}
	byGroup := map[string][]label{}
	for _, l := range labels {
		byGroup[l.group] = append(byGroup[l.group], l)
	}
	groups := make([]string, 0, len(byGroup))
	for g := range byGroup {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	for _, g := range groups {
		if err := write(filepath.Join("generator", "azure-arm", g+".yaml"), byGroup[g]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

// read collects the labels a generator log asks for, each once.
func read(path string) ([]label, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	seen := map[label]bool{}
	var out []label
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		for _, m := range unlabelled.FindAllStringSubmatch(sc.Text(), -1) {
			version, group, ok := groupVersion(m[1])
			if !ok {
				return nil, fmt.Errorf("autolabel: cannot read a group and version from %q", m[1])
			}
			value := "simple"
			if armName.MatchString(m[3]) {
				value = "arm"
			}
			l := label{group: group, version: version, typ: m[2], prop: m[3], key: "$referenceType", value: value}
			if !seen[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
		for _, m := range maybeSecret.FindAllStringSubmatch(sc.Text(), -1) {
			version, group, ok := groupVersion(m[1])
			if !ok {
				return nil, fmt.Errorf("autolabel: cannot read a group and version from %q", m[1])
			}
			value := "never"
			if secretName.MatchString(m[3]) {
				value = "required"
			}
			l := label{group: group, version: version, typ: strings.TrimSuffix(m[2], "_STATUS"), prop: m[3], key: "$importSecretMode", value: value}
			if !seen[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
	}
	return out, sc.Err()
}

// groupVersion reads the configuration's version (2025-06-01,
// 2023-12-01-preview) and the group from a package path.
func groupVersion(pkg string) (version, group string, ok bool) {
	p := packagePath.FindStringSubmatch(pkg)
	if p == nil {
		return "", "", false
	}
	version = p[2] + "-" + p[3] + "-" + p[4]
	if p[5] != "" {
		version += "-preview"
	}
	return version, p[1], true
}

// write adds the labels to a group's configuration file, keeping what is
// there (comments included).
func write(path string, labels []label) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("autolabel: %w (the group's configuration file)", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("autolabel: %s: %w", path, err)
	}
	root := doc.Content[0]
	gmc := child(root, "groupModelConfiguration")
	for _, l := range labels {
		prop := child(child(child(gmc, l.version), l.typ), l.prop)
		if v := value(prop, l.key); v != nil {
			return fmt.Errorf("autolabel: %s: %s.%s.%s already has %s %s; the generator still asks", path, l.version, l.typ, l.prop, l.key, v.Value)
		}
		prop.Content = append(prop.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: l.key},
			&yaml.Node{Kind: yaml.ScalarNode, Value: l.value, LineComment: "autolabel: review"})
		fmt.Printf("%s: %s %s.%s: %s: %s\n", path, l.version, l.typ, l.prop, l.key, l.value)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// child is the mapping under key in m, made when missing.
func child(m *yaml.Node, key string) *yaml.Node {
	if v := value(m, key); v != nil {
		if v.Kind != yaml.MappingNode {
			v.Kind, v.Tag, v.Value = yaml.MappingNode, "!!map", ""
		}
		return v
	}
	v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
	return v
}

// value is the node under key in the mapping m, or nil.
func value(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if strings.EqualFold(m.Content[i].Value, key) {
			return m.Content[i+1]
		}
	}
	return nil
}
