# Live runs: our kinds against a real Azure subscription

Every kind is **untested on real Azure** until it has a row in the ledger
below; the fake cloud (`docs/fake-cloud.md`) stays the test of record for
`task check`. This file says how a live run is made, what keeps it apart
from everything else, and what each run found. The AWS sibling's
`docs/live-runs.md` (ack-extensions) is the model.

**Status, 2026-11-10:** the builder made the sandbox and the identity and
said "feel free to start testing against those asap".

| What | Value |
| --- | --- |
| Subscription | `149e8810-e0fe-48e2-a46e-6f8f575bde01` ("Azure subscription 1") |
| Tenant | `13b0f5ef-4412-44a3-bd17-eec79070fb78` |
| Resource group (the only target) | `aso-extensions-live-tests`, eastus2 |
| App registration | `aso-extensions-live-tests`, client ID `cfe23580-20d6-4bd4-bb42-5d302fc07c03`, service principal `2628ee15-e543-48b3-863f-309a35250a4e` |
| Its credentials | no secret, no certificate; one federated credential, `google-offload-runner`: issuer `https://accounts.google.com`, subject `102306610785199453342` (the Google service account `offload-runner@our-chassis-514316-r0`), audience `api://AzureADTokenExchange` |
| Its role | Contributor on the resource group, nothing else |

The builder owns the identity and the group; agents never change them.
These IDs are in `hack/live/azure.env`; none is a secret.

## What keeps it apart

