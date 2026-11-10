#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: MIT
#
# Tests `hack/upstream.sh --check` on a small repository standing in for the
# upstream checkout, with two patches that change the same file (as 0005 and
# 0006 do). The check must pass the pin plus both patches and fail anything
# else, including a copy that has only the first patch: the same files
# changed, other content. Needs no upstream checkout or network.
set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"
dir="$(mktemp -d)"
trap 'rm -rf "${dir}"' EXIT

up="${dir}/upstream"
patches="${dir}/patches"
mkdir -p "${up}" "${patches}"
g() { git -C "${up}" -c user.name=test -c user.email=test@example.invalid "$@"; }
g init --quiet
printf 'one\ntwo\nthree\n' > "${up}/a.txt"
printf 'kept\n' > "${up}/b.txt"
printf 'build/\n' > "${up}/.gitignore"
g add -A
g commit --quiet -m pin
pin="$(g rev-parse HEAD)"

# Patch 1 changes a.txt and adds c.txt; patch 2 changes a.txt again.
printf 'ONE\ntwo\nthree\n' > "${up}/a.txt"
printf 'new\n' > "${up}/c.txt"
g add -A
g diff --cached > "${patches}/0001-first.patch"
g commit --quiet -m first
printf 'ONE\ntwo\nTHREE\n' > "${up}/a.txt"
g diff > "${patches}/0002-second.patch"
g reset --quiet --hard "${pin}"

check() { UPSTREAM_COMMIT="${pin}" UPSTREAM_DEST="${up}" UPSTREAM_PATCHES="${patches}" hack/upstream.sh --check >"${dir}/out" 2>&1; }
apply() { for p in "$@"; do git -C "${up}" apply "${patches}/${p}"; done; }
reset() { g reset --quiet --hard "${pin}"; g clean --quiet -fdx; }

expect_fail() { # expect_fail <what> <message in the output>
  if check; then echo "FAIL: ${1} passed" >&2; cat "${dir}/out" >&2; exit 1; fi
  grep -qF -- "$2" "${dir}/out" || { echo "FAIL: ${1} failed for another reason:" >&2; cat "${dir}/out" >&2; exit 1; }
  echo "ok: ${1} fails"
  reset
}

apply 0001-first.patch 0002-second.patch
check || { echo "FAIL: the pin plus both patches was refused:" >&2; cat "${dir}/out" >&2; exit 1; }
echo "ok: the pin plus both patches passes"
mkdir -p "${up}/build" && touch "${up}/build/ignored"
check || { echo "FAIL: an ignored file was refused:" >&2; cat "${dir}/out" >&2; exit 1; }
echo "ok: an ignored file passes"
reset

expect_fail "the bare pin" "a.txt"
apply 0001-first.patch
expect_fail "a stale copy with the first patch only (same files changed)" "M	a.txt"
apply 0001-first.patch 0002-second.patch; echo extra >> "${up}/b.txt"
expect_fail "an edit to an unpatched file" "M	b.txt"
apply 0001-first.patch 0002-second.patch; rm "${up}/c.txt"
expect_fail "a missing patched file" "D	c.txt"
apply 0001-first.patch 0002-second.patch; touch "${up}/d.txt"
expect_fail "a file nothing adds" "?	d.txt"
apply 0001-first.patch 0002-second.patch; chmod +x "${up}/b.txt"
expect_fail "a changed file mode" "M	b.txt"
printf 'not a patch\n' > "${patches}/0003-broken.patch"
expect_fail "a patch that does not apply" "0003-broken.patch does not apply"
rm "${patches}/0003-broken.patch"
apply 0001-first.patch 0002-second.patch; g -c advice.detachedHead=false commit --quiet -am moved
expect_fail "a checkout at another commit" "UPSTREAM.md pins"
echo "all upstream check tests passed"
