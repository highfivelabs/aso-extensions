#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: MIT
# Run ASO's generator (hack/generate-arm.sh) until it passes, writing the
# reference labels it asks for between runs (hack/autolabel), at most
# ITERATIONS times (default 8); then hack/generate.sh. One offload run does
# what took a run per label error before (docs/throughput.md):
#
#   offload run --prep 'task tools' --step 'hack/generate-iterate.sh'
#
# It writes offload-artifacts/generate.tgz (apis, the registration file,
# config, generator/) and each run's log, offload-artifacts/generate-<n>.log.
# It exits non-zero when the generator still fails with an error autolabel
# cannot write: read the last log.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"
mkdir -p offload-artifacts

pack() {
  local paths=()
  for p in apis pkg/controllers/arm_resources_gen.go config generator; do
    if [[ -e "${p}" ]]; then paths+=("${p}"); fi
  done
  tar czf offload-artifacts/generate.tgz "${paths[@]}"
}

max="${ITERATIONS:-8}"
for ((n = 1; n <= max; n++)); do
  log="offload-artifacts/generate-${n}.log"
  echo "generate-iterate: run ${n} of at most ${max}"
  status=0
  hack/generate-arm.sh > "${log}" 2>&1 || status=$?
  tail -n 5 "${log}"
  if [[ "${status}" -eq 0 ]]; then
    echo "generate-iterate: the generator passed on run ${n}; hack/generate.sh"
    gen_status=0
    hack/generate.sh > offload-artifacts/generate-sh.log 2>&1 || gen_status=$?
    tail -n 20 offload-artifacts/generate-sh.log
    pack
    exit "${gen_status}"
  fi
  label_status=0
  go run ./hack/autolabel "${log}" || label_status=$?
  if [[ "${label_status}" -ne 0 ]]; then
    pack
    echo "generate-iterate: run ${n} failed with errors autolabel cannot write (exit ${status}); see ${log}" >&2
    grep -E ' E[0-9]{4}|error|Error' "${log}" | tail -n 30 >&2 || true
    exit 1
  fi
done
pack
echo "generate-iterate: still failing after ${max} runs" >&2
exit 1
