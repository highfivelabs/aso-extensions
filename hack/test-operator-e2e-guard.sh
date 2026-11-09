#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: MIT
#
# Tests hack/operator-e2e.sh with scripts that pass by doing nothing, and one
# that does not. Needs no cluster.
set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"
dir="$(mktemp -d)"
trap 'rm -rf "${dir}"' EXIT

required="${dir}/required.txt"
printf '# a comment\nthing one\nthing two\n' > "${required}"

wrapper() { E2E_REQUIRED="${required}" E2E_MIN_LINES=3 E2E_LOG="${dir}/log" E2E_SCRIPT="$1" hack/operator-e2e.sh >"${dir}/out" 2>&1; }

mk() { # mk <name> <body...>: an executable script
  local f="${dir}/$1"; shift
  { echo '#!/usr/bin/env bash'; printf '%s\n' "$@"; } > "${f}"
  chmod +x "${f}"
  echo "${f}"
}

expect_fail() { # expect_fail <what> <script> <message in the output>
  if wrapper "$2"; then echo "FAIL: ${1} passed" >&2; cat "${dir}/out" >&2; exit 1; fi
  grep -qF -- "$3" "${dir}/out" || { echo "FAIL: ${1} failed for another reason:" >&2; cat "${dir}/out" >&2; exit 1; }
  echo "ok: ${1} fails"
}

: > "${dir}/empty.sh"; chmod +x "${dir}/empty.sh"
expect_fail "an empty script" "${dir}/empty.sh" "is empty"
expect_fail "a missing script" "${dir}/nonexistent.sh" "does not exist"
expect_fail "a script that is not executable" "$(f="${dir}/plain.sh"; printf 'a\nb\nc\nd\n' > "${f}"; echo "${f}")" "not executable"
expect_fail "a short script" "$(mk short.sh 'true')" "truncated?"
expect_fail "a script that prints nothing and exits 0" "$(mk silent.sh ':' ':' ':')" "ends with ''"
expect_fail "a script that never prints the last line" "$(mk nolast.sh 'echo "ok: thing one"' 'echo "ok: thing two"' 'echo done')" "ends with 'done'"
expect_fail "a script that prints it and then fails" "$(mk failing.sh 'echo "all checks passed"' 'exit 3' ':')" "exited 3"
expect_fail "a script that skips a required check" "$(mk skips.sh 'echo "ok: thing one"' ':' 'echo "all checks passed"')" "1 of 2 required"
# A long log must not trip pipefail (grep -q on a pipe).
good="$(mk good.sh 'seq 1 200000' 'echo "ok: thing one"' 'echo "ok: thing two: more"' 'echo' 'echo "all checks passed"' 'echo')"
wrapper "${good}" || { echo "FAIL: a good script was refused:" >&2; cat "${dir}/out" >&2; exit 1; }
echo "ok: a script that prints every required line and ends with 'all checks passed' passes"
echo "all guard tests passed"
