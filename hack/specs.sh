#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: MIT
# Fetch the Azure REST API specs ASO's generator reads, at the commit ASO's
# own submodule pins (the "Specs commit" row of UPSTREAM.md), into
# .cache/azure-rest-api-specs: a blob-less, depth-1 fetch with a sparse
# checkout of the services listed in generator/specs.txt (and
# specification/common-types, which every service references). The whole
# repository is several GB; the services we generate are a few MB.
#
#   hack/specs.sh          fetch if missing, check out the pin and the services

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

SPECS_COMMIT="$(sed -n 's/^| Specs commit | `\([0-9a-f]\{40\}\)`.*/\1/p' UPSTREAM.md | head -1)"
SPECS_REPO="https://github.com/Azure/azure-rest-api-specs.git"
DEST=".cache/azure-rest-api-specs"

if [[ -z "${SPECS_COMMIT}" ]]; then
  echo "could not read the specs commit from UPSTREAM.md" >&2
  exit 1
fi

mkdir -p "$(dirname "${DEST}")"
if [[ ! -d "${DEST}/.git" ]]; then
  git init --quiet "${DEST}"
  git -C "${DEST}" remote add origin "${SPECS_REPO}"
  git -C "${DEST}" config remote.origin.promisor true
  git -C "${DEST}" config remote.origin.partialclonefilter blob:none
fi

dirs=(specification/common-types)
while IFS= read -r line; do
  line="${line%%#*}"
  line="$(echo "${line}" | tr -d '[:space:]')"
  [[ -n "${line}" ]] && dirs+=("${line}")
done < generator/specs.txt

git -C "${DEST}" sparse-checkout init --cone
git -C "${DEST}" sparse-checkout set "${dirs[@]}"
if ! git -C "${DEST}" cat-file -e "${SPECS_COMMIT}^{commit}" 2>/dev/null; then
  git -C "${DEST}" fetch --quiet --depth 1 --filter=blob:none origin "${SPECS_COMMIT}"
fi
git -C "${DEST}" -c advice.detachedHead=false checkout --quiet --force "${SPECS_COMMIT}"
echo "${DEST} is ${SPECS_COMMIT} with: ${dirs[*]}"
