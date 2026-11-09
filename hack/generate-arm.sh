#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: MIT
# Run ASO's generator (patched, patches/azure-service-operator/) over the
# ARM specs with generator/azure-arm.yaml: Go types, ARM wire types, storage
# versions and conversions, extension points and the registration file for
# the ARM kinds in our groups. Heavy (the generator reads every spec of the
# services in generator/specs.txt): run it with `../offload/offload run
# --ubuntu`, not on a laptop. hack/generate.sh then makes deepcopy code and
# CRDs from what this writes.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

hack/upstream.sh --check
hack/specs.sh

GEN_DIR="third_party/azure-service-operator/v2/tools/generator"
mkdir -p bin
(cd "${GEN_DIR}" && go build -o "${REPO_ROOT}/bin/generator" .)

# What the patched generator reads (patches/azure-service-operator/0002):
# our groups end in .azure.hfl.dev; the owners listed in
# generator/external-owners.txt are ASO's and keep .azure.com; no webhooks.
owners="$(sed -e 's/#.*//' -e 's/[[:space:]]//g' generator/external-owners.txt | grep . | paste -sd, -)"
export ASO_GENERATOR_GROUP_SUFFIX=".azure.hfl.dev"
export ASO_GENERATOR_EXTERNAL_OWNERS="${owners}"
export ASO_GENERATOR_NO_WEBHOOKS=1
export ASO_GENERATOR_API_STORAGE=1
export ASO_GENERATOR_NO_NESTED_FLATTEN=1
export ASO_GENERATOR_TENANT_CHILDREN=1

rm -rf apis/arm pkg/controllers/arm_resources_gen.go generator/reports
mkdir -p generator/reports/fragments
bin/generator gen-types generator/azure-arm.yaml
