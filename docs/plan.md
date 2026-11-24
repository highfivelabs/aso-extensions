# Plan

Status, 2026-11-09: the repository has four kinds,
`dbforpostgresql.azure.hfl.dev/v1` `User` (ASO's PostgreSQL user with
Microsoft Entra principals, per-database privileges, schema and default
privileges, and database ownership),
`keyvault.azure.hfl.dev/v1` `VaultSecret` and `VaultCertificate`, and
`ManagementLock`, the first plain ARM kind (section 1: ARM resources are
in scope; hand-written as `authorization.azure.hfl.dev/v1` until
2026-11-15, generated as `v20200501` since). All pass their end-to-end tests against a fake
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

**ARM resources are in scope** (decided 2026-11-09): Azure API resources are
not forbidden; build them or take them from upstream, whichever is easier.
This replaces the earlier rule,
which sent every ARM gap upstream as configuration and never built one
here (open question 6, recommended then; the decision first made one
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

**`ManagementLock`, the first ARM kind.** Azure management locks were made
an exception on 2026-11-09. `Microsoft.Authorization/locks`
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
  to install. The generated ARM kinds lost ASO's webhook validations;
  patch 0016 (`ASO_GENERATOR_CEL_VALIDATIONS`, 2026-11-16, §8 ASOOPEN)
  puts the main ones back as CEL rules: `spec.owner` cannot change, a set
  `spec.azureName` cannot change, and an owner reference takes `name` or
  `armId` (`armId` alone, or `group`, `kind` and `name`, for an extension's
  owner). A rule on ASO's `ResourceReference` (the references inside a
  spec) is not added: one on a type used inside unbounded lists exceeds the
  CEL cost budget of the larger CRDs (the application gateway's total,
  measured with the API server's own estimate), so a malformed reference
  there is refused at reconcile, on the Ready condition. Unlike the
  webhook, the write-once rules hold before the resource exists in Azure
  (CEL cannot read the annotation that records it): a wrong owner is fixed
  by deleting the object and creating it again.
  `test/e2e/cel_validation_test.go` proves them.
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
  (`ASO_GENERATOR_API_STORAGE`). Later patches, each behind its own
  variable: nested `x-ms-client-flatten` (0004), children of a tenant scope
  resource (0005), resources at several scopes (0006, below) and child
  resources built on a `SubResource` (0007, below).
- **Resources at several scopes** (patch 0006, `ASO_GENERATOR_MULTI_SCOPE`).
  The specs define some resources once per scope: a policy definition at
  `/subscriptions/{s}/providers/Microsoft.Authorization/policyDefinitions/{n}`
  and at `/providers/Microsoft.Management/managementGroups/{g}/providers/...`,
  a maintenance configuration assignment at four scopes. ASO's generator keeps
  the first path it reads (map order, so the output can change from run to
  run) and refused every path that runs through a management group
  ("unexpected hard coded name"), so a definition was a subscription-only
  resource with no owner. With the patch, the resource a path defines is the
  one after its last `/providers/`, and a resource defined under two or more
  scopes is an extension resource (`/{scope}/providers/...`): its `owner` is
  an `ArbitraryOwnerReference` (our `ManagementGroup`, any ASO resource or an
  ARM ID), as ASO's `RoleAssignment` and our `TagSet` take. The kind accepts
  any owner; Azure refuses a scope the resource does not support (a policy
  definition at a resource group), and the refusal reaches the Ready
  condition. The kinds generated before are unchanged (`task check`
  regenerates them and finds no difference). This is one answer to the
  design question ASO issue #3756 waits on for locks, chosen here for our
  kinds; `ManagementLock` stays hand-written.
- **Child resources built on a `SubResource`** (patch 0007,
  `ASO_GENERATOR_SUBRESOURCE_RESOURCES`). ASO's generator takes a path as a
  resource when its GET returns a schema marked `x-ms-azure-resource` or with
  string `id`, `name` and `type`. The network specs marked their `SubResource`
  `x-ms-azure-resource` until their TypeSpec conversion (2025-05-01 on); since
  then a child built on a `SubResource` with a `name` and no `type`
  (ExpressRoute gateway connections, hub virtual network connections, hub
  route tables, VPN connections, NVA connections) is no resource, and
  `$exportAs` fails with "type not seen" (`$isResource` does not help: it
  changes a type after the paths are read). With the patch, a named schema
  built on a `SubResource` with a string `id` is a resource, as it was in
  2025-03-01. The kinds generated before are unchanged: the only difference
  in their files is shared types (`RoutingConfiguration`) moving to the
  first file, alphabetically, of a kind that uses them; their CRDs and
  `structure.txt` entries are the same.
- **Resources kept directly under a subscription** (patch 0009,
  `ASO_GENERATOR_SUBSCRIPTION_EXTENSION`). ASO's generator gives a path
  `/subscriptions/{subscriptionId}/providers/{namespace}/{type}/{name}`
  Location scope, which it cannot model (its own TODO): the kind has no
  owner and its ARM ID comes out as a resource group named after it (found
  by the activity log profile's test: the PUT went to
  `/subscriptions/…/resourceGroups/default`). With the patch it is an
  extension resource owned by the subscription's ARM ID, as budgets and
  tags are. No kind generated before had Location scope, so none changes.
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

  **Decided, 2026-11-10.** Per-version kinds (2) are preferred over a
  conversion webhook, and a second API version of a generated kind should
  be avoided in most cases: move a kind to a newer API version only when a
  field or behaviour needs it.
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
- **Extension resources on a management group** (Azure Policy, foundation row
  3). A policy definition, set definition, assignment or exemption is
  generated as an extension resource (patch 0006) whose owner may be our
  `ManagementGroup`: ASO's resolver builds
  `/providers/Microsoft.Management/managementGroups/{g}/providers/Microsoft.Authorization/...`
  from the group, and ASO's subscription check is skipped for an ID with no
  subscription. At a subscription or below, the check applies as for every
  kind: the scope must be in the credential's subscription.

### Hand-written extensions of generated kinds

ASO keeps a kind's hand-written behaviour (the nine extension interfaces:
`ARMResourceModifier`, `Claimer`, `Deleter`, ...) in
`api/<group>/customizations/*_extensions.go`, beside the generated
`*_extension_types_gen.go`. Here `hack/generate-arm.sh` deletes `apis/arm` on
every run, so such a file lives in `generator/_extensions/<group>/customizations/`
(mirroring `apis/arm`; the leading underscore keeps it out of `go build ./...`,
where it would not compile alone) and the script copies it into `apis/arm`
after the generator has written the package; `task check` regenerates and
finds the copy unchanged. What such a file needs from ASO's `v2/internal`
(the ARM client an `ARMResourceModifier` receives, `reflecthelpers.SetProperty`,
`randextensions`) is exported by patch 0003 (`v2/pkg/outoftree/extensions.go`),
as is ASO's `resolveBuiltInRoleDefinition` and the ARM error an
`ErrorClassifier` receives (`CloudError`). The first ones are the PIM
requests' (foundation row 5). The ExpressRoute kinds (row 9) added two more
sorts: an `ARMResourceModifier` that keeps a parent's child collections on its
PUT (a circuit's peerings and authorizations; ASO's `RouteTable` code and its
helpers, copied rather than imported, since importing ASO's network
customizations would build in its network API packages and the Azure SDK's
`armnetwork`), with `$resourceLifecycleOwnedByParent` on the collections so
the generator keeps them in the ARM payload only; and a
`KubernetesSecretExporter` that writes a key Azure makes to the Secret
`operatorSpec.secrets` names (`$azureGeneratedSecrets`), for which patch 0003
exports ASO's set type. Writing that Secret needs the manager to create
Secrets, which the default install does not grant (§8, question 9).

### Names that must be GUIDs

Some ARM resources take only a GUID as their name (a PIM schedule request,
as a role assignment). ASO gives an empty `azureName` a UUID in a defaulting
webhook (`stable`: a UUID v5 of the owner, group, kind, namespace and name,
unless the name already is a UUID; or `random`, by
`operatorSpec.namingConvention`); there is no webhook here, and without one
`AzureName()` falls back to `metadata.name`, which Azure refuses.

**Chosen (2026-11-09, with PIM): the reconciler fills an empty `azureName`
once, the first time it claims the object, and records it.** The kind's
`Claimer` extension (ASO's own extension point, run before the ARM ID is
first computed) runs the webhook's code unchanged and then ASO's claim; the
claim's commit (it adds the finalizer) writes `spec.azureName` to the object,
so every later reconcile, a restart of the manager or a change of the owner
sees the same name, and `random` is random once. Why not the alternatives:
- *Require users to supply a GUID* (as `metadata.name` or `azureName`): Kubernetes
  names are for people (`eligible-reader`); a GUID name is unreadable and easy
  to copy between objects by mistake, which would make two objects one Azure
  request. It stays possible: a name that already is a UUID is kept.
- *Compute it in `AzureName()` every time* (a generator patch): no record, so
  `random` cannot work and the name moves if the owner reference is edited.
- *A webhook*: the install has none on purpose (section 2).
The cost: until the first reconcile the object has no `azureName` (a client
that reads it before then sees empty), and an object created while the manager
is down is named when it starts. The UUID is ASO's algorithm over our group
(`authorization.azure.hfl.dev`), so it differs from what ASO would compute for
the same object; once recorded it moves with the object.

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

### The Azure SQL User

`sql.azure.hfl.dev/v1` `User`: ASO's Azure SQL `User` (`sql.azure.com`,
`localUser` only at the pin) with ASO pull request #5104's `aadUser`,
copied read-only from its head (`1219643c`, 2026-11-10), as the PIM kinds
were copied from #5638. The answer to open question 5: copy ASO pull
request #5104, fix it up and ship it.

- **What #5104 adds.** `spec.aadUser` (`alias`, `serverAdminUsername`)
  beside `localUser`, exactly one of them; the `aadUser` connector, which
  signs in to the database with an access token for
  `https://database.windows.net/` from the credential ASO would use for
  the object, runs `CREATE USER [name] FROM EXTERNAL PROVIDER` when the
  user is missing, sets the roles as for a local user, and drops the user
  on delete. The user name is the alias when given, else `azureName`.
