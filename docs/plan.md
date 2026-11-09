# Plan

Status, 2026-11-08: the repository exists locally with two kinds,
`dbforpostgresql.azure.hfl.dev/v1` `User` (ASO's PostgreSQL user with
Microsoft Entra principals, per-database privileges, schema and default
privileges, and database ownership) and
`keyvault.azure.hfl.dev/v1` `VaultSecret`. Both pass their end-to-end tests
against a fake cloud, and the install runs on kind (section 7). It has no
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

So a gap in ASO is one of two kinds:

1. **An ARM resource ASO does not export.** It is usually in the specs
   already; exporting it upstream is configuration (an `$exportAs` entry,
   a sample, a recorded test). The right move is an upstream pull request,
   not a kind here. Building one here would mean running ASO's generator
   with our group domain, which it cannot do without a patch (the domain
   is in its template) and the 1 GB+ specs submodule. Not done; open
   question 6.
2. **Something ARM does not expose, or that ASO declined.** Data-plane
   objects: users and grants inside a database, secrets in a vault. These
   are hand-written in ASO too. This repository's kinds are all of this
   kind.

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
  to install.
- **Dependencies at ASO's versions.** `hack/check-go-mod.sh` fails if any
  module both `go.mod` files require differs from ASO's.

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
| b6 | Key Vault certificates from a Kubernetes Secret (import) | TLS certificates issued in-cluster, kept in Key Vault | after b4; the same shape as `VaultSecret` |
| b7 | ARM resources in the specs but not exported | varies | upstream, by configuration (section 1) |

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
  delete, get deleted, recover, purge), accepting only the tokens a test
  trusts.
- **PostgreSQL**: a real PostgreSQL 17 in Docker with TLS, set up like a
  flexible server: `azure_pg_admin`, a password administrator, Entra
  administrators whose password is the token of the identity that may sign
  in as them, and stand-ins for Azure's `pgaadauth` functions.
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
ConfigMaps, Namespaces and the two ASO owner kinds, events, and tokens
for `aso-workload` only. It cannot change any ASO resource. Sharing ASO's
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

## 7. Proven, and not

Proven by `make test` (envtest, 13 tests):

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
  the user itself as that role.

Proven on kind by `make operator-e2e` (`tests/operator/run.sh`; passed
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
drops the role and soft-deletes the secret. The ownership checks were
added with b5 and are proven by the run recorded in its pull request.

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
above, ADMIN OPTION on roles `pgaadauth` creates.

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
   Recommended: never; ARM gaps go upstream as configuration.
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
