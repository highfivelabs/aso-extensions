# Live runs: our kinds against a real Azure subscription

Every kind is **untested on real Azure** until it has a row in the ledger
below; the fake cloud (`docs/fake-cloud.md`) stays the test of record for
`task check`. This file says how a live run is made, what keeps it apart
from everything else, and what each run found. The AWS sibling's
`docs/live-runs.md` (ack-extensions) is the model.

**Status, 2026-11-10:** the sandbox and the identity exist, and live runs
against them are approved.

**Status, 2026-11-11:** register O36 (the resource providers) was reported
done. Read back from the subscription the same day
(`task live-providers`), only `Microsoft.CostManagementExports` had
changed: every other provider below still read `NotRegistered`. The tests
for those kinds are written and skip, naming the provider, until it is
registered.

**Status, 2026-11-12:** live runs work again: the offload VMs' service
account now has a federated credential on the identity, and an ID token
from an offload VM is exchanged at Entra (first run `ax120809d931`). Read
back the same day, every provider in the table under "What cannot run
here" is `Registered`, so the tests that skipped for a provider now run.

| What | Value |
| --- | --- |
| Subscription | `$AZURE_SUBSCRIPTION_ID` (`.env.example`) |
| Tenant | `$AZURE_TENANT_ID` |
| Resource group (the only target) | `aso-extensions-live-tests`, eastus2 |
| App registration | `aso-extensions-live-tests`, client ID `$AZURE_CLIENT_ID` |
| Its credentials | no secret, no certificate; federated credentials only: issuer `https://accounts.google.com`, subject the unique ID of the offload VMs' Google service account (`$OFFLOAD_SA`), audience `api://AzureADTokenExchange` |
| Its role | Contributor on the resource group, nothing else |

The identity and the group are owned outside this repository; agents never
change them. Their IDs are not in the tree: a live run takes them from the
environment (`.env.example`), and `hack/live/azure.env` fails with the
name of any that is missing.

## What keeps it apart

| Lock | Where |
| --- | --- |
| The code that reaches real Azure is compiled only with `-tags live` | `test/live` |
| `task check` compiles it (`go vet -tags live ./test/live/`) and runs none of it; only `task test-live` and `task live-list` reach `hack/live` | `Taskfile.yml` |
| An explicit opt-in on every run: `ASO_EXT_LIVE=create-real-azure-resources` | `hack/live/run.sh`, `test/live/env_test.go` |
| The group must be in `hack/live/targets.txt` (as `${AZURE_SUBSCRIPTION_ID}/<group>`), a reviewed change | the same |
| Federated credentials only: a client secret, certificate or user password in the environment refuses the run, and the Azure CLI's directory is pointed at an empty one | `hack/live/credentials.sh`, `test/live/env_test.go` |
| Contributor on one resource group: nothing outside it can be made or changed | the identity |

The manager runs as production runs it (`manager.Options{}` zero): ASO's
global credential from the `AZURE_*` environment, workload identity, the
public Resource Manager. The tests' own checks and fixtures use the same
identity (`azidentity.NewWorkloadIdentityCredential`).

## Credentials: federation from the offload VMs

On an offload VM started with `--sa "$OFFLOAD_SA"`,
`hack/live/credentials.sh` asks Google's metadata server for an ID token of
the VM's service account with the audience `api://AzureADTokenExchange`,
writes it to a file and exports `AZURE_FEDERATED_TOKEN_FILE` (with
`AZURE_CLIENT_ID`, `AZURE_TENANT_ID`, `AZURE_SUBSCRIPTION_ID` from the
environment, checked by `hack/live/azure.env`, and `USE_WORKLOAD_IDENTITY_AUTH=true`). azidentity
sends it to Entra as a client assertion; Entra checks it against the
federated credential (issuer, subject = the service account's unique ID,
audience) and returns a token for the app registration. Google's tokens
last an hour; a background loop writes a fresh one every 20 minutes.

