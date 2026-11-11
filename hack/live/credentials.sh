# Copyright 2026 High Five Labs
# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Federated Azure credentials for a live run, sourced by hack/live/run.sh on
# an offload VM started with --sa: Google's metadata server gives the VM's
# service account an ID token with the audience api://AzureADTokenExchange;
# it is written to a file that ASO's workload identity credential (and the
# tests' own) read through AZURE_FEDERATED_TOKEN_FILE and exchange at Entra
# for the app registration AZURE_CLIENT_ID. No secrets. Google's tokens last
# an hour; a background loop writes a fresh one every 20 minutes, and
# live_creds_stop ends it.
LIVE_AUDIENCE="api://AzureADTokenExchange"
_live_token_pid=""

live_token() {
  curl -fsS -H 'Metadata-Flavor: Google' \
    "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/identity?audience=${LIVE_AUDIENCE}&format=full"
}

live_creds_start() { # live_creds_start <directory>
  local dir="$1"
  # No other credential source may be in the environment.
  unset AZURE_CLIENT_SECRET AZURE_CLIENT_CERTIFICATE AZURE_CLIENT_CERTIFICATE_PATH \
    AZURE_CLIENT_CERTIFICATE_PASSWORD AZURE_USERNAME AZURE_PASSWORD
  export AZURE_CONFIG_DIR="${dir}/az-none"
  local tok="${dir}/google-id-token"
  live_token >"${tok}.new" || { echo "live: no ID token from the metadata server: is this an offload VM run with --sa?" >&2; return 1; }
  mv "${tok}.new" "${tok}"
  chmod 600 "${tok}"
  ( while sleep 1200; do live_token >"${tok}.new" && mv "${tok}.new" "${tok}"; done ) &
  _live_token_pid=$!
  export AZURE_FEDERATED_TOKEN_FILE="${tok}" USE_WORKLOAD_IDENTITY_AUTH=true
}

live_creds_stop() {
  [[ -n "${_live_token_pid}" ]] && kill "${_live_token_pid}" 2>/dev/null || true
}

# arm_token prints a Resource Manager access token for the identity, from the
# Google token (for the sweeper; the tests use azidentity).
arm_token() {
  curl -fsS -X POST "https://login.microsoftonline.com/${AZURE_TENANT_ID}/oauth2/v2.0/token" \
    --data-urlencode "client_id=${AZURE_CLIENT_ID}" \
    --data-urlencode 'grant_type=client_credentials' \
    --data-urlencode 'scope=https://management.azure.com/.default' \
    --data-urlencode 'client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer' \
    --data-urlencode "client_assertion=$(cat "${AZURE_FEDERATED_TOKEN_FILE}")" | jq -er .access_token
}
