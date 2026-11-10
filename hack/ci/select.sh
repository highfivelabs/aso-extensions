#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: MIT
#
# What a pull request must run, from the paths it changes (the builder,
# 2026-11-10: the whole suite nightly, otherwise only what changed).
#
#   hack/ci/select.sh plan BASE HEAD   the plan for git diff BASE...HEAD
#   hack/ci/select.sh plan-files       the plan for the paths on stdin
#   hack/ci/select.sh run GROUP...     check:static, build, the groups' tests
#   hack/ci/select.sh tests GROUP...   the groups' test/e2e tests
#
# The plan, as GITHUB_OUTPUT lines:
#   mode=none    docs only: nothing heavy runs
#   mode=groups  check:static, build and the tests of the groups only
#   mode=full    task check
#   groups=...   the groups (mode=groups)
#   e2e=true     task operator-e2e runs too (on a Spot runner)
#
# Each path is one of:
#   docs      *.md, docs/, LICENSE, NOTICE; UPSTREAM.md unless a row
#             with a commit (the pin) changed
#   group G   apis/arm/G/ and generator/azure-arm/G.yaml (group arm-G:
#             ./apis/arm/G/... and the test/e2e tests in files that import
#             it); apis/G/ and pkg/reconcilers/G/ (group G, the same way);
#             test/e2e/F_test.go with tests (group e2e:F: that file's
#             tests); pkg/fakecloud/ (group fakecloud: its own tests);
#             hack/coverage/ (group coverage)
#   adjacent  what a kind's pull request also touches and check:static or
#             the operator e2e covers: config/crds/, generator/reports/,
#             pkg/controllers/arm_resources_gen.go, tests/operator/
#   full      everything else: go.mod, patches/, the pin, upstream.yaml,
#             cmd/, pkg/ (the rest), generator/ (the rest), hack/, a test/e2e
#             file without tests (the harness), Taskfile.yml, taskfiles/,
#             config/ (the rest), .github/
# A pull request is full if any path is; else groups if any path is a
# group or adjacent; else none. The nightly run (nightly.yml) runs
# everything on main, which catches what a group's tests miss.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
MODULE="$(sed -n "s/^module //p" go.mod)"

# classify PATH: prints "docs", "adjacent", "full" or "group G".
classify() {
  local p="$1" g
  case "$p" in
    # UPSTREAM.md holds the pin (a table row with a commit) and prose that
    # a kind's pull request updates; only a changed pin row is full.
    UPSTREAM.md) if [[ "${PIN_CHANGED:-1}" == 1 ]]; then echo full; else echo docs; fi ;;
    *.md|docs/*|LICENSE|NOTICE) echo docs ;;
    apis/arm/*/*)
      g="${p#apis/arm/}"; echo "group arm-${g%%/*}" ;;
    generator/azure-arm/*.yaml)
      g="${p#generator/azure-arm/}"; echo "group arm-${g%.yaml}" ;;
    apis/*/*)
      g="${p#apis/}"; echo "group ${g%%/*}" ;;
    pkg/reconcilers/*/*)
      g="${p#pkg/reconcilers/}"; echo "group ${g%%/*}" ;;
    test/e2e/*_test.go)
      if [[ -f "$p" ]] && grep -q '^func Test' "$p"; then
        g="${p#test/e2e/}"; echo "group e2e:${g%_test.go}"
      else
        echo full
      fi ;;
    pkg/fakecloud/*) echo "group fakecloud" ;;
    hack/coverage/*) echo "group coverage" ;;
    config/crds/*|generator/reports/*|pkg/controllers/arm_resources_gen.go|tests/operator/*) echo adjacent ;;
    *) echo full ;;
  esac
}

# pkgs GROUP: the group's Go packages (tested whole).
pkgs() {
  local d
  case "$1" in
    e2e:*) ;;
    fakecloud) echo ./pkg/fakecloud/... ;;
    coverage) echo ./hack/coverage/... ;;
    arm-*) [[ -d "apis/arm/${1#arm-}" ]] && echo "./apis/arm/${1#arm-}/..." ;;
    *) for d in "apis/$1" "pkg/reconcilers/$1"; do [[ -d "$d" ]] && echo "./$d/..."; done ;;
  esac
  return 0
}

# e2e_tests GROUP: the group's test/e2e tests (names, one a line).
e2e_tests() {
  local files
  case "$1" in
    e2e:*) files="test/e2e/${1#e2e:}_test.go" ;;
    fakecloud|coverage) return 0 ;;
    arm-*) files="$(grep -lF "\"$MODULE/apis/arm/${1#arm-}/" test/e2e/*_test.go 2>/dev/null || true)" ;;
    *) files="$(grep -lE "\"$MODULE/(apis|pkg/reconcilers)/$1[/\"]" test/e2e/*_test.go 2>/dev/null || true)" ;;
  esac
  [[ -n "$files" ]] || return 0
  # shellcheck disable=SC2086
  sed -nE 's/^func (Test[A-Za-z0-9_]*)\(t \*testing\.T\).*/\1/p' $files
}

