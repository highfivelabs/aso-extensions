#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: Apache-2.0
# The production manager must not contain the fake cloud: no --fake-cloud
# flag and no pkg/fakecloud. Only a build with -tags fakecloud (the test
# image, tests/operator/run.sh) has them.
set -o errexit -o nounset -o pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

module="$(go list -m)"
# The lists are read whole before grep: `go list | grep -q` fails under
# pipefail when grep stops at the first match and go list, still writing,
# gets SIGPIPE (seen 2026-11-23, one run in three).
deps="$(go list -deps ./cmd/manager)"
test_deps="$(go list -tags fakecloud -deps ./cmd/manager)"
if grep -q "^${module}/pkg/fakecloud" <<<"${deps}"; then
  echo "cmd/manager links pkg/fakecloud without -tags fakecloud" >&2
  exit 1
fi
if ! grep -q "^${module}/pkg/fakecloud/entra$" <<<"${test_deps}"; then
  echo "cmd/manager with -tags fakecloud no longer links pkg/fakecloud/entra; update this check" >&2
  exit 1
fi
bin="$(mktemp -d)/manager"
go build -o "${bin}" ./cmd/manager
help="$("${bin}" --help 2>&1 || true)"
if grep -q -- '-fake-cloud' <<<"${help}"; then
  echo "the production manager has a --fake-cloud flag" >&2
  exit 1
fi
echo "ok: the production manager has no fake-cloud switch"