| Lock | Where |
| --- | --- |
| The code that reaches real Azure is compiled only with `-tags live` | `test/live` |
| `task check` compiles it (`go vet -tags live ./test/live/`) and runs none of it; only `task test-live` and `task live-list` reach `hack/live` | `Taskfile.yml` |
| An explicit opt-in on every run: `ASO_EXT_LIVE=create-real-azure-resources` | `hack/live/run.sh`, `test/live/env_test.go` |
| The subscription and group must be in `hack/live/targets.txt`, a reviewed change | the same |
| Federated credentials only: a client secret, certificate or user password in the environment refuses the run, and the Azure CLI's directory is pointed at an empty one | `hack/live/credentials.sh`, `test/live/env_test.go` |
| Contributor on one resource group: nothing outside it can be made or changed | the identity (the builder's) |

The manager runs as production runs it (`manager.Options{}` zero): ASO's
global credential from the `AZURE_*` environment, workload identity, the
public Resource Manager. The tests' own checks and fixtures use the same
identity (`azidentity.NewWorkloadIdentityCredential`).

## Credentials: federation from the offload VMs

On an offload VM started with `--sa offload-runner@our-chassis-514316-r0...`,
`hack/live/credentials.sh` asks Google's metadata server for an ID token of
the VM's service account with the audience `api://AzureADTokenExchange`,
writes it to a file and exports `AZURE_FEDERATED_TOKEN_FILE` (with
`AZURE_CLIENT_ID`, `AZURE_TENANT_ID`, `AZURE_SUBSCRIPTION_ID` from
`hack/live/azure.env`, and `USE_WORKLOAD_IDENTITY_AUTH=true`). azidentity
sends it to Entra as a client assertion; Entra checks it against the
federated credential (issuer, subject = the service account's unique ID,
audience) and returns a token for the app registration. Google's tokens
last an hour; a background loop writes a fresh one every 20 minutes.

First checked 2026-11-10: the token's claims were `iss`
`https://accounts.google.com`, `aud` `api://AzureADTokenExchange`, `sub`
`102306610785199453342`; Entra issued a Resource Manager token, and the
identity read the resource group.

## Running one

our-chassis has 12 vCPUs of quota: one 8-vCPU VM at a time.

```sh
OFFLOAD_PROJECT=our-chassis-514316-r0 \
OFFLOAD_BUCKET=gs://our-chassis-514316-r0-offload \
OFFLOAD_SA=offload-runner@our-chassis-514316-r0.iam.gserviceaccount.com \
../offload/offload run --machine e2-standard-8 \
  --sa offload-runner@our-chassis-514316-r0.iam.gserviceaccount.com \
  --prep 'task tools' -- \
  env ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveLocalNetworkGateway"
```

`hack/live/run.sh` sweeps the group of leftovers older than 2 hours, runs
`go test -tags live ./test/live/`, then, pass or fail, deletes everything
created in the group since the run started (`hack/live/sweep.sh`, top-level
resources; children go with them) and lists what is left. Azure names carry
the run ID (`ax<ddhhmm><hex4>`). Output:
`offload-artifacts/live/<run ID>/` (`summary.md`, `live.log`, `left.txt`).
`task live-list` lists the group (its resources, its tags, budgets at it).

Each test deletes what it made: its objects through the manager (which
deletes them in Azure), its fixtures (storage accounts, virtual networks, a
VM, a virtual network gateway) with Resource Manager calls, and checks Azure
answers 404 afterwards. Extension resources at the group (its `TagSet`, a
budget, a cost export) are not listed as resources: the tests remove them,
and `sweep.sh --list` shows the group's tags and budgets.

## What cannot run here (for the builder)

**Resource providers not registered in the subscription.** Registering
one is a subscription-scope action (`*/register/action`), which Contributor
on a group cannot do, so every kind whose provider is unregistered fails
with `MissingSubscriptionRegistration`. Registered on 2026-11-10:
Authorization, Compute, Consumption, CostManagement, ManagedIdentity,
Network, Resources, Storage (and some not used here). The kinds waiting on
a registration, by provider:

| Provider | Kinds |
| --- | --- |
| `Microsoft.OperationalInsights` | `QueryPack`, `QueryPacksQuery`, workspace `SavedSearch`, `Table`, `DataExport`, `LinkedService`, `LinkedStorageAccount` |
| `Microsoft.Insights` | `PrivateLinkScope`, `PrivateLinkScopesScopedResource`, `PrivateLinkScopesPrivateEndpointConnection` |
| `Microsoft.ContainerRegistry` | `RegistriesScopeMap`, `RegistriesToken`, `RegistriesWebhook`, `RegistriesCredentialSet` |
| `Microsoft.App` | `ManagedEnvironmentsDaprComponent`, `ManagedEnvironmentsCertificate`, `ManagedEnvironmentsJavaComponent`, `ManagedEnvironmentsManagedCertificate`, `ManagedEnvironmentsStorage` |
| `Microsoft.Web` | `SitesSlot`, `SitesHostNameBinding`, `SitesSlotsHostNameBinding`, `Certificate` |
| `Microsoft.RecoveryServices` | `Vault`, `VaultsBackupPolicy`, `VaultsBackupResourceGuardProxy`, `VaultsPrivateEndpointConnection` |
| `Microsoft.Maintenance` | `MaintenanceConfiguration`, `ConfigurationAssignment` |
| `Microsoft.DevTestLab` | `Schedule` |
| `Microsoft.KeyVault` | `VaultSecret`, `VaultCertificate` (data plane, on a vault) |
| `Microsoft.DBforPostgreSQL`, `Microsoft.Sql` | PostgreSQL `User`, Azure SQL `User` (data plane, on a server) |
| `Microsoft.CostManagementExports` | cost `Export` (Azure, 2026-11-10: "RP Not Registered. Register destination storage account subscription with Microsoft.CostManagementExports"; the test skips until it is) |
| `Microsoft.PolicyInsights` | `PolicyRemediation`, `PolicyAttestation` (they also need a policy assignment, below) |

**Subscription features.** `NetworkSecurityPerimetersLoggingConfiguration`
was refused: "tenant is not whitelisted and EnableServiceTagsInNsp AFEC flag
is not registered for the subscription" (`az feature register --namespace
Microsoft.Network --name EnableServiceTagsInNsp`, the builder's). Its subtest
runs with `LIVE_NSP_LOGGING=1`. While Azure refused it, deleting the
object hung too: ASO's delete got the same refusal and kept the finalizer
(the test now creates it only when the feature is on).

**Rights Contributor does not have.** Contributor excludes
`Microsoft.Authorization/*/write`, and the identity has nothing outside the
group:

| Kind | Needs |
| --- | --- |
| `ManagementLock` | `Microsoft.Authorization/locks/write` (Owner or User Access Administrator, or a custom role) on the group |
| `PolicyDefinition`, `PolicySetDefinition` | subscription or management group scope |
| `PolicyAssignment`, `PolicyExemption` | `Microsoft.Authorization/policyAssignments/write`, `.../policyExemptions/write` |
| `PolicyRemediation`, `PolicyAttestation` | a policy assignment to act on (above) |
| `RoleEligibilityScheduleRequest`, `RoleAssignmentScheduleRequest`, `RoleManagementPolicyAssignment` | `Microsoft.Authorization` write, and Entra ID P2 for PIM |
| `ManagementGroup`, `ManagementGroupsSubscription`, `ManagementGroupsSetting` | tenant scope |
| `NetworkManager` and its children, `NetworkManagerConnection` | a scope of a subscription or management group (`networkManagerScopes`), read on it; the connection is a subscription resource |
| Entra `DirectoryRoleAssignment` (open pull request #46) | Microsoft Graph application permissions the identity lacks (`RoleManagement.ReadWrite.Directory`) |

**Skipped for cost** (an hourly fixed cost over about $0.50, or a long
commitment): every ExpressRoute kind (`ExpressRouteCircuit` and its
peerings, authorizations and connections, `ExpressRoutePort` and its
authorizations, `ExpressRouteGateway` and its connections),
`VirtualHubsRoutingIntent` (needs an Azure Firewall or NVA in the hub), and
`VirtualHubsIpConfiguration` (a Route Server-style hub, about $0.45 an hour,
not yet written). Key Vault managed HSMs and AKS kinds are not on `main`.

## What Azure checks that the fake did not

Found by the first runs, 2026-11-10 (the tests were written against the
fake; each is now in `test/live`, and the fake should learn them):

- A `VpnSite` needs an address space or BGP settings on its links
  (`VpnSiteAddressPrefixesAndVpnSiteLinkBgpSettingsCannotBeNull`); the fake
  accepted a site with neither.
- Tag names refuse `<`, `>`, `%`, `&`, `\`, `?`, `/` (`InvalidTagNameCharacters`);
  ASO reports it as retryable, so the object waits, not fails.
- A cost export to a storage account needs `Microsoft.CostManagementExports`
  registered in the account's subscription.
- A point-to-site gateway made together with its server configuration is
  refused while the configuration is updating, and ASO's retries did not
  get past it in over an hour; made after the configuration is Ready, it
  succeeded (the fifth run).
- `Standard_B1s`, `Standard_B2ats_v2` and `Standard_B2als_v2` were refused for
  capacity in eastus2 (`SkuNotAvailable`); `Standard_D2als_v6` was taken.

## Ledger

| Date | Run | Commit | Tests | Result | Found |
| --- | --- | --- | --- | --- | --- |
| 2026-11-10 | `ax101807e8c8` | `33e8146`+ | `TestLiveLocalNetworkGateway` | PASS (53s) | the first live kind; the group empty afterwards |
| 2026-11-10 | `ax101813e66a` | `33e8146`+ | vWAN, NSP, TagSet, Budget, Export, VM run command | Budget PASS; 5 FAIL | VM sizes refused (`SkuNotAvailable`), VPN site without address space refused, tag name with `/` refused, NSP logging needs a feature, exports need a provider; group empty afterwards |
| 2026-11-10 | `ax101935c071` | `04f021c` | VM run command, vWAN/site/server configuration, NSP, TagSet, Export, virtual hub, VNet gateway connection | PASS: `VirtualMachinesRunCommand` (on Standard_D2als_v6), `VirtualWan`, `VpnSite`, `VpnServerConfiguration`, NSP and its profile, access rule, association, link, `LocalNetworkGateway` and `VirtualNetworkGatewayConnection` (VpnGw1AZ, 41 min); Export SKIP (provider); TagSet FAIL (a test bug: no tags read back as `{}`, fixed) | the VM was deleted by offload after 45 minutes without a log line while the hub's gateways were being made, so the run's cleanup never ran: a sweep from another VM removed the hub, VPN gateway, site, server configuration, WAN and network (gateways take about 15 minutes to delete). The waits now log every 4 minutes. |
| 2026-11-10 | `ax102159e7d7` | `57e3eb4` | TagSet, virtual hub | PASS: `TagSet`, `VirtualHub`, `VirtualHubsHubVirtualNetworkConnection`, `VirtualHubsHubRouteTable`, `VirtualHubsBgpConnection`, `VpnGateway` (made, Ready after about 40 min); `P2sVpnGateway` not Ready | the point-to-site gateway, made at once with its server configuration, was refused for over an hour (`ReferencedResourceNotProvisioned`: the configuration "is in Updating state", though it read Succeeded), holding the NAT rule and VPN connection behind it; the run was stopped before the VM's limit and swept (the hub and its gateways took about 35 minutes to delete) |
| 2026-11-11 | `ax1100357acb` | `d7a4544` | virtual hub | PASS (each step): `VirtualHub` and its connection, hub route table, BGP connection; `VpnGateway`, `VpnGatewaysNatRule`, `VpnGatewaysVpnConnection`; `VpnServerConfiguration` then `P2sVpnGateway` (Ready after about 35 min) | the Spot VM was preempted while the point-to-site gateway was being deleted, after every check had passed; a sweep from another VM emptied the group. Hub runs take about two hours end to end (hub 30 min, each gateway 35 to 40 min, deletes 15 to 35 min). |