First checked 2026-11-10: the token's claims were `iss`
`https://accounts.google.com`, `aud` `api://AzureADTokenExchange`, `sub`
the service account's unique ID; Entra issued a Resource Manager token, and the
identity read the resource group.

## Running one

The live project has 12 vCPUs of quota: one 8-vCPU VM at a time.

```sh
set -a; . ./.env; set +a   # OFFLOAD_*, AZURE_* (.env.example)
../offload/offload run --machine e2-standard-8 \
  --sa "$OFFLOAD_SA" \
  --prep 'task tools' -- \
  env AZURE_SUBSCRIPTION_ID="$AZURE_SUBSCRIPTION_ID" AZURE_TENANT_ID="$AZURE_TENANT_ID" \
  AZURE_CLIENT_ID="$AZURE_CLIENT_ID" \
  ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveLocalNetworkGateway"
```

`hack/live/run.sh` sweeps the group of leftovers older than 2 hours, runs
`go test -tags live ./test/live/`, then, pass or fail, deletes everything
created in the group since the run started (`hack/live/sweep.sh`, top-level
resources; children go with them) and lists what is left. Azure names carry
the run ID (`ax<ddhhmm><hex4>`). Output:
`offload-artifacts/live/<run ID>/` (`summary.md`, `live.log`, `left.txt`).
`task live-list` lists the group (its resources, its tags, budgets at it).
`task live-providers` prints every resource provider's registration state in
the subscription (read only; the identity cannot register one, and agents
never do).

Each test deletes what it made: its objects through the manager (which
deletes them in Azure), its fixtures (storage accounts, virtual networks, a
VM, a virtual network gateway) with Resource Manager calls, and checks Azure
answers 404 afterwards. Extension resources at the group (its `TagSet`, a
budget, a cost export) are not listed as resources: the tests remove them,
and `sweep.sh --list` shows the group's tags and budgets.

## What cannot run here (needs the subscription's owner)

**Resource providers not registered in the subscription.** Registering
one is a subscription-scope action (`*/register/action`), which Contributor
on a group cannot do, so every kind whose provider is unregistered fails
with `MissingSubscriptionRegistration`. Registered on 2026-11-10:
Authorization, Compute, Consumption, CostManagement, ManagedIdentity,
Network, Resources, Storage (and some not used here); on 2026-11-11 also
CostManagementExports; by 2026-11-12 every provider in the table below
(`task live-providers`). The tests still call `requireProvider`, so a
provider unregistered later skips again rather than fails. The kinds (and
live tests) that need each:

