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
# A built-in policy definition (Allowed locations), at the tenant root.
BUILTIN_POLICY=/providers/Microsoft.Authorization/policyDefinitions/e56962a6-4747-49cd-b67b-bf8b01975c4c
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
kubectl -n fake-cloud create secret tls fakesql-tls --cert "${OUT}/fixtures/fakesql.crt" --key "${OUT}/fixtures/fakesql.key" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n fake-cloud create configmap fakesql-principals --from-literal=team-a="${CLIENT_A}=system:serviceaccount:team-a:aso-workload" \
  --from-literal=team-b="${CLIENT_B}=system:serviceaccount:team-b:aso-workload" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n fake-cloud create configmap fakearm-resources --from-literal=resources="/subscriptions/${SUB}/resourceGroups/team-a-rg,/subscriptions/${SUB}/resourceGroups/team-b-rg,/subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.App/managedEnvironments/team-a-env,/subscriptions/${SUB}/resourceGroups/team-b-rg/providers/Microsoft.App/managedEnvironments/team-b-env,/subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Web/sites/team-a-site,/subscriptions/${SUB}/resourceGroups/team-b-rg/providers/Microsoft.Web/sites/team-b-site,/subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.OperationalInsights/workspaces/team-a-law,/subscriptions/${SUB}/resourceGroups/team-b-rg/providers/Microsoft.OperationalInsights/workspaces/team-b-law,/subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.ContainerRegistry/registries/teamareg,/subscriptions/${SUB}/resourceGroups/team-b-rg/providers/Microsoft.ContainerRegistry/registries/teambreg,/subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Compute/virtualMachines/teamavm,/subscriptions/${SUB}/resourceGroups/team-b-rg/providers/Microsoft.Compute/virtualMachines/teambvm,/subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Storage/storageAccounts/teamacosts,/subscriptions/${SUB}/resourceGroups/team-b-rg/providers/Microsoft.Storage/storageAccounts/teambcosts,/subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/virtualNetworkGateways/team-a-vng,/subscriptions/${SUB}/resourceGroups/team-b-rg/providers/Microsoft.Network/virtualNetworkGateways/team-b-vng,/subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/virtualHubs/team-a-hub,/subscriptions/${SUB}/resourceGroups/team-b-rg/providers/Microsoft.Network/virtualHubs/team-b-hub,/subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/virtualNetworks/team-a-spoke,/subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/virtualNetworks/team-a-spoke/subnets/RouteServerSubnet,/subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/azureFirewalls/team-a-fw,/subscriptions/${SUB}/resourceGroups/team-b-rg/providers/Microsoft.Network/virtualNetworks/team-b-spoke,/subscriptions/${SUB}/resourceGroups/team-b-rg/providers/Microsoft.Network/virtualNetworks/team-b-spoke/subnets/RouteServerSubnet,/subscriptions/${SUB}/resourceGroups/team-b-rg/providers/Microsoft.Network/azureFirewalls/team-b-fw,/subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Kubernetes/connectedClusters/team-a-arc,${BUILTIN_POLICY}" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n fake-cloud create configmap postgres-init --from-file=init.sql="${OUT}/fixtures/init.sql" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n fake-cloud create configmap fakevault-trust --from-literal=trust="${CLIENT_A}=system:serviceaccount:team-a:aso-workload" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "${ASO_NS}" create configmap fake-cloud-ca --from-file=ca.crt="${OUT}/fixtures/ca.crt" --dry-run=client -o yaml | kubectl apply -f -
ko_apply config/fake-cloud/fake-cloud.yaml
# On a reused cluster the fixtures (CA, certificates) are new: restart so
# every pod reads them. PostgreSQL keeps no volume, so it starts empty.
kubectl -n fake-cloud rollout restart deploy/postgres deploy/fakevault deploy/fakearm deploy/fakesql >/dev/null
kubectl -n fake-cloud rollout status deploy/postgres --timeout=180s
kubectl -n fake-cloud rollout status deploy/fakevault --timeout=120s
kubectl -n fake-cloud rollout status deploy/fakearm --timeout=120s
kubectl -n fake-cloud rollout status deploy/fakesql --timeout=120s

