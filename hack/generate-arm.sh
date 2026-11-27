#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: Apache-2.0
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
export ASO_GENERATOR_MULTI_SCOPE=1
export ASO_GENERATOR_SUBRESOURCE_RESOURCES=1
export ASO_GENERATOR_RECEIVER_NOT_PARAMETER=1
export ASO_GENERATOR_SUBSCRIPTION_EXTENSION=1
export ASO_GENERATOR_MORE_EXTENSION_SCOPES=1
export ASO_GENERATOR_RAPID_ONEOF_RELATED=1
export ASO_GENERATOR_EXTENSION_CHILDREN=1
# A PUT body when the path declares its own parameters (patch 0017).
export ASO_GENERATOR_PATH_PARAMETERS=1
# A property marked flatten whose type has no definition (patch 0018).
export ASO_GENERATOR_FLATTEN_EXTERNAL=1
# A spec and status pair visited once by the spec initialization scanner (patch 0019).
export ASO_GENERATOR_SPEC_INIT_CYCLES=1
# CEL rules for what ASO's webhooks would check on update (patch 0016):
# spec.owner and a set spec.azureName cannot change.
export ASO_GENERATOR_CEL_VALIDATIONS=1
# Fixed-name child resources (patch 0014; generator/fixed-names.txt).
export ASO_GENERATOR_FIXED_NAMES="$(sed -e 's/#.*//' -e 's/[[:space:]]//g' generator/fixed-names.txt | grep . | paste -sd, -)"
# A type transformer's named target from a property of no named type (patch 0021).
export ASO_GENERATOR_NAMED_TARGET_FROM_ANY=1
# One resource per member of a listed union (patch 0020; generator/split-oneof.txt).
export ASO_GENERATOR_SPLIT_ONEOF="$(sed -e 's/#.*//' -e 's/[[:space:]]//g' generator/split-oneof.txt | grep . | paste -sd, -)"

rm -rf apis/arm pkg/controllers/arm_resources_gen.go generator/reports
mkdir -p generator/reports/fragments
# GENERATOR_ARGS: extra flags, such as --verbose (hack/generate-iterate.sh).
# shellcheck disable=SC2086
bin/generator gen-types ${GENERATOR_ARGS:-} generator/azure-arm.yaml

# Hand-written extensions of generated kinds (an ASO customizations file, as
# ASO keeps beside its generated ones: a Claimer, an ARMResourceModifier).
# They live outside apis/arm, which is deleted above on every run, in
# generator/_extensions/ (a directory the go tool ignores, for its leading
# underscore), mirroring apis/arm/; copied in after the generator has written
# the package each one extends.
if [[ -d generator/_extensions ]]; then
  (cd generator/_extensions && find . -name '*.go' -type f) | while IFS= read -r f; do
    dest="apis/arm/${f#./}"
    [[ -d "$(dirname "${dest}")" ]] || { echo "generator/_extensions/${f#./}: the generator wrote no $(dirname "${dest}")" >&2; exit 1; }
    cp "generator/_extensions/${f#./}" "${dest}"
  done
fi
