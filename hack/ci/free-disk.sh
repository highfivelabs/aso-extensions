#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: Apache-2.0
#
# GitHub's runner only (ci.yml's check job): the pinned upstream checkout,
# its Go module graph and build cache need more room than the runner
# leaves free, so delete toolchains this repository never uses. Never run
# it on a laptop or a VM.
set -euo pipefail

[[ "${GITHUB_ACTIONS:-}" == true ]] || { echo "free-disk.sh: GitHub's runner only" >&2; exit 2; }
sudo rm -rf /usr/share/dotnet /usr/local/lib/android /opt/ghc /opt/hostedtoolcache/CodeQL
df -h /
