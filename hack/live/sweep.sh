#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: Apache-2.0
#
# List or delete what is in the live-test resource group (never the group
# itself), with the run's federated credentials (credentials.sh; run.sh
# calls this).
#
#   sweep.sh --list               resources (type, name, created), the
#                                 group's tags, budgets at the group
#   sweep.sh --since ISO-TIME     delete resources created at or after it
#   sweep.sh --before ISO-TIME    delete resources created before it
#
# Top-level resources only (deleting one deletes its children). A delete
# that fails (a dependency not yet gone) is tried again, up to 30 rounds a minute apart (gateways take a quarter of an hour to go).
set -o errexit -o nounset -o pipefail
: "${AZURE_SUBSCRIPTION_ID:?}" "${LIVE_RESOURCE_GROUP:?}"
# shellcheck source=hack/live/credentials.sh
source "$(dirname "${BASH_SOURCE[0]}")/credentials.sh"

ARM=https://management.azure.com
RG="/subscriptions/${AZURE_SUBSCRIPTION_ID}/resourceGroups/${LIVE_RESOURCE_GROUP}"
TOKEN="$(arm_token)"
api() { curl -sS -H "Authorization: Bearer ${TOKEN}" "$@"; }

resources() { # id type name createdTime, one per line
  api "${ARM}${RG}/resources?api-version=2021-04-01&\$expand=createdTime" \
    | jq -r '.value[] | "\(.id) \(.type) \(.name) \(.createdTime)"'
}

api_version() { # the newest non-preview API version of a type
  local ns="${1%%/*}" type="${1#*/}"
  api "${ARM}/subscriptions/${AZURE_SUBSCRIPTION_ID}/providers/${ns}?api-version=2021-04-01" \
    | jq -r --arg t "${type}" '.resourceTypes[] | select((.resourceType|ascii_downcase) == ($t|ascii_downcase)) | .apiVersions | map(select(test("preview")|not)) | .[0] // empty'
}

case "${1:-}" in
  --list)
    echo "resources in ${LIVE_RESOURCE_GROUP}:"
    resources | awk '{print "  " $2 " " $3 " (created " $4 ")"}'
    echo "tags on the group: $(api "${ARM}${RG}?api-version=2021-04-01" | jq -c '.tags')"
    echo "budgets at the group: $(api "${ARM}${RG}/providers/Microsoft.Consumption/budgets?api-version=2023-05-01" | jq -c '[.value[]?.name]')"
    # What the subscription-scope tests (test/live/subscription_test.go) make
    # carries the run ID in its name or display name; each test deletes it.
    # Listed only (the sweep never deletes at the subscription); empty when
    # the identity cannot read there (hack/live/roles/ not assigned).
    SUB="/subscriptions/${AZURE_SUBSCRIPTION_ID}"
    live='[.value[]? | select((.name|test("-ax[0-9]{6}[0-9a-f]{4}$")) or ((.properties.displayName // "")|startswith("aso-extensions live"))) | .name]'
    for t in "Microsoft.Authorization/policyDefinitions 2023-04-01" "Microsoft.Authorization/policySetDefinitions 2023-04-01" \
      "Microsoft.Authorization/accessReviewScheduleDefinitions 2021-12-01-preview" "Microsoft.Security/alertsSuppressionRules 2019-01-01-preview" \
      "Microsoft.Security/governanceRules 2022-01-01-preview"; do
      read -r type v <<<"${t}"
      echo "live-run ${type} at the subscription: $(api "${ARM}${SUB}/providers/${type}?api-version=${v}" | jq -c "${live}" 2>/dev/null || echo '[]')"
    done
    echo "live-run policy assignments in the group: $(api "${ARM}${RG}/providers/Microsoft.Authorization/policyAssignments?api-version=2023-04-01&\$filter=atExactScope()" | jq -c "${live}" 2>/dev/null || echo '[]')"
    ;;
  --since|--before)
    mode="$1" at="${2:?time}"
    for round in $(seq 30); do
      mapfile -t doomed < <(resources | awk -v m="${mode}" -v at="${at}" '(m == "--since" && $4 >= at) || (m == "--before" && $4 < at)')
      [[ ${#doomed[@]} -eq 0 ]] && exit 0
      echo "sweep round ${round}: ${#doomed[@]} resources"
      for line in "${doomed[@]}"; do
        read -r id type name _ <<<"${line}"
        v="$(api_version "${type}")"
        code="$(api -o /dev/null -w '%{http_code}' -X DELETE "${ARM}${id}?api-version=${v}")"
        echo "  delete ${type} ${name} (api ${v}): ${code}"
      done
      sleep 60
    done
    echo "sweep: resources left after 30 rounds" >&2
    resources >&2
    exit 1
    ;;
  *) echo "usage: sweep.sh --list | --since ISO | --before ISO" >&2; exit 2 ;;
esac
