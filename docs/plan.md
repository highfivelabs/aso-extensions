# Plan

Status, 2026-11-09: the repository has four kinds,
`dbforpostgresql.azure.hfl.dev/v1` `User` (ASO's PostgreSQL user with
Microsoft Entra principals, per-database privileges, schema and default
privileges, and database ownership),
`keyvault.azure.hfl.dev/v1` `VaultSecret` and `VaultCertificate`, and
`authorization.azure.hfl.dev/v1` `ManagementLock`, the first plain ARM
kind (section 1: ARM resources are in scope). All pass their end-to-end tests against a fake
cloud, and the install runs on kind (section 7). It has no
remote, no published image and no release. This document says how Azure
Service Operator is built, how a kind is built here, which gaps come next,
how identity works, and what is left.

## 1. How Azure Service Operator v2 is built

Read at the pin in `UPSTREAM.md` (main, 16 commits after v2.22.0).

- **Generated from the Azure REST API specs.** ASO's generator
  (`v2/tools/generator`) reads the OpenAPI specs of
  `Azure/azure-rest-api-specs` (a git submodule at `v2/specs/`) and writes,
  per ARM resource and API version, the Kubernetes types, an ARM wire type,
  storage versions with conversions between them, webhooks and registration
  (`api/<group>/<version>/`). Which resources it emits is configuration:
  `v2/azure-arm.yaml` and one `v2/azure-arm/<group>.yaml` per group, where a
  resource is exported with `$exportAs` and `$supportedFrom`, and shaped
  with `typeFilters`, `typeTransformers` and per-property settings. At the
  pin about 628 resource versions are exported across 50 groups. The
  generator's package template hard-codes the group domain
  (`{{.GroupName}}.azure.com`, `tools/generator/internal/astmodel/package_definition.go`).
- **One generic ARM reconciler.** Every generated kind is reconciled by the
  same code (`internal/reconcilers/arm`): it resolves the owner chain to an
  ARM ID, PUTs the resource, follows the long-running operation, GETs the
  status back, and DELETEs on deletion, through `genericarmclient`. Per-kind
  behaviour that the specs cannot express goes in hand-written
  "extensions" (`api/<group>/customizations/`), such as `VaultKey`'s
  data-plane delete.
- **Hand-written reconcilers for what ARM does not reach.** Database users
  (MySQL, PostgreSQL, Azure SQL) and Entra objects have no ARM resource;
  ASO reconciles them with hand-written reconcilers (`internal/reconcilers/{mysql,postgresql,azuresql,entra}`)
  on the same generic reconciler (`internal/reconcilers/generic`), which
  owns finalizers, the Ready condition, the `reconcile-policy` annotations,
  claiming and requeue.
- **Credentials per object.** `internal/identity`: a per-resource
  `serviceoperator.azure.com/credential-from` secret, else the namespace's
  `aso-credential`, else the global credential from the operator's settings
  (section 6).
- **Tests** are envtest plus go-vcr recordings of real Azure (recorded
  once against a subscription, replayed in CI).

So a gap in ASO is one of two kinds, and **both are in scope here**:

1. **An ARM resource ASO does not export.** It is usually in the specs
   already; ASO would export it with configuration (an `$exportAs` entry,
   a sample, a recorded test).
2. **Something ARM does not expose, or that ASO declined.** Data-plane
   objects: users and grants inside a database, secrets in a vault. These
   are hand-written in ASO too.

**ARM resources are in scope.** The builder, 2026-11-09: "Let's not forbid
Azure API resources. I think those are perfectly fine. Build them or pull
them from upstream, whichever is easier." This replaces the earlier rule,
which sent every ARM gap upstream as configuration and never built one
here (open question 6, recommended then; the builder first made one
exception, for management locks, then lifted the rule). What stays is the
upstream-first habit. For each ARM kind, in this order:

1. **Recheck upstream, read-only**, and record what was found: the latest
   release, `main` since the pin, open and draft pull requests, issues. A
   kind that is already in the pinned code or in a release is skipped, and
   the skip recorded in section 3.
