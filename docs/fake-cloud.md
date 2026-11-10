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
| Entra | an `http.RoundTripper` answering instance discovery, OpenID metadata and `/oauth2/v2.0/token`; azidentity's own credentials run against it, through ASO's `TokenCredentialProvider` hook. A token it issues reads as the tenant, client ID, subject (the client assertion's `sub`, or a digest of the client secret) and audience | `pkg/fakecloud/entra` |
| Key Vault | the data plane, REST 7.5. Secrets: set, get (latest or a version), update attributes, soft delete, get deleted, recover, purge. Certificates: PEM import (refused unless it holds a certificate and the key that matches it), get, update attributes, soft delete, get deleted, recover, purge, each with the secret of the same name and version Key Vault keeps (the PEM as its value). Soft delete is always on; requests carry a bearer token the test decides to accept | `pkg/fakecloud/keyvault`, `cmd/fakevault` |
| Azure Database for PostgreSQL | PostgreSQL 17 in Docker (tests) or a pod (kind), TLS on, with `azure_pg_admin`, a password administrator, Entra administrators whose password is the fake token of the identity that may sign in as them, and stand-ins for `pgaadauth_create_principal` and `pgaadauth_create_principal_with_oid` | `pkg/fakecloud/postgres` |
| Azure SQL Database | a TDS 7.4 server over TLS (go-mssqldb runs unchanged against it) with the database users and fixed-role memberships ASO's statements change: sign-in with the SQL administrator's password, a contained user's password, or an access token that signs in as an Entra principal (the server's Entra administrator, or a user created `FROM EXTERNAL PROVIDER`); only an administrator changes users; only a token connection creates Entra users (33159), and only for a name in its directory (33130); names compare without case | `pkg/fakecloud/azuresql`, `cmd/fakesql` |
| Resource Manager | a store of resources by ARM ID (PUT with the parent existing, GET, DELETE; the body is kept as sent with `id`, `name`, `type` and `provisioningState: Succeeded` added, no properties checked but the management group hierarchy's, Azure Policy's, Privileged Identity Management's, budgets', cost exports', site-to-site VPN's and ExpressRoute's (circuits, ports, gateways), Virtual WAN's (hubs, VPN gateways), Virtual Network Manager's and maintenance assignments' references and scopes; built-in role definitions listed by name; ASO's own ARM client, for the generated kinds, talks to it) and management locks (`Microsoft.Authorization/locks`, 2020-05-01, level, notes and owners kept as sent) at any scope that exists; a lock covers its scope and everything under it: `CanNotDelete` refuses deletes (and deleting a scope that holds a locked resource), `ReadOnly` also refuses writes, each with 409 `ScopeLocked`; locks themselves can always be changed; requests carry a bearer token the test decides to accept | `pkg/fakecloud/arm`, `cmd/fakearm` |
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

## Where the fakes differ from Azure

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
  DELETE finishes in the response), provisioning that takes time or fails,
  names ARM refuses, conflicts between a child and its parent's state, or
  the properties of any resource type: a Dapr component the real service
  would reject for a bad `componentType` is accepted. The generated kinds
  are proven to send what the specs describe, not that Azure accepts it.
- Management groups (`Microsoft.Management/managementGroups`, `.../subscriptions/<id>`,
  `.../settings/default`) are stored like any resource, plus a hierarchy: the
  tenant root (`/`) is always a valid parent; a group's `properties.details.parent.id`
  must name an existing group and not a descendant; a group with a child group
  or a subscription cannot be deleted (409); a subscription is in one group at
  a time. Not modelled: the tenant root group (a group with no parent is just at
  the root), the asynchronous create and delete Azure returns, the
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
  and tenant ID. Not modelled: spend and notifications, an export running
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
  another), which is then `InUse`. Not modelled: a provider provisioning a
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
- AKS and fleet managed namespaces and node pool snapshots
  (`Microsoft.ContainerService/managedClusters/managedNamespaces`,
  `fleets/managedNamespaces`, `snapshots`;
  `pkg/fakecloud/arm/containerservice.go`): a managed namespace's name is a
  DNS-1123 label and not `kube-system`, `kube-public` or `kube-node-lease`;
  a snapshot names an agent pool that exists. Not modelled: the namespace
  in the cluster, quotas and network policies in effect, propagation to a
  fleet's members, adoption, and the error codes (the fake's own).
- Maintenance configuration assignments
  (`Microsoft.Maintenance/configurationAssignments` at any scope;
  `pkg/fakecloud/arm/maintenance.go`): an assignment names a maintenance
  configuration that exists; on a resource, `resourceId` is that resource
  (set when left out, 400 when another); at a subscription or resource group
  (dynamic scope) it gives a `filter` and names an `InGuestPatch`
  configuration (assumed from Azure's documentation of dynamic scoping). Not
  modelled: which resource types a configuration's scope can maintain, the
  machines a filter selects, and the error codes (the fake's own).
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
  `scopeConnections`, `networkGroups` and a group's `staticMembers`, and
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
  rule gives protocol and direction. Not modelled: configurations reaching
  the networks (nothing is deployed until committed), the rules a `Default`
  rule's flag names, address prefixes checked, two rules with one priority,
  a scope managed by two network managers for the same feature, the other tenant accepting a scope connection, a
  network manager connection's state, dynamic membership through Azure
  Policy, a static member's region, and the error codes (the fake's own).
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
