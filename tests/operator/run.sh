#!/usr/bin/env bash
# Copyright 2026 High Five Labs
# SPDX-License-Identifier: MIT
#
# The install on kind with the fake cloud: our CRDs install, our manager
# runs in Azure Service Operator's namespace under its own narrow service
# account, reads ASO's settings and each namespace's aso-credential, and
# acts there as the identity ASO would: with ASO's strict workload identity
# it requests a token for <namespace>/aso-workload, which the fake Entra
# (in the manager's process) exchanges, and the fake vault, the fake
# Resource Manager and the fake PostgreSQL accept only the identity they
# were told to trust.
#
# ASO itself is not installed: the footprint ours reads is made by hand
# (its namespace, aso-controller-settings, the owner CRDs, and FlexibleServer
# and Vault objects with the status ASO would write). Real Azure, real
# Entra and real workload identity federation are not involved.
#
#   tests/operator/run.sh          create the cluster, run, leave it up
#   KEEP=0 tests/operator/run.sh   delete the cluster at the end

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${REPO_ROOT}"

NAME="${NAME:-aso-extensions}"
CLUSTER="${CLUSTER:-${NAME}-operator}"
KEEP="${KEEP:-1}"
ASO_NS=azureserviceoperator-system
SA="${NAME}-manager"
TENANT=00000000-0000-0000-0000-00000000000a
CLIENT_A=00000000-0000-0000-0000-0000000000c1
CLIENT_B=00000000-0000-0000-0000-0000000000c2
SUB=00000000-0000-0000-0000-0000000000f1
OUT="${REPO_ROOT}/bin/operator-e2e"
export KUBECONFIG="${OUT}/kubeconfig"
mkdir -p "${OUT}"

# ko pulls a public base image; an empty Docker config keeps it from asking
# a desktop credential helper (which can block without a session).
KO_DOCKER_CONFIG="${OUT}/ko-docker-config"
mkdir -p "${KO_DOCKER_CONFIG}"
ko() { DOCKER_CONFIG="${KO_DOCKER_CONFIG}" command ko "$@"; }

# ko_apply <file> [env...]: build the ko:// images in <file> into the local
# Docker daemon for the node's platform, load them into kind, and apply.
# (ko's own kind.local loader cannot tag images on kind 0.33's containerd.)
NODE_ARCH="$(docker exec "${CLUSTER}-control-plane" uname -m 2>/dev/null || uname -m)"
case "${NODE_ARCH}" in aarch64|arm64) NODE_ARCH=arm64 ;; x86_64|amd64) NODE_ARCH=amd64 ;; esac
ko_apply() {
  local file="$1" resolved
  resolved="$(KO_DOCKER_REPO=ko.local ko resolve --platform="linux/${NODE_ARCH}" -f "${file}")"
  for image in $(grep -Eo 'image: ko\.local[^[:space:]]+' <<<"${resolved}" | awk '{print $2}' | sort -u); do
    kind load docker-image "${image}" --name "${CLUSTER}" >/dev/null
  done
  kubectl apply -f - <<<"${resolved}"
}

step() { printf '\n== %s\n' "$*"; }
fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok: $*"; }

# wait_for <seconds> <description> <command...>: retry until the command succeeds.
wait_for() {
  local secs="$1" what="$2"; shift 2
  for _ in $(seq 1 "${secs}"); do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  "$@" || true
  fail "${what} (after ${secs}s)"
}

ready() { # ready <kind> <ns> <name>
  [[ "$(kubectl get "$1" -n "$2" "$3" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')" == "True" ]]
}
message() { # message <kind> <ns> <name>
  kubectl get "$1" -n "$2" "$3" -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}'
}

# logged <deployment> <text>: the fake cloud's log of <deployment> has <text>.
# (Not kubectl logs piped into grep -q: grep stops at the first match, kubectl
# is killed, and pipefail fails the check whenever the log is long.)
logged() {
  local out
  out="$(kubectl -n fake-cloud logs "deploy/$1")"
  grep -qF -- "$2" <<<"${out}"
}