| Provider | Kinds | Live test (skips while unregistered) |
| --- | --- | --- |
| `Microsoft.OperationalInsights` | `QueryPack`, `QueryPacksQuery`, workspace `SavedSearch`, `Table`, `DataExport`, `LinkedService`, `LinkedStorageAccount` (also needs a role assignment, below) | `TestLiveQueryPack`, `TestLiveWorkspaceChildren`, `TestLiveMonitorPrivateLinkScope` (its workspace) |
| `Microsoft.Insights` | `PrivateLinkScope`, `PrivateLinkScopesScopedResource`, `PrivateLinkScopesPrivateEndpointConnection` | `TestLiveMonitorPrivateLinkScope` |
| `Microsoft.ContainerRegistry` | `RegistriesScopeMap`, `RegistriesToken`, `RegistriesWebhook`, `RegistriesCredentialSet` | `TestLiveContainerRegistry` |
| `Microsoft.App` | `ManagedEnvironmentsDaprComponent`, `ManagedEnvironmentsCertificate`, `ManagedEnvironmentsJavaComponent`, `ManagedEnvironmentsManagedCertificate`, `ManagedEnvironmentsStorage` | `TestLiveContainerApps` (not `ManagedEnvironmentsManagedCertificate`: below) |
| `Microsoft.Web` | `SitesSlot`, `SitesHostNameBinding`, `SitesSlotsHostNameBinding`, `Certificate` | `TestLiveWebSlotAndCertificate` |
| `Microsoft.RecoveryServices` | `Vault`, `VaultsBackupPolicy`, `VaultsBackupResourceGuardProxy`, `VaultsPrivateEndpointConnection` | `TestLiveRecoveryServices` |
| `Microsoft.Maintenance` | `MaintenanceConfiguration`, `ConfigurationAssignment` | `TestLiveMaintenanceAndShutdown` |
| `Microsoft.DevTestLab` | `Schedule` | `TestLiveMaintenanceAndShutdown` |
| `Microsoft.KeyVault` | `VaultSecret`, `VaultCertificate` (data plane, on a vault) | `TestLiveKeyVault` |
| `Microsoft.DocumentDB` (**NotRegistered**, 2026-11-12, run `ax1218083181`) | the Cosmos DB children (`documentdb.azure.hfl.dev`) | `TestLiveCosmosGremlin` (skips, naming the provider; the builder: `az provider register --namespace Microsoft.DocumentDB --subscription "$AZURE_SUBSCRIPTION_ID"`) |
| `Microsoft.DBforPostgreSQL`, `Microsoft.Sql` | PostgreSQL `User`, Azure SQL `User` (data plane, on a server) | `TestLivePostgresUser`, `TestLiveSqlUser` |
| `Microsoft.PolicyInsights` | `PolicyRemediation`, `PolicyAttestation` (they also need a policy assignment, below) | none: they need a policy assignment (below) |
| `Microsoft.Automation` | none of ours: the Automation account `WorkspacesLinkedService` links to | the `LinkedService` step of `TestLiveWorkspaceChildren` |
| `Microsoft.DataProtection` | none of ours: the resource guard `VaultsBackupResourceGuardProxy` points at | the `ResourceGuardProxy` step of `TestLiveRecoveryServices` |

