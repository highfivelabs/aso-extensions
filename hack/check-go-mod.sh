#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: MIT
# We build against Azure Service Operator's framework, so every module both
# go.mod files require must be at the version ASO's go.mod pins: a newer
# one here would move ASO's code onto versions it is not tested with. Fail
# if any shared module differs.

set -o errexit
set -o nounset
set -o pipefail
# sort and join must agree on order; the C locale makes both byte order on any OS.
export LC_ALL=C

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

ASO_MOD="third_party/azure-service-operator/v2/go.mod"
reqs() { go mod edit -json "$1" | python3 -c 'import json,sys; [print(r["Path"], r["Version"]) for r in json.load(sys.stdin).get("Require") or []]' | sort; }
diffs="$(join <(reqs "${ASO_MOD}") <(reqs go.mod) | awk '$2 != $3 {print $1": ASO "$2", ours "$3}')"
if [[ -n "${diffs}" ]]; then
  echo "go.mod requires these modules at other versions than ${ASO_MOD}:" >&2
  echo "${diffs}" >&2
  exit 1
fi
echo "ok: every module shared with the pinned ASO is at ASO's version"
