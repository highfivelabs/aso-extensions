#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: MIT
#
# Print the registration state of every resource provider in the live-test
# subscription (hack/live/azure.env), with the run's federated credentials,
# from an offload VM run with --sa (docs/live-runs.md). Read only: the
# identity cannot register a provider, and agents never do; the builder does.
#
#   task live-providers
#
# Output: "<state> <namespace>", Registered first, then the rest.
set -o errexit -o nounset -o pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"
set -o allexport
# shellcheck source=hack/live/azure.env
source hack/live/azure.env
set +o allexport
# shellcheck source=hack/live/credentials.sh
source hack/live/credentials.sh
live_creds_start "$(mktemp -d)"
trap live_creds_stop EXIT

token="$(arm_token)"
body="$(curl -fsS -H "Authorization: Bearer ${token}" \
  "https://management.azure.com/subscriptions/${AZURE_SUBSCRIPTION_ID}/providers?api-version=2021-04-01")"
jq -r '.value[] | "\(.registrationState) \(.namespace)"' <<<"${body}" \
  | sort -k1,1r -k2,2
