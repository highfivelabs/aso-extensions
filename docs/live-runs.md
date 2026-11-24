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
| Its role | Contributor on the resource group; to be added by the builder, the two narrow custom roles of `hack/live/roles/` ("The subscription roles", below), not yet assigned |

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
| A test reaches the subscription itself only when `hack/live/targets.txt` names it on the subscription's line, and skips otherwise | `subscriptionScope`, `test/live/subscription_test.go` |
| Federated credentials only: a client secret, certificate or user password in the environment refuses the run, and the Azure CLI's directory is pointed at an empty one | `hack/live/credentials.sh`, `test/live/env_test.go` |
| Contributor on one resource group: nothing outside it can be made or changed | the identity |

The manager runs as production runs it (`manager.Options{}` zero): ASO's
global credential from the `AZURE_*` environment, workload identity, the
public Resource Manager. The tests' own checks and fixtures use the same
identity (`azidentity.NewWorkloadIdentityCredential`).

## The subscription roles

The builder's answer to `docs/plan.md` section 8, Q15 (2026-11-23, option
b): the kinds kept at a subscription are live-tested in the sandbox
subscription through a narrow custom role there; the tenant's
`SubscriptionTenantPolicy` and the management group kinds stay fake-only.
Two role definitions, reviewable files, cover exactly what the tests do:

| File | Role | Assigned at | Actions |
| --- | --- | --- | --- |
| `hack/live/roles/subscription.json` | `aso-extensions live tests (subscription)` | the subscription | `read`, `write`, `delete` on `Microsoft.Authorization/accessReviewScheduleDefinitions`, `accessReviewHistoryDefinitions`, `policyDefinitions`, `policySetDefinitions`, `Microsoft.CostManagement/settings`, `Microsoft.Security/alertsSuppressionRules`, `governanceRules`; `read` and `write` on `Microsoft.Authorization/accessReviewScheduleSettings` and `Microsoft.Security/pricings` (neither can be deleted) |
| `hack/live/roles/group-policy.json` | `aso-extensions live tests (group policy)` | the live resource group only | `read`, `write`, `delete` on `Microsoft.Authorization/policyAssignments` and `policyExemptions` (an assignment at the subscription is never made: the test assigns at the group) |

