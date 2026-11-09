#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: MIT
#
# Run the kind test (tests/operator/run.sh) and refuse to pass unless it
# really ran. tests/operator/run.sh was once truncated to zero bytes and
# `task operator-e2e` still exited 0, so a kind's test was never run. This
# wrapper fails when
#   - the script is missing, empty, not executable or shorter than
#     E2E_MIN_LINES (400; the script has about 500);
#   - the script exits non-zero;
#   - the last non-empty line of its output is not "all checks passed";
#   - an "ok: ..." line listed in tests/operator/required-ok.txt is missing
#     (one line per kind and per step the script must still run; a PR that
#     adds a kind adds its lines there, and one that removes a check has to
#     remove its line, which a reviewer sees).
#
# The output is captured to a file first and searched there, never piped into
# `grep -q` (grep stops at the first match, the writer gets SIGPIPE and
# pipefail fails the pipeline whenever the log is long).
#
# For testing the wrapper itself (hack/test-operator-e2e-guard.sh):
#   E2E_SCRIPT, E2E_REQUIRED, E2E_MIN_LINES, E2E_LOG

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

script="${E2E_SCRIPT:-tests/operator/run.sh}"
required="${E2E_REQUIRED:-tests/operator/required-ok.txt}"
min_lines="${E2E_MIN_LINES:-400}"
log="${E2E_LOG:-bin/operator-e2e/last-run.log}"

bad() { echo "operator-e2e: FAIL: $*" >&2; exit 1; }

[[ -e "${script}" ]] || bad "${script} does not exist"
[[ -s "${script}" ]] || bad "${script} is empty"
[[ -x "${script}" ]] || bad "${script} is not executable"
lines="$(wc -l < "${script}" | tr -d ' ')"
(( lines >= min_lines )) || bad "${script} has ${lines} lines, want at least ${min_lines} (truncated?)"
[[ -s "${required}" ]] || bad "${required} is missing or empty"

mkdir -p "$(dirname "${log}")"
rc=0
"${script}" 2>&1 | tee "${log}" || rc=$?
(( rc == 0 )) || bad "${script} exited ${rc}"

last="$(awk 'NF { l = $0 } END { print l }' "${log}")"
[[ "${last}" == "all checks passed" ]] || bad "the output of ${script} ends with '${last}', want 'all checks passed'"

missing=0
count=0
while IFS= read -r want || [[ -n "${want}" ]]; do
  [[ -z "${want}" || "${want}" == \#* ]] && continue
  count=$((count + 1))
  if ! grep -qF -- "ok: ${want}" "${log}"; then
    echo "operator-e2e: no 'ok: ${want}' line in the output" >&2
    missing=$((missing + 1))
  fi
done < "${required}"
(( missing == 0 )) || bad "${missing} of ${count} required checks did not report"
echo "operator-e2e: ok: ${script} ran, ended with 'all checks passed' and reported all ${count} required checks"
