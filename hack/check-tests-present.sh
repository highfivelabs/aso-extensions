#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: MIT
#
# `go test` passes when a test file is emptied or deleted: the tests are
# simply not there. Fail if the end-to-end tests (test/e2e) have fewer than
# E2E_MIN_TESTS test functions (36 at the time of writing) or if any of their files is empty, so a truncated file is noticed.
set -o errexit -o nounset -o pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

min="${E2E_MIN_TESTS:-30}"
count=0
for f in test/e2e/*_test.go; do
  [[ -s "${f}" ]] || { echo "${f} is empty" >&2; exit 1; }
  n="$(grep -c '^func Test' "${f}" || true)"
  count=$((count + n))
done
(( count >= min )) || { echo "test/e2e has ${count} test functions, want at least ${min} (files emptied or deleted?)" >&2; exit 1; }
echo "ok: test/e2e has ${count} test functions"
