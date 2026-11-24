# The fake cloud

What the tests run against instead of Azure. Nothing in it reaches a real
Azure endpoint or needs a credential, and none of it is in the production
manager: `pkg/fakecloud` links only into tests and into builds with
`-tags fakecloud` (`hack/check-no-fake-cloud.sh` checks the production
build).

## Why not recordings

ASO's own tests replay go-vcr recordings of real Azure, made once against a
subscription. That needs a subscription to record, and a recording only
shows the requests it was made with. The kinds here talk to data planes
whose behaviour is small and documented, and to PostgreSQL, which can run
for real, so the fakes below are written from the API documentation and
run the real client code against them.

## The parts

| Part | What it is | Where |
| --- | --- | --- |
| Entra | an `http.RoundTripper` answering instance discovery, OpenID metadata and `/oauth2/v2.0/token`; azidentity's own credentials run against it, through ASO's `TokenCredentialProvider` hook. A token it issues reads as the tenant, client ID, subject (the client assertion's `sub`, or a digest of the client secret) and audience. The same answers over HTTPS (`entra.Handler`, `cmd/fakeentra`) for stock ASO, below | `pkg/fakecloud/entra`, `cmd/fakeentra` |
| Key Vault | the data plane, REST 7.5. Secrets: set, get (latest or a version), update attributes, soft delete, get deleted, recover, purge. Certificates: PEM import (refused unless it holds a certificate and the key that matches it), get, update attributes, soft delete, get deleted, recover, purge, each with the secret of the same name and version Key Vault keeps (the PEM as its value). Soft delete is always on; requests carry a bearer token the test decides to accept | `pkg/fakecloud/keyvault`, `cmd/fakevault` |
| Azure Database for PostgreSQL | PostgreSQL 17 in Docker (tests) or a pod (kind), TLS on, with `azure_pg_admin`, a password administrator, Entra administrators whose password is the fake token of the identity that may sign in as them, and stand-ins for `pgaadauth_create_principal` and `pgaadauth_create_principal_with_oid` | `pkg/fakecloud/postgres` |
| Microsoft Graph | directory role assignments (`/beta` and `/v1.0` `roleManagement/directory/roleAssignments`: create, get, list with a `$filter` on `principalId`, `roleDefinitionId` and `directoryScopeId`, delete; no update, as in Graph) over a directory of principals, administrative units and role definitions (four built-in roles by template ID, and any a test adds); refuses an unknown principal, role or administrative unit (404), a scope that is neither `/` nor an administrative unit, both or neither of `directoryScopeId` and `appScopeId` (400) and the same assignment twice (409); takes gzipped bodies, as the Graph SDK's default middleware sends them; requests carry a bearer token the test decides to accept | `pkg/fakecloud/graph`, `cmd/fakegraph` |
| Azure SQL Database | a TDS 7.4 server over TLS (go-mssqldb runs unchanged against it) with the database users and fixed-role memberships ASO's statements change: sign-in with the SQL administrator's password, a contained user's password, or an access token that signs in as an Entra principal (the server's Entra administrator, or a user created `FROM EXTERNAL PROVIDER`); only an administrator changes users; only a token connection creates Entra users (33159), and only for a name in its directory (33130); names compare without case | `pkg/fakecloud/azuresql`, `cmd/fakesql` |
| Resource Manager | a store of resources by ARM ID (PUT with the parent existing, GET, DELETE; the body is kept as sent with `id`, `name`, `type` and `provisioningState: Succeeded` added, no properties checked but the management group hierarchy's, Azure Policy's, Privileged Identity Management's, budgets', cost exports', site-to-site VPN's and ExpressRoute's (circuits, ports, gateways), Virtual WAN's (hubs, VPN gateways), Virtual Network Manager's and maintenance assignments' references and scopes; built-in role definitions listed by name; the properties Azure computes that clients read back (a vault's `vaultUri`, a key's `keyUri`, identities' principal, client and tenant IDs, a public IP's address and FQDN, a registry's login server, a workspace's `customerId`, record sets' FQDNs, a storage account's endpoints, an AKS cluster's FQDN, node resource group, kubelet identity and OIDC issuer, an App Service site's or slot's default host name and custom domain verification ID, an Azure SQL server's and a PostgreSQL or MySQL flexible server's FQDN, a Cosmos DB account's document endpoint and its locations' IDs, endpoints and read and write lists, an Automation account's hybrid service URL; an Automation account's agent registration information, its DSC server endpoint, answered to a GET), each derived from the resource's ID; the children Azure makes with a parent (a storage account's default blob, file, queue and table services, a DNS zone's SOA record set); Key Vault keys in the vault's data plane too (`https://<vault>.vault.azure.net`: get, update, delete, rotation policy), as stock ASO's `VaultKey` reads them; ASO's own ARM client, for the generated kinds, talks to it) and management locks (`Microsoft.Authorization/locks`, 2020-05-01, level, notes and owners kept as sent) at any scope that exists; a lock covers its scope and everything under it: `CanNotDelete` refuses deletes (and deleting a scope that holds a locked resource), `ReadOnly` also refuses writes, each with 409 `ScopeLocked`; locks themselves can always be changed; requests carry a bearer token the test decides to accept | `pkg/fakecloud/arm`, `cmd/fakearm` |
| ASO | not run: its owner CRDs (generated from ASO's types at the pin) and `FlexibleServer`/`Vault`/`ResourceGroup` objects carrying the status, Ready condition and ARM ID annotation ASO would write | `config/test/aso-crds`, the tests |
| TLS | a throwaway CA and server certificates; the manager trusts the CA through `SSL_CERT_FILE` on kind, through its client options in tests | `pkg/fakecloud/tlsutil` |

## Running it

```sh
task test           # envtest + fake Entra, vault and Resource Manager in process, PostgreSQL in Docker
task operator-e2e   # the same fakes on kind, with the install from config/default
```

`task test` needs Docker and pulls `postgres:17` if it is missing.
`task operator-e2e` also needs kind and ko; it leaves the cluster
(`aso-extensions-operator`) up unless `KEEP=0`.

## Stock ASO against the fake cloud

The manager here takes the fake Entra in process (`-tags fakecloud`).
Stock Azure Service Operator cannot: it fetches tokens from Entra over
HTTPS, and MSAL asks `login.microsoftonline.com` for instance discovery
whatever authority host it is given (`AZURE_AUTHORITY_HOST` alone is not
enough; MSAL then fails discovery against the real Entra, or reaches it).
So the fake answers *as* `login.microsoftonline.com` (added 2026-11-12):

| Piece | What | Where |
| --- | --- | --- |
| The server | `cmd/fakeentra`, TLS, a certificate for `login.microsoftonline.com` and `fakeentra.fake-cloud.svc` from the fake cloud's CA | `config/fake-cloud/fake-cloud.yaml` (Deployment and Service `fakeentra`), `tests/operator/fixtures` |
| The name | a CoreDNS rewrite in the test cluster: `rewrite name exact login.microsoftonline.com fakeentra.fake-cloud.svc.cluster.local answer auto`, after `ready` in the Corefile | `tests/operator/run.sh`, "the fake Entra for stock ASO" |
| The trust | the fake cloud's CA in the client's `SSL_CERT_FILE` (a ConfigMap `fake-cloud-ca` mounted at `/fake-cloud-ca`) | the same, `config/fake-cloud/entra-check.yaml` |
| The proof | a Job running `fakeentra --check`: azidentity's client-secret credential, the public cloud unchanged, gets a fake token; `ok: fake Entra answers a stock azidentity client as login.microsoftonline.com` | `tests/operator/run.sh`, `pkg/fakecloud/entra/handler_test.go` (client secret and workload identity over HTTPS, and an untrusting client refused) |

To run stock ASO against it (a chart's `tests/mock.values.yaml`), in the
same kind cluster: install ASO with `AZURE_RESOURCE_MANAGER_ENDPOINT` and
`AZURE_RESOURCE_MANAGER_AUDIENCE` set to `https://fakearm.fake-cloud.svc`
(and `MICROSOFT_GRAPH_ENDPOINT` to `https://fakegraph.fake-cloud.svc` for
Entra kinds), `AZURE_SUBSCRIPTION_ID` and `AZURE_TENANT_ID` any GUIDs,
`AZURE_CLIENT_ID` and `AZURE_CLIENT_SECRET` any values (a client secret's
token carries `secret:<digest>` as its subject) or workload identity (the
subject is the service account), and the `fake-cloud-ca` ConfigMap mounted
in the controller with `SSL_CERT_FILE=/fake-cloud-ca/ca.crt`. For
`VaultKey`, which reads its key in the vault's data plane, send every
vault's name to `fakearm` too (`rewrite name regex (.+)\.vault\.azure\.net
fakearm.fake-cloud.svc.cluster.local answer auto`; the fixtures' `fakearm`
certificate carries `*.vault.azure.net`) and give `fakearm` ASO's tenant
(`--tenant`): Key Vault's challenge names it, and azidentity refuses a
token for another tenant. `fakearm`
accepts a token whose client and subject are listed in its `--trust`
(`fakevault-trust`): add `<client ID>=<subject>` there. Not done here:
installing ASO itself in the install test (its chart and image are
upstream's); the catalog's chart tests do that.

## Where the fakes differ from Azure

- Resource Manager's computed properties (`pkg/fakecloud/arm/computed.go`):
  each is derived from the resource's ID, so it is the same on every PUT
  and in every run, and a value the body gives is kept. Azure allocates
  some of them (a public IP's address, an identity's principal ID) and may
  change or release them; a Basic SKU's dynamic address appears only once
  it is attached, which the fake does not wait for. Key Vault keys
  (`vaultkeys.go`) have one version each, no cryptographic operations, no
  soft-deleted state to recover or purge, and a modulus that is not a real
  key's; any accepted token may do anything (no access policies or RBAC).
  A site's `customDomainVerificationId` is one per subscription, as Azure's,
  but not Azure's value; a site's default host name is the classic
  `<site>.azurewebsites.net`, never the regional unique one
  (`autoGeneratedDomainNameLabelScope`). A Cosmos DB account's locations are
  all read locations, and the priority 0 one (or every one, with
  `enableMultipleWriteLocations`) the write locations; no failover is
  modelled.

- App Service: a site's (and a slot's) default host name binding,
  `<site>.azurewebsites.net`, is made by Azure with the site and is never
  deleted on its own; the fake deletes it on a DELETE. Found live, run
  `ax12160408f5` (`docs/live-runs.md`).
- App Service app settings and connection strings (`web.go`): PUT replaces
  the set and `POST .../list` reads it; a GET or DELETE of a section is
  refused with 405, which is the fake's reading of the 2026-07-15 spec (it
  has neither), not checked live. A connection string's type is checked
  against the spec's enum. Not modelled: a `Site` or slot PUT whose
  `siteConfig.appSettings` or `siteConfig.connectionStrings` Azure writes to
  the same sections (the fake keeps the site's body apart, so two writers
  never show as fighting); Key Vault references in values
  (`@Microsoft.KeyVault(...)`), which Azure resolves and the fake keeps as
  text; locks on a section (a `ReadOnly` lock refuses the POST in Azure).

- Resource Manager's `systemData`: the fake writes `createdAt` when a
  resource is made, keeps it on every later PUT, and sets
  `lastModifiedAt` on each PUT; it does not write `createdBy`,
  `createdByType` or their `lastModified` pairs, and real Azure omits
  `systemData` for some resource types. The slot move test
  (`test/slotmove`) reads `createdAt` to show a resource was adopted, not
  made again; live, a resource type without `systemData` needs another
  witness (its ID with a property Azure sets once, such as a GUID).

- Microsoft Graph: only directory role assignments. Not modelled: the
  rights the caller needs (Privileged Role Administrator, or the
  application permission `RoleManagement.ReadWrite.Directory`), roles that
  cannot be scoped to an administrative unit, application scopes and
  conditions (sent and kept, not checked), custom role definitions' own
  API, Graph's replication delay (a new assignment appears in a `$filter`
  after some seconds), and the error codes and messages (the fake's own,
  close to Graph's).
- Azure SQL: no SQL Server runs (SQL Server outside Azure has no Entra
  sign-in and refuses `FROM EXTERNAL PROVIDER`). The fake interprets
  exactly the statements ASO's Azure SQL User sends and refuses anything
  else with a syntax error (102), so a change in what the operator sends
  fails a test. It does not show: Entra resolving a display name (the
  fake's directory is a list of names), Entra group administrators, object
  IDs, permissions finer than administrator or not, TLS 1.3 or TDS 8.0
  (strict encryption), or Azure's error texts beyond those in its
  documentation.

- Entra checks nothing about a client assertion but its subject: a real
  tenant also checks the federated credential (issuer, subject, audience).
- The `pgaadauth` stand-ins record the principal in the role's comment
  (Azure: a security label), and give the caller ADMIN OPTION on the new
  role (needed on PostgreSQL 16 for a CREATEROLE administrator to alter or
  drop it; not documented for Azure).
- Schema and default privileges run on the real PostgreSQL 17, so
  PostgreSQL's own rules apply (since 16, a CREATEROLE administrator holds
  only ADMIN OPTION on the roles it creates). What the fake does not show:
  whether Azure's administrator holds ADMIN OPTION on roles Azure's own
  `pgaadauth` creates (the stand-ins give it), who owns a database ARM
  creates (here `azure_pg_admin`), and PostgreSQL before 16.
- Database ownership (`ownedDatabases`, `hfl_owner`) also runs on the real
  PostgreSQL. What the fake does not show: whether Azure lets an
  administrator grant a role to `azure_pg_admin` (a role Azure manages),
  and whether an Entra administrator's membership in it carries INHERIT and
  SET as the fake's does (created `IN ROLE azure_pg_admin`).
- Resource Manager does not model long-running operations (every PUT and
  DELETE finishes in the response; virtual machine backup, below, is the
  exception), provisioning that takes time or fails,
  names ARM refuses, conflicts between a child and its parent's state, or
  the properties of any resource type: a Dapr component the real service
  would reject for a bad `componentType` is accepted. The generated kinds
  are proven to send what the specs describe, not that Azure accepts it.
- Recovery Services backup of a virtual machine (`VaultsProtectedItem`,
  `pkg/fakecloud/arm/protecteditems.go`; ASOQ Q14, builder, 2026-11-15),
  after the 2026-07-01 backup spec (`bms.json` and its examples
  `ConfigureProtection.json`, `StopProtection.json`, `ProtectedItem_Delete.json`,
  `RefreshContainers.json`) and Azure's REST guide for backing up a VM.
  What the fake shows: `backupFabrics/Azure` exists under every vault and
  is no resource; a container refresh (`POST .../refreshContainers`, 202
  and a `Location` that answers 202 once, then 204) makes the vault see the
  subscription's machines, and a PUT for a machine it has not seen is
  refused with 404; the container and item names must be the machine's
  (`iaasvmcontainer;iaasvmcontainerv2;<rg>;<vm>`, `vm;iaasvmcontainerv2;<rg>;<vm>`);
  the policy must be a backup policy of the same vault (required unless
  `protectionState` is `ProtectionStopped`); a machine protected by another
  vault is refused; Azure registers the machine's container itself, so the
  fake does; the PUT and the DELETE are long-running (202 with an
  `Azure-AsyncOperation` that answers `InProgress` once, then `Succeeded`);
  the item reads `IRPending` until a first backup; the container goes with
  the machine's last item; and a vault that holds an item cannot be
  deleted. What it cannot show: backups themselves (no recovery points, no
  `lastBackupTime`, the state never leaves `IRPending`), soft delete (Azure
  keeps a deleted item's data soft-deleted for the vault's retention days,
  14 by default, and the vault cannot be deleted until it is purged or
  expires), whether Azure really refuses a PUT without the refresh (the
  REST guide makes it the first step; the reconciler always refreshes
  before an item's first PUT, so either way works), the rights Azure needs
  (Backup Contributor on the vault, Virtual Machine Contributor on the
  machine), that the vault must be in the machine's region, item types
  other than virtual machines (file shares, SQL and SAP HANA in a VM), how
  long Azure takes (minutes), and Azure's exact error codes for these
  refusals (the codes are the fake's).
- Management groups (`Microsoft.Management/managementGroups`, `.../subscriptions/<id>`,
  `.../settings/default`) are stored like any resource, plus a hierarchy: the
  tenant root (`/`) is always a valid parent; a group's `properties.details.parent.id`
  must name an existing group and not a descendant; a group with a child group
  or a subscription cannot be deleted (409); a subscription is in one group at
  a time. The tenant root group (`.../managementGroups/<tenant ID>`, `fakearm`'s
  tenant) is there from the start, as Azure makes it with the tenant: a group
  may name it as its parent, its hierarchy settings (`.../settings/default`)
  can be written, it can be renamed, and it cannot be deleted (400). Not
  modelled: a group with no parent is at the tenant root (`/`), not placed
  under the tenant root group as Azure places it; the asynchronous create and
  delete Azure returns, the
  subscriptions a group holds in its own `children`, and the error codes (the
  fake's own).
- Azure Policy (`Microsoft.Authorization/policyDefinitions`,
  `policySetDefinitions`, `policyAssignments`, `policyExemptions`;
  `pkg/fakecloud/arm/policy.go`): a definition or set definition is kept only
  at a management group or a subscription (else 400); a set's definitions and
  an assignment's definition must exist and be kept at or above where they are
  used, following the management group hierarchy (a subscription placed in a
  group is under it and every group above), else 404, as for a reference not
  there yet; a built-in (`/providers/Microsoft.Authorization/policyDefinitions/x`)
  exists only when a test adds it and then serves every scope; an exemption's
  assignment must exist (404) and the exemption must be at or under its scope
  unless `assignmentScopeValidation` is `DoNotValidate` (400). Not modelled:
  evaluation and compliance (no rule is run, nothing is denied), the
  validation of `policyRule`, `mode` and parameter types and values, deletes
  Azure refuses (a definition a set still names), definition versions, an
  assignment's managed identity, and the error codes (the fake's own).
- Policy remediations and attestations (`Microsoft.PolicyInsights/remediations`,
  `attestations`; `pkg/fakecloud/arm/policyinsights.go`): a remediation is kept
  at a management group, subscription, resource group or resource, an
  attestation at a subscription, resource group or resource (400 at a group,
  as the spec has no such path); either one's assignment must exist (404) and
  be at or above it, following the management group hierarchy (400); a
  remediation of a set assignment must name the set's definition
  (`policyDefinitionReferenceId`), and its `filters.resourceIds` cannot be
  used with `ReEvaluateCompliance` (400; both from the spec's descriptions).
  Not modelled: running a remediation (the fake's is `Succeeded` at once with
  no deployments; Azure's is `Evaluating`, then `Complete` or `Failed`),
  cancelling one, whether the definition's effect can be remediated or
  attested, an attestation changing compliance, and the error codes (the
  fake's own).
- Role definitions and Privileged Identity Management (`pkg/fakecloud/arm/pim.go`):
  four built-in roles (`Owner`, `Contributor`, `Reader`, `User Access
  Administrator`, by their real IDs) exist at every scope and are listed, with
  any role definition a test stores, by `GET {scope}/providers/Microsoft.Authorization/roleDefinitions`
  (optionally `$filter=roleName eq '...'`), which is how ASO resolves
  `wellKnownName: Reader`; a role eligibility or assignment schedule request's
  name must be a GUID, its `principalId` is required, its `requestType` is one
  of Azure's (400 each), and its role must exist (404); a request is never
  deleted (405, as Azure has no DELETE for one; ASO's reconciler, knowing that from the spec, sends none); an eligibility request
  (`AdminAssign` and the like) makes the eligibility schedule of its principal,
  role and scope and names it in `targetRoleEligibilityScheduleId`, and
  `AdminRemove` needs and removes it (404); an assignment request does the same
  with assignment schedules, `SelfActivate` needing an eligibility at or above
  its scope and `SelfDeactivate` the assignment (404); every request is
  `Provisioned` (`Revoked` for a removal), and a PUT of a request that exists
  (ASO PUTs on every reconcile) keeps that outcome and applies nothing again
  (what Azure does with a second PUT of a request name is not known here); a role management policy assignment
  is named `{policy GUID}_{role GUID}` (400) and its policy and role must exist
  (404). Not modelled: approvals, justification, ticket and MFA rules (no
  request is ever `PendingApproval`), schedule start and expiry, whether the
  principal exists, the cancel and validate actions, the role management
  policies themselves (a test stores the ones it names), and the error codes
  (the fake's own; what Azure answers to a DELETE of a request is not known
  here).
- Access reviews (`Microsoft.Authorization/accessReviewScheduleDefinitions`,
  `accessReviewHistoryDefinitions`, `accessReviewScheduleSettings`, API
  2021-12-01-preview) and the tenant's subscription policy
  (`Microsoft.Subscription/policies/default`, 2021-10-01;
  `pkg/fakecloud/arm/accessreview.go`), from the specs: the PUT body of each is
  the resource's properties themselves, which a GET returns under
  `properties` (the fake keeps them so); a schedule or history definition's
  name is a GUID (400; the API's examples and the portal make GUIDs, Azure's
  refusal of another name is not known here); default settings are named
  `default`, and the subscription policy is the tenant's alone, at
  `/providers/Microsoft.Subscription/policies/default`, with exempted
  principals as object IDs (400 each); neither singleton can be deleted (405,
  no DELETE in the specs). Not modelled: an access review running
  (instances, decisions, reviewers notified, a history report made; the
  fake fills no instance or status), which scopes and principals Azure
  accepts, Entra ID P2 licensing, the subscription policy being enforced on
  subscriptions moving between tenants, who may write it (a global
  administrator or an exempted principal), and the error codes (the fake's
  own).
- Budgets and cost exports (`Microsoft.Consumption/budgets`,
  `Microsoft.CostManagement/exports`; `pkg/fakecloud/arm/cost.go`), each rule
  from the spec's descriptions: either is kept at a subscription, resource
  group, management group or billing scope, not on a resource (400); a budget
  has at most five notifications, thresholds from 0 to 1000, contact groups
  only at a subscription or resource group and each one existing (404), a
  contact email (or there, a group) per notification, a start on the first
  of a month from 2017-06-01 and an end after it, and at least two items in
  a filter's `and` (400 each); an export's storage account by `resourceId`
  must exist (404), or it gives `storageAccount` with a `sasToken` (400 when
  neither); a `Custom` timeframe needs a `timePeriod`, periods end after they
  start, and a managed identity needs a location (400); the SAS token is kept
  and returned obfuscated, and a system-assigned identity gets a principal
  and tenant ID. A Cost Management setting (`settings/{type}`) is tag
  inheritance, its name and `kind` both `taginheritance` (400). Not
  modelled: tags actually being inherited onto cost data, spend and notifications, an export running
  (no run history or next run time), the rules that depend on the day (a
  budget's start at most a year ahead, a recurrence starting in the future),
  the storage subscription's registration with
  `Microsoft.CostManagementExports`, what an agreement type allows, and the
  error codes (the fake's own).
- Site-to-site VPN (`Microsoft.Network/connections`,
  `localNetworkGateways`; `pkg/fakecloud/arm/vpn.go`): a connection names its
  `connectionType` and `virtualNetworkGateway1` (the spec's required
  properties) and the other side its type needs (`localNetworkGateway2` for
  IPsec, `virtualNetworkGateway2` for `Vnet2Vnet`, `peer` for ExpressRoute;
  400 when missing), each of which must exist (404); its `sharedKey` is kept
  but never returned, as the spec says Azure no longer returns it
  (`SharedKey` reads it for tests, as `GetSharedKey` does); a local network
  gateway gives its `gatewayIpAddress` or `fqdn` (400). Not modelled: a
  tunnel coming up (`connectionStatus`, bytes transferred), the gateway SKU
  and VPN type rules, overlapping address spaces, regions, provisioning
  time, and the error codes (the fake's own). An ExpressRoute connection to
  a circuit in another subscription redeems one of the circuit's
  authorization keys (below).
- ExpressRoute (`Microsoft.Network/expressRouteCircuits` with `peerings`,
  `authorizations` and `peerings/{p}/connections`, `expressRoutePorts` with
  `authorizations`; `pkg/fakecloud/arm/expressroute.go`): a circuit names
  its `sku` and a provider (`serviceProviderProperties`) or a port
  (`expressRoutePort`, which must exist, with `bandwidthInGbps`); Azure's
  keys (a circuit's `serviceKey`, an authorization's `authorizationKey`)
  are made on the first PUT and kept, whatever a PUT sends
  (`AuthorizationKey` reads them for tests); a circuit lists its peerings
  and authorizations, and a peering its Global Reach connections, from the
  children on every GET, and a PUT of the parent deletes a child its list
  leaves out (assumed of Azure, as ASO assumes for route tables); a peering
  is named for its `peeringType` and gives its VLAN, peer ASN and both
  prefixes; a Global Reach connection names its own peering, a peer peering
  that exists and a `/29`; a circuit on another subscription's port, a
  Global Reach connection to another subscription's circuit and an
  ExpressRoute gateway connection to one redeem an authorization key of
  that port or circuit (400 when missing, not its own or in use by
  another), which is then `InUse`. An ExpressRoute Direct LAG
  (`expressRouteLags`, with `authorizations`) gives its `peeringLocation`
  and `bandwidthInGbps`; its authorizations are child resources only, and a
  LAG PUT leaves them as they are. Not known: whether Azure deletes a LAG's
  authorizations on a PUT that omits the list (our `ExpressRouteLag` cannot
  send it: the spec gives an authorization no writable field), and what
  redeems one (no circuit field names a LAG). Not modelled: a provider provisioning a
  circuit, BGP sessions, ARP and route tables, the SKU and bandwidth rules,
  Microsoft peering's prefix validation and route filters, MACsec,
  deleting an authorization in use, the hours a real circuit takes, and the
  error codes (the fake's own).
- ExpressRoute gateways (`Microsoft.Network/expressRouteGateways` with
  `expressRouteConnections`, same file): a gateway names a virtual hub that
  exists (`virtualHub`, our `VirtualHub` or a bare ID), a
  hub has one ExpressRoute gateway, and scale bounds, when given, have
  1 <= min <= max; a gateway lists its connections from the children on
  every GET and a PUT without them deletes them (assumed, as for circuits); a
  connection names a circuit's `AzurePrivatePeering` that exists, and to a
  circuit in another subscription redeems one of its authorization keys, as
  above. Not modelled: scale units, the routes a hub learns, a connection's
  routing configuration checked against the hub's route tables, internet
  security and FastPath, and the time a real gateway takes.
- Log Analytics clusters, data sources and storage insight configs
  (`Microsoft.OperationalInsights/clusters`, `workspaces/dataSources`,
  `workspaces/storageInsightConfigs`; `pkg/fakecloud/arm/loganalytics.go`):
  a cluster's sku is `CapacityReservation` at one of Azure's commitment
  tiers (100 to 50,000 GB a day); a data source gives its `kind`; a storage
  insight config names a storage account that exists and gives its key,
  which is kept aside (`SharedKey`) and never returned, as Azure does not
  return it. Not modelled: linking workspaces to a cluster, data flowing,
  what each data source kind takes, and the error codes (the fake's own).
- Operations Management solutions
  (`Microsoft.OperationsManagement/solutions`;
  `pkg/fakecloud/arm/operationsmanagement.go`): a solution names a Log
  Analytics workspace that exists and gives a plan; a Microsoft gallery
  solution is named, and its plan is named, `<type>(<workspace>)`, and its
  product is `OMSGallery/<type>`; contained and referenced resources exist.
  Not modelled: which solution types exist, contained resources locked and
  deleted with the solution, third-party publishers' rules, and the error
  codes (the fake's own).
- AKS and fleet managed namespaces and node pool snapshots
  (`Microsoft.ContainerService/managedClusters/managedNamespaces`,
  `fleets/managedNamespaces`, `snapshots`;
  `pkg/fakecloud/arm/containerservice.go`): a managed namespace's name is a
  DNS-1123 label and not `kube-system`, `kube-public` or `kube-node-lease`;
  a snapshot names an agent pool that exists. Not modelled: the namespace
  in the cluster, quotas and network policies in effect, propagation to a
  fleet's members, adoption, and the error codes (the fake's own).
- Container registry tasks and connected registries
  (`Microsoft.ContainerRegistry/registries/tasks`, `connectedRegistries`;
  `pkg/fakecloud/arm/containerregistry.go`): a task gives a platform os and
  one step of type Docker (with its Dockerfile), EncodedTask or FileTask, and
  a timeout of 300 to 28,800 seconds; its tokens and custom registry
  credentials are kept aside (`SharedKey`) and never returned; a connected
  registry gives a mode and a sync token that exists, and its client tokens
  exist. Not modelled: runs, triggers firing, activation, the registry's
  SKU, and the error codes (the fake's own).
- Source control configurations and Kubernetes configuration private link
  scopes (`Microsoft.KubernetesConfiguration/sourceControlConfigurations`,
  `privateLinkScopes`; `pkg/fakecloud/arm/kubernetesconfiguration.go`): a
  configuration is on a managed or Arc-enabled cluster, gives a repository
  URL and an operator type of Flux; its protected settings are kept aside
  (`SharedKey`, under the configuration's ID and `/<name>`) and never
  returned; a scope names a cluster that exists. Not modelled: Flux in the
  cluster, the generated SSH key, compliance, connections arriving from a
  consumer, and the error codes (the fake's own).
- Activity log profiles and workbook templates
  (`Microsoft.Insights/logprofiles`, `workbooktemplates`;
  `pkg/fakecloud/arm/insights.go`): a log profile is at a subscription, one
  per subscription, with categories among Write, Delete and Action, at
  least one location, a storage account or Event Hubs rule that exists, and
  0 to 365 days; a workbook template gives its template and a gallery. Not
  modelled: the activity log flowing, the template's content, and the error
  codes (the fake's own).
- Maintenance configuration assignments
  (`Microsoft.Maintenance/configurationAssignments` at any scope;
  `pkg/fakecloud/arm/maintenance.go`): an assignment names a maintenance
  configuration that exists; on a resource, `resourceId` is that resource
  (set when left out, 400 when another); at a subscription or resource group
  (dynamic scope) it gives a `filter` and names an `InGuestPatch`
  configuration (assumed from Azure's documentation of dynamic scoping). Not
  modelled: which resource types a configuration's scope can maintain, the
  machines a filter selects, and the error codes (the fake's own).
- Managed HSMs, their keys and Key Vault private endpoint connections
  (`Microsoft.KeyVault/managedHSMs`, `managedHSMs/keys`, and
  `privateEndpointConnections` of a managed HSM or a vault;
  `pkg/fakecloud/arm/keyvault.go`): an HSM is made with a `sku`, a
  `tenantId` and at least one `initialAdminObjectIds`; soft delete cannot be
  turned off, `softDeleteRetentionInDays` is 7 to 90 and cannot change, and
  purge protection once on stays on (Azure's documentation of Managed HSM
  soft delete). A key needs its `kty`; a PUT on a key that exists returns it
  unchanged (the API's own description) and a key cannot be deleted through
  Resource Manager (no DELETE in the spec: 405). A private endpoint
  connection is made by a private endpoint, so a PUT updates one that exists
  and is refused 404 otherwise (assumed from the API's "Updates the specified
  private endpoint connection"; tests add the connection as the endpoint
  would). Not modelled: the security domain download that activates an HSM,
  regions and network rules taking effect, key versions, rotation and
  release, the private endpoint itself, and the error codes (the fake's own).
- Virtual WAN (`Microsoft.Network/virtualWans`, `virtualHubs` and a hub's
  `hubVirtualNetworkConnections`, `hubRouteTables`, `routeTables`,
  `routingIntent`, `bgpConnections`, `ipConfigurations`;
  `pkg/fakecloud/arm/virtualwan.go`): a WAN's type is Basic or Standard
  (Standard when left out); a hub names a WAN that exists and an
  `addressPrefix` in CIDR form, and keeps or deletes its `routeTables` as its
  `virtualHubRouteTableV2s` list says (assumed, as for circuits); a virtual
  network has one hub connection; a connection's associated route table, a
  BGP connection's hub connection, a route's or routing policy's next hop and
  an IP configuration's subnet exist, and the first two are the same hub's; a
  hub has one routing intent, whose policies send `Internet` or
  `PrivateTraffic`. Not modelled: the hub router and the routes it learns and
  propagates, the default and none route tables Azure makes in every hub,
  address overlap with a connected network, Basic WANs allowing only
  site-to-site VPN, the time a real hub takes, and the error codes (the
  fake's own).
- Virtual WAN VPN (`Microsoft.Network/vpnGateways` with their
  `vpnConnections` and `natRules`, `vpnSites`, `vpnServerConfigurations`,
  `p2svpnGateways`; `pkg/fakecloud/arm/vpngateway.go`): a VPN gateway and a
  point-to-site gateway each name a hub that exists, which has one of each;
  a VPN gateway keeps or deletes its connections and NAT rules as its
  `connections` and `natRules` lists say (assumed, as for circuits); a VPN
  site names a WAN that exists and gives an `ipAddress` or links, each link
  an `ipAddress` or `fqdn`; a connection names a site that exists and each
  link connection a link of that site; a NAT rule is `Static` (when left
  out) or `Dynamic`, `EgressSnat` or `IngressSnat`, with internal and
  external mappings; a server configuration gives its authentication types
  and what each needs (root certificates, a RADIUS server, an AAD tenant); a
  point-to-site gateway names a server configuration that exists and a
  client address pool per connection configuration. Link connections' shared
  keys and the RADIUS secret are kept but never returned (`SharedKey` reads
  them for tests); a site's `siteKey` is kept as sent. Not modelled: tunnels
  and BGP sessions coming up, the gateway's public IP addresses, scale
  units, NAT applied to traffic, a hub refusing deletion while it holds a
  gateway, client VPN profiles, the hour a real gateway takes, and the
  error codes (the fake's own).
- Virtual Network Manager (`Microsoft.Network/networkManagers` with their
  `scopeConnections`, `networkGroups` and a group's `staticMembers`, their
  configurations, rule collections and rules, `commits`, and
  `networkManagerConnections` at a subscription or a management group;
  `pkg/fakecloud/arm/networkmanager.go`): a network manager gives
  `networkManagerScopes` with at least one subscription or management group,
  each its ARM ID; a network manager connection names a network manager that
  exists; a scope connection gives a `tenantId` and a subscription's or
  management group's `resourceId`, and stays `Pending`; a static member names
  a resource that exists, a virtual network or a subnet as its group's
  `memberType` says. Connectivity configurations are `HubAndSpoke` (with
  hubs that exist) or `Mesh`; a connectivity configuration and a rule
  collection apply to network groups of the same manager that exist; an
  admin rule is `Custom` (protocol, direction, access `Allow`, `Deny` or
  `AlwaysAllow`, priority 1 to 4096) or `Default` (a flag); a security user
  rule gives protocol and direction. A routing rule collection applies to
  network groups as above; a routing rule gives a destination (type
  `AddressPrefix` or `ServiceTag` and its address) and a next hop type,
  with an address for `VirtualAppliance`. A commit's `commitType` must be
  in the manager's `networkManagerScopeAccesses`, it needs target
  locations, and each configuration it names is one of that type in the
  same manager (400) that exists (404); its target locations become its
  active locations at once. Not modelled: configurations reaching the
  networks (a commit is only recorded), a commit's time and per-region
  failures, configurations a commit leaves out being removed, the rules a `Default`
  rule's flag names, address prefixes checked, two rules with one priority,
  a scope managed by two network managers for the same feature, the other tenant accepting a scope connection, a
  network manager connection's state, dynamic membership through Azure
  Policy, a static member's region, and the error codes (the fake's own).
- Network Watcher connection monitors and application security group
  address prefix sets (`networkWatchers/connectionMonitors`,
  `applicationSecurityGroups/addressPrefixSets`, 2026-03-01;
  `pkg/fakecloud/arm/networkwatcher.go`), from the spec: a (v2) monitor has
  a test group, and each test group's sources and destinations name the
  monitor's own endpoints and its test configurations the monitor's own
  (400); an endpoint's `resourceId`, and a v1 monitor's source, must exist
  (404); an address prefix set has at least one prefix, each CIDR (IPv4 or
  IPv6, 400). Not modelled: a monitor running (no checks or results), the
  Network Watcher agent, endpoint filters and scopes, the watcher's region,
  a prefix set taking effect in security rules, and the error codes (the
  fake's own). A rule collection group's draft
  (`ruleCollectionGroups/{g}/ruleCollectionGroupDrafts/default`) is kept as
  sent: deploying a draft is a POST on the policy, not faked.
- A Service Bus or Event Hubs namespace's network rule set and
  geo-disaster recovery alias (`Microsoft.ServiceBus` and
  `Microsoft.EventHub` `namespaces/networkRuleSets/default` and
  `disasterRecoveryConfigs`, 2026-01-01; `pkg/fakecloud/arm/messaging.go`):
  the one rule set is named default (404 otherwise) and has no DELETE (405);
  an IP rule's mask is an IPv4 address or CIDR range (400); a virtual
  network rule names a subnet (400) that exists (404); an alias names a
  partner namespace (400) that exists (404) and is another namespace of the
  same service (400); a namespace is in one pairing at most (409). An Event
  Hubs application group names its clients as `SASKeyName=<key>` or
  `AADAppID=<GUID>` (400) with policies of unique names (400); schema groups
  and dedicated clusters are kept as sent. The namespaces' private endpoint
  connections follow `privateendpoints.go`.
  Not modelled: the tiers each takes (private endpoints and pairing are
  Premium's), the partner's region, the secondary's copy of the
  alias, breaking the pairing and failover, and the error codes.
- Event Grid system topics, event subscriptions and MQTT broker children
  (`Microsoft.EventGrid`, 2025-02-15; `pkg/fakecloud/arm/eventgrid.go`): a
  system topic names a source (400) that exists (404) and a topic type
  (400), one system topic a source (409); a system topic's or namespace
  topic's event subscription has a destination (400), one named by
  resource ID exists (404), a webhook's URL is https (400), and a namespace
  topic's delivers by Queue or Push with a destination (400); a topic space
  has one to ten templates, a client group a query, a CA certificate its
  certificate (400); a permission binding is Publisher or Subscriber (400)
  and names a topic space and client group of its namespace that exist
  (404). Not modelled: events, the region a system topic shares with its
  source, filter semantics, a webhook's validation handshake, MQTT clients
  connecting, whether the namespace's broker is on, and the error codes.
- API Management operations, policies, tags, schemas and providers
  (`Microsoft.ApiManagement/service/...`, 2024-05-01;
  `pkg/fakecloud/arm/apimanagement.go`): each kind sends what the spec
  requires of it (400); an operation's URL template names only its
  template parameters (400); an operation's policy in the xml or rawxml
  format is XML with a `<policies>` root (400); an API's, product's or
  operation's tag names a tag of the service that exists (404); an identity
  provider is named by its type (400); a cache's Redis, when named, exists
  (404). Not modelled: the gateway serving the APIs, policy semantics, the
  developer portal, the providers' endpoints answering, and the error codes.
- Virtual network TAP (`virtualNetworkTaps` and
  `networkInterfaces/tapConfigurations`, 2026-03-01;
  `pkg/fakecloud/arm/vnettap.go`): a TAP has one destination, a NIC's IP
  configuration or a load balancer's frontend IP configuration, not both
  (400), which exists (404), and a VXLAN `destinationPort` of 1 to 65535
  (400); a tap configuration names a TAP (400) that exists (404); a TAP is
  returned with `networkInterfaceTapConfigurations` listing the tap
  configurations that name it. Not modelled: traffic being mirrored, the
  destination's virtual network (Azure wants the TAP's or a peered one), the
  VM sizes and NICs that can be tapped, whether Azure refuses deleting a TAP
  still named by tap configurations, and the error codes (the fake's own).
- Defender for Cloud governance rules (`Microsoft.Security/governanceRules`,
  2022-01-01-preview; `pkg/fakecloud/arm/governance.go`), from the spec: a
  rule's key (its name) is a GUID, its priority from 0 to 1000 and unique at
  its scope, and it gives a condition set (400 each). Not modelled: owners
  assigned to recommendations or notified, the condition sets' content
  (free-form in the spec), excluded and member scopes, which scopes Azure
  takes, and the error codes (the fake's own). Alerts suppression rules
  (`alertsSuppressionRules`, 2019-01-01-preview) are kept as sent: no alert
  is suppressed, and an expiry date in the past is not refused (Azure's rule
  depends on the day).
- Defender for Cloud custom assessments (`Microsoft.Security/assessmentMetadata`,
  `assessments`, 2025-05-04; `pkg/fakecloud/arm/assessments.go`): a
  metadata's key (its name) is a GUID (400); an assessment is named after
  metadata that exists in its resource's subscription (404; the fake has no
  built-in assessments) and gives a status code (400). Not modelled: built-in
  metadata, the secure score, an assessment showing or expiring, and the
  error codes. Defender private links and SQL vulnerability assessment
  settings and baseline rules are kept as sent (no scan runs).
- Cosmos DB role definitions and assignments of the Cassandra, Gremlin,
  MongoDB (Entra) and Table APIs (2026-03-15; `pkg/fakecloud/arm/cosmosrbac.go`):
  names are GUIDs (400); an assignment names a definition of the same API
  in the same account (400) that exists (404), scoped to the account or a
  path under it (400). Not modelled: built-in definitions, the data plane
  enforcing anything, the principal existing, and the error codes.
- Azure Compute Gallery and dedicated hosts (`Microsoft.Compute/galleries`
  and children, 2026-03-03; `hostGroups`, 2025-04-01;
  `pkg/fakecloud/arm/gallery.go`): a gallery's name has letters, digits,
  dots and underscores only; a version's name is `X.Y.Z`; an image version
  names a source that exists (404) and an application version its package's
  `mediaLink`, a script version its `scriptLink`; a restore point
  collection's source VM exists (404); a host group has 1 to 3 fault
  domains (400 each). Not modelled: replication to regions (a version is
  ready at once), OS state and generation matching, script parameters,
  access control rules being enforced, restore points being taken, host
  SKU availability, quota, and the error codes.
- Static Web Apps' children and an App Service app's virtual network
  connections (`Microsoft.Web/staticSites/*`, `sites/*/virtualNetworkConnections`,
  2026-07-15; `pkg/fakecloud/arm/staticsites.go`): a linked backend, a
  user-provided function app, a database connection and a network
  connection name a resource that exists (404). Not modelled: custom domain
  validation (a domain is Ready at once), builds, environments, the
  repository and deployment token, one backend per site, SKUs, and the
  error codes.
- An App Service app's and slot's sidecar containers, private access and a
  slot's virtual network connections (`Microsoft.Web/sites/*/sitecontainers`,
  `.../privateAccess/virtualNetworks`, the same under `sites/*/slots`,
  2026-07-15; `pkg/fakecloud/arm/sitechildren.go`): a container gives its
  image and `isMain` (400), an app or slot has one main container (409);
  private access and a slot's network connection name networks that exist
  (404). Not modelled: images being pulled and containers started, registry
  credentials, the Linux `sitecontainers` mode a sidecar app needs,
  functions being deployed or run, premier add-ons being bought, and the
  error codes.
- App Service Environment v3 (`Microsoft.Web/hostingEnvironments` and its
  `configurations/customdnssuffix`, 2026-07-15;
  `pkg/fakecloud/arm/hostingenvironments.go`): an environment names a
  network that exists (400 when it names none, 404); a custom DNS suffix
  comes with its Key Vault certificate URL, and the other way round (400).
  Not modelled: the subnet's delegation and size, the hours an environment
  takes, the certificate being read, the addresses Azure assigns, and the
  error codes.
- The private endpoint connections of an Azure SQL server, a container
  app, a disk access, an Azure Cache for Redis cache, a Redis Enterprise
  cluster, an App Configuration store, a PostgreSQL flexible server, a
  MongoDB vCore cluster, an App Service app or slot, a static web app, an
  App Service Environment and a SQL managed instance (`.../privateEndpointConnections/*`; `pkg/fakecloud/arm/privateendpoints.go`):
  a private endpoint makes a connection, so a PUT for one that does not
  exist is 404 and a PUT changes one that does (approve, reject). Not
  modelled: the private endpoint (a test adds the connection to the fake as
  one would), the states Azure allows a move between (a rejected connection
  cannot be approved again), and the error codes (the fake's own).
- Azure SQL Managed Instance children and instance pools
  (`Microsoft.Sql/managedInstances/*`, `instancePools`, 2025-01-01;
  `pkg/fakecloud/arm/sqlmanagedinstance.go`): a Key Vault key gives its URI
  (400); a Key Vault encryption protector names a key of its instance that
  exists (404); a vulnerability assessment, the instance's or a database's,
  gives its container and a key or SAS (400); Entra-only authentication is
  turned on only after a Microsoft Entra administrator (400); a pool's
  subnet exists (404); a private endpoint makes the instance's connections.
  Not modelled: Key Vault being read, scans, schedules stopping the
  instance, the SQL Server end of a distributed availability group, DTC
  and SQL Agent, pool capacity, and the error codes.
- Azure SQL Microsoft support operations auditing and workload groups
  (`Microsoft.Sql/servers/*/devOpsAuditingSettings/Default`,
  `.../databases/*/workloadGroups/*`, 2025-01-01;
  `pkg/fakecloud/arm/sqlserverextras.go`): Enabled auditing names a storage
  endpoint or Azure Monitor (400); a workload group's minimum share is no
  more than its cap, both 0 to 100 (400). Not modelled: workload groups
  needing a dedicated SQL pool, masking being applied, maintenance windows
  being kept, ledger digests being uploaded, baselines matching scans, and
  the error codes.
- Azure Cosmos DB for PostgreSQL clusters
  (`Microsoft.DBforPostgreSQL/serverGroupsv2/*`, 2022-11-08;
  `pkg/fakecloud/arm/cosmospostgres.go`): a new cluster that is no replica
  or restore has an administrator password, a coordinator edition, storage
  and vCores and a node count (400); a node count is never 1 (400); a
  replica's or restore's source cluster exists (404); a firewall rule has
  IPv4 addresses (400; the CRD's pattern refuses others first); a role has
  a password and a configuration a value (400); a private endpoint makes a
  connection (`privateendpoints.go`). Not modelled: the servers a cluster
  makes, its state, high availability, replicas kept in step, a
  configuration's name being a server parameter, and the error codes.
- Azure Cosmos DB fleets and MongoDB vCore cluster users
  (`Microsoft.DocumentDB/fleets/*`, 2026-03-15, and
  `mongoClusters/*/users/*`, 2026-06-01; `pkg/fakecloud/arm/cosmosfleet.go`):
  a fleetspace's pool has its minimum at or below its maximum (400); a
  fleetspace account names a Cosmos DB account (400) that exists (404); a
  cluster user is named by an Entra object ID (400) and has an identity
  provider (400). Not modelled: throughput being pooled, regions matching,
  an account in one fleetspace only, the principal existing, roles being
  granted in the cluster, and the error codes.
- Storage connectors, data shares and blob access points
  (`Microsoft.Storage/storageAccounts/*/{connectors,dataShares,blobAccessPointConfigurations}/*`,
  2026-06-01 and 2026-09-01; `pkg/fakecloud/arm/storagenewcomers.go`): a
  connector's and an access point's source type cannot change (400); a new
  data share has its access policies and assets (400). Not modelled: the
  connection test, data being shared or read through a connector or an
  access point, capacity metrics being emitted, context caches caching, and
  the error codes.
- Container Apps sandbox groups and Azure SRE Agent agents
  (`Microsoft.App/sandboxGroups/*`, `Microsoft.App/agents/*`, 2026-07-01;
  `pkg/fakecloud/arm/appextras.go`): a sandbox group's environment link
  cannot be changed or removed once set (409); the environment it links and
  the subnet its network connection names exist (404; a connection names
  one, 400); an agent's subnet and AzureVNet sandbox egress come together
  (400). Not modelled: sandboxes running, the agent acting, its connectors
  reaching their data sources, and the error codes.
- Recovery Services vault settings (`vaults/*/backupconfig/vaultconfig`,
  `backupstorageconfig/vaultstorageconfig`,
  `backupEncryptionConfigs/backupResourceEncryptionConfig`, 2026-07-01;
  `pkg/fakecloud/arm/vaultsettings.go`): soft delete set AlwaysON stays on
  and its retention is 14 to 180 days (400); the storage redundancy cannot
  change once the vault holds a protected item (400); customer-managed
  encryption names its key and cannot go back to Microsoft-managed keys
  (400). Not modelled: enhanced security and multi-user authorization,
  cross-region restore taking effect, the key being reachable, and the
  error codes.
- DevTest Labs (`Microsoft.DevTestLab/labs/*`, 2018-09-15;
  `pkg/fakecloud/arm/devtestlab.go`): a lab's schedules are named
  `LabVmsShutdown` (`LabVmsShutdownTask`) or `LabVmAutoStart`
  (`LabVmsStartupTask`) and a lab VM's is `LabVmsShutdown` (400); a lab's
  cost settings are named `targetCost` (400); a lab virtual network names a
  virtual network that exists (404); a lab VM names a virtual network of
  its own lab that exists (404) and one of its lab subnets (400), and has a
  password, or an SSH key when it signs in with one (400); a user's secret
  has a value (400). Not modelled: VMs, disks and environments being made
  (in the lab's resource group or their own), artifacts being applied,
  schedules running, the lab's policies, and the error codes.
- Azure Site Recovery, Azure to Azure (`vaults/*/replication*`, 2026-07-01;
  `pkg/fakecloud/arm/siterecovery.go`): an Azure fabric names its region
  (400) and a vault has one Azure fabric per region (409); an A2A policy's
  app-consistent frequency is at most its recovery point retention (400); a
  protection container mapping names a policy and a target container that
  exist (404), not its own container (400); a replicated item names a
  policy that exists (404), and for A2A the VM, recovery container and
  recovery resource group exist (404) and its container maps to that
  recovery container with that policy (400); a recovery plan's fabrics and
  replicated items exist (404). Not modelled: replication running (initial
  replication, recovery points, health), failover and the other POST
  actions, the mobility agent, the on-premises scenarios beyond the generic
  resource, and the error codes.
- App Service leftovers (`sites/*/certificates`, `virtualNetworkConnections/*/gateways`,
  `staticSites/*/basicAuth`, `sites/*/slots/*/sourcecontrols/web`,
  `kubeEnvironments`, 2026-07-15; `pkg/fakecloud/arm/webextras.go`): an
  app's or slot's certificate comes from a PFX with its password, a Key
  Vault secret, or a host name (400); an integration's gateway is named
  `primary` (400); a static web app's basic authentication is named
  `default`, is refused on the Free plan, names its scope (and the
  environments for `SpecifiedEnvironments`) and has a password or secret
  URL (400); a slot's source control names its repository (400); a
  Kubernetes environment names a custom location (400) that exists (404).
  Not modelled: certificates read or issued, the VPN package, basic
  authentication taking effect, deployments, the Arc cluster, AI gateways,
  and the error codes.
- Logic Apps API connections, custom APIs and on-premises data gateways
  (`Microsoft.Web/connections`, `customApis`, `connectionGateways`,
  2016-06-01; `pkg/fakecloud/arm/logicconnections.go`): a connection names
  its API (400), a managed API in the connection's region (400) or a custom
  API that exists (404), and its `parameterValues` and
  `customParameterValues` are kept aside (`SharedKey`, under the
  connection's ID and `/parameterValues/<name>`) and never returned; a
  custom API carries an OpenAPI document, an OpenAPI URL or a WSDL (400),
  and an OAuth connection parameter's client secret is kept aside and never
  returned; a gateway names a gateway installation that exists (404). Not
  modelled: OAuth consent and a connection's status, connections reaching
  their target, the managed API catalogue (any name is accepted), the
  gateway's machine, and the error codes.
- Application Insights smart detection rules
  (`Microsoft.Insights/components/ProactiveDetectionConfigs`,
  2018-05-01-preview; `pkg/fakecloud/arm/smartdetection.go`): a rule is one
  Application Insights defines (`slowpageloadtime` and the others its
  documentation names; 400), and has no DELETE (405). Not modelled: the
  rules running, mail, a rule's definition (Azure's own), and the error
  codes.
- Alert processing rules (`Microsoft.AlertsManagement/actionRules`,
  2021-08-08; `pkg/fakecloud/arm/alertprocessing.go`): a rule is Global
  (400), applies to at least one scope that exists (400, 404), and has
  exactly one action: one to five action groups that exist, or removing
  them all (400, 404); a schedule ends after it starts, and a weekly or
  monthly recurrence names its days (400). Not modelled: alerts being
  processed, time zone names, and the error codes.
- Azure Monitor pipeline groups (`Microsoft.Monitor/pipelineGroups`,
  2026-04-01; `pkg/fakecloud/arm/monitorpipeline.go`): a group runs on a
  custom location (400) that exists (404); its receivers, processors and
  exporters have unique names and a receiver or exporter carries its type's
  settings (400); its pipelines name only what it declares (400). Not
  modelled: the pipeline running, data reaching the data collection rule,
  persistence, TLS certificates, and the error codes.
- A VM's diagnostic run commands
  (`virtualMachines/*/diagnosticRunCommands`, 2026-04-01;
  `pkg/fakecloud/arm/diagnosticruncommands.go`): the source is exactly one
  of a script, a script URI, a command ID or a gallery script version
  (400), and a gallery script version exists (404). Not modelled: the
  script running, its output, the run-as account, and the error codes. A
  VM's ordinary run commands are not checked.
- Microsoft Foundry managed networks and outbound rules
  (`Microsoft.CognitiveServices/accounts/*/managedNetworks/*`, 2026-09-01;
  `pkg/fakecloud/arm/foundry.go`): a managed network is named `default`
  (400) and does not go back from V2 to V1 (400); a private endpoint rule's
  target exists (404). Not modelled: the network being provisioned (its
  firewall and endpoints), agents running, RAI topics training, tool labels
  being applied, and the error codes.
- Azure SQL Elastic Jobs (`Microsoft.Sql/servers/jobAgents/*`,
  2025-01-01; `pkg/fakecloud/arm/sqljobs.go`): a job agent names a job
  database that exists (404); a credential has a username and a password
  (400); a job step names a target group, and any credential, that exist
  (404) in its own job agent (400). Not modelled: jobs running (schedules,
  executions, outputs), target group members being resolved, the job
  database's schema, SKUs and managed identities, and the error codes.
- Log Analytics summary rules (`workspaces/summaryLogs`, 2025-07-01;
  `pkg/fakecloud/arm/loganalytics.go`): a rule's definition gives a query
  and a bin size of 20, 30, 60, 120, 180, 360, 720 or 1440 minutes (400).
  Not modelled: the rule running, its destination table being made, and
  the query being valid KQL.
- Microsoft Sentinel settings and the workspace manager
  (`Microsoft.SecurityInsights/settings`, `workspaceManager*`,
  2025-10-01-preview; `pkg/fakecloud/arm/sentinel.go`): a setting is named
  after its kind (`Anomalies`, `EntityAnalytics`, `EyesOn`, `Ueba`; 400); a
  member's target workspace, a group's members, an assignment's group and
  its items must exist (404). Not modelled: settings taking effect, the
  configuration's mode gating the rest, content being published to members
  (no job fields), members in other tenants, and the error codes (the
  fake's own). Content metadata and source controls (2025-09-01) are kept
  as sent: no repository is reached and no OAuth code exchanged.
- Resource Manager checks no role: real Azure lets only Owner and User
  Access Administrator (or a custom role with
  `Microsoft.Authorization/locks/*`) write locks, so ASO's identity may
  lack it. Its resources are bare IDs; nothing below a lock checks the
  resource type, and a missing scope is a plain 404 `ResourceNotFound`.
- Key Vault certificates: only PEM import is faked (no PFX, no issuance
  policy, no key properties); the key beside a certificate is an ID only;
  the secret beside it can be written or deleted directly, which Key Vault
  refuses for a certificate's secret.
- Key Vault's recover and purge finish at once (Azure: asynchronously, so
  the manager requeues after a recover); there is no Key Vault RBAC or
  access policy beyond the accepted tokens.
