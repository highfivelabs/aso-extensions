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
| Resource Manager | a store of resources by ARM ID (PUT with the parent existing, GET, DELETE; the body is kept as sent with `id`, `name`, `type` and `provisioningState: Succeeded` added, no properties checked; ASO's own ARM client, for the generated kinds, talks to it) and management locks (`Microsoft.Authorization/locks`, 2020-05-01) at any scope that exists; a lock covers its scope and everything under it: `CanNotDelete` refuses deletes (and deleting a scope that holds a locked resource), `ReadOnly` also refuses writes, each with 409 `ScopeLocked`; locks themselves can always be changed; requests carry a bearer token the test decides to accept | `pkg/fakecloud/arm`, `cmd/fakearm` |
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