cleanup() {
  if [[ "${KEEP}" == "0" ]]; then
    kind delete cluster --name "${CLUSTER}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

step "kind cluster ${CLUSTER}"
if ! kind get clusters | grep -qx "${CLUSTER}"; then
  kind create cluster --name "${CLUSTER}" --kubeconfig "${KUBECONFIG}" --wait 120s
else
  kind get kubeconfig --name "${CLUSTER}" > "${KUBECONFIG}"
fi
docker image inspect postgres:17 >/dev/null 2>&1 || docker pull -q postgres:17 >/dev/null
kind load docker-image postgres:17 --name "${CLUSTER}" >/dev/null

step "ASO's footprint, made by hand"
kubectl create namespace "${ASO_NS}" --dry-run=client -o yaml | kubectl apply -f -
# Server-side: ASO's Site CRD is larger than the annotation client-side apply keeps.
kubectl apply --server-side -k config/test/aso-crds
# ASO's settings: no global credential; strict workload identity for the
# namespaces' aso-credential secrets; Resource Manager is the fake one.
kubectl -n "${ASO_NS}" create secret generic aso-controller-settings \
  --from-literal=AZURE_WORKLOAD_IDENTITY_AUTH_MODE=strict \
  --from-literal=AZURE_RESOURCE_MANAGER_ENDPOINT=https://fakearm.fake-cloud.svc \
  --from-literal=AZURE_RESOURCE_MANAGER_AUDIENCE=https://fakearm.fake-cloud.svc \
  --dry-run=client -o yaml | kubectl apply -f -

step "the fake cloud"
rm -rf "${OUT}/fixtures"
go run ./tests/operator/fixtures --out "${OUT}/fixtures" --tenant "${TENANT}" --client "${CLIENT_A}" --namespace team-a
kubectl create namespace fake-cloud --dry-run=client -o yaml | kubectl apply -f -
kubectl -n fake-cloud create secret tls postgres-tls --cert "${OUT}/fixtures/postgres.crt" --key "${OUT}/fixtures/postgres.key" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n fake-cloud create secret tls fakevault-tls --cert "${OUT}/fixtures/fakevault.crt" --key "${OUT}/fixtures/fakevault.key" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n fake-cloud create secret tls fakearm-tls --cert "${OUT}/fixtures/fakearm.crt" --key "${OUT}/fixtures/fakearm.key" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n fake-cloud create configmap fakearm-resources --from-literal=resources="/subscriptions/${SUB}/resourceGroups/team-a-rg,/subscriptions/${SUB}/resourceGroups/team-b-rg,/subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.App/managedEnvironments/team-a-env,/subscriptions/${SUB}/resourceGroups/team-b-rg/providers/Microsoft.App/managedEnvironments/team-b-env,/subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Web/sites/team-a-site,/subscriptions/${SUB}/resourceGroups/team-b-rg/providers/Microsoft.Web/sites/team-b-site,/subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.OperationalInsights/workspaces/team-a-law,/subscriptions/${SUB}/resourceGroups/team-b-rg/providers/Microsoft.OperationalInsights/workspaces/team-b-law" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n fake-cloud create configmap postgres-init --from-file=init.sql="${OUT}/fixtures/init.sql" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n fake-cloud create configmap fakevault-trust --from-literal=trust="${CLIENT_A}=system:serviceaccount:team-a:aso-workload" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "${ASO_NS}" create configmap fake-cloud-ca --from-file=ca.crt="${OUT}/fixtures/ca.crt" --dry-run=client -o yaml | kubectl apply -f -
ko_apply config/fake-cloud/fake-cloud.yaml
# On a reused cluster the fixtures (CA, certificates) are new: restart so
# every pod reads them. PostgreSQL keeps no volume, so it starts empty.
kubectl -n fake-cloud rollout restart deploy/postgres deploy/fakevault deploy/fakearm >/dev/null
kubectl -n fake-cloud rollout status deploy/postgres --timeout=180s
kubectl -n fake-cloud rollout status deploy/fakevault --timeout=120s
kubectl -n fake-cloud rollout status deploy/fakearm --timeout=120s

step "the install, with the manager built -tags fakecloud"
kubectl kustomize tests/operator/overlay > "${OUT}/install.yaml"
GOFLAGS=-tags=fakecloud ko_apply "${OUT}/install.yaml"
for crd in users.dbforpostgresql.azure.hfl.dev vaultsecrets.keyvault.azure.hfl.dev vaultcertificates.keyvault.azure.hfl.dev managementlocks.authorization.azure.hfl.dev managedenvironmentsdaprcomponents.app.azure.hfl.dev sitesslots.web.azure.hfl.dev workspacestables.operationalinsights.azure.hfl.dev networksecurityperimeters.network.azure.hfl.dev; do
  kubectl wait --for=condition=Established "crd/${crd}" --timeout=60s >/dev/null
done
pass "CRDs installed and established"
kubectl -n "${ASO_NS}" rollout restart "deploy/${SA}" >/dev/null
kubectl -n "${ASO_NS}" rollout status "deploy/${SA}" --timeout=180s

step "the manager runs under its own service account in ASO's namespace"
got="$(kubectl -n "${ASO_NS}" get deploy "${SA}" -o jsonpath='{.spec.template.spec.serviceAccountName}')"
pod_sa="$(kubectl -n "${ASO_NS}" get pods -l app.kubernetes.io/component=manager --field-selector=status.phase=Running -o jsonpath='{.items[*].spec.serviceAccountName}')"
[[ -n "${pod_sa}" && "$(tr ' ' '\n' <<<"${pod_sa}" | sort -u)" == "${SA}" ]] || fail "running manager pods run as '${pod_sa}', want ${SA}"
[[ "${got}" == "${SA}" ]] || fail "manager pod runs as ${got}, want ${SA}"
pass "manager pod runs as ${ASO_NS}/${SA}"

step "what that service account may do"
can() { kubectl auth can-i "$@" --as="system:serviceaccount:${ASO_NS}:${SA}" >/dev/null 2>&1; }
can create serviceaccounts/aso-workload --subresource=token -n team-a || fail "cannot request aso-workload tokens"
! can create serviceaccounts/default --subresource=token -n team-a || fail "may request tokens for other service accounts"
can get secrets -n team-a || fail "cannot read secrets"
! can create secrets -n team-a || fail "may create secrets"
! can update flexibleservers.dbforpostgresql.azure.com -n team-a || fail "may change ASO's FlexibleServers"
! can create resourcegroups.resources.azure.com -n team-a || fail "may create ASO resources"
! can create mutatingwebhookconfigurations.admissionregistration.k8s.io || fail "may change webhooks"
can get resourcegroups.resources.azure.com -n team-a || fail "cannot read a ManagementLock's owner"
can list managedenvironments.app.azure.com -n team-a || fail "cannot read the owner of the generated app kinds"
can list sites.web.azure.com -n team-a || fail "cannot read the owner of the generated web kinds"
can list workspaces.operationalinsights.azure.com -n team-a || fail "cannot read the owner of the generated Log Analytics kinds"
! can list storageaccounts.storage.azure.com -n team-a || fail "may list ASO resources it only needs to get"
! can update resourcegroups.resources.azure.com -n team-a || fail "may change ASO's ResourceGroups"
pass "aso-workload tokens, read secrets and the ASO owners, get any ASO resource, our kinds; nothing of ASO's to change"

# team <ns> <client>: a namespace with ASO's strict workload identity credential.
team() {
  kubectl create namespace "$1" --dry-run=client -o yaml | kubectl apply -f -
  kubectl -n "$1" create serviceaccount aso-workload --dry-run=client -o yaml | kubectl apply -f -
  kubectl -n "$1" create secret generic aso-credential \
    --from-literal=AZURE_SUBSCRIPTION_ID="${SUB}" --from-literal=AZURE_TENANT_ID="${TENANT}" \
    --from-literal=AZURE_CLIENT_ID="$2" --dry-run=client -o yaml | kubectl apply -f -
  kubectl -n "$1" create secret generic app-config --from-literal=url=postgres://app --dry-run=client -o yaml | kubectl apply -f -
  kubectl -n "$1" create secret tls app-tls --cert "${OUT}/fixtures/app-tls.crt" --key "${OUT}/fixtures/app-tls.key" --dry-run=client -o yaml | kubectl apply -f -
  # The ASO objects, with the status ASO would write when it provisioned them
  # (and, on the ResourceGroup, the ARM ID annotation ASO writes).
  kubectl apply -f - <<EOF
apiVersion: resources.azure.com/v1api20200601storage
kind: ResourceGroup
metadata:
  name: rg
  namespace: $1
  annotations: {serviceoperator.azure.com/resource-id: /subscriptions/${SUB}/resourceGroups/$1-rg}
spec: {azureName: $1-rg, location: westus}
---
apiVersion: dbforpostgresql.azure.com/v20250801storage
kind: FlexibleServer
metadata: {name: pg, namespace: $1}
spec: {azureName: pg, owner: {name: rg}}
---
apiVersion: keyvault.azure.com/v1api20230701storage
kind: Vault
metadata: {name: vault, namespace: $1}
spec: {azureName: vault, owner: {name: rg}}
---
apiVersion: app.azure.com/v20250101storage
kind: ManagedEnvironment
metadata:
  name: env
  namespace: $1
  annotations: {serviceoperator.azure.com/resource-id: /subscriptions/${SUB}/resourceGroups/$1-rg/providers/Microsoft.App/managedEnvironments/$1-env}
spec: {azureName: $1-env, owner: {name: rg}}
---
apiVersion: web.azure.com/v20260715storage
kind: Site
metadata:
  name: site
  namespace: $1
  annotations: {serviceoperator.azure.com/resource-id: /subscriptions/${SUB}/resourceGroups/$1-rg/providers/Microsoft.Web/sites/$1-site}
spec: {azureName: $1-site, owner: {name: rg}}
---
apiVersion: operationalinsights.azure.com/v20250701storage
kind: Workspace
metadata:
  name: law
  namespace: $1
  annotations: {serviceoperator.azure.com/resource-id: /subscriptions/${SUB}/resourceGroups/$1-rg/providers/Microsoft.OperationalInsights/workspaces/$1-law}
spec: {azureName: $1-law, owner: {name: rg}}
EOF
  local now; now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  local cond="{\"type\":\"Ready\",\"status\":\"True\",\"severity\":\"\",\"reason\":\"Succeeded\",\"lastTransitionTime\":\"${now}\",\"observedGeneration\":1}"
  kubectl -n "$1" patch flexibleserver pg --subresource=status --type=merge \
    -p "{\"status\":{\"fullyQualifiedDomainName\":\"postgres.fake-cloud.svc\",\"conditions\":[${cond}]}}" >/dev/null
  kubectl -n "$1" patch vault vault --subresource=status --type=merge \
    -p "{\"status\":{\"properties\":{\"vaultUri\":\"https://fakevault.fake-cloud.svc/\"},\"conditions\":[${cond}]}}" >/dev/null
  kubectl -n "$1" patch resourcegroup rg --subresource=status --type=merge \
    -p "{\"status\":{\"conditions\":[${cond}]}}" >/dev/null
  kubectl -n "$1" patch managedenvironment env --subresource=status --type=merge \
    -p "{\"status\":{\"conditions\":[${cond}]}}" >/dev/null
  kubectl -n "$1" patch sites.web.azure.com site --subresource=status --type=merge \
    -p "{\"status\":{\"conditions\":[${cond}]}}" >/dev/null
  kubectl -n "$1" patch workspaces.operationalinsights.azure.com law --subresource=status --type=merge \
    -p "{\"status\":{\"conditions\":[${cond}]}}" >/dev/null
  kubectl apply -f - <<EOF
apiVersion: dbforpostgresql.azure.hfl.dev/v1
kind: User
metadata: {name: app-identity, namespace: $1}
spec:
  owner: {name: pg}
  aadUser: {serverAdminUsername: aso-team-a}
  roles: [app_readers]
  databasePrivileges: {app: [CONNECT, CREATE]}
  schemaPrivileges: {app: {public: [USAGE, CREATE]}}
  ownedDatabases: [orders]
---
apiVersion: keyvault.azure.hfl.dev/v1
kind: VaultSecret
metadata: {name: app-url, namespace: $1}
spec:
  owner: {name: vault}
  value: {name: app-config, key: url}
  deleteMode: delete
---
apiVersion: keyvault.azure.hfl.dev/v1
kind: VaultCertificate
metadata: {name: app-tls, namespace: $1}
spec:
  owner: {name: vault}
  certificate: {name: app-tls, key: tls.crt}
  privateKey: {name: app-tls, key: tls.key}
  deleteMode: delete
---
apiVersion: app.azure.hfl.dev/v20250101
kind: ManagedEnvironmentsDaprComponent
metadata: {name: statestore, namespace: $1}
spec:
  owner: {name: env}
  componentType: state.redis
  version: v1
  metadata:
  - {name: redisHost, value: "redis.example:6379"}
---
apiVersion: web.azure.hfl.dev/v20260715
kind: SitesSlot
metadata: {name: staging, namespace: $1}
spec:
  owner: {name: site}
  location: westus
  httpsOnly: true
---
apiVersion: operationalinsights.azure.hfl.dev/v20250701
kind: WorkspacesTable
metadata: {name: appevents, namespace: $1}
spec:
  azureName: AppEvents_CL
  owner: {name: law}
  plan: Analytics
  retentionInDays: 30
---
apiVersion: network.azure.hfl.dev/v20250501
kind: NetworkSecurityPerimeter
metadata: {name: perimeter, namespace: $1}
spec:
  owner: {name: rg}
  location: westus
---
apiVersion: authorization.azure.hfl.dev/v1
kind: ManagementLock
metadata: {name: keep-rg, namespace: $1}
spec:
  owner: {group: resources.azure.com, kind: ResourceGroup, name: rg}
  level: CanNotDelete
  notes: kept by aso-extensions
EOF
}

step "team-a: the identity both fakes trust"
team team-a "${CLIENT_A}"
wait_for 90 "team-a's User Ready" ready users.dbforpostgresql.azure.hfl.dev team-a app-identity
pass "team-a User Ready"
wait_for 90 "team-a's VaultSecret Ready" ready vaultsecrets.keyvault.azure.hfl.dev team-a app-url
pass "team-a VaultSecret Ready"
psql() { kubectl -n fake-cloud exec deploy/postgres -- psql -U postgres -tAc "$1"; }
[[ "$(psql "SELECT shobj_description(oid, 'pg_authid') FROM pg_roles WHERE rolname = 'app-identity'")" == "aadauth,name=app-identity,type=service" ]] \
  || fail "the Entra role app-identity is not in PostgreSQL"
[[ "$(psql "SELECT string_agg(a.privilege_type, ',' ORDER BY a.privilege_type) FROM pg_database d CROSS JOIN LATERAL aclexplode(d.datacl) a JOIN pg_roles r ON r.oid = a.grantee WHERE r.rolname = 'app-identity' AND d.datname = 'app'")" == "CONNECT,CREATE" ]] \
  || fail "app-identity's privileges on app are wrong"
pass "PostgreSQL holds the Entra role app-identity with CONNECT, CREATE on app"
[[ "$(kubectl -n fake-cloud exec deploy/postgres -- psql -U postgres -d app -tAc "SELECT string_agg(a.privilege_type, ',' ORDER BY a.privilege_type) FROM pg_namespace n CROSS JOIN LATERAL aclexplode(n.nspacl) a JOIN pg_roles r ON r.oid = a.grantee WHERE r.rolname = 'app-identity' AND n.nspname = 'public'")" == "CREATE,USAGE" ]] \
  || fail "app-identity's privileges on schema public of app are wrong"
pass "app-identity holds CREATE, USAGE on schema public of app (granted on a connection to app)"
[[ "$(psql "SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = 'orders'")" == "app-identity" ]] \
  || fail "app-identity does not own the database orders"
pass "app-identity owns the database orders"
logged fakevault "sub=system:serviceaccount:team-a:aso-workload scope=https://fake-cloud.svc/.default accepted=true" \
  || fail "the fake vault saw no accepted request from team-a's aso-workload"
pass "the fake vault accepted team-a's aso-workload identity"
wait_for 90 "team-a's VaultCertificate Ready" ready vaultcertificates.keyvault.azure.hfl.dev team-a app-tls
logged fakevault "request POST /certificates/app-tls/import" \
  || fail "the fake vault saw no import of app-tls"
pass "team-a VaultCertificate Ready: app-tls imported into the vault"
wait_for 90 "team-a's DaprComponent Ready" ready managedenvironmentsdaprcomponents.app.azure.hfl.dev team-a statestore
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.App/managedEnvironments/team-a-env/daprComponents/statestore 201" \
  || fail "the fake resource manager saw no Dapr component created in team-a's environment"
pass "team-a DaprComponent Ready: generated by ASO's generator, PUT by ASO's ARM reconciler as team-a's aso-workload identity"
wait_for 90 "team-a's SitesSlot Ready" ready sitesslots.web.azure.hfl.dev team-a staging
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Web/sites/team-a-site/slots/staging 201" \
  || fail "the fake resource manager saw no slot created on team-a's app"
pass "team-a SitesSlot Ready: a deployment slot of ASO's Site, generated, PUT by ASO's ARM reconciler"
wait_for 90 "team-a's WorkspacesTable Ready" ready workspacestables.operationalinsights.azure.hfl.dev team-a appevents
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.OperationalInsights/workspaces/team-a-law/tables/AppEvents_CL 201" \
  || fail "the fake resource manager saw no table created in team-a's workspace"
pass "team-a WorkspacesTable Ready: a table of ASO's Log Analytics Workspace, generated, PUT by ASO's ARM reconciler"
wait_for 90 "team-a's NetworkSecurityPerimeter Ready" ready networksecurityperimeters.network.azure.hfl.dev team-a perimeter
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/networkSecurityPerimeters/perimeter 201" \
  || fail "the fake resource manager saw no network security perimeter created in team-a's resource group"
pass "team-a NetworkSecurityPerimeter Ready: generated, owned by ASO's ResourceGroup, PUT by ASO's ARM reconciler"
wait_for 90 "team-a's ManagementLock Ready" ready managementlocks.authorization.azure.hfl.dev team-a keep-rg
pass "team-a ManagementLock Ready"
logged fakearm "sub=system:serviceaccount:team-a:aso-workload scope=https://fakearm.fake-cloud.svc/.default accepted=true" \
  || fail "the fake resource manager saw no accepted request from team-a's aso-workload"
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Authorization/locks/keep-rg 201" \
  || fail "the fake resource manager saw no lock created on team-a-rg"
pass "the fake resource manager holds a lock on team-a-rg, set as team-a's aso-workload identity"

step "team-b: another identity, refused by both"
team team-b "${CLIENT_B}"
wait_for 90 "team-b's User refused" bash -c "[[ \"\$(kubectl get users.dbforpostgresql.azure.hfl.dev -n team-b app-identity -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].message}')\" == *'password authentication failed'* ]]"
pass "team-b User refused: $(message users.dbforpostgresql.azure.hfl.dev team-b app-identity | cut -c1-120)..."
wait_for 90 "team-b's VaultSecret refused" bash -c "[[ \"\$(kubectl get vaultsecrets.keyvault.azure.hfl.dev -n team-b app-url -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].message}')\" == *'401'* ]]"
pass "team-b VaultSecret refused: $(message vaultsecrets.keyvault.azure.hfl.dev team-b app-url | cut -c1-120)..."
wait_for 90 "team-b's VaultCertificate refused" bash -c "[[ \"\$(kubectl get vaultcertificates.keyvault.azure.hfl.dev -n team-b app-tls -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].message}')\" == *'401'* ]]"
pass "team-b VaultCertificate refused: $(message vaultcertificates.keyvault.azure.hfl.dev team-b app-tls | cut -c1-120)..."
wait_for 90 "team-b's DaprComponent refused" bash -c "[[ \"\$(kubectl get managedenvironmentsdaprcomponents.app.azure.hfl.dev -n team-b statestore -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].message}')\" == *'this identity may not use Resource Manager'* ]]"
pass "team-b DaprComponent refused: $(message managedenvironmentsdaprcomponents.app.azure.hfl.dev team-b statestore | cut -c1-120)..."
wait_for 90 "team-b's SitesSlot refused" bash -c "[[ \"\$(kubectl get sitesslots.web.azure.hfl.dev -n team-b staging -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].message}')\" == *'this identity may not use Resource Manager'* ]]"
pass "team-b SitesSlot refused: $(message sitesslots.web.azure.hfl.dev team-b staging | cut -c1-120)..."
wait_for 90 "team-b's WorkspacesTable refused" bash -c "[[ \"\$(kubectl get workspacestables.operationalinsights.azure.hfl.dev -n team-b appevents -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].message}')\" == *'this identity may not use Resource Manager'* ]]"
pass "team-b WorkspacesTable refused: $(message workspacestables.operationalinsights.azure.hfl.dev team-b appevents | cut -c1-120)..."
wait_for 90 "team-b's NetworkSecurityPerimeter refused" bash -c "[[ \"\$(kubectl get networksecurityperimeters.network.azure.hfl.dev -n team-b perimeter -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].message}')\" == *'this identity may not use Resource Manager'* ]]"
pass "team-b NetworkSecurityPerimeter refused: $(message networksecurityperimeters.network.azure.hfl.dev team-b perimeter | cut -c1-120)..."
wait_for 90 "team-b's ManagementLock refused" bash -c "[[ \"\$(kubectl get managementlocks.authorization.azure.hfl.dev -n team-b keep-rg -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].message}')\" == *'401'* ]]"
pass "team-b ManagementLock refused: $(message managementlocks.authorization.azure.hfl.dev team-b keep-rg | cut -c1-120)..."

step "deleting team-a's objects"
kubectl -n team-a delete users.dbforpostgresql.azure.hfl.dev app-identity --timeout=60s
kubectl -n team-a delete vaultsecrets.keyvault.azure.hfl.dev app-url --timeout=60s
[[ -z "$(psql "SELECT 1 FROM pg_roles WHERE rolname = 'app-identity'")" ]] || fail "the role app-identity was not dropped"
pass "the Entra role app-identity is dropped"
[[ "$(psql "SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = 'orders'")" == "hfl_owner" ]] \
  || fail "the database orders was not given to hfl_owner"
[[ "$(psql "SELECT rolcanlogin FROM pg_roles WHERE rolname = 'hfl_owner'")" == "f" ]] || fail "hfl_owner can sign in"
pass "the database orders went to hfl_owner, which cannot sign in"
kubectl -n team-a get events --field-selector reason=DatabaseReassigned -o jsonpath='{.items[*].message}' | grep -q "gave database orders" \
  || fail "no DatabaseReassigned event names orders"
pass "a DatabaseReassigned event names orders"
logged fakevault "request DELETE /secrets/app-url" || fail "the vault saw no delete of app-url"
pass "the vault soft-deleted app-url (deleteMode delete)"
kubectl -n team-a delete vaultcertificates.keyvault.azure.hfl.dev app-tls --timeout=60s
logged fakevault "request DELETE /certificates/app-tls" || fail "the vault saw no delete of app-tls"
pass "the vault soft-deleted the certificate app-tls (deleteMode delete)"
# The lock on team-a-rg (CanNotDelete) blocks the delete of anything under it:
# ASO's reconciler reports ScopeLocked and retries until the lock is gone.
kubectl -n team-a delete managedenvironmentsdaprcomponents.app.azure.hfl.dev statestore --wait=false
wait_for 90 "the Dapr component's delete blocked by the lock" bash -c "[[ \"\$(kubectl get managedenvironmentsdaprcomponents.app.azure.hfl.dev -n team-a statestore -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].reason}')\" == ScopeLocked ]]"
pass "the lock on team-a-rg blocks deleting the Dapr component: $(message managedenvironmentsdaprcomponents.app.azure.hfl.dev team-a statestore | cut -c1-90)..."
kubectl -n team-a delete managementlocks.authorization.azure.hfl.dev keep-rg --timeout=60s
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Authorization/locks/keep-rg 200" \
  || fail "the fake resource manager saw no delete of the lock"
pass "the lock on team-a-rg is removed"
kubectl -n team-a wait --for=delete managedenvironmentsdaprcomponents.app.azure.hfl.dev/statestore --timeout=300s
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.App/managedEnvironments/team-a-env/daprComponents/statestore 200" \
  || fail "the fake resource manager saw no delete of the Dapr component"
pass "with the lock gone, the Dapr component is deleted in Resource Manager"
kubectl -n team-a delete sitesslots.web.azure.hfl.dev staging --timeout=120s
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Web/sites/team-a-site/slots/staging 200" \
  || fail "the fake resource manager saw no delete of the slot"
pass "the slot is deleted in Resource Manager"
kubectl -n team-a delete workspacestables.operationalinsights.azure.hfl.dev appevents --timeout=120s
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.OperationalInsights/workspaces/team-a-law/tables/AppEvents_CL 200" \
  || fail "the fake resource manager saw no delete of the table"
pass "the table is deleted in Resource Manager"
kubectl -n team-a delete networksecurityperimeters.network.azure.hfl.dev perimeter --timeout=120s
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/networkSecurityPerimeters/perimeter 200" \
  || fail "the fake resource manager saw no delete of the perimeter"
pass "the perimeter is deleted in Resource Manager"

echo
echo "all checks passed"