Left out on purpose: PIM (`roleEligibilityScheduleRequests`,
`roleAssignmentScheduleRequests`, `roleManagementPolicyAssignments`): an
identity that may write them may grant itself any role, so no role of
that kind is narrow, and PIM needs Entra ID P2; policy remediations (they
deploy resources through the assignment's identity) and attestations;
management groups and the tenant policy (tenant scope).

**Applying them is the builder's** (an IAM change; agents never do it). As
an Owner or User Access Administrator of the subscription, with the
`.env` of `.env.example`:

```sh
set -a; . ./.env; set +a
for r in subscription group-policy; do
  sed "s|\${AZURE_SUBSCRIPTION_ID}|${AZURE_SUBSCRIPTION_ID}|g" "hack/live/roles/${r}.json" >"/tmp/live-role-${r}.json"
  az role definition create --role-definition "@/tmp/live-role-${r}.json"
done
sp="$(az ad sp show --id "${AZURE_CLIENT_ID}" --query id -o tsv)"
az role assignment create --assignee-object-id "${sp}" --assignee-principal-type ServicePrincipal \
  --role "aso-extensions live tests (subscription)" --scope "/subscriptions/${AZURE_SUBSCRIPTION_ID}"
az role assignment create --assignee-object-id "${sp}" --assignee-principal-type ServicePrincipal \
  --role "aso-extensions live tests (group policy)" \
  --scope "/subscriptions/${AZURE_SUBSCRIPTION_ID}/resourceGroups/aso-extensions-live-tests"
```

A later change to a file is applied with `az role definition update
--role-definition @<file>` (the same substitution). The action names were
written from Azure's operation names without reading them back from the
tenant (no agent can): if Azure refuses one as unknown, or a test fails
with `AuthorizationFailed` naming another action, the file is corrected in
a reviewed change.

**The tests** (`test/live/subscription_test.go`) run only when
`hack/live/targets.txt`'s subscription line names them (`subscriptionScope`)
and the identity's permissions at the subscription (read from
`Microsoft.Authorization/permissions`) hold the actions (they skip, naming
the action, until the roles are assigned). Each deletes what it made; a
setting that cannot be deleted (an access review default setting, a
Defender plan, tag inheritance that was already on) is read before the
change and put back with a PUT afterwards (`restoreAfter`), then read back.
Names carry the run ID (`<base>-ax...`) or the display name
`aso-extensions live <run ID>`; `task live-list` lists any left at the
subscription (the sweep never deletes there).

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
CostManagementExports; by 2026-11-16 every provider in the table below
(`task live-providers`, including `Microsoft.DocumentDB`). The tests still call `requireProvider`, so a
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
| `Microsoft.DocumentDB` | the Cosmos DB children (`documentdb.azure.hfl.dev`) | `TestLiveCosmosGremlin` |
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
| `PolicyDefinition`, `PolicySetDefinition` | subscription or management group scope; at the subscription, `hack/live/roles/subscription.json` ("The subscription roles"; `TestLiveAzurePolicy`, pending) |
| `PolicyAssignment`, `PolicyExemption` | `Microsoft.Authorization/policyAssignments/write`, `.../policyExemptions/write`: `hack/live/roles/group-policy.json` on the group (`TestLiveAzurePolicy`, pending) |
| `PolicyRemediation`, `PolicyAttestation` | a policy assignment to act on (above) |
| `RoleEligibilityScheduleRequest`, `RoleAssignmentScheduleRequest`, `RoleManagementPolicyAssignment` | `Microsoft.Authorization` write, and Entra ID P2 for PIM; not in the roles of `hack/live/roles/` (a role that may write them may grant any role): fake-only for now |
| `ManagementGroup`, `ManagementGroupsSubscription`, `ManagementGroupsSetting` | tenant scope: fake-only (`docs/plan.md` section 8, Q15) |
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
- `RegistriesCredentialSet` validates that its Key Vault secret URI host
  resolves in DNS (`UriHostNotFound`); `TestLiveContainerRegistry` now creates
  a Key Vault fixture (`accessPolicies: []`) in `test/live/apps_test.go`.

## Pending: written, not yet run

Live tests written without a run (an agent cannot reach Azure; each runs
from an offload VM as "Running one" shows). A kind leaves this list when the
ledger has a row for it.