2. **Copy upstream where it exists**: an open pull request or unreleased
   `main` that adds the kind is copied here (header kept, a "Modified by
   High Five Labs" note, a line in `UPSTREAM.md`), as for the PostgreSQL
   `User`.
3. **Generate it where ASO's own generator can.** The kind's CRD, types
   and ARM wire types come from the pinned ARM specs and generator
   configuration, not from hand-written Go. The configuration is ours,
   written the way ASO writes `v2/azure-arm/<group>.yaml`, in this
   repository; the generated files are checked in and never hand-edited.
   This is preferred over hand-writing; the mechanism is "Generated ARM
   kinds" in section 2.
4. **Hand-write it** only when neither applies, as `ManagementLock` was:
   a lock is one type at four scopes, which ASO's generator needs a design
   decision to model (issue #3756).

When ASO ships a kind that we carry, ours is deprecated for a release and
users move to ASO's (section 4).

**`ManagementLock`, the first ARM kind.** The builder, 2026-11-09: "Yes,
make the exception for Azure management locks." `Microsoft.Authorization/locks`
is built here as `ManagementLock`, hand-written. Why it was first, and why
hand-written:

- they are the biggest Azure gap by count: 31 of the 33 Azure Verified
  Modules read for the seed catalog accept a `lock`
  (`catalog/docs/azure.md`, gap 1);
- upstream cannot take them as configuration: a lock is one type at four
  scopes (subscription, resource group, resource, any `{scope}`), and
  ASO's generator needs a design decision on which to model before it can
  emit it. Issue #3756 is open (rechecked 2026-11-09): a maintainer wrote
  in 2026-05 that it needs design work first, and the latest comment
  (2026-07) says the scope question is what blocks it. No pull request;
  #4629, the earlier request, is closed; v2.22.0 is still the latest
  release;
- a lock is small (a level and notes), synchronous (no long-running
  operation), and an extension resource on any owner, so a hand-written
  reconciler on ASO's generic reconciler, like this repository's other
  kinds, carries it without ASO's generator.

The exception that made it possible is superseded by the rule above.

## 2. How a kind is built here

- **Our kinds live in our groups**, `<service>.azure.hfl.dev`, reconciled by
  our process. Owners stay ASO kinds in `<service>.azure.com`: a `User`'s
  owner is ASO's `FlexibleServer`, a `VaultSecret`'s is ASO's `Vault`. The
  resolver reads them from the cache (the manager starts informers for
  them) and ASO's claiming requires them Ready, as in ASO.
- **ASO's framework, imported unchanged.** ASO is a Go module used from a
  checkout at the pin through `replace`. Its framework is under
  `v2/internal/`, which another module may not import, so one patch adds a
  file to the checkout, `v2/pkg/outoftree/outoftree.go`, that re-exports it
  through type aliases (the generic reconciler, credential provider,
  resolver, kube client, config, predicates). Nothing of ASO's is edited.
  Copying the framework instead was rejected: `internal/identity` alone is
  2,500 lines, and the generic reconciler pulls in more.
- **Per-kind code is copied from the nearest ASO code**, with its header
  kept and a "Modified by High Five Labs" note (`UPSTREAM.md`). The
  PostgreSQL `User` is ASO's PostgreSQL `User` plus what ASO's MySQL `User`
  has (`aadUser`, `databasePrivileges`). `VaultSecret` is new; its shape
  follows the hand-written reconcilers and its modes follow `VaultKey`.
- **No webhook.** ASO validates and defaults with webhooks; here validation
  is CEL rules in the CRDs (exactly one of `localUser` and `aadUser`,
  immutable names and owners), and the one default (`azureName` is
  `metadata.name`) is computed in code. No webhook server or certificate
  to install. The generated ARM kinds lose ASO's webhook validations (below).
- **Dependencies at ASO's versions.** `hack/check-go-mod.sh` fails if any
  module both `go.mod` files require differs from ASO's.

### Generated ARM kinds

The preferred way to build an ARM kind (section 1): ASO's own generator,
run over the pinned specs with configuration kept here.

- **Inputs.** `generator/azure-arm.yaml` (the generator's top-level
  configuration: the filters, the `Tags` transformer and the common-types
  namespace override, taken from ASO's `v2/azure-arm.yaml`),
  `generator/azure-arm/<group>.yaml` (per group, as ASO's: which versions
  and resources are exported with `$exportAs` and `$supportedFrom`, which
  properties are references or secrets), `generator/specs.txt` (the specs'
  services) and `generator/external-owners.txt` (the ASO kinds our kinds are
  owned by). The specs are the `azure-rest-api-specs` commit ASO pins
  (`UPSTREAM.md`), fetched by `hack/specs.sh` into `.cache/` as a sparse,
  blob-less checkout of only the services listed (about 170 MB with the
  common types, not several GB).
- **Run.** `hack/generate-arm.sh` builds ASO's generator from the checkout
  and runs it; `hack/generate.sh` then makes deepcopy code and CRDs. It
  takes about half a minute but loads every spec of the services, so it
  runs on the Ubuntu machine (`task generate-arm`), and `task check`
  regenerates and fails if the checked-in output is stale. The output is
  `apis/arm/<group>/<version>` (types, `storage` and `arm` subpackages and
  generated tests), `apis/arm/<group>/customizations`,
  `pkg/controllers/arm_resources_gen.go` (registration, indexes and secret
  watches) and `generator/reports/`. None of it is edited by hand.
- **Patches to the generator** (`patches/azure-service-operator/0002`),
  each switched by an environment variable the script sets, so the
  generator is otherwise ASO's: our groups end in `.azure.hfl.dev`
  (`ASO_GENERATOR_GROUP_SUFFIX`) while an owner listed in
  `external-owners.txt` keeps ASO's group, so a child of ASO's
  `ManagedEnvironment` names `app.azure.com` as its owner
  (`ASO_GENERATOR_EXTERNAL_OWNERS`); ASO's legacy and hybrid version
  migration tables are emptied (they describe ASO's releases); no webhooks
  are generated, `AzureName()` falls back to the object's name, which ASO's
  defaulting webhook would have set (`ASO_GENERATOR_NO_WEBHOOKS`); and the
  API version is the one the controller reconciles
  (`ASO_GENERATOR_API_STORAGE`).
- **One version, no conversion webhook.** ASO's CRDs have two versions, an
  API version and a storage (hub) version, converted by a webhook. Ours
  serve only the API version (`v20250101`): `hack/generate.sh` makes CRDs
  from the API packages only, the controller is registered on the API type,
  and ASO's reconciler needs no conversion when the object already is its
  original version. The storage and `arm` packages stay as in-memory code
  (the generated `ConvertSpecTo`/`ConvertStatusTo` pass through the storage
  types). The price: a second API version of a kind would need a webhook,
  or a new kind name. Each kind is pinned to the API version it was
  generated from.

  **An assumption to revisit before the repository goes public** (the
  coordinator, 2026-11-09: go with it for now; the repository is private and
  has no users, so this is cheap to change today and not later). What the
  revisit would involve, if more than one API version of a generated kind
  is wanted:
  1. *A conversion webhook*, as ASO has: serve the API versions and the
     storage version from each CRD with `conversion.strategy: Webhook`; run
     controller-runtime's `/convert` handler in the manager (the generated
     types already implement `ConvertTo`/`ConvertFrom` against a hub); give
     it a certificate and put the CA bundle in every CRD (cert-manager, or a
     small controller that writes both, which needs the right to patch
     CRDs); register the storage (hub) type with the reconciler again, as
     ASO does, and stop patching `registersAsStorageVersion` in the
     generator. Cost: a webhook server, a certificate and a wider install,
     which this repository avoided on purpose.
  2. *Per-version kinds*: a new API version generates a new kind
     (`SitesSlotV20270101`) beside the old, with no conversion, the old
     deprecated. Cost: users migrate by hand (a new object per resource;
     `reconcile-policy: detach-on-delete` on the old one makes that safe),
     and every version is another CRD in the install.
  Either is a decision for the builder; per-version kinds are the cheaper
  one while there are no users.
- **What the missing webhooks cost.** ASO's webhooks also validate resource
  references, secret destinations and that `owner` and `azureName` do not
  change. The generated kinds have the CRD's own schema validation (types,
  enums, required fields) and none of that; an immutable change reaches ARM
  and is refused or misapplied there.
- **Reconciling.** ASO's generic ARM reconciler (`NewAzureDeploymentReconciler`)
  and its ARM client, exported by `patches/azure-service-operator/0003`,
  with the credential ASO picks for the object, so a generated kind has
  ASO's behaviour: claiming under a Ready owner, the long-running
  operation, `reconcile-policy`, `operatorSpec` secrets and config maps.
- **Owners.** The ASO kinds a generated kind names as owners are read
  through the cache, so each is in `controllers.OwnerTypes()` (and the
  manager may list and watch it, `config/rbac/role.yaml`); the stand-ins in
  the tests carry the `serviceoperator.azure.com/resource-id` annotation ASO
  writes on what it provisions, which the owner chain needs.

### Tenant-scope resources

Checked 2026-11-09, before the management group, subscription and policy
kinds (`docs/coverage/foundation.md` rows 1 to 4) are designed.

- **ASO's framework supports them.** `genruntime.ResourceScopeTenant`,
  `ResourceHierarchyRootTenant` and `genericarmclient.MakeTenantScopeARMID`
  build `/providers/Microsoft.X/type/name` with no subscription; ASO's own
  `subscription` `Alias` is the generated precedent (no owner). The ARM
  reconciler's subscription check is skipped for an ID with no subscription
  (`checkSubscription`: "Some resources like `/providers/Microsoft.Subscription/aliases`
  do not have subscriptionID"). An extension resource on a tenant-scope owner
  (a policy definition on a management group) is covered by the resolver's
  tests (`ExtensionOnTenantScopeResource`). The generator emits
  `GetResourceScope() == ResourceScopeTenant` for such types.
- **The identity.** The credential is the namespace's, as for every kind, but
  its rights must be at the tenant root or on the management group (management
  group Owner or Contributor, or "elevate access"); a subscription-level role
  is not enough to create a management group, move a subscription or assign
  policy above a subscription. Real Azure needs this and the fake does not
  check it, as it checks no role. Two namespaces with different tenants cannot
  share a management group object: the object belongs to the credential's
  tenant.
- **The fake does it now** (management groups PR). `pkg/fakecloud/arm` accepts
  the tenant root (`/`) as a parent, so `/providers/Microsoft.Management/...` and
  `/providers/Microsoft.Subscription/aliases/x` can be created, and models the
  management group hierarchy: a group's `details.parent.id` must name a group
  that exists, a group cannot be moved under its own descendant, a group that
  has child groups or subscriptions cannot be deleted, and a subscription is in
  one group at a time (placing it in another takes it out of the first). Unit
  tests: `pkg/fakecloud/arm/arm_test.go`. The error codes of those refusals are
  the fake's, not Azure's.
- **Children of a tenant scope resource** (a subscription placed in a group, the
  group's settings). ASO's resolver roots a hierarchy at a tenant scope resource
  and builds the children's ARM IDs from it, but ASO's generator categorizes
  any URL that does not start with `/subscriptions` as Tenant scope, gives it no
  owner, and the child's name is then taken for the parent's. Patch 0005
  (`ASO_GENERATOR_TENANT_CHILDREN`) categorizes a nested tenant URL as an owned
  resource, as the children of resource-group resources are. The group
  `ManagementGroup` is the root (`GetResourceScope() == Tenant`, `Owner() ==
  nil`); `ManagementGroupsSubscription` and `ManagementGroupsSetting` name it as
  `owner`. Billing aliases may still need an ARM ID owner (billing accounts are
  not ASO kinds).

### The PostgreSQL User

- `aadUser`: the operator signs in as the server's Entra administrator
  (`serverAdminUsername`) with an Entra token from the credential ASO picks
  for the object, and creates the role with Azure's
  `pgaadauth_create_principal` (bound by display name) or, with `objectId`
  and `principalType`, `pgaadauth_create_principal_with_oid`. Roles and
  role options apply as for a local user.
- `databasePrivileges`: per database, `CONNECT`, `CREATE`, `TEMPORARY` or
  `ALL`. The user ends with exactly these on every database; anything else
  is revoked, as ASO's MySQL `databasePrivileges` does. Read back from
  `pg_database.datacl` into `status.appliedDatabasePrivileges`.
- Dropping a user first revokes its database privileges; PostgreSQL refuses
  to drop a role that holds any. ASO's PostgreSQL user has no privileges to
  revoke, so it never needed this.
- `schemaPrivileges`: per database and schema, `USAGE`, `CREATE` or `ALL`.
  Since PostgreSQL 15 only the database owner may create in `public`, so an
  app that runs its own migrations needs `CREATE` there. Schema ACLs live in
  each database's own catalog, so the operator connects to each database
  named (as the same administrator, with the same password or Entra token).
  In each database named now, or named at the last reconcile (kept in
  `status.appliedSchemaPrivileges`), the user ends with exactly the listed
  privileges on every schema; databases never named are left alone, so
  existing users without the field are unchanged.
- `defaultPrivileges`: a list of database, schema, `forRole`, object type
  (`tables`, `sequences`, `functions`, `types`) and privileges, as
  `ALTER DEFAULT PRIVILEGES FOR ROLE <forRole> IN SCHEMA <schema> GRANT …`.
  This is how a reader or an app gets rights on tables a migration role
  creates later. The same exact-per-visited-database rule, with
  `status.appliedDefaultPrivileges`. CEL rules refuse a privilege that does
  not apply to the object type; `ALL` on tables includes `MAINTAIN` on
  PostgreSQL 17. Default privileges set without a schema (global ones) are
  not managed: revoking from them interacts with the built-in defaults.
- **The administrator borrows a role's privileges for one transaction.**
  Granting on a schema needs the privileges of its owner, and
  `ALTER DEFAULT PRIVILEGES FOR ROLE r` needs those of `r`. An Azure
  administrator is not a superuser, and since PostgreSQL 16 `CREATE ROLE`
  gives its creator only ADMIN OPTION on the new role, without INHERIT
  (checked on PostgreSQL 17: "permission denied to change default
  privileges"). So when the administrator lacks a role's privileges, the
  operator runs `GRANT r TO CURRENT_USER WITH INHERIT TRUE`, does the
  change, and `REVOKE r FROM CURRENT_USER GRANTED BY CURRENT_USER`, all in
  one transaction: it uses only the ADMIN OPTION the administrator already
  holds, and no membership outlives the transaction (the tests check
  `pg_auth_members`). Without ADMIN OPTION on `r` the change fails with
  PostgreSQL's error. The alternative, requiring the builder to give the
  administrator lasting membership in every such role by hand, was
  rejected: it leaves the administrator with more than it needs.
- Every grant and revoke is read back; a privilege PostgreSQL did not grant
  or revoke (one made by another grantor) is an error rather than a silent
  success.
- Deleting a user revokes, in each database its schema or default
  privileges name, those privileges and every privilege it holds on that
  database's tables (and columns), views, sequences, functions and types,
  then drops the role. PostgreSQL refuses to drop a role that holds any, and
  default privileges grant exactly such privileges as other roles create
  objects; each revoke borrows the object owner's privileges the same way.
  Objects the user owns are not dropped: deleting a user that owns tables
  fails with PostgreSQL's error, as in ASO, until they are dropped or
  reassigned (except in the databases it owns: see `ownedDatabases`).
- `ownedDatabases` (b5; open question 8, answered): the user owns each
  database listed (`ALTER DATABASE … OWNER TO`), such as the one its
  migrations run in. Nothing is orphaned: a database that leaves the list
  goes to the `reassignOwnedTo` role (the database only), and deleting the
  user first runs `REASSIGN OWNED BY <user> TO <role>` in each database it
  owns, which moves those databases and everything the user owns inside
  them, then drops the user. `status.ownedDatabases` lists what it owns;
  `status.reassignedDatabases` and a `DatabaseReassigned` event name every
  database moved (on delete the status lasts only while the delete is
  retried; the events stay).
- The `reassignOwnedTo` role is `hfl_owner` unless the field names another
  (the field and its name are assumptions; see question 8). The operator
  creates it `NOLOGIN` when missing, refuses a role of that name that can
  sign in, and grants it to `azure_pg_admin`, so every administrator of
  the server (the password one and each Entra one) holds its privileges:
  any of them can later hand a database to a real owner, and a second
  administrator can reassign to a role the first one created.
- Ownership changes borrow privileges as above: `ALTER DATABASE … OWNER
  TO` needs the current owner's privileges and, since PostgreSQL 16, SET
  ROLE to the new owner; `REASSIGN OWNED` needs both roles'. Once a User
  owns a database, granting another user privileges on that database or
  on its `public` schema (owned by `pg_database_owner`, which stands for
  the database's owner and cannot be granted) borrows that User's
  privileges the same way. `databasePrivileges` on a database the user
  owns are not managed: an owner holds them all.

### VaultSecret

- The value comes from a key of a Kubernetes Secret in the namespace; a
  change to it, or to `contentType`, makes a new version. A change to
  `enabled`, `notBefore`, `expiresOn` or tags patches the current version.
- `createMode` for a name held by a soft-deleted secret, and `deleteMode`,
  are `VaultKey`'s: `default`, `recover`, `createOrRecover`,
  `purgeThenCreate`; `detach` (default), `delete`, `disable`.
- A small client for the secrets data plane (REST 7.5) over azcore's
  pipeline, with a bearer token for the vault's audience
  (`https://<dns suffix>/.default`). `azsecrets` is in ASO's module graph
  only indirectly, and its challenge-based auth checks that the vault's host
  matches the token audience, which a test vault cannot.

### VaultCertificate

- b6: a Key Vault certificate imported from a certificate and private key
  in Kubernetes Secrets, typically the `tls.crt` and `tls.key` of the
  `kubernetes.io/tls` Secret cert-manager issues. Two
  `genruntime.SecretReference`s (`certificate`, `privateKey`), so ASO's
  resolver reads them and a change to either Secret reconciles the object.
- Both are checked to make a pair before anything is sent; then imported
  as PEM (`policy.secret_props.contentType: application/x-pem-file`), key
  first, then the chain. Key Vault keeps a key and a secret of the same
  name beside the certificate; the secret's value is that PEM, which App
  Service, Application Gateway and Front Door read
  (`status.secretId`).
- A different leaf (a renewal) is imported as a new version, compared by
  the SHA-1 thumbprint Key Vault reports (`x5t`); `enabled` and tags patch
  the current version. Validity comes from the certificate itself
  (`status.notBefore`, `status.expiresOn`).
- `createMode` and `deleteMode` are `VaultSecret`'s (ASO's `VaultKey`'s),
  applied to `deletedcertificates`; deleting soft-deletes the certificate
  with its key and secret.
- The same owner (an ASO `Vault`), credential and data-plane client as
  `VaultSecret`, with the certificate operations added (REST 7.5). PFX
  import is not built: cert-manager and most in-cluster issuers write PEM.

### ManagementLock

- `owner` is ASO's `ArbitraryOwnerReference`, as ASO's own extension
  resource `RoleAssignment` takes: an ASO resource in the same namespace
  by group, kind and name, or an ARM ID (a subscription, a resource group
  or a resource). A named owner must be Ready and carry the ARM ID ASO
  writes on what it provisions (`serviceoperator.azure.com/resource-id`);
  until then the lock waits with ASO's `WaitingForOwner`. The owner becomes
  the lock's Kubernetes owner, as ASO's `ClaimResource` does.
- The owner is read by name as an unstructured object from the API server,
  so any ASO kind can own a lock without the manager carrying ASO's Go
  types or an informer per kind. That needs `get` (only; no list or watch)
  on every ASO API group: a generated ClusterRole,
  `config/rbac/aso-owner-reader.yaml`, lists ASO's groups at the pin
  (`entra.azure.com` aside).
- `level` is `CanNotDelete` or `ReadOnly`, `notes` up to 512 characters;
  the lock's name is `azureName` or the object's name. Changes are a PUT
  when Azure's lock differs. Deleting the object deletes the lock, from
  the ARM ID recorded when it was claimed, so it works with the owner
  gone; a lock or scope Azure no longer has is deleted already.
- The credential is the one ASO picks for the object; ASO's subscription
  check refuses a scope in another subscription than the credential's.
  Resource Manager's endpoint and audience are ASO's settings
  (`AZURE_RESOURCE_MANAGER_ENDPOINT`, `..._AUDIENCE`), or the credential's
  own cloud.
- A small Resource Manager client (`pkg/armlocks`, API 2020-05-01) over
  azcore's pipeline, as the Key Vault client. ASO's own ARM client is
  internal and built for generated kinds.
- What a lock means for ASO: a `CanNotDelete` lock on what ASO manages
  makes ASO's delete of it fail with `ScopeLocked`, which ASO retries
  slowly until the lock goes; `ReadOnly` also stops ASO's updates. Because
  the owner is the lock's Kubernetes owner, deleting the owner with
  foreground propagation removes the lock first; with background
  propagation (kubectl's default) the owner waits until the lock is
  deleted. That is the point of a lock.

## 3. Candidates

Ranked by how much an application stack on Azure needs them and how cheap
they are to carry. "Built" means in this repository with tests.

(a) Upstream has code or an open pull request (read-only `gh`, 2026-11-07;
rechecked 2026-11-08, see below):

| # | What | State | Move |
| --- | --- | --- | --- |
| a1 | Azure SQL Entra users (`sql.azure.com` `User` with `aadUser`) | PR [#5104](https://github.com/Azure/azure-service-operator/pull/5104), open, last updated 2026-09-28 | next to build here, copying the PR, if a test without a real Azure SQL is acceptable: `CREATE USER … FROM EXTERNAL PROVIDER` runs only on Azure SQL, so no local server can run it (open question 5) |
| a2 | Defender for Storage settings | PR #5783, open | ARM, configuration upstream; no kind here |
| a3 | Privileged Identity Management | PR #5638, open | not an application need |
| a4 | Databricks | PR #5028, draft | not an application need |
| a5 | main since v2.22.0 | only ARO-HCP 2026-10-01 (#5806) | nothing to copy |

(b) Not in ASO:

| # | What | Why an app needs it | Move |
| --- | --- | --- | --- |
| b1 | PostgreSQL user bound to an Entra principal | an app's managed identity signs in to its database without a password; ASO's MySQL user can, its PostgreSQL user cannot | **built**; offer upstream |
| b2 | PostgreSQL privileges per database | an app's user needs `CONNECT`/`CREATE` on its own database | **built**; ASO issue #4694 is open and its maintainers say they still want it, so offer upstream |
| b3 | Key Vault secrets from Kubernetes Secrets | apps on Azure read configuration from Key Vault; ASO declined to create secrets (#1894) | **built**; a lasting gap |
| b4 | PostgreSQL schema privileges and default privileges | since PostgreSQL 15 only the database owner may create in `public`; an app needs `USAGE`/`CREATE` on a schema and default privileges on new tables | **built** (`schemaPrivileges`, `defaultPrivileges` on the same `User`); a1 was skipped because it waits on open question 5 |
| b5 | PostgreSQL database ownership | migrations need the app to own its database | **built** (`ownedDatabases`, `reassignOwnedTo` on the same `User`), after open question 8 was answered |
| b6 | Key Vault certificates from a Kubernetes Secret (import) | TLS certificates issued in-cluster, kept in Key Vault | **built** as `VaultCertificate`, the same shape as `VaultSecret`; no ASO issue or pull request (rechecked 2026-11-09) |
| b7 | ARM resources in the specs but not exported | varies | built here, from the catalog's ranking (table (c) below), by the rule in section 1 |
| b8 | Management locks (`Microsoft.Authorization/locks`) | 31 of 33 Azure Verified Modules take a `lock`; protects what an app cannot lose | **built** as `ManagementLock`, hand-written (section 1); ASO issue #3756 is open |

(c) ARM gaps from the seed catalog (`catalog/docs/azure.md`, section 5),
in its rank order. State at the pin and upstream, read-only, 2026-11-09:
none of these is exported at the pin (the `app` group exports
`AuthConfig`, `ContainerApp`, `Job`, `ManagedEnvironment`; `web` exports
`ServerFarm`, `Site`, `SitesSourcecontrol`; `operationalinsights` only
`Workspace`; `containerregistry` `Registry`, `RegistryCacheRule`,
`RegistryReplication`; there is no Recovery Services group), no open or
draft pull request adds any, and no ASO issue asks for most (the one
found, #4455, is a Recovery Services vault for storage). Each kind
rechecks upstream again when it is built.

| Rank | Gap | State | Move |
| --- | --- | --- | --- |
| c1 | Management locks | built (b8) | done |
| c3 | Container Apps environment children (Dapr components, storages, certificates, managed certificates, ...) | not at the pin, no pull request | **built**, generated: `ManagedEnvironmentsDaprComponent`, `Storage`, `Certificate`, `ManagedCertificate`, `JavaComponent` (`app.azure.hfl.dev`, API 2025-01-01). Not built: Dapr subscriptions, HTTP route configs, .NET components and maintenance configurations exist only in preview API versions, which the filters prune |
| c4 | App Service site children (slots, hostname bindings, certificates, config sections, publishing policies) | not at the pin, no pull request | **built**, generated (`web.azure.hfl.dev`, API 2026-07-15, ASO's own `Site` version): `SitesSlot`, `SitesHostNameBinding`, `SitesSlotsHostNameBinding` (a custom domain on the app and on a slot), `Certificate` (a PFX and its password from Secrets, owned by the resource group). Not built: the generator does not produce the fixed-name config sections (`appsettings`, `connectionstrings`, `authsettingsV2`, `logs`, `backup`, `metadata`) or the `ftp`/`scm` publishing policies as resources (it produced only `config/web`, which duplicates the inline `siteConfig` of ASO's `Site`, so two controllers would fight over it); ASO's `Site` and our `SitesSlot` carry app settings and connection strings inline. Slot children other than host name bindings (config, certificates, containers) are left out |
| c5 | Log Analytics children (tables, data exports, linked services, linked storage, query packs, solutions) | not at the pin, no pull request (rechecked 2026-11-09: v2.22.0 still the latest release, `main` three documentation commits past the pin, no open pull request or issue for any of them; ASO exports only `Workspace`, whose newest version is 2025-07-01) | **built**, generated (`operationalinsights.azure.hfl.dev`, API 2025-07-01, ASO's own `Workspace` version): `WorkspacesTable` (plan, retention, schema, restored logs and search results), `WorkspacesSavedSearch`, `WorkspacesLinkedService` (an Automation account or a cluster), `WorkspacesLinkedStorageAccount`, `WorkspacesDataExport` (added with c10), `QueryPack` (owned by the resource group) and `QueryPacksQuery` (owned by our pack). Data exports (`WorkspacesDataExport`) were first left out because ASO's generator flattens one level only (the spec nests `destination` and `metaData` inside `properties`) and were added with patch 0004 (see the c10 row). Not built: **solutions** (`Microsoft.OperationsManagement`, a 2015 preview API), because its `SolutionProperties` lacks `type: object` and the generator drops it as an empty type, leaving no `workspaceResourceId`; **data sources** (the generator names the receiver `source` and the storage conversions do not compile; they configure the retired Log Analytics agent), **storage insight configs** (legacy), **summary logs**, **clusters** (dedicated clusters, a capacity commitment, not an application need) and **network security perimeter configurations** (read-only). A linked storage account's name is one of five values; the generator keeps such an enum in the storage version, which then imports the API version (a cycle), so `azureName` is a plain string there and Azure refuses a wrong one |
| c6 | Azure Monitor private link scope and scoped resources; network security perimeter associations | not at the pin, no pull request (rechecked 2026-11-09: v2.22.0 still the latest release, `main` three documentation commits past the pin, no open pull request; ASO's `insights` and `network` groups export neither) | **built**, generated: `insights.azure.hfl.dev/v20210901` `PrivateLinkScope`, `PrivateLinkScopesScopedResource` (a workspace, component or endpoint scoped into it, an ARM reference) and `PrivateLinkScopesPrivateEndpointConnection` (approving or rejecting a connection); `network.azure.hfl.dev/v20250501` `NetworkSecurityPerimeter`, `NetworkSecurityPerimetersProfile`, `...ProfilesAccessRule`, `...ResourceAssociation` (a PaaS resource and a profile, both ARM references, in `Learning`, `Enforced` or `Audit`), `...Link` and `...LoggingConfiguration`. An association needs a perimeter and a profile, which ASO does not have either, so the whole family is built (owners: the resource group, then our kinds). Version choices: 2021-09-01 is the newest stable private link scope API; the perimeter is at 2025-05-01 and not 2025-03-01 (ASO's network version), because at 2025-03-01 the generator drops every writable property of these kinds from the spec side (a perimeter without `location`, an association without `profile`) and nothing says why. Not built: the Log Analytics workspace's own perimeter configurations (`workspaces/networkSecurityPerimeterConfigurations`, read-only, in c5's skip list) |
| c7 | Key Vault keys | `VaultKey` merged in ASO (#5564), next release | skip: wait for ASO's release |
| c8 | Management groups, Azure Policy, budgets, vWAN hub connections, subscription tags | management groups #2249 open, no pull request | now `docs/coverage/foundation.md` rows 1 to 10. **Management groups built** (row 1), generated: `management.azure.hfl.dev/v20230401` `ManagementGroup` (tenant scope, no owner; `displayName`, parent by object or ARM ID), `ManagementGroupsSubscription` (a subscription's placement; `azureName` is the subscription ID; owned by the group) and `ManagementGroupsSetting` (`settings/default`: default group for new subscriptions, authorization to create top-level groups; owned by the group). Version 2023-04-01, the newest stable. **Generator**: patch 0005 makes a child of a tenant scope resource an owned resource (the generator called it Tenant scope, with no owner, so the child's name was taken for the group's); `children` of a group's status is dropped at the second level (directly recursive, controller-gen cannot unroll it). Not built: service groups (preview only), the group's `descendants`, `entities` and `tenantBackfill` actions (read or POST) |
| c9 | ACR tokens, scope maps, credential sets | not at the pin, no pull request (rechecked 2026-11-09: v2.22.0 still the latest release, `main` three documentation commits past the pin, nothing open on registries; ASO exports `Registry`, `RegistryReplication` and `RegistryCacheRule`, whose `credentialSetResourceId` had no kind to point at) | **built**, generated (`containerregistry.azure.hfl.dev`, API 2025-11-01, ASO's own `Registry` version): `RegistriesToken` (bound to a scope map, enabled or disabled; certificates in the spec), `RegistriesScopeMap`, `RegistriesCredentialSet` (the Key Vault secret URIs of an upstream registry, for a cache rule; identity as ARM references) and, beyond the ranking, `RegistriesWebhook` (service URI and custom headers are `SecretReference`s, so they come from Kubernetes Secrets). Not built: a token's **passwords**: they are generated by the `generateCredentials` action, returned once, and are not part of the token's PUT, so no CRD field could hold them (a password would be a Key Vault secret an operator writes after a POST; that is a hand-written kind, not queued); connected registries (IoT Edge), private endpoint connections |
| c10 | VM extras: run command, auto-shutdown schedule, maintenance assignment, Recovery Services backup | not at the pin, no pull request (rechecked 2026-11-09: v2.22.0 still the latest release, `main` three documentation commits past the pin; open issues #1187 asks for database backup only; ASO has no `devtestlab`, `maintenance` or `recoveryservices` group) | **built in two parts**. Part one, generated: `compute.azure.hfl.dev/v20220301` `VirtualMachinesRunCommand` (script or script URI, parameters, the run-as password from a Secret, owned by ASO's `VirtualMachine`); `devtestlab.azure.hfl.dev/v20180915` `Schedule` (a VM's auto-shutdown: task type `ComputeVmShutdownTask`, daily time, time zone, notification; the VM an ARM reference; owned by the resource group); `maintenance.azure.hfl.dev/v20230401` `MaintenanceConfiguration` (owned by the resource group). **Not built: `ConfigurationAssignment`** (assigning a configuration to a VM): the spec has the same resource at four scopes (subscription, resource group, resource, nested resource), ASO's generator picks one of the four paths by map order, so each run differs (extension resource, then location scope, then another) and `task check` would fail at random; it is `ManagementLock`'s problem (a hand-written kind, not queued). Part two, Recovery Services, is generated too: `recoveryservices.azure.hfl.dev/v20260701` `Vault` (owned by the resource group: SKU, identity, redundancy, encryption, monitoring, public network access), `VaultsBackupPolicy` (the union of IaaS VM, SQL, file share, workload and other policies, with schedule and retention; owned by our `Vault`), `VaultsBackupResourceGuardProxy` and `VaultsPrivateEndpointConnection`. **Not built, and why it matters**: the protection container, the protected item (what turns backup on for a VM) and the protection intent. Their paths hold a fixed fabric segment (`backupFabrics/Azure`) that is no resource, so the generator makes the resource group the owner and the Kubernetes object cannot name the ARM ID ("had 1 azureNames and 3 resourceTypes", found by the test); a protected item is also owned by its container, which a VM never needs created. They, the singleton settings (`backupstorageconfig`, `backupconfig`, encryption config) and Site Recovery are hand-written candidates (see docs/handoff.md). Redundancy is set on the vault itself (`redundancySettings`), so the storage-config singleton is not needed to choose geo or local. **c18 (stale ARM versions)**: ASO's `VirtualMachine` is at 2022-03-01 while the Compute spec has 2026-04-01, so the run command is built at 2022-03-01, ASO's version, so owner and child share a vintage; it moves when ASO's `VirtualMachine` does. **Generator change**: patch 0004 (UPSTREAM.md) makes a property flattened out of a flattened property stay an object, which the Maintenance window (`properties.maintenanceWindow.*`) and the Log Analytics data export needed; it changes nothing for kinds that generated before |
| c11 | Storage local users (SFTP) | not at the pin, no pull request | queued |
| c12 | Cognitive Services RAI policies | not at the pin, no pull request | queued |
| c13 | Cosmos DB Gremlin, SQL dedicated gateway | not at the pin, no pull request | queued |
| c14 | AKS managed namespaces | not at the pin, no pull request | queued |
| c18 | Stale ARM versions (VirtualMachine, ApplicationGateway, DNS zones) | not a new kind | queued |

App Configuration key-values are already in ASO (`appconfiguration`
`KeyValue`), so they are not a gap.

Upstream, rechecked read-only on 2026-11-08 before building b4: the latest
release is still v2.22.0 (2026-10-30; the rolling `experimental`
pre-release aside); `main` is two commits past the pin, both
documentation (#5826, #5827). No open pull request touches PostgreSQL
users (the open ones are #5828, #5821, #5820, #5814, #5804, #5783, #5780,
#5773, #5769, #5768, #5693, #5659, #5638, #5104, #5028, #4210); the last
changes to `internal/reconcilers/postgresql` and
`internal/util/postgresql` are the escaping and connection fixes already
at the pin (#5762, #5684, #5551). Issues: #4694 (per-database privileges)
and #4333 (per-database users for `FlexibleServersDatabase`) are open with
no pull request; nothing asks for schema or default privileges. ASO has no
code for either to copy, so b4 is ours, written the way b2 is.

Rechecked read-only on 2026-11-09 before building b5: unchanged. The
release is still v2.22.0, `main` still two documentation commits past the
pin, no open pull request touches PostgreSQL, and nothing upstream sets a
database's owner from a user (#4333 asks for an owner on
`FlexibleServersDatabase` and is open, without a pull request). b5 is ours.

## 4. Upstream and sync

The pin, what is copied and the patch are in `UPSTREAM.md`, with how to
move the pin. A kind or field here moves to ASO when ASO ships it: offer b1
and b2 upstream (they extend ASO's own PostgreSQL user the way its MySQL
user already works), keep ours until a release carries them, then
deprecate ours for a release. The export patch is the one piece that must
keep applying at every pin move.

## 5. Testing: the fake cloud

ASO's own tests replay recordings of real Azure, which cannot be made
without a subscription. These kinds are data plane, so the fakes are small
and exact (`docs/fake-cloud.md`):

- **Entra**: an in-process `http.RoundTripper` that answers instance
  discovery, OpenID metadata and the token endpoint, plugged in through
  ASO's own `TokenCredentialProvider` hook. azidentity's credentials run
  unchanged: client secret, client assertion (strict workload identity),
  workload identity. Its tokens say who asked (client, the assertion's
  subject or a digest of the secret, the audience), so a fake service can
  decide which identity to trust.
- **Key Vault**: the secrets data plane over TLS (set, get, update, soft
  delete, get deleted, recover, purge), and certificates (PEM import, get,
  update, soft delete, get deleted, recover, purge, with the secret Key
  Vault keeps beside each), accepting only the tokens a test trusts.
- **PostgreSQL**: a real PostgreSQL 17 in Docker with TLS, set up like a
  flexible server: `azure_pg_admin`, a password administrator, Entra
  administrators whose password is the token of the identity that may sign
  in as them, and stand-ins for Azure's `pgaadauth` functions.
- **Resource Manager**: a store of resources by ARM ID (a PUT stores the
  body with `id`, `name`, `type` and a `provisioningState` of `Succeeded`
  added, and needs the parent to exist; GET and DELETE as expected) and
  management locks at any scope, over TLS, accepting only the tokens a
  test trusts. ASO's own ARM client, in the generated kinds' tests, talks
  to it.
  Locks act as Azure documents: `CanNotDelete` refuses deletes at and
  under its scope, and of a scope holding a locked resource; `ReadOnly`
  refuses writes too; each with 409 `ScopeLocked`.
- **envtest** with our CRDs and ASO's owner CRDs (generated from ASO's
  types at the pin, storage versions only).

Azurite (the storage emulator) does not apply to these kinds.

## 6. Identity

### How ASO gets credentials (at the pin)

- **Global**: ASO's manager reads `AZURE_*` settings from the
  `aso-controller-settings` secret in its namespace: a client secret, a
  client certificate, user-assigned identity credentials, or workload
  identity (`USE_WORKLOAD_IDENTITY_AUTH`, the Azure Workload Identity
  webhook projecting a token for the manager pod's service account,
  `azureserviceoperator-default`); else environment, managed identity
  (IMDS) or the Azure CLI.
- **Per namespace**: a secret named `aso-credential` in the object's
  namespace; **per resource**: the secret the
  `serviceoperator.azure.com/credential-from` annotation names. Either holds
  a client ID and tenant and a client secret, a certificate, pod identity,
  or nothing more, which means workload identity:
  - relaxed (the default): the manager pod's own projected token, so the
    managed identity's federated credential names ASO's service account;
  - strict (`AZURE_WORKLOAD_IDENTITY_AUTH_MODE=strict`): ASO requests a
    token for a service account in the object's own namespace
    (`aso-workload`, or the one the secret names) with the TokenRequest API,
    and its ClusterRole may create tokens only for `aso-workload`.

### This manager

It uses ASO's credential provider itself, so it picks the same credential
for the same object. It runs in ASO's namespace (so it reads
`aso-controller-settings` the same way) under its own service account,
`aso-extensions-manager`, whose rights are our kinds, reading Secrets,
ConfigMaps, Namespaces and the ASO owner kinds it resolves (list and
watch: the FlexibleServer, Vault, ManagedEnvironment and ResourceGroup),
`get` on any ASO resource (a `ManagementLock`'s owner), events, and tokens
for `aso-workload` only. It cannot change any ASO resource, nor list the
rest. Sharing ASO's
service account would hand our manager all of ASO's Kubernetes rights.

| ASO's credential | Same Azure identity? | Extra setup |
| --- | --- | --- |
| client secret, certificate | yes: the same secret | none |
| strict workload identity | yes: the same `<ns>/aso-workload` token | none |
| relaxed workload identity, global workload identity | yes, once the managed identity trusts our service account | one federated credential per managed identity: issuer the cluster's, subject `system:serviceaccount:azureserviceoperator-system:aso-extensions-manager`, audience `api://AzureADTokenExchange`; and for the global credential, the same `azure.workload.identity/client-id` annotation on our service account |
| pod identity, IMDS | yes (the node's or pod's identity) | none |

`config/share-aso-identity` is the opt-in that runs our manager as
`azureserviceoperator-default` for quick trials, with no federated
credential to add, at the cost above. It renders; it is not exercised on
kind.

### Why not an operator

The GCP sibling of this repository needs an operator because Config
Connector, in namespaced mode, runs one manager per namespace, each as that
namespace's identity, and the sibling mirrors that. ASO
runs one manager and picks a credential per object, so ours does the same:
one Deployment, nothing per namespace to create. A static install
(`config/default`) is enough.

## Coverage dashboard

`docs/coverage/` says how much of the Azure Resource Manager API ASO covers and
how much more our kinds do, by service, resource and attribute, with bars in
the root README and a tier ranking (`priority.csv`, from the catalog's
download ranking). It is made by `hack/coverage/` from pinned inputs (the specs
at ASO's own pin, ASO's API types, our CRDs) and checked with
`hack/coverage/run.sh --check`. Method and caveats: `docs/coverage/README.md`.
Rerun it when a kind or a pin changes.

## 7. Proven, and not

Proven by `task test` (envtest, 24 tests, and the generated kinds' own
serialization and conversion tests):

- a password user gets exactly its database privileges, changes revoke
  what is no longer listed (including grants made by hand), and deletion
  drops the role with its privileges;
- an Entra user is created through `pgaadauth`, by name and by object ID,
  with roles and privileges, signing in as the Entra administrator with the
  token of the namespace's `aso-workload` (strict workload identity) from
  azidentity's client-assertion credential;
- another namespace's identity cannot sign in as that administrator, and
  the fake vault refuses it;
- a `VaultSecret` follows its Kubernetes Secret (new versions), patches
  attributes and tags, soft-deletes, recovers with `createOrRecover`,
  replaces with `purgeThenCreate`, reports `recover` with nothing to
  recover and the soft-delete conflict, and detaches by default;
- `credential-from` picks a client-secret credential over the namespace's;
- the CRDs' CEL rules refuse a user with both or neither credential kind;
- schema privileges: granted on a connection
  to the named database, changed to exactly the new list (taking back a
  grant made by hand), revoked when the database is no longer named,
  refused for a schema that does not exist, revoked before the role is
  dropped; an Entra user's, signed in to that database with its token; a
  user that has `CREATE` on `public` can create a table there;
- default privileges: a reader gets `SELECT` on tables and `USAGE, SELECT`
  on sequences a migrator user creates later, and not `INSERT`; changes
  are exact (a default privilege made by hand is revoked); `ALL` on tables
  is PostgreSQL 17's eight; a schema the migrator owns is granted with the
  migrator's privileges; the administrator keeps only its ADMIN OPTION on
  the migrator afterwards; deleting the reader revokes its default
  privileges and what they granted it on the migrator's table and sequence,
  and drops it, so the migrator can be dropped too; the CRD refuses a
  privilege that does not apply to the object type, and duplicates;
- database ownership: a password user owns the two databases it lists
  and can create a table in `public` of one; another user is granted
  `CONNECT` on that database and `USAGE` on its `public` schema with the
  owner's privileges, borrowed; the administrator keeps only its ADMIN
  OPTION on the owner; a database that leaves the list goes to a named
  `reassignOwnedTo` role, with status and an event; deleting the owner
  gives its database and the table it created there to `hfl_owner`
  (`NOLOGIN`, held by `azure_pg_admin`), with an event, and drops it; the
  other user keeps its privileges and can still be deleted; a
  `reassignOwnedTo` role that can sign in is refused, and the CRD refuses
  the user itself as that role;
- Key Vault certificates: the certificate and key of a `kubernetes.io/tls`
  Secret are imported with the namespace's identity, the vault keeps the
  PEM in the certificate's secret, and status carries the thumbprint,
  expiry and secret ID; a renewal in the Secret is a new version; disabling
  patches the current one; a key that does not match is refused before
  the vault is called; `deleteMode: delete` soft-deletes the certificate
  and its secret; recreating it is refused by default while soft-deleted,
  and `createOrRecover` recovers it; another identity is refused; the CRD
  needs both the certificate and the key;
- Container Apps environment children, generated (c3): a Dapr component
  owned by ASO's `ManagedEnvironment` (a stand-in with the annotation ASO
  writes) is PUT by ASO's own ARM reconciler with the namespace's identity at
  the ARM ID computed from the owner chain (resource group, environment,
  component), its secret value from a Kubernetes Secret; status is read
  back; a spec change is PUT again; deleting the object deletes the
  component and leaves the environment; a storage (key from a Secret), a
  certificate (value and password from Secrets), a managed certificate and a
  Java component are created and deleted the same way; the CRD refuses an
  unknown domain validation method and a missing owner; a `CanNotDelete`
  `ManagementLock` on the resource group makes deleting a component fail
  with `ScopeLocked`, and the delete goes through once the lock is removed.
  The generated packages' own tests (JSON and property-conversion round
  trips) pass;
- App Service children, generated (c4): a deployment slot of ASO's `Site`
  (a stand-in) is PUT by ASO's reconciler with the namespace's identity,
  with its server farm as an ARM reference; a host name bound to the slot is
  owned by our slot (the owner chain runs through ASO's `Site` and our
  kind); a change is PUT again; deleting the binding and then the slot
  deletes them and leaves the `Site`; a host name bound to the app and a
  certificate (the PFX and its password from Secrets) are created and
  deleted the same way; the CRD refuses an unknown SSL state;
- Log Analytics children, generated (c5): a custom table of ASO's
  `Workspace` (a stand-in with the annotation ASO writes), with its plan,
  retention and schema, a saved search, a linked storage account (its
  storage account an ARM reference) and a linked Automation service are PUT
  by ASO's reconciler with the namespace's identity at the ARM ID computed
  from the owner chain; a retention change is PUT again and status is read
  back; deleting them deletes them and leaves the workspace; a query pack
  owned by the resource group and a query owned by our pack (the owner
  chain runs through our kind) are created and deleted the same way; the
  CRD refuses a table plan that does not exist and a retention under four
  days;
- Azure Monitor private link and network security perimeters, generated
  (c6): a private link scope owned by the resource group (a global
  resource) with its access modes, a workspace scoped into it (owned by our
  scope, the workspace an ARM reference) and a change of access mode are PUT
  by ASO's reconciler with the namespace's identity; a perimeter, its
  profile, an access rule (owned by the profile) and an association of a
  storage account with the profile (owned by the perimeter, both ends ARM
  references) are created, changed (`Learning` to `Enforced`) and deleted
  child first; the CRD refuses an access mode or rule direction that does
  not exist;
- container registry tokens, scope maps, credential sets and webhooks,
  generated (c9): a scope map of ASO's `Registry` (a stand-in), a token that
  references it by ARM ID, and a webhook whose service URI and headers come
  from Kubernetes Secrets are PUT by ASO's reconciler with the namespace's
  identity; disabling the token is PUT again; they are deleted and leave the
  registry; a credential set with Key Vault secret URIs is PUT and deleted;
  the CRD refuses a credential name that does not exist;
- VM extras, generated (c10): a run command of ASO's `VirtualMachine` (a
  stand-in) with a script, a parameter and the run-as password from a
  Secret (the fake holds it in the body), an auto-shutdown schedule owned
  by the resource group that names the VM by ARM ID, a maintenance
  configuration (scope, nested window, patch settings) are PUT with the namespace's identity; a schedule
  change is PUT again; they are deleted and leave the VM; the CRD refuses a
  maintenance scope and a schedule status that do not exist. A Log Analytics
  data export to a storage account is PUT and deleted likewise;
- management groups, generated (foundation row 1): a group at the tenant root
  (no resource group, no subscription in its ARM ID; the namespace's identity
  PUTs `/providers/Microsoft.Management/managementGroups/<name>`), a child
  group naming its parent by Kubernetes object, a grandchild naming it by ARM
  ID, the hierarchy settings (`settings/default`) and a subscription placed in
  a group (both children of a tenant scope resource, owned by the group), a
  changed display name PUT again, and the deletes, children first; a group
  whose parent is not in Resource Manager is refused and the refusal reaches
  its Ready condition. On kind, `ManagementGroup` and
  `ManagementGroupsSubscription` are Ready for team-a and refused for team-b.
  The fake does not show: whether Azure accepts these names or parents (a
  group's name is a free string here), that creating one takes minutes
  (asynchronous operation), that a new group's parent defaults to the tenant
  root group, that a subscription moved by hand leaves our placement object
  stale, and the rights the identity needs;
- Recovery Services, generated (c10 part two): a vault owned by the resource
  group (SKU `RS0`, system-assigned identity, local redundancy, public
  network access) and a backup policy owned by our vault (an IaaS VM
  policy: daily schedule, long-term retention; the owner chain runs through
  our kind) are PUT by ASO's reconciler with the namespace's identity; a
  longer retention is PUT again; they are deleted policy first. A protection
  container was tried and refused by the owner chain (above), which is why it
  is not a kind;
- management locks: a lock on an ASO `ResourceGroup` is set in the fake
  Resource Manager on the ARM ID ASO recorded, with the namespace's
  `aso-workload` identity, and the `ResourceGroup` becomes its Kubernetes
  owner; while it is `CanNotDelete`, deleting the resource group or a
  resource in it is refused with `ScopeLocked` and changing the resource is
  not; changed to `ReadOnly`, the change is refused too; deleting the lock
  removes it and the deletes go through; locks by ARM ID on a resource
  (with a name holding spaces) and on the subscription protect what is
  under them; a scope in another subscription than the credential's is
  refused (ASO's check); an owner not there yet holds the lock back
  (`WaitingForOwner`); an identity the fake does not trust is refused; the
  CRD refuses an owner that is neither or both of an ARM ID and a named
  resource, a named owner without group and kind, and an unknown level.

Proven on kind by `task operator-e2e` (`tests/operator/run.sh`; passed
2026-11-08 on kind 0.33 on an Apple-silicon Mac, Docker 20.10, both on a
new cluster and rerun on the same one): the CRDs install; the manager runs in ASO's namespace as
`aso-extensions-manager`; that account may request tokens only for
`aso-workload`, may read Secrets and may not create them, may not change
ASO's resources or webhooks; in team-a the `User` and `VaultSecret` become
Ready with team-a's `aso-workload` identity, PostgreSQL holds the Entra role
with its database privileges and its schema privileges in `app`, the role
owns the database `orders`, and the fake vault logs that identity; in
team-b, another identity, both are refused; deletion gives `orders` to
`hfl_owner` (which cannot sign in, with a `DatabaseReassigned` event),
drops the role and soft-deletes the secret. With the b5 checks, it passed
again on 2026-11-09 on the builder's Ubuntu machine (a warm kind cluster),
after `task check` there. With `ManagementLock` (passed the same day, the
same way): the manager may get an ASO `ResourceGroup` but not list or
change it; team-a's lock on its `ResourceGroup` becomes Ready, and the
fake Resource Manager (`cmd/fakearm`) logs the lock created on the
resource group's ARM ID with team-a's `aso-workload` identity; team-b's
is refused (401); deleting team-a's lock removes it. With the generated
kinds (passed 2026-11-09, the same way): team-a's Dapr component is created in
the fake Resource Manager under team-a's environment with team-a's
`aso-workload` identity, team-b's is refused, the `CanNotDelete` lock on
team-a's resource group makes deleting it fail with `ScopeLocked` (ASO's
reconciler, ASO's error handling), and once the lock is removed ASO's
retry deletes it. With
`VaultCertificate` (passed 2026-11-09, the same way): team-a's certificate
from a `kubernetes.io/tls` Secret is imported into the fake vault, team-b's
is refused (401), and deleting team-a's soft-deletes it.

Not provable locally: real Entra exchanging the `aso-workload` token
(the managed identity's federated credential, issuer and audience
checks); the Azure Workload Identity webhook; Azure's own `pgaadauth`
(the stand-ins record the binding in a role comment where Azure uses a
security label, and give the caller ADMIN OPTION on the new role, which
PostgreSQL 16 requires and Azure does not document); Key Vault's RBAC and
its asynchronous recover and purge (instant in the fake). For schema and
default privileges: whether Azure's administrator holds ADMIN OPTION on a
role created by Azure's own `pgaadauth` functions (the stand-ins give it;
without it, default privileges `forRole` an Entra role fail with
PostgreSQL's error), who owns a database ARM creates (the fake: the
`azure_pg_admin` role, so the administrator can grant on `public`), and
PostgreSQL before 16, where role membership has no INHERIT option (the
code handles it, no test runs it). For database ownership: whether Azure
lets an administrator grant a role to `azure_pg_admin` (a role Azure
manages; if it refuses, giving up a database fails with its error and
the delete waits), whether an Entra administrator's membership in
`azure_pg_admin` carries INHERIT and SET as the fake's does, and, as
above, ADMIN OPTION on roles `pgaadauth` creates. For management locks:
real Resource Manager's authorization (who may write
`Microsoft.Authorization/locks/*`: Owner or User Access Administrator, not
Contributor, so ASO's identity may need a role it lacks), the exact
errors for a missing scope, and ASO's own behaviour against a real lock
(it is ASO's code, not exercised here; issue #3756 reports it retries
`ScopeLocked` slowly).

Not built for b4: privileges on objects that already exist (`GRANT … ON ALL
TABLES IN SCHEMA`), and global default privileges (no schema). Not built
for b5: `REASSIGN OWNED` in databases the user does not own (a user that
created tables in another database's schema still cannot be deleted
until they are moved, as in ASO), and the default privileges a user holds
as `forRole` (PostgreSQL's `REASSIGN OWNED` leaves them; they block the
drop until the users they grant to are deleted).

## 8. Open questions

1. **Licence**: MIT, following ASO, is the orchestrator's reading of "maybe
   I follow suit with theirs". Apache-2.0 (the GCP sibling's licence) is
   the alternative; switch before publishing if wanted.
2. **Upstream first?** b1 and b2 extend ASO's own PostgreSQL user the way
   its MySQL user works, and ASO's maintainers say they want b2 (#4694).
   Offer both upstream and keep ours until a release ships them, or
   upstream only? Recommended: both, ours until the release.
3. **Relaxed workload identity**: the default install needs one extra
   federated credential per managed identity. Keep the narrow account as
   the default (recommended), or default to sharing ASO's?
4. **Reading Secrets cluster-wide**: like ASO, the manager reads all
   Secrets (credentials and values). ASO's `AZURE_TARGET_NAMESPACES`
   narrows its cache and ours alike; is that enough, or should the install
   default to it?
5. **Azure SQL Entra users (a1)**: copy PR #5104 here with a test that
   cannot run the real `FROM EXTERNAL PROVIDER` statement, or wait for
   upstream?
6. **ARM gaps here at all?** Building an ARM kind in our group needs a
   patch to ASO's generator (the group domain) and the specs submodule.
   Recommended then: never; ARM gaps go upstream as configuration.

   **Answered, 2026-11-09, twice.** First, with one exception: the
   builder said "Yes, make the exception for Azure management locks."
   (`ManagementLock`, built, hand-written.) Then, lifting the rule: "Let's
   not forbid Azure API resources. I think those are perfectly fine. Build
   them or pull them from upstream, whichever is easier." Plain ARM
   resources are in scope; the order of preference (copy upstream, then
   generate, then hand-write) and the upstream recheck before each kind are
   in section 1. The recommendation above is superseded.
7. **Name collisions**: our `User` shares its kind name with ASO's three
   `User` kinds in other groups; `kubectl get users` is ambiguous and needs
   the group. Rename to `PostgreSQLUser`, or keep ASO's name so moving to
   ASO's group later is a group change only? Recommended: keep it.
8. **PostgreSQL database ownership (b5)**: `ALTER DATABASE … OWNER TO` the
   user is the next candidate. PostgreSQL refuses to drop a role that owns
   a database, so deleting such a user must first give the database to
   someone: back to its previous owner (recorded in status when ownership
   was taken), to the administrator that reconciles the user, or nobody
   (the delete waits until a person moves it). Dropping the database is not
   an option: it is ASO's `FlexibleServersDatabase`. Recommended: back to
   the previous owner, falling back to the administrator when that role is
   gone; objects the user created inside stay its own, so the delete still
   fails until they are dropped or reassigned, as for any user that owns
   tables.

   **Answered, 2026-11-09.** The builder: "we should try to not orphan the
   thing and instead like have a root user, just some dummy root user that
   becomes the owner." The coordinator's reading, which was built: the
   operator ensures a `NOLOGIN` role on each server, named `hfl_owner`
   unless a field overrides it; deleting a User that owns databases runs
   `REASSIGN OWNED BY <user> TO hfl_owner` in each database it owned, then
   drops the user; status and events name every database moved; nothing
   signs in as `hfl_owner`, and an administrator can later hand a database
   to a real owner. Assumed in building it (not the builder's words): the
   override is a field on each `User`, `spec.reassignOwnedTo`; a database
   removed from `ownedDatabases` while the user lives goes to the same role
   (the database only, without `REASSIGN OWNED`); the role is granted to
   `azure_pg_admin` so every administrator holds it; and a role of that
   name that can sign in is refused rather than changed.