plan() {
  local p c mode=none groups="" full="" lines=""
  while IFS= read -r p; do
    [[ -n "$p" ]] || continue
    c="$(classify "$p")"
    lines="$lines$c	$p
"
    case "$c" in
      full) full=1 ;;
      group*) groups="$groups ${c#group }"; [[ "$mode" == none ]] && mode=groups ;;
      adjacent) [[ "$mode" == none ]] && mode=groups ;;
    esac
  done
  [[ -n "$full" ]] && mode=full
  groups="$(echo "$groups" | tr ' ' '\n' | sed '/^$/d' | sort -u | tr '\n' ' ' | sed 's/ $//')"
  [[ "$mode" == groups ]] || groups=""
  local e2e=false
  [[ "$mode" == none ]] || e2e=true
  echo "mode=$mode"
  echo "groups=$groups"
  echo "e2e=$e2e"
  {
    echo "### What this pull request runs: $mode${groups:+ ($groups)}"
    echo
    echo "| Class | Paths | Example |"
    echo "| --- | --- | --- |"
    printf '%s' "$lines" | awk -F'\t' 'NF == 2 { n[$1]++; if (!($1 in ex)) ex[$1] = $2 } END { for (c in n) printf "| %s | %d | `%s` |\n", c, n[c], ex[c] }' | sort
  } >&2
}

run_groups() {
  [[ $# -gt 0 ]] || echo "select.sh run: no groups; running check:static and build only"
  local g units="" tests=""
  for g in "$@"; do
    units="$units $(pkgs "$g" | tr '\n' ' ')"
    tests="$tests $(e2e_tests "$g" | tr '\n' ' ')"
  done
  units="$(echo "$units" | xargs)"
  tests="$(echo "$tests" | tr ' ' '\n' | sed '/^$/d' | sort -u | paste -sd'|' -)"
  task check:static
  task build
  if [[ -n "$units" ]]; then
    echo "select.sh: the packages of $*: $units"
    task test:pkgs PKGS="$units"
  fi
  if [[ -n "$tests" ]]; then
    echo "select.sh: the end-to-end tests of $*: $tests"
    task test:pkgs PKGS=./test/e2e RUN="^($tests)\$"
  fi
}

case "${1:-}" in
  plan)
    [[ $# -eq 3 ]] || { echo "usage: hack/ci/select.sh plan BASE HEAD" >&2; exit 2; }
    # A diff that fails must fail the plan, never read as "nothing changed".
    files="$(git diff --name-only --no-renames "$2...$3")"
    pin="$(git diff "$2...$3" -- UPSTREAM.md)"
    PIN_CHANGED=0
    if grep -qE '^[-+][|].*[0-9a-f]{40}' <<<"$pin"; then PIN_CHANGED=1; fi
    PIN_CHANGED="$PIN_CHANGED" plan <<<"$files" ;;
  plan-files) plan ;;
  run) shift; run_groups "$@" ;;
  tests) shift; for g in "$@"; do e2e_tests "$g"; done ;;
  *) echo "usage: hack/ci/select.sh plan BASE HEAD | plan-files | run GROUP... | tests GROUP..." >&2; exit 2 ;;
esac
