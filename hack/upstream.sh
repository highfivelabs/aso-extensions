#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: MIT
# Fetch Azure Service Operator at the commit pinned in UPSTREAM.md into
# third_party/azure-service-operator, then apply our patch series from
# patches/azure-service-operator/ in order.
#
# The checkout is not committed (it is about 675 MB). go.mod points at it
# with replace directives, so nothing builds until this has run.
#
#   hack/upstream.sh          fetch if missing, reset to the pin, apply patches
#   hack/upstream.sh --check  fail if the checkout is not the pin plus patches

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

# The single source of the pin: the "Commit" row of UPSTREAM.md.
ASO_COMMIT="$(sed -n 's/^| Commit | `\([0-9a-f]\{40\}\)`.*/\1/p' UPSTREAM.md | head -1)"
ASO_REPO="https://github.com/Azure/azure-service-operator.git"
DEST="third_party/azure-service-operator"
PATCHES="${REPO_ROOT}/patches/azure-service-operator"

if [[ -z "${ASO_COMMIT}" ]]; then
  echo "could not read the Azure Service Operator commit from UPSTREAM.md" >&2
  exit 1
fi

if [[ "${1:-}" == "--check" ]]; then
  if [[ ! -d "${DEST}/.git" ]]; then
    echo "${DEST} is missing; run hack/upstream.sh" >&2
    exit 1
  fi
  head="$(git -C "${DEST}" rev-parse HEAD)"
  if [[ "${head}" != "${ASO_COMMIT}" ]]; then
    echo "${DEST} is at ${head}, UPSTREAM.md pins ${ASO_COMMIT}" >&2
    exit 1
  fi
  # Every patch must be applied, and nothing else changed.
  expected="$(mktemp)"
  actual="$(mktemp)"
  trap 'rm -f "${expected}" "${actual}"' EXIT
  for p in "${PATCHES}"/*.patch; do
    [[ -e "${p}" ]] || continue
    git -C "${DEST}" apply --numstat "${p}" | awk '{print $3}'
  done | sort -u > "${expected}"
  git -C "${DEST}" status --porcelain --untracked-files=all | awk '{print $2}' | sort -u > "${actual}"
  if ! diff -u "${expected}" "${actual}"; then
    echo "${DEST} differs from the pin plus patches/ (above: expected vs actual changed files)" >&2
    exit 1
  fi
  echo "ok: ${DEST} is ${ASO_COMMIT} plus $(ls "${PATCHES}"/*.patch 2>/dev/null | wc -l | tr -d ' ') patch(es)"
  exit 0
fi

mkdir -p "$(dirname "${DEST}")"
if [[ ! -d "${DEST}/.git" ]]; then
  git init --quiet "${DEST}"
  git -C "${DEST}" remote add origin "${ASO_REPO}"
fi
if ! git -C "${DEST}" cat-file -e "${ASO_COMMIT}^{commit}" 2>/dev/null; then
  git -C "${DEST}" fetch --quiet --depth 1 origin "${ASO_COMMIT}"
fi
git -C "${DEST}" -c advice.detachedHead=false checkout --quiet --force "${ASO_COMMIT}"
git -C "${DEST}" reset --quiet --hard "${ASO_COMMIT}"
git -C "${DEST}" clean --quiet -fdx

for p in "${PATCHES}"/*.patch; do
  [[ -e "${p}" ]] || continue
  echo "applying $(basename "${p}")"
  git -C "${DEST}" apply --whitespace=nowarn "${p}"
done

echo "${DEST} is ${ASO_COMMIT} plus our patches"
