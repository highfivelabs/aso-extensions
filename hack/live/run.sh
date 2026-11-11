#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: MIT
#
# Run the live tests (test/live, -tags live) against the real Azure
# subscription AZURE_SUBSCRIPTION_ID (hack/live/azure.env), from an offload VM whose service
# account the live-test identity trusts (docs/live-runs.md). `task test-live`
# calls this; task check never does.
#
#   ASO_EXT_LIVE=create-real-azure-resources hack/live/run.sh [TEST...]
#
# Refuses unless ASO_EXT_LIVE is exactly that and the target is in
# hack/live/targets.txt (checked again by the tests). TEST is a test name
# (default: every test in test/live). Before the run, leftovers older than
# 2h are swept; after it, pass or fail, everything the run created in the
# group is swept and what is left is listed.
#
# Output: offload-artifacts/live/<run ID>/{summary.md,live.log,left.txt}.
set -o errexit -o nounset -o pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"
# shellcheck source=hack/live/credentials.sh
source hack/live/credentials.sh
set -o allexport
# shellcheck source=hack/live/azure.env
source hack/live/azure.env
set +o allexport

if [[ "${ASO_EXT_LIVE:-}" != create-real-azure-resources ]]; then
  echo "live: a live run creates real Azure resources; set ASO_EXT_LIVE=create-real-azure-resources to mean it" >&2
  exit 2
fi
# targets.txt writes the subscription as ${AZURE_SUBSCRIPTION_ID} (the ID is
# not in the tree): put its value in before comparing.
targets() {
  local line
  while IFS= read -r line || [[ -n "${line}" ]]; do
    line="${line%%#*}"; line="${line//[[:space:]]/}"
    # shellcheck disable=SC2016 # the placeholder is literal text
    if [[ -n "${line}" ]]; then echo "${line//'${AZURE_SUBSCRIPTION_ID}'/${AZURE_SUBSCRIPTION_ID}}"; fi
  done < hack/live/targets.txt
}
grep -qx "${AZURE_SUBSCRIPTION_ID}/${LIVE_RESOURCE_GROUP}" <(targets) \
  || { echo "live: ${AZURE_SUBSCRIPTION_ID}/${LIVE_RESOURCE_GROUP} is not in hack/live/targets.txt" >&2; exit 2; }
: "${KUBEBUILDER_ASSETS:?run through task test-live, which installs envtest}"
pattern="."
[[ $# -gt 0 ]] && pattern="^($(IFS='|'; echo "$*"))$"

# Azure names carry the run ID: lower case, short.
run_id="ax$(date -u +%d%H%M)$(od -An -N2 -tx1 /dev/urandom | tr -d ' \n')"
out="offload-artifacts/live/${run_id}"
mkdir -p "${out}"
commit="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
dirty="$(git status --porcelain 2>/dev/null | wc -l | tr -d ' ')"
started="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

live_creds_start "$(mktemp -d)"
export LIVE_RUN_ID="${run_id}"
finish() {
  local code=$?
  set +o errexit
  echo "== live ${run_id}: cleaning up"
  hack/live/sweep.sh --since "${started}" || code=1
  hack/live/sweep.sh --list | tee "${out}/left.txt"
  live_creds_stop
  echo "== live ${run_id}: summary (${out}/summary.md)"
  cat "${out}/summary.md" 2>/dev/null
  exit "${code}"
}
trap finish EXIT

echo "== live ${run_id}: sweeping leftovers older than 2h"
hack/live/sweep.sh --before "$(date -u -d '-2 hours' +%Y-%m-%dT%H:%M:%SZ)"
hack/live/sweep.sh --list

echo "== live ${run_id}: ${AZURE_SUBSCRIPTION_ID}/${LIVE_RESOURCE_GROUP} in ${LIVE_LOCATION}"
live=PASS
t0=$(date +%s)
ASO_EXT_LIVE=create-real-azure-resources \
  go test -tags live -count=1 -v -timeout 230m -run "${pattern}" ./test/live/ 2>&1 | tee "${out}/live.log" \
  | grep -E --line-buffered '^\s*--- (PASS|FAIL|SKIP)|^(ok|FAIL)\s|_test.go:[0-9]+:|^=== RUN|^live:' || true
grep -q '^ok' "${out}/live.log" || live=FAIL
{
  echo "Run \`${run_id}\`, commit \`${commit}\` (${dirty} changed files), started ${started}, tests \`${pattern}\`."
  echo
  echo "| Test | Result |"
  echo "| --- | --- |"
  grep -E '^\s*--- (PASS|FAIL|SKIP)' "${out}/live.log" | sed -E 's/^\s*--- (PASS|FAIL|SKIP): ([^ ]+) \(([^)]*)\)/| \2 | \1 (\3) |/'
  echo
  echo "Live: ${live}, $(( $(date +%s) - t0 ))s."
} >"${out}/summary.md"
[[ "${live}" == PASS ]]
