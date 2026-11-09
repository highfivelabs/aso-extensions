// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Command coverage measures how much of the Azure Resource Manager API this
// repository's CRDs and Azure Service Operator's cover. See docs/coverage/README.md.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	coverageDir = "docs/coverage"
	startMarker = "<!-- coverage:start -->"
	endMarker   = "<!-- coverage:end -->"
)

// sources is docs/coverage/sources.yaml: the inputs, pinned.
type sources struct {
	Specs struct {
		Repo       string `yaml:"repo"`
		Commit     string `yaml:"commit"`
		CommitDate string `yaml:"commitDate"`
		Fetched    string `yaml:"fetched"`
	} `yaml:"specs"`
	ASO struct {
		Repo           string `yaml:"repo"`
		Commit         string `yaml:"commit"`
		CommitDate     string `yaml:"commitDate"`
		NearestRelease string `yaml:"nearestRelease"`
		Fetched        string `yaml:"fetched"`
	} `yaml:"aso"`
}

func main() {
	root := flag.String("root", ".", "the repository root")
	check := flag.Bool("check", false, "fail if the committed outputs differ from a regeneration")
	verbose := flag.Bool("v", false, "print what was found")
	spot := flag.String("spotcheck", "", "compare with the CRDs in this manifest (a release's customresourcedefinitions yaml) instead of regenerating")
	spotTypes := flag.String("spot-resources", defaultSpot, "comma-separated ARM resource types for -spotcheck")
	flag.Parse()
	spotFile, spotList = *spot, strings.Split(*spotTypes, ",")
	if err := run(*root, *check, *verbose); err != nil {
		fmt.Fprintln(os.Stderr, "coverage:", err)
		os.Exit(1)
	}
}

var (
	spotFile string
	spotList []string
)

const defaultSpot = "Microsoft.Network/virtualNetworks,Microsoft.Network/virtualNetworks/subnets,Microsoft.Network/networkSecurityGroups," +
	"Microsoft.Network/publicIPAddresses,Microsoft.Compute/virtualMachines,Microsoft.Compute/disks,Microsoft.ContainerService/managedClusters," +
	"Microsoft.Storage/storageAccounts,Microsoft.KeyVault/vaults,Microsoft.DBforPostgreSQL/flexibleServers,Microsoft.Sql/servers," +
	"Microsoft.DocumentDB/databaseAccounts,Microsoft.ServiceBus/namespaces/queues,Microsoft.EventHub/namespaces," +
	"Microsoft.ManagedIdentity/userAssignedIdentities,Microsoft.Network/dnszones/A,Microsoft.Authorization/roleAssignments,Microsoft.Web/serverfarms," +
	"Microsoft.Network/privateEndpoints,Microsoft.Network/virtualNetworkGateways,Microsoft.Network/azureFirewalls,Microsoft.Network/loadBalancers," +
	"Microsoft.ContainerService/managedClusters/agentPools,Microsoft.KubernetesConfiguration/fluxConfigurations,Microsoft.ContainerRegistry/registries"

func run(root string, check, verbose bool) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	var src sources
	b, err := os.ReadFile(filepath.Join(root, coverageDir, "sources.yaml"))
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(b, &src); err != nil {
		return err
	}
	if err := checkPins(root, &src); err != nil {
		return err
	}
	cache := filepath.Join(root, ".cache", "coverage")

	// The denominator: the specs.
	specs := &gitCache{dir: filepath.Join(cache, "specs-"+src.Specs.Commit[:12]), remote: src.Specs.Repo, commit: src.Specs.Commit}
	if err := specs.open(); err != nil {
		return err
	}
	// The kinds: ASO's and ours.
	official, err := asoKinds(cache, &src)
	if err != nil {
		return err
	}
	ours, err := oursKinds(root)
	if err != nil {
		return err
	}
	docs, err := checkDocs(asoCache(cache, &src), official)
	if err != nil {
		return err
	}
	res, nsName, stats, err := readSpecs(specs, append(append([]*Kind(nil), official...), ours...))
	if err != nil {
		return err
	}
	rows, svcs := computeRows(res, nsName, official, ours)
	if verbose {
		stats.print(os.Stderr, rows, svcs, official, ours)
	}

	if spotFile != "" {
		return spotCheck(spotFile, spotList, rows, official, os.Stdout)
	}
	prio, err := readPriority(root, svcs, rows)
	if err != nil {
		return err
	}
	tiers := tierRollup(svcs, prio)
	cats := categoryRollup(rows, prio)
	files := outputs(rows, svcs, tiers, cats)
	avm, err := readAVM(root, rows)
	if err != nil {
		return err
	}
	return finish(root, check, files, []block{
		{"README.md", startMarker, endMarker, readmeSection(svcs, tiers, cats, prio)},
		{coverageDir + "/foundation.md", "<!-- foundation:start -->", "<!-- foundation:end -->", foundationTable(rows, res, prio, avm, cats)},
		{coverageDir + "/README.md", "<!-- numbers:start -->", "<!-- numbers:end -->", numbersBlock(&src, stats, docs, res, rows, svcs, tiers, cats, official, ours)},
	})
}

