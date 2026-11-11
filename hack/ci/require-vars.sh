#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: MIT
#
#   hack/ci/require-vars.sh NAME...   fail, naming each, unless every
#                                     NAME is set and not empty
#
# The workflows read the environment's identifiers from repository
# variables (.env.example lists them); a job that needs one stops here
# rather than later with a less clear error.
set -euo pipefail

missing=0
for v in "$@"; do
  if [[ -z "${!v:-}" ]]; then
    if [[ "${GITHUB_ACTIONS:-}" == true ]]; then
      echo "::error::${v} is not set: set the repository variable ${v}"
    else
      echo "${v} is not set (see .env.example)" >&2
    fi
    missing=1
  fi
done
exit "$missing"
