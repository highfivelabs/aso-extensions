// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// gitCache is a blob-less, depth-1 clone of one commit of a big repository.
// Only the files asked for are written to its work tree (git fetches their
// blobs in one batch), so the multi-gigabyte specs repository costs tens of
// megabytes.
type gitCache struct {
	dir    string // work tree, under .cache/
	remote string
	commit string
	oids   map[string]string // path -> blob id, read once
}

func (g *gitCache) git(stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"--literal-pathspecs", "-C", g.dir}, args...)...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// open creates the clone if it is missing and fetches the commit's trees.
func (g *gitCache) open() error {
	if err := os.MkdirAll(g.dir, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(g.dir, ".git")); err != nil {
		if _, err := g.git(nil, "init", "--quiet"); err != nil {
			return err
		}
		for _, a := range [][]string{
			{"remote", "add", "origin", g.remote},
			{"config", "remote.origin.promisor", "true"},
			{"config", "remote.origin.partialclonefilter", "blob:none"},
			{"config", "core.sparseCheckout", "false"},
		} {
			if _, err := g.git(nil, a...); err != nil {
				return err
			}
		}
	}
	if _, err := g.git(nil, "cat-file", "-e", g.commit+"^{commit}"); err != nil {
		fmt.Fprintf(os.Stderr, "fetching %s at %s (trees only)\n", g.remote, g.commit)
		if _, err := g.git(nil, "fetch", "--quiet", "--depth", "1", "--filter=blob:none", "origin", g.commit); err != nil {
			return err
		}
	}
	return nil
}

// list returns the files of the commit under prefix, sorted.
func (g *gitCache) list(prefix string) ([]string, error) {
	out, err := g.git(nil, "ls-tree", "-r", "--name-only", g.commit, "--", prefix)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, l)
		}
	}
	sort.Strings(files)
	return files, nil
}

// ensure writes the given files (paths in the repository) to the work tree
// when they are not there yet.
func (g *gitCache) ensure(paths []string) error {
	var missing []string
	for _, p := range paths {
		// An empty file is what an interrupted checkout leaves.
		if st, err := os.Stat(filepath.Join(g.dir, p)); err != nil || st.Size() == 0 {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	fmt.Fprintf(os.Stderr, "fetching %d files from %s\n", len(missing), g.remote)
	if err := g.prefetch(missing); err != nil {
		return err
	}
	var in bytes.Buffer
	for _, p := range missing {
		in.WriteString(p)
		in.WriteByte(0)
	}
	_, err := g.git(in.Bytes(), "-c", "advice.detachedHead=false", "checkout", "--quiet", "--force", g.commit,
		"--pathspec-from-file=-", "--pathspec-file-nul")
	return err
}

// prefetch fetches the blobs of the given paths in one request. Without it
// git fetches them one by one while checking them out, which takes minutes.
func (g *gitCache) prefetch(paths []string) error {
	if g.oids == nil {
		out, err := g.git(nil, "ls-tree", "-r", "-z", g.commit)
		if err != nil {
			return err
		}
		g.oids = map[string]string{}
		for _, ent := range strings.Split(string(out), "\x00") {
			meta, p, ok := strings.Cut(ent, "\t")
			if !ok {
				continue
			}
			if f := strings.Fields(meta); len(f) == 3 {
				g.oids[p] = f[2]
			}
		}
	}
	var in bytes.Buffer
	for _, p := range paths {
		if oid, ok := g.oids[p]; ok {
			in.WriteString(oid + "\n")
		}
	}
	_, err := g.git(in.Bytes(), "-c", "fetch.negotiationAlgorithm=noop", "fetch", "origin", "--no-tags",
		"--no-write-fetch-head", "--recurse-submodules=no", "--filter=blob:none", "--stdin")
	return err
}