var pinRow = regexp.MustCompile("(?m)^\\| (Commit|Specs commit) \\| `([0-9a-f]{40})`")

// checkPins fails when UPSTREAM.md pins other commits than sources.yaml.
func checkPins(root string, src *sources) error {
	b, err := os.ReadFile(filepath.Join(root, "UPSTREAM.md"))
	if err != nil {
		return err
	}
	for _, m := range pinRow.FindAllStringSubmatch(string(b), -1) {
		want := src.ASO.Commit
		if m[1] == "Specs commit" {
			want = src.Specs.Commit
		}
		if m[2] != want {
			return fmt.Errorf("UPSTREAM.md %q is %s, docs/coverage/sources.yaml has %s: move the pin there too and rerun hack/coverage/run.sh", m[1], m[2], want)
		}
	}
	return nil
}

// block is a generated part of a hand-written file, between two markers.
type block struct {
	file       string // relative to the repository root
	start, end string
	body       string
}

// finish writes the outputs, or in check mode compares them with the disk.
func finish(root string, check bool, files map[string][]byte, blocks []block) error {
	dir := filepath.Join(root, coverageDir)
	// Generated paths: everything this run makes, plus stale files in the
	// directories that hold only generated files.
	have := map[string]bool{}
	for _, sub := range []string{"bars", "attributes"} {
		entries, _ := os.ReadDir(filepath.Join(dir, sub))
		for _, e := range entries {
			have[sub+"/"+e.Name()] = true
		}
	}
	for _, f := range []string{"services.csv", "resources.csv", "attributes.csv", "tiers.csv", "categories.csv"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			have[f] = true
		}
	}
	var diffs []string
	for _, p := range sortedKeys(files) {
		old, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil || !bytes.Equal(old, files[p]) {
			diffs = append(diffs, p)
		}
	}
	var stale []string
	for p := range have {
		if _, ok := files[p]; !ok {
			stale = append(stale, p)
		}
	}
	sort.Strings(stale)
	type edit struct {
		file string
		old  []byte
		new  []byte
	}
	var edits []edit
	for _, bl := range blocks {
		old, err := os.ReadFile(filepath.Join(root, bl.file))
		if err != nil {
			return err
		}
		nb, err := replaceBetween(old, bl.start, bl.end, bl.body)
		if err != nil {
			return fmt.Errorf("%s: %w", bl.file, err)
		}
		if !bytes.Equal(old, nb) {
			edits = append(edits, edit{bl.file, old, nb})
		}
	}
	if check {
		if len(diffs) == 0 && len(stale) == 0 && len(edits) == 0 {
			fmt.Println("coverage: committed outputs match a regeneration from the pinned inputs")
			return nil
		}
		var msg []string
		for _, d := range diffs {
			msg = append(msg, "differs: docs/coverage/"+d)
		}
		for _, s := range stale {
			msg = append(msg, "stale: docs/coverage/"+s)
		}
		for _, e := range edits {
			msg = append(msg, "differs: "+e.file+" (generated section)")
		}
		return fmt.Errorf("committed outputs are out of date; run hack/coverage/run.sh and commit:\n  %s", strings.Join(msg, "\n  "))
	}
	for _, p := range diffs {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, files[p], 0o644); err != nil {
			return err
		}
	}
	for _, s := range stale {
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(s))); err != nil {
			return err
		}
	}
	for _, e := range edits {
		if err := os.WriteFile(filepath.Join(root, e.file), e.new, 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("coverage: %d files in %s (%d changed), %d generated sections of hand-written files changed\n",
		len(files), coverageDir, len(diffs)+len(stale), len(edits))
	return nil
}

func replaceBetween(file []byte, start, end, body string) ([]byte, error) {
	s := string(file)
	i, j := strings.Index(s, start), strings.Index(s, end)
	if i < 0 || j < i {
		return nil, fmt.Errorf("no %s ... %s markers", start, end)
	}
	return []byte(s[:i+len(start)] + "\n" + body + s[j:]), nil
}