- **Fixed up here.** (1) The webhook's checks (exactly one of `localUser`
  and `aadUser`; neither the user type nor `aadUser.alias` changes; nor
  `azureName`) are CEL rules on the CRD, as the PostgreSQL `User`'s are.
  (2) `azureName` falls back to `metadata.name` in code (#5104 relies on
  ASO's defaulting webhook, so without one its user name was empty).
  (3) `ConnectToDBUsingAAD` (ASO's, at the pin) built a DSN that left
  encryption at go-mssqldb's default; here it uses the same configuration
  as a password sign-in (encryption required, the server's certificate
  checked against its host name) with go-mssqldb's security-token
  connector. (4) `DropUser` is `DROP USER IF EXISTS`, so a user removed by
  hand no longer holds its object's deletion forever (this changes the
  password user too). (5) `aadUser.serverAdminUsername` is optional: #5104
  requires it and never reads it (a token names who signs in); it is kept
  so an object written for #5104 applies here unchanged.
- **Who signs in.** The identity ASO resolves for the object (the
  namespace's `aso-credential`, strict workload identity by default) must
  be the server's Microsoft Entra administrator or a member of its
  administrator group: only an Entra-authenticated connection may create
  Entra users (Azure's error 33159).
- **Owner.** ASO's `sql.azure.com` `ServersDatabase` (its hub,
  `v20250101`), read through the cache with its `Server` (for
  `status.fullyQualifiedDomainName`) and resource group, so all three are in
  `controllers.OwnerTypes()` and the manager may list and watch them.
- **Not built.** `WITH OBJECT_ID` (Azure's way to create a user for a
  principal whose display name is not unique); database-level permissions
  beyond the fixed roles; contained users for Entra groups by object ID.
  Upstream review of #5104 may change its shape; the kind moves to ASO's
  group when a release carries it (section 4).

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

### App settings and connection strings

`web.azure.hfl.dev/v20260715` `SitesConfigAppSettings` and
`SitesConfigConnectionStrings` (c4c; RECWEB (builder, 2026-11-15): "yes"
to a kind whose values can be Secret references).

- **Why by hand.** `sites/config/appsettings` and `.../connectionstrings`
  (and the same under `sites/slots`) have a PUT and a `POST .../list` and
  nothing else: no GET, no DELETE (2026-07-15 spec). ASO's generic ARM
  reconciler reads every resource back with a GET after its PUT (its
  `getStatus` needs GET or HEAD), so a generated kind would never become
  Ready (§8 Q13 says the same of `backup`), and the generator cannot make a
  map whose values are each a Secret reference. Patching ASO's reconciler
  to read with a POST would be a framework change for two resources. So
  they are hand-written kinds on ASO's generic reconciler with their own
  `genruntime.Reconciler` (`pkg/reconcilers/web`), as `VaultSecret` is:
  ASO's annotations, conditions, claim and credential, and a small Resource
  Manager client (`pkg/webconfig`) for the PUT and the POST, with the
  credential's cloud configuration and the manager's ARM transport (the
  tests' fake applies). The slot guard does not cover them: it guards a
  kind whose spec has `tags` and reads the holder's tag with a GET, and the
  sections have neither, so two slots could both write one app's settings
  during a move.
- **API version.** The ARM version of ASO's `Site` and of the group's
  generated kinds, `v20260715`, as §8 Q14 settled for a hand-built ARM
  kind; the Go package is `apis/web/v20260715` (outside `apis/arm`, which
  the generator rewrites), registering into the same group version.
- **Shape.** `owner` is a `Site` (by name, the default), our `SitesSlot`
  (`group: web.azure.hfl.dev, kind: SitesSlot`) or either by `armId`; the
  CRD refuses any other kind. `settings` (`connectionStrings`) is a list
  keyed by name; each item is exactly one of `value`, `valueFromSecret`
  (ASO's `SecretReference`) and `valueFromConfig` (ASO's
  `ConfigMapReference`, for a value another object exports, such as an ASO
  `Component`'s connection string), checked by CEL; a referenced value may
  have a `template`, text holding `$(value)` where it goes (Kubernetes'
  `$(VAR)` syntax), for a value only partly secret: the storage connection
  string AVM's web-site module writes around an access key
  (`AzureWebJobsStorage`, a Logic App's content share); a connection string
  also carries its `type`, the spec's enum. The status holds the section's
  ARM ID and the names (and types) Azure lists, never a value; values are
  never logged.
- **The whole set.** Each reconcile lists the section and PUTs the desired
  set only when it differs; a PUT replaces every setting, so a setting
  added elsewhere (the portal, a deployment tool) is removed at the next
  reconcile, as Terraform's `app_settings` does. A change to a named Secret
  or ConfigMap reconciles again (indexes and watches, as `VaultSecret`'s).
- **Two writers.** ASO's `Site` (and our `SitesSlot`) carry
  `siteConfig.appSettings` and `siteConfig.connectionStrings` in plain
  text; Azure writes them to the same sections. ASO sends them only when
  they are set, so a user picks one writer per app: these kinds, with the
  two `siteConfig` fields left out. Two objects of one kind on one app
  fight the same way. Neither is detected.
- **Delete.** Azure has no DELETE for the sections, which live as long as
  the app; deleting the object makes no call and leaves the last values
  (the generated config sections' `Deleter`,
  `site_config_extensions.go`). To clear them, set an empty list first.
- **Not built:** a slot's sticky names are `SitesConfigSlotConfigNames`
  (c4b); static sites' app settings (`staticSites/.../config/appsettings`)
  and `azurestorageaccounts`, `pushsettings`, `metadata` are left out.

### ManagementLock

Generated since 2026-11-15 (section 8 Q10, answered 2026-11-13 and again
2026-11-15 as EXTQ-ASO-Q10-1): `authorization.azure.hfl.dev/v20200501`
`ManagementLock`, from `Microsoft.Authorization/locks` 2020-05-01
(`specification/resources`), `$exportAs: ManagementLock` in
`generator/azure-arm/authorization.yaml`. The spec defines a lock at four
scopes, so patch 0006 makes it an extension resource
(`/{scope}/providers/Microsoft.Authorization/locks/{lockName}`) reconciled by
ASO's generic reconciler. The hand-written `authorization.azure.hfl.dev/v1`
kind, its reconciler (`pkg/reconcilers/authorization`) and its Resource
Manager client (`pkg/armlocks`) are removed. Nothing was deployed, so no
`v1` object was migrated; the catalog's `aso.lock` helper moved to
`v20200501` in the same window, after this repository.

- The spec keeps the `v1` fields and names: `azureName`, `owner` (ASO's
  `ArbitraryOwnerReference`: an object by group, kind and name, or an ARM
  ID), `level`, `notes`, `owners` (application IDs; ASO issue #3756 is still
  open). The status is Azure's lock as read back (`id`, `level`, `notes`,
  `owners`, `systemData`); `v1`'s `status.scope` is gone (the owner's ARM ID
  is in the lock's `id`).
- **Owners by name** are resolved by ASO's resolver, which knows only the
  kinds the manager registers: our own kinds and the ASO kinds in
  `controllers.OwnerTypes()`. `v1` read any ASO kind as an unstructured
  object; to keep the catalog's charts working, the ASO kinds their locks
  name were added to `OwnerTypes()` (network: `AzureFirewall`,
  `BastionHost`, `DdosProtectionPlan`, `FirewallPolicy`, `LoadBalancer`,
  `NatGateway`, `NetworkInterface`, `NetworkSecurityGroup`,
  `PrivateEndpoint`, `PublicIPAddress`, `RouteTable`, `VirtualNetwork`,
  `DnsResolver`, `DnsForwardingRuleset`; web `ServerFarm`; sql
  `ServersElasticPool`; compute `Disk`; cache `Redis`), with `list` and
  `watch` in `config/rbac/role.yaml` and their test CRDs. Any other ASO
  kind is named by `armId`. An owner kind whose CRD ASO's install left out
  (its `crdPattern`) is skipped at start (logged) instead of stopping the
  manager.
- Lost against `v1`, all admission checks the generator does not write:
  `azureName`'s pattern and length, `notes`' 512 characters (Azure refuses
  both), and `level` now also accepts Azure's `NotSpecified`. The CRD still
  requires `owner` and `level`.
- Unchanged: the credential is the one ASO picks for the object, and ASO's
  subscription check refuses a scope in another subscription. A
  `CanNotDelete` lock on what ASO manages makes ASO's delete of it fail with
  `ScopeLocked`, retried until the lock goes; `ReadOnly` also stops ASO's
  updates. The owner is the lock's Kubernetes owner.
- Proven: `TestManagementLockOnResourceGroup`, `TestManagementLockByARMID`,
  `TestManagementLockRefused`, `TestManagedEnvironmentsDaprComponent` (a lock blocking a delete),
  the conformance test, and the operator e2e (`team-a`'s lock).

### VaultsProtectedItem

`recoveryservices.azure.hfl.dev/v20260701` `VaultsProtectedItem`: backup
turned on for a virtual machine (c24; ASOQ Q14, builder, 2026-11-15, "as
recommended": by hand, in the generated Recovery Services kinds' group and
version, so it reads like them).

- **Why by hand.** The ARM ID is
  `.../vaults/{v}/backupFabrics/Azure/protectionContainers/{c}/protectedItems/{i}`.
  `backupFabrics/Azure` is a fixed segment, no resource, so ASO's generator
  makes the resource group the owner and the object cannot name its ARM ID
  (handoff, "a path with a fixed non-resource segment"). The kind's own
  `Claim` builds the ID instead.
- **On ASO's framework.** A hand-written `genruntime.Reconciler` on ASO's
  generic reconciler, as `VaultSecret` is: ASO's `ClaimResource` (waits for
  a Ready vault, sets the owner reference), ASO's resolver for the
  references, ASO's ARM client and connection factory (the generated kinds'
  own, so the credential is the one ASO picks for the object and every
  request goes through the same HTTP client), ASO's subscription check, and
  ASO's poller annotations (`serviceoperator.azure.com/poller-resume-token`
  and `-id`) for a pending operation. ASO's generic ARM reconciler itself
  was not usable: it computes the ID from the owner chain on every PUT.
- **Spec.** `owner`: our `Vault` (or a vault's ARM ID).
  `sourceResourceReference`: the VM, ASO's `compute.azure.com` or our
  `compute.azure.hfl.dev` `VirtualMachine` by group, kind and name, or an
  ARM ID. `policyReference`: our `VaultsBackupPolicy` or an ARM ID,
  required unless `protectionState` is `ProtectionStopped` (a CEL rule).
  `protectionState`: `Protected` (by default), `BackupsSuspended` or
  `ProtectionStopped` (stop and keep the data). `diskExclusionProperties`:
  `diskLunList` and `isInclusionList` (which data disks; managed only when
  given). `containerName` and `azureName` (the item): derived from the VM
  when empty, `iaasvmcontainer;iaasvmcontainerv2;<rg>;<vm>` and
  `vm;iaasvmcontainerv2;<rg>;<vm>`, written into the spec at the claim (as
  the PIM kinds record a GUID name), so they never move; both are
  immutable, as are `owner` and `sourceResourceReference` (CEL rules). A
  source that is not a `Microsoft.Compute/virtualMachines` is refused at
  the claim.
- **What Azure does on PUT** (the 2026-07-01 spec, `bms.json`, and its
  examples; Azure's REST guide "Back up an Azure VM using REST API"):
  `ProtectedItems_CreateOrUpdate` is long-running (202 with
  `Azure-AsyncOperation` `.../operationsStatus/{id}` and `Location`
  `.../operationResults/{id}`); the guide's first step is discovery,
  `POST .../backupFabrics/Azure/refreshContainers` (long-running, 202 and a
  `Location`), after which the VM's protection container
  (`IaasVMContainer;iaasvmcontainerv2;<rg>;<vm>`) is there to protect it in;
  Azure registers that container itself (`ProtectionContainers_Register` is
  for storage accounts and workloads, not VMs). So the reconciler refreshes
  once before an item's first PUT, follows the refresh, then PUTs
  `properties.protectedItemType: Microsoft.Compute/virtualMachines`,
  `sourceResourceId`, `policyId`, `protectionState` (when given) and
  `extendedProperties.diskExclusionProperties`. A change PUTs again; the
  item is compared with what Azure reports first (a state left out is met
  by `Protected`, `IRPending` before the first backup, or
  `ProtectionError`, which a PUT would not mend).
  The catalog's source, Azure Verified Module
  `avm-res-compute-virtualmachine` v0.21.0 (`main.backup.tf`), PUTs the
  same names and body (API 2024-10-01) with no refresh, so Azure takes a
  PUT without one at least for a machine it already knows; the refresh
  costs one long-running call and covers a machine made moments before.
- **Delete.** `ProtectedItems_Delete` (202, followed through the vault's
  `backupOperations`) stops protection and deletes the backup data, from
  the ARM ID recorded at the claim, so it works with the vault object
  gone; with the vault's soft delete on (Azure's default) the data stays
  soft-deleted for the retention days (14 by default) and the vault cannot
  be deleted until then: a chart that deletes a vault with its items waits
  on Azure (to keep the data, `reconcile-policy: detach-on-delete`, or
  `protectionState: ProtectionStopped` first).
- **Not built.** Other item types (Azure file shares,
  `AzureFileShareProtectedItem`, go on this kind later, with the storage
  account's container registered by `ProtectionContainers_Register`; SQL
  and SAP HANA in a VM), on-demand backups and restores (actions, not
  resources), the protection intent (auto-protection of workloads), and a
  VM's `Microsoft.ClassicCompute` items.

### Moving resources between slots

Hosted Bundt runs its infra clusters as *slots* (current, previous), each
running one version of every provider, and moves an organisation's objects
from one slot to another with one sequence: pause or release the object on
the old slot, adopt the resource on the new one, check there is no
difference, delete the old object. It treats ours and ASO's alike, so ours
use ASO's names for each step (checked 2026-11-12, at the pin).

**Release, adopt, pause, per kind family.** Every kind here runs on ASO's
generic reconciler (`pkg/manager`, `outoftree.RegisterAll`), which reads
`serviceoperator.azure.com/reconcile-policy` (on the object, else its
namespace, else ASO's default) before calling a kind's reconciler
(`third_party/azure-service-operator/v2/internal/reconcilers/generic/generic_reconciler.go`,
`createOrUpdate` and `delete`): `skip` calls neither CreateOrUpdate nor
Delete (it reads the status only) and lets the object go without a delete;
`detach-on-delete` reconciles as usual and lets the object go without a
delete. So pause and release were there for every kind before this work;
nothing was added.

| Family | Release | Adopt | Pause | Where |
|---|---|---|---|---|
| Generated ARM kinds (`apis/arm/**`) | had: `detach-on-delete`, `skip` | had: ASO's PUT of the spec onto the existing resource | had: `skip` | `pkg/controllers/controllers.go` (`generatedStorageTypes`, ASO's `NewAzureDeploymentReconciler`) |
| Kinds Azure cannot delete (PIM requests, managed HSM keys, SOA record sets) | had: `detach-on-delete` is required to let the object go | had | had | conformance fixtures, `test/e2e/arm_pim_test.go` |
| ManagementLock (hand-written, ARM) | had | had: PUT of the lock | had | `pkg/reconcilers/authorization` |
| VaultSecret, VaultCertificate (Key Vault data plane) | had, and `deleteMode: detach` is their default | had: an existing secret or certificate is read and written only where it differs | had | `pkg/reconcilers/keyvault` |
| PostgreSQL User, Azure SQL User (SQL) | had | had: an existing role or user is altered, not made again | had | `pkg/reconcilers/postgresql`, `pkg/reconcilers/azuresql` |
| DirectoryRoleAssignment (Microsoft Graph) | had | had: `adoptionPolicy` (AdoptOrCreate, the default; AdoptOnly) | had | `pkg/reconcilers/entra` |

**The slot guard (added).** Off unless the manager is given a slot:
`--slot=<name>`, else `HFL_SLOT`; neither, and nothing changes. With a
slot, `pkg/slot` sits under every Resource Manager and Key Vault request
(a `http.RoundTripper` in the ARM connection factory's and the Key Vault
client's transport) and around every kind's reconciler. For a kind whose
cloud resource carries tags (its spec has a `map[string]string` `tags`),
each write (PUT, PATCH, DELETE, a certificate import) is preceded by a GET
of the same resource:

- no `hfl-dev-slot` tag, or this slot's: the write is sent, with
  `hfl-dev-slot: <slot>` added to its tags (create and adopt claim it);
- another slot's tag: the write is not sent. The reconciler gets a 409
  `SlotMismatch`, the object's Ready condition is False with reason
  `SlotMismatch` (severity Warning, retried slowly), and a Warning event
  `SlotMismatch` says `cloud resource is held by slot <x>; this controller
  is slot <y>`. Reads are never refused. A delete is refused the same way,
  so the object stays until it is released (`skip`);
- the slot the object names in `hfl.dev/slot-takeover`: the write is
  sent and the resource moves to this slot. This is the adopting half of
  a move; without the annotation a slot never takes another's resource.
  The name and meaning are config-connector-extensions' (aligned
  2026-11-12). Adding, changing or removing it reconciles the object at
  once (`slot.TakeoverChanged`, in every kind's predicate), and a refused
  write is an error ASO retries, so a refused object takes the resource
  over when it is given the annotation later.

A slot's own tag is removed from what its reconciler reads, so it is never
a difference between spec and Azure (status shows the user's tags only).
Another slot's tag is left, so a reconciler that writes only on a
difference (VaultSecret) sees one on a takeover and writes, which claims
the resource. The tag name is not the label's because Azure tag names
cannot hold `/`. On each successful create or update the object gets the
label `hfl.dev/slot: <slot>`.

The move, with the guard: annotate the old object `reconcile-policy: skip`;
create the new object with `hfl.dev/slot-takeover: <old slot>`; when
it is Ready, compare (the resource's `systemData.createdAt` or a secret's
version shows it was not made again); delete the old object (with `skip`
no delete is sent); remove the takeover annotation at leisure (it only
widens what the new slot may write).

**What the guard cannot cover.** A resource without tags cannot hold the
lease, so for these kinds the guard does nothing (they are still labelled)
and Bundt must rely on the pause alone, pausing the old slot's object
before the new one is created and never unpausing it:

- generated ARM kinds whose spec has no resource tags: child and
  extension resources mostly (the `tags=0` kinds: policy definitions,
  assignments and exemptions, PIM requests and policy assignments,
  budgets, cost exports and scheduled actions, Security Center settings
  and standards, Sentinel alert rules, network manager configurations,
  groups and rules, ExpressRoute and VPN children, route filter rules,
  query pack queries, whose `tags` are lists of labels, management groups
  and their settings and subscriptions, and the rest of `apis/arm` whose
  spec lacks `tags`); `slot.Taggable` decides, per kind, from the Go type;
- ManagementLock (a lock has no tags);
- PostgreSQL User and Azure SQL User (database roles);
- DirectoryRoleAssignment (Microsoft Graph has no tags).

Also not guarded: POST actions other than a certificate import (listKeys
and other actions). Any other write is checked against its own target's
tag, so a write an extension makes to another resource is guarded by that
resource's holder, not the object's.

**Proven offline** (`task test:slot-move`, `test/slotmove`): two managers
wired as `cmd/manager`, slots blue and green, each watching its own
namespace, against one fake Resource Manager and one fake Key Vault.
For IpGroup and QueryPack (generated, tagged), QueryPacksQuery (generated,
no tags) and VaultSecret (hand-written, data plane): blue makes the
resource and holds it; blue is paused; green takes it over and holds it,
with Azure unchanged but the tag and the resource not made again (same
`createdAt`, same secret version, no DELETE); blue's object is deleted and
the resource stays; a blue object for it again is refused (condition,
event, no write reaches Azure), and so is its deletion; green deletes it.
Without the takeover annotation green is refused a resource blue holds.

**Proven live** (`TestLiveSlotMove`, run `ax1209571e99`, 2026-11-12,
docs/live-runs.md): the same sequence on a real IP group, with
Resource Manager's `createdTime` as the witness (IP groups return no
`systemData`).
`pkg/slot`'s unit tests cover the flag and variable, the tag on create and
update, the refusals, the takeover and the pass-through. Both managers run
one build; two releases side by side (the previous release beside main)
would need the previous release built first, and it has no guard.

## 3. Candidates

Ranked by how much an application stack on Azure needs them and how cheap
they are to carry. "Built" means in this repository with tests.

Tier 3 (the builder's application primitives, the tier 3 rows of
`docs/coverage/priority.csv`, and the services no row names) is ranked in
`docs/coverage/foundation.md`, "Tier 3: application primitives" (t1 to t21,
2026-11-24); its rows are recorded in the table under (c) as they are built,
f61 on.

(a) Upstream has code or an open pull request (read-only `gh`, 2026-11-07;
rechecked 2026-11-08, see below):

| # | What | State | Move |
| --- | --- | --- | --- |
| a1 | Azure SQL Entra users (`sql.azure.com` `User` with `aadUser`) | PR [#5104](https://github.com/Azure/azure-service-operator/pull/5104), open, last updated 2026-09-28 (rechecked 2026-11-10, read-only: head `1219643c`, review required; not in `main` or a release, v2.22.0 still the latest) | **built** (open question 5, answered 2026-11-10: copy it and ship it): `sql.azure.hfl.dev/v1` `User`, ASO's Azure SQL `User` at the pin with #5104's `aadUser` copied in and fixed up (section 2, "The Azure SQL User"); proven on a fake Azure SQL server that speaks TDS (`pkg/fakecloud/azuresql`), since no local server runs `FROM EXTERNAL PROVIDER` |
| a2 | Defender for Storage settings | PR #5783, open | ARM, configuration upstream; no kind here |
| a3 | Privileged Identity Management | PR #5638, open | not an application need |
| a4 | Databricks | PR #5028, draft | not an application need |
| a5 | main since v2.22.0 | only ARO-HCP 2026-10-01 (#5806) | nothing to copy |

(b) Not in ASO:

| # | What | Why an app needs it | Move |
| --- | --- | --- | --- |
| b1 | PostgreSQL user bound to an Entra principal | an app's managed identity signs in to its database without a password; ASO's MySQL user can, its PostgreSQL user cannot | **built**; offered upstream as [ASO PR #5845](https://github.com/Azure/azure-service-operator/pull/5845) (closes #1399), opened for review 2026-11-16 |
| b2 | PostgreSQL privileges per database | an app's user needs `CONNECT`/`CREATE` on its own database | **built**; ASO issue #4694 is open and its maintainers say they still want it; offered upstream as [ASO PR #5844](https://github.com/Azure/azure-service-operator/pull/5844): CLA signed, ready for review (2026-11-16) |
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
| c4b | App Service config sections and publishing policies (c4's "not built"; the catalog's `avm-res-web-site`: `auth_settings_v2`, `logs`, `sticky_settings`, `ftp_publish_basic_authentication_enabled`, `scm_publish_basic_authentication_enabled`, and `auth_settings`, `backup`) | rechecked 2026-11-13, read-only: ASO's `web` group at the pin, on `main` and in v2.22.0 exports `Site`, `ServerFarm` and `SitesSourcecontrol`; no open pull request or issue adds a config section or publishing policy | **built**, generated: `web.azure.hfl.dev/v20260715` `SitesConfigAuthSettingsV2`, `SitesConfigLogs`, `SitesConfigSlotConfigNames`, `SitesBasicPublishingCredentialsPolicyFtp`, `SitesBasicPublishingCredentialsPolicyScm`, owned by ASO's `Site`. **Generator patch 0014** (`ASO_GENERATOR_FIXED_NAMES`, `generator/fixed-names.txt`): the generator took a path's literal last segment for a resource only when it was `default`, `current` or `web` (so only `config/web` came out, c4); a listed literal now makes its own resource of the parent's ARM type, its name fixed to the literal. A log destination's SAS URL is a Secret reference (`$importSecretMode: required`) and removed from the status. Azure has no DELETE for any of them (they live as long as the site): a hand-written `Deleter` (`generator/_extensions/web/customizations/site_config_extensions.go`, as the SOA record's) completes the delete without a call and leaves the last values in Azure. **Not built: `authsettings` (v1) and `backup`**: each has a PUT and no GET (they are read with `POST .../list`), and ASO's reconciler reads a resource back with a GET after its PUT; a hand-written extension that reads them with the POST would do it (open question 13). `appsettings`, `connectionstrings`, `metadata`, `azurestorageaccounts` and `pushsettings` are left out too (PUT-only; ASO's `Site` carries the first three inline in `siteConfig`), as are a slot's sections. Proven: `TestSiteConfigSections` and the conformance test |
| c4c | App Service app settings and connection strings with Secret references (c4b's "left out"; the catalog's `avm-res-web-site`: `application_insights_connection_string`, `application_insights_key`, `storage_account_access_key`, `slot_sensitive_app_settings`, `connection_strings`, the Logic App settings that carry the storage key; catalog PR #213) | rechecked 2026-11-15, read-only: ASO v2.22.0 is the latest release; `main` (`60fdf3e9`, 11 commits past the pin) changes only `property-changes.md` files under `api/web`; no open pull request or issue adds app settings, connection strings or Secret references for them; ASO's `Site` takes both in plain text in `siteConfig` | **built**, by hand (RECWEB (builder, 2026-11-15)): `web.azure.hfl.dev/v20260715` `SitesConfigAppSettings`, `SitesConfigConnectionStrings`, owned by ASO's `Site` or our `SitesSlot`, each value plain or from a Secret or ConfigMap, the whole set PUT and read back with `POST .../list` (no GET: a generated kind would never be Ready, §8 Q13); deleting leaves the values (no DELETE). §2 "App settings and connection strings". Proven: `TestSiteAppSettingsAndConnectionStrings`, `TestWebConfigSections` (the fake), the operator e2e. Not built: `backup` (§8 Q13, built when a chart needs it: `avm-res-web-site`'s `backup` is such a chart, next), static sites' app settings |
| c5 | Log Analytics children (tables, data exports, linked services, linked storage, query packs, solutions) | not at the pin, no pull request (rechecked 2026-11-09: v2.22.0 still the latest release, `main` three documentation commits past the pin, no open pull request or issue for any of them; ASO exports only `Workspace`, whose newest version is 2025-07-01) | **built**, generated (`operationalinsights.azure.hfl.dev`, API 2025-07-01, ASO's own `Workspace` version): `WorkspacesTable` (plan, retention, schema, restored logs and search results), `WorkspacesSavedSearch`, `WorkspacesLinkedService` (an Automation account or a cluster), `WorkspacesLinkedStorageAccount`, `WorkspacesDataExport` (added with c10), `QueryPack` (owned by the resource group) and `QueryPacksQuery` (owned by our pack). Data exports (`WorkspacesDataExport`) were first left out because ASO's generator flattens one level only (the spec nests `destination` and `metaData` inside `properties`) and were added with patch 0004 (see the c10 row). Not built: **solutions** (`Microsoft.OperationsManagement`, a 2015 preview API), because its `SolutionProperties` lacks `type: object` and the generator drops it as an empty type, leaving no `workspaceResourceId`; **data sources** (the generator names the receiver `source` and the storage conversions do not compile; they configure the retired Log Analytics agent), **storage insight configs** (legacy), **summary logs**, **clusters** (dedicated clusters, a capacity commitment, not an application need) and **network security perimeter configurations** (read-only). A linked storage account's name is one of five values; the generator keeps such an enum in the storage version, which then imports the API version (a cycle), so `azureName` is a plain string there and Azure refuses a wrong one |
| c6 | Azure Monitor private link scope and scoped resources; network security perimeter associations | not at the pin, no pull request (rechecked 2026-11-09: v2.22.0 still the latest release, `main` three documentation commits past the pin, no open pull request; ASO's `insights` and `network` groups export neither) | **built**, generated: `insights.azure.hfl.dev/v20210901` `PrivateLinkScope`, `PrivateLinkScopesScopedResource` (a workspace, component or endpoint scoped into it, an ARM reference) and `PrivateLinkScopesPrivateEndpointConnection` (approving or rejecting a connection); `network.azure.hfl.dev/v20250501` `NetworkSecurityPerimeter`, `NetworkSecurityPerimetersProfile`, `...ProfilesAccessRule`, `...ResourceAssociation` (a PaaS resource and a profile, both ARM references, in `Learning`, `Enforced` or `Audit`), `...Link` and `...LoggingConfiguration`. An association needs a perimeter and a profile, which ASO does not have either, so the whole family is built (owners: the resource group, then our kinds). Version choices: 2021-09-01 is the newest stable private link scope API; the perimeter is at 2025-05-01 and not 2025-03-01 (ASO's network version), because at 2025-03-01 the generator drops every writable property of these kinds from the spec side (a perimeter without `location`, an association without `profile`) and nothing says why. Not built: the Log Analytics workspace's own perimeter configurations (`workspaces/networkSecurityPerimeterConfigurations`, read-only, in c5's skip list) |
| c7 | Key Vault keys | `VaultKey` merged in ASO (#5564), next release | skip: wait for ASO's release |
| c8 | Management groups, Azure Policy, budgets, vWAN hub connections, subscription tags | management groups #2249 open, no pull request | now `docs/coverage/foundation.md` rows 1 to 10. **Management groups built** (row 1), generated: `management.azure.hfl.dev/v20230401` `ManagementGroup` (tenant scope, no owner; `displayName`, parent by object or ARM ID), `ManagementGroupsSubscription` (a subscription's placement; `azureName` is the subscription ID; owned by the group) and `ManagementGroupsSetting` (`settings/default`: default group for new subscriptions, authorization to create top-level groups; owned by the group). Version 2023-04-01, the newest stable. **Generator**: patch 0005 makes a child of a tenant scope resource an owned resource (the generator called it Tenant scope, with no owner, so the child's name was taken for the group's); `children` of a group's status is dropped at the second level (directly recursive, controller-gen cannot unroll it). Not built: service groups (preview only), the group's `descendants`, `entities` and `tenantBackfill` actions (read or POST) |
| c8b | Subscription tags, billing aliases (foundation row 2) | tags: none at the pin, no pull request (rechecked 2026-11-09); billing aliases: none | **`resources.azure.hfl.dev/v20250401` `TagSet` built**, generated (`Microsoft.Resources/tags/default`, an extension resource: `owner` is an ASO resource or an ARM ID, `properties.tags`; the generator names the resource `Tag` and cannot rename it to `Tags` because that type exists, hence `TagSet`; the other Resources API types (deployments, templates, resource groups) are not exported). A TagSet PUTs the whole set: it replaces the tags of its scope. **Not built**: `BillingSubscriptionAlias` (`Microsoft.Billing/billingAccounts/.../billingSubscriptionAliases`): ASO's `subscription/Alias` already creates a subscription under a billing scope (EA, MCA), and the Billing alias is a second route to the same result under a billing account that is no kind; `Microsoft.Subscription/policies` (tenant-wide subscription policy, 3 attributes; ASO's `subscription` group has no kind for it) is left for a later session |
| c8c | Azure Policy: definitions, set definitions, assignments, exemptions (foundation row 3) | rechecked 2026-11-09, read-only: v2.22.0 still the latest release; `main` four documentation commits past the pin (#5826, #5827, #5828, #5769); no open or draft pull request adds a policy kind (the open ones are #5829, #5821, #5820, #5814, #5804, #5783, #5780, #5773, #5768, #5693, #5659, #5638, #5104, #5028, #4210); issues #4975 and #2767 are open (a maintainer, 2026-07: "Definition I think is recursive which can cause us issues"; it was not, for the generator, once `policyRule` and `metadata` are free-form JSON) | **built**, generated: `authorization.azure.hfl.dev/v20260701` `PolicyDefinition`, `PolicySetDefinition`, `PolicyAssignment` (the newest stable API) and `v20260101preview` `PolicyExemption` (exemptions exist only in preview versions; the newest). Each is an extension resource (patch 0006): owner our `ManagementGroup`, an ASO resource or an ARM ID. References are `$referenceType: arm`, so a set names its definitions, an assignment its definition or set, and an exemption its assignment by Kubernetes object or ARM ID (`policyDefinitionReference`, `policyAssignmentReference`; a built-in by ARM ID), as is a group's `additionalMetadataId`. `MissingTokenAction` (audit or deny) is marked not a secret. Hand-written code: none (the generated share is 100% of the kind code; the fake's policy rules, tests and configuration are ours). Not built: definition and set definition **versions** (`.../versions/{v}`, read-mostly snapshots of a definition), policy **variables** and **values** (preview), **enrollments** (preview), and remediation (row 4, `Microsoft.PolicyInsights`) |
| c8d | Policy remediations and attestations (foundation rows 4 and 21) | rechecked 2026-11-09, read-only: v2.22.0 still the latest release; `main` four documentation commits past the pin (#5826, #5827, #5828, #5769); no open or draft pull request adds a Policy Insights kind; issue #2767 ("definition, assignment, and remediation") is open with no pull request; nothing asks for attestations | **built**, generated: `policyinsights.azure.hfl.dev/v20241001` `PolicyRemediation` and `PolicyAttestation` (2024-10-01, the newest stable Policy Insights API, one TypeSpec file holding both). Both are extension resources (patch 0006; the spec has a remediation at a management group, a subscription, a resource group and a resource, an attestation at the last three): owner our `ManagementGroup`, an ASO resource or an ARM ID. The assignment is a reference (`policyAssignmentReference`, our `PolicyAssignment` by object or any by ARM ID), as are a remediation's `filters.resourceIds` (`resourceReferences`). The attestation's own `owner` property (the person responsible, an Entra object ID) collides with the Kubernetes `owner`, so ASO's generator names it `properties_owner` in the CRD (`PropertiesOwner` in Go, `owner` on the wire); left as ASO names it. Built together because they share the API version, the group and the fake's rules; attestations were row 21. Hand-written code: none (the generated share is 100% of the kind code). Not built: the remediation's `cancel` action (a POST; deleting the object deletes the remediation) and its deployments (read-only); policy states, events, metadata and restriction checks (read or POST only) |
| c8e | Privileged Identity Management (foundation row 5) | rechecked 2026-11-09, read-only: v2.22.0 still the latest release; `main` four documentation commits past the pin (#5826, #5827, #5828, #5769); open pull request **#5638** ("Add support for Privileged Identity Management in ASO workflow", head `5d0e686e`, unchanged since 2026-10-16, review required) adds `RoleEligibilityScheduleRequest` at 2020-10-01; issue #4335 (PIM) is open; nothing upstream for the assignment request or the policy assignment | **built**: `authorization.azure.hfl.dev/v20201001` `RoleEligibilityScheduleRequest` (**copied from #5638**: its generator configuration as is, its `ARMResourceModifier` that resolves `wellKnownName: Reader` to the built-in role's ID by listing role definitions, and its webhook's name defaulting, run as a `Claimer` here, section 2 "Names that must be GUIDs"), `RoleAssignmentScheduleRequest` (configured and extended the same way: activation, deactivation, admin assignment) and `RoleManagementPolicyAssignment` (generated; `policyReference` and `roleDefinitionReference` by ARM ID; its name is `{policy GUID}_{role GUID}`, which the user gives as `azureName`, as Azure says). All three are extension resources (owner any ASO resource, our `ManagementGroup` or an ARM ID). A request cannot be deleted in Azure (the spec has no DELETE, so ASO's reconciler sends none and reports `DeletionNotSupportedInAzure`): the object needs `reconcile-policy: detach-on-delete` to go, and an eligibility or assignment is undone with another request, as #5638's own test does. Generated share: the kind code is generated but for two hand-written extension files (235 lines, 115 of them a copy of the other with the type changed) against 11,724 generated lines, tests aside, of `apis/arm/authorization/v20201001` and the extension types (98% generated). Not built: the role management **policies** themselves (Azure makes one per scope and role and takes only PATCH, which ASO's generator does not model), the requests' `cancel` and `validate` actions, the 2024-09-01-preview API (approval and conditions on the newer fields), and Entra directory roles (`Microsoft.Graph`, not ARM). A built-in role by name needs a subscription in the request's scope (ASO builds `/subscriptions/{s}/providers/.../roleDefinitions/{id}`), so at a management group name the role by ARM ID |
| c8f | Budgets and cost exports (foundation row 6) | rechecked 2026-11-09, read-only: v2.22.0 still the latest release; `main` four documentation commits past the pin (#5826, #5827, #5828, #5769); no open or draft pull request adds a Consumption or Cost Management kind (the open ones are #5829, #5821, #5820, #5814, #5804, #5783, #5780, #5773, #5768, #5693, #5659, #5638, #5104, #5028, #4210); no issue asks for budgets or exports; ASO's `azure-arm.yaml` prunes the whole `costmanagement` group ("QueryFilter is a self-referential, infinitely recursive type") and has no `consumption` configuration | **built**, generated: `consumption.azure.hfl.dev/v20260601` `Budget` (`Microsoft.Consumption/budgets`, the one the subscription-vending module uses) and `costmanagement.azure.hfl.dev/v20260601` `Export` (2026-06-01, the newest stable of each). Both are `/{scope}/providers/...` in the spec, so extension resources without patch 0006: owner any ASO resource, our `ManagementGroup` or an ARM ID (a billing scope only by ARM ID). A notification's contact groups are references (`contactGroupsReferences`, Monitor action groups by ARM ID; ASO's `ActionGroup` is not in the manager's scheme), as is an export's storage account (`resourceReference`); an export's `sasToken` (`x-ms-secret`) is a Secret reference; `storageAccount` (a name) is marked not a reference. Cost Management's query and view filters (`QueryFilter`, `ReportConfigFilter`) are directly recursive and stop the generator at stage 12 even unexported, so they are pruned by name (neither kind uses them) rather than the whole group. A run in an export's `status.runHistory` loses its `runSettings` (the export's own properties again, holding `runHistory`: recursive, which overflowed the generated tests and left an untyped field in the CRD), by a `typeTransformers` `remove: true`. The kind is `Export`, as the spec names it, in group `costmanagement` (the queue proposed `CostExport`; `Export` was kept, 2026-11-10). Hand-written code: none (the generated share is 100% of the kind code: 16,590 lines, tests aside; the configuration, the fake's rules and the tests are ours). Not built: `Microsoft.CostManagement/budgets` (the same budget under the other API, which also takes cost-allocation and reservation-utilization budgets), views, scheduled actions (alerts and scheduled emails), settings, markup and cost allocation rules; an export's `run` action and run history (read-only) |
| c8g | Site-to-site VPN connections and local network gateways (foundation row 8) | rechecked 2026-11-09, read-only: v2.22.0 still the latest release (an `experimental` pre-release beside it); `main` four documentation commits past the pin (#5826, #5827, #5828, #5769); ASO issue #4486 ("Support Virtual Network Gateway Connections") open, 3 comments, no pull request; no open pull request adds either kind; ASO's `network.yaml` exports `VirtualNetworkGateway` (2025-03-01) and no connection or local gateway | **built**, generated: `network.azure.hfl.dev/v20260301` `VirtualNetworkGatewayConnection` (`Microsoft.Network/connections`; the generator's resource is `Connection`, exported under the spec's model name, the queue's name, since `Connection` alone says nothing) and `LocalNetworkGateway` (`localNetworkGateways`), from `networkGateway.json` of 2026-03-01, the newest stable (ASO's own network kinds are 2025-03-01; a connection names its gateways by reference, so the versions need not match). Both are owned by a resource group. The gateways a connection names (`virtualNetworkGateway1`, `virtualNetworkGateway2`, `localNetworkGateway2`) are whole gateway objects in the spec; the generator reduces each to a reference (`reference`), so ASO's `VirtualNetworkGateway` is named by ARM ID and our `LocalNetworkGateway` by object or ARM ID; `SubResource.Id` (the peer, NAT rules, route tables of `routingConfiguration`) is labelled `$referenceType: arm`. `sharedKey` (`x-ms-secret`) is a Secret reference; `authorizationKey` (an ExpressRoute circuit authorization's key) is made one too (`$importSecretMode: required`, as Terraform marks it sensitive), so neither reaches the status. Hand-written code: none (the generated share is 100% of the kind code: 15,817 lines, tests aside, 289 of them in the 2025-05-01 network storage version and the registration; the configuration, the fake's rules and the tests are ours). Not built: a connection's `sharedkey` child (a fixed-name singleton; the key is set through `sharedKey` on the connection), the packet capture, reset and IKE SA actions, and point-to-site (ASO's `VirtualNetworkGateway` holds its client configuration) |
| c8h | ExpressRoute circuits and ports (foundation row 9, first part) | rechecked 2026-11-09, read-only: v2.22.0 still the latest release (an `experimental` pre-release beside it); `main` four documentation commits past the pin (#5826, #5827, #5828, #5769); ASO issue #4371 ("Support Express Route Resource creation Using ASO", `cx-request`) open, no pull request; no open or draft pull request adds an ExpressRoute kind (the open ones are #5829, #5821, #5820, #5814, #5804, #5783, #5780, #5773, #5768, #5693, #5659, #5638, #5104, #5028, #4210); ASO's `network.yaml` exports none | **built**, generated: `network.azure.hfl.dev/v20260301` `ExpressRouteCircuit` (owned by a resource group; a provider circuit by `serviceProviderProperties`, or an ExpressRoute Direct circuit on our `ExpressRoutePort` by reference), `ExpressRouteCircuitsPeering`, `ExpressRouteCircuitsAuthorization`, `ExpressRouteCircuitsPeeringsConnection` (Global Reach, owned by a peering; its own and the peer's peering are references), `ExpressRoutePort` and `ExpressRoutePortsAuthorization`, from `expressRoute.json` of 2026-03-01 (the version of row 8). **Keys**: what the user gives is a Secret reference (`$importSecretMode: required`: a peering's BGP `sharedKey`, a circuit's `authorizationKey` for another subscription's port and its partner `activationKey`, a Global Reach connection's `authorizationKey`); what Azure makes (a circuit's `serviceKey`, a circuit or port authorization's `authorizationKey`) is removed from spec and status (`typeTransformers`) and written to the Secret `operatorSpec.secrets` names by a hand-written exporter (`$azureGeneratedSecrets`, as ASO's API Management `Subscription` keys). **Children**: a circuit's PUT carries its `peerings` and `authorizations`, a peering's its `connections`; Azure's network resources may treat such a list as the whole set, so that a PUT without it deletes the children (ASO's `RouteTable`, `NetworkSecurityGroup` and `VirtualNetwork` subnets do; whether circuits and peerings do could not be checked without Azure, so it is assumed, and copying the lists is harmless if they do not): the lists are `$resourceLifecycleOwnedByParent` (out of the Kubernetes spec, kept in the ARM payload) and hand-written `ARMResourceModifier`s copy them from a GET before the PUT, ASO's own code. A child resource's own `id` (the spec's `SubResource` base) is removed from the child kinds' spec, as ASO's subnets have none, and labelled `$referenceType: simple` where it stays in the parent's ARM payload. Generated share: 21,963 lines of kind code generated (322 of the VPN kinds' shared types moved between files), 471 hand-written in `generator/_extensions/network` (210 of them the child-collection code, 109 of those ASO's helpers copied unchanged; three exporters and a shared key helper): 97.9% generated, tests aside; patch 0003 grows by one type alias. Not built: **cross-connections** (`expressRouteCrossConnections` and their peerings: the connectivity provider's side, created by Azure, with no DELETE; the generator does not take them as resources, "type not seen"); ExpressRoute LAGs (`expressRouteLags`, new in 2026-03-01, not in the queue); the circuit's ARP, route table and stats reads and the port's `generateLoa` (POST actions); gateways and their connections (next, from `virtualWan.json`) |
| c8i | ExpressRoute gateways and their connections (foundation row 9, second part) | rechecked 2026-11-09, read-only: v2.22.0 still the latest release (an `experimental` pre-release beside it); `main` four documentation commits past the pin (#5826, #5827, #5828, #5769); ASO issue #4371 open, no pull request; no ASO issue or pull request mentions an ExpressRoute gateway, a Virtual WAN or a virtual hub; no open or draft pull request adds an ExpressRoute kind (the open ones are #5821, #5820, #5814, #5804, #5783, #5780, #5773, #5768, #5693, #5659, #5638, #5104, #5028, #4210); ASO's `network.yaml` exports none | **built**, generated: `network.azure.hfl.dev/v20260301` `ExpressRouteGateway` (owned by a resource group; its Virtual WAN hub by ARM ID until row 10 builds `VirtualHub`; its PUT keeps its connections, the circuit's `ARMResourceModifier` pattern) and `ExpressRouteGatewaysExpressRouteConnection` (owned by the gateway; a circuit's private peering by object or ARM ID; a cross-subscription authorization key from a Secret; its routing configuration's route tables and route maps by ARM ID), spec `virtualWan.json` 2026-03-01. The connection needed generator patch 0007 (§2, "Child resources built on a `SubResource`"). Not built: the gateway's failover-test and route-information actions (POST, no resource) |
| c8j | Virtual WAN and its hubs (foundation row 10, first part) | rechecked 2026-11-09, read-only: ASO `main` at `6f170b07` (2026-11-09); its `network.yaml` exports no Virtual WAN, hub or VPN gateway kind; no ASO issue or pull request found for "virtual wan" or `VirtualHub` | **built**, generated: `network.azure.hfl.dev/v20260301` `VirtualWan` and `VirtualHub` (owned by a resource group; the hub's WAN by object or ARM ID), and, owned by the hub, `VirtualHubsHubVirtualNetworkConnection` (a virtual network by ARM ID; its routing configuration's route tables by object or ARM ID), `VirtualHubsHubRouteTable` (a route's `nextHop` is a reference, `$referenceType: arm` on a string), `VirtualHubsRouteTable` (the older `routeTables`), `VirtualHubsRoutingIntent` (a policy's `nextHop` a reference), `VirtualHubsBgpConnection` (its hub connection a reference) and `VirtualHubsIpConfiguration` (its subnet and public IP references, reduced by the generator from whole embedded objects), spec `virtualWan.json` 2026-03-01; the hub connection and `routeTables` are resources by patch 0007. **Children**: a hub's PUT carries `virtualHubRouteTableV2s` (its `routeTables`); as for circuits, the list is `$resourceLifecycleOwnedByParent` and a hand-written `ARMResourceModifier` copies it from a GET before the PUT (the circuit's `keepChildCollections`, reused). A child's own `id` is removed from its spec (`typeTransformers`) and labelled `$referenceType: simple` where it stays in the hub's ARM payload; `HubRoute`'s `destinationType` and `nextHopType` are labelled simple (the generator took them for references). `ExpressRouteGateway` can now name our `VirtualHub` by object. Generated share: 22,278 lines of kind code generated, 42 hand-written (the hub's modifier): 99.8%, tests aside. Not built: hub `routeMaps` and `connectionPolicies` (not in the queue's row), the hub's effective-routes and inbound/outbound-routes reads (POST actions), and the VPN half of row 10 (next) |
| c8k | Virtual WAN VPN gateways (foundation row 10, second part) | rechecked 2026-11-09, read-only: ASO `main` at `6f170b07` (the pin plus four documentation commits); its `network.yaml` exports no VPN gateway, VPN site, server configuration or point-to-site kind; no ASO issue or open pull request found for VPN gateways, VPN sites or Virtual WAN | **built**, generated: `network.azure.hfl.dev/v20260301` `VpnGateway` (in a hub by object or ARM ID), `VpnGatewaysVpnConnection` and `VpnGatewaysNatRule` (owned by the gateway; the connection by patch 0007), `VpnSite` (in a WAN), `VpnServerConfiguration`, `P2sVpnGateway` (in a hub, with a server configuration by object or ARM ID), spec `virtualWan.json` 2026-03-01. **Children**: a VPN gateway's PUT carries `connections` and `natRules`; both are `$resourceLifecycleOwnedByParent: VpnGateway`, and a hand-written `ARMResourceModifier` (`vpn_gateway_extensions.go`, `keepChildCollections`) copies them from a GET. **Embedded, not kinds**: a site's links, a connection's link connections and a server configuration's policy groups are `SubResourceModel`s, which the generator takes for resources and reduces to their ID; `$isResource: false` on them (and their `_STATUS`) keeps them whole in the parent's spec, as ASO's CDN and compute configurations do. **Keys**: a link connection's `sharedKey` and the RADIUS secrets are `x-ms-secret` (Secret references without help); a site's `siteKey` is labelled `$importSecretMode: required`. The connection's own `sharedKey` is deprecated in the spec ("no more used") and `$importSecretMode` on it is never consumed (the generator reports it unused under both names it has), so it is removed by `typeTransformers` rather than left a plain string. A server configuration's status lists its point-to-site gateways whole (`p2SVpnGateways`); the generated tests of that embedded copy collide with the gateway kind's (`RunJSONSerializationTestForP2SVpnGateway_STATUS redeclared`, found by `go vet`, not by `go build`), so the list is removed from the status. Generated share: 30,731 lines of kind code generated, 44 hand-written (the gateway's modifier): 99.9%, tests aside. Not built: the link connections' `sharedKeys/default` singleton, server configuration policy groups as their own kind (they stay in the configuration's spec), and the POST actions (reset, packet capture, VPN profiles, connection health) |
| c8l | Virtual Network Manager (foundation row 11, first part) | rechecked 2026-11-10, read-only: ASO `main` at `a538468c` (the pin plus four documentation commits; latest release v2.22.0); its `network.yaml` exports no network manager kind; no ASO issue or pull request found for network managers (searched "network manager", "NetworkManager", "AVNM") | **built**, generated: `network.azure.hfl.dev/v20260301` `NetworkManager` (over subscriptions and management groups by ARM ID, as strings), `NetworkManagerConnection` (at a subscription or a management group: an extension resource with an arbitrary owner by patch 0006; the manager by object or ARM ID), `NetworkManagersScopeConnection` (another tenant's subscription or management group by ARM ID), `NetworkManagersNetworkGroup`, `NetworkManagersNetworkGroupsStaticMember` (a virtual network or subnet by ARM ID), spec `networkManager.json` 2026-03-01. No generator patch: 2026-03-01 declares its parameters per operation (the older versions' path-level parameters are what made ASO drop the PUT body, handoff). Labels: `$referenceType: arm` on `networkManagerId` and both `resourceId`s; nothing else. Generated share: 7,194 lines of kind code generated, none hand-written: 100%, tests aside. Next, in two more pull requests: connectivity, security admin and security user configurations with their rule collections and rules; routing configurations with theirs, and `commits`. Not in this row's plan: IPAM pools and verifier workspaces (other spec files) |
| c8m | Virtual Network Manager configurations (foundation row 11, second part) | rechecked 2026-11-10, read-only: ASO `main` at `a538468c` (unchanged since part 1); no network manager kind, issue or pull request | **built**, generated: `network.azure.hfl.dev/v20260301` `NetworkManagersConnectivityConfiguration` (mesh or hub and spoke; hubs a virtual network or Virtual WAN hub by reference, network groups by reference), `NetworkManagersSecurityAdminConfiguration`, `NetworkManagersSecurityAdminRuleCollection`, `NetworkManagersSecurityAdminRule`, `NetworkManagersSecurityUserConfiguration`, `NetworkManagersSecurityUserRuleCollection`, `NetworkManagersSecurityUserRule`, spec `networkManager.json` 2026-03-01. **Polymorphic admin rule**: its PUT body is `BaseAdminRule` with `kind` `Custom` (`AdminRule`) or `Default` (`DefaultAdminRule`); the generator stopped at stage 41 ("resource spec doesn't have \"Name\" property") until the two leaves and their `_STATUS` were labelled `$resourceEmbeddedInParent: NetworkManagers_SecurityAdminConfigurations_RuleCollections_Rule`, as ASO's `kusto.yaml` does for a database; the spec then has `adminRule` and `defaultAdminRule`, exactly one of which is set (ASO's OneOf). Labels besides: `$referenceType: arm` on the three `networkGroupId`s and a hub's `resourceId`. An address prefix of type `NetworkGroup` stays a string. **Names**: the mechanical export names (`NetworkManagersSecurityAdminConfigurationsRuleCollectionsRule`) make a `listKind` of 65 characters, over Kubernetes' 63 (envtest refuses the CRD: `spec.names.listKind: Invalid value`), so the rule collections and rules are exported as `NetworkManagersSecurityAdminRuleCollection`, `NetworkManagersSecurityAdminRule`, `NetworkManagersSecurityUserRuleCollection` and `NetworkManagersSecurityUserRule` (routing's, in part 3, the same way). A kind name plus `List` must fit in 63 characters. Generated share: 14,144 lines of kind code generated, none hand-written: 100%, tests aside |
| c8n | Virtual Network Manager routing and commits (foundation row 11, third part) | rechecked 2026-11-10, read-only: ASO `main` at `6f170b07` (the pin plus four documentation commits; latest release v2.22.0); its `network.yaml` exports no network manager kind; no issue or pull request | **built**, generated: `network.azure.hfl.dev/v20260301` `NetworkManagersRoutingConfiguration` (route table usage `ManagedOnly` or `UseExisting`), `NetworkManagersRoutingRuleCollection` (for network groups by reference), `NetworkManagersRoutingRule` (a destination, an address prefix or service tag, and a next hop), `NetworkManagersCommit` (a commit of configurations of one type to target regions: `properties.configurationReferences`, the configurations by object or ARM ID; Azure records the active regions in the status), spec `networkManager.json` 2026-03-01. Names: the rule collection and rule are exported shortened, as the security rule kinds are (the pattern was approved, section 8). Labels: `$referenceType: arm` on the routing group item's `networkGroupId`; a commit's `configurationIds` is `format: arm-id` already, so it takes no label (the generator refuses one). No generator patch. Generated share: 6,680 lines of kind code generated, none hand-written: 100%, tests aside. Row 11 is complete but for IPAM pools and verifier workspaces (other spec files, not in this row's plan) |
| c8o | IP groups, route filters, service endpoint policies, DNS SOA record sets and DNS security policies (foundation row 12) | rechecked 2026-11-10, read-only: ASO `main` at the pin plus four documentation commits (#5826, #5827, #5828, #5769; latest release v2.22.0); its `network.yaml` exports none of these (it exports every DNS record type but SOA); no open pull request adds them; issues: #4199 (service endpoint policies as their own kind) closed stale in 2024, #3081 (an IP group bug) closed; none for route filters, SOA records or DNS security policies | **built**, generated: `network.azure.hfl.dev/v20260301` `IpGroup`, `RouteFilter`, `RouteFiltersRouteFilterRule`, `ServiceEndpointPolicy`, `ServiceEndpointPoliciesServiceEndpointPolicyDefinition` (spec 2026-03-01; a filter's `rules` and a policy's `serviceEndpointPolicyDefinitions` are `$resourceLifecycleOwnedByParent` with their `Id` `simple`, and two extensions keep them on the parent's PUT, the circuit's pattern; the rule's `SubResource` base needs patch 0007); `v20180501` `DnsZonesSOARecord` (dns 2018-05-01, the version of ASO's `DnsZone`, its owner, listed in `generator/external-owners.txt`; `Etag` removed as ASO removes it from the other record types; `SubResource.Id` labelled `arm`, a record set's `targetResource`) and `v20240601` `PrivateDnsZonesSOARecord` (privatedns 2024-06-01, owner ASO's `PrivateDnsZone`); `v20250501` `DnsResolverPolicy`, `DnsResolverPoliciesDnsSecurityRule`, `DnsResolverPoliciesVirtualNetworkLink`, `DnsResolverDomainList` (dnsresolver 2025-05-01, the newest stable; ASO's resolver kinds are 2022-07-01, which has no policies). New specs: `specification/dns`, `privatedns`, `dnsresolver`. **Shared version**: dnsresolver's `openapi.json` and the network security perimeter's file are both 2025-05-01, so the generator renames each file's `SubResource` after its file (`NetworkSecurityPerimeterSubResource`, `OpenapiSubResource`), both labelled `arm`; the perimeter kinds' Go type changed name, their CRDs did not. **SOA record sets**: Azure makes one with each zone and deletes it only with the zone, so the two kinds have a hand-written `Deleter` (`generator/_extensions/network/customizations/soa_record_extensions.go`) that leaves the record set in Azure when the object is deleted; the generated spec carries every record type's list (the spec's `RecordSetProperties`), which Azure refuses on a SOA. Generated share: 35,438 lines of kind code generated (the perimeter rename included), 150 hand-written (three extensions): 99.6%, tests aside |
| c9 | ACR tokens, scope maps, credential sets | not at the pin, no pull request (rechecked 2026-11-09: v2.22.0 still the latest release, `main` three documentation commits past the pin, nothing open on registries; ASO exports `Registry`, `RegistryReplication` and `RegistryCacheRule`, whose `credentialSetResourceId` had no kind to point at) | **built**, generated (`containerregistry.azure.hfl.dev`, API 2025-11-01, ASO's own `Registry` version): `RegistriesToken` (bound to a scope map, enabled or disabled; certificates in the spec), `RegistriesScopeMap`, `RegistriesCredentialSet` (the Key Vault secret URIs of an upstream registry, for a cache rule; identity as ARM references) and, beyond the ranking, `RegistriesWebhook` (service URI and custom headers are `SecretReference`s, so they come from Kubernetes Secrets). Not built: a token's **passwords**: they are generated by the `generateCredentials` action, returned once, and are not part of the token's PUT, so no CRD field could hold them (a password would be a Key Vault secret an operator writes after a POST; that is a hand-written kind, not queued); connected registries (IoT Edge), private endpoint connections |
| f17 | Container registry tasks, connected registries, private endpoint connections (foundation row 17) | rechecked 2026-11-10, read-only: v2.22.0 still the latest release; `main` four documentation commits past the pin; no open pull request or issue adds a registry task or a connected registry; ASO's `containerregistry` group exports `Registry`, replications and cache rules | **built**, generated in `containerregistry.azure.hfl.dev`, owned by ASO's `Registry`: `v20190401` `RegistriesTask` (Registry Tasks has its own spec, `RegistryTasks/stable/2019-04-01`, whose only newer version is a preview: platform, agent, timeout, a Docker, encoded or file step as a one-of whose `type` must be given, source, timer and base image triggers, identity, custom registry credentials; the step's `contextAccessToken`, a source trigger's `token` and `refreshToken`, and a custom registry's user name and password are Secret references, removed from the status by `typeTransformers`); `v20251101` `RegistriesConnectedRegistry` (mode, parent and its sync token, our `RegistriesToken` by object or an ARM ID, client tokens, logging, garbage collection, notifications) and `RegistriesPrivateEndpointConnection`. Hand-written code: none (the kind code is 100% generated). Not built: task runs and agent pools (no platform need; runs are actions, not desired state); an encoded or file step's `values` with `isSecret` stay plain strings in the spec (the spec does not say which are secret); the registry's Premium SKU is not checked |
| c10 | VM extras: run command, auto-shutdown schedule, maintenance assignment, Recovery Services backup | not at the pin, no pull request (rechecked 2026-11-09: v2.22.0 still the latest release, `main` three documentation commits past the pin; open issues #1187 asks for database backup only; ASO has no `devtestlab`, `maintenance` or `recoveryservices` group) | **built in two parts**. Part one, generated: `compute.azure.hfl.dev/v20220301` `VirtualMachinesRunCommand` (script or script URI, parameters, the run-as password from a Secret, owned by ASO's `VirtualMachine`); `devtestlab.azure.hfl.dev/v20180915` `Schedule` (a VM's auto-shutdown: task type `ComputeVmShutdownTask`, daily time, time zone, notification; the VM an ARM reference; owned by the resource group); `maintenance.azure.hfl.dev/v20230401` `MaintenanceConfiguration` (owned by the resource group). **`ConfigurationAssignment` built later** (2026-11-09, after generator patch 0006): the spec has the same resource at four scopes (subscription, resource group, resource, nested resource), and ASO's generator picked one of the four paths by map order, so each run differed; with patch 0006 it is an extension resource, `maintenance.azure.hfl.dev/v20230401` `ConfigurationAssignment`, owned by any resource (ASO's `VirtualMachine` by group and kind), a resource group or a subscription (an ARM ID), naming our `MaintenanceConfiguration` by object or an ARM ID, with the dynamic-scope `filter` (resource types, OS types, locations, resource groups, tags). Rechecked before building, read-only: v2.22.0 still the latest release, `main` four documentation commits past the pin, no ASO issue or pull request on maintenance assignments, ASO still has no `maintenance` group. Part two, Recovery Services, is generated too: `recoveryservices.azure.hfl.dev/v20260701` `Vault` (owned by the resource group: SKU, identity, redundancy, encryption, monitoring, public network access), `VaultsBackupPolicy` (the union of IaaS VM, SQL, file share, workload and other policies, with schedule and retention; owned by our `Vault`), `VaultsBackupResourceGuardProxy` and `VaultsPrivateEndpointConnection`. **Not built, and why it matters**: the protection container, the protected item (what turns backup on for a VM) and the protection intent. Their paths hold a fixed fabric segment (`backupFabrics/Azure`) that is no resource, so the generator makes the resource group the owner and the Kubernetes object cannot name the ARM ID ("had 1 azureNames and 3 resourceTypes", found by the test); a protected item is also owned by its container, which a VM never needs created. They, the singleton settings (`backupstorageconfig`, `backupconfig`, encryption config) and Site Recovery are hand-written candidates (see docs/handoff.md). Redundancy is set on the vault itself (`redundancySettings`), so the storage-config singleton is not needed to choose geo or local. **c18 (stale ARM versions)**: ASO's `VirtualMachine` is at 2022-03-01 while the Compute spec has 2026-04-01, so the run command is built at 2022-03-01, ASO's version, so owner and child share a vintage; it moves when ASO's `VirtualMachine` does. **Generator change**: patch 0004 (UPSTREAM.md) makes a property flattened out of a flattened property stay an object, which the Maintenance window (`properties.maintenanceWindow.*`) and the Log Analytics data export needed; it changes nothing for kinds that generated before |
| c11 | Storage local users (SFTP) | not at the pin, no pull request | queued |
| f16 | Source control configurations (legacy Flux v1 GitOps) and Kubernetes configuration private link scopes (foundation row 16) | rechecked 2026-11-10, read-only: v2.22.0 still the latest release; `main` four documentation commits past the pin; no open pull request or issue adds either; ASO's `kubernetesconfiguration` group exports `Extension` and `FluxConfiguration` only | **built**, generated in `kubernetesconfiguration.azure.hfl.dev`: `v20230501` `SourceControlConfiguration` (an extension resource with an arbitrary owner, a managed or Arc-enabled cluster, as ASO's `FluxConfiguration`: repository, operator type, scope, namespace, instance name and parameters, Helm operator, known hosts; `configurationProtectedSettings` a Secret map reference, `$importSecretMode: required` as ASO's `ExtensionProperties`, so it reaches neither spec nor status; `systemData` removed from the spec, as ASO does for `FluxConfiguration`) and `v20241101preview` (no stable API exists) `PrivateLinkScope` (owned by a resource group; the Arc cluster an ARM reference) and `PrivateLinkScopesPrivateEndpointConnection` (owned by our scope). A connection's spec keeps `provisioningState`: it comes from common-types v3's `PrivateEndpointConnectionProperties`, which other groups share and which is removable only there; Azure ignores it on a PUT. Hand-written code: none (the kind code is 100% generated). Not built: nothing else in the row |
| c12 | Cognitive Services RAI policies | not at the pin, no pull request | queued |
| f15 | AKS managed namespaces, fleet managed namespaces, private endpoint connections, node pool snapshots (foundation row 15; c14's managed namespaces) | rechecked 2026-11-10, read-only: v2.22.0 still the latest release; `main` four documentation commits past the pin; no open pull request or issue adds a managed namespace, a cluster's private endpoint connection or a snapshot; ASO's `containerservice` group exports `ManagedCluster` (newest 2026-05-01), agent pools, maintenance configurations, trusted access role bindings, identity bindings and `Fleet` (2025-03-01) with members, update runs, strategies and auto-upgrade profiles | **built**, generated in `containerservice.azure.hfl.dev`: `v20260501` (ASO's `ManagedCluster` storage version, so owner and child share a vintage) `ManagedClustersManagedNamespace` (labels, annotations, default resource quota and network policy, adoption and delete policies; owned by ASO's `ManagedCluster`), `ManagedClustersPrivateEndpointConnection` (approving or rejecting a connection; owned by ASO's `ManagedCluster`) and `Snapshot` (a node pool snapshot in a resource group, of an agent pool by ARM reference, which the spec already marks as one); `v20260601` `FleetsManagedNamespace` (owned by ASO's `Fleet`; 2026-06-01 is the first stable fleet API with managed namespaces, ASO's `Fleet` is at 2025-03-01 which has none). ASO's `ManagedCluster` and `Fleet` are new external owners (`generator/external-owners.txt`, `controllers.OwnerTypes()`, their test CRDs in `config/test/aso-crds`). The group carries ASO's `$payloadType: explicitCollections`. Hand-written code: none (the kind code is 100% generated). Not built: the managed cluster snapshot (`managedclustersnapshots`, preview only), and the cluster attributes ASO's `ManagedCluster` lacks against 2026-08-01 (API-version lag, upstream's) |
| c13 | Cosmos DB Gremlin, SQL dedicated gateway | not at the pin, no pull request | queued |
| f18 | Activity log profiles and workbook templates (foundation row 18; the rest of the row, newer versions of ASO's alert and data collection kinds, is upstream's) | rechecked 2026-11-10, read-only: v2.22.0 still the latest release; `main` four documentation commits past the pin; no open pull request or issue adds a log profile or a workbook template; ASO's `insights` group has neither | **built**, generated in `insights.azure.hfl.dev`: `v20160301` `LogProfile` (the only stable version: categories, locations, a storage account and an Event Hubs authorization rule as ARM references, retention), owned by the subscription (an ARM ID) by **generator patch 0009** (a resource directly under a subscription is an extension resource; without it the generator gave it Location scope, which it cannot model, and its PUT went to a resource group named after it); `v20201120` `WorkbookTemplate` (galleries, localized galleries, priority, author, and the template as free-form JSON), owned by a resource group. Hand-written code: none (the kind code is 100% generated). Not built: the alert, scheduled query and data collection endpoint attributes ASO's kinds lack against newer spec versions (upstream's, a `$supportedFrom` there); log profiles are retired by Azure in favour of diagnostic settings, which ASO has, and are built for subscriptions that still use them |
| c14 | AKS managed namespaces | not at the pin, no pull request | queued |
| f14b | Operations Management solutions (foundation row 14, second part; c5 had left them out) | rechecked 2026-11-10, read-only: v2.22.0 still the latest release; `main` four documentation commits past the pin (#5826, #5827, #5828, #5769); no open pull request or issue adds an Operations Management solution, and ASO has no `operationsmanagement` group | **built**, generated: `operationsmanagement.azure.hfl.dev/v20151101preview` `Solution` (the only API version, a 2015 preview), owned by a resource group: the plan (name, publisher, product, promotion code), the workspace it is enabled on (ASO's `Workspace` by group and kind, or an ARM ID) and its contained and referenced resources (ARM references). **c5's reason was wrong**: the generator handles a schema without `type: object` (`getSubSchemaType` treats any schema with properties as an object); what emptied `SolutionProperties` is its `x-ms-azure-resource: true`, which made the embedded resource remover take it for a resource embedded in the solution and keep only its (missing) `Id`. `$resourceEmbeddedInParent: Solution` on `SolutionProperties` and `SolutionProperties_STATUS` (as ASO's `kusto.yaml` labels a database's) keeps it whole: no generator patch. Hand-written code: none (the kind code is 100% generated). Not built: management associations and management configurations (the same spec; their properties are emptied the same way, and they are no platform need) |
| c15 | Microsoft Entra directory roles (`DirectoryRoleAssignment`) | rechecked 2026-11-10, read-only: ASO's `entra.azure.com` group at the pin has `Application`, `SecurityGroup` and `ServicePrincipal`, no directory role; `main` (`6f170b07`, four documentation commits past the pin) the same; open pull request **#5821** ("chore: Extract base reconciler for Entra resources", head `6c93419b`) factors a base out of ASO's Entra reconcilers and adds no kind; no issue or pull request for directory roles | **built**, hand-written (Microsoft Graph, not ARM: nothing to generate): `entra.azure.hfl.dev/v1` `DirectoryRoleAssignment`, Graph's `unifiedRoleAssignment` (`POST /roleManagement/directory/roleAssignments`) with Graph's property names (`principalId`, `roleDefinitionId`, `directoryScopeId`, `appScopeId`, `condition`) and ASO's Entra conventions: `operatorSpec.creationMode` (`AdoptOrCreate` by default: an assignment of the same role to the same principal at the same scope is adopted, found by `$filter`; `AlwaysCreate`; `AdoptOnly`, which leaves the assignment in Graph on delete), the principal by object ID or `principalIdFromConfig` (a ConfigMap, as an ASO `SecurityGroup` or `ServicePrincipal` exports its ID), the Graph ID in the `serviceoperator.azure.com/resource-id` annotation. The reconciler is built on **#5821's base reconciler** (copied, section `UPSTREAM.md`) and shaped as its `SecurityGroupReconciler`; the Graph client is ASO's Entra client cache with an endpoint (`MICROSOFT_GRAPH_ENDPOINT`, default `https://graph.microsoft.com`; a national cloud or the fake sets its own); the beta Graph SDK, ASO's own dependency at the pin. Graph has no update for an assignment, so the CRD refuses changes (CEL), and exactly one principal source and one scope (CEL). Generated share: 423 lines generated (deepcopy and the CRD, by controller-gen) against 831 written (the type, the reconciler, its base and the client; 347 of them copied from ASO and #5821 and adapted): 34% generated; the fake (`pkg/fakecloud/graph`, `cmd/fakegraph`) and tests aside. The builder names the kind on the decisions page; `DirectoryRoleAssignment` until they say otherwise |
| f19 | Defender for Cloud (foundation rows 19, 20 and 23 but security connectors) | rechecked 2026-11-11, read-only: v2.22.0 the latest release (an "Experimental" pre-release since); `main` eight commits past the pin, none adding a kind; open #5783 adds `security.azure.com/v20250601` `DefenderForStorageSetting` (copied); no other pull request or issue adds a Defender for Cloud kind | **built**, generated, `security.azure.hfl.dev`, twelve kinds over eight API versions in one group file (`generator/azure-arm/security.yaml`), each resource at its newest version: `Pricing`, `DefenderForStorageSetting`, `SecurityStandard`, `StandardAssignment`, `CustomRecommendation`, `SecurityContact`, `Automation`, `ServerVulnerabilityAssessmentsSetting`, `Setting`, `AdvancedThreatProtectionSetting`, `AutoProvisioningSetting`, `WorkspaceSetting`. Generator patch 0010: `/{resourceId}/` and `/{scopeId}/` paths and paths directly under a subscription are extension resources (ASO called them Tenant or Location scope and built no correct ARM ID), and the prune check before a one-of type is made (#5783's change; a pruned version's `AlertSimulatorRequestProperties` stopped the run). `Setting` and `ServerVulnerabilityAssessmentsSetting` are polymorphic (`$resourceEmbeddedInParent` on the leaves); a setting's enum-valued name is a string (`DataExportSettings_Name`, the import cycle of `operationalinsights.yaml`). Seven reference labels written by `hack/autolabel` (marked for review: `AutomationScope.ScopePath` is `simple`, a scope; the rest name ARM resources). Proven: the conformance test (create, Ready, the fake's type and ID, change, delete) for all twelve; no fake rules (the fake keeps them as sent). Not built: security connectors and their DevOps children, JIT network access policies (a fixed `locations/{ascLocation}` segment), governance rules, alert suppression rules, assessments |
| f22 | Microsoft Sentinel (foundation row 22) | rechecked 2026-11-11, read-only: no ASO release, `main` commit or open pull request adds a `securityinsights` kind | **built**, generated: `securityinsights.azure.hfl.dev/v20250901`, eleven kinds (onboarding state, alert rules and their actions, automation rules, data connectors and definitions, content packages and templates, ML analytics settings, watchlists and items). Polymorphic resources by `$resourceEmbeddedInParent` on their leaves. Removed, with the reason in `generator/azure-arm/securityinsights.yaml`: four recursive properties the generator cannot unroll or overflows on (a template's dependant templates, a dependency's criteria, a UI step's inner steps, an automation rule's nested and per-item conditions) and a modify-properties action's incident `Owner` (named `Owner`, it breaks the storage version; `$renameTo` broke the conversions). Generator patch 0012: a child of an extension resource (watchlist items, alert rule actions) is a child resource, and the resolver builds its ARM ID from its parent's. Proven: conformance for all eleven; `TestSentinelWatchlistItem` walks the Kubernetes owner chain through an extension resource. Not built: incidents, bookmarks, threat intelligence indicators, source controls |
| f2b | Governance leftovers (rows 2, 3 and 6's "not built", and `Microsoft.Resources`) | rechecked 2026-11-11, read-only: none of them in an ASO release, `main` or open pull request | **built**, generated, eleven kinds: template specs and versions, deployment scripts (polymorphic), deployment stacks (several scopes, an extension resource by patch 0006), policy definition and set definition versions (children of an extension resource, patch 0012), policy variables, Resource Manager private links and associations, cost scheduled actions, service groups. One generator run with autolabel (four labels, reviewed: an association's `privateLink` is an ARM reference, written by hand; a stack's `deploymentScope` is a scope, `simple`). Proven: conformance. Not built: policy variable values (the spec types a scalar column value as an object; the generated kind sent an object and dropped an empty one, which the conformance test caught), `Microsoft.Subscription/policies` (a tenant singleton), cost views (their filter type is recursive and pruned), deployments themselves (ASO prunes the type: defined differently by different specs) |
| f24 | Cloud HSM, Dedicated HSM, Purview (foundation row 24, part) | rechecked 2026-11-11, read-only: none in an ASO release, `main` or open pull request | **built**, generated, six kinds in a resource group (`hardwaresecuritymodules` 2025-03-31, `purview` 2021-12-01); one generator run, one autolabel label (a private endpoint's ID). Proven: conformance. Not built: DDoS custom policies and security partner providers (`network.yaml`, after #50), the CDN WAF policy |
| f27 | Firewall policy drafts and IDPS signature overrides (the foundation inventory's `no kind` rows `firewallPolicies/firewallPolicyDrafts` and `firewallPolicies/signatureOverrides`) | rechecked 2026-11-20, read-only: v2.22.0 still the latest release; ASO `main`'s `network.yaml` exports `FirewallPolicy` and its rule collection groups, neither child here; none of the 20 open pull requests adds one | **built**, generated: `network.azure.hfl.dev/v20260301` `FirewallPoliciesFirewallPolicyDraft` (`firewallPolicyDrafts/default`: the policy's settings staged before they are deployed; base policy and Log Analytics workspaces by reference) and `FirewallPoliciesSignatureOverride` (`signatureOverrides/default`: IDPS signature ID to mode; no DELETE in the spec, so detach-on-delete), both singletons named `default`, owned by ASO's `FirewallPolicy` (already an external owner), spec `firewallPolicy.json` 2026-03-01. Labels: `$referenceType: simple` on an IDPS signature specification's `Id` (a signature number, as ASO's own `network.yaml` labels it). No generator patch, no extension. Proven by the conformance test against the fake. Not built from the same file: `ruleCollectionGroups/{g}/ruleCollectionGroupDrafts/default` (it would need ASO's `FirewallPoliciesRuleCollectionGroup` as a new external owner) and the POST that deploys a draft (an action, not a resource) |
| f25 | Network virtual appliances (the foundation inventory's `no kind` network rows 64, 66, 68 and 72) | rechecked 2026-11-20, read-only: v2.22.0 still the latest release; ASO `main`'s `network.yaml` exports no network virtual appliance or child; none of the 20 open pull requests adds one; no issue asks for one (searched "virtual appliance") | **built**, generated: `network.azure.hfl.dev/v20260301` `NetworkVirtualAppliance` (in a resource group; its hub, subnets and ingress public IPs by reference), `NetworkVirtualAppliancesVirtualApplianceSite`, `NetworkVirtualAppliancesConnection` (the generator's `NetworkVirtualAppliances_NetworkVirtualApplianceConnection`, shortened; route tables and route maps by reference), `NetworkVirtualAppliancesInboundSecurityRule` (no DELETE in the spec: detach-on-delete, as `ManagedHSMsKey`), spec `virtualNetwork.json` and `virtualWan.json` 2026-03-01. The appliance's PUT lists its sites, connections and rules read-only, so no extension keeps them. Labels: `$importSecretMode: required` on `cloudInitConfiguration` (vendor cloud-init carries admin passwords and licence or registration tokens: a Secret reference, removed from the status; review of PR #104); the bootstrap and cloud-init blob URL lists stay plain strings (point them at blobs the appliance's identity reads, not SAS URLs); the three children's own `Id` (the spec's `SubResource`/`SubResourceModel`) removed by `typeTransformers`, as the other network children's. No generator patch. Proven by the conformance test (each kind Ready, PUT again on a tag change, deleted or detached). Not built from the same inventory rows: `virtualNetworkGateways/natRules` (ASO embeds them in its `VirtualNetworkGateway`, `$resourceEmbeddedInParent`: a kind of its own would fight the gateway's PUT), `vpnServerConfigurations/configurationPolicyGroups` (kept in our server configuration's spec), `applicationSecurityGroups/addressPrefixSets` (needs ASO's `ApplicationSecurityGroup`, hub v1api20240101, as a new external owner) |
| f26 | ExpressRoute Direct link aggregation groups (the foundation inventory's `no kind` rows `expressRouteLags` and `expressRouteLags/authorizations`) | rechecked 2026-11-20, read-only: v2.22.0 still the latest release; ASO `main`'s `network.yaml` exports no ExpressRoute kind; none of the 20 open pull requests adds one; issue #4371 (ExpressRoute, `cx-request`) open, no pull request | **built**, generated: `network.azure.hfl.dev/v20260301` `ExpressRouteLag` (in a resource group; its links and their members embedded, each link's MACsec Key Vault secret identifiers as strings, as on our `ExpressRoutePort`), `ExpressRouteLagsAuthorization`, spec `expressRoute.json` 2026-03-01 (TypeSpec: `properties` is not flattened, as for the IPAM kinds). Labels: `$referenceType: simple` on a link's and a member's own `Id`. No generator patch, no extension: a LAG authorization has no field a client can write (even its `name` is read-only in the LAG's list), so the generator drops `authorizations` from the LAG's spec and a LAG PUT never carries the list; a circuit's would need `$resourceLifecycleOwnedByParent` and an extension, a LAG's cannot have one. Proven by the conformance test against the fake, which now asks a LAG for `peeringLocation` and `bandwidthInGbps` (`pkg/fakecloud/arm/expressroute_lag_test.go`). Not proven: whether Azure deletes a LAG's authorizations on a PUT that omits the list (a circuit's are assumed to be; the fake leaves a LAG's alone), and what redeems a LAG authorization (no circuit field names a LAG at 2026-03-01); a live run needs ExpressRoute Direct ports, which cost money and take a provider, so none is planned |
| f28 | Governance leftovers, second part (the foundation inventory's `no kind` rows `Microsoft.Subscription/policies`, `Microsoft.Authorization/accessReviewScheduleDefinitions`, `accessReviewHistoryDefinitions`, `accessReviewScheduleSettings`, `Microsoft.CostManagement/settings`) | rechecked 2026-11-23, read-only (git, the GitHub API being closed to this session): the latest release is still v2.22.0; ASO `main` (`f451aaa`, 2026-11-22) has merged #5783 (Defender for Storage settings, which row 19 copied: ours stays until a release carries it, then is deprecated) and no kind of these; ASO's `subscription` group exports `Alias` only, its `authorization` group role assignments and definitions; no pull request head from #5780 on adds any of these | **built**, generated: `subscription.azure.hfl.dev/v20211001` `SubscriptionTenantPolicy` (`/providers/Microsoft.Subscription/policies/default`, the tenant's policy on subscriptions moving into or out of it, with exempted principals: a tenant singleton with no owner, as ASO's `Alias`, and no DELETE: detach-on-delete; new spec `specification/subscription`); `authorization.azure.hfl.dev/v20211201preview` `AccessReviewScheduleDefinition`, `AccessReviewHistoryDefinition` and `AccessReviewScheduleSetting` (access reviews of Azure role assignments, the newest version with them, preview only; extension resources at a subscription or any scope under it, patch 0006; the default settings are a singleton named `default`, no DELETE); `costmanagement.azure.hfl.dev/v20260601` `Setting` (tag inheritance, polymorphic by `kind` with its one leaf embedded, as security's `Setting`; named `taginheritance`). The specs make each PUT body the properties themselves (no `properties` wrapper; the GET wraps them), which the generator follows. A definition's name must be a GUID: two `Claimer`s in `generator/_extensions/authorization/customizations/access_review_extensions.go`, copied from #5638's, default it as the PIM requests' do ("Names that must be GUIDs"). A schedule definition's settings are the type of the default settings' body, which ASO's embedded-resource remover emptied: `$resourceEmbeddedInParent` keeps them. Labels: an access review scope's `excludeResourceId` is `simple` (a filter, possibly several IDs). Proven: conformance; `TestAccessReviewsAndSubscriptionPolicy` (`test/e2e`: the stable GUID name recorded and kept across a change, a random one, the flattened settings and recurrence sent nested as the spec has them, the singletons detached and left in Azure); the fake's rules (`pkg/fakecloud/arm/accessreview.go`, `cost.go`: flat bodies kept as properties, GUID names, undeletable singletons, a tenant-only policy, a tag inheritance setting by name and kind). The coverage dashboard counts most of a schedule definition's attributes as missing: it does not follow the generator's flattening of `settings` and `recurrence` (the wire types carry them). Not proven: whether Azure refuses a non-GUID definition name (assumed from the examples and the portal), the spec's `readOnly` review `scope` and `reviewersType` (so a review's role and principal filter cannot be set; the review covers its owner scope), live runs (pending: a subscription-scope role the live identity lacks). Not built: access review instances and decisions (operational), `markupRules` and `costAllocationRules` (under a billing account or profile, a parent no kind here can own), cost views (recursive filter). Also not built (recorded 2026-11-23), the preview-only rows of the foundation inventory: `Microsoft.Authorization/privilegedResourceFavorites` (2026-06-01-preview, its only version: a favorite "for the calling user" in the PIM portal's list, a person's bookmark, not a resource a platform declares), `Microsoft.Authorization/roleAssignmentApprovals/{a}/stages/{s}` (2021-01-01-preview, its only version: a PUT "records a decision" on a pending PIM approval, an approver's act on a request that already exists, operational; there is no PUT of the approval itself), and `Microsoft.Subscription/subscriptionDefinitions` (2017-11-01-preview, its only version: the old way to create a subscription, superseded by `Microsoft.Subscription/aliases`, which ASO's `subscription` `Alias` covers) |
| f29 | Network leftovers with an ASO owner (the foundation inventory's `no kind` rows `networkWatchers/connectionMonitors`, `applicationSecurityGroups/addressPrefixSets`, `firewallPolicies/ruleCollectionGroups/ruleCollectionGroupDrafts`; f25 and f27 left the last two for want of the owner) | rechecked 2026-11-23, read-only (git): v2.22.0 still the latest release; ASO `main` (`f451aaa`) exports `NetworkWatcher` (and its flow logs), `ApplicationSecurityGroup` and `FirewallPoliciesRuleCollectionGroup`, none of these children; no pull request head from #5780 on adds one | **built**, generated: `network.azure.hfl.dev/v20260301` `NetworkWatchersConnectionMonitor` (endpoints, test configurations, test groups and outputs; an endpoint's resource and an output's workspace by reference), `ApplicationSecurityGroupsAddressPrefixSet` (CIDRs under an application security group), `FirewallPoliciesRuleCollectionGroupDraft` (`ruleCollectionGroupDrafts/default`, a rule collection group's staged rules; the generator's `FirewallPoliciesRuleCollectionGroupsRuleCollectionGroupDraft` is shortened, its list kind passing the 63 characters a CRD allows; its own `Id`, the spec's `SubResourceModel`, removed as the other network children's). Three new external owners, each in the five places (`generator/external-owners.txt`, the scheme and `OwnerTypes()`, `hack/generate.sh`'s test CRDs, `config/rbac/role.yaml`): ASO's `NetworkWatcher` (hub v1api20241001), `ApplicationSecurityGroup` (v1api20240101) and `FirewallPoliciesRuleCollectionGroup` (`group/ARMName=Kind`, patch 0013). Labels, reviewed: a monitor endpoint's and a v1 source's and destination's `resourceId`, and the workspace's, `arm`. No generator patch, no extension. Proven: conformance; `TestNetworkWatcherAndAddressPrefixSets` (`test/e2e`: owners by name through the cache, a test group naming a missing endpoint refused on the Ready condition and then fixed, references sent as ARM IDs, deletes); the fake's rules (`pkg/fakecloud/arm/networkwatcher.go`). Not proven: monitoring itself (the fake runs no test), live runs (pending). Not built: `networkWatchers/packetCaptures` and `connectionAnalyzers` (operations rather than state), `vpnLinkConnections/sharedKeys/default` (its parent, a link connection, is embedded in our `VpnGatewaysVpnConnection`, not a kind), `virtualNetworkGateways/natRules` (embedded in ASO's gateway, f25) |
| f30 | Log Analytics summary rules (the foundation inventory's `no kind` row `workspaces/summaryLogs`) | rechecked 2026-11-23, read-only (git): v2.22.0 still the latest release; ASO `main`'s `operationalinsights` group exports `Workspace` only; no pull request head from #5780 on adds a summary rule | **built**, generated: `operationalinsights.azure.hfl.dev/v20250701` `WorkspacesSummaryLog` (a KQL query run on a schedule over a workspace, its results to a destination table), owned by ASO's `Workspace`, in the version our other workspace children use. No label, patch or extension. Proven: conformance and the fake's rule (`loganalytics.go`: a query and a bin size Azure allows). Not built: `workspaces/features/machineGroups` (Service Map, retired by Azure) |
| f31 | Defender for Cloud alerts suppression rules and governance rules (the security-adjacent inventory's `no kind` rows `alertsSuppressionRules`, `governanceRules`) | rechecked 2026-11-23, read-only (git): v2.22.0 still the latest release; ASO `main` has merged #5783 (Defender for Storage settings, row 19) and adds no other `security` kind; no pull request head from #5780 on adds either | **built**, generated: `security.azure.hfl.dev/v20190101preview` `AlertsSuppressionRule` (dismiss alerts of a type matching a scope; kept directly under a subscription, an extension resource by patch 0009) and `v20220101preview` `GovernanceRule` (owners and due dates for recommendations, at a subscription, management group or security connector, `/{scope}/`; condition sets free-form JSON), both preview only. A governance rule's key must be a GUID: a `Claimer` in `generator/_extensions/security/customizations/governance_rule_extensions.go`, copied from #5638's as the access reviews' (f28). Proven: conformance and the fake's rules (`governance.go`: GUID keys, priority 0 to 1000 and unique per scope, a condition set). Not proven: suppression taking effect, live runs (pending: a subscription-scope role). Not built from the same rows: governance assignments (`assessments/{a}/governanceAssignments`, per recommendation: operational), JIT network access policies (under `locations/{ascLocation}`, a segment that is no resource: hand-written, as h1's protection containers would be), custom assessments and their metadata, IoT security solutions and device security groups |
| f32 | Microsoft Sentinel, second part (the security-adjacent inventory's `no kind` rows `metadata`, `sourcecontrols`, `settings`, `workspaceManagerConfigurations`, `workspaceManagerGroups`, `workspaceManagerMembers`, `workspaceManagerAssignments`) | rechecked 2026-11-23, read-only (git): no ASO release, `main` commit (`f451aaa`) or pull request head from #5780 on adds a `securityinsights` kind | **built**, generated: `securityinsights.azure.hfl.dev/v20250901` `ContentMetadata` (the spec's `metadata`, which the generator singularizes to `Metadatum`: a content item's source, author, support and dependencies; `parentId` is a reference, our `AlertRule` and the like by object or an ARM ID) and `SourceControl` (a GitHub or Azure DevOps repository content is deployed from; its OAuth code, token and client secret are Secret references, x-ms-secret in the spec; no DELETE: detach-on-delete); `v20251001preview` `Setting` (polymorphic, `Anomalies`, `EntityAnalytics`, `EyesOn`, `Ueba`, each leaf embedded) and the workspace manager's `WorkspaceManagerConfiguration`, `WorkspaceManagerGroup`, `WorkspaceManagerMember` (its workspace ASO's `Workspace` by object or an ARM ID) and `WorkspaceManagerAssignment` (content items by reference), preview only. The six recursion transformers of f22 now apply to both versions. Labels, reviewed: `parentId` `arm` (autolabel wrote `simple`), an assignment item's `resourceId` and a member's workspace `arm`. The conformance fixtures are now looked up by `group/Kind` before `Kind` (three groups have a `Setting`). Proven: conformance and the fake's rules (`sentinel.go`: a setting named by its kind; members, groups and assignments naming what exists). Not proven: content being published to members, a repository connection's OAuth flow (Azure exchanges the code with GitHub), live runs (pending). Not built: incidents and their comments, relations and tasks, bookmarks, hunts, cases, threat intelligence indicators, file imports, entity queries (operational data or preview-only investigation features) |
| f33 | Defender for Cloud leftovers (the security-adjacent inventory's `no kind` rows `assessmentMetadata`, `assessments`, `privateLinks` and their endpoint connections, `sqlVulnerabilityAssessments` and their baseline rules) | rechecked 2026-11-23, read-only (git): no ASO release, `main` commit (`f451aaa`) or pull request head from #5780 on adds any of them | **built**, generated: `security.azure.hfl.dev/v20250504` `AssessmentMetadata` (a custom recommendation's definition, directly under a subscription, patch 0009; generated as `AssessmentMetadatum`; its key a GUID, defaulted by a `Claimer` copied from #5638's, `generator/_extensions/security/customizations/assessment_metadata_extensions.go`) and `Assessment` (a resource's result against it, `/{resourceId}/`; named after the metadata's key), `v20260101` `PrivateLink`, `PrivateLinksPrivateEndpointConnection`, `v20260401preview` `SqlVulnerabilityAssessment` (express configuration on a SQL server or database, the fixed name `default`) and `SqlVulnerabilityAssessmentsBaselineRule` (its child, patch 0012). Labels, reviewed: an attack path's, node's and edge's `id` `simple` (identifiers, not ARM IDs; autolabel wrote `arm`), an on-premises machine's `workspaceId` `arm` (autolabel wrote `simple`). Proven: conformance and the fake's rules (`assessments.go`: GUID metadata keys, an assessment named after metadata that exists in its subscription, a status code). Not built, with reasons: JIT network access policies (under `locations/{ascLocation}`, which no generated kind can own; a hand-written kind on ASO's generic reconciler, as `VaultsProtectedItem`, would be several hundred lines, not small); IoT security solutions and device security groups (their only stable version, 2019-08-01, defines the solution in two files and the generator refuses to load it); legacy or superseded APIs (`standards` and `assignments` 2021-08-01-preview, superseded by the built `SecurityStandard`/`StandardAssignment`; `informationProtectionPolicies`, no DELETE; `serverVulnerabilityAssessments`, the retired Qualys integration; `applications` 2022-07-01-preview, never stable); operations or preview-only new services (`governanceAssignments`, `batchPricings` with no DELETE, `pricings/securityOperators`, `serviceEntitlements`, `dataScanners`, `sensitivitySettings` and `apiCollections`, the last left for the API Management owner it needs) |
| f34 | Cosmos DB role-based access for the Cassandra, Gremlin, MongoDB (Entra) and Table APIs (the application inventory's `no kind` rows `databaseAccounts/{cassandra,gremlin,mongoMI,table}Role{Definitions,Assignments}`; application tier 2, no AVM needs any application row, so by size) | rechecked 2026-11-23, read-only (git): ASO's `documentdb` group exports `SqlRoleAssignment` (and our `DatabaseAccountsSqlRoleDefinition` fills the SQL definitions), no other API's; no release, `main` commit (`f451aaa`) or pull request head from #5780 on adds one | **built**, generated: `documentdb.azure.hfl.dev/v20260315` (the newest stable; 2024-08-15, our other Cosmos kinds' version, has none) `DatabaseAccounts{Cassandra,Gremlin,MongoMI,Table}RoleDefinition` and `...RoleAssignment`, eight kinds on ASO's `DatabaseAccount`. Names are GUIDs: Claimers in `generator/_extensions/documentdb/customizations/role_based_access_extensions.go`, copied from ASO's own `SqlRoleAssignment` defaulting webhook at the pin (`api/documentdb/v1api20240815/webhook/sql_role_assignment_defaults.go`), run at the claim as the PIM ones are. A principal ID may come from a ConfigMap, as ASO's `SqlRoleAssignment` takes it; `roleDefinitionId` stays a string, as ASO's. Labels, reviewed: a definition's own `id` and a permission's `id` `simple` (autolabel wrote `arm`). Proven: conformance (a non-GUID name is refused by the fake, so it shows the Claimers) and the fake's rules (`cosmosrbac.go`). Not proven: data-plane enforcement, built-in role definitions, live runs (pending). Not built from Cosmos: `chaosFaults`, `copyJobs`, `dataTransferJobs` (operations), `notebookWorkspaces` (retired), fleets and throughput pools (next by size) |
| f35 | Compute: Azure Compute Gallery, SSH public keys, proximity placement groups, dedicated hosts (the application inventory's `no kind` rows `galleries`, `galleries/images`, `.../versions`, `galleries/applications`, `.../versions`, `sshPublicKeys`, `proximityPlacementGroups`, `hostGroups`, `hostGroups/hosts`; tier 2) | rechecked 2026-11-23, read-only (git): ASO `main`'s `compute.yaml` exports availability sets, capacity reservations, disks, disk accesses and encryption sets, images, snapshots, VMs, scale sets and extensions, none of these; no pull request head from #5780 on adds one | **built**, generated: `compute.azure.hfl.dev/v20260303` (GalleryRP, the newest stable) `Gallery`, `GalleriesImage`, `GalleriesImagesVersion` (its source a managed image, VM or image version by reference, or OS and data disk images; disk encryption sets by reference), `GalleriesApplication`, `GalleriesApplicationsVersion`; `v20250401` (our `VirtualMachine`'s version; its CRD unchanged) `SshPublicKey`, `ProximityPlacementGroup`, `HostGroup`, `HostGroupsHost`. Labels, reviewed: a source's `id` and `virtualMachineId` and an image encryption's `diskEncryptionSetId` `arm` (autolabel wrote `simple` for the last two), a community gallery image's ID `simple`. No patch, no extension. Proven: conformance; the existing VM tests unchanged; the fake's rules (`gallery.go`: gallery names, `X.Y.Z` version names, an image version's source existing, an application version's package, host group fault domains). Not proven: replication to target regions, live runs (pending; a dedicated host is billed by the hour). Not built: gallery scripts and in-VM access control profiles (next by size), cloud services (classic, retiring), restore point collections and restore points, VM and scale set VM run commands of the diagnostic kind, workload spaces (preview) |
| f36 | Static Web Apps and more App Service site children (the application inventory's `no kind` rows `staticSites` and its custom domains, linked backends, user-provided function apps, database connections and private endpoint connections; `sites/domainOwnershipIdentifiers`, `privateEndpointConnections`, `publicCertificates`, `virtualNetworkConnections`, `hybridconnection`; tier 2) | rechecked 2026-11-23, read-only (git): ASO `main`'s `web.yaml` exports `Site`, `ServerFarm` and `SitesSourcecontrol` only; no pull request head from #5780 on adds a static site or these children | **built**, generated: `web.azure.hfl.dev/v20260715` `StaticSite` (its GitHub repository token a Secret reference, x-ms-secret in the spec), `StaticSitesCustomDomain` (the TXT validation token Azure makes stays in the status: `$importSecretMode: never`, autolabel wrote `required`), `StaticSitesLinkedBackend`, `StaticSitesUserProvidedFunctionApp`, `StaticSitesDatabaseConnection` (its connection string a Secret reference and removed from the status), `StaticSitesPrivateEndpointConnection`; on ASO's `Site`: `SitesDomainOwnershipIdentifier`, `SitesPrivateEndpointConnection`, `SitesPublicCertificate`, `SitesVirtualNetworkConnection`, `SitesHybridConnection` (generated as `Sites_Hybridconnection`; its Relay connection strings Secret references, removed from the status: the spec does not mark them secret). Labels, reviewed: linked backend, function app, database and network IDs `arm`; a domain ownership identifier's `id` (its value) `simple`, autolabel wrote `arm`. No patch, no extension. Proven: conformance, the existing App Service tests unchanged, the fake's rules (`staticsites.go`). Not built: static site app settings (`config/appsettings`, `functionappsettings`; settings with secrets want the hand-written treatment of `SitesConfigAppSettings`, c4c), basic auth (no DELETE), builds and their children (made by deployments), slot copies of the new site children, site extensions (no body), `networkConfig/virtualNetwork` and `privateAccess` (fixed names, for a next row) |
| f37 | Private endpoint connections of application services (the application inventory's `no kind` rows `Microsoft.Sql/servers/privateEndpointConnections`, `Microsoft.App/containerApps/privateEndpointConnections`, `Microsoft.Compute/diskAccesses/privateEndpointConnections`, `Microsoft.Cache/redis/privateEndpointConnections`; tier 2, each small; a platform team approves the connections an app's private endpoints ask for) | rechecked 2026-11-23, read-only (git, a blob-less fetch of `main` `f451aaa` and the 70 open pull request heads from #5780 on): ASO's `sql.yaml` exports no connection (job agents and DNS aliases are commented out), its `app.yaml`, `compute.yaml` and `cache.yaml` none; no pull request adds one (ASO's `appconfiguration.yaml` prunes its store's connection type) | **built**, generated: `sql.azure.hfl.dev/v20250101` `ServersPrivateEndpointConnection` (owned by ASO's `Server`; the private endpoint's ID a reference, label reviewed), `app.azure.hfl.dev/v20260701` `ContainerAppsPrivateEndpointConnection` (the first stable version with them; owned by ASO's `ContainerApp`), `compute.azure.hfl.dev/v20260302` `DiskAccessesPrivateEndpointConnection` (DiskRP, a new included version; owned by ASO's `DiskAccess`, a new external owner: `external-owners.txt`, `controllers.OwnerTypes()`, `config/rbac/role.yaml`, the test CRDs) and a new group, `cache.azure.hfl.dev/v20241101` `RedisPrivateEndpointConnection` (`specification/redis` added to `generator/specs.txt`; ASO's `Redis` was already an owner type). No patch or extension. Proven: `TestPrivateEndpointConnections` (each owned by its ASO stand-in by name; a connection a private endpoint made approved, then rejected, then deleted; one no endpoint made refused), the conformance test, and the fake's rule (`privateendpoints.go`: a PUT cannot make a connection, `TestPrivateEndpointConnections` in `pkg/fakecloud/arm`). The other application services' connections are f38 |
| f38 | Private endpoint connections, second part (the application inventory's `no kind` rows `Microsoft.Cache/redisEnterprise/privateEndpointConnections`, `Microsoft.AppConfiguration/configurationStores/privateEndpointConnections`, `Microsoft.DBforPostgreSQL/flexibleServers/privateEndpointConnections`, `Microsoft.DocumentDB/mongoClusters/privateEndpointConnections`; tier 2) | rechecked 2026-11-23 with f37, read-only: ASO's `cache.yaml`, `appconfiguration.yaml` (which prunes the connection type), `dbforpostgresql.yaml` and `documentdb.yaml` on `main` and in the open pull requests from #5780 on export none | **built**, generated, each at the version of its ASO owner: `cache.azure.hfl.dev/v20250401` `RedisEnterprisePrivateEndpointConnection` (owned by ASO's `RedisEnterprise`; `specification/redisenterprise`), a new group `appconfiguration.azure.hfl.dev/v20240601` `ConfigurationStoresPrivateEndpointConnection` (ASO's `ConfigurationStore`; the private endpoint's ID a reference, label reviewed), `dbforpostgresql.azure.hfl.dev/v20250801` `FlexibleServersPrivateEndpointConnection` (ASO's `FlexibleServer`; the first generated kind in the group of our hand-written PostgreSQL `User`, as another version of it), `documentdb.azure.hfl.dev/v20240701` `MongoClustersPrivateEndpointConnection` (ASO's `MongoCluster`; `specification/mongocluster`; its state under `properties`). Three new external owners (`RedisEnterprise`, `ConfigurationStore`, `MongoCluster`: `external-owners.txt`, `controllers.OwnerTypes()` and the scheme, `config/rbac/role.yaml`, the test CRDs); four spec services added to `generator/specs.txt`. No patch or extension. Proven: `TestPrivateEndpointConnections` (now eight owners), the conformance test and the fake's rule (`privateendpoints.go`, widened). Not built: a Redis Enterprise cluster's `migrations` (an operation, preview behaviour) |
| f39 | Azure SQL Elastic Jobs and DNS aliases (the application inventory's `no kind` rows `Microsoft.Sql/servers/jobAgents`, `jobAgents/credentials`, `jobAgents/jobs`, `jobAgents/jobs/steps`, `jobAgents/targetGroups`, `jobAgents/privateEndpoints`, `servers/dnsAliases`; tier 2, `Microsoft.Sql` the service with the most `no kind` rows left) | rechecked 2026-11-23 with f37, read-only: ASO's `sql.yaml` on `main` and in every open pull request from #5780 on has `Servers_JobAgent` and `Servers_DnsAliase` commented out ("Not sure how this works - it has no properties but name?") and nothing else of either | **built**, generated: `sql.azure.hfl.dev/v20250101` `ServersJobAgent` (its job database `databaseReference`, ASO's `ServersDatabase` by object or an ARM ID), `ServersJobAgentsCredential` (the password a Secret reference, `$importSecretMode: required`, not in the status), `ServersJobAgentsJob`, `ServersJobAgentsJobsStep` (its target group, credential and output credential references: autolabel wrote `simple`, reviewed to `arm` so a step names our target group and credential by object), `ServersJobAgentsTargetGroup`, `ServersJobAgentsPrivateEndpoint` and `ServersDnsAlias` (a name only, as ASO's note says; generated as `Servers_DnsAlias`). Proven: `TestSQLElasticJobs` (the whole chain on ASO's `Server` and `ServersDatabase` stand-ins, the step changed, a step naming another agent's credential refused, all deleted), the conformance test, and the fake's rules (`sqljobs.go`: the agent's database exists, a credential's username and password, a step's target group and credentials exist in its own agent; `TestSQLJobs`). Not built: job executions and job versions (operations and read-only history) |
| f40 | App Service: the rest of a site's children, and a slot's (the application inventory's `no kind` rows `Microsoft.Web/sites/sitecontainers`, `functions`, `premieraddons`, `privateAccess`, and under `sites/slots` those and `config` (299 attributes), `basicPublishingCredentialsPolicies`, `domainOwnershipIdentifiers`, `privateEndpointConnections`, `publicCertificates`, `virtualNetworkConnections`, `hybridconnection`; tier 2, `Microsoft.Web` the service with the most `no kind` rows left) | rechecked 2026-11-23, read-only (git; the GitHub API is closed to this session): the latest release tag is still v2.22.0; ASO `main` (`f451aaa`) exports `Site`, `ServerFarm` and `SitesSourcecontrol` from `web.yaml`, none of these; no pull request head from #5780 on (70 fetched) changes `web.yaml` | **built**, generated: `web.azure.hfl.dev/v20260715` on ASO's `Site`: `SitesSitecontainer` (sidecar containers; the registry password `passwordSecret` a Secret reference, removed from the status), `SitesFunction`, `SitesPremieraddon`, `SitesPrivateAccess` (`privateAccess/virtualNetworks`, a fixed name; a network by reference, autolabel reviewed); on our `SitesSlot` the same four (`SitesSlots...`) and `SitesSlotsConfigAuthSettingsV2`, `SitesSlotsConfigLogs`, `SitesSlotsBasicPublishingCredentialsPolicyFtp`, `...Scm` (fixed names, `generator/fixed-names.txt`), `SitesSlotsDomainOwnershipIdentifier`, `SitesSlotsPrivateEndpointConnection`, `SitesSlotsPublicCertificate`, `SitesSlotsVirtualNetworkConnection`, `SitesSlotsHybridConnection`. The fixed names with no DELETE (private access, the slot's sections and policies) leave Azure's section with the app when deleted (`site_config_extensions.go`, as the site's). Not built: function keys (`functions/{f}/keys/{k}` has a PUT and no GET, which ASO's reconciler needs; a hand-written kind could), the Relay connections under `hybridConnectionNamespaces/{namespace}/relays` (that segment is no resource, so the generator gave them a resource group as owner), `networkConfig/virtualNetwork` (ASO's `Site` and our `SitesSlot` carry the same connection as `virtualNetworkSubnetId`: two writers would fight), `siteextensions` (a PUT with no body), deployments, MSDeploy and OneDeploy (operations, not configuration), and a slot's app settings and connection strings (hand-written for the site, c4c; the slot's are the next step there). Proven: `TestWebSiteAndSlotChildren` (containers on the site and slot, the password kept out of the status, a second main container refused, a function, private access, a slot's logs and FTP policy left with the slot when deleted and gone with it), the conformance test, and the fake's rules (`sitechildren.go`; private endpoint connections of apps, slots and static web apps now as the other services'). Live: `TestLiveSiteContainers` written, pending (`docs/live-runs.md`) |
| f41 | App Service Environment v3 (the application inventory's `no kind` rows `Microsoft.Web/hostingEnvironments`, `hostingEnvironments/configurations`, `hostingEnvironments/privateEndpointConnections`; tier 2; the isolated, network-injected hosting an ASO `Site` names in `hostingEnvironmentProfile`) | rechecked 2026-11-23 with f40, read-only (git): no release tag past v2.22.0; ASO `main` (`f451aaa`) and the pull request heads from #5780 on export no `HostingEnvironment` (`web.yaml` unchanged in every head) | **built**, generated: `web.azure.hfl.dev/v20260715` `HostingEnvironment` (owned by ASO's `ResourceGroup`; its subnet `virtualNetwork.id` a reference, autolabel reviewed; the spec's embedded networking and DNS suffix configurations are kept out of its spec by the generator, as embedded resources), `HostingEnvironmentsConfigurationNetworking` and `HostingEnvironmentsConfigurationCustomDnsSuffix` (fixed names `configurations/networking` and `configurations/customdnssuffix`, `generator/fixed-names.txt`; networking has no DELETE, so deleting the object leaves it with the environment, `site_config_extensions.go`), `HostingEnvironmentsPrivateEndpointConnection`. Not built: `multiRolePools/default` and `workerPools` (only the retired App Service Environment v1 and v2 use them; v3 scales its workers with its plans). Proven: `TestAppServiceEnvironment` (the environment in a subnet, its networking changed and left with it, a custom DNS suffix without its certificate refused, a connection approved, all gone with the environment), the conformance test, and the fake's rules (`hostingenvironments.go`). Live: not written (an environment takes one to two hours to make and bills an Isolated v2 instance while it exists) |
| f42 | Azure SQL Managed Instance children and instance pools (the application inventory's `no kind` rows `Microsoft.Sql/managedInstances/administrators`, `azureADOnlyAuthentications`, `encryptionProtector`, `keys`, `securityAlertPolicies`, `vulnerabilityAssessments`, `advancedThreatProtectionSettings`, `privateEndpointConnections`, `dnsAliases`, `serverTrustCertificates`, `startStopSchedules`, `dtc`, `serverConfigurationOptions`, `sqlAgent`, `distributedAvailabilityGroups`, the database's `backupShortTermRetentionPolicies`, `backupLongTermRetentionPolicies`, `securityAlertPolicies`, `vulnerabilityAssessments`, `advancedThreatProtectionSettings`, `ledgerDigestUploads`, and `instancePools`; tier 2, `Microsoft.Sql` still the service with the most `no kind` rows) | rechecked 2026-11-23, read-only (git): ASO's `sql.yaml` on `main` (`f451aaa`) exports `ManagedInstance`, `ManagedInstancesDatabase`, its TDE and `InstanceFailoverGroup` (v2.22.0) and no other instance child; the one pull request head from #5780 on that touches `sql.yaml` (#5808, release documentation) changes only their `$supportedFrom` | **built**, generated: `sql.azure.hfl.dev/v20250101`, the 21 children above owned by ASO's `ManagedInstance` and `ManagedInstancesDatabase` (new external owners: `external-owners.txt`, `controllers.OwnerTypes()`, `config/rbac/role.yaml`, the test CRDs), as `ManagedInstances...` and `ManagedInstancesDatabases...`, and `InstancePool` in a resource group (its subnet a reference: autolabel wrote `simple`, reviewed). Labels as ASO's `sql.yaml` has them for the server's: storage keys and SAS tokens Secret references (removed from the status), a container path and an administrator's SID and tenant optionally from a ConfigMap; a private endpoint's ID a reference. The singletons with no DELETE (encryption protector, security alert policies, threat protection, DTC, SQL Agent, server configuration option, short-term retention, ledger digest uploads) take `detach-on-delete`, as ASO's own server singletons and our geo-backup policy do. Not built: vulnerability assessment rule baselines and column sensitivity labels (the `rules`, `schemas`, `tables` and `columns` segments between are no resources, so the generator cannot own them), a restorable dropped database's retention (operational), virtual clusters (no PUT). Proven: `TestSQLManagedInstanceChildren` (Entra-only authentication on an instance with no Entra administrator refused; an Entra admin by ConfigMap, then Entra-only authentication once the admin is Ready (made together, the test waited on ASO's retry backoff and timed out under full-suite load, 2026-11-23), a Key Vault key and the protector naming it, a database's assessment with its SAS kept out of the status, retention changed, a schedule; a protector naming a missing key refused; deleted, the protector detached), the conformance test, and the fake's rules (`sqlmanagedinstance.go`). Live: not written (a managed instance takes hours to make and bills by the vCore-hour) |
| f43 | Azure SQL server and database leftovers (the application inventory's `no kind` rows `Microsoft.Sql/servers/devOpsAuditingSettings`, `servers/databases/dataMaskingPolicies`, `maintenanceWindows`, `ledgerDigestUploads`, `workloadGroups` and their `workloadClassifiers`, `servers/sqlVulnerabilityAssessments/baselines` and their `rules`; tier 2) | rechecked 2026-11-23 with f42, read-only (git): ASO's `sql.yaml` on `main` has `Servers_Databases_DataMaskingPolicy`, `..._ExtendedAuditingSetting`, `..._MaintenanceWindow`, `..._WorkloadGroup`, `..._WorkloadGroups_WorkloadClassifier`, the sync kinds and `Servers_SyncAgent` commented out ("of questionable value to users, but if we get an ask for them we can add them in"); no pull request head from #5780 on adds one | **built**, generated: `sql.azure.hfl.dev/v20250101` `ServersDevOpsAuditingSetting` (Microsoft support operations auditing; its storage key a Secret reference, removed from the status), `ServersDatabasesDataMaskingPolicy`, `ServersDatabasesMaintenanceWindow` (`maintenanceWindows/current`; generated as `Servers_Databases_MaintenanceWindow`), `ServersDatabasesLedgerDigestUpload`, `ServersDatabasesWorkloadGroup`, `ServersDatabasesWorkloadGroupsWorkloadClassifier`, `ServersSqlVulnerabilityAssessmentsBaseline` and `...BaselinesRule` (under our `ServersSqlVulnerabilityAssessment`, c20). The singletons with no DELETE take `detach-on-delete`. Not built: extended auditing settings (the server's and a database's: the same auditing policy as ASO's `ServersAuditingSetting` and `ServersDatabasesAuditingSetting` with a `predicateExpression`; two kinds writing one policy would fight, so it is a field for ASO's kinds; draft issue: "sql: export the extended auditing settings, or add `predicateExpression` to the auditing settings"), data masking rules (a PUT and no GET), SQL Data Sync groups, members and agents (Data Sync is retired on 2027-09-30), database vulnerability assessment baselines and sensitivity labels (the segments between them and the database are no resources), and import, export and replication links (operations). Proven: `TestSQLServerAuditingAndWorkloads` (each kind on ASO's `Server` and `ServersDatabase` stand-ins, the storage key kept out of the status, a group changed and one whose minimum is above its cap refused, deleted or detached), the conformance test, and the fake's rules (`sqlserverextras.go`). Live: `TestLiveSqlServerExtras` written, pending (`docs/live-runs.md`) |
| f44 | Microsoft Foundry's newer children (the application inventory's `no kind` rows `Microsoft.CognitiveServices/accounts/managedNetworks` and their `outboundRules`, `accounts/raitopics`, `accounts/raiToolLabels`, `accounts/projects/applications` and their `agentDeployments`, `raiPolicy` and `raiExternalSafetyProviders` at a subscription; tier 2; only in the 2026-09-01 spec) | rechecked 2026-11-23, read-only (git): ASO's `cognitiveservices.yaml` on `main` (`f451aaa`) exports `Account`, `Deployment` and `Project` at 2025-06-01 only; no pull request head from #5780 on changes it | **built**, generated: `cognitiveservices.azure.hfl.dev/v20260901` (a second version in our group, for new kinds only; the 2025-06-01 kinds stay where they are): `AccountsManagedNetwork` (named `default`; its outbound rules may also be set in its own spec, so use one or the other for a rule), `AccountsManagedNetworksOutboundRule` (one of FQDN, private endpoint, service tag; a private endpoint's target a reference, autolabel reviewed), `AccountsRaiTopic`, `AccountsRaiToolLabel`, `AccountsProjectsApplication` (agent applications), `AccountsProjectsApplicationsAgentDeployment` (hosted or managed), all owned by ASO's `Account` and `Project` (2025-06-01; an owner is named by kind) or each other; `RaiPolicy` and `RaiExternalSafetyProvider`, directly under a subscription (patch 0009: owned by the subscription's ARM ID). The spec does not flatten their `properties`, so the kinds keep a `properties` object. Not built: `quotaTiers/default` (a subscription's quota tier, a PATCH-style setting with no DELETE, an operator's choice rather than a platform's), `testRaiExternalSafetyProvider` (an action: a PUT with no GET). Proven: `TestFoundryNetworkAndAgents` (a V2 managed network with an FQDN and a private endpoint rule, a RAI topic, an agent application changed and its managed deployment, a subscription's RAI policy; a move back to V1 refused; all deleted), the conformance test, and the fake's rules (`foundry.go`). Live: not written (FQDN rules provision a billed Azure Firewall, agent applications need agents made in Foundry's data plane, and a RAI topic trains from a sample blob) |
| f45 | Compute leftovers: restore points and gallery scripts and access control profiles (the application inventory's `no kind` rows `Microsoft.Compute/restorePointCollections` and their `restorePoints`, `galleries/scripts` and their `versions`, `galleries/inVMAccessControlProfiles` and their `versions`; tier 2) | rechecked 2026-11-23, read-only (git): ASO's `compute.yaml` on `main` (`f451aaa`) exports none of these; no pull request head from #5780 on changes it | **built**, generated: `compute.azure.hfl.dev/v20250401` `RestorePointCollection` (its source VM a reference, autolabel reviewed: ASO's or our `VirtualMachine`, or an ARM ID) and `RestorePointCollectionsRestorePoint`, at the version of our `VirtualMachine`; `v20260303` `GalleriesScript`, `GalleriesScriptsVersion` (its script a blob link, kept as a string as an application version's `mediaLink` is), `GalleriesInVMAccessControlProfile` and `GalleriesInVMAccessControlProfilesVersion` (the metadata security protocol's rules), beside f35's gallery kinds. The 2026-03-03 spec does not flatten the script and profile `properties`, so those kinds keep a `properties` object. Not built: scale set VM instances and their extensions and run commands (`virtualMachineScaleSets/virtualMachines/*`: instances a scale set makes, not declared), `cloudServices` (Cloud Services (extended support), retired on 2027-03-31), `workloadSpaces` (preview only), `interconnectBlocks`. Proven: `TestComputeRestorePointsAndGalleryScripts` (a collection of ASO's `VirtualMachine` by object and a restore point, a script and its version, a change, a version named by a word refused by the CRD and a collection of a missing VM by the fake), the conformance test, and the fake's rules (`gallery.go`). Live: `TestLiveRestorePoints` written, pending (`docs/live-runs.md`) |
| f46 | Azure Cosmos DB for PostgreSQL (Citus) clusters (the application inventory's `no kind` rows `Microsoft.DBforPostgreSQL/serverGroupsv2` and its `coordinatorConfigurations`, `nodeConfigurations`, `firewallRules`, `roles`, `privateEndpointConnections`; tier 2, `specification/postgresqlhsc`; the previous session left it for want of disk) | rechecked 2026-11-23, read-only (git, a blob-less fetch of `main` and the 70 open pull request heads from #5780 on): v2.22.0 still the latest release; ASO `main` (`f451aaa`) has no `serverGroupsv2` kind and no `postgresqlhsc` spec in its generator configuration; the pull request heads that touch `dbforpostgresql` (#5804 reformatting, #5805 the specs update, #5844 and #5845 ours, #5848 property tests) change only the flexible server and the PostgreSQL user | **built**, generated from the newest stable spec, 2022-11-08 (`specification/postgresqlhsc` added to `generator/specs.txt`; 2023-03-02-preview has more, such as Entra authentication and customer-managed keys, but is a preview): `dbforpostgresql.azure.hfl.dev/v20221108` `ServerGroupsv2` (a cluster in a resource group; its administrator password a Secret reference, removed from the status by the spec's `x-ms-secret`; a read replica's or restore's source cluster `sourceResourceId` a reference, `$referenceType: arm`, so another `ServerGroupsv2` by object or an ARM ID), `ServerGroupsv2CoordinatorConfiguration` and `ServerGroupsv2NodeConfiguration` (a server parameter on the coordinator or the workers; no DELETE, so `detach-on-delete`), `ServerGroupsv2FirewallRule` (IPv4 addresses, the spec's pattern in the CRD), `ServerGroupsv2Role` (its password a Secret reference) and `ServerGroupsv2PrivateEndpointConnection`, in the group of our PostgreSQL `User`, as other versions of it. Kind names are the ARM type's, as the generator gives them. Not built: the cluster's `servers` and `configurations` (read-only, no PUT) and the restart, start, stop and promote actions (POST). Proven: `TestCosmosDBForPostgreSQL` (a single-node cluster with its password from a Secret and a maintenance window, a firewall rule, a role, a coordinator and a node configuration, a read replica naming the cluster by object; a change PUT again; an IPv6 address refused by the CRD and a node count of 1 by the fake; deleted, the configurations detached), the conformance test, and the fake's rules (`cosmospostgres.go`; private endpoint connections in `privateendpoints.go`). Live: `TestLiveCosmosPostgres` written, pending (`docs/live-runs.md`) |
| f47 | Cosmos DB fleets and a MongoDB vCore cluster's Entra users (the application inventory's `no kind` rows `Microsoft.DocumentDB/fleets`, `fleets/fleetspaces`, `fleets/fleetspaces/fleetspaceAccounts`, `mongoClusters/users`; tier 2) | rechecked 2026-11-23 with f46, read-only (git): ASO's `documentdb.yaml` on `main` (`f451aaa`) exports `MongoCluster` and its firewall rules at 2024-07-01 and no fleet or user; the pull request heads that touch `documentdb` (#5804 reformatting, #5805 the specs update) regenerate the existing kinds only | **built**, generated: `documentdb.azure.hfl.dev/v20260315` `Fleet` (in a resource group), `FleetsFleetspace` (NoSQL, its tier, data regions and throughput pool) and `FleetsFleetspacesFleetspaceAccount` (the Cosmos DB account it pools a reference, `$referenceType: arm`: ASO's `DatabaseAccount` by object or an ARM ID), beside f34's role-based access kinds; `v20260601` `MongoClustersUser` (from `specification/mongocluster`, the newest stable version; ASO's `MongoCluster` is at 2024-07-01, which has no users), owned by ASO's `MongoCluster`, named by the Entra principal's object ID, its identity provider a one-of (Microsoft Entra ID only) and its roles. Not built: `notebookWorkspaces` (the retired notebooks feature, no writable attribute), and the preview-only `throughputPools`, `copyJobs`, `dataTransferJobs` and `chaosFaults` (the last three operations). Proven: `TestCosmosFleetsAndMongoUsers` (a fleet, a fleetspace with a pool changed, an account of ASO's `DatabaseAccount` stand-in by object, a service principal's user on ASO's `MongoCluster` stand-in; a pool's minimum above its maximum and a user named by a word refused; all deleted), the conformance test, and the fake's rules (`cosmosfleet.go`). Live: `TestLiveCosmosFleet` (the fleet and a fleetspace, free) written, pending (`docs/live-runs.md`); the users not written (a vCore cluster with Entra authentication, ASO's 2024-07-01 `MongoCluster` cannot turn it on) |
| f48 | Storage account newcomers (the application inventory's `no kind` rows `Microsoft.Storage/storageAccounts/connectors`, `dataShares`, `advancedPlatformMetrics`, `blobAccessPointConfigurations`, `contextCaches` and their `contextCacheContainers`; tier 2; c11 had left the 2026-06-01 ones) | rechecked 2026-11-23 with f46, read-only (git): ASO's `storage.yaml` on `main` (`f451aaa`) exports the account, its services, containers, shares, queues, tables and management policy at 2026-06-01 and none of these; the one pull request head that touches `storage` (#5805, the specs update) regenerates the existing kinds only | **built**, generated: `storage.azure.hfl.dev/v20260601` (ASO's hub version) `StorageAccountsDataShare` (a share of folders or blobs to managed identities), `StorageAccountsConnector` (reaching another account's data share, authenticated as a user-assigned identity by reference: autolabel wrote `arm`, reviewed), `StorageAccountsAdvancedPlatformMetric` (named `ContainerLevelCapacityMetrics`, the only rule type), `ContextCache` (in a resource group) and `ContextCachesContextCacheContainer`; `v20260901` `StorageAccountsBlobAccessPointConfiguration` (new in that version: an S3-compatible, NetApp, Dell, Qumulo, Nasuni or Commvault store exposed as blobs; its secret access key a Secret reference, not in the status). Owned by ASO's `StorageAccount`. A data share's access policies and assets cannot be sent empty (the generated types omit an empty list), so a share has at least one of each. Still not built: container immutability policies (`blobServices/default/containers/{c}/immutabilityPolicies/default`, stable 2026-09-01): their DELETE requires an `If-Match` header with the policy's ETag (so do the `extend` and `lock` POSTs, and a PUT that changes an existing policy should send one), and ASO's generic reconciler sends no `If-Match` on any call, so a generated kind's delete would always be refused and the object never go; a locked policy cannot be deleted at all. A hand-written kind on ASO's reconciler that reads the ETag and sends it (as `VaultsProtectedItem` adds its own calls) would be the way; not small, and version-level immutability (`immutableStorageWithVersioning` on ASO's account and container) covers most uses (recorded 2026-11-23). Proven: `TestStorageNewcomers` (each kind on ASO's `StorageAccount` stand-in, the access point's key from a Secret, the access point made inactive and the connector changed; a move of the access point to another source type refused; all deleted), the conformance test, and the fake's rules (`storagenewcomers.go`). Live: `TestLiveStorageDataShare` written, pending (`docs/live-runs.md`); the others not written (a connector needs another tenant's share, an access point an S3-compatible store, the metrics rule bills as custom metrics) |
| f49 | Container Apps sandbox groups and Azure SRE Agent (the application inventory's `no kind` rows `Microsoft.App/sandboxGroups` and their `vnetConnections`, `agents` and their `connectors`, and `managedEnvironments/daprComponents/resiliencyPolicies`; tier 2) | rechecked 2026-11-23 with f46, read-only (git): ASO's `app.yaml` on `main` (`f451aaa`) exports 2025-01-01 and 2024-03-01 kinds only, no sandbox group, agent or resiliency policy; the pull request heads that touch `app` (#5804 reformatting, #5805 the specs update, #5849 property tests) change only the existing kinds | **built**, generated at 2026-07-01, the version our `ContainerApp` and container app connections are at: `app.azure.hfl.dev/v20260701` `SandboxGroup` (in a resource group; the Container Apps environment it links a reference, ASO's `ManagedEnvironment` by object or an ARM ID, as the spec marks it), `SandboxGroupsVnetConnection` (a subnet by reference), and from the same version's `SreAgent` spec `Agent` (an Azure SRE Agent: its action mode and access level, identities and subnet by reference, sandbox egress and packages, knowledge graph, incident management with its connection key and Application Insights connection string as Secret references) and `AgentsConnector` (its data source string a Secret reference). No label was needed: the TypeSpec specs mark their ARM IDs. Not built: a Dapr component's `resiliencyPolicies`, which exist only from 2026-07-01 while our `ManagedEnvironmentsDaprComponent` is a 2025-01-01 kind: generated at 2026-07-01 its owner would be that version's unexported `ManagedEnvironments_DaprComponent`, reachable by ARM ID only (a second version of the component kind is the builder's call, §2, "One version"); the preview-only `builders`, `builds` and container app `resiliencyPolicies`; `logicApps` (no writable attribute). Proven: `TestAppSandboxesAndAgents` (a sandbox group linked to ASO's `ManagedEnvironment` stand-in by object and its network connection, an agent in review mode with AzureVNet egress through a subnet, and a connector with its data source from a Secret; a change PUT again; a sandbox group's move to another environment and an agent's AzureVNet egress without a subnet refused; all deleted), the conformance test, and the fake's rules (`appextras.go`). Live: `TestLiveSandboxGroup` (a group with no environment, free) written, pending (`docs/live-runs.md`); the network connection and agents not written (an agent bills per agent-hour) |
| f50 | A Recovery Services vault's settings singletons (the application inventory's `no kind` rows `Microsoft.RecoveryServices/vaults/backupconfig`, `backupstorageconfig`, `backupEncryptionConfigs`; tier 2; `docs/handoff.md` h1 item 3, a hand-written candidate before patch 0014) | rechecked 2026-11-23 with f46, read-only (git): ASO has no `recoveryservices` group on `main` (`f451aaa`), and no open pull request head from #5780 on touches one | **built**, generated, no hand-written code: `recoveryservices.azure.hfl.dev/v20260701` `VaultsBackupConfig` (`backupconfig/vaultconfig`: soft delete and its retention, enhanced security), `VaultsBackupStorageConfig` (`backupstorageconfig/vaultstorageconfig`: storage redundancy, cross-region restore) and `VaultsBackupEncryptionConfig` (`backupEncryptionConfigs/backupResourceEncryptionConfig`: customer-managed keys, infrastructure encryption), fixed-name children of our `Vault` by generator patch 0014 (`generator/fixed-names.txt`); no DELETE in the spec, so each takes `detach-on-delete`, as f42's singletons. Not built: the vault's `extendedInformation/vaultExtendedInfo` (the legacy agent registration's integrity and encryption keys, which the spec does not mark secret: a kind would keep them in its spec), `certificates` (an upload with no GET), the Site Recovery settings and fabrics (`replication*`), and protection containers and intents (h1 item 2). Proven: `TestRecoveryServicesVaultSettings` (each on our `Vault`; retention and redundancy changed; soft delete turned off after AlwaysON, a redundancy change once an item is protected, and a move back to Microsoft-managed keys refused; deleted, each detached and kept), the conformance test, and the fake's rules (`vaultsettings.go`). Live: `TestLiveVaultSettings` written, pending (`docs/live-runs.md`) |
| f51 | DevTest Labs (the application inventory's `no kind` rows `Microsoft.DevTestLab/labs` and its `artifactsources`, `costs`, `customimages`, `formulas`, `notificationchannels`, `schedules`, `servicerunners`, `users` and their `disks`, `environments`, `secrets`, `servicefabrics` and their `schedules`, `virtualmachines` and their `schedules`, `virtualnetworks`, `policysets/policies`; tier 2, `Microsoft.DevTestLab` at 4.7% with only the global auto-shutdown `Schedule` built) | rechecked 2026-11-23, read-only (git, a blob-less fetch): v2.22.0 still the latest release; ASO `main` still `f451aaa` and no pull request head newer than #5857; ASO has no `devtestlab` group on `main`, and no open pull request head touches one | **built**, generated from the newest stable spec, 2018-09-15 (the version of our `Schedule`), no hand-written code: `devtestlab.azure.hfl.dev/v20180915` `Lab` (in a resource group), `LabsVirtualNetwork` (a virtual network and its subnets by reference, ASO's or an ARM ID), `LabsSchedule` (a lab's `LabVmsShutdown` and `LabVmAutoStart`), `LabsNotificationChannel`, `LabsArtifactSource` (its repository token a Secret reference), `LabsCost` (`targetCost`; no DELETE, so `detach-on-delete`), `LabsCustomImage`, `LabsFormula` (its VM's password and SSH key Secret references), `LabsServiceRunner`, `LabsUser`, `LabsUsersDisk`, `LabsUsersEnvironment`, `LabsUsersSecret` (its value a Secret reference), `LabsUsersServiceFabric`, `LabsUsersServicefabricsSchedule`, `LabsVirtualMachine` (its password and SSH key Secret references; its lab network, custom image and environment by reference) and `LabsVirtualmachinesSchedule`; the secrets are removed from the status (`typeTransformers`); the lab's network, images, disks and VMs are `$referenceType: arm`, so our kinds by object or an ARM ID. Not built: a lab's policies (`policysets/{policySetName}/policies`: the policy set is no resource, so the generator cannot own them; the set is always `default`, a fixed-name middle segment patch 0014 does not handle). Proven: `TestDevTestLabs` (a lab, its network on a virtual network by ARM ID, its shutdown changed, a notification channel, a private artifact source, a cost target, a user's secret and a lab VM on the lab network by object with its shutdown; no token, secret or password in a status; a lab schedule with another name and a VM on a missing lab subnet refused; deleted, the cost target detached and gone with the lab), the conformance test, and the fake's rules (`devtestlab.go`). Live: `TestLiveDevTestLab` (a lab with no VM, its schedule, channel and cost target) written, pending (`docs/live-runs.md`) |
| f52 | Azure Site Recovery (the application inventory's `no kind` rows `Microsoft.RecoveryServices/vaults/replicationFabrics`, `replicationFabrics/replicationProtectionContainers` and their `replicationProtectionContainerMappings`, `replicationProtectedItems`, `replicationMigrationItems` and `replicationProtectionClusters`, `replicationFabrics/replicationRecoveryServicesProviders`, `replicationvCenters`, `replicationNetworks/replicationNetworkMappings`, `replicationStorageClassifications/replicationStorageClassificationMappings`, `vaults/replicationPolicies`, `replicationRecoveryPlans`, `replicationAlertSettings`, `replicationVaultSettings`, `replicationProtectionIntents`; tier 2, `Microsoft.RecoveryServices` the application service with the most uncovered attributes; disaster recovery of an app's VMs to another region) | rechecked 2026-11-23 with f51, read-only (git): ASO has no `recoveryservices` group on `main` (`f451aaa`), and no open pull request head touches one | **built**, generated from `specification/recoveryservicessiterecovery` 2026-07-01 (added to `generator/specs.txt`; the version of our `Vault`), no hand-written code: `recoveryservices.azure.hfl.dev/v20260701` `VaultsReplicationFabric` (an Azure fabric per region, or the VMware and physical site fabrics), `VaultsReplicationProtectionContainer`, `VaultsReplicationProtectionContainerMapping` (its target container and policy by reference), `VaultsReplicationPolicy` (A2A, Hyper-V, InMage and VMware policies, one-of), `VaultsReplicationProtectedItem` (replication of a VM: its policy, the VM, the recovery container, resource group, network, availability set or zone, disks and encryption by reference), `VaultsReplicationMigrationItem`, `VaultsReplicationProtectionCluster`, `VaultsReplicationRecoveryServicesProvider`, `VaultsReplicationvCenter`, `VaultsReplicationRecoveryPlan` (its fabrics and replicated items by reference), `VaultsReplicationAlertSetting`, `VaultsReplicationVaultSetting` and `VaultsReplicationProtectionIntent`. Named `Vaults<Type>`, dropping the fabric and container segments: a CRD's plural is one DNS label of at most 63 characters, and `vaultsreplicationfabricsreplicationprotectioncontainersreplicationprotecteditems` is 81. The ARM IDs the spec describes are `$referenceType: arm` (autolabel wrote 60 as simple; reviewed: all but a lifecycle GUID and a job name are ARM IDs), and the A2A inputs the generator does not flag (the VM, the recovery container, resource group and disks, a mapping's target and policy, a plan's fabrics) are labelled too. Protection containers, alert settings, vault settings and protection intents have no DELETE (a container is removed with a POST): `detach-on-delete`. Not built: network and storage classification mappings (`replicationNetworks/{n}` and `replicationStorageClassifications/{s}` are no resources, so the generator cannot own them), and the POST actions (failover, test failover, commit, reprotect, remove). Proven: `TestSiteRecovery` (Azure to Azure on our `Vault`: a fabric per region, a container in each, a policy changed, a mapping by object, a VM by ARM ID replicated to a recovery resource group, a recovery plan naming the fabrics and the item by object, alert settings; a second fabric for a region and an item with no mapping refused; deleted, the containers and alert settings detached), the conformance test (a migration item needs its VMware input: a fixture), and the fake's rules (`siterecovery.go`). Live: `TestLiveSiteRecovery` (an empty vault's fabric, policy and alert settings, free) written, pending (`docs/live-runs.md`) |
| f53 | App Service leftovers (the application inventory's `no kind` rows `Microsoft.Web/sites/certificates`, `sites/slots/certificates`, `sites/slots/sourcecontrols`, `sites/virtualNetworkConnections/gateways`, `sites/slots/virtualNetworkConnections/gateways`, `staticSites/basicAuth`, `aigateways`, `kubeEnvironments`, `serverfarms/virtualNetworkConnections/gateways` and `routes`, `publishingUsers`, `sourcecontrols`; tier 2, `Microsoft.Web` still the service with the most `no kind` rows) | rechecked 2026-11-23 with f51, read-only (git): ASO's `web.yaml` on `main` (`f451aaa`) exports `ServerFarm`, `Site` and `SitesSourcecontrol` only; no open pull request head touches it | **built**, generated at 2026-07-15, no hand-written code: `web.azure.hfl.dev/v20260715` `SitesCertificate` and `SitesSlotsCertificate` (a certificate scoped to an app or slot: a PFX and its password from Secrets, as our `Certificate`, a Key Vault secret by reference, or a managed certificate), `SitesSlotsSourcecontrol` (`sourcecontrols/web` of our `SitesSlot`, as ASO's `SitesSourcecontrol` is an app's; the GitHub Actions registry password a Secret reference), `SitesVirtualNetworkConnectionsGateway` and `SitesSlotsVirtualNetworkConnectionsGateway` (the gateway of a gateway-required virtual network integration, owned by our f36 and f40 connections; no DELETE: `detach-on-delete`), `StaticSitesBasicAuth` (`basicAuth/default`, password protection of a static web app, the password a Secret reference; no DELETE, so f36 had left it: now `detach-on-delete`, as every no-DELETE kind since), `AiGateway` and `KubeEnvironment` (App Service on Azure Arc: the custom location, AKS cluster and subnets by reference; the Log Analytics shared key a Secret reference). Not built: a plan's virtual network gateways and routes (`serverfarms/{name}/virtualNetworkConnections/{vnet}` is no resource, so the generator cannot own them), `publishingUsers/web` and `sourcecontrols/{type}` (the calling user's own deployment credentials and OAuth tokens, not a resource a platform declares), and a static web app's settings (f36). Proven: `TestAppServiceLeftovers` (on ASO's `Site` stand-in and our `SitesSlot`: a certificate from a PFX kept out of the status and a slot's from Key Vault, a slot's source control changed, the integration gateway, basic authentication on a Standard static web app, an AI gateway, a Kubernetes environment on a custom location; a certificate with no source, a gateway not named primary, basic authentication on a Free app and an environment on a missing custom location refused; deleted, the gateway and basic authentication detached), the conformance test, and the fake's rules (`webextras.go`). Live: `TestLiveStaticSiteBasicAuth` (a Standard static web app, cents) written, pending (`docs/live-runs.md`) |
| f54 | Azure Monitor leftovers (the platform-foundation inventory's `no kind` rows `Microsoft.Monitor/pipelineGroups`, `accounts/metricsContainers`, `accounts/issues`, `observabilityAgents`, `slis`; tier 1, the foundation rows with no recorded reason left) | rechecked 2026-11-23 with f51, read-only (git): ASO's `monitor.yaml` on `main` (`f451aaa`) exports `Account` (2023-04-03) only; no open pull request head touches it | **built**, generated: `monitor.azure.hfl.dev/v20260401` `PipelineGroup` (a new group; `specification/monitoringservice` added to `generator/specs.txt`; 2026-04-01, its only stable version): the Azure Monitor pipeline at the edge, on an Arc-enabled cluster's custom location, with its syslog and OTLP receivers, batch and transformation processors, exporters to a data collection rule, TLS configurations and pipelines; in a resource group. One generator run, no label. Not built: the Azure Monitor workspace's `metricsContainers` (one attribute, at 2025-10-03 while ASO's `Account` is 2023-04-03: a new external owner for one setting; no DELETE) and `issues` (investigations Azure Monitor opens and a person works: operational), and `observabilityAgents` and `slis` (preview only). Proven: `TestMonitorPipelineGroup` (a group on a custom location with a syslog receiver, a batch processor and a workspace exporter in one pipeline; the batch size changed; a pipeline naming an undeclared exporter and a missing custom location refused; deleted), the conformance test (covered by that test: its smallest object needs a whole pipeline), and the fake's rules (`monitorpipeline.go`). Live: not written (a pipeline group needs an Arc-enabled Kubernetes cluster and a custom location, which the live resource group does not have) |
| f55 | A VM's diagnostic run commands (the application inventory's `no kind` rows `Microsoft.Compute/virtualMachines/diagnosticRunCommands` and `virtualMachineScaleSets/virtualMachines/diagnosticRunCommands`; tier 2; f45 had left `interconnectBlocks` without a reason) | rechecked 2026-11-23 with f51, read-only (git): ASO's `compute.yaml` on `main` (`f451aaa`) exports no run command of either kind; no open pull request head touches it | **built**, generated: `compute.azure.hfl.dev/v20260401` `VirtualMachinesDiagnosticRunCommand` (the newest stable ComputeRP version, 2026-04-01, the only one with them, a new included version; owned by ASO's `VirtualMachine`, an external owner already): a run command kept apart for diagnostics, with the ordinary run command's properties (a script, script URI, command ID or gallery script version by reference, `$referenceType: arm`, autolabel wrote simple; the run-as password from a Secret, as our `VirtualMachinesRunCommand`). The 2026-04-01 spec does not flatten `properties`. Including the version adds cross-version conversions to the storage types of our other compute versions (generated). Not built: a scale set instance's diagnostic run commands (instances a scale set makes, not declared, as f45 said), and `interconnectBlocks` (new, ten attributes; left for a row that reads what an interconnect block reserves and bills). Known limit, as for `VirtualMachinesRunCommand`: `protectedParameters` are plain strings in the spec. Proven: `TestVirtualMachineDiagnosticRunCommand` (a gallery script by ARM ID with its password from a Secret kept out of the status, changed, one with two sources refused, deleted), the conformance test, and the fake's rule (`diagnosticruncommands.go`). Live: `TestLiveVirtualMachineRunCommand` now also makes one on its VM, pending (`docs/live-runs.md`) |
| f56 | Logic Apps API connections, custom APIs and on-premises data gateways (the application inventory's `no kind` rows `Microsoft.Web/connections`, `customApis`, `connectionGateways`; tier 2, `Microsoft.Web` still the service with the most uncovered attributes; none recorded before) | rechecked 2026-11-23, read-only (git, blob-less fetch): the latest release is still v2.22.0; ASO `main` (`f451aaa`) exports `ServerFarm`, `Site` and `SitesSourcecontrol` in `web.yaml` and nothing of `logicAppsManagementClient.json`; ASO has no `logic` group; of the pull request heads from #5780 to #5857, only #5786 differs in `web.yaml`, from an older base (it lacks main's 2026-07-15 entries) and adds nothing of these | **built**, generated from `logicAppsManagementClient.json` 2016-06-01 (their only version, stable; a new included version of the group, no conversions with the others: no kind is in both): `web.azure.hfl.dev/v20160601` `ApiConnection` (exported from the spec's `Connection`: a connection a Logic App uses, to a managed API (`/subscriptions/{s}/providers/Microsoft.Web/locations/{l}/managedApis/{api}`) by ARM ID or to our `CustomApi` by object, `$referenceType: arm`; its `parameterValues` and `customParameterValues`, the credentials, each a `SecretMapReference` (every key of a Secret), removed from the status; `nonSecretParameterValues` plain), `CustomApi` (a custom connector: an inline OpenAPI document, an OpenAPI URL or a WSDL, its backend, and connection parameters whose OAuth client secret is a Secret reference, removed from the status) and `ConnectionGateway` (an on-premises data gateway resource on a gateway installation, by ARM ID); all in a resource group, owned by ASO's `ResourceGroup`. The connection's status fields the spec leaves writable (`statuses`, `createdTime`, `changedTime`, `testLinks`) are in the spec type as generated; nothing sends them unless set. Not built: consent (`listConsentLinks`, `confirmConsentCode`: an OAuth sign-in a person completes in a browser, so a connection to an OAuth API such as Office 365 is made but stays unauthenticated until someone signs in, as one made by an ARM template does), `connectionGatewayInstallations` (registered by installing the gateway on a machine; read-only in ARM) and `managedApis` (Azure's catalogue, read-only). Also recorded, the other `Microsoft.Web` rows with no kind and no reason written before: `csrs` (2015-08-01, the retired certificate signing request API), `managedHostingEnvironments` (2015-08-01, App Service Environment v1, retired on 2024-08-31; f41 built v3), and a static web app build's `linkedBackends`, `userProvidedFunctionApps` and `databaseConnections` (`staticSites/{s}/builds/{environment}/...`: a build is made by a deployment, not declared, as f36 said of builds). Proven: `TestLogicAppsConnections` (a custom API from an OpenAPI URL with its OAuth client secret from a Secret; a connection to a managed API in its region with its parameter values from a Secret, sent to Resource Manager and never in the status, and one to the custom API by object; a gateway on its installation; a connection to a managed API in another region, a custom API with no definition and a gateway on a missing installation refused; a change PUT again; all deleted), the conformance test, and the fake's rules (`logicconnections.go`, `TestLogicConnections`). Not proven: Azure's own refusal texts and whether it refuses a managed API in another region in the same words (the fake's rule follows the documented pattern of a connection and its API in one region). Live: `TestLiveLogicAppsConnections` (a custom API from an inline OpenAPI document, a connection to it and one to the RSS managed API, which needs no sign-in; free) written, pending (`docs/live-runs.md`); a gateway needs an installed gateway, so it is not live-tested |
| f57 | Application Insights smart detection rules (the application inventory's `no kind` row `Microsoft.Insights/components/ProactiveDetectionConfigs`; tier 1, Terraform's `azurerm_application_insights_smart_detection_rule`; with the component's other children that had no reason written: `exportconfiguration`, `favorites`, `analyticsItems`, `myanalyticsItems`) | rechecked 2026-11-24, read-only (git, blob-less fetch): the latest release is still v2.22.0; ASO `main` (`f451aaa`) exports `Component`, `Webtest` and `PricingPlan` of Application Insights and no child of a component's beyond the pricing plan; no pull request head from #5780 to #5857 differs from `main` in `insights.yaml` | **built**, generated: `insights.azure.hfl.dev/v20180501preview` `ComponentsProactiveDetectionConfig`, owned by ASO's `Component` (an external owner already): a smart detection rule, named as Application Insights names them (`slowpageloadtime`, `slowserverresponsetime`, `longdependencyduration`, the `degradationin...` and `extension_...` rules), turned on or off, with or without mail to the subscription's owners and with custom recipients. **From the preview version, though a stable one exists**: 2015-05-01, the stable version (and the one Terraform calls), is now a TypeSpec `openapi.json` that supersedes the hand-written `componentProactiveDetection_API.json` in the generator's loader, and its body is neither marked `x-ms-azure-resource` nor has an `id` or `type`, so ASO's generator does not see a resource there ("version 2015-05-01 not seen"); 2018-05-01-preview, the newest version, has the resource shape (`properties` wrapped, flattened here). A patch to the loader for one 12-attribute kind was not worth it; if the builder wants the stable version, the cost is a generator patch that reads the legacy file or marks the TypeSpec body a resource. A rule has no DELETE (it lives as long as its component): the object takes `detach-on-delete`. Not built: `exportconfiguration` (continuous export, retired by Microsoft on 2024-02-29 in favour of diagnostic settings, which ASO has), `favorites` (a person's saved portal queries and pinned items of a component, user content rather than a resource a platform declares), and `analyticsItems` and `myanalyticsItems` (saved Analytics queries and functions: their path `components/{c}/{scopePath}/item` has no name segment, the item named by query parameters, which no generated kind can model; the legacy query store, superseded by Log Analytics query packs, which `WorkspacesSavedSearch`, `QueryPack` and `QueryPacksQuery` cover). Proven: `TestSmartDetectionRules` (on ASO's `Component` stand-in: a rule turned off with a custom recipient and an extension rule, sent wrapped in `properties`; a rule name Application Insights does not define refused; a change PUT again; detached, the rules stay), the conformance test, and the fake's rules (`smartdetection.go`, `TestSmartDetection`). Not proven: that Azure refuses an unknown rule name (the fake's rule follows the documented list), and the preview version's behaviour against the stable one. Live: `TestLiveSmartDetectionRule` (a workspace-based component and one rule; free) written, pending (`docs/live-runs.md`) |
| f58 | Alert processing rules (the inventory's `no kind` row `Microsoft.AlertsManagement/actionRules`; `Microsoft.AlertsManagement` is tier 2, its `priority.csv` row naming "Prometheus rule groups and alert processing"; Terraform's `azurerm_monitor_alert_processing_rule_action_group` and `..._suppression`) | rechecked 2026-11-24, read-only (git, blob-less fetch): the latest release is still v2.22.0; ASO `main` (`f451aaa`) `alertsmanagement.yaml` exports `PrometheusRuleGroup` (2023-03-01) and `SmartDetectorAlertRule` (2021-04-01) only; no pull request head from #5780 to #5857 differs from `main` in it | **built**, generated from `AlertProcessingRules.json` 2021-08-08 (the newest and only stable version; `specification/alertsmanagement` added to `generator/specs.txt`), in a new group: `alertsmanagement.azure.hfl.dev/v20210808` `ActionRule` (the ARM type `actionRules`; an alert processing rule), in a resource group: its scopes (subscriptions, resource groups or resources) and the action groups it adds as references (`$referenceType: arm`, ASO's `ActionGroup` by object or an ARM ID), filter conditions, a schedule with daily, weekly or monthly recurrences, and one action, `addActionGroups` or `removeAllActionGroups` (a polymorphic action, one property per kind as the generator makes them). One generator run, no other label. Not built: `tenantActivityLogAlerts` (preview only, 2023-04-01-preview) and the `AlertsManagement` alerts and `issues` themselves (fired by Azure Monitor, operational). Known limit, generated: an `actions` item with neither `addActionGroups` nor `removeAllActionGroups` set passes the CRD and fails at the PUT with ASO's marshalling error ("unexpected end of JSON input"), as any one-of of ASO's generated kinds does. Proven: `TestAlertProcessingRules` (a rule routing the group's Sev0 alerts to an action group by ARM ID, and a weekend suppression; a rule in a region, one naming a missing action group and one whose schedule ends before it starts refused; a change PUT again; both deleted), the conformance test, and the fake's rules (`alertprocessing.go`, `TestAlertProcessingRules`: Global, scopes and action groups that exist, one action, one to five groups, a schedule in order, the days a recurrence needs). Not proven: Azure's own refusal texts. Live: `TestLiveAlertProcessingRule` (a disabled weekend suppression on the live group, free) written, pending (`docs/live-runs.md`) |
| f59 | The tier 1 and 2 inventory's remaining `no kind` rows with no reason written (application, platform-foundation and security-adjacent categories; found 2026-11-24 by listing every uncovered resource of those tiers whose name `docs/plan.md` did not mention) | read 2026-11-24 with f56 to f58 (git, blob-less): none is in an ASO release, `main` (`f451aaa`) or a pull request head from #5780 to #5857 | **not built**, with reasons: `Microsoft.Network/loadBalancers/backendAddressPools` (ASO's `LoadBalancer` carries its pools inline, `$resourceEmbeddedInParent: LoadBalancer` in ASO's `network.yaml` with a "TODO[breaking]" saying the pool is a real child resource kept embedded for compatibility; a separate kind would fight the load balancer's PUT, which sends the whole list: an upstream design question, not ours to settle; draft issue: "network: model load balancer backend address pools as child resources, as subnets are"); `Microsoft.Network/frontDoors` and `frontDoors/rulesEngines` (Azure Front Door (classic), which Microsoft retires on 2027-03-31; ASO's `cdn` group has Azure Front Door Standard and Premium, `Profile`, `AfdEndpoint`, `AfdOriginGroup` and the rest); `Microsoft.Network/NetworkExperimentProfiles/Experiments` and `virtualNetworkAppliances/capabilities` (preview features in `priority.csv`); `Microsoft.Sql/locations/serverTrustGroups` (under `locations/{l}`, a segment that is no resource, which no generated kind can own, as the plan says of Defender's JIT network access policies); the `Microsoft.Sql` rows whose only version is 2014-04-01 (`servers/advisors`, `servers/auditingPolicies`, `servers/communicationLinks`, `servers/disasterRecoveryConfiguration`, `servers/databases/advisors`, `servers/databases/auditingPolicies`, `servers/databases/connectionPolicies`: the legacy API, superseded by the auditing settings, failover groups and server connection policies ASO covers); `Microsoft.Maintenance/applyUpdates` (no writable attribute: a PUT starts an update run now, operational); and the preview-only `Microsoft.ContainerService/aiManagers` with its `customAIModels`, `modelSources` and `namespaces/modelDeployments` (2026-09-02-preview), `Microsoft.DocumentDB/throughputPools/throughputPoolAccounts` (2025-05-01-preview), `Microsoft.Security/serviceEntitlementSettings` (2026-09-16-preview) and `Microsoft.SecurityInsights/entityQueries` and `fileImports` (2025-04-01-preview). Left as candidates for a later row: `Microsoft.Network/virtualNetworkTaps` and `networkInterfaces/tapConfigurations` (virtual network TAP, stable 2026-03-01; a tap configuration needs ASO's `NetworkInterface` as a new external owner, and the spec embeds whole network interface IP configurations, which the generator would reduce to IDs) |
| f60 | Virtual network TAP (f59's candidate: the inventory's `no kind` rows `Microsoft.Network/virtualNetworkTaps` and `networkInterfaces/tapConfigurations`; `Microsoft.Network` is tier 1) | rechecked 2026-11-24, read-only (git, blob-less fetch): the latest release is still v2.22.0; ASO `main` is still `f451aaa`, whose `network.yaml` exports no TAP or tap configuration (its `NetworkInterface`, 2025-03-01, carries `tapConfigurations` only in the status); no pull request head from #5780 to #5857 (the newest; only #5851 moved, in `containerservice.yaml` and `security.yaml`) mentions either in `v2/azure-arm*` | **built**, generated from the 2026-03-01 network specs (`networkGateway.json`, `virtualNetwork.json`): `network.azure.hfl.dev/v20260301` `VirtualNetworkTap` (in a resource group; its destination, a collector NIC's IP configuration or an internal load balancer's frontend IP configuration, which the spec embeds whole and the generator reduces to a reference, `$referenceType: arm` on the two `..._VirtualNetworkTap_SubResourceEmbedded` IDs; a VXLAN destination port) and `NetworkInterfacesTapConfiguration` (owned by ASO's `NetworkInterface`, a new external owner: `generator/external-owners.txt`; the scheme, `OwnerTypes()`, its test CRD and the manager's role already had it for locks; the TAP by object or ARM ID; its own `Id` removed as the other children's). Proven: `TestVirtualNetworkTap` (a TAP to a collector NIC by ARM ID and a tap configuration on ASO's NIC by name naming the TAP by object; a TAP with no destination and a tap configuration naming a missing TAP refused; a port change PUT again, and the TAP's status then lists the tap configuration; deleted, tap configurations first), the conformance test, and the fake's rules (`vnettap.go`, `TestVirtualNetworkTap`: one destination that exists, a port of 1 to 65535, a TAP that exists, the TAP's read-only list rendered from its tap configurations). Not proven: whether Azure takes a tap configuration on a NIC no VM uses, whether it refuses deleting a TAP still in use, its refusal texts. Coverage: the two resources' 561 attributes count as ours, most of them the embedded NIC IP and frontend configurations the kind takes by reference (as ASO's own `NetworkInterface` is counted); `Microsoft.Network` ours-beyond 22.4% to 32.9%, overall 13.8% to 15.0%. Live: `TestLiveVirtualNetworkTap` (two NICs with no VM, free; the TAP for the run's minutes) written, pending (`docs/live-runs.md`) |
| f61 | Service Bus namespace children (tier 3 row t1, `docs/coverage/foundation.md`: the inventory's `no kind` rows `Microsoft.ServiceBus/namespaces/networkRuleSets`, `privateEndpointConnections`, `disasterRecoveryConfigs`) | rechecked 2026-11-24, read-only (git, blob-less fetch): the latest release is still v2.22.0; ASO `main` (`f451aaa`) `servicebus.yaml` exports `Namespace`, `NamespacesAuthorizationRule`, `NamespacesQueue`, `NamespacesTopic`, `NamespacesTopicsSubscription`, `NamespacesTopicsSubscriptionsRule`, `TopicAuthorizationRule` and `QueueAuthorizationRule` (2024-01-01 the newest) and removes the namespace's `PrivateEndpointConnections` as read-only; no pull request head from #5780 to #5857 changes `servicebus.yaml` | **built**, generated from `servicebus.json` 2026-01-01 (the newest stable; `specification/servicebus` added to `generator/specs.txt`), in a new group: `servicebus.azure.hfl.dev/v20260101` `NamespacesNetworkRuleSet` (the one rule set, `networkRuleSets/default`: IP rules, virtual network rules with the subnet by reference, default action, public network access, trusted services; Azure has no DELETE for it, so a Deleter extension, `generator/_extensions/servicebus`, leaves it when the object goes, as a site's config sections are left), `NamespacesPrivateEndpointConnection` (approve or reject; its `provisioningState` removed as read-only, as ASO does for Event Hubs) and `NamespacesDisasterRecoveryConfig` (the alias; `partnerNamespace` a reference, ASO's `Namespace` by object or an ARM ID). ASO's `Namespace` (v20240101 storage) is a new external owner (external-owners.txt, the scheme and `OwnerTypes()`, its test CRD in `hack/generate.sh`, the manager's role). Not built: `ipfilterrules` and `virtualnetworkrules` (2018-01-01-preview only, replaced by the rule set) and `migrationConfigurations` (a PUT starts a Standard to Premium migration). Proven: `TestServiceBusNamespaceChildren` (a rule set with an IP range and a subnet, an alias naming ASO's other namespace by object, a connection approved; an IPv6 mask, a missing partner and a connection no private endpoint made refused; a change PUT again; deleted, all go but the rule set, left), the conformance test, and the fake's rules (`messaging.go`, `TestServiceBusNamespaceChildren`: the default rule set and no DELETE, IPv4 masks, subnets that exist, a partner of the same service in one pairing). Not proven: which tiers take what (Azure's documentation puts private endpoints and pairing in Premium; whether a Standard namespace takes IP and virtual network rules is for a live run; the fake does not know the namespace's tier), breaking a pairing before deleting its alias, Azure's refusal texts. Coverage: `Microsoft.ServiceBus` ours-beyond 0.0% to 10.6%, overall 15.0% to 15.1%. Live: `TestLiveServiceBusNetworkRuleSet` (a Standard namespace's rule set without IP rules, cents) written, pending; the other two need Premium namespaces |
| f62 | Event Hubs namespace children and dedicated clusters (tier 3 row t2, `docs/coverage/foundation.md`: the inventory's `no kind` rows `Microsoft.EventHub/namespaces/networkRuleSets`, `privateEndpointConnections`, `applicationGroups`, `schemagroups`, `disasterRecoveryConfigs` and `clusters`) | rechecked 2026-11-24 with f61, read-only (git, blob-less): v2.22.0 still the latest release; ASO `main` (`f451aaa`) `eventhub.yaml` exports `Namespace`, `NamespacesAuthorizationRule`, `NamespacesEventhub`, `NamespacesEventhubsAuthorizationRule`, `NamespacesEventhubsConsumerGroup` (2024-01-01 the newest); no pull request head from #5780 to #5857 changes `eventhub.yaml` | **built**, generated from `openapi.json` 2026-01-01 (the newest stable; `specification/eventhub` added to `generator/specs.txt`), in a new group: `eventhub.azure.hfl.dev/v20260101` `NamespacesNetworkRuleSet` (as Service Bus's, f61, with `SecuredByPerimeter` among the public access values; the same Deleter extension leaves it), `NamespacesPrivateEndpointConnection`, `NamespacesApplicationGroup` (a client group, `SASKeyName=` or `AADAppID=`, enabled or not, with throttling policies on incoming or outgoing bytes or messages; the policy union one property per kind, as the generator makes them), `NamespacesSchemaGroup` (Avro, JSON or Protobuf, its compatibility), `NamespacesDisasterRecoveryConfig` (the partner by reference) and `Cluster` (a dedicated cluster in a resource group: capacity units, scaling, zone redundancy). ASO's Event Hubs `Namespace` (v20240101 storage) is a new external owner (the five places). Not built: `ipfilterrules`, `virtualnetworkrules` (2018-01-01-preview only, replaced by the rule set). Proven: `TestEventHubsKinds` (each kind Ready, the alias naming ASO's other namespace by object; a client identifier of neither form and a Service Bus partner refused; a throttle change PUT again; deleted, all go but the rule set), the conformance test (the dedicated cluster's fixture qualified by group: Log Analytics has a `Cluster` too), and the fake's rules (`messaging.go`, `TestEventHubsNamespaceChildren`). Not proven: the tiers each child needs (application groups, schema groups, pairing), the cluster's capacity rules, breaking a pairing, Azure's refusal texts. Coverage: `Microsoft.EventHub` ours-beyond 0.0% to 30.8%. Live: `TestLiveEventHubsNamespaceChildren` (a Standard namespace's rule set, schema group and application group, cents) written, pending |
| f63 | Event Grid system topics, namespace topic subscriptions and the MQTT broker's children (tier 3 row t3, `docs/coverage/foundation.md`: the inventory's `no kind` rows `Microsoft.EventGrid/systemTopics` and its `eventSubscriptions`, `namespaces/topics/eventSubscriptions`, `namespaces/clients`, `clientGroups`, `topicSpaces`, `permissionBindings`, `caCertificates`) | rechecked 2026-11-24, read-only (git, blob-less): v2.22.0 still the latest release; ASO `main` (`f451aaa`) `eventgrid.yaml` exports `Domain`, `DomainsTopic`, `EventSubscription` (an extension resource), `Namespace`, `NamespaceTopic` and `Topic` (2020-06-01 and 2025-02-15); no pull request head from #5780 to #5857 changes `eventgrid.yaml` | **built**, generated from `EventGrid.json` 2025-02-15 (the newest stable and ASO's own; `specification/eventgrid` added to `generator/specs.txt`), in a new group: `eventgrid.azure.hfl.dev/v20250215` `SystemTopic` (in a resource group; `source` a reference, `$referenceType: arm`), `SystemTopicsEventSubscription` (owned by it; every destination ASO's `EventSubscription` has, resource IDs as references, a webhook's `endpointUrl` a Secret as the generator makes it; filters, retry and dead-letter settings), `NamespacesTopicsEventSubscription` (owned by ASO's `NamespaceTopic`: queue or push delivery), and the MQTT broker's `NamespacesClient`, `NamespacesClientGroup`, `NamespacesTopicSpace`, `NamespacesPermissionBinding` and `NamespacesCaCertificate` (owned by ASO's `Namespace`). New external owners: ASO's `Namespace` and `NamespaceTopic` (`eventgrid/Namespaces_Topic=NamespaceTopic`; the five places). The identity's `principalId` and `tenantId` removed from the spec, and the webhook and storage queue configuration labels copied, as ASO's `eventgrid.yaml` has them. Not built: the typed `topics`, `domains` and `domains/topics` `eventSubscriptions` (ASO's extension `EventSubscription`, owned by the topic or domain, makes the same subscription at `{topic}/providers/Microsoft.EventGrid/eventSubscriptions/{name}`); `{parentType}/{parentName}/privateEndpointConnections` (the parent type is a path parameter, which no generated kind's owner can be; a candidate for a hand-written kind); the partner resources (`partnerRegistrations`, `partnerNamespaces` and their `channels`, `partnerConfigurations`, `partnerTopics` and their subscriptions: a SaaS partner's side of Event Grid, ranked as t3b). Proven: `TestEventGridKinds` (a system topic on a storage account with an event hub and a webhook subscription, the URL from a Secret; ASO's namespace and topic by name, a queue subscription, a topic space, client group, client and the binding naming them; a second system topic of the source, a missing event hub and a missing topic space refused; a change PUT again; deleted, all go), the conformance test, and the fake's rules (`eventgrid.go`, `TestEventGrid`). Not proven: events flowing, the system topic's region rule, a webhook's validation handshake, MQTT clients, Azure's refusal texts. Coverage: `Microsoft.EventGrid` ours-beyond 0.0% to 20.6%; overall 15.1% to 15.4%. Live: `TestLiveEventGridSystemTopic` (a storage account's system topic and a queue subscription, storage cents) written, pending |
| f64 | API Management, first part (tier 3 row t4, `docs/coverage/foundation.md`: the inventory's `no kind` rows `Microsoft.ApiManagement/service/apis/operations` and their `policies` and `tags`, `apis/schemas`, `apis/tagDescriptions`, `apis/tags`, `products/tags`, `tags`, `schemas`, `identityProviders`, `openidConnectProviders`, `authorizationServers`, `caches`) | rechecked 2026-11-24, read-only (git, blob-less): v2.22.0 still the latest release; ASO `main` (`f451aaa`) `apimanagement.yaml` exports, at 2024-05-01, `Service`, `Api`, `ApiVersionSet`, `ApiDiagnostic`, `ApiPolicy`, the authorization providers, `Backend`, `Certificate`, `Diagnostic`, the service gateways, `Group`, `Logger`, `NamedValue`, `Policy`, `PolicyFragment`, `Product`, `ProductApi`, `ProductGroup`, `ProductPolicy`, `Subscription`, `User`, and no operation, schema, tag, provider, authorization server or cache; no pull request head from #5780 to #5857 changes `apimanagement.yaml` | **built**, generated from the 2024-05-01 specs (the newest stable, ASO's own; `specification/apimanagement` added to `generator/specs.txt`), in a new group: `apimanagement.azure.hfl.dev/v20240501` `ApiOperation`, `ApiOperationPolicy`, `ApiOperationTag`, `ApiSchema`, `ApiTagDescription`, `ApiTag`, `ProductTag` (owned by ASO's `Api` and `Product` and our `ApiOperation`), `Tag`, `GlobalSchema`, `IdentityProvider`, `OpenIdConnectProvider`, `AuthorizationServer`, `Cache` (owned by ASO's `Service`); client secrets, the resource owner password and the cache's connection string are Secrets (`$importSecretMode: required`; the password removed from the status), the cache's Redis a reference. New external owners: ASO's `Service`, `Api` and `Product` (`apimanagement/Service_Api=Api`, `Service_Product=Product`; the five places). An identity provider's name, an enum of its types, is a string (the generator's enum-in-storage import cycle, as `operationalinsights.yaml` says). Generator patch 0008 now also keeps a receiver from being named `schema` (the package the resource methods use; `ApiSchema` and `GlobalSchema` did not compile); two generated files change only in their receivers' names. Not built here: the tags' `apiLinks`, `operationLinks` and `productLinks` (the same links made from the tag's side); workspaces and their children (a Premium service; the next part of t4); `gateways` and `gateways/configConnections` (the v2 self-hosted gateway resource); the developer portal's content (`portalRevisions`, `portalconfigs`, `portalsettings`, `contentTypes`, `templates`, `notifications`), `apis/issues`, `apis/releases`, `apis/wikis`, `products/wikis`, `documentations`, `policyRestrictions`, `tenant` and `apis/resolvers` (GraphQL resolvers); next rows of t4. Proven: `TestAPIManagementKinds` (an operation with a policy and a tag, the API's tag, schema and tag description, the product's tag, the service's tag, an Entra ID identity provider and an authorization server with secrets; an undeclared template parameter, a policy without a `<policies>` root and a link to a missing tag refused; a change PUT again; deleted, all go), the conformance test, and the fake's rules (`apimanagement.go`, `TestAPIManagement`). Not proven: policy semantics, the gateway, the developer portal, Azure's refusal texts. Coverage: `Microsoft.ApiManagement` ours-beyond 0.0% to 13.8%; overall 15.4% to 15.7%. Live: `TestLiveAPIManagementOperation` (a Consumption service's operation, policy and tags; Consumption bills calls only) written, pending |
| f65 | Azure Front Door and CDN leftovers (tier 3 row t5, `docs/coverage/foundation.md`: the inventory's `no kind` rows `Microsoft.Cdn/profiles/endpoints/originGroups`, `origins`, `customDomains`, `edgeActions` and its `versions` and `executionFilters`) | read 2026-11-24 with f64 (git, blob-less): v2.22.0 still the latest release; ASO `main` (`f451aaa`) `cdn.yaml` exports the Front Door Standard and Premium kinds, `Profile` and the classic `ProfilesEndpoint`, which carries its `origins` and `originGroups` inline; #5805 (a specs refresh) renames a `cdn` type and adds no kind; no other pull request head from #5780 to #5857 changes `cdn.yaml` | **not built**, with reasons: `profiles/endpoints/origins` and `originGroups` (a classic endpoint's PUT sends its whole `origins` and `originGroups` lists, which ASO's `ProfilesEndpoint` holds; a separate kind would fight it, as f59 says of load balancer backend pools; an upstream design question); `profiles/endpoints/customDomains` (one attribute, of Azure CDN Standard from Microsoft (classic), which Microsoft is retiring; the Front Door Standard and Premium `AfdCustomDomain` is ASO's); `edgeActions`, `versions` and `executionFilters` (2026-10-01, new: a version's code is deployed by a POST, `deployVersionCode`, which a generated kind cannot send, so the kinds alone would be empty shells; and `generator/azure-arm/cdn.yaml` already prunes the `EdgeAction` delivery rule types, which fail the generator's allOf merge). Recheck when ASO models classic endpoint children or edge actions gain a code property |
| f66 | MySQL flexible server children (tier 3 row t6, `docs/coverage/foundation.md`: the inventory's `no kind` rows `Microsoft.DBforMySQL/flexibleServers/advancedThreatProtectionSettings` and `privateEndpointConnections`) | rechecked 2026-11-24, read-only (git, blob-less): v2.22.0 still the latest release; ASO `main` (`f451aaa`) `dbformysql.yaml` exports `FlexibleServer`, `FlexibleServersAdministrator`, `FlexibleServersConfiguration`, `FlexibleServersDatabase` and `FlexibleServersFirewallRule` (2024-12-30 the newest stable, 2025-06-01-preview); no pull request head from #5780 to #5857 changes `dbformysql.yaml` | **built**, generated from the FlexibleServers `openapi.json` 2024-12-30 (the newest stable and ASO's own; `specification/mysql` added to `generator/specs.txt`), in a new group: `dbformysql.azure.hfl.dev/v20241230` `FlexibleServersAdvancedThreatProtectionSetting` (its one name, Default, a string rather than the generator's enum, which would make an import cycle; Azure has no DELETE for it, so a Deleter extension, `generator/_extensions/dbformysql`, leaves it, as Service Bus's rule set) and `FlexibleServersPrivateEndpointConnection` (approve or reject; its `provisioningState` is the shared common type's, as PostgreSQL's is, f38). ASO's MySQL `FlexibleServer` (v20241230 storage) is a new external owner (the five places). Not built: the single server types (`servers/*`, Azure Database for MySQL single server, retired 2024-09-16) and `flexibleServers/backups` and `backupsV2` (a PUT takes an on-demand backup: an operation). Proven: `TestMySQLFlexibleServerChildren` (protection on, then off, PUT again; a connection approved; a setting of another name and a connection no private endpoint made refused; deleted, the connection goes and the setting is left), the conformance test, and the fake's rules (`mysql.go`, `TestMySQLFlexibleServerChildren`). Not proven: Defender's plan and tier rules, Azure's refusal texts. Coverage: `Microsoft.DBforMySQL` ours-beyond 0.0% to 3.6%. Live: not written (a flexible server takes minutes to make and to delete; the setting turns on Defender's billing) |
| f67 | Container Instances container group profiles, NGroups and sandbox groups (tier 3 row t7, `docs/coverage/foundation.md`: the inventory's `no kind` rows `Microsoft.ContainerInstance/containerGroupProfiles`, `ngroups`, `sandboxGroups`) | rechecked 2026-11-24, read-only (git, blob-less): v2.22.0 still the latest release; ASO `main` (`f451aaa`) `containerinstance.yaml` exports `ContainerGroup` (2021-10-01) only; no pull request head from #5780 to #5857 changes it | **built**, generated from `containerInstance.json` 2026-07-01 (the newest stable; `specification/containerinstance` added to `generator/specs.txt`), in a new group: `containerinstance.azure.hfl.dev/v20260701` `ContainerGroupProfile`, `NGroup` (its profiles by reference, `ApiEntityReference.Id`; load balancer and application gateway pools by ARM ID, since ASO keeps pools inside their parents) and `SandboxGroup`, in a resource group. Secrets: environment `secureValue`, registry `password`, Azure Files `storageAccountKey`, the workspace key, as the generator flags them, and a secret volume's files (`$importSecretMode: required` on `Volume.Secret`, a `SecretMapReference`; removed from the status); a registry credential's `passwordReference` names a secret reference and is no secret. ASO's two labels for its `ContainerGroup` (subnet IDs, the workspace) copied. Proven: `TestContainerInstanceKinds` (a profile whose container takes a secure value and a secret volume from Secrets, an NGroup of it by object, a sandbox group; two containers of one name and a missing profile refused; a count change PUT again; deleted, all go), the conformance test, and the fake's rules (`containerinstance.go`, `TestContainerInstance`). Not proven: container groups made from profiles, images, regions' SKUs, Azure's refusal texts. Coverage: `Microsoft.ContainerInstance` ours-beyond 0.0% to 55.6%; overall 15.7% to 16.1%. Live: `TestLiveContainerGroupProfile` (a profile and an NGroup of zero, free) written, pending |
| f68 | Web PubSub and SignalR leftovers (tier 3 row t8, `docs/coverage/foundation.md`: the inventory's `no kind` rows `Microsoft.SignalRService/webPubSub` and its `hubs`, `replicas`, `customDomains`, `customCertificates`, `sharedPrivateLinkResources`, `replicas/sharedPrivateLinkResources`, `privateEndpointConnections`; `signalR/sharedPrivateLinkResources`, `signalR/replicas/sharedPrivateLinkResources`, `signalR/privateEndpointConnections`) | rechecked 2026-11-24, read-only (git, blob-less): v2.22.0 still the latest release; ASO `main` (`f451aaa`) `signalrservice.yaml` exports `SignalR` (2021-10-01, 2024-03-01), `Replica`, `CustomDomain` and `CustomCertificate`, no Web PubSub kind; no pull request head from #5780 to #5857 changes it | **built**, generated from `webpubsub.json` and `signalr.json` 2024-03-01 (the newest stable, ASO's own; `specification/signalr` and `specification/webpubsub` added to `generator/specs.txt`), in a new group: `signalrservice.azure.hfl.dev/v20240301` `WebPubSub` (in a resource group), `WebPubSubHub`, `WebPubSubReplica` (its SKU required, as ASO makes SignalR's), `WebPubSubCustomDomain` (the certificate by reference), `WebPubSubCustomCertificate`, `WebPubSubSharedPrivateLinkResource`, `WebPubSubReplicasSharedPrivateLinkResource`, `WebPubSubPrivateEndpointConnection`, `SignalRSharedPrivateLinkResource`, `SignalRReplicasSharedPrivateLinkResource`, `SignalRPrivateEndpointConnection`. A replica's shared private links have no DELETE: a Deleter extension (`generator/_extensions/signalrservice`) leaves them. New external owners: ASO's `SignalR` and `Replica` (`signalrservice/SignalR_Replica=Replica`; the five places). ASO's labels for its SignalR (`KeyVaultBaseURI`, `ResourceReference.Id`) copied. Not done: a Web PubSub service's keys as a Secret (ASO's `SignalR` exports its keys through the SignalR SDK's listKeys; Web PubSub's would need `armwebpubsub`, a new module dependency, which is the builder's call: §8 Q16). Proven: `TestWebPubSubAndSignalRKinds` (a Premium service with a hub, a replica elsewhere, a certificate and the domain naming it by object, a shared private link; SignalR's own and replica's links and an approved connection on ASO's owners by name; a dashed hub name and a replica in the service's region refused; a change PUT again; deleted, all go but the replica's link), the conformance test (the replica covered by the test above), and the fake's rules (`signalr.go`, `TestWebPubSubAndSignalR`). Not proven: the tiers that take domains and private links, clients and event handlers, Azure's refusal texts. Coverage: `Microsoft.SignalRService` ours-beyond 0.0% to 59.3% (now nothing uncovered); overall 16.1% to 16.3%. Live: `TestLiveWebPubSubHub` (a Free service and a hub, free) written, pending |
| f69 | Azure Data Explorer cluster children (tier 3 row t9, `docs/coverage/foundation.md`: the inventory's `no kind` rows `Microsoft.Kusto/clusters/attachedDatabaseConfigurations`, `managedPrivateEndpoints`, `privateEndpointConnections`, `sandboxCustomImages`, `databases/scripts`) | rechecked 2026-11-24, read-only (git, blob-less): v2.22.0 still the latest release; ASO `main` (`f451aaa`) `kusto.yaml` exports `Cluster`, `ClusterPrincipalAssignment`, `Database`, `DataConnection`, `PrincipalAssignment` (2023-08-15, 2024-04-13); no pull request head from #5780 to #5857 changes it | **built**, generated from `kusto.json` 2025-02-14 (the newest stable; ASO's kinds are 2024-04-13; `specification/azure-kusto` added to `generator/specs.txt`), in a new group: `kusto.azure.hfl.dev/v20250214` `ClustersAttachedDatabaseConfiguration` (the leader cluster a reference), `ClustersManagedPrivateEndpoint` (its target a reference), `ClustersPrivateEndpointConnection`, `ClustersSandboxCustomImage` (owned by ASO's `Cluster`) and `ClustersDatabasesScript` (owned by ASO's `Database`; the script's content and its URL's SAS token are Secrets, the first as the spec marks it, the second by `$importSecretMode`). New external owners: ASO's `Cluster` and `Database` (`kusto/Clusters_Database=Database`; the five places). Proven: `TestKustoClusterChildren` (a follower of ASO's other cluster by object, a managed private endpoint to a storage account, a Python image, an approved connection, a script from a Secret; a cluster following itself and a script with no source refused; a change PUT again; deleted, all go), the conformance test, and the fake's rules (`kusto.go`, `TestKustoClusterChildren`). Not proven: followers kept in step, scripts running, Azure's refusal texts. Coverage: `Microsoft.Kusto` ours-beyond 0.0% to 34.0%. Live: not written (a Dev cluster bills by the hour and takes a quarter of an hour or more to make) |
| c18 | Stale ARM versions (VirtualMachine, ApplicationGateway, DNS zones) | not a new kind | queued |
| c11 | Storage account children (the catalog's `Microsoft.Storage/storageAccounts/localUsers`, catalog `docs/azure.md` section 8) | rechecked 2026-11-12, read-only: ASO's `storage` group at the pin exports the account (hub v20260601), its blob, file, queue and table services and their containers, shares, queues and tables, and the management policy; no local user, encryption scope, inventory policy, object replication policy, private endpoint connection or storage task assignment kind in a release, `main` or an open pull request | **built**, generated: `storage.azure.hfl.dev/v20260601` (ASO's hub version), six kinds owned by ASO's `StorageAccount` (a new external owner): `StorageAccountsLocalUser` (SFTP and NFSv3 local users: permission scopes, home directory, SSH authorized keys, groups), `StorageAccountsEncryptionScope` (no DELETE in the spec: `detach-on-delete`; Azure disables a scope, it never deletes one), `StorageAccountsInventoryPolicy`, `StorageAccountsObjectReplicationPolicy`, `StorageAccountsPrivateEndpointConnection`, `StorageAccountsStorageTaskAssignment`. Not built: a local user's password (the `regeneratePassword` action's answer, returned once), container immutability policies (every write and delete needs the policy's ETag in `If-Match`), and the 2026-06-01 newcomers (connectors, data shares, advanced platform metrics, context caches; built later, f48). Proven: the conformance test. Hand-written code: none |
| c13 | Cosmos DB children (Gremlin, Cassandra, Table API, services, SQL role definitions; the catalog's Gremlin and dedicated gateway types, `docs/coverage/catalog-azure.md`) | rechecked 2026-11-12, read-only: ASO's `documentdb` group at the pin exports the account, SQL and MongoDB databases and their children, SQL role assignments, MongoDB role and user definitions, Cassandra managed clusters and MongoDB vCore clusters; no Gremlin, Cassandra keyspace, Table API, service, SQL role definition or private endpoint connection kind in a release, `main` or an open pull request | **built**, generated: `documentdb.azure.hfl.dev/v20240815` (the version of ASO's `DatabaseAccount`, `v1api20240815`, its storage version), fourteen kinds: `DatabaseAccountsGremlinDatabase`, `...GremlinDatabasesGraph`, `...CassandraKeyspace`, `...CassandraKeyspacesTable`, `...Table` and the five throughput settings of those (`throughputSettings/default`), `DatabaseAccountsService` (polymorphic `properties`: one of the four service types), `DatabaseAccountsSqlRoleDefinition`, `DatabaseAccountsPrivateEndpointConnection`, `DatabaseAccountsSqlDatabasesClientEncryptionKey`. ASO's `DatabaseAccount` and `SqlDatabase` are new external owners. Labels: each resource body's own `id` is `simple` (a name, as ASO labels the SQL ones), a private endpoint's ID `arm` (autolabel, reviewed). One generator run past autolabel. A SQL role definition's name must be a GUID; without a webhook it is the object's name or `azureName`, which the user gives (no `Claimer` was written). A client encryption key has no DELETE in the spec (`detach-on-delete`). Proven: the conformance test. Hand-written code: none |
| c12 | Cognitive Services children (RAI policies, the catalog's `Microsoft.CognitiveServices/accounts/raiPolicies`, `docs/coverage/catalog-azure.md`, and the account's other children) | rechecked 2026-11-12, read-only: ASO's `cognitiveservices` group at the pin exports `Account`, `Deployment` and `Project` (2025-06-01); no RAI, blocklist, encryption scope, commitment plan, connection or capability host kind in a release, `main` or an open pull request | **built**, generated: `cognitiveservices.azure.hfl.dev/v20250601` (ASO's version), thirteen kinds: `AccountsRaiPolicy`, `AccountsRaiBlocklist`, `AccountsRaiBlocklistsRaiBlocklistItem`, `AccountsEncryptionScope`, `AccountsCommitmentPlan`, `CommitmentPlan` (a resource group's), `CommitmentPlansAccountAssociation` (the account by reference), `AccountsPrivateEndpointConnection`, `AccountsDefenderForAISetting` (no DELETE in the spec: `detach-on-delete`), `AccountsConnection`, `AccountsProjectsConnection` (polymorphic on `authType`: one property per credential type; the spec's `x-ms-secret` credentials, and a custom-keys connection's `keys` by label, come from Secrets), `AccountsCapabilityHost`, `AccountsProjectsCapabilityHost`. ASO's `Account` and `Project` are new external owners. Labels beyond the secrets: a managed identity connection's `resourceId` and an association's `accountId` are ARM references (autolabel wrote `simple` for the second; corrected). Proven: the conformance test. Hand-written code: none |
| c19 | Azure Automation (the catalog's `azurerm_automation_account`, `docs/coverage/catalog-azure.md`; what `WorkspacesLinkedService` links a workspace to) | rechecked 2026-11-12, read-only: ASO has no `automation` group at the pin, in a release, in `main` or in an open pull request | **built**, generated: `automation.azure.hfl.dev/v20241023` (the newest stable, one TypeSpec file), twenty kinds: `AutomationAccount` (owned by a resource group) and, owned by it, certificates, DSC configurations, connections, connection types, credentials, hybrid runbook worker groups and their workers (`AutomationAccountsHybridRunbookWorker`, shortened: the mechanical name plus `List` is over 63 characters), job schedules, modules, private endpoint connections, runbooks, runtime environments and their packages, schedules, software update configurations, source controls, variables, watchers and webhooks. **Secrets** the spec does not mark: a certificate's `base64Value`, a credential's `password`, a connection's `fieldDefinitionValues` (a Secret map: the connection type says which fields are encrypted, so all are taken from the Secret), a source control's tokens and a webhook's `uri` are `$importSecretMode: required`; a variable's `value` stays a plain string (it is a JSON value; an encrypted variable's value would show in the spec). Labels: a software update's machines and a hybrid worker's VM `arm`, its query scopes `simple` (autolabel, reviewed). **Not built**: jobs and runbook drafts (actions), source control sync jobs (runs), Python 2 packages (retired), Python 3 packages (the generator names a receiver `3P...`, which does not compile: a generator patch, not written), DSC node configurations (the status's `source` is a string and the spec's an object; the generator finds no conversion; State Configuration retires in 2027). Proven: the conformance test. Hand-written code: an account's DSC server endpoint (azurerm's `dsc_server_endpoint`, the catalog's `avm-ptn-alz-management` output) is not in the account's GET but in `agentRegistrationInformation`, which no hook of ASO's reconciler can copy into the status; `$manualConfigs: [DscServerEndpoint]` and a hand-written `KubernetesConfigExporter` (`generator/_extensions/automation`, as ASO's `PrivateEndpoint` exports its NIC address) write it to the ConfigMap `operatorSpec.configMaps.dscServerEndpoint` names (2026-11-13; the registration keys beside it are secrets and not written). The fake computes `automationHybridServiceUrl` and answers the registration GET; envtest reads the endpoint from the ConfigMap. Not shown by the fake: Azure's real endpoint host for an account (the fake's is shaped like Azure's, `<guid>.agentsvc.<region>.azure-automation.net/accounts/<guid>`) |
| c20 | Azure SQL express vulnerability assessment and geo-backup policy (the catalog's `avm-res-sql-server`, `docs/azure.md` gap 19, 2026-11-13) | rechecked 2026-11-13, read-only: ASO's `sql` group at the pin exports the classic, storage-backed `ServersVulnerabilityAssessment` and `ServersDatabasesVulnerabilityAssessment`, not `sqlVulnerabilityAssessments`; its `sql.yaml` leaves `Servers_Databases_GeoBackupPolicy` commented out ("Not actually supported in ARM at the moment"); no release, `main` commit or open pull request adds either | **built**, generated: `sql.azure.hfl.dev/v20250101` `ServersSqlVulnerabilityAssessment` (`servers/{s}/sqlVulnerabilityAssessments/default`, the express configuration: `state`; owned by ASO's `Server`) and `ServersDatabasesGeoBackupPolicy` (`databases/{d}/geoBackupPolicies/Default`: `state`, which azurerm sets to `Disabled` for a DataWarehouse database; owned by ASO's `ServersDatabase`; no DELETE in the spec, so it needs `reconcile-policy: detach-on-delete`). Each name has one value in the spec, so neither kind has `azureName`. 2025-01-01, the newest stable and the version of ASO's own `v20250101` Server and ServersDatabase, now external owners of the generator (`generator/external-owners.txt`; the manager already read them for the Azure SQL `User`); `specification/sql` added to `generator/specs.txt`. One generator run, no label. **Owner fixed 2026-11-13** (found by the catalog): the geo-backup policy's owner reference named `sql.azure.hfl.dev/Servers_Database` (the owner's ARM name, in our group), which no controller serves, so a policy owned by name never became Ready ("was not in reconciledResourceLookup"); `external-owners.txt` listed `sql/ServersDatabase`, but the generator sees the ARM name. Generator patch 0013 lets an entry be `group/ARMName=Kind`, and `sql/Servers_Database=ServersDatabase` now names ASO's `sql.azure.com/ServersDatabase`. The same fix renames the owners of `DatabaseAccountsSqlDatabasesClientEncryptionKey` (`documentdb/DatabaseAccounts_SqlDatabase=SqlDatabase`) and of `AccountsProjectsConnection` and `AccountsProjectsCapabilityHost` (`cognitiveservices/Accounts_Project=Project`), broken the same way. The assessment's owner (`sql.azure.com/Server`) was right. Proven: the conformance test (it owns by ARM ID, so it did not catch the owner), `TestSQLChildrenOwnedByASOKinds` (both kinds owned by name by ASO's `ServersDatabase` and `Server`, Ready in the fake), and `TestGeneratedOwnersAreKnown` (every generated kind's owner is a kind the resolver serves). Not shown by the fake: that Azure accepts a geo-backup policy only on a DataWarehouse database, and that the express assessment needs Defender for SQL on the server. Hand-written code: none |
| c3b | Container Apps at 2026-01-01 (c3's "not built": HTTP route configs and maintenance configurations, which the catalog's managed environment module makes, `docs/coverage/catalog-azure.md`, are stable since 2025-07-01) | rechecked 2026-11-12, read-only: ASO's `app` group at the pin exports `AuthConfig`, `ContainerApp`, `Job`, `ManagedEnvironment` (2025-01-01); none of these in a release, `main` or an open pull request | **built**, generated: `app.azure.hfl.dev/v20260101` (the newest stable; ASO's owners stay at 2025-01-01), nine kinds: `ConnectedEnvironment` and its `Certificate`, `DaprComponent` and `Storage`, `ContainerAppsSourceControl` (owned by ASO's `ContainerApp`, a new external owner), `ManagedEnvironmentsHttpRouteConfig`, `ManagedEnvironmentsMaintenanceConfiguration`, `ManagedEnvironmentsPrivateEndpointConnection`, `SessionPool`. As ASO's `app.yaml`, an identity property takes common-types v6's `ManagedServiceIdentity` (the 2026-01-01 file imports two versions, and the generator stopped at stage 16, "two shapes"). Labels: identities `arm+wellknown` (ASO's label for 2025-01-01), a custom domain's certificate `arm`, a session pool registry's `passwordSecretRef` not a secret (a name). Still not built: Dapr subscriptions and .NET components (preview only), Logic Apps on Container Apps (an extension resource), builders and builds. Proven: the conformance test. Hand-written code: none |
| c3c | Container Apps gaps the catalog's `avm-res-app-managedenvironment` found (catalog `docs/azure.md` gap 3, 2026-11-13): Dapr subscriptions, .NET components, a storage's `accountKeyVaultProperties`, Java components of the Nacos and Spring Cloud Gateway types | rechecked 2026-11-13, read-only: ASO's `app` group at the pin exports the environment, apps, jobs and a few others, none of these; no release, `main` commit or open pull request adds them | **built**, generated. `ManagedEnvironmentsStorage` **moved** from 2025-01-01 to `v20260101` (the newest stable; `azureFile.accountKeyVaultProperties`: a Key Vault secret URL and the identity, `arm+wellknown`, as the other Key Vault properties) and `ManagedEnvironmentsJavaComponent` from 2025-01-01 to `v20260302preview` (the Nacos and Spring Cloud Gateway types exist only in previews; a gateway route's `id` is the route's own name, labelled `simple`, which autolabel wrote `arm`), as §2's decision of 2026-11-10 allows when a field needs it: one version per kind, so a `v20250101` object of either kind must be applied again at the new version (the catalog's charts change their `apiVersion`). New: `ManagedEnvironmentsDaprSubscription` (`v20260302preview`, preview only) and `ManagedEnvironmentsDotNetComponent` (`v20260701`, the first stable with it; `componentType` `AspireDashboard`). 2026-03-02-preview also defines `builders` with an identity from two common-types versions, so the identity transform names `Builder` too. A Java component's `ingress` is read-only in every version (its FQDN), so it is in the status, not the spec. Two generator runs (one label). Proven: `TestManagedEnvironmentsChildren` (a storage with its key from Key Vault, Nacos, a gateway with a route, a subscription, a .NET component) and the conformance test. Hand-written code: none |
| c3d | Container Apps fields ASO's own 2025-01-01 kinds lack (the catalog's `avm-res-app-managedenvironment` and `avm-res-app-containerapp`, catalog `docs/coverage/variables.csv`, 2026-11-13): the environment's `appInsightsConfiguration`, `openTelemetryConfiguration`, `ingressConfiguration`, `publicNetworkAccess`, `availabilityZones`, `diskEncryptionConfiguration`; the app's `kind` and an identity's lifecycle `Retain` | rechecked 2026-11-13, read-only: v2.22.0 the latest release (an `experimental` pre-release of the same day beside it); ASO's `app` group at the pin and on `main` exports `ManagedEnvironment` and `ContainerApp` at 2025-01-01 only; no open pull request moves them | **built**, generated, **beside ASO's kinds, with ASO's kind names in our group** (the first top-level kinds here that ASO also has, at a newer API version): `app.azure.hfl.dev/v20251002preview` `ManagedEnvironment` and `v20260701` `ContainerApp`, each owned by ASO's `ResourceGroup`. The version: 2026-07-01 (the newest stable) has Application Insights, OpenTelemetry, ingress and public network access and lacks zones and disk encryption; 2026-03-02-preview has zones and disk encryption and lacks Application Insights and OpenTelemetry; **2025-10-02-preview is the only version with all six**, so the environment is generated from it (a preview, as `ManagedEnvironmentsJavaComponent` is, §2's rule: a version when a field needs it). The app is at 2026-07-01 (`kind`: `functionapp`, `workflowapp`). Secrets: the Application Insights connection string, the Datadog key, the Dapr AI strings, a Log Analytics shared key and a custom domain's certificate are Secret references (the spec marks them). An OTLP destination's `headers` stay plain strings (the spec does not mark them secret; an API key in a header is in the spec). A child we generate names ASO's `ManagedEnvironment` as its owner kind (`external-owners.txt`), so under our environment it takes `owner.armId` (shown by the test). ASO's `$generatedConfigs` on the app (its FQDN to a ConfigMap) is left out: the generated code imports ASO's internal `genericarmclient`. **Not built: an identity's lifecycle `Retain`**: no spec version at the specs commit ASO pins (`a2de2d1a`) has it (the enum is `None`, `Main`, `Init`, `All` in 2026-07-01 and 2026-03-02-preview); it needs a newer specs commit (open question 12). Proven: `TestManagedEnvironmentNewerFields`, `TestContainerAppKind`, the conformance test. Hand-written code: none |
| f13 | Key Vault: managed HSMs, their keys, private endpoint connections (foundation row 13; row 12 is network leftovers, for the agent that owns `network.yaml`) | rechecked 2026-11-10, read-only: v2.22.0 still the latest release (an `experimental` pre-release beside it); `main` four documentation commits past the pin (#5826, #5827, #5828, #5769); no open or draft pull request adds a Key Vault kind (the open ones are #5821, #5820, #5814, #5804, #5783, #5780, #5773, #5768, #5693, #5659, #5638, #5104, #5028, #4210); no issue asks for managed HSMs; access policies as their own resource were declined upstream (#2940, closed: the `accessPolicies/{operationKind}` path, `add`, `replace` or `remove`, does not fit ASO's model; #3697 closed as its duplicate) and ARM-side secrets too (#1894); ASO's `keyvault.yaml` exports `Vault` and `VaultKey` at 2023-07-01 | **built**, generated: `keyvault.azure.hfl.dev/v20260515` `ManagedHSM` (owned by a resource group; its subnets in `networkAcls.virtualNetworkRules` are references; `tenantId` or `tenantIdFromConfig`, ASO's `Vault` label), `ManagedHSMsKey` (owned by our HSM), `ManagedHSMsPrivateEndpointConnection` (owned by our HSM) and `VaultsPrivateEndpointConnection` (owned by ASO's `Vault`, a 2023-07-01 kind, named by object: the versions need not match), from the 2026-05-15 TypeSpec file, the newest stable. Kind names are the generator's (the ARM types `managedHSMs` and `privateEndpointConnections`), as this repository's other child kinds. **A key** has no DELETE in the spec (ASO's reconciler sends none and reports `DeletionNotSupportedInAzure`; the object goes with `reconcile-policy: detach-on-delete`), and its PUT does not change a key that exists (the API: "returns the existing key without any write operations"), so a change of the spec is sent and the key stays as it was, which `status` shows; ASO's own `VaultKey` works around both through the data plane (a create mode and a delete mode, hand-written), which is not done here. **A private endpoint connection** is made by the private endpoint (ASO's `PrivateEndpoint` with manual approval, or one in another tenant); the kind approves, rejects or removes it, and waits (404 on its Ready condition) until the endpoint has asked. The HSM's connection kind carries `location`, `sku` and `identity` because the spec's model does. Hand-written code: none (the generated share is 100% of the kind code: 18,721 lines, tests aside, 58 of them in the registration; the configuration, the fake's rules and the tests are ours). Not built: the `accessPolicies` resource (declined upstream; use RBAC, ASO's `RoleAssignment`, or the access policies in ASO's `Vault`), ARM-side vault secrets (`vaults/secrets`, which Azure documents as for ARM deployments only and which has no DELETE; our data-plane `VaultSecret` covers secrets), vault keys at 2026-05-15 (ASO's `VaultKey`), the HSM's security domain activation (data plane), and purging a deleted HSM (a POST) |
| f14 | Log Analytics clusters, data sources and storage insight configs (foundation row 14, first part; c5 had left them out) | rechecked 2026-11-10, read-only: v2.22.0 still the latest release; `main` four documentation commits past the pin (#5826, #5827, #5828, #5769); no open or draft pull request or issue adds a Log Analytics cluster, data source, storage insight config or an Operations Management solution; ASO exports only `Workspace` (2025-07-01) | **built**, generated in `operationalinsights.azure.hfl.dev/v20250701` (ASO's `Workspace` version): `Cluster` (dedicated cluster, owned by a resource group: commitment tier, billing type, customer-managed key, availability zones, replication), `WorkspacesDataSource` (owned by ASO's `Workspace`: `kind`, one of 33, and its `properties` as free-form JSON, since each kind takes its own) and `WorkspacesStorageInsightConfig` (owned by ASO's `Workspace`: the storage account a reference, its key a Secret reference, `$importSecretMode: required`, so it reaches neither spec nor status). Data sources needed **generator patch 0008** (`ASO_GENERATOR_RECEIVER_NOT_PARAMETER`): the kind's receiver was named `source`, which the generated conversion functions' parameter shadowed, and the storage conversions did not compile (§2's "a resource whose kind ends in `Source`"); upstream already keeps receivers from being `arm` or `storage`, and the patch adds `source` and `destination`, renaming two receivers of kinds built before (cost `Export`'s delivery destination, a run command's script source) with no other change. Hand-written code: none (the kind code is 100% generated). Not built yet: **Operations Management solutions** (row 14's second part: the 2015 preview spec's `SolutionProperties` lacks `type: object` and the generator drops it); summary logs and network security perimeter configurations (not in the queue) |
| c21 | Application Insights linked storage accounts (catalog `avm-res-insights-component`'s `linked_storage_account`; catalog-azure.md listed it uncovered) | rechecked 2026-11-13, read-only: ASO's `insights` group exports `Component` (2020-02-02), web tests and pricing plans, no linked storage account, at the pin, on `main` and in v2.22.0; no open pull request adds it | **built**, generated: `insights.azure.hfl.dev/v20200301preview` `ComponentsLinkedStorageAccount` (`componentLinkedStorageAccounts_API.json`, the only version), owned by ASO's `Component` (a new external owner: `external-owners.txt`, `controllers.OwnerTypes()`, `config/rbac/role.yaml`, the test CRDs, the operator e2e's footprint). The name is the one-value enum `ServiceProfiler`, so it is fixed; `linkedStorageAccount` is a reference (`$referenceType: arm`). Its generated `PopulateFromARM` declared `typedInput` and did not use it (the owner was its only assignment): **generator patch 0015** declares it only when a statement reads it; nothing generated before changes. Proven: `TestComponentsLinkedStorageAccount`, the conformance test. Hand-written code: none |
| c22 | A user-assigned identity's `isolationScope` (catalog `avm-res-managedidentity-userassignedidentity`; ASO's `UserAssignedIdentity` is at 2023-01-31) | rechecked 2026-11-13, read-only: ASO's `managedidentity` group exports `UserAssignedIdentity` at 2018-11-30 and 2023-01-31 only (pin, `main`, v2.22.0); no open pull request moves it | **built**, generated, beside ASO's under ASO's name (open question 11, as c3d): `managedidentity.azure.hfl.dev/v20241130` `UserAssignedIdentity`, owned by ASO's `ResourceGroup`; new spec service `specification/msi`. Without ASO's `$azureGeneratedSecrets` and `$generatedConfigs` (their code is ASO's internal packages): the client, principal and tenant IDs are in the status, and `operatorSpec.configMapExpressions` exports them. Federated credentials stay ASO's (`FederatedIdentityCredential`, owned by ASO's identity; under ours by `owner.armId`). Proven: `TestUserAssignedIdentityIsolationScope`, the conformance test. Hand-written code: none |
| c23 | A virtual machine's `storageProfile.diskControllerType` and `patchSettings.automaticByPlatformSettings.bypassPlatformSafetyChecksOnUserSchedule`, and an application gateway's `entraJWTValidationConfigs` (catalog `avm-res-compute-virtualmachine` and `avm-res-network-applicationgateway`; c18's stale versions, for these fields) | rechecked 2026-11-13, read-only: ASO exports `VirtualMachine` at 2020-12-01 and 2022-03-01 and `ApplicationGateway` at 2022-07-01, at the pin, on `main` and in v2.22.0; no open pull request moves them | **built**, generated, beside ASO's under ASO's names (open question 11, as c3d): `compute.azure.hfl.dev/v20250401` `VirtualMachine` (2025-04-01, the version of ASO's newest compute kinds; the size a plain string, as ASO's; labels as ASO's 2022-03-01, plus the ones the generator asked for, reviewed) and `network.azure.hfl.dev/v20260301` `ApplicationGateway` (the newest stable network version, already ours for other kinds). In 2026-03-01 (TypeSpec) a gateway's children are no resources, so ASO's `$resourceEmbeddedInParent` labels are not needed; each child's own `id` is a reference (a rule names its listener, pool, settings or JWT configuration by the ID Azure gives it). The admin password, a certificate's data and password are Secret references. Not carried over from ASO's: its VM extensions as their own kind stay ASO's (`VirtualMachinesExtension`, under ours by `owner.armId`). Proven: `TestVirtualMachineNewerFields`, `TestApplicationGatewayEntraJWTValidation`, the conformance test. Hand-written code: none |
| c24 | Recovery Services protected items (`vaults/backupFabrics/protectionContainers/protectedItems`: backup turned on for a VM; catalog `avm-res-compute-virtualmachine`'s `azure_backup_configurations`; h1.2) | rechecked 2026-11-13 and again 2026-11-15, read-only: ASO has no `recoveryservices` group at the pin, on `main` (`60fdf3e9`), in v2.22.0 (the latest release; an `experimental` pre-release of 2026-11-13 aside) or in an open pull request; ASO's `dataprotection` group is Backup vaults (`Microsoft.DataProtection`), which do not back up VMs; open issues #1187 (database backup) and #4455 (a storage account container in a vault) are not this | **built by hand** (ASOQ Q14, builder, 2026-11-15, "as recommended"): `recoveryservices.azure.hfl.dev/v20260701` `VaultsProtectedItem` (`apis/recoveryservices/v20260701`, `pkg/reconcilers/recoveryservices`; section 2, "VaultsProtectedItem"), beside the generated Recovery Services kinds in the same group and version and on the same backup API (2026-07-01). The fixed `backupFabrics/Azure` segment is no resource, so ASO's owner chain cannot build the ID (handoff, "a path with a fixed non-resource segment"); the kind's reconciler builds it from our `Vault`'s ARM ID and the container and item names, derived from the VM when not given (`iaasvmcontainer;iaasvmcontainerv2;<rg>;<vm>`, `vm;iaasvmcontainerv2;<rg>;<vm>`: the spec's examples and Azure's tools put the `iaasvmcontainer;` prefix on the container; the question's sketch had left it off). Virtual machines only; file shares (`AzureFileShareProtectedItem`) later on the same kind. The protection container and protection intent stay unbuilt (a VM's container is registered by Azure with the item) |

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

### Field gaps in ASO's own kinds (the catalog's Azure charts, 2026-11-12)

Reported from the catalog's Azure charts (catalog `docs/azure.md` section
8). Each is in ASO's kind, not ours; what this repository can do is noted.
Upstream issues are not filed from here (CLAUDE.md, "Never"); the drafts are
for the builder.

**Intentional overlap (the builder, 2026-11-15, UPBUG).** The builder files
the upstream issues (not urgent) and our extensions cover these gaps as
*intentional overlap*: where a kind of ours can carry what ASO's lacks, we
build it beside ASO's under ASO's name in our group (as section 8 Q11 does
for newer API versions), recommend ours for the case, and retire it when an
ASO release fixes the gap, the upstream issue being the trigger. Rules for
each overlapping kind:
- Its row here and its `docs/plan.md` section 3 row link the upstream issue
  it overlaps and say when it is retired (the release that closes the
  issue).
- A user picks one of the two kinds for each Azure resource; two
  controllers on one resource fight. The README says which one we recommend
  and warns against pointing both at one resource.
- On retirement, a migration note: set `serviceoperator.azure.com/reconcile-policy: skip`
  on ours, create ASO's adopting the same resource, delete ours.

The upstream issue links below are **placeholders** until the builder files
them (`ISSUE-TBD`); replace each with the issue's URL then.

| Gap | Upstream issue (placeholder) | Overlap of ours |
| --- | --- | --- |
| Storage `signedIdentifiers[].id` generated as an ARM reference | Azure/azure-service-operator#ISSUE-TBD (storage: `signedIdentifiers[].id`) | possible: our own `storage.azure.hfl.dev` `StorageAccountsTableServicesTable` and `StorageAccountsFileServicesShare` with the label `$referenceType: simple`; not built yet (built when a chart needs a stored access policy) |
| Peering `syncRemoteAddressSpace` | Azure/azure-service-operator#ISSUE-TBD (network: peering cannot request `syncRemoteAddressSpace`) | possible only with a hand-written extension that adds the query parameter to the PUT (a generated kind sends none); not built yet |
| `StorageAccount` Smart access tier | none to file: ASO's `main` already has it; the next release carries it | none needed: wait for the release |
| `VaultKey` release policy `immutable` | none to file: not in ARM | a data-plane key kind beside `VaultSecret` (not an overlap of ASO's ARM kind); not built |
| A storage account's static website, a vault's certificate contacts | none to file: data plane | hand-written data-plane kinds, as `VaultSecret`; not built |

| Gap | At the pin (read-only) | What we can do |
| --- | --- | --- |
| A table's and a share's `signedIdentifiers[].id` is an ARM reference (`reference`, `armReference:"Id"`) | ASO's `azure-arm/storage.yaml` labels `TableSignedIdentifier.Id` and `SignedIdentifier.Id` `$referenceType: arm` at every version (v2.22.0 and `main`); the field is the access policy's own name (up to 64 characters), so a plain ID cannot be given and an ARM ID is sent as the name | nothing in our extensions (the kinds are ASO's); upstream fix: label both `$referenceType: simple`. Draft issue: "storage: `signedIdentifiers[].id` on `StorageAccountsTableServicesTable` and `StorageAccountsFileServicesShare` is generated as an ARM reference; it is an identifier string" |
| `VaultKey`'s release policy has no `immutable` flag | Microsoft.KeyVault's ARM spec (2023-07-01, and ASO's `VaultKey` built on it) has only `contentType` and `data` in `KeyReleasePolicy`; `immutable` is a Key Vault **data plane** property (keys API 7.3+) | not in ARM, so no generated kind can carry it; a data-plane key kind beside our `VaultSecret` could (not built). Nothing to file against ASO's generator |
| `StorageAccount` lacks the Smart access tier | ASO's `main` (the pin) has `storage/v20260601` with `accessTier: Smart`; the latest release, v2.22.0, stops at 2025-06-01 | nothing: the next ASO release carries it (or ASO's `experimental` build) |
| A peering cannot send `syncRemoteAddressSpace` | it is a **query parameter** of the peering's PUT (`?syncRemoteAddressSpace=true`), not a body property; ASO's generic reconciler sends none | nothing in our extensions; upstream would need an `ARMResourceModifier`-like hook for query parameters. Draft issue: "network: `VirtualNetworksVirtualNetworkPeering` cannot request `syncRemoteAddressSpace`" |
| A storage account's static website, a vault's certificate contacts | data plane only (blob service properties; Key Vault `certificates/contacts`) | out of scope for generation; both fit the hand-written data-plane pattern of `VaultSecret` and `VaultCertificate` (`pkg/keyvault`, the namespace's identity), certificate contacts most cheaply; not built |

## 4. Upstream and sync

The pin, what is copied and the patch are in `UPSTREAM.md`, with how to
move the pin. A kind or field here moves to ASO when ASO ships it: offer b1
and b2 upstream (they extend ASO's own PostgreSQL user the way its MySQL
user already works), keep ours until a release carries them, then
deprecate ours for a release. The export patch is the one piece that must
keep applying at every pin move.

**Do not wait for upstream releases** (decided 2026-11-10): a kind an
open ASO pull request adds is copied and shipped here, and kept in step with
upstream mechanically (`upstream.yaml`, the coverage check) until a release
carries it.

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
writing ConfigMaps (`create` and `update`, what ASO's
`controllerutil.CreateOrUpdate` of a kind's `operatorSpec.configMaps` and
`configMapExpressions` uses; the ConfigMap is owned by the object and the
garbage collector removes it, so no `delete`; ASO's own role has the same
verbs and more, cluster-wide; added 2026-11-13, when the catalog's
`avm-ptn-alz` found a policy assignment could not export its identity's
principal),
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

`config/secret-writer` is the opt-in that lets our manager write Secrets
(`create`, `update`, `delete`; not `patch`, which ASO's
`controllerutil.CreateOrUpdate` does not use), for the keys Azure makes that
kinds export through `operatorSpec.secrets`, and for
`operatorSpec.secretExpressions` (§8, question 9). It is a
kustomize component (a ClusterRole and a ClusterRoleBinding, both
`aso-extensions-secret-writer`, named in full because a component does not
take the install's `namePrefix`), added to a user's overlay of
`config/default`; README.md, "Writing Secrets", shows how, and how to bind
it per namespace when `AZURE_TARGET_NAMESPACES` narrows the manager. Off by
default. On kind (`tests/operator/run.sh`), a circuit's service key export
is refused (`forbidden`) on the Ready condition with the default install,
then written to its Secret, with an authorization's key, once the component
is applied (`tests/operator/overlay-secret-writer`).

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

Proven by `task test` (envtest, 59 tests, and the generated kinds' own
serialization and conversion tests):

- a move between two slots (section 2, "Moving resources between
  slots"; `test/slotmove`, 2026-11-12): two managers as slots blue and
  green; IpGroup, QueryPack, QueryPacksQuery and VaultSecret are paused on
  blue, taken over by green without being made again or changed, and
  blue's later writes and deletes are refused with `SlotMismatch` before
  they reach Azure;
- a password user gets exactly its database privileges, changes revoke
  what is no longer listed (including grants made by hand), and deletion
  drops the role with its privileges;
- an Entra user is created through `pgaadauth`, by name and by object ID,
  with roles and privileges, signing in as the Entra administrator with the
  token of the namespace's `aso-workload` (strict workload identity) from
  azidentity's client-assertion credential;
- another namespace's identity cannot sign in as that administrator, and
  the fake vault refuses it;
- a Microsoft Entra directory role assignment (`entra.azure.hfl.dev`
  `DirectoryRoleAssignment`, c15) is created in the fake Microsoft Graph
  with the namespace's `aso-workload` token for Graph's audience: Global
  Reader over the directory by object ID, User Administrator over an
  administrative unit with the principal from a ConfigMap; an assignment
  made outside Kubernetes is adopted (`AdoptOrCreate`) rather than made
  twice; `AdoptOnly` waits until there is one to adopt and leaves it in
  Graph on delete; an unknown principal and an unknown administrative unit
  are refused on the Ready condition with Graph's message; a change to the
  role is refused by the API server; another namespace's identity is
  refused (401); deleting removes the assignments. The fake is tested with
  the Graph SDK itself (`pkg/fakecloud/graph`). Not proven: Microsoft Graph
  itself (its error codes and messages are the fake's; real Graph takes
  some seconds to show a new assignment to a `$filter`, so an adoption
  right after a creation elsewhere can miss it), the rights the identity
  needs (Privileged Role Administrator, or `RoleManagement.ReadWrite.Directory`),
  custom role definitions and app scopes beyond being sent, conditions;
- an Azure SQL Entra user (`sql.azure.hfl.dev` `User`, `aadUser`) is
  created with `CREATE USER ... FROM EXTERNAL PROVIDER` on a connection
  signed in with the namespace's `aso-workload` token as the server's Entra
  administrator, by name and by alias, with roles that follow the spec;
  a name Entra does not know is refused (33130); another namespace's
  identity is refused at sign-in; the alias and the user type cannot
  change; deleting drops the user; ASO's password user still works
  (created, its password and roles set, it signs in with the password);
  the fake itself is tested with go-mssqldb (`pkg/fakecloud/azuresql`),
  including TLS with the server's certificate checked and a password
  administrator refused when creating an Entra user (33159). Not proven:
  Azure SQL itself (that Entra resolves the display name to the intended
  principal, group membership of the administrator, Azure's own error
  texts beyond those copied from its documentation);
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
- Container Apps at newer versions beside ASO's (c3d): our
  `ManagedEnvironment` (2025-10-02-preview) is PUT with Application
  Insights (its connection string from a Secret), OpenTelemetry (a Datadog
  key from a Secret), ingress settings, public network access, three zones
  and disk encryption with an identity by ARM ID, all as sent; a change is PUT
  again; a storage of ours names it by `owner.armId` and is made and
  deleted under it; deleting the environment deletes it. Our `ContainerApp`
  (2026-07-01) is PUT with `kind: functionapp`, the environment by ARM ID and
  an identity's lifecycle, read back into the status, and deleted. The fake
  does not check that zones need a virtual network, that the key URL is a
  key the identity may read, or that a `functionapp` runs a Functions image;
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
- App Service config sections and publishing policies (c4b): logs (a
  blob destination's SAS URL from a Secret, not in the status), the
  slot-sticky setting names, authentication v2, and ftp and scm
  publishing turned off are PUT at their fixed names under ASO's `Site`;
  a change is PUT again; deleting the objects leaves every section in the
  fake, as Azure keeps them with the site. The fake does not refuse a
  section under a site that does not exist, nor check a SAS URL;
- App Service app settings and connection strings, by hand (c4c): under
  ASO's `Site` (a stand-in), a set of three settings (plain, from a Secret,
  from a ConfigMap) and two connection strings (from a Secret, plain) are
  PUT with the namespace's identity and read back with `POST .../list`;
  the status names them (with the connection strings' types) and holds no
  value; a change to the Secret is PUT again; a setting taken out of the
  list goes from Azure; a setting with two values is refused by the CRD; a
  slot's settings (owner our `SitesSlot`) wait for a missing Secret and are
  PUT once it exists; deleting the objects leaves the last values. The fake
  refuses a GET and a DELETE of a section, a section of an app that does
  not exist, and an unknown connection string type. Not shown: what Azure
  does with a `Site` PUT that carries `siteConfig.appSettings` beside these
  kinds (the fake keeps the site's body apart), and whether a real GET of
  a section is refused as the spec implies (not run live);
- an Application Insights component's linked storage account (c21), owned
  by ASO's `Component` (a stand-in), is PUT at the fixed name
  `ServiceProfiler` with the storage account's ARM ID and deleted, the
  component left; our `UserAssignedIdentity` (c22) is PUT with
  `isolationScope: Regional`, changed to `None` and deleted. The fake does
  not check that the region supports isolation scopes or that the storage
  account exists;
- our `VirtualMachine` (c23, 2025-04-01) is PUT with `diskControllerType:
  NVMe`, the safety-check bypass on and the admin password from a Secret,
  changed to `SCSI` and deleted; our `ApplicationGateway` (c23, 2026-03-01)
  is PUT with an Entra JWT validation configuration and a rule that names it
  by ID, and deleted. The fake does not check that the size supports NVMe,
  that a JWT configuration's tenant and client exist, or the gateway's
  internal references;
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
- Log Analytics clusters, data sources and storage insight configs,
  generated (f14): a cluster in a resource group at a commitment tier of
  500, changed to 1,000 and PUT again; a syslog data source of ASO's
  `Workspace` (its kind and free-form properties as sent); a storage insight
  config whose storage account is an ARM reference and whose key comes from
  a Secret, the key reaching Resource Manager and neither the status nor a
  GET; all deleted; a cluster at a capacity that is no commitment tier
  refused on its Ready condition. On kind, team-a's cluster and a data
  source of its workspace are Ready and deleted. The fake does not show:
  workspaces linked to a cluster, data collected, and what each data source
  kind accepts;
- Operations Management solutions, generated (f14b): the Updates gallery
  solution in a resource group, enabled on ASO's `Workspace` named by group
  and kind (the workspace's ARM ID reaching Resource Manager and the
  status), with a referenced Automation account by ARM ID, its tags changed
  and PUT again, deleted; a gallery solution named for another workspace
  refused on its Ready condition. On kind, team-a's solution on its
  workspace is Ready and deleted. The fake does not show: which solution
  types exist, contained resources locked and deleted with the solution;
- AKS managed namespaces and snapshots, generated (f15): a managed namespace
  of ASO's `ManagedCluster` (labels, quota, network policy, adoption and
  delete policies), its quota changed and PUT again; a managed namespace of
  ASO's `Fleet`; a node pool snapshot of the cluster's system pool (an ARM
  reference); a private endpoint connection approved; all deleted; a
  managed namespace named `kube-system` refused on its Ready condition. On
  kind, team-a's managed namespace and snapshot are Ready and deleted. The
  fake does not show: the namespace inside the cluster, quotas and network
  policies in effect, a fleet propagating a namespace to its members;
- Container registry tasks and connected registries, generated (f17): a
  Docker build task of ASO's `Registry` with a commit trigger, its context
  and trigger tokens and a custom registry's user name and password from
  Secrets, reaching Resource Manager and neither the status nor a GET, its
  timeout changed; a connected registry syncing with our `RegistriesToken`
  by object; a private endpoint connection approved; all deleted, the kept
  tokens with them; a connected registry whose sync token is not there
  refused on its Ready condition. On kind, team-a's task with a timer
  trigger is Ready and deleted. The fake does not show: runs, triggers
  firing, a connected registry's activation, the Premium SKU;
- Source control configurations and Kubernetes configuration private link
  scopes, generated (f16): a configuration on an AKS cluster named by ARM
  ID, its SSH key a protected setting from a Secret that reaches Resource
  Manager and neither the status nor a GET, its repository changed and PUT
  again; a private link scope for an Arc-enabled cluster (an ARM
  reference) and a private endpoint connection approved; all deleted, the
  protected setting with them; a configuration on a virtual machine
  refused on its Ready condition. On kind, team-a's configuration on an Arc
  cluster and its private link scope are Ready and deleted. The fake does
  not show: Flux running, the SSH key Azure generates, compliance status;
- Activity log profiles and workbook templates, generated (f18, patch
  0009): the subscription's log profile owned by the subscription's ARM ID,
  archiving to a storage account by ARM ID, its retention changed and PUT
  again; a workbook template in a resource group (its template as JSON,
  one gallery); both deleted; a log profile with neither a storage account
  nor an Event Hubs rule refused on its Ready condition. On kind, team-a's
  log profile and workbook template are Ready and deleted. The fake does
  not show: the activity log flowing, a template being a valid workbook;
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
  its Ready condition, and is retried at ASO's usual pace, so a child applied
  with its parent is made once the parent is (a hand-written
  `ErrorClassifier`, `generator/_extensions/management`, added 2026-11-13
  when the catalog's `avm-ptn-alz` found a child waiting the sync period:
  ASO retries an unknown 400 only that slowly). Azure's own code for a
  missing parent is not documented; the classifier matches a code or message
  that says a parent is not found. On kind, `ManagementGroup` and
  `ManagementGroupsSubscription` are Ready for team-a and refused for team-b.
  The fake does not show: whether Azure accepts these names or parents (a
  group's name is a free string here), that creating one takes minutes
  (asynchronous operation), that a new group's parent defaults to the tenant
  root group, that a subscription moved by hand leaves our placement object
  stale, and the rights the identity needs;
- tags as one object, generated (foundation row 2): a `TagSet` on the
  subscription (by ARM ID) and one on an ASO `ResourceGroup` (by reference) are
  PUT as `.../providers/Microsoft.Resources/tags/default`; a change replaces
  the set (the dropped tag is gone); deleting the object deletes the set. On
  kind, Ready for team-a and refused for team-b. The fake does not show Azure's
  rules: a limit of 50 tags, name and value lengths, that PUT on a scope
  replaces every tag including the ones ASO sets on its own resources, and
  that tags on a resource group are inherited by nothing;
- Azure Policy, generated (foundation row 3): a definition kept at a
  management group (our `ManagementGroup` by object; mode, a parameter, the
  rule and metadata as JSON, sent as written) and one at the subscription
  (by ARM ID); a set definition at a child group naming the first by object
  (its ARM ID resolved and sent); assignments at the child group (the set,
  with a parameter value and a non-compliance message), at the
  subscription (placed in the child group, so the root group's definition
  serves it) and at an ASO `ResourceGroup` (the subscription's definition,
  `DoNotEnforce`, a not-scope); an exemption at the resource group from the
  subscription's assignment (`Waiver`, an expiry); an enforcement-mode change
  PUT again; the deletes, users first. Refused, and the refusal reaches the
  Ready condition: a definition at a resource group, and an assignment at a
  group of a definition kept at the subscription. The fake checks Azure
  Policy's scope rules (`docs/fake-cloud.md`). On kind, a definition at the
  subscription, an assignment of it at team-a's `ResourceGroup` by object and
  an assignment of a built-in at team-a's `ManagementGroup` by ARM ID are
  Ready, team-b's definition is refused, and the deletes reach the fake. The
  fake does not show: what Azure's policy engine does with a rule (no
  evaluation, no compliance, no `deny`), the validation of `policyRule` and
  parameter types, that an assignment's identity (for `deployIfNotExists`
  and `modify`) needs a location and role assignments, that Azure refuses
  deleting a definition a set still names, and the rights the identity
  needs (Resource Policy Contributor or Owner at the scope);
- Policy remediations and attestations, generated (foundation rows 4 and
  21): a management group with a definition and a set kept at it and the
  subscription placed in it; assignments at the group, the subscription (the
  set) and an ASO `ResourceGroup`; a remediation at each of the three scopes,
  each naming its assignment by object (the ARM ID resolved and sent), the
  one of the set with its `policyDefinitionReferenceId` and
  `ReEvaluateCompliance`, the one in the resource group with filters (a
  location, a resource by ARM ID), a failure threshold, parallel deployments
  and a resource count; an attestation on a storage account (an ARM ID
  owner) with evidence, an expiry, the responsible person (`properties_owner`,
  sent as `owner`) and metadata as JSON, its compliance state changed and PUT
  again; the deletes. Refused, and the refusal reaches the Ready condition: a
  remediation above its assignment's scope, a remediation of a set
  assignment without `policyDefinitionReferenceId`, and an attestation at a
  management group. On kind, team-a's remediation (assignment by object) and
  attestation (assignment by ARM ID) at its `ResourceGroup` are Ready,
  team-b's attestation is refused, and the deletes reach the fake. The fake
  does not show: a remediation running (Azure's is `Evaluating`, then
  `Complete` or `Failed`, with deployments; the fake's is `Succeeded` at once
  with none), whether the definition's effect can be remediated
  (`deployIfNotExists`, `modify`) or attested (`manual`), compliance changing
  with an attestation, cancelling, and the rights the identity needs
  (Resource Policy Contributor at the scope, and the assignment's identity
  for the deployments);
- Privileged Identity Management, generated (foundation row 5): an
  eligibility request for `Reader` named by `wellKnownName` (resolved by
  listing the role definitions, sent as the role's ARM ID), its principal
  from a ConfigMap, on an ASO `ResourceGroup`, with no `azureName`: the
  reconciler gives it ASO's stable UUID and records it in `spec.azureName`;
  the eligibility schedule it makes is in its status. An activation
  (`SelfActivate`, the role by ARM ID, `namingConvention: random`) gets a
  random UUID that stays the same when the object is changed and PUT again.
  Deleting a request is refused by ASO's reconciler itself: the spec has no
  DELETE for it, so the generated kind says so, no DELETE is sent and the
  object waits with `DeletionNotSupportedInAzure` on its Ready condition (the
  fake would answer a DELETE with 405);
  `reconcile-policy: detach-on-delete` lets it go, the request staying in
  Resource Manager; an `AdminRemove` request whose name is already a GUID
  keeps it and removes the eligibility. A role management policy assignment
  is created and deleted. On kind, team-a's eligibility request is Ready with
  a GUID name, team-b's is refused, and deleting the detached object sends no
  DELETE. The fake does not show: approvals and the policy rules (Azure may
  leave a request `PendingApproval`), schedule times and expiry, whether the
  principal exists, what Azure answers to a DELETE of a request, an
  activation by the principal itself (Azure takes `SelfActivate` only from
  the eligible principal's own token; the operator's identity is someone
  else's), and the rights the identity needs (Owner, User Access
  Administrator or Privileged Role Administrator at the scope);
- budgets and cost exports, generated (foundation row 6): budgets at a
  subscription (an ARM ID owner), an ASO `ResourceGroup` and our
  `ManagementGroup`, with notifications at actual and forecast thresholds to
  addresses, roles and a Monitor action group (by ARM ID), a locale, a
  filter (tags and dimensions under `and`) and a time period; exports at the
  subscription (to a storage account by reference, Parquet with snappy
  compression, partitioned, a column list, a system-assigned identity whose
  principal ID reaches `status.identity`, a daily schedule) and at the
  resource group (to an account by name with the SAS token read from a
  Secret: the fake refuses a name without a token, and keeps and returns it
  obfuscated, as Azure does); a budget's amount and an export's schedule
  status changed and PUT again; the deletes. Refused, and the refusal
  reaches the Ready condition: a budget on a resource (not a Cost Management
  scope), an action group at a management group, a start that is not the
  first of a month, and an export to a storage account that does not exist.
  On kind, team-a's budget and export at its `ResourceGroup` are Ready,
  team-b's budget is refused, and the deletes reach the fake. The fake does
  not show: spend (`currentSpend`, `forecastSpend`) or notifications sent, an
  export running or delivering (no run history), the rules that depend on
  the day (a budget's start at most twelve months ahead, an export's
  recurrence starting in the future), the registration with
  `Microsoft.CostManagementExports` an export's storage needs, what an
  agreement type allows (billing time grains are for web-direct customers),
  and the rights the identity needs (Cost Management Contributor at the
  scope, and the export's identity on the storage account);
- site-to-site VPN, generated (foundation row 8): a local network gateway
  on an ASO `ResourceGroup` (an IP address, an address space, BGP settings)
  and an IPsec connection from a virtual network gateway (by ARM ID, as
  ASO's `VirtualNetworkGateway` would be) to it (by object), with BGP, a
  custom IPsec policy and its shared key read from a Secret: the key reaches
  Resource Manager and is not kept on the resource; a routing weight and
  the on-premises prefixes changed and PUT again; a `Vnet2Vnet` connection
  between two gateways; the deletes, connections first. Refused, and the
  refusal reaches the Ready condition: a connection to a gateway that does
  not exist and a local gateway with neither an address nor an FQDN; the
  CRD refuses a connection type that does not exist. On kind, team-a's
  local gateway and IPsec connection are Ready and deleted. The fake does
  not show: a tunnel coming up (`connectionStatus`, bytes transferred), what
  the gateway's SKU and VPN type allow (policy-based selectors, custom IPsec
  policies, BGP), overlapping address spaces, regions, ExpressRoute
  authorization keys, the half hour a real connection takes to provision,
  and the rights the identity needs (Network Contributor on both sides);
- Maintenance configuration assignments, generated (h1, by patch 0006): our
  configuration assigned to ASO's `VirtualMachine` (owner by group and kind;
  the fake sets `resourceId` to the VM) and at the subscription (owner by ARM
  ID) with a filter by type, OS and tag; the filter changed and PUT again; an
  assignment at a resource group with no filter refused on its Ready
  condition (the fake's rule: a dynamic scope needs a filter and an
  `InGuestPatch` configuration); deleted. On kind, team-a's configuration
  assigned to its VM is Ready and deleted. The fake does not show: which
  resource types a configuration's scope can maintain, machines the filter
  selects, and patching itself;
- Key Vault managed HSMs, generated (f13): an HSM with its SKU, tenant,
  administrators, retention and purge protection, public access turned off
  and PUT again, deleted; a key in it made, a change of its size sent and
  the key kept as it was (the status shows Azure's key), its delete refused
  on the Ready condition (no DELETE in the spec) until `detach-on-delete`
  lets the object go with the key staying; a connection a private endpoint
  made, approved through the kind and removed; an HSM with soft delete off
  refused on its Ready condition; a connection on ASO's `Vault` waiting until
  the endpoint has asked, then approved, rejected and removed. On kind,
  team-a's HSM and key are Ready, the key is detached (no DELETE sent) and
  the HSM deleted. The fake does not show: the security domain that
  activates an HSM, key versions and rotation, the private endpoint itself,
  and the roles the identity needs (Managed HSM Contributor; the local RBAC
  of the HSM for keys);
- ExpressRoute, generated (foundation row 9, first part): a provider
  circuit on an ASO `ResourceGroup` whose service key, made by Azure, is
  written to the Secret `operatorSpec.secrets` names; its private peering
  with the BGP key from a Secret, and an authorization whose key is written
  to a Secret and kept out of the status; the circuit changed and PUT again,
  keeping its peering and authorization (the fake deletes children a parent's
  PUT leaves out, as Azure's network resources do), with the keys unchanged;
  a second circuit's peering joined to the first by Global Reach, its
  authorization key read from the exported Secret, which the fake checks
  against the first circuit's authorizations and marks in use; that peering
  changed and PUT again, keeping the connection; ExpressRoute Direct: a port,
  its authorization's key exported, and a circuit on the port redeeming it;
  everything deleted, children first. Refused, and the refusal reaches the
  Ready condition: a circuit with neither a provider nor a port, a peering
  not named for its type, a Global Reach connection whose key is no
  authorization's; the CRD refuses a peering type that does not exist. On
  kind, team-a's circuit, private peering (BGP key from a Secret) and
  authorization are Ready and deleted; the exported keys are not tried on
  kind, where the manager may not create Secrets (§8, question 9). The fake
  does not show: a provider provisioning the circuit, BGP sessions, ARP and
  route tables, what the SKU and a port's bandwidth allow, Microsoft
  peering's prefix validation, the hours a real circuit or port takes, and
  whether Azure really deletes a circuit's peerings on a PUT without them
  (assumed, as ASO assumes for route tables; the copied code is right either
  way); and the rights the identity needs (Network Contributor);
- ExpressRoute gateways, generated (foundation row 9, second part): a
  gateway on an ASO `ResourceGroup` in a virtual hub named by ARM ID, with
  its scale bounds; its connection to our circuit's private peering (by
  object) redeeming an authorization whose key the manager exported to a
  Secret, which the fake marks in use by the connection, the key kept out of
  the connection's status; the gateway changed and PUT again, keeping its
  connection; a second gateway in the same hub refused on its Ready
  condition; everything deleted, children first. On kind, team-a's gateway
  and its connection (to the circuit's private peering by object) are Ready
  and deleted. The fake does not show: scale units, the routes a hub learns
  and whether a connection's routing configuration fits the hub's route
  tables, the time a real gateway takes, and whether Azure deletes a
  gateway's connections on a PUT without them (assumed, as for circuits);
  and the rights the identity needs (Network Contributor);
- Virtual WAN, generated (foundation row 10, first part): a WAN and a hub
  in it (by object) on an ASO `ResourceGroup`; owned by the hub, a
  connection to a virtual network (by ARM ID), a hub route table whose
  route's next hop is that connection (by object), with which the
  connection is then associated (by object), a route table, routing intent
  to a firewall (by ARM ID), a BGP connection over the hub connection (by
  object) and an IP configuration in a subnet (by ARM ID); an ExpressRoute
  gateway in our hub by object; the hub changed and PUT again, keeping its
  route table; a second routing intent refused on its Ready condition;
  everything deleted, children first. On kind, team-a's WAN, hub and the
  six hub children are Ready and deleted. The fake does not show: the hub
  router and the routes it learns, the default route tables Azure makes,
  address overlap, the time a real hub takes, whether Azure deletes a hub's
  route tables on a PUT without them (assumed, as for circuits); and the
  rights the identity needs (Network Contributor);
- Virtual WAN VPN, generated (foundation row 10, second part): in a WAN and
  a hub (by object), a VPN site with one link (its key from a Secret), a
  VPN gateway in the hub (by object), a NAT rule and a connection to the
  site owned by the gateway (the link connection's shared key from a
  Secret, the site link by ARM ID, the NAT rule by object), a RADIUS VPN
  server configuration (its secret from a Secret) and a point-to-site
  gateway in the hub using it (by object); the keys reach the fake and are
  not returned; the gateway changed and PUT again, keeping its connection
  and NAT rule; a second VPN gateway in the hub refused on its Ready
  condition; everything deleted, children first. On kind, team-a's six VPN
  kinds are Ready and deleted. The fake does not show: tunnels and BGP
  sessions coming up, the gateway's public IP addresses and scale units,
  NAT applied to traffic, a hub refusing deletion while it holds a gateway,
  whether Azure deletes a gateway's connections and NAT rules on a PUT
  without them (assumed, as for circuits), whether Azure returns a site's
  `siteKey` (the fake does), the hour a real gateway takes; and the rights
  the identity needs (Network Contributor);
- Virtual Network Manager, generated (foundation row 11, first part): a
  network manager over a subscription and our management group, network
  manager connections at the subscription (owner by ARM ID) and at our
  `ManagementGroup` (by object), both naming the manager by object; a scope
  connection to another tenant's subscription (its `Pending` state in the
  status); a network group of virtual networks with a static member (a
  virtual network by ARM ID); the manager changed and PUT again; a subnet
  refused as a member of a group of virtual networks, on its Ready
  condition; everything deleted, children first. On kind, team-a's six
  objects are Ready and deleted. The fake does not show: configurations
  reaching the networks (deployments and `commits`, next pull requests),
  the other tenant accepting a scope connection, two managers over one
  scope for the same feature, dynamic membership through Azure Policy, and
  the rights the identity needs (Network Contributor on the manager's
  scopes; a connection at a management group needs rights there);
- IP groups, route filters, service endpoint policies, DNS SOA record sets
  and DNS security policies, generated (foundation row 12): an IP group of a
  prefix, an address and a range; a route filter with a rule allowing two
  Microsoft BGP communities, the filter changed and PUT again keeping its
  rule; a service endpoint policy with a definition for a storage account by
  ARM ID, kept the same way; a host name in an IP group, a second rule on a
  filter and a definition for another service than storage refused on their
  Ready conditions, an access that is no `Access` refused by the CRD; a
  public and a private zone's SOA record sets (owners stand-ins for ASO's
  `DnsZone` and `PrivateDnsZone`) set and changed, one not named `@`
  refused, and on delete the objects go and the record sets stay in
  Resource Manager; a DNS security policy with a domain list, a rule
  blocking it (by object) and a link to a virtual network (by ARM ID), a
  rule with a priority under 100 and a second policy's link to the same
  network refused on their Ready conditions, an action that does not exist
  refused by the CRD; everything else deleted, children first. On kind,
  team-a's eleven objects are Ready, the nine that Azure deletes are
  deleted, and no SOA delete reaches the fake. The fake does not show:
  firewalls using an IP group, a route filter on a circuit's Microsoft
  peering and the routes it lets through, a subnet using a service
  endpoint policy, Azure keeping its own SOA host and serial number,
  queries being allowed, alerted or blocked, the limits on groups, rules
  and domains, and whether Azure deletes a filter's rules or a policy's
  definitions on a PUT without them (assumed, as for circuits);
- Virtual Network Manager configurations, generated (foundation row 11,
  second part): in a manager with a network group (by object), a hub and
  spoke connectivity configuration (the hub, a virtual network, by ARM ID;
  the group by object); a security admin configuration with a rule
  collection for the group and a `Custom` and a `Default` rule (both sides
  of the polymorphic admin rule reach Azure with their `kind`); a security
  user configuration with a rule collection and a rule; the custom rule
  changed and PUT again; a missing hub refused on its Ready condition;
  everything deleted, children first. On kind, team-a's eight objects are
  Ready and deleted. The fake does not show: configurations reaching the
  networks (they deploy only when committed, part 3), the rules a `Default`
  rule's flag stands for, overlapping priorities;
- Virtual Network Manager routing and commits, generated (foundation row
  11, third part): in a manager with Connectivity and Routing access and a
  network group (by object), a routing configuration with a rule collection
  for the group and a rule sending 0.0.0.0/0 to a virtual appliance; a mesh
  connectivity configuration; a Routing and a Connectivity commit (the
  configurations by object) to two regions, their active regions in the
  status; a SecurityAdmin commit (no such access) and a Routing commit that
  names a connectivity configuration refused on their Ready conditions;
  everything deleted, children first. On kind, team-a's four objects are
  Ready and deleted. The fake does not show: a commit deploying anything
  (it is recorded and its regions made active at once; Azure takes
  minutes and may fail per region), routes reaching the networks' route
  tables, a commit's effect on configurations not named (Azure removes
  them from the regions), and that a commit cannot really be deleted to
  undo a deployment (Azure: commit again with fewer configurations);
- Recovery Services, generated (c10 part two): a vault owned by the resource
  group (SKU `RS0`, system-assigned identity, local redundancy, public
  network access) and a backup policy owned by our vault (an IaaS VM
  policy: daily schedule, long-term retention; the owner chain runs through
  our kind) are PUT by ASO's reconciler with the namespace's identity; a
  longer retention is PUT again; they are deleted policy first. A protection
  container was tried and refused by the owner chain (above), which is why it
  is not a kind;
- Recovery Services protected items, by hand (c24; ASOQ Q14, builder,
  2026-11-15; `TestVaultsProtectedItem`, `TestProtectedItems` in the fake):
  a `VaultsProtectedItem` of ASO's `VirtualMachine` in our `Vault` with our
  `VaultsBackupPolicy`, created with them at once, waits for them, names
  its container and item from the VM (written to the spec), refreshes the
  vault's containers before its first PUT (the fake refuses a VM the vault
  has not discovered), follows the long-running refresh and PUT through
  ASO's poller annotations, becomes Ready with Azure's state (`IRPending`)
  in status; a change of disks (`diskExclusionProperties`) and of state
  (`ProtectionStopped`) is PUT again; deleting it follows the long-running
  DELETE and removes the item and the VM's container, before the vault
  (which the fake refuses to delete while it holds an item); a source that
  is not a VM is refused at the claim (`TestVaultsProtectedItemRefusesOtherSources`).
  On kind (`tests/operator/run.sh`), team-a's item is PUT by the manager in
  its own pod with team-a's identity. Not proven (no live run: it needs a
  VM, which costs): that Azure accepts the PUT as sent, how long it takes,
  whether a PUT without the refresh fails, soft delete, and the rights
  Azure checks (docs/fake-cloud.md);
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

**The answers of 2026-11-16** (the builder, on the Open decisions page,
ASOOPEN: Q1 to Q4 and the webhook-less validation of
`docs/handoff.md`), recorded at each question below, verbatim:
- ASOOPEN (Q1 to Q4, CEL rules): "as recommended (Licence Apache-2.0, like the other extension repos (switch the LICENSE before publishing). Offer b1 and b2 upstream and keep ours until ASO releases them. Keep the narrow workload identity as the default. Add CEL rules for reference format and immutable owner and azureName by a generator patch. Read Secrets only in the object's namespace.)"

**The answers of 2026-11-15** (the builder, on the Open decisions page),
recorded at each question below, verbatim:
- ASOQ (Q4, Q11 to Q14): "ASOQ: as recommended (Q11 (a) keep ASO's names in our group, retire each when ASO ships the version; Q12 keep ASO's specs commit until a needed field is past it; Q13 build `backup` by hand when a chart needs it, leave authsettings v1; Q14 hand-built `VaultsProtectedItem` at v20260701. Q4: none written.)"
- EXTQ-ASO-Q10-1 (Q10, after the catalog came to use locks): "EXTQ-ASO-Q10-1: as recommended ((a) Go ahead: generate authorization.azure.hfl.dev/v20200501 ManagementLock in aso-extensions and, in the same window, switch the catalog's aso.lock helper (37 copies of _aso.tpl) and goldens to v20200501, merging aso-extensions first. Nothing is deployed yet, so no live v1 objects need migrating.)"
- UPBUG (the field gaps of ASO's own kinds, section 3): "UPBUG: i will file both, non-urgent but you should create linear tasks due monday eve that contains the bug text or even a gh command to create them. dont surface until then. Cover the S3 and ASO gaps in krm-extensions as intentional overlap, linked to the upstream issues."

**The answers of 2026-11-10**, recorded at each question below and
summarised here:
- Q5 (Azure SQL Entra users, a1): copy ASO pull request #5104, fix it up
  and ship it.
- c15 (Entra directory roles): build it unless it already exists in ASO.
  ASO has none; built as `DirectoryRoleAssignment`
  (section 3, c15).
- One CRD version (section 2): prefer **per-version kinds** over a
  conversion webhook, and avoid a second API version of a generated kind in
  most cases.
- Q7 (the `User` name): keep `User`; the API group says it is PostgreSQL.
- The cost `Export` kind (c8f): keep `Export`, matching the upstream API
  (not `CostExport`).
- Q9 (Secrets): the opt-in `config/secret-writer` overlay is confirmed.
- The four shortened Virtual Network Manager rule kind names (PR #35:
  `NetworkManagersSecurityAdminRuleCollection`,
  `NetworkManagersSecurityAdminRule`,
  `NetworkManagersSecurityUserRuleCollection`,
  `NetworkManagersSecurityUserRule`) are approved; routing's follow the
  same pattern.
- Upstream releases: **do not wait for them.** Ship our kinds and keep up
  with upstream mechanically (section 4).
- Azure resource locks (`ManagementLock`) are not needed for Bundt's first
  catalog (K10); the kind stays as built.

1. **Licence**: MIT, following ASO's licence, is the orchestrator's reading
   of the direction given. Apache-2.0 (the GCP sibling's licence) is
   the alternative; switch before publishing if wanted.

   **Answered, 2026-11-16** (ASOOPEN): Apache-2.0, like the other extension
   repositories. **Done**: `LICENSE` is the Apache License 2.0 text of
   `ack-extensions` and `config-connector-extensions`; every SPDX line of
   ours says Apache-2.0 and `hack/check-spdx.sh` requires it; files copied
   from ASO and the code its generator writes keep Microsoft's MIT header,
   with ASO's licence text in `LICENSE-azure-service-operator` (`NOTICE`).
2. **Upstream first?** b1 and b2 extend ASO's own PostgreSQL user the way
   its MySQL user works, and ASO's maintainers say they want b2 (#4694).
   Offer both upstream and keep ours until a release ships them, or
   upstream only? Recommended: both, ours until the release.

   **Answered, 2026-11-16** (ASOOPEN): offer b1 and b2 upstream and keep
   ours until ASO releases them.

   **b2 offered, 2026-11-16.** [ASO PR #5844](https://github.com/Azure/azure-service-operator/pull/5844)
   (from the builder's fork, closes #4694). The builder signed Microsoft's
   CLA on it (ASOSIGN, 2026-11-16, "done"): `license/cla` is green, and the
   pull request is out of draft, ready for review (2026-11-16).
   **b1 offered, 2026-11-16.** [ASO PR #5845](https://github.com/Azure/azure-service-operator/pull/5845)
   (from the builder's fork, branch `feat/postgresql-user-entra`, closes
   #1399, whose maintainers still want it): `spec.aadUser` on ASO's own
   PostgreSQL `User`, the MySQL `User`'s shape, with the webhook's checks
   where ours has CEL. It is independent of #5844; whichever merges second
   is rebased. Ours stay until a release ships them.
3. **Relaxed workload identity**: the default install needs one extra
   federated credential per managed identity. Keep the narrow account as
   the default (recommended), or default to sharing ASO's?

   **Answered, 2026-11-16** (ASOOPEN): keep the narrow workload identity as
   the default. As built: `config/default` runs the manager as
   `aso-extensions-manager` (`config/manager/manager.yaml`), sharing ASO's
   account is the opt-in `config/share-aso-identity` (section 6, "This
   manager"), and `tests/operator/run.sh` checks that the running pods use
   our account. Nothing changed.
4. **Reading Secrets cluster-wide**: like ASO, the manager reads all
   Secrets (credentials and values). ASO's `AZURE_TARGET_NAMESPACES`
   narrows its cache and ours alike; is that enough, or should the install
   default to it?

   **Still open, 2026-11-15.** ASOQ's answer was "as recommended", and the
   recommendation for Q4 was "none written": the install is unchanged (no
   default `AZURE_TARGET_NAMESPACES`). It waits for the builder.

   **Answered, 2026-11-16** (ASOOPEN): read Secrets only in the object's
   namespace. **Done**: every read already was (the namespace's
   `aso-credential`, a `credential-from` Secret, whose name may not contain
   `/`, and the Secrets a value names, a reference with no namespace field);
   `test/e2e/secret_namespace_test.go` now proves it (a Secret of the same
   name in another namespace is never used; `credential-from` naming
   another namespace is refused). The right to read is its own ClusterRole,
   `aso-extensions-secret-reader` (`config/rbac/secret-reader.yaml`), bound
   in every namespace by default because ASO's framework keeps an informer
   on Secrets in each namespace the manager watches; an install that sets
   `AZURE_TARGET_NAMESPACES` adds `config/namespaced-secret-reader`, which
   drops that binding, and binds the role per namespace (README.md,
   "Reading Secrets"). `AZURE_TARGET_NAMESPACES` is still not set by
   default: it is ASO's setting, shared with ASO's own manager.
5. **Azure SQL Entra users (a1)**: copy PR #5104 here with a test that
   cannot run the real `FROM EXTERNAL PROVIDER` statement, or wait for
   upstream?

   **Answered, 2026-11-10.** Copy ASO pull request #5104, fix it up and
   ship it. Copied read-only, as PIM was (#5638); what the fake
   cannot show is written down with the kind.
6. **ARM gaps here at all?** Building an ARM kind in our group needs a
   patch to ASO's generator (the group domain) and the specs submodule.
   Recommended then: never; ARM gaps go upstream as configuration.

   **Answered, 2026-11-09, twice.** First, with one exception: Azure
   management locks (`ManagementLock`, built, hand-written). Then, lifting
   the rule: Azure API resources are not forbidden; build them or take them
   from upstream, whichever is easier. Plain ARM
   resources are in scope; the order of preference (copy upstream, then
   generate, then hand-write) and the upstream recheck before each kind are
   in section 1. The recommendation above is superseded.
7. **Name collisions**: our `User` shares its kind name with ASO's three
   `User` kinds in other groups; `kubectl get users` is ambiguous and needs
   the group. Rename to `PostgreSQLUser`, or keep ASO's name so moving to
   ASO's group later is a group change only? Recommended: keep it.

   **Answered, 2026-11-10.** Keep `User`; the API group
   (`dbforpostgresql.azure.hfl.dev`) says it is PostgreSQL.
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

   **Answered, 2026-11-09.** Do not orphan the database: a placeholder root
   role becomes its owner. The coordinator's reading, which was built: the
   operator ensures a `NOLOGIN` role on each server, named `hfl_owner`
   unless a field overrides it; deleting a User that owns databases runs
   `REASSIGN OWNED BY <user> TO hfl_owner` in each database it owned, then
   drops the user; status and events name every database moved; nothing
   signs in as `hfl_owner`, and an administrator can later hand a database
   to a real owner. Assumed in building it (not part of the answer): the
   override is a field on each `User`, `spec.reassignOwnedTo`; a database
   removed from `ownedDatabases` while the user lives goes to the same role
   (the database only, without `REASSIGN OWNED`); the role is granted to
   `azure_pg_admin` so every administrator holds it; and a role of that
   name that can sign in is refused rather than changed.
9. **May the manager write Secrets?** Keys Azure makes (an ExpressRoute
   circuit's service key, a circuit or port authorization's key; later
   others with `$azureGeneratedSecrets`) are written to the Secret a kind's
   `operatorSpec.secrets` names, as ASO's own manager does for its kinds.
   The default install lets the manager read Secrets and not create them
   (`tests/operator/run.sh` checks it), so such an export fails there with
   "forbidden" on the Ready condition; envtest, with no RBAC, shows it
   working. Options: (a) grant `create`, `update` and `delete` on Secrets by
   default, as ASO does; (b) an opt-in overlay beside `share-aso-identity`,
   the default unchanged; (c) no export, the user reads the key from Azure
   and writes the Secret. Recommended: (b), then (a) if every user turns it
   on. Asked 2026-11-09 with ExpressRoute (row 9); nothing waits on it but
   the export itself.

   **Recommended, awaiting the builder.** (b) is built so the export can be
   used and tested: `config/secret-writer`, a kustomize component granting
   `create`, `update` and `delete` on Secrets, off by default (§6, "This
   manager"; README.md, "Writing Secrets"); the default install is
   unchanged. Choosing (a) would move its rules into `config/rbac/role.yaml`
   and drop the `! can create secrets` check of `tests/operator/run.sh`;
   choosing (c) would remove the component and the exporters.

   **Answered, 2026-11-10.** (b) is confirmed: the opt-in
   `config/secret-writer` overlay; the default install stays unable to
   create Secrets.

10. **Move `ManagementLock` to the generator?** (asked 2026-11-12, for the
   standing rule "where a kind is hand-written but the generator could own
   it, move it"). `ManagementLock` was hand-written because a lock is one
   type at four scopes, which ASO's generator could not model then
   (section 1). Generator patch 0006 (2026-11-09) now makes a resource at
   several scopes an extension resource with an arbitrary owner, the shape
   `ManagementLock` already has, so `Microsoft.Authorization/locks`
   (`specification/resources`, 2020-05-01) could be generated like the
   policy kinds and the hand-written reconciler and `pkg/armlocks` removed.
   The other hand-written kinds (`VaultSecret`, `VaultCertificate`, the
   PostgreSQL and Azure SQL `User`, `DirectoryRoleAssignment`) are data
   plane or Microsoft Graph, which the generator does not reach.
   The catch: the generated kind's API version is the ARM version,
   `authorization.azure.hfl.dev/v20200501`, not `v1`, and a CRD here has one
   version (section 2), so existing `v1` objects (Bundt's catalog does not
   use locks yet, K10) would have to be recreated (with
   `reconcile-policy: skip` first so the lock stays in Azure). Options: (a)
   generate it as `v20200501` `ManagementLock` and drop `v1`; (b) keep the
   hand-written `v1`; (c) generate it under another kind name beside `v1`
   and deprecate `v1`. Recommended: (a) now, while no catalog uses the
   kind. Not done: a kind's API version is the builder's call. Nothing
   waits on it.

   **Answered, 2026-11-13** (the builder: "Q10 yes, now"): (a), generate
   `ManagementLock` as `authorization.azure.hfl.dev/v20200501` and drop the
   hand-written `v1`, its reconciler and `pkg/armlocks`, now, while no
   catalog chart depends on it. Existing `v1` objects are recreated after
   `reconcile-policy: skip`, so the locks stay in Azure.

   **Asked again and answered, 2026-11-15** (EXTQ-ASO-Q10-1, after the
   catalog had come to use the `v1` kind in 37 charts since 2026-11-12):
   (a), go ahead, in step with the catalog; this repository first. Nothing
   is deployed, so no `v1` object is migrated. **Done**: `ManagementLock`
   is generated as `authorization.azure.hfl.dev/v20200501` (section 2,
   "ManagementLock"); the hand-written `v1`, its reconciler and
   `pkg/armlocks` are gone; the catalog's `aso.lock` helper follows.

11. **Our kinds beside ASO's, at a newer API version, under ASO's names?**
   (asked 2026-11-13, with c3d.) The catalog needs fields ASO's own kinds
   lack because ASO stops at an older API version (`ManagedEnvironment` and
   `ContainerApp` at 2025-01-01, `VirtualMachine` at 2022-03-01,
   `ApplicationGateway` at 2022-07-01, `UserAssignedIdentity` at
   2023-01-31). c3d built the first two in our group under ASO's names
   (`app.azure.hfl.dev` `ManagedEnvironment`, `ContainerApp`), as §8 Q7 kept
   `User`: moving to ASO's kind later is a group change. The costs: two CRDs
   with one kind name (`kubectl get managedenvironments` needs the group);
   a user picks one of the two for each resource (two controllers on one
   Azure resource would fight); and our children, whose owner kind is ASO's,
   name ours by `owner.armId`. Options: (a) keep ASO's names in our group
   (built); (b) a suffix (`ManagedEnvironmentV20251002`), as §2's
   per-version kinds would name a second version; (c) do not build them,
   wait for ASO. Recommended: (a), and retire each when an ASO release
   carries the version, as §4 says for every kind of ours. Nothing waits on
   it but the decision; the later rows of item 3 (VM, application gateway,
   identity) follow whatever is chosen.

   **Answered, 2026-11-15** (ASOQ): (a), ASO's names in our group (as
   built), each retired when an ASO release carries its version.
12. **A newer specs commit?** The generator reads `azure-rest-api-specs` at
   the commit ASO pins (`a2de2d1a`, `UPSTREAM.md`). A field newer than it
   cannot be generated: a container app identity's lifecycle `Retain` is in
   no version there. Options: (a) keep ASO's commit (built kinds stay
   reproducible with ASO's generator); (b) pin a newer specs commit of our
   own for the generator, recorded in `UPSTREAM.md`, regenerating every
   kind (a large, mechanical diff). Recommended: (a) until a field the
   catalog needs is only past it, then (b) in a pull request of its own.

   **Answered, 2026-11-15** (ASOQ): keep ASO's specs commit until a field
   a chart needs is only past it; then a newer commit of our own, in a pull
   request of its own.
13. **A site's `authsettings` (v1) and `backup`?** (asked 2026-11-13, c4b.)
   Both have a PUT and no GET; they are read with `POST .../list`. ASO's
   reconciler reads every resource back with a GET after its PUT, so a
   generated kind would never become Ready. A hand-written extension of the
   generated kind (an `ARMResourceModifier`-like hook is not enough: the
   read is the reconciler's) or a hand-written kind like `ManagementLock`
   would do it. `backup` also carries a SAS URL (a Secret) and has a
   DELETE. The catalog's `auth_settings` is the deprecated v1 form of
   `auth_settings_v2`, which is built. Recommended: build `backup` by hand
   when a chart needs it; leave `authsettings` v1 (charts use v2).

   **Answered, 2026-11-15** (ASOQ): build `backup` by hand when a chart
   needs it; leave `authsettings` v1 unbuilt (charts use v2).
14. **Recovery Services protected items by hand?** (asked 2026-11-13, c24.)
   Backup for a VM is `vaults/{v}/backupFabrics/Azure/protectionContainers/{c}/protectedItems/{i}`.
   ASO's generator cannot own through the fixed `backupFabrics/Azure`
   segment. A hand-written kind on ASO's generic reconciler, as
   `ManagementLock` is (`pkg/reconcilers/authorization`), would: owner our
   `recoveryservices.azure.hfl.dev` `Vault`; spec the VM by reference, the
   policy by reference, and optionally the container and item names
   (derived from the VM when absent, `iaasvmcontainerv2;<rg>;<vm>` and
   `vm;iaasvmcontainerv2;<rg>;<vm>`); PUT and DELETE through ASO's ARM
   client (both are long-running); the fake taught the fabric and container
   segments. Its API version is the builder's call, as Q10 says for locks:
   `v1` (hand-written, as `ManagementLock`) or the ARM version
   (`v20260701`, as the generated vault). Recommended: build it by hand as
   `v20260701` `VaultsProtectedItem`, so it reads like the generated
   Recovery Services kinds; file shares (`AzureFileShareProtectedItem`)
   later on the same kind.

   **Answered, 2026-11-15** (ASOQ): hand-built `VaultsProtectedItem` at
   `v20260701`.
15. **Live runs for kinds kept at a subscription or above?** (asked
   2026-11-23, f28 and f31.) The live identity is Contributor on the
   resource group `aso-extensions-live-tests` only (`docs/live-runs.md`), so
   no live test can exercise `SubscriptionTenantPolicy` (the tenant's
   `Microsoft.Subscription/policies/default`: a tenant-wide setting a global
   administrator writes), the access review kinds, the Cost Management
   `Setting`, `AlertsSuppressionRule` or `GovernanceRule` (a subscription or
   wider scope), nor the existing subscription and management group kinds
   (policy, PIM, Defender plans, management groups). Options: (a) leave them
   fake-only and say so per kind in the README; (b) grant the live identity
   a narrow custom role at the sandbox subscription for the actions these
   kinds need (`Microsoft.Authorization/accessReview*`,
   `Microsoft.Security/alertsSuppressionRules/*`,
   `Microsoft.Security/governanceRules/*`,
   `Microsoft.CostManagement/settings/*`), with `hack/live/targets.txt`
   naming the subscription for those tests only and every test deleting or
   restoring what it changed; (c) a second, empty sandbox subscription for
   subscription-scope tests. The tenant policy should stay fake-only under
   any option: it changes the whole tenant, and its writer is a global
   administrator. Recommended: (b) for the subscription-scope kinds, (a) for
   the tenant policy and management groups.

   **Answered, 2026-11-23** (the builder): (b) as recommended. Grant the live
   identity a narrow custom role at the sandbox subscription covering only
   the actions the subscription-scope kinds need; `hack/live/targets.txt`
   names the subscription for those tests only; every test deletes or
   restores what it changed; the tenant policy and the management groups
   stay fake-only. **Done** (not applied: assigning a role is the
   builder's): the role definitions `hack/live/roles/subscription.json` (at
   the subscription: access reviews, the Cost Management setting, alert
   suppression and governance rules, Defender plans, policy definitions and
   set definitions) and `hack/live/roles/group-policy.json` (policy
   assignments and exemptions, at the live group only), with the command to
   apply them in `docs/live-runs.md`, "The subscription roles";
   `hack/live/targets.txt`'s subscription line naming five tests; those
   tests in `test/live/subscription_test.go` (`TestLiveAccessReviews`,
   `TestLiveCostTagInheritance`, `TestLiveDefenderAlertRules`,
   `TestLiveDefenderPlan`, `TestLiveAzurePolicy`), compiled by `task check`,
   pending a run. PIM is left out: an identity that may write its requests
   may grant itself any role, so it stays fake-only with the tenant policy
   and the management groups (README.md).

16. **A Web PubSub service's access keys as a Secret?** (asked 2026-11-24,
   f68.) ASO's `SignalR` exports its keys and connection strings
   (`operatorSpec.secrets`) through an extension that calls listKeys with the
   SignalR management SDK. Our generated `WebPubSub` cannot: its listKeys is
   in the Web PubSub SDK (`github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/webpubsub/armwebpubsub`),
   a new module dependency, which is the builder's call. Options: (a) add the
   dependency and copy ASO's SignalR secrets extension for `WebPubSub`; (b)
   call listKeys through ASO's generic ARM client (a POST with no SDK), a
   small hand-written extension with no new dependency; (c) leave keys out
   and document Entra authentication (`disableLocalAuth`), which the Web
   PubSub documentation recommends. Recommended: (c) now, (b) if a chart
   needs a connection string.
