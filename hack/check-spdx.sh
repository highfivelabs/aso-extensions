#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: MIT
# Every Go file carries a licence header: ours (an SPDX line) or, for a
# file copied from Azure Service Operator, Microsoft's MIT header plus a
# "Modified by High Five Labs" note. Generated deepcopy files carry ours.
set -o errexit -o nounset -o pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

bad=0
while IFS= read -r f; do
  head="$(head -12 "${f}")"
  if grep -q "SPDX-License-Identifier: MIT" <<<"${head}"; then
    continue
  fi
  if grep -q "Copyright (c) Microsoft Corporation." <<<"${head}" && grep -q "Licensed under the MIT" <<<"${head}"; then
    if grep -q "Modified by High Five Labs" "${f}"; then
      continue
    fi
    echo "${f}: copied from ASO without a 'Modified by High Five Labs' note" >&2
    bad=1
    continue
  fi
  echo "${f}: no licence header" >&2
  bad=1
done < <(git ls-files --cached --others --exclude-standard -- '*.go')
[[ "${bad}" == 0 ]] && echo "ok: every Go file has a licence header"
exit "${bad}"