**Subscription features.** `NetworkSecurityPerimetersLoggingConfiguration`
was refused: "tenant is not whitelisted and EnableServiceTagsInNsp AFEC flag
is not registered for the subscription" (`az feature register --namespace
Microsoft.Network --name EnableServiceTagsInNsp`, for the subscription's owner). Its subtest
runs with `LIVE_NSP_LOGGING=1`. While Azure refused it, deleting the
object hung too: ASO's delete got the same refusal and kept the finalizer
(the test now creates it only when the feature is on).

**Rights Contributor does not have.** Contributor excludes
`Microsoft.Authorization/*/write`, and the identity has nothing outside the
group:

| Kind | Needs |
| --- | --- |
| `ManagementLock` | `Microsoft.Authorization/locks/write` (Owner or User Access Administrator, or a custom role) on the group; `TestLiveManagementLock` (a lock on an IP group fixture) reads the identity's permissions and skips, naming the action, until a role grants it (`ax1218083181`) |
| `ResourceManagementPrivateLink` | `Microsoft.Authorization/resourceManagementPrivateLinks/write` on the group (refused `AuthorizationFailed`, 2026-11-12); `TestLiveResourceManagementPrivateLink` reads the identity's permissions on the group and skips until a role grants it |
| `WorkspacesLinkedStorageAccount` | a role assignment (`Microsoft.Authorization/roleAssignments/write`): Azure retired `CustomLogs` links in 2025, and a `Query` or `Alerts` link needs the workspace's managed identity to hold Storage Table Data Contributor on the account; the `LinkedStorageAccount` step of `TestLiveWorkspaceChildren` skips the same way |
| `PolicyDefinition`, `PolicySetDefinition` | subscription or management group scope |
| `PolicyAssignment`, `PolicyExemption` | `Microsoft.Authorization/policyAssignments/write`, `.../policyExemptions/write` |
| `PolicyRemediation`, `PolicyAttestation` | a policy assignment to act on (above) |
| `RoleEligibilityScheduleRequest`, `RoleAssignmentScheduleRequest`, `RoleManagementPolicyAssignment` | `Microsoft.Authorization` write, and Entra ID P2 for PIM |
| `ManagementGroup`, `ManagementGroupsSubscription`, `ManagementGroupsSetting` | tenant scope |
| `NetworkManager` and its children, `NetworkManagerConnection` | a scope of a subscription or management group (`networkManagerScopes`), read on it; the connection is a subscription resource |
| Entra `DirectoryRoleAssignment` (open pull request #46) | Microsoft Graph application permissions the identity lacks (`RoleManagement.ReadWrite.Directory`) |

**The subscription's limits in eastus2** (found 2026-11-12). The fixtures
for these tests now try other SKUs and US regions in turn
(`fixtureFirst`, `regions()` in `test/live/helpers_test.go`: the run's
region, then centralus, westus3, northcentralus, westus2, eastus,
southcentralus), so a refusal moves the fixture rather than skipping the
test; the test skips only when every candidate is refused, naming each
refusal. The group is in eastus2, but a resource group holds resources in
any region and the sweep deletes them all. What the subscription allowed
(runs `ax1215322672`, `ax12160408f5`, `ax121631c6ec`; each test now passes):

| Test | Refused | Taken |
| --- | --- | --- |
| `TestLiveWebSlotAndCertificate` | App Service plan S1 in eastus2 and centralus: quota 0 ("Operation cannot be completed without additional quota", `Current Limit (S1 VMs): 0`) | S1 in westus3; then Premium v3 P0v3, then Basic B1 (no slots: the slot steps skip and name the quota) |
| `TestLiveSqlUser` | Azure SQL server in eastus2: `ProvisioningDisabled` ("Provisioning is restricted in this region") | centralus |
| `TestLivePostgresUser` | PostgreSQL flexible server Burstable B1ms in eastus2: `SkuNotAvailable` | B1ms in centralus; then B2s |

Lifting the eastus2 limits is a request to Azure, no longer needed for
these tests: a quota request for Standard (S1) App Service in eastus2
(portal: Quotas, App Service), and a support request, issue type "Service
and subscription limits (quotas)", for Azure SQL Database and PostgreSQL
flexible server region access in eastus2.

**The live manager stopped two minutes in** (found 2026-11-12, run
`ax1215322672`): since the slot moves (PR #69) the live harness's main
manager filtered its cache by `metadata.namespace`, which Kubernetes
refuses on cluster-scoped Namespaces; the Namespace informer never synced
and the manager exited ("failed to wait for ... caches to sync") after its
two-minute timeout, so any object created later had no Ready condition.
Runs since #69 had finished their kinds inside the two minutes. Fixed in
`test/live/env_test.go`: Namespaces are cached without the selector.

**A site's default host name binding** (`<site>.azurewebsites.net`, and a
slot's) is Azure's: it is made with the site, and Azure keeps it until the
site goes; deleting a `SitesHostNameBinding` for it with the default
reconcile policy left the object waiting in deletion (run `ax12160408f5`).
The test now detaches it (`reconcile-policy: detach-on-delete`) and checks
Azure still has it. The fake deletes it (`docs/fake-cloud.md` should learn
this; a `Deleter` that leaves a default host name's binding in Azure, as the
SOA record sets have, is the fix in the kind, not written).

**Needs a domain we control.** `ManagedEnvironmentsManagedCertificate`
(a free certificate Azure issues only after it sees a CNAME or TXT record
for the name, pointing at an app in the environment) has no live test: the
sandbox has no DNS zone delegated to it. For the same reason the App
Service host name bindings are tested on the site's and slot's own
`azurewebsites.net` names, not a custom domain.

**Soft-deleted Key Vaults.** `TestLiveKeyVault` deletes its vault, which
Azure keeps soft-deleted for 7 days (free; the name is the run's own).
Purging it is a subscription-scope action the identity does not have.

**Skipped for cost** (an hourly fixed cost over about $0.50, or a long
commitment): every ExpressRoute kind (`ExpressRouteCircuit` and its
peerings, authorizations and connections, `ExpressRoutePort` and its
authorizations, `ExpressRouteGateway` and its connections),
`VirtualHubsRoutingIntent` (needs an Azure Firewall or NVA in the hub), and
`VirtualHubsIpConfiguration` (a Route Server-style hub, about $0.45 an hour,
not yet written). Key Vault managed HSMs and AKS kinds are not on `main`.

**Federation from the previous dev project** (resolved 2026-11-12). On
2026-11-11 the offload VMs ran as `$OFFLOAD_SA`, the previous dev project's
`offload-runner`, whose unique ID no federated credential named, so every
live run stopped at the token exchange (`AADSTS700213`). The identity's
owner added a federated credential for that account; from an offload VM
the token's claims are `iss` `https://accounts.google.com`, `aud`
`api://AzureADTokenExchange`, `sub` the account's unique ID, and Entra
issues a Resource Manager token for them.

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

Found 2026-11-12 (runs `ax120809d931`, `ax120853723a`, `ax1210377aa0`):

- A query pack's PUT needs a `properties` object, even an empty one: the
  spec marks it required and every property in it read-only, so the
  generator dropped the field and Azure answered 400 `Value cannot be
  null.`. Fixed: an `ARMResourceModifier` sends `"properties": {}`, and the
  fake now refuses a pack without it (it had accepted one).
- Azure refuses a `CustomLogs` linked storage account (`InvalidParameter`,
  "might be considered faulted due to mismatch between workspace region
  ... or ... not assigned as bypass for AzureService"; the region and the
  account's network rules were fine). Azure retired custom-log storage links
  in 2025, and a `Query` or `Alerts` link now needs the workspace's managed
  identity and a role assignment on the account. The fake does not model
  either.
- `Microsoft.Authorization/resourceManagementPrivateLinks/write` is a
  Microsoft.Authorization action, outside Contributor.
- A resource guard proxy's DELETE is refused
  (`BMSUserErrorUnlockPreviligeAccessIsNeeded`) until a POST to the proxy's
  `unlockDelete` names it and the guard's `deleteResourceGuardProxyRequests`
  request. Fixed: the kind's `Deleter` sends the unlock first, and the fake
  now refuses the delete without it (it had deleted at once). The unlock
  itself was accepted for the live identity.
- A maintenance configuration assignment at a resource group takes the
  group's region: `global` or empty is allowed at a subscription only
  (`BadRequest`). The live test had sent `global`; fixed there, and the
  fake now refuses it.
- The subscription's limits in eastus2: App Service S1 has a quota of 0
  (refused at once, "without additional quota"); Azure SQL servers are
  `ProvisioningDisabled` ("Provisioning is restricted in this region", a
  support request lifts it); PostgreSQL flexible server Burstable B1ms is
  `SkuNotAvailable`. SQL and PostgreSQL accept the PUT and fail the
  operation later, dropping the server; fixtures now read the operation and
  skip on these, naming them.
- Two tests made one Log Analytics workspace (the same name in a run); the
  private link scope's link to it refused the other test's delete
  (`CannotDeleteWorkspaceWhenLinkedToPrivateLinkScopes`). Each test now has
  its own.
- A Container Apps environment (Consumption workload profile, eastus2)
  stayed in provisioningState `Waiting` for the fixture's 15 minutes in two
  runs, and its delete took another 15 to 30 minutes; none of our kinds was
  reached. Not understood yet: the operation reported no error while
  waiting. Resolved 2026-11-12 (run `ax1218083181`): the environment
  fixture now tries other regions, eastus2 last, and was ready in
  centralus within minutes; the subscription holds **one** Container Apps
  environment (`MaxNumberOfGlobalEnvironmentsInSubExceeded`, run
  `ax12165076f0`), so a waiting one is deleted, and gone, before the next
  region is tried, and the fixture's delete may take 30 minutes or more.

## Ledger

| Date | Run | Commit | Tests | Result | Found |
| --- | --- | --- | --- | --- | --- |
| 2026-11-10 | `ax101807e8c8` | `33e8146`+ | `TestLiveLocalNetworkGateway` | PASS (53s) | the first live kind; the group empty afterwards |
| 2026-11-10 | `ax101813e66a` | `33e8146`+ | vWAN, NSP, TagSet, Budget, Export, VM run command | Budget PASS; 5 FAIL | VM sizes refused (`SkuNotAvailable`), VPN site without address space refused, tag name with `/` refused, NSP logging needs a feature, exports need a provider; group empty afterwards |
| 2026-11-10 | `ax101935c071` | `04f021c` | VM run command, vWAN/site/server configuration, NSP, TagSet, Export, virtual hub, VNet gateway connection | PASS: `VirtualMachinesRunCommand` (on Standard_D2als_v6), `VirtualWan`, `VpnSite`, `VpnServerConfiguration`, NSP and its profile, access rule, association, link, `LocalNetworkGateway` and `VirtualNetworkGatewayConnection` (VpnGw1AZ, 41 min); Export SKIP (provider); TagSet FAIL (a test bug: no tags read back as `{}`, fixed) | the VM was deleted by offload after 45 minutes without a log line while the hub's gateways were being made, so the run's cleanup never ran: a sweep from another VM removed the hub, VPN gateway, site, server configuration, WAN and network (gateways take about 15 minutes to delete). The waits now log every 4 minutes. |
| 2026-11-10 | `ax102159e7d7` | `57e3eb4` | TagSet, virtual hub | PASS: `TagSet`, `VirtualHub`, `VirtualHubsHubVirtualNetworkConnection`, `VirtualHubsHubRouteTable`, `VirtualHubsBgpConnection`, `VpnGateway` (made, Ready after about 40 min); `P2sVpnGateway` not Ready | the point-to-site gateway, made at once with its server configuration, was refused for over an hour (`ReferencedResourceNotProvisioned`: the configuration "is in Updating state", though it read Succeeded), holding the NAT rule and VPN connection behind it; the run was stopped before the VM's limit and swept (the hub and its gateways took about 35 minutes to delete) |
| 2026-11-11 | `ax1100357acb` | `d7a4544` | virtual hub | PASS (each step): `VirtualHub` and its connection, hub route table, BGP connection; `VpnGateway`, `VpnGatewaysNatRule`, `VpnGatewaysVpnConnection`; `VpnServerConfiguration` then `P2sVpnGateway` (Ready after about 35 min) | the Spot VM was preempted while the point-to-site gateway was being deleted, after every check had passed; a sweep from another VM emptied the group. Hub runs take about two hours end to end (hub 30 min, each gateway 35 to 40 min, deletes 15 to 35 min). |
| 2026-11-11 | none | `c80dc92` | `TestLiveTemplateSpec`, `TestLiveResourceManagementPrivateLink` (new), `TestLiveQueryPack`, `TestLiveWorkspaceChildren`, and `task live-providers` to read O36b back | NOT RUN: Entra refused the token exchange | `go vet -tags live` passed on the VM; then Entra answered `AADSTS700213: No matching federated identity record found for presented assertion subject` for the VM's account, the previous dev project's `offload-runner`. The identity's federated credential trusts another Google service account, which the person starting offload may not attach to a VM. Nothing was made in Azure, so there was nothing to clean up; the providers (O36b) could not be read back. Needs the identity's owner: "Federation from the previous dev project" |
| 2026-11-11 | `ax110816141d` | `c9afc12` (run from the tree before the commit) | Export, and the tests for every kind waiting on a provider (O36) | PASS: `Export` (`TestLiveCostExport`, 52s; `Microsoft.CostManagementExports` now registered); SKIP: the other 11 tests, each naming its provider, `NotRegistered` | O36, reported done, had registered only `Microsoft.CostManagementExports`: OperationalInsights, Insights, ContainerRegistry, App, Web, RecoveryServices, Maintenance, DevTestLab, KeyVault, DBforPostgreSQL, Sql, PolicyInsights, Automation and DataProtection still read `NotRegistered` (`task live-providers`); nothing was made but the export's storage account; the group empty afterwards |
| 2026-11-12 | `ax120809d931` | `7c1a24a` | `task live-providers`; `TestLiveTemplateSpec`, `TestLiveResourceManagementPrivateLink`, `TestLiveQueryPack`, `TestLiveWorkspaceChildren`, `TestLiveTagSet`, `TestLiveBudget`, `TestLiveLocalNetworkGateway` | PASS: `TemplateSpec`, `TemplateSpecsVersion`, `TagSet`, `Budget`, `LocalNetworkGateway`; FAIL: `ResourceManagementPrivateLink` (`AuthorizationFailed`), `QueryPack` (`Value cannot be null.`), `WorkspacesLinkedStorageAccount` (`CustomLogs` refused) | the first run after federation was fixed: the token exchange worked; every provider read `Registered`. The three failures are above ("Found 2026-11-12"); the group empty afterwards |
| 2026-11-12 | `ax120853723a` | `d6b4af9` | the twelve tests that had skipped for a provider, and the three fixed | PASS: `QueryPack` and `QueryPacksQuery`, `RegistriesScopeMap`/`Token`/`Webhook`/`CredentialSet` (`TestLiveContainerRegistry`), `VaultSecret`/`VaultCertificate` (`TestLiveKeyVault`), `PrivateLinkScope` and its scoped resource and endpoint connection (`TestLiveMonitorPrivateLinkScope`), `WorkspacesTable`, `WorkspacesSavedSearch`, `WorkspacesDataExport`, `WorkspacesLinkedService` (each step of `TestLiveWorkspaceChildren`; the test failed on its fixture's delete), `Vault`, `VaultsBackupPolicy`, `VaultsPrivateEndpointConnection`, `VaultsBackupResourceGuardProxy` made (`TestLiveRecoveryServices`); SKIP: `ResourceManagementPrivateLink`, `WorkspacesLinkedStorageAccount` (roles); FAIL: the proxy's delete (unlock), `ConfigurationAssignment` at the group (`global`), the App Service plan (quota 0), the SQL server and PostgreSQL server fixtures (region limits), the Container Apps environment fixture (provisioningState `Waiting` for 30 minutes, then cancelled by the sweep) | each failure is above; the sweep took the environment, and the group was empty afterwards |
| 2026-11-12 | `ax1209571e99` | `6a0a3f0` | `TestLiveSlotMove` (docs/plan.md, "Moving resources between slots") | PASS (37s): two managers as slots blue and green; an `IpGroup` made by blue carried `hfl-dev-slot: blue`; with blue paused (`skip`) green took it over (the annotation then named `hfl.dev/slot-takeover-from: blue`, renamed `hfl.dev/slot-takeover` the same day to match config-connector-extensions), the tag became green, and its `createdTime` (Resource Manager's resource list; IP groups return no `systemData`) and addresses did not change; blue's object deleted with the group kept; a blue object for it again, with another address, was Ready False `SlotMismatch` with the Warning event and Azure unchanged; green deleted it | nothing new; the group was empty before and after |
| 2026-11-12 | `ax1210377aa0` | `979ec24` | the tests that failed in `ax120853723a`, with the fixes | PASS: `WorkspacesTable`, `WorkspacesSavedSearch`, `WorkspacesDataExport`, `WorkspacesLinkedService` (`TestLiveWorkspaceChildren`, a workspace of its own), `MaintenanceConfiguration`, `ConfigurationAssignment` (on a VM and at the group), DevTestLab `Schedule` (`TestLiveMaintenanceAndShutdown`), `Vault`, `VaultsBackupPolicy`, `VaultsPrivateEndpointConnection`, `VaultsBackupResourceGuardProxy` (made, unlocked and deleted by the kind); SKIP: `TestLiveWebSlotAndCertificate` (S1 quota 0), `TestLiveSqlUser` (`ProvisioningDisabled`), `WorkspacesLinkedStorageAccount` (role); FAIL: `TestLivePostgresUser` (the server dropped for `SkuNotAvailable`, seen before the fixture waited for its operation), `TestLiveContainerApps` (the environment fixture `Waiting`) | the fixes for the query pack, the resource guard proxy and the assignment's location hold on real Azure; the group empty afterwards (the environment went in the sweep) |
| 2026-11-12 | `ax121126a94c` | `2d08ae3` | `TestLivePostgresUser`, `TestLiveSqlUser` | SKIP both: `SkuNotAvailable` (PostgreSQL B1ms) and `ProvisioningDisabled` (Azure SQL), each read from the create operation | the fixtures now wait for the operation and name the region's limit; the group empty afterwards |
| 2026-11-12 | `ax1215322672` | `5539c29`+ | `TestLiveSqlUser`, `TestLivePostgresUser`, `TestLiveWebSlotAndCertificate`, with fixtures that try other SKUs and regions | FAIL all three: every fixture was made (Azure SQL in centralus, PostgreSQL B1ms in centralus, App Service S1 in westus3, after eastus2's refusals), then no object of ours got a Ready condition | the live manager had stopped two minutes in (a `metadata.namespace` cache selector on Namespaces, since PR #69); fixed in `test/live/env_test.go`; the group empty afterwards |
| 2026-11-12 | `ax12160408f5` | `8face67`+ | the same three | PASS: Azure SQL `User` (`TestLiveSqlUser`, centralus), PostgreSQL `User` (`TestLivePostgresUser`, B1ms in centralus), `Certificate`; FAIL: the host name bindings' deletes (`TestLiveWebSlotAndCertificate`; `SitesSlot` made and changed) | Azure keeps a default host name's binding (above); the group empty afterwards |
| 2026-11-12 | `ax121631c6ec` | `686f659`+ | `TestLiveWebSlotAndCertificate` | PASS: `Certificate`, `SitesHostNameBinding`, `SitesSlot`, `SitesSlotsHostNameBinding` (S1 in westus3; the bindings detached and kept by Azure) | the first App Service run on real Azure; the group empty afterwards |
| 2026-11-12 | `ax12165076f0` | `7b50314`+ | `TestLiveContainerApps`, the environment fixture trying other regions | FAIL: the eastus2 environment `Waiting` after 12 minutes; the next region was refused, `MaxNumberOfGlobalEnvironmentsInSubExceeded` (one environment per subscription) | the fixture now deletes a waiting environment before the next region, and tries eastus2 last; the group empty afterwards |
| 2026-11-12 | `ax12172992bc` | `7b50314`+ | `TestLiveContainerApps` | PASS of every step (`ManagedEnvironmentsDaprComponent`, `ManagedEnvironmentsStorage`, `ManagedEnvironmentsCertificate`, `ManagedEnvironmentsJavaComponent`, on an environment in centralus); FAIL: the fixture's delete outlasted 15 minutes | the environment's cleanup now waits 45 minutes; the sweep removed it |
| 2026-11-12 | `ax1218083181` | `9029eda`+ | `TestLiveContainerApps`, `TestLiveAutomation`, `TestLiveManagementLock`, `TestLiveCosmosGremlin` (new) | PASS: `TestLiveContainerApps` (centralus; the first pass on real Azure), `AutomationAccount`, `AutomationAccountsRunbook`, `AutomationAccountsVariable`, `AutomationAccountsSchedule` (`TestLiveAutomation`, 53s); SKIP: `ManagementLock` (`Microsoft.Authorization/locks/write`), `TestLiveCosmosGremlin` (`Microsoft.DocumentDB` NotRegistered) | the group empty afterwards |
| 2026-11-13 | `ax132221bb8d` | `847a26a`+ | `TestLiveUserAssignedIdentityIsolationScope` (new) | PASS (23s): our `UserAssignedIdentity` (2024-11-30, `docs/plan.md` c22) made with `isolationScope: Regional`, its principal and client IDs in the status, changed to `None`, deleted | Azure takes both values in the live region, as the fake does; the group empty afterwards |
