#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: MIT
# The production manager must not contain the fake cloud: no --fake-cloud
# flag and no pkg/fakecloud. Only a build with -tags fakecloud (the test
# image, tests/operator/run.sh) has them.
set -o errexit -o nounset -o pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

module="$(go list -m)"
if go list -deps ./cmd/manager | grep -q "^${module}/pkg/fakecloud"; then
  echo "cmd/manager links pkg/fakecloud without -tags fakecloud" >&2
  exit 1
fi
if ! go list -tags fakecloud -deps ./cmd/manager | grep -q "^${module}/pkg/fakecloud/entra$"; then
  echo "cmd/manager with -tags fakecloud no longer links pkg/fakecloud/entra; update this check" >&2
  exit 1
fi
bin="$(mktemp -d)/manager"
go build -o "${bin}" ./cmd/manager
if "${bin}" --help 2>&1 | grep -q -- '-fake-cloud'; then
  echo "the production manager has a --fake-cloud flag" >&2
  exit 1
fi
echo "ok: the production manager has no fake-cloud switch"
