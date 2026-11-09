// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// oursFile is ours.yaml: the hand-written kinds and what they cover.
type oursFile struct {
	Kinds []oursKind `yaml:"kinds"`
}

type oursKind struct {
	CRD string `yaml:"crd"`
	// ARMType is the ARM resource the kind manages, when it is one in the
	// OpenAPI specs; Map then names, for each CRD spec path, the ARM
	// attribute path it sets.
	ARMType string `yaml:"armType"`
	// API is the ARM or data-plane API version the kind speaks.
	API string            `yaml:"api"`
	Map map[string]string `yaml:"map"`
	// Outside names a resource the specs do not have (a data-plane object,
	// a database role): the kind is listed, and left out of the percentages.
	Outside string `yaml:"outside"`
	// Ignore lists CRD spec paths that are not attributes of the resource
	// (owner, azureName).
	Ignore []string `yaml:"ignore"`
	// FromUpstreamPR names the open upstream pull request or unreleased
	// upstream commit the kind is copied from.
	FromUpstreamPR string `yaml:"fromUpstreamPR"`
	Note           string `yaml:"note"`
}

// crd is the part of a CustomResourceDefinition read here.
type crd struct {
	Spec struct {
		Group string `yaml:"group"`
		Names struct {
			Kind string `yaml:"kind"`
		} `yaml:"names"`
		Versions []struct {
			Name   string `yaml:"name"`
			Served bool   `yaml:"served"`
			Schema struct {
				OpenAPIV3Schema map[string]any `yaml:"openAPIV3Schema"`
			} `yaml:"schema"`
		} `yaml:"versions"`
	} `yaml:"spec"`
}

func readCRD(file string) (*crd, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var c crd
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if len(c.Spec.Versions) == 0 {
		return nil, fmt.Errorf("%s: no versions", file)
	}
	return &c, nil
}

// crdLeaves lists the leaf paths of the CRD's spec, in the notation of the
// attribute paths: dots, [] for arrays, {} for maps.
func crdLeaves(c *crd) []string {
	spec := c.Spec.Versions[0].Schema.OpenAPIV3Schema
	props, _ := spec["properties"].(map[string]any)
	s, _ := props["spec"].(map[string]any)
	var out []string
	var walk func(m map[string]any, p string)
	walk = func(m map[string]any, p string) {
		emitted := false
		if items, ok := m["items"].(map[string]any); ok {
			emitted = true
			walk(items, p+"[]")
		}
		if ap, ok := m["additionalProperties"].(map[string]any); ok {
			emitted = true
			walk(ap, p+"{}")
		}
		if ps, ok := m["properties"].(map[string]any); ok && len(ps) > 0 {
			emitted = true
			names := make([]string, 0, len(ps))
			for n := range ps {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				if c, ok := ps[n].(map[string]any); ok {
					walk(c, join(p, n))
				}
			}
		}
		if !emitted && p != "" {
			out = append(out, p)
		}
	}
	walk(s, "")
	sort.Strings(out)
	return out
}

// oursHand is a hand-written kind, read.
type oursHand struct {
	oursKind
	Group, Kind string
	Leaves      []string // CRD spec paths, minus the ignored ones
}

func readOurs(root string, ignoredCRDs map[string]bool) ([]*oursHand, error) {
	b, err := os.ReadFile(filepath.Join(root, "hack/coverage/ours.yaml"))
	if err != nil {
		return nil, err
	}
	var f oursFile
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	var out []*oursHand
	for _, k := range f.Kinds {
		c, err := readCRD(filepath.Join(root, k.CRD))
		if err != nil {
			return nil, err
		}
		h := &oursHand{oursKind: k, Group: c.Spec.Group, Kind: c.Spec.Names.Kind}
		ign := map[string]bool{}
		for _, i := range k.Ignore {
			ign[i] = true
		}
		used := map[string]bool{}
		for _, l := range crdLeaves(c) {
			if hasPrefixPath(l, ign) {
				continue
			}
			h.Leaves = append(h.Leaves, l)
			if k.ARMType != "" {
				if _, ok := k.Map[l]; !ok {
					return nil, fmt.Errorf("%s: spec.%s is neither mapped nor ignored in hack/coverage/ours.yaml", k.CRD, l)
				}
				used[l] = true
			}
		}
		for m := range k.Map {
			if !used[m] {
				return nil, fmt.Errorf("%s: ours.yaml maps spec.%s, which the CRD does not have", k.CRD, m)
			}
		}
		if (k.ARMType == "") == (k.Outside == "") {
			return nil, fmt.Errorf("%s: exactly one of armType and outside", k.CRD)
		}
		ignoredCRDs[filepath.Base(k.CRD)] = true
		out = append(out, h)
	}
	return out, nil
}

// hasPrefixPath reports whether p is, or is under, one of the paths in set.
func hasPrefixPath(p string, set map[string]bool) bool {
	for len(p) > 0 {
		if set[p] {
			return true
		}
		i := strings.LastIndexAny(p, ".[{")
		if i < 0 {
			return false
		}
		p = p[:i]
	}
	return false
}