| Written | Test | Kinds | Fixtures (cost) | Command |
| --- | --- | --- | --- | --- |
| 2026-11-23 | `TestLiveAddressPrefixSet` | `ApplicationSecurityGroupsAddressPrefixSet` | an application security group (free) | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveAddressPrefixSet"` |
| 2026-11-23 | `TestLiveRuleCollectionGroupDraft` | `FirewallPoliciesRuleCollectionGroupDraft` | a Standard firewall policy and a rule collection group (free while no firewall uses the policy) | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveRuleCollectionGroupDraft"` |
| 2026-11-23 | `TestLiveConnectionMonitor` | `NetworkWatchersConnectionMonitor` | a network watcher in the live group, a virtual network, a workspace (free); skips with `NetworkWatcherCountLimitReached` when the subscription's watcher for the region is elsewhere (usually `NetworkWatcherRG`, which the identity may not touch) | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveConnectionMonitor"` |
| 2026-11-23 | `TestLiveSummaryRule` | `WorkspacesSummaryLog` | a workspace (free, nothing ingested) | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveSummaryRule"` |
| 2026-11-23 | `TestLiveSentinelWorkspace` | Sentinel `Setting` (EyesOn), `ContentMetadata`, `WorkspaceManagerConfiguration`, `WorkspaceManagerMember`, `WorkspaceManagerGroup`, `WorkspaceManagerAssignment` | two workspaces onboarded to Sentinel and a disabled scheduled analytics rule (free with nothing ingested); skips unless `Microsoft.SecurityInsights` is registered | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveSentinelWorkspace"` |
| 2026-11-23 | `TestLiveSiteContainers` | `SitesSitecontainer` | a Linux App Service plan, Basic B1 (about $0.02 an hour; the first region it is allowed in), and an app in `sitecontainers` mode | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveSiteContainers"` |
| 2026-11-23 | `TestLiveSqlServerExtras` | `ServersDevOpsAuditingSetting`, `ServersDatabasesDataMaskingPolicy`, `ServersSqlVulnerabilityAssessment` | an Azure SQL server (free, public access off) and a Basic database (about $0.007 an hour) in the first region that takes them | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveSqlServerExtras"` |
| 2026-11-23 | `TestLiveRestorePoints` | `RestorePointCollection`, `RestorePointCollectionsRestorePoint`, `Gallery`, `GalleriesScript`, `GalleriesInVMAccessControlProfile` | a small Linux VM (about $0.03 an hour) and its restore point's disk snapshot (a fraction of a cent); the gallery definitions are free | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveRestorePoints"` |
| 2026-11-23 | `TestLiveCosmosPostgres` | `ServerGroupsv2`, `ServerGroupsv2FirewallRule`, `ServerGroupsv2Role`, `ServerGroupsv2CoordinatorConfiguration` | none: the cluster is the kind under test, one Burstable node (1 vCore, 32 GiB; a few cents an hour, about 15 minutes to make and to delete); skips unless `Microsoft.DBforPostgreSQL` is registered | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveCosmosPostgres"` |
| 2026-11-23 | `TestLiveCosmosFleet` | `Fleet`, `FleetsFleetspace` | none: a fleet and a fleetspace with no throughput pool (free); skips unless `Microsoft.DocumentDB` is registered | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveCosmosFleet"` |
| 2026-11-23 | `TestLiveStorageDataShare` | `StorageAccountsDataShare` | a StorageV2 account (storage cents); the share is read-only to the live identity itself | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveStorageDataShare"` |
| 2026-11-23 | `TestLiveSandboxGroup` | `SandboxGroup` | none: a sandbox group with no environment linked (free: no sandbox runs); skips unless `Microsoft.App` is registered | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveSandboxGroup"` |
| 2026-11-23 | `TestLiveVaultSettings` | `VaultsBackupStorageConfig`, `VaultsBackupConfig` (and `Vault`) | none: an empty vault (free while it protects nothing); soft delete left Enabled, not AlwaysON; skips unless `Microsoft.RecoveryServices` is registered | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveVaultSettings"` |
| 2026-11-23 | `TestLiveDevTestLab` | `Lab`, `LabsSchedule`, `LabsNotificationChannel`, `LabsCost` | none: a lab with no VM (DevTest Labs bills what a lab runs; it makes a storage account and a Key Vault beside the lab in the live group, storage cents, deleted with it); up to half an hour to make and delete; skips unless `Microsoft.DevTestLab` is registered | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveDevTestLab"` |
| 2026-11-23 | `TestLiveSiteRecovery` | `VaultsReplicationFabric`, `VaultsReplicationPolicy`, `VaultsReplicationAlertSetting` (and `Vault`) | none: an empty vault with an Azure fabric for the live region and an A2A policy (free: nothing is replicated); skips unless `Microsoft.RecoveryServices` is registered | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveSiteRecovery"` |
| 2026-11-23 | `TestLiveStaticSiteBasicAuth` | `StaticSitesBasicAuth` (and `StaticSite`) | none: a Standard static web app with no content in westus2 (Standard bills per app-month, prorated: cents for a run); its password made per run | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveStaticSiteBasicAuth"` |
| 2026-11-23 | `TestLiveLogicAppsConnections` | `CustomApi`, `ApiConnection` | none: a custom API from an inline OpenAPI document, a connection to it and one to the RSS managed API (no sign-in) in the live region; free (connections bill nothing until a Logic App runs an action) | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveLogicAppsConnections"` |
| 2026-11-24 | `TestLiveSmartDetectionRule` | `ComponentsProactiveDetectionConfig` | a Log Analytics workspace and a workspace-based Application Insights component (free with nothing ingested); the rule is detached and goes with the component | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveSmartDetectionRule"` |
| 2026-11-24 | `TestLiveAlertProcessingRule` | `ActionRule` | none: a disabled alert processing rule on the live group (free); skips unless `Microsoft.AlertsManagement` is registered | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveAlertProcessingRule"` |
| 2026-11-24 | `TestLiveVirtualNetworkTap` | `VirtualNetworkTap`, `NetworkInterfacesTapConfiguration` | a virtual network and two NICs with no VM (free); the TAP for the minutes of the run (no traffic mirrored). Unknown until run: whether Azure takes a tap configuration on a NIC no VM uses | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveVirtualNetworkTap"` |
| 2026-11-24 | `TestLiveServiceBusNetworkRuleSet` | `NamespacesNetworkRuleSet` | a Standard Service Bus namespace (a base rate by the hour: cents for a run); skips unless `Microsoft.ServiceBus` is registered. Not live yet: IP and virtual network rules (the tier they need is to be seen), private endpoint connections and disaster recovery (Premium namespaces) | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveServiceBusNetworkRuleSet"` |
| 2026-11-24 | `TestLiveEventHubsNamespaceChildren` | `NamespacesNetworkRuleSet`, `NamespacesSchemaGroup`, `NamespacesApplicationGroup` (Event Hubs) | a Standard Event Hubs namespace of one throughput unit (by the hour: cents for a run); skips unless `Microsoft.EventHub` is registered. Unknown until run: whether Standard takes application groups. Not live: connections, the alias and the dedicated cluster | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveEventHubsNamespaceChildren"` |
| 2026-11-24 | `TestLiveEventGridSystemTopic` | `SystemTopic`, `SystemTopicsEventSubscription` | a StorageV2 account and a queue in it (storage cents); Event Grid bills operations and nothing is published; skips unless `Microsoft.EventGrid` is registered. Not live: the MQTT children and namespace topic subscriptions (an Event Grid namespace, by throughput unit and hour) | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveEventGridSystemTopic"` |
| 2026-11-24 | `TestLiveAPIManagementOperation` | `ApiOperation`, `ApiOperationPolicy`, `Tag`, `ApiTag` | a Consumption API Management service and an API (Consumption bills calls only, none is made; a few minutes to make); skips unless `Microsoft.ApiManagement` is registered. Not live: identity providers (no developer portal in Consumption), authorization servers, caches, schemas | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveAPIManagementOperation"` |
| 2026-11-24 | `TestLiveAPIManagementLinks` | `Tag`, `TagApiLink`, `ProductApiLink`, `ApiRelease` | a Consumption API Management service, an API and a product (Consumption bills calls only, none is made; a few minutes to make); skips unless `Microsoft.ApiManagement` is registered. Not live: resolvers (a GraphQL API), wikis and documentations (no developer portal in Consumption), the v2 workspace gateways (billed by the unit-hour) | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveAPIManagementLinks"` |
| 2026-11-24 | `TestLiveBatchPool` | `BatchAccountsApplication`, `BatchAccountsPool` | a Batch account in BatchService mode (free by itself); the pool has zero nodes (Batch bills nodes only); skips unless `Microsoft.Batch` is registered. Unknown until run: whether the subscription's Batch quota allows a pool of the VM size at all (a quota of zero cores refuses it even at zero nodes) | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveBatchPool"` |
| 2026-11-24 | `TestLiveLogicWorkflow` | `IntegrationAccount`, `IntegrationAccountsPartner`, `IntegrationAccountsSchema`, `Workflow` | none: a Free integration account (free; one a region a subscription, so the run fails if another is there) and a disabled Consumption workflow (bills runs only, none); skips unless `Microsoft.Logic` is registered. Not live: agreements (real X12, AS2 or EDIFACT settings), certificates (a Key Vault key), maps, batch configurations, sessions, assemblies | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveLogicWorkflow"` |
| 2026-11-24 | `TestLiveProvisioningService` | `ProvisioningService`, `ProvisioningServicesCertificate` | none: an S1 Device Provisioning Service (bills operations, none is made) and a CA certificate made for the run; skips unless `Microsoft.Devices` is registered. Not live: an IoT hub's certificates and connections (a Free hub is one a subscription), linking a hub | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveProvisioningService"` |
| 2026-11-24 | `TestLiveEventGridPartnerConfiguration` | `PartnerConfiguration` | none: the live group's partner configuration authorizing a partner by name (free: an authorization list); skips unless `Microsoft.EventGrid` is registered. Not live: partner registrations, namespaces and channels (the partner's side, which Microsoft onboards), partner topics (a channel makes them) | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveEventGridPartnerConfiguration"` |
| 2026-11-24 | `TestLiveDataFactoryChildren` | `FactoriesCredential`, `FactoriesGlobalParameter` | a factory and a user-assigned identity (free: Data Factory bills runs, none is made); skips unless `Microsoft.DataFactory` is registered. Not live: the managed network and its private endpoints (approval on the target), connections | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveDataFactoryChildren"` |
| 2026-11-24 | `TestLiveContainerGroupProfile` | `ContainerGroupProfile`, `NGroup` | none: a profile (a template) and an NGroup with a desired count of zero (free: no container group runs); skips unless `Microsoft.ContainerInstance` is registered | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveContainerGroupProfile"` |
| 2026-11-24 | `TestLiveWebPubSubHub` | `WebPubSub`, `WebPubSubHub` | none: a Free_F1 Web PubSub service and a hub (free; a subscription's Free units are limited, and a run past the limit fails on the service); skips unless `Microsoft.SignalRService` is registered | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveWebPubSubHub"` |
| 2026-11-24 | `TestLiveDataProtectionResourceGuard` | `ResourceGuard`, `BackupVaultsBackupResourceGuardProxy` | an empty backup vault (free); the guard and proxy are free; skips unless `Microsoft.DataProtection` is registered | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveDataProtectionResourceGuard"` |
| 2026-11-23 | `TestLiveVirtualMachineRunCommand` (extended) | `VirtualMachinesDiagnosticRunCommand` | the test's small Linux VM (about $0.03 an hour); the run command part passed on 2026-11-10 (`ax101935c071`), the diagnostic run command part is new | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveVirtualMachineRunCommand"` |

All of them in one run: `ASO_EXT_LIVE=create-real-azure-resources task test-live
TESTS="TestLiveAddressPrefixSet TestLiveRuleCollectionGroupDraft
TestLiveConnectionMonitor TestLiveSummaryRule TestLiveSentinelWorkspace
TestLiveSiteContainers TestLiveSqlServerExtras TestLiveRestorePoints
TestLiveCosmosPostgres TestLiveCosmosFleet TestLiveStorageDataShare
TestLiveSandboxGroup TestLiveVaultSettings TestLiveDevTestLab
TestLiveSiteRecovery TestLiveStaticSiteBasicAuth
TestLiveVirtualMachineRunCommand TestLiveLogicAppsConnections
TestLiveSmartDetectionRule TestLiveAlertProcessingRule
TestLiveVirtualNetworkTap TestLiveServiceBusNetworkRuleSet
TestLiveEventHubsNamespaceChildren TestLiveEventGridSystemTopic
TestLiveAPIManagementOperation TestLiveContainerGroupProfile
TestLiveWebPubSubHub TestLiveDataProtectionResourceGuard
TestLiveAPIManagementLinks TestLiveBatchPool TestLiveLogicWorkflow
TestLiveProvisioningService TestLiveEventGridPartnerConfiguration
TestLiveDataFactoryChildren"`.

Written for the subscription (above, "The subscription roles"); each skips
until the builder assigns the roles of `hack/live/roles/`:

| Written | Test | Kinds | Changes at the subscription, and how it is undone | Command |
| --- | --- | --- | --- | --- |
| 2026-11-23 | `TestLiveAccessReviews` | `AccessReviewScheduleDefinition`, `AccessReviewHistoryDefinition`, `AccessReviewScheduleSetting` | a review definition starting in a year (no review runs, no mail) and a history definition, deleted; the default review settings' `justificationRequiredOnApproval` flipped, then put back. Needs Entra ID P2 or Governance in the tenant | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveAccessReviews"` |
| 2026-11-23 | `TestLiveCostTagInheritance` | Cost Management `Setting` (`taginheritance`) | tag inheritance set, changed, deleted, then put back as read (deleted if it was off) | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveCostTagInheritance"` |
| 2026-11-23 | `TestLiveDefenderAlertRules` | `AlertsSuppressionRule`, `GovernanceRule` | a Disabled suppression rule and a disabled governance rule, changed, deleted | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveDefenderAlertRules"` |
| 2026-11-23 | `TestLiveDefenderPlan` | `Pricing` | the Defender for Key Vault plan switched to the other tier (Standard is billed per vault-month: cents for a run's minutes), then put back as read | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveDefenderPlan"` |
| 2026-11-23 | `TestLiveAzurePolicy` | `PolicyDefinition`, `PolicySetDefinition`, `PolicyAssignment`, `PolicyExemption` | an audit-only definition and a set at the subscription, assigned only to the live group with enforcement off, an exemption there; all deleted | `ASO_EXT_LIVE=create-real-azure-resources task test-live TESTS="TestLiveAzurePolicy"` |

All five in one run: `ASO_EXT_LIVE=create-real-azure-resources task test-live
TESTS="TestLiveAccessReviews TestLiveCostTagInheritance
TestLiveDefenderAlertRules TestLiveDefenderPlan TestLiveAzurePolicy"`.

Not written: the tenant's `SubscriptionTenantPolicy` and the management
group kinds (`ManagementGroup`, `ManagementGroupsSubscription`,
`ManagementGroupsSetting`, `ServiceGroup`): fake-only by the answer to Q15;
PIM, policy remediations and attestations (above, "Left out on purpose").
Sentinel's `SourceControl` needs a real repository and a credential for it.
The App Service Environment kinds (`HostingEnvironment` and its
configurations and connections, `docs/plan.md` f41): an environment takes one
to two hours to make and bills an Isolated v2 instance while it exists.
The SQL Managed Instance children and `InstancePool` (`docs/plan.md` f42): an
instance takes hours to make and bills by the vCore-hour.
The Foundry kinds of `docs/plan.md` f44: FQDN rules provision a billed Azure
Firewall, agent applications need agents made in Foundry's data plane, and a
RAI topic trains from a sample blob.
`MongoClustersUser` and `FleetsFleetspacesFleetspaceAccount` (`docs/plan.md`
f47): a user needs a MongoDB vCore cluster with Microsoft Entra
authentication on (ASO's 2024-07-01 `MongoCluster` cannot turn it on), and a
fleetspace account pools a provisioned-throughput account into a billed
pool. The storage connectors, blob access points, capacity metrics rule and
context caches of f48: a connector needs another tenant's data share, an
access point an S3-compatible store, and the metrics rule bills as custom
metrics. `SandboxGroupsVnetConnection`, `Agent` and `AgentsConnector`
(`docs/plan.md` f49): a connection needs a subnet delegated to sandboxes,
and an SRE agent bills per agent-hour.

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
| 2026-11-16 | `ax161835c086` | `81d8926` | `TestLiveSlotMove`, `TestLiveUserAssignedIdentityIsolationScope`, `TestLiveTemplateSpec`, `TestLiveKeyVault`, `TestLiveAutomation`, `TestLiveWebSlotAndCertificate`, `TestLiveMaintenanceAndShutdown`, `TestLiveManagementLock`, `TestLiveResourceManagementPrivateLink` | PASS: all 7 runnable tests (`WebSlotAndCertificate` S1 in westus3, `MaintenanceAndShutdown` `Standard_D2als_v6`); SKIP: `ManagementLock`, `ResourceManagementPrivateLink` (Contributor) | all 7 passing cleanly; the group empty afterwards |
| 2026-11-16 | `ax161856b1a3` | `81d8926` | `TestLiveQueryPack`, `TestLiveWorkspaceChildren`, `TestLiveMonitorPrivateLinkScope`, `TestLiveContainerRegistry`, `TestLiveContainerApps`, `TestLiveRecoveryServices` | PASS: `QueryPack`, `WorkspaceChildren` (`LinkedService` PASS, `LinkedStorageAccount` SKIP), `MonitorPrivateLinkScope`, `RecoveryServices`, `ContainerApps` (westus3 after centralus timed out); FAIL: `ContainerRegistry` (`UriHostNotFound` on Key Vault URI) | `RegistriesCredentialSet` requires a resolvable Key Vault host; the group empty afterwards |
| 2026-11-16 | `ax1620167567` | `81d8926` | `TestLiveSqlUser`, `TestLivePostgresUser`, `TestLiveCosmosGremlin` | PASS: `SqlUser` (centralus), `PostgresUser` (B1ms in centralus), `CosmosGremlin` (eastus2) | first live pass of `TestLiveCosmosGremlin` (`Microsoft.DocumentDB` now Registered); the group empty afterwards |
| 2026-11-16 | `ax1620450a82` | `81d8926`+ | `TestLiveContainerRegistry` | PASS (117.37s) | fixed `TestLiveContainerRegistry` in `test/live/apps_test.go` to create a Key Vault fixture (`accessPolicies: []`); the group empty afterwards |