step "the install, with the manager built -tags fakecloud"
kubectl kustomize tests/operator/overlay > "${OUT}/install.yaml"
GOFLAGS=-tags=fakecloud ko_apply "${OUT}/install.yaml"
for crd in users.dbforpostgresql.azure.hfl.dev users.sql.azure.hfl.dev vaultsecrets.keyvault.azure.hfl.dev vaultcertificates.keyvault.azure.hfl.dev managementlocks.authorization.azure.hfl.dev managedenvironmentsdaprcomponents.app.azure.hfl.dev sitesslots.web.azure.hfl.dev workspacestables.operationalinsights.azure.hfl.dev networksecurityperimeters.network.azure.hfl.dev registriesscopemaps.containerregistry.azure.hfl.dev virtualmachinesruncommands.compute.azure.hfl.dev vaults.recoveryservices.azure.hfl.dev managementgroups.management.azure.hfl.dev tagsets.resources.azure.hfl.dev; do
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
can list registries.containerregistry.azure.com -n team-a || fail "cannot read the owner of the generated container registry kinds"
can list virtualmachines.compute.azure.com -n team-a || fail "cannot read the owner of the generated compute kinds"
can list serversdatabases.sql.azure.com -n team-a || fail "cannot read the owner of the Azure SQL User"
! can update servers.sql.azure.com -n team-a || fail "may change ASO's SQL servers"
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
  kubectl -n "$1" create secret generic vpn-psk --from-literal=key="$1-shared-key" --dry-run=client -o yaml | kubectl apply -f -
  kubectl -n "$1" create secret generic er-bgp --from-literal=md5="$1-bgp-key" --dry-run=client -o yaml | kubectl apply -f -
  kubectl -n "$1" create secret generic vwan-vpn --from-literal=site="$1-site-key" --from-literal=link="$1-link-key" --from-literal=radius="$1-radius" --dry-run=client -o yaml | kubectl apply -f -
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
---
apiVersion: containerregistry.azure.com/v20251101storage
kind: Registry
metadata:
  name: reg
  namespace: $1
  annotations: {serviceoperator.azure.com/resource-id: /subscriptions/${SUB}/resourceGroups/$1-rg/providers/Microsoft.ContainerRegistry/registries/${1//-/}reg}
spec: {azureName: ${1//-/}reg, owner: {name: rg}}
---
apiVersion: compute.azure.com/v20220301storage
kind: VirtualMachine
metadata:
  name: vm
  namespace: $1
  annotations: {serviceoperator.azure.com/resource-id: /subscriptions/${SUB}/resourceGroups/$1-rg/providers/Microsoft.Compute/virtualMachines/${1//-/}vm}
spec: {azureName: ${1//-/}vm, owner: {name: rg}}
---
apiVersion: sql.azure.com/v20250101storage
kind: Server
metadata: {name: sql, namespace: $1}
spec: {azureName: $1-sql, owner: {name: rg}}
---
apiVersion: sql.azure.com/v20250101storage
kind: ServersDatabase
metadata: {name: appdb, namespace: $1}
spec: {azureName: appdb, owner: {name: sql}}
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
  kubectl -n "$1" patch registries.containerregistry.azure.com reg --subresource=status --type=merge \
    -p "{\"status\":{\"conditions\":[${cond}]}}" >/dev/null
  kubectl -n "$1" patch virtualmachines.compute.azure.com vm --subresource=status --type=merge \
    -p "{\"status\":{\"conditions\":[${cond}]}}" >/dev/null
  kubectl -n "$1" patch servers.sql.azure.com sql --subresource=status --type=merge \
    -p "{\"status\":{\"fullyQualifiedDomainName\":\"fakesql.fake-cloud.svc\",\"conditions\":[${cond}]}}" >/dev/null
  kubectl -n "$1" patch serversdatabases.sql.azure.com appdb --subresource=status --type=merge \
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
apiVersion: sql.azure.hfl.dev/v1
kind: User
metadata: {name: app-identity, namespace: $1}
spec:
  owner: {name: appdb}
  aadUser: {}
  roles: [db_datareader, db_datawriter]
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
apiVersion: recoveryservices.azure.hfl.dev/v20260701
kind: Vault
metadata: {name: backups, namespace: $1}
spec:
  azureName: $1-backups
  owner: {name: rg}
  location: westus
  sku: {name: RS0, tier: Standard}
---
apiVersion: compute.azure.hfl.dev/v20220301
kind: VirtualMachinesRunCommand
metadata: {name: hello, namespace: $1}
spec:
  azureName: hello
  owner: {name: vm}
  location: westus
  source: {script: "echo hello"}
---
apiVersion: maintenance.azure.hfl.dev/v20230401
kind: MaintenanceConfiguration
metadata: {name: patching, namespace: $1}
spec:
  owner: {name: rg}
  location: westus
  maintenanceScope: InGuestPatch
  extensionProperties: {InGuestPatchMode: User}
  installPatches: {rebootSetting: IfRequired}
  maintenanceWindow: {startDateTime: "2026-12-01 02:00", duration: "03:00", timeZone: UTC, recurEvery: Week Saturday}
---
apiVersion: maintenance.azure.hfl.dev/v20230401
kind: ConfigurationAssignment
metadata: {name: vm-patching, namespace: $1}
spec:
  azureName: patching
  owner: {group: compute.azure.com, kind: VirtualMachine, name: vm}
  location: westus
  maintenanceConfigurationReference: {group: maintenance.azure.hfl.dev, kind: MaintenanceConfiguration, name: patching}
---
apiVersion: containerregistry.azure.hfl.dev/v20251101
kind: RegistriesScopeMap
metadata: {name: pull-apps, namespace: $1}
spec:
  azureName: pull-apps
  owner: {name: reg}
  actions: [repositories/apps/content/read]
---
apiVersion: network.azure.hfl.dev/v20250501
kind: NetworkSecurityPerimeter
metadata: {name: perimeter, namespace: $1}
spec:
  owner: {name: rg}
  location: westus
---
apiVersion: network.azure.hfl.dev/v20260301
kind: LocalNetworkGateway
metadata: {name: onprem, namespace: $1}
spec:
  owner: {name: rg}
  location: westus
  gatewayIpAddress: 203.0.113.10
  localNetworkAddressSpace: {addressPrefixes: [10.20.0.0/16]}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: VirtualNetworkGatewayConnection
metadata: {name: to-onprem, namespace: $1}
spec:
  owner: {name: rg}
  location: westus
  connectionType: IPsec
  virtualNetworkGateway1: {reference: {armId: /subscriptions/${SUB}/resourceGroups/$1-rg/providers/Microsoft.Network/virtualNetworkGateways/$1-vng}}
  localNetworkGateway2: {reference: {group: network.azure.hfl.dev, kind: LocalNetworkGateway, name: onprem}}
  sharedKey: {name: vpn-psk, key: key}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: ExpressRouteCircuit
metadata: {name: er, namespace: $1}
spec:
  owner: {name: rg}
  location: westus
  sku: {name: Standard_MeteredData, tier: Standard, family: MeteredData}
  serviceProviderProperties: {serviceProviderName: Equinix, peeringLocation: Silicon Valley, bandwidthInMbps: 50}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: ExpressRouteCircuitsPeering
metadata: {name: private, namespace: $1}
spec:
  owner: {name: er}
  azureName: AzurePrivatePeering
  peeringType: AzurePrivatePeering
  vlanId: 100
  peerASN: 65010
  primaryPeerAddressPrefix: 192.168.10.0/30
  secondaryPeerAddressPrefix: 192.168.10.4/30
  sharedKey: {name: er-bgp, key: md5}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: ExpressRouteCircuitsAuthorization
metadata: {name: to-partner, namespace: $1}
spec:
  owner: {name: er}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: ExpressRouteGateway
metadata: {name: hub-er, namespace: $1}
spec:
  owner: {name: rg}
  location: westus
  virtualHub: {reference: {armId: /subscriptions/${SUB}/resourceGroups/$1-rg/providers/Microsoft.Network/virtualHubs/$1-hub}}
  autoScaleConfiguration: {bounds: {min: 1, max: 2}}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: ExpressRouteGatewaysExpressRouteConnection
metadata: {name: to-er, namespace: $1}
spec:
  owner: {name: hub-er}
  expressRouteCircuitPeering: {reference: {group: network.azure.hfl.dev, kind: ExpressRouteCircuitsPeering, name: private}}
  routingWeight: 10
---
apiVersion: network.azure.hfl.dev/v20260301
kind: VirtualWan
metadata: {name: wan, namespace: $1}
spec:
  owner: {name: rg}
  location: westus
  type: Standard
---
apiVersion: network.azure.hfl.dev/v20260301
kind: VirtualHub
metadata: {name: vhub, namespace: $1}
spec:
  owner: {name: rg}
  location: westus
  addressPrefix: 10.100.0.0/23
  virtualWan: {reference: {group: network.azure.hfl.dev, kind: VirtualWan, name: wan}}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: VirtualHubsHubVirtualNetworkConnection
metadata: {name: spoke, namespace: $1}
spec:
  owner: {name: vhub}
  remoteVirtualNetwork: {reference: {armId: /subscriptions/${SUB}/resourceGroups/$1-rg/providers/Microsoft.Network/virtualNetworks/$1-spoke}}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: VirtualHubsHubRouteTable
metadata: {name: spokes, namespace: $1}
spec:
  owner: {name: vhub}
  labels: [spokes]
  routes:
  - name: to-spoke
    destinationType: CIDR
    destinations: [10.1.0.0/16]
    nextHopType: ResourceId
    nextHopReference: {group: network.azure.hfl.dev, kind: VirtualHubsHubVirtualNetworkConnection, name: spoke}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: VirtualHubsRouteTable
metadata: {name: v2, namespace: $1}
spec:
  owner: {name: vhub}
  attachedConnections: [All_Vnets]
---
apiVersion: network.azure.hfl.dev/v20260301
kind: VirtualHubsRoutingIntent
metadata: {name: intent, namespace: $1}
spec:
  owner: {name: vhub}
  routingPolicies:
  - name: private
    destinations: [PrivateTraffic]
    nextHopReference: {armId: /subscriptions/${SUB}/resourceGroups/$1-rg/providers/Microsoft.Network/azureFirewalls/$1-fw}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: VirtualHubsBgpConnection
metadata: {name: nva, namespace: $1}
spec:
  owner: {name: vhub}
  peerAsn: 65010
  peerIp: 10.1.0.4
  hubVirtualNetworkConnection: {reference: {group: network.azure.hfl.dev, kind: VirtualHubsHubVirtualNetworkConnection, name: spoke}}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: VirtualHubsIpConfiguration
metadata: {name: ipconfig1, namespace: $1}
spec:
  owner: {name: vhub}
  subnet: {reference: {armId: /subscriptions/${SUB}/resourceGroups/$1-rg/providers/Microsoft.Network/virtualNetworks/$1-spoke/subnets/RouteServerSubnet}}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: VpnSite
metadata: {name: branch, namespace: $1}
spec:
  owner: {name: rg}
  location: westus
  virtualWan: {reference: {group: network.azure.hfl.dev, kind: VirtualWan, name: wan}}
  siteKey: {name: vwan-vpn, key: site}
  vpnSiteLinks:
  - name: isp1
    ipAddress: 203.0.113.10
    linkProperties: {linkProviderName: ISP1, linkSpeedInMbps: 100}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: VpnGateway
metadata: {name: s2s, namespace: $1}
spec:
  owner: {name: rg}
  location: westus
  vpnGatewayScaleUnit: 1
  virtualHub: {reference: {group: network.azure.hfl.dev, kind: VirtualHub, name: vhub}}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: VpnGatewaysNatRule
metadata: {name: egress, namespace: $1}
spec:
  owner: {name: s2s}
  type: Static
  mode: EgressSnat
  internalMappings: [{addressSpace: 10.100.0.0/24}]
  externalMappings: [{addressSpace: 100.64.0.0/24}]
---
apiVersion: network.azure.hfl.dev/v20260301
kind: VpnGatewaysVpnConnection
metadata: {name: branch, namespace: $1}
spec:
  owner: {name: s2s}
  remoteVpnSite: {reference: {group: network.azure.hfl.dev, kind: VpnSite, name: branch}}
  vpnLinkConnections:
  - name: isp1
    vpnSiteLink: {reference: {armId: /subscriptions/${SUB}/resourceGroups/$1-rg/providers/Microsoft.Network/vpnSites/branch/vpnSiteLinks/isp1}}
    sharedKey: {name: vwan-vpn, key: link}
    egressNatRules: [{reference: {group: network.azure.hfl.dev, kind: VpnGatewaysNatRule, name: egress}}]
---
apiVersion: network.azure.hfl.dev/v20260301
kind: VpnServerConfiguration
metadata: {name: users, namespace: $1}
spec:
  owner: {name: rg}
  location: westus
  vpnProtocols: [OpenVPN]
  vpnAuthenticationTypes: [Radius]
  radiusServerAddress: 10.100.0.10
  radiusServerSecret: {name: vwan-vpn, key: radius}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: P2sVpnGateway
metadata: {name: users, namespace: $1}
spec:
  owner: {name: rg}
  location: westus
  vpnGatewayScaleUnit: 1
  virtualHub: {reference: {group: network.azure.hfl.dev, kind: VirtualHub, name: vhub}}
  vpnServerConfiguration: {reference: {group: network.azure.hfl.dev, kind: VpnServerConfiguration, name: users}}
  p2SConnectionConfigurations:
  - name: clients
    vpnClientAddressPool: {addressPrefixes: [172.16.0.0/24]}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: NetworkManager
metadata: {name: avnm, namespace: $1}
spec:
  owner: {name: rg}
  location: westus
  networkManagerScopes:
    subscriptions: [/subscriptions/${SUB}]
    managementGroups: [/providers/Microsoft.Management/managementGroups/$1-platform]
  networkManagerScopeAccesses: [Connectivity, SecurityAdmin]
---
apiVersion: network.azure.hfl.dev/v20260301
kind: NetworkManagerConnection
metadata: {name: avnm-sub, namespace: $1}
spec:
  azureName: $1-avnm
  owner: {armId: /subscriptions/${SUB}}
  networkManagerReference: {group: network.azure.hfl.dev, kind: NetworkManager, name: avnm}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: NetworkManagerConnection
metadata: {name: avnm-mg, namespace: $1}
spec:
  azureName: avnm
  owner: {group: management.azure.hfl.dev, kind: ManagementGroup, name: platform}
  networkManagerReference: {group: network.azure.hfl.dev, kind: NetworkManager, name: avnm}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: NetworkManagersScopeConnection
metadata: {name: partner, namespace: $1}
spec:
  owner: {name: avnm}
  tenantId: 00000000-0000-0000-0000-0000000000e9
  resourceReference: {armId: /subscriptions/00000000-0000-0000-0000-0000000000f9}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: NetworkManagersNetworkGroup
metadata: {name: spokes, namespace: $1}
spec:
  owner: {name: avnm}
  memberType: VirtualNetwork
---
apiVersion: network.azure.hfl.dev/v20260301
kind: NetworkManagersNetworkGroupsStaticMember
metadata: {name: spoke, namespace: $1}
spec:
  owner: {name: spokes}
  resourceReference: {armId: /subscriptions/${SUB}/resourceGroups/$1-rg/providers/Microsoft.Network/virtualNetworks/$1-spoke}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: NetworkManagersConnectivityConfiguration
metadata: {name: hub-spoke, namespace: $1}
spec:
  owner: {name: avnm}
  connectivityTopology: HubAndSpoke
  hubs:
  - resourceReference: {armId: /subscriptions/${SUB}/resourceGroups/$1-rg/providers/Microsoft.Network/virtualNetworks/$1-spoke}
    resourceType: Microsoft.Network/virtualNetworks
  appliesToGroups:
  - networkGroupReference: {group: network.azure.hfl.dev, kind: NetworkManagersNetworkGroup, name: spokes}
    groupConnectivity: None
---
apiVersion: network.azure.hfl.dev/v20260301
kind: NetworkManagersSecurityAdminConfiguration
metadata: {name: baseline, namespace: $1}
spec:
  owner: {name: avnm}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: NetworkManagersSecurityAdminRuleCollection
metadata: {name: deny-mgmt, namespace: $1}
spec:
  owner: {name: baseline}
  appliesToGroups: [{networkGroupReference: {group: network.azure.hfl.dev, kind: NetworkManagersNetworkGroup, name: spokes}}]
---
apiVersion: network.azure.hfl.dev/v20260301
kind: NetworkManagersSecurityAdminRule
metadata: {name: deny-rdp, namespace: $1}
spec:
  owner: {name: deny-mgmt}
  adminRule:
    kind: Custom
    protocol: Tcp
    direction: Inbound
    access: Deny
    priority: 10
    sources: [{addressPrefix: Internet, addressPrefixType: ServiceTag}]
    destinationPortRanges: ["3389"]
---
apiVersion: network.azure.hfl.dev/v20260301
kind: NetworkManagersSecurityAdminRule
metadata: {name: default, namespace: $1}
spec:
  owner: {name: deny-mgmt}
  defaultAdminRule: {kind: Default, flag: AllowVnetInbound}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: NetworkManagersSecurityUserConfiguration
metadata: {name: team, namespace: $1}
spec:
  owner: {name: avnm}
---
apiVersion: network.azure.hfl.dev/v20260301
kind: NetworkManagersSecurityUserRuleCollection
metadata: {name: app, namespace: $1}
spec:
  owner: {name: team}
  appliesToGroups: [{networkGroupReference: {group: network.azure.hfl.dev, kind: NetworkManagersNetworkGroup, name: spokes}}]
---
apiVersion: network.azure.hfl.dev/v20260301
kind: NetworkManagersSecurityUserRule
metadata: {name: https, namespace: $1}
spec:
  owner: {name: app}
  protocol: Tcp
  direction: Inbound
  destinationPortRanges: ["443"]
---
apiVersion: management.azure.hfl.dev/v20230401
kind: ManagementGroup
metadata: {name: platform, namespace: $1}
spec:
  azureName: $1-platform
  displayName: Platform
---
apiVersion: management.azure.hfl.dev/v20230401
kind: ManagementGroupsSubscription
metadata: {name: workload, namespace: $1}
spec:
  azureName: ${SUB}
  owner: {name: platform}
---
apiVersion: resources.azure.hfl.dev/v20250401
kind: TagSet
metadata: {name: rg-tags, namespace: $1}
spec:
  owner: {group: resources.azure.com, kind: ResourceGroup, name: rg}
  properties:
    tags: {team: $1, managed-by: aso-extensions}
---
apiVersion: authorization.azure.hfl.dev/v1
kind: ManagementLock
metadata: {name: keep-rg, namespace: $1}
spec:
  owner: {group: resources.azure.com, kind: ResourceGroup, name: rg}
  level: CanNotDelete
  notes: kept by aso-extensions
  owners: [{applicationId: 00000000-0000-0000-0000-00000000a110}]
---
apiVersion: authorization.azure.hfl.dev/v20260701
kind: PolicyDefinition
metadata: {name: require-team-tag, namespace: $1}
spec:
  azureName: $1-require-team-tag
  owner: {armId: /subscriptions/${SUB}}
  policyType: Custom
  mode: Indexed
  policyRule: {if: {field: "tags['team']", exists: "false"}, then: {effect: audit}}
---
apiVersion: authorization.azure.hfl.dev/v20260701
kind: PolicyAssignment
metadata: {name: require-team-tag, namespace: $1}
spec:
  owner: {group: resources.azure.com, kind: ResourceGroup, name: rg}
  policyDefinitionReference: {group: authorization.azure.hfl.dev, kind: PolicyDefinition, name: require-team-tag}
---
apiVersion: authorization.azure.hfl.dev/v20260701
kind: PolicyAssignment
metadata: {name: allowed-locations, namespace: $1}
spec:
  owner: {group: management.azure.hfl.dev, kind: ManagementGroup, name: platform}
  policyDefinitionReference: {armId: ${BUILTIN_POLICY}}
  parameters: {listOfAllowedLocations: {value: [westus]}}
---
apiVersion: policyinsights.azure.hfl.dev/v20241001
kind: PolicyRemediation
metadata: {name: require-team-tag, namespace: $1}
spec:
  owner: {group: resources.azure.com, kind: ResourceGroup, name: rg}
  policyAssignmentReference: {group: authorization.azure.hfl.dev, kind: PolicyAssignment, name: require-team-tag}
  resourceDiscoveryMode: ExistingNonCompliant
  filters: {locations: [westus]}
---
apiVersion: policyinsights.azure.hfl.dev/v20241001
kind: PolicyAttestation
metadata: {name: require-team-tag, namespace: $1}
spec:
  owner: {group: resources.azure.com, kind: ResourceGroup, name: rg}
  policyAssignmentReference: {armId: /subscriptions/${SUB}/resourceGroups/$1-rg/providers/Microsoft.Authorization/policyAssignments/require-team-tag}
  complianceState: Compliant
  comments: tagged by the team's pipeline
---
apiVersion: consumption.azure.hfl.dev/v20260601
kind: Budget
metadata: {name: monthly, namespace: $1}
spec:
  owner: {group: resources.azure.com, kind: ResourceGroup, name: rg}
  category: Cost
  amount: 500
  timeGrain: Monthly
  timePeriod: {startDate: "2026-11-01T00:00:00Z"}
  notifications:
    actual80: {enabled: true, operator: GreaterThan, threshold: 80, contactEmails: [$1@example.com]}
    forecast100: {enabled: true, operator: GreaterThan, threshold: 100, thresholdType: Forecasted, contactEmails: [$1@example.com], contactRoles: [Owner]}
---
apiVersion: costmanagement.azure.hfl.dev/v20260601
kind: Export
metadata: {name: daily-costs, namespace: $1}
spec:
  owner: {group: resources.azure.com, kind: ResourceGroup, name: rg}
  format: Csv
  deliveryInfo:
    destination:
      type: AzureBlob
      resourceReference: {armId: /subscriptions/${SUB}/resourceGroups/$1-rg/providers/Microsoft.Storage/storageAccounts/${1//-/}costs}
      container: exports
  definition: {type: ActualCost, timeframe: MonthToDate, dataSet: {granularity: Daily}}
  schedule: {status: Active, recurrence: Daily, recurrencePeriod: {from: "2026-12-01T00:00:00Z"}}
---
apiVersion: authorization.azure.hfl.dev/v20201001
kind: RoleEligibilityScheduleRequest
metadata:
  name: eligible-reader
  namespace: $1
  annotations: {serviceoperator.azure.com/reconcile-policy: detach-on-delete}
spec:
  owner: {group: resources.azure.com, kind: ResourceGroup, name: rg}
  principalId: 11111111-2222-3333-4444-555555555555
  roleDefinitionReference: {wellKnownName: Reader}
  requestType: AdminAssign
  scheduleInfo: {expiration: {type: AfterDuration, duration: P30D}}
EOF
}

step "team-a: the identity both fakes trust"
team team-a "${CLIENT_A}"
wait_for 90 "team-a's User Ready" ready users.dbforpostgresql.azure.hfl.dev team-a app-identity
pass "team-a User Ready"
wait_for 90 "team-a's Azure SQL User Ready" ready users.sql.azure.hfl.dev team-a app-identity
logged fakesql "aso-sql-admins signed in to appdb with a token, as the Entra administrator" \
  || fail "the fake Azure SQL saw no token sign-in as its Entra administrator"
logged fakesql "CREATE USER [app-identity] FROM EXTERNAL PROVIDER" || fail "the fake Azure SQL saw no CREATE USER ... FROM EXTERNAL PROVIDER"
logged fakesql "ALTER ROLE [db_datareader] ADD MEMBER [app-identity]" || fail "the fake Azure SQL saw no role grant"
pass "team-a Azure SQL User Ready: app-identity created FROM EXTERNAL PROVIDER, signed in with team-a's aso-workload token"
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
wait_for 90 "team-a's RegistriesScopeMap Ready" ready registriesscopemaps.containerregistry.azure.hfl.dev team-a pull-apps
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.ContainerRegistry/registries/teamareg/scopeMaps/pull-apps 201" \
  || fail "the fake resource manager saw no scope map created in team-a's registry"
pass "team-a RegistriesScopeMap Ready: a scope map of ASO's Registry, generated, PUT by ASO's ARM reconciler"
wait_for 90 "team-a's VirtualMachinesRunCommand Ready" ready virtualmachinesruncommands.compute.azure.hfl.dev team-a hello
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Compute/virtualMachines/teamavm/runCommands/hello 201" \
  || fail "the fake resource manager saw no run command created on team-a's VM"
pass "team-a VirtualMachinesRunCommand Ready: a run command of ASO's VirtualMachine, generated, PUT by ASO's ARM reconciler"
wait_for 90 "team-a's ConfigurationAssignment Ready" ready configurationassignments.maintenance.azure.hfl.dev team-a vm-patching
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Compute/virtualMachines/teamavm/providers/Microsoft.Maintenance/configurationAssignments/patching 201" \
  || fail "the fake resource manager saw no maintenance assignment on team-a's VM"
pass "team-a ConfigurationAssignment Ready: our maintenance configuration assigned to ASO's VirtualMachine, an extension resource"
wait_for 90 "team-a's Vault Ready" ready vaults.recoveryservices.azure.hfl.dev team-a backups
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.RecoveryServices/vaults/team-a-backups 201" \
  || fail "the fake resource manager saw no Recovery Services vault created in team-a's resource group"
pass "team-a Vault (Recovery Services) Ready: generated, owned by ASO's ResourceGroup, PUT by ASO's ARM reconciler"
wait_for 90 "team-a's ManagementGroup Ready" ready managementgroups.management.azure.hfl.dev team-a platform
logged fakearm "request PUT /providers/Microsoft.Management/managementGroups/team-a-platform 201" \
  || fail "the fake resource manager saw no management group created at the tenant root"
pass "team-a ManagementGroup Ready: a tenant scope resource (no resource group, no owner), generated, PUT by ASO's ARM reconciler"
wait_for 90 "team-a's ManagementGroupsSubscription Ready" ready managementgroupssubscriptions.management.azure.hfl.dev team-a workload
logged fakearm "request PUT /providers/Microsoft.Management/managementGroups/team-a-platform/subscriptions/${SUB} 201" \
  || fail "the fake resource manager saw no subscription placed in team-a's management group"
pass "team-a ManagementGroupsSubscription Ready: a child of a tenant scope resource, ARM ID built from its group"
wait_for 90 "team-a's TagSet Ready" ready tagsets.resources.azure.hfl.dev team-a rg-tags
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Resources/tags/default 201" \
  || fail "the fake resource manager saw no tags set on team-a's resource group"
pass "team-a TagSet Ready: the tags of an ASO ResourceGroup as one object, an extension resource, PUT by ASO's ARM reconciler"
wait_for 90 "team-a's PolicyDefinition Ready" ready policydefinitions.authorization.azure.hfl.dev team-a require-team-tag
logged fakearm "request PUT /subscriptions/${SUB}/providers/Microsoft.Authorization/policyDefinitions/team-a-require-team-tag 201" \
  || fail "the fake resource manager saw no policy definition kept at the subscription"
pass "team-a PolicyDefinition Ready: kept at the subscription, generated (one type at several scopes, patch 0006), PUT by ASO's ARM reconciler"
wait_for 90 "team-a's PolicyAssignment Ready" ready policyassignments.authorization.azure.hfl.dev team-a require-team-tag
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Authorization/policyAssignments/require-team-tag 201" \
  || fail "the fake resource manager saw no policy assignment on team-a's resource group"
pass "team-a PolicyAssignment Ready: at an ASO ResourceGroup, naming our PolicyDefinition by object (resolved to its ARM ID)"
wait_for 90 "team-a's PolicyAssignment at the management group Ready" ready policyassignments.authorization.azure.hfl.dev team-a allowed-locations
logged fakearm "request PUT /providers/Microsoft.Management/managementGroups/team-a-platform/providers/Microsoft.Authorization/policyAssignments/allowed-locations 201" \
  || fail "the fake resource manager saw no policy assignment on team-a's management group"
pass "team-a PolicyAssignment at the management group Ready: a built-in definition by ARM ID, assigned at our ManagementGroup"
wait_for 90 "team-a's PolicyRemediation Ready" ready policyremediations.policyinsights.azure.hfl.dev team-a require-team-tag
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.PolicyInsights/remediations/require-team-tag 201" \
  || fail "the fake resource manager saw no remediation on team-a's resource group"
pass "team-a PolicyRemediation Ready: at an ASO ResourceGroup, naming our PolicyAssignment by object, generated (patch 0006)"
wait_for 90 "team-a's PolicyAttestation Ready" ready policyattestations.policyinsights.azure.hfl.dev team-a require-team-tag
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.PolicyInsights/attestations/require-team-tag 201" \
  || fail "the fake resource manager saw no attestation on team-a's resource group"
pass "team-a PolicyAttestation Ready: at an ASO ResourceGroup, naming the assignment by ARM ID"
wait_for 90 "team-a's Budget Ready" ready budgets.consumption.azure.hfl.dev team-a monthly
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Consumption/budgets/monthly 201" \
  || fail "the fake resource manager saw no budget on team-a's resource group"
pass "team-a Budget Ready: at an ASO ResourceGroup, an extension resource, generated"
wait_for 90 "team-a's Export Ready" ready exports.costmanagement.azure.hfl.dev team-a daily-costs
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.CostManagement/exports/daily-costs 201" \
  || fail "the fake resource manager saw no cost export on team-a's resource group"
pass "team-a Export Ready: at an ASO ResourceGroup, to a storage account by ARM ID, generated"
wait_for 90 "team-a's LocalNetworkGateway Ready" ready localnetworkgateways.network.azure.hfl.dev team-a onprem
pass "team-a LocalNetworkGateway Ready: owned by ASO's ResourceGroup, generated"
wait_for 90 "team-a's VirtualNetworkGatewayConnection Ready" ready virtualnetworkgatewayconnections.network.azure.hfl.dev team-a to-onprem
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/connections/to-onprem 201" \
  || fail "the fake resource manager saw no connection on team-a's resource group"
pass "team-a VirtualNetworkGatewayConnection Ready: IPsec from a gateway by ARM ID to our LocalNetworkGateway by object, the shared key from a Secret"
wait_for 90 "team-a's ExpressRouteCircuit Ready" ready expressroutecircuits.network.azure.hfl.dev team-a er
pass "team-a ExpressRouteCircuit Ready: owned by ASO's ResourceGroup, through a connectivity provider, generated"
wait_for 90 "team-a's ExpressRouteCircuitsPeering Ready" ready expressroutecircuitspeerings.network.azure.hfl.dev team-a private
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/expressRouteCircuits/er/peerings/AzurePrivatePeering 201" \
  || fail "the fake resource manager saw no peering on team-a's circuit"
pass "team-a ExpressRouteCircuitsPeering Ready: private peering owned by our circuit, the BGP key from a Secret"
wait_for 90 "team-a's ExpressRouteCircuitsAuthorization Ready" ready expressroutecircuitsauthorizations.network.azure.hfl.dev team-a to-partner
pass "team-a ExpressRouteCircuitsAuthorization Ready: owned by our circuit"
wait_for 90 "team-a's ExpressRouteGateway Ready" ready expressroutegateways.network.azure.hfl.dev team-a hub-er
pass "team-a ExpressRouteGateway Ready: owned by ASO's ResourceGroup, in a Virtual WAN hub by ARM ID"
wait_for 90 "team-a's ExpressRouteGatewaysExpressRouteConnection Ready" ready expressroutegatewaysexpressrouteconnections.network.azure.hfl.dev team-a to-er
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/expressRouteGateways/hub-er/expressRouteConnections/to-er 201" \
  || fail "the fake resource manager saw no connection on team-a's ExpressRoute gateway"
pass "team-a ExpressRouteGatewaysExpressRouteConnection Ready: owned by our gateway, to our circuit's private peering by object"
wait_for 90 "team-a's VirtualWan Ready" ready virtualwans.network.azure.hfl.dev team-a wan
pass "team-a VirtualWan Ready: owned by ASO's ResourceGroup, generated"
wait_for 90 "team-a's VirtualHub Ready" ready virtualhubs.network.azure.hfl.dev team-a vhub
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/virtualHubs/vhub 201" \
  || fail "the fake resource manager saw no virtual hub created in team-a's resource group"
pass "team-a VirtualHub Ready: in our VirtualWan by object"
wait_for 90 "team-a's VirtualHubsHubVirtualNetworkConnection Ready" ready virtualhubshubvirtualnetworkconnections.network.azure.hfl.dev team-a spoke
pass "team-a VirtualHubsHubVirtualNetworkConnection Ready: owned by our hub, to a virtual network by ARM ID"
wait_for 90 "team-a's VirtualHubsHubRouteTable Ready" ready virtualhubshubroutetables.network.azure.hfl.dev team-a spokes
pass "team-a VirtualHubsHubRouteTable Ready: a route whose next hop is our hub connection by object"
wait_for 90 "team-a's VirtualHubsRouteTable Ready" ready virtualhubsroutetables.network.azure.hfl.dev team-a v2
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/virtualHubs/vhub/routeTables/v2 201" \
  || fail "the fake resource manager saw no route table on team-a's hub"
pass "team-a VirtualHubsRouteTable Ready: owned by our hub"
wait_for 90 "team-a's VirtualHubsRoutingIntent Ready" ready virtualhubsroutingintents.network.azure.hfl.dev team-a intent
pass "team-a VirtualHubsRoutingIntent Ready: private traffic to a firewall by ARM ID"
wait_for 90 "team-a's VirtualHubsBgpConnection Ready" ready virtualhubsbgpconnections.network.azure.hfl.dev team-a nva
pass "team-a VirtualHubsBgpConnection Ready: over our hub connection by object"
wait_for 90 "team-a's VirtualHubsIpConfiguration Ready" ready virtualhubsipconfigurations.network.azure.hfl.dev team-a ipconfig1
pass "team-a VirtualHubsIpConfiguration Ready: in a subnet by ARM ID"
wait_for 90 "team-a's VpnSite Ready" ready vpnsites.network.azure.hfl.dev team-a branch
pass "team-a VpnSite Ready: in our VirtualWan by object, its key from a Secret"
wait_for 90 "team-a's VpnGateway Ready" ready vpngateways.network.azure.hfl.dev team-a s2s
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/vpnGateways/s2s 201" \
  || fail "the fake resource manager saw no VPN gateway created in team-a's resource group"
pass "team-a VpnGateway Ready: in our VirtualHub by object"
wait_for 90 "team-a's VpnGatewaysNatRule Ready" ready vpngatewaysnatrules.network.azure.hfl.dev team-a egress
pass "team-a VpnGatewaysNatRule Ready: owned by our VPN gateway"
wait_for 90 "team-a's VpnGatewaysVpnConnection Ready" ready vpngatewaysvpnconnections.network.azure.hfl.dev team-a branch
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/vpnGateways/s2s/vpnConnections/branch 201" \
  || fail "the fake resource manager saw no connection on team-a's VPN gateway"
pass "team-a VpnGatewaysVpnConnection Ready: to our VpnSite by object, the link's key from a Secret, our NAT rule by object"
wait_for 90 "team-a's VpnServerConfiguration Ready" ready vpnserverconfigurations.network.azure.hfl.dev team-a users
pass "team-a VpnServerConfiguration Ready: RADIUS, its secret from a Secret"
wait_for 90 "team-a's P2sVpnGateway Ready" ready p2svpngateways.network.azure.hfl.dev team-a users
pass "team-a P2sVpnGateway Ready: in our VirtualHub, with our VpnServerConfiguration, by object"
wait_for 90 "team-a's NetworkManager Ready" ready networkmanagers.network.azure.hfl.dev team-a avnm
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/networkManagers/avnm 201" \
  || fail "the fake resource manager saw no network manager created in team-a's resource group"
pass "team-a NetworkManager Ready: over a subscription and a management group"
wait_for 90 "team-a's subscription NetworkManagerConnection Ready" ready networkmanagerconnections.network.azure.hfl.dev team-a avnm-sub
logged fakearm "request PUT /subscriptions/${SUB}/providers/Microsoft.Network/networkManagerConnections/team-a-avnm 201" \
  || fail "the fake resource manager saw no network manager connection at the subscription"
pass "team-a NetworkManagerConnection Ready: at a subscription, to our NetworkManager by object"
wait_for 90 "team-a's management group NetworkManagerConnection Ready" ready networkmanagerconnections.network.azure.hfl.dev team-a avnm-mg
logged fakearm "request PUT /providers/Microsoft.Management/managementGroups/team-a-platform/providers/Microsoft.Network/networkManagerConnections/avnm 201" \
  || fail "the fake resource manager saw no network manager connection at the management group"
pass "team-a NetworkManagerConnection Ready: at our ManagementGroup, to our NetworkManager by object"
wait_for 90 "team-a's NetworkManagersScopeConnection Ready" ready networkmanagersscopeconnections.network.azure.hfl.dev team-a partner
pass "team-a NetworkManagersScopeConnection Ready: to another tenant's subscription by ARM ID"
wait_for 90 "team-a's NetworkManagersNetworkGroup Ready" ready networkmanagersnetworkgroups.network.azure.hfl.dev team-a spokes
pass "team-a NetworkManagersNetworkGroup Ready: owned by our NetworkManager"
wait_for 90 "team-a's NetworkManagersNetworkGroupsStaticMember Ready" ready networkmanagersnetworkgroupsstaticmembers.network.azure.hfl.dev team-a spoke
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/networkManagers/avnm/networkGroups/spokes/staticMembers/spoke 201" \
  || fail "the fake resource manager saw no static member in team-a's network group"
pass "team-a NetworkManagersNetworkGroupsStaticMember Ready: a virtual network by ARM ID"
wait_for 90 "team-a's NetworkManagersConnectivityConfiguration Ready" ready networkmanagersconnectivityconfigurations.network.azure.hfl.dev team-a hub-spoke
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/networkManagers/avnm/connectivityConfigurations/hub-spoke 201" \
  || fail "the fake resource manager saw no connectivity configuration on team-a's network manager"
pass "team-a NetworkManagersConnectivityConfiguration Ready: hub and spoke, the hub by ARM ID, our network group by object"
wait_for 90 "team-a's NetworkManagersSecurityAdminConfiguration Ready" ready networkmanagerssecurityadminconfigurations.network.azure.hfl.dev team-a baseline
pass "team-a NetworkManagersSecurityAdminConfiguration Ready: owned by our NetworkManager"
wait_for 90 "team-a's NetworkManagersSecurityAdminRuleCollection Ready" ready networkmanagerssecurityadminrulecollections.network.azure.hfl.dev team-a deny-mgmt
pass "team-a NetworkManagersSecurityAdminRuleCollection Ready: for our network group by object"
wait_for 90 "team-a's custom admin rule Ready" ready networkmanagerssecurityadminrules.network.azure.hfl.dev team-a deny-rdp
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/networkManagers/avnm/securityAdminConfigurations/baseline/ruleCollections/deny-mgmt/rules/deny-rdp 201" \
  || fail "the fake resource manager saw no custom admin rule"
pass "team-a NetworkManagersSecurityAdminRule Ready: a Custom rule"
wait_for 90 "team-a's default admin rule Ready" ready networkmanagerssecurityadminrules.network.azure.hfl.dev team-a default
pass "team-a NetworkManagersSecurityAdminRule Ready: a Default rule"
wait_for 90 "team-a's NetworkManagersSecurityUserConfiguration Ready" ready networkmanagerssecurityuserconfigurations.network.azure.hfl.dev team-a team
pass "team-a NetworkManagersSecurityUserConfiguration Ready: owned by our NetworkManager"
wait_for 90 "team-a's NetworkManagersSecurityUserRuleCollection Ready" ready networkmanagerssecurityuserrulecollections.network.azure.hfl.dev team-a app
pass "team-a NetworkManagersSecurityUserRuleCollection Ready: for our network group by object"
wait_for 90 "team-a's NetworkManagersSecurityUserRule Ready" ready networkmanagerssecurityuserrules.network.azure.hfl.dev team-a https
pass "team-a NetworkManagersSecurityUserRule Ready: owned by our rule collection"
wait_for 90 "team-a's RoleEligibilityScheduleRequest Ready" ready roleeligibilityschedulerequests.authorization.azure.hfl.dev team-a eligible-reader
pim_name="$(kubectl -n team-a get roleeligibilityschedulerequests.authorization.azure.hfl.dev eligible-reader -o jsonpath='{.spec.azureName}')"
[[ "${pim_name}" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]] \
  || fail "the eligibility request's recorded azureName '${pim_name}' is not a GUID"
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Authorization/roleEligibilityScheduleRequests/${pim_name} 201" \
  || fail "the fake resource manager saw no eligibility request on team-a's resource group"
logged fakearm "/providers/Microsoft.Authorization/roleDefinitions 200" \
  || fail "the fake resource manager saw no listing of role definitions (the built-in role Reader by name)"
pass "team-a RoleEligibilityScheduleRequest Ready: its name defaulted to the GUID ${pim_name} and recorded, the built-in role Reader resolved by name"
wait_for 90 "team-a's ManagementLock Ready" ready managementlocks.authorization.azure.hfl.dev team-a keep-rg
pass "team-a ManagementLock Ready"
[[ "$(kubectl -n team-a get managementlocks.authorization.azure.hfl.dev keep-rg -o jsonpath='{.status.owners[0].applicationId}')" == 00000000-0000-0000-0000-00000000a110 ]] \
  || fail "team-a's lock does not report its owner as Resource Manager holds it"
pass "team-a ManagementLock owners: the application ID sent and read back from Resource Manager"
logged fakearm "sub=system:serviceaccount:team-a:aso-workload scope=https://fakearm.fake-cloud.svc/.default accepted=true" \
  || fail "the fake resource manager saw no accepted request from team-a's aso-workload"
logged fakearm "request PUT /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Authorization/locks/keep-rg 201" \
  || fail "the fake resource manager saw no lock created on team-a-rg"
pass "the fake resource manager holds a lock on team-a-rg, set as team-a's aso-workload identity"

step "team-b: another identity, refused by both"
team team-b "${CLIENT_B}"
wait_for 90 "team-b's User refused" bash -c "[[ \"\$(kubectl get users.dbforpostgresql.azure.hfl.dev -n team-b app-identity -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].message}')\" == *'password authentication failed'* ]]"
pass "team-b User refused: $(message users.dbforpostgresql.azure.hfl.dev team-b app-identity | cut -c1-120)..."
wait_for 90 "team-b's Azure SQL User refused" bash -c "[[ \"\$(kubectl get users.sql.azure.hfl.dev -n team-b app-identity -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].message}')\" == *\"Login failed for user 'team-b-identity'\"* ]]"
pass "team-b Azure SQL User refused: $(message users.sql.azure.hfl.dev team-b app-identity | cut -c1-120)..."
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
wait_for 90 "team-b's RegistriesScopeMap refused" bash -c "[[ \"\$(kubectl get registriesscopemaps.containerregistry.azure.hfl.dev -n team-b pull-apps -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].message}')\" == *'this identity may not use Resource Manager'* ]]"
pass "team-b RegistriesScopeMap refused: $(message registriesscopemaps.containerregistry.azure.hfl.dev team-b pull-apps | cut -c1-120)..."
wait_for 90 "team-b's VirtualMachinesRunCommand refused" bash -c "[[ \"\$(kubectl get virtualmachinesruncommands.compute.azure.hfl.dev -n team-b hello -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].message}')\" == *'this identity may not use Resource Manager'* ]]"
pass "team-b VirtualMachinesRunCommand refused: $(message virtualmachinesruncommands.compute.azure.hfl.dev team-b hello | cut -c1-120)..."
wait_for 90 "team-b's Vault refused" bash -c "[[ \"\$(kubectl get vaults.recoveryservices.azure.hfl.dev -n team-b backups -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].message}')\" == *'this identity may not use Resource Manager'* ]]"
pass "team-b Vault refused: $(message vaults.recoveryservices.azure.hfl.dev team-b backups | cut -c1-120)..."
wait_for 90 "team-b's ManagementGroup refused" bash -c "[[ \"\$(kubectl get managementgroups.management.azure.hfl.dev -n team-b platform -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].message}')\" == *'this identity may not use Resource Manager'* ]]"
pass "team-b ManagementGroup refused: $(message managementgroups.management.azure.hfl.dev team-b platform | cut -c1-120)..."
wait_for 90 "team-b's TagSet refused" bash -c "[[ \"\$(kubectl get tagsets.resources.azure.hfl.dev -n team-b rg-tags -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].message}')\" == *'this identity may not use Resource Manager'* ]]"
pass "team-b TagSet refused: $(message tagsets.resources.azure.hfl.dev team-b rg-tags | cut -c1-120)..."
wait_for 90 "team-b's PolicyDefinition refused" bash -c "[[ \"\$(kubectl get policydefinitions.authorization.azure.hfl.dev -n team-b require-team-tag -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].message}')\" == *'this identity may not use Resource Manager'* ]]"
pass "team-b PolicyDefinition refused: $(message policydefinitions.authorization.azure.hfl.dev team-b require-team-tag | cut -c1-120)..."
wait_for 90 "team-b's PolicyAttestation refused" bash -c "[[ \"\$(kubectl get policyattestations.policyinsights.azure.hfl.dev -n team-b require-team-tag -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].message}')\" == *'this identity may not use Resource Manager'* ]]"
pass "team-b PolicyAttestation refused: $(message policyattestations.policyinsights.azure.hfl.dev team-b require-team-tag | cut -c1-120)..."
wait_for 90 "team-b's Budget refused" bash -c "[[ \"\$(kubectl get budgets.consumption.azure.hfl.dev -n team-b monthly -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].message}')\" == *'this identity may not use Resource Manager'* ]]"
pass "team-b Budget refused: $(message budgets.consumption.azure.hfl.dev team-b monthly | cut -c1-120)..."
wait_for 90 "team-b's RoleEligibilityScheduleRequest refused" bash -c "[[ \"\$(kubectl get roleeligibilityschedulerequests.authorization.azure.hfl.dev -n team-b eligible-reader -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].message}')\" == *'this identity may not use Resource Manager'* ]]"
pass "team-b RoleEligibilityScheduleRequest refused: $(message roleeligibilityschedulerequests.authorization.azure.hfl.dev team-b eligible-reader | cut -c1-120)..."
wait_for 90 "team-b's ManagementLock refused" bash -c "[[ \"\$(kubectl get managementlocks.authorization.azure.hfl.dev -n team-b keep-rg -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].message}')\" == *'401'* ]]"
pass "team-b ManagementLock refused: $(message managementlocks.authorization.azure.hfl.dev team-b keep-rg | cut -c1-120)..."

step "deleting team-a's objects"
kubectl -n team-a delete users.dbforpostgresql.azure.hfl.dev app-identity --timeout=60s
kubectl -n team-a delete vaultsecrets.keyvault.azure.hfl.dev app-url --timeout=60s
kubectl -n team-a delete users.sql.azure.hfl.dev app-identity --timeout=60s
logged fakesql "DROP USER IF EXISTS [app-identity]" || fail "the fake Azure SQL saw no DROP USER of app-identity"
pass "the Azure SQL Entra user app-identity is dropped"
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
kubectl -n team-a delete virtualnetworkgatewayconnections.network.azure.hfl.dev to-onprem --timeout=120s
kubectl -n team-a delete localnetworkgateways.network.azure.hfl.dev onprem --timeout=120s
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/connections/to-onprem 200" \
  || fail "the fake resource manager saw no delete of the connection"
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/localNetworkGateways/onprem 200" \
  || fail "the fake resource manager saw no delete of the local network gateway"
pass "the VPN connection and the local network gateway are deleted in Resource Manager"
kubectl -n team-a delete expressroutegatewaysexpressrouteconnections.network.azure.hfl.dev to-er --timeout=120s
kubectl -n team-a delete expressroutegateways.network.azure.hfl.dev hub-er --timeout=120s
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/expressRouteGateways/hub-er/expressRouteConnections/to-er 200" \
  || fail "the fake resource manager saw no delete of the gateway connection"
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/expressRouteGateways/hub-er 200" \
  || fail "the fake resource manager saw no delete of the ExpressRoute gateway"
pass "the ExpressRoute gateway and its connection are deleted in Resource Manager"
for kind_name in networkmanagerssecurityuserrules/https networkmanagerssecurityuserrulecollections/app networkmanagerssecurityuserconfigurations/team networkmanagerssecurityadminrules/default networkmanagerssecurityadminrules/deny-rdp networkmanagerssecurityadminrulecollections/deny-mgmt networkmanagerssecurityadminconfigurations/baseline networkmanagersconnectivityconfigurations/hub-spoke; do
  kubectl -n team-a delete "${kind_name%%/*}.network.azure.hfl.dev" "${kind_name#*/}" --timeout=120s
done
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/networkManagers/avnm/securityAdminConfigurations/baseline/ruleCollections/deny-mgmt/rules/deny-rdp 200" \
  || fail "the fake resource manager saw no delete of the custom admin rule"
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/networkManagers/avnm/securityUserConfigurations/team 200" \
  || fail "the fake resource manager saw no delete of the security user configuration"
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/networkManagers/avnm/connectivityConfigurations/hub-spoke 200" \
  || fail "the fake resource manager saw no delete of the connectivity configuration"
pass "the network manager's connectivity, security admin and security user configurations, rule collections and rules are deleted in Resource Manager"
for kind_name in networkmanagersnetworkgroupsstaticmembers/spoke networkmanagersnetworkgroups/spokes networkmanagersscopeconnections/partner networkmanagerconnections/avnm-mg networkmanagerconnections/avnm-sub networkmanagers/avnm; do
  kubectl -n team-a delete "${kind_name%%/*}.network.azure.hfl.dev" "${kind_name#*/}" --timeout=120s
done
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/networkManagers/avnm/networkGroups/spokes/staticMembers/spoke 200" \
  || fail "the fake resource manager saw no delete of the static member"
logged fakearm "request DELETE /providers/Microsoft.Management/managementGroups/team-a-platform/providers/Microsoft.Network/networkManagerConnections/avnm 200" \
  || fail "the fake resource manager saw no delete of the management group's network manager connection"
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/networkManagers/avnm 200" \
  || fail "the fake resource manager saw no delete of the network manager"
pass "the network manager, its connections, scope connection, network group and static member are deleted in Resource Manager"
for kind_name in p2svpngateways/users vpnserverconfigurations/users vpngatewaysvpnconnections/branch vpngatewaysnatrules/egress vpngateways/s2s vpnsites/branch; do
  kubectl -n team-a delete "${kind_name%%/*}.network.azure.hfl.dev" "${kind_name#*/}" --timeout=120s
done
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/vpnGateways/s2s/vpnConnections/branch 200" \
  || fail "the fake resource manager saw no delete of the VPN connection"
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/vpnGateways/s2s 200" \
  || fail "the fake resource manager saw no delete of the VPN gateway"
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/p2svpnGateways/users 200" \
  || fail "the fake resource manager saw no delete of the point-to-site gateway"
pass "the VPN gateways, their connection and NAT rule, the VPN site and the server configuration are deleted in Resource Manager"
for kind_name in virtualhubsipconfigurations/ipconfig1 virtualhubsbgpconnections/nva virtualhubsroutingintents/intent virtualhubsroutetables/v2 virtualhubshubvirtualnetworkconnections/spoke virtualhubshubroutetables/spokes virtualhubs/vhub virtualwans/wan; do
  kubectl -n team-a delete "${kind_name%%/*}.network.azure.hfl.dev" "${kind_name#*/}" --timeout=120s
done
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/virtualHubs/vhub/hubVirtualNetworkConnections/spoke 200" \
  || fail "the fake resource manager saw no delete of the hub connection"
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/virtualHubs/vhub 200" \
  || fail "the fake resource manager saw no delete of the virtual hub"
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/virtualWans/wan 200" \
  || fail "the fake resource manager saw no delete of the virtual WAN"
pass "the virtual WAN, its hub and the hub's children are deleted in Resource Manager"
kubectl -n team-a delete expressroutecircuitsauthorizations.network.azure.hfl.dev to-partner --timeout=120s
kubectl -n team-a delete expressroutecircuitspeerings.network.azure.hfl.dev private --timeout=120s
kubectl -n team-a delete expressroutecircuits.network.azure.hfl.dev er --timeout=120s
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/expressRouteCircuits/er/peerings/AzurePrivatePeering 200" \
  || fail "the fake resource manager saw no delete of the peering"
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/expressRouteCircuits/er 200" \
  || fail "the fake resource manager saw no delete of the circuit"
pass "the ExpressRoute circuit, its peering and its authorization are deleted in Resource Manager"
kubectl -n team-a delete registriesscopemaps.containerregistry.azure.hfl.dev pull-apps --timeout=120s
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.ContainerRegistry/registries/teamareg/scopeMaps/pull-apps 200" \
  || fail "the fake resource manager saw no delete of the scope map"
pass "the scope map is deleted in Resource Manager"
kubectl -n team-a delete virtualmachinesruncommands.compute.azure.hfl.dev hello --timeout=120s
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Compute/virtualMachines/teamavm/runCommands/hello 200" \
  || fail "the fake resource manager saw no delete of the run command"
pass "the run command is deleted in Resource Manager"
kubectl -n team-a delete configurationassignments.maintenance.azure.hfl.dev vm-patching --timeout=120s
kubectl -n team-a delete maintenanceconfigurations.maintenance.azure.hfl.dev patching --timeout=120s
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Compute/virtualMachines/teamavm/providers/Microsoft.Maintenance/configurationAssignments/patching 200" \
  || fail "the fake resource manager saw no delete of the maintenance assignment"
pass "the maintenance assignment and its configuration are deleted in Resource Manager"
kubectl -n team-a delete vaults.recoveryservices.azure.hfl.dev backups --timeout=120s
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.RecoveryServices/vaults/team-a-backups 200" \
  || fail "the fake resource manager saw no delete of the vault"
pass "the vault is deleted in Resource Manager"

kubectl -n team-a delete policyremediations.policyinsights.azure.hfl.dev require-team-tag --timeout=120s
kubectl -n team-a delete policyattestations.policyinsights.azure.hfl.dev require-team-tag --timeout=120s
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.PolicyInsights/remediations/require-team-tag 200" \
  || fail "the fake resource manager saw no delete of the remediation"
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.PolicyInsights/attestations/require-team-tag 200" \
  || fail "the fake resource manager saw no delete of the attestation"
pass "the policy remediation and attestation are deleted in Resource Manager"
kubectl -n team-a delete budgets.consumption.azure.hfl.dev monthly --timeout=120s
kubectl -n team-a delete exports.costmanagement.azure.hfl.dev daily-costs --timeout=120s
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Consumption/budgets/monthly 200" \
  || fail "the fake resource manager saw no delete of the budget"
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.CostManagement/exports/daily-costs 200" \
  || fail "the fake resource manager saw no delete of the cost export"
pass "the budget and the cost export are deleted in Resource Manager"

step "team-a: GitOps on an Arc-enabled cluster and its private link scope (kubernetesconfiguration.azure.hfl.dev)"
kc_arc="/subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Kubernetes/connectedClusters/team-a-arc"
kc_rg="/subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.KubernetesConfiguration"
kubectl apply -f - <<EOF
apiVersion: kubernetesconfiguration.azure.hfl.dev/v20230501
kind: SourceControlConfiguration
metadata: {name: cluster-config, namespace: team-a}
spec:
  owner: {armId: ${kc_arc}}
  repositoryUrl: https://github.com/contoso/cluster-config
  operatorType: Flux
  operatorScope: cluster
  operatorNamespace: flux
  operatorInstanceName: cluster-config
---
apiVersion: kubernetesconfiguration.azure.hfl.dev/v20241101preview
kind: PrivateLinkScope
metadata: {name: arc, namespace: team-a}
spec:
  owner: {name: rg}
  location: westus
  properties:
    clusterResourceReference: {armId: ${kc_arc}}
    publicNetworkAccess: Disabled
EOF
wait_for 120 "team-a's SourceControlConfiguration Ready" ready sourcecontrolconfigurations.kubernetesconfiguration.azure.hfl.dev team-a cluster-config
logged fakearm "request PUT ${kc_arc}/providers/Microsoft.KubernetesConfiguration/sourceControlConfigurations/cluster-config 201" || fail "the fake resource manager saw no source control configuration made on the cluster"
pass "team-a SourceControlConfiguration Ready"
wait_for 120 "team-a's PrivateLinkScope Ready" ready privatelinkscopes.kubernetesconfiguration.azure.hfl.dev team-a arc
logged fakearm "request PUT ${kc_rg}/privateLinkScopes/arc 201" || fail "the fake resource manager saw no Kubernetes configuration private link scope made"
pass "team-a Kubernetes configuration PrivateLinkScope Ready"
kubectl -n team-a delete sourcecontrolconfigurations.kubernetesconfiguration.azure.hfl.dev cluster-config --timeout=120s
kubectl -n team-a delete privatelinkscopes.kubernetesconfiguration.azure.hfl.dev arc --timeout=120s
logged fakearm "request DELETE ${kc_arc}/providers/Microsoft.KubernetesConfiguration/sourceControlConfigurations/cluster-config 200" || fail "the fake resource manager saw no delete of the source control configuration"
logged fakearm "request DELETE ${kc_rg}/privateLinkScopes/arc 200" || fail "the fake resource manager saw no delete of the private link scope"
pass "the source control configuration and the private link scope are deleted in Resource Manager"
kubectl -n team-a delete roleeligibilityschedulerequests.authorization.azure.hfl.dev eligible-reader --timeout=120s
if logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Authorization/roleEligibilityScheduleRequests/${pim_name}"; then
  fail "the eligibility request was sent a DELETE, though it is detached (Azure refuses one)"
fi
pass "the eligibility request is detached, not deleted: Azure keeps a request, and undoes it with another"
kubectl -n team-a delete policyassignments.authorization.azure.hfl.dev require-team-tag allowed-locations --timeout=120s
kubectl -n team-a delete policydefinitions.authorization.azure.hfl.dev require-team-tag --timeout=120s
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Authorization/policyAssignments/require-team-tag 200" \
  || fail "the fake resource manager saw no delete of the policy assignment on the resource group"
logged fakearm "request DELETE /providers/Microsoft.Management/managementGroups/team-a-platform/providers/Microsoft.Authorization/policyAssignments/allowed-locations 200" \
  || fail "the fake resource manager saw no delete of the policy assignment on the management group"
logged fakearm "request DELETE /subscriptions/${SUB}/providers/Microsoft.Authorization/policyDefinitions/team-a-require-team-tag 200" \
  || fail "the fake resource manager saw no delete of the policy definition"
pass "the policy assignments and definition are deleted in Resource Manager"

kubectl -n team-a delete managementgroupssubscriptions.management.azure.hfl.dev workload --timeout=120s
logged fakearm "request DELETE /providers/Microsoft.Management/managementGroups/team-a-platform/subscriptions/${SUB} 200" \
  || fail "the fake resource manager saw no removal of the subscription from the management group"
kubectl -n team-a delete managementgroups.management.azure.hfl.dev platform --timeout=120s
logged fakearm "request DELETE /providers/Microsoft.Management/managementGroups/team-a-platform 200" \
  || fail "the fake resource manager saw no delete of the management group"
pass "the management group and its subscription placement are deleted in Resource Manager"
kubectl -n team-a delete tagsets.resources.azure.hfl.dev rg-tags --timeout=120s
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Resources/tags/default 200" \
  || fail "the fake resource manager saw no delete of the tags"
pass "the tags are deleted in Resource Manager"

step "keys Azure makes: not written by default, written with config/secret-writer"
# docs/plan.md §8, question 9. The default install may read Secrets and not
# create them (checked above), so exporting a key Azure makes fails; the
# opt-in component grants create, update and delete, and the export works.
kubectl apply -f - <<EOF
apiVersion: network.azure.hfl.dev/v20260301
kind: ExpressRouteCircuit
metadata: {name: keys, namespace: team-a}
spec:
  owner: {name: rg}
  location: westus
  sku: {name: Standard_MeteredData, tier: Standard, family: MeteredData}
  serviceProviderProperties: {serviceProviderName: Equinix, peeringLocation: Silicon Valley, bandwidthInMbps: 50}
  operatorSpec: {secrets: {serviceKey: {name: er-keys, key: serviceKey}}}
EOF
wait_for 120 "the service key's export refused without the Secret writer" bash -c "[[ \"\$(kubectl get expressroutecircuits.network.azure.hfl.dev -n team-a keys -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].message}')\" == *forbidden* ]]"
kubectl -n team-a get secret er-keys >/dev/null 2>&1 && fail "the service key's Secret exists without the Secret writer"
pass "keys not written by default: the circuit's service key export is refused (forbidden) on its Ready condition, no Secret"
kubectl kustomize tests/operator/overlay-secret-writer > "${OUT}/install-secret-writer.yaml"
GOFLAGS=-tags=fakecloud ko_apply "${OUT}/install-secret-writer.yaml"
for verb in create update delete; do
  can "${verb}" secrets -n team-a || fail "may not ${verb} secrets with config/secret-writer"
done
! can patch secrets -n team-a || fail "may patch secrets: config/secret-writer grants create, update and delete only"
pass "config/secret-writer on: the manager may create, update and delete Secrets"
kubectl apply -f - <<EOF
apiVersion: network.azure.hfl.dev/v20260301
kind: ExpressRouteCircuitsAuthorization
metadata: {name: keys-auth, namespace: team-a}
spec:
  owner: {name: keys}
  operatorSpec: {secrets: {authorizationKey: {name: er-keys-auth, key: key}}}
EOF
wait_for 180 "team-a's circuit Ready once the Secret writer is on" ready expressroutecircuits.network.azure.hfl.dev team-a keys
wait_for 120 "team-a's exporting authorization Ready" ready expressroutecircuitsauthorizations.network.azure.hfl.dev team-a keys-auth
uuid='^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
service_key="$(kubectl -n team-a get secret er-keys -o jsonpath='{.data.serviceKey}' | base64 -d)"
auth_key="$(kubectl -n team-a get secret er-keys-auth -o jsonpath='{.data.key}' | base64 -d)"
[[ "${service_key}" =~ ${uuid} ]] || fail "the service key's Secret holds '${service_key}', not the key the fake made"
[[ "${auth_key}" =~ ${uuid} && "${auth_key}" != "${service_key}" ]] || fail "the authorization key's Secret holds '${auth_key}'"
statuses="$(kubectl -n team-a get expressroutecircuits.network.azure.hfl.dev keys -o jsonpath='{.status}'; kubectl -n team-a get expressroutecircuitsauthorizations.network.azure.hfl.dev keys-auth -o jsonpath='{.status}')"
[[ "${statuses}" != *"${service_key}"* && "${statuses}" != *"${auth_key}"* ]] || fail "a key Azure made is in a status"
pass "keys exported with config/secret-writer: a circuit's service key and an authorization's key in their Secrets, not in the status"
kubectl -n team-a delete expressroutecircuitsauthorizations.network.azure.hfl.dev keys-auth --timeout=120s
kubectl -n team-a delete expressroutecircuits.network.azure.hfl.dev keys --timeout=120s
logged fakearm "request DELETE /subscriptions/${SUB}/resourceGroups/team-a-rg/providers/Microsoft.Network/expressRouteCircuits/keys 200" \
  || fail "the fake resource manager saw no delete of the exporting circuit"
pass "the exporting circuit and authorization are deleted in Resource Manager"

echo
echo "all checks passed"
