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
#   hack/upstream.sh --check  fail unless every file of the checkout holds
#                             what the pin plus patches give (by content)

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

# The single source of the pin: the "Commit" row of UPSTREAM.md.
# UPSTREAM_COMMIT, UPSTREAM_DEST and UPSTREAM_PATCHES replace the pin, the
# checkout and the patch directory for hack/test-upstream-check.sh only.
ASO_COMMIT="${UPSTREAM_COMMIT:-$(sed -n 's/^| Commit | `\([0-9a-f]\{40\}\)`.*/\1/p' UPSTREAM.md | head -1)}"
ASO_REPO="https://github.com/Azure/azure-service-operator.git"
DEST="${UPSTREAM_DEST:-third_party/azure-service-operator}"
PATCHES="${UPSTREAM_PATCHES:-${REPO_ROOT}/patches/azure-service-operator}"

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
  # Every file must hold what the pin plus the patches give, byte for byte.
  # The expected tree is built in a temporary index (the pin's tree with
  # each patch applied to it in order), and the working tree is compared to
  # it by content. Comparing only the list of changed files let a stale copy
  # pass: an older checkout lacked a later patch's change to a file an
  # earlier patch also changes.
  index="$(mktemp)"
  out="$(mktemp)"
  trap 'rm -f "${index}" "${out}"' EXIT
  export GIT_INDEX_FILE="${index}"
  git -C "${DEST}" read-tree "${ASO_COMMIT}"
  for p in "${PATCHES}"/*.patch; do
    [[ -e "${p}" ]] || continue
    if ! git -C "${DEST}" apply --cached --whitespace=nowarn "${p}" 2>"${out}"; then
      cat "${out}" >&2
      echo "$(basename "${p}") does not apply to the pin plus the patches before it" >&2
      exit 1
    fi
  done
  git -C "${DEST}" update-index -q --refresh >/dev/null || true
  # Tracked files whose content or mode differs, or that are missing; then
  # files that are neither expected nor ignored.
  git -C "${DEST}" diff --name-status > "${out}"
  git -C "${DEST}" ls-files --others --exclude-standard | sed 's/^/?\t/' >> "${out}"
  unset GIT_INDEX_FILE
  if [[ -s "${out}" ]]; then
    cat "${out}" >&2
    echo "${DEST} differs from the pin plus patches/ in the files above (M changed, D missing, ? not expected); run hack/upstream.sh" >&2
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
