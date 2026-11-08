# Plan

Status, 2026-11-08: the repository exists locally with two kinds,
`dbforpostgresql.azure.hfl.dev/v1` `User` (ASO's PostgreSQL user with
Microsoft Entra principals and per-database privileges) and
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

(a) Upstream has code or an open pull request (read-only `gh`, 2026-11-07):

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
| b4 | PostgreSQL schema privileges and default privileges | since PostgreSQL 15, `public` grants nothing to new users; an app needs `USAGE`/`CREATE` on a schema and default privileges on new tables | next after a1; a field on the same `User` |
| b5 | PostgreSQL database ownership | migrations need the app to own its database | a field on the same `User`, with b4 |
| b6 | Key Vault certificates from a Kubernetes Secret (import) | TLS certificates issued in-cluster, kept in Key Vault | after b4; the same shape as `VaultSecret` |
| b7 | ARM resources in the specs but not exported | varies | upstream, by configuration (section 1) |

App Configuration key-values are already in ASO (`appconfiguration`
`KeyValue`), so they are not a gap.

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

Proven by `make test` (envtest, 8 tests):

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
- the CRDs' CEL rules refuse a user with both or neither credential kind.

Proven on kind by `make operator-e2e` (`tests/operator/run.sh`; passed
2026-11-08 on kind 0.33 on an Apple-silicon Mac, Docker 20.10, both on a
new cluster and rerun on the same one): the CRDs install; the manager runs in ASO's namespace as
`aso-extensions-manager`; that account may request tokens only for
`aso-workload`, may read Secrets and may not create them, may not change
ASO's resources or webhooks; in team-a the `User` and `VaultSecret` become
Ready with team-a's `aso-workload` identity, PostgreSQL holds the Entra role
with its privileges, and the fake vault logs that identity; in team-b,
another identity, both are refused; deletion drops the role and
soft-deletes the secret.

Not provable locally: real Entra exchanging the `aso-workload` token
(the managed identity's federated credential, issuer and audience
checks); the Azure Workload Identity webhook; Azure's own `pgaadauth`
(the stand-ins record the binding in a role comment where Azure uses a
security label, and give the caller ADMIN OPTION on the new role, which
PostgreSQL 16 requires and Azure does not document); Key Vault's RBAC and
its asynchronous recover and purge (instant in the fake).

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
