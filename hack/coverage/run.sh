#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: MIT
# The coverage dashboard (docs/coverage/README.md): how much of the Azure
# Resource Manager API Azure Service Operator's CRDs cover, and how much more
# ours do.
#
#   hack/coverage/run.sh            regenerate docs/coverage/ and the Coverage
#                                   section of README.md from the pinned inputs
#   hack/coverage/run.sh --check    fail if what is committed differs from a
#                                   regeneration (for CI and before a commit)
#   hack/coverage/run.sh --test     the generator's own unit tests
#   hack/coverage/run.sh -v         also print what was found
#   hack/coverage/run.sh --spotcheck FILE
#                                   compare with the CRDs of a release, for example
#                                   gh release download v2.22.0 --repo Azure/azure-service-operator \
#                                     -p azureserviceoperator_customresourcedefinitions_v2.22.0.yaml -D .cache/spotcheck
#                                   (the result is recorded in docs/coverage/README.md)
#
# The inputs are pinned in docs/coverage/sources.yaml and cached under
# .cache/coverage (gitignored): a blob-less depth-1 fetch of two commits,
# ASO's (its ARM wire types, about 20 MB) and the OpenAPI specs' (the latest
# version of each spec, about 100 MB; the repository is several GB). The
# first run fetches them, which takes several minutes; later runs read the
# cache and take about a minute.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${REPO_ROOT}"
export GOTOOLCHAIN="${GOTOOLCHAIN:-local}"

if [[ "${1:-}" == "--test" ]]; then
  cd hack/coverage
  go vet ./...
  exec go test ./... -count=1
fi

mkdir -p .cache/coverage/bin
(cd hack/coverage && go build -o "${REPO_ROOT}/.cache/coverage/bin/coverage" .)
args=()
for a in "$@"; do
  case "${a}" in
    --check) args+=(-check) ;;
    --spotcheck) args+=(-spotcheck) ;;
    *) args+=("${a}") ;;
  esac
done
exec .cache/coverage/bin/coverage -root "${REPO_ROOT}" "${args[@]+"${args[@]}"}"
