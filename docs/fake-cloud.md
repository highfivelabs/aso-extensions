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
| Key Vault | the secrets data plane, REST 7.5: set, get (latest or a version), update attributes, soft delete, get deleted, recover, purge; soft delete is always on; requests carry a bearer token the test decides to accept | `pkg/fakecloud/keyvault`, `cmd/fakevault` |
| Azure Database for PostgreSQL | PostgreSQL 17 in Docker (tests) or a pod (kind), TLS on, with `azure_pg_admin`, a password administrator, Entra administrators whose password is the fake token of the identity that may sign in as them, and stand-ins for `pgaadauth_create_principal` and `pgaadauth_create_principal_with_oid` | `pkg/fakecloud/postgres` |
| ASO | not run: its owner CRDs (generated from ASO's types at the pin) and `FlexibleServer`/`Vault` objects carrying the status and Ready condition ASO would write | `config/test/aso-crds`, the tests |
| TLS | a throwaway CA and server certificates; the manager trusts the CA through `SSL_CERT_FILE` on kind, through its client options in tests | `pkg/fakecloud/tlsutil` |

## Running it

```sh
make test           # envtest + fake Entra + fake vault in process, PostgreSQL in Docker
make operator-e2e   # the same fakes on kind, with the install from config/default
```

`make test` needs Docker and pulls `postgres:17` if it is missing.
`make operator-e2e` also needs kind and ko; it leaves the cluster
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
- Key Vault's recover and purge finish at once (Azure: asynchronously, so
  the manager requeues after a recover); there is no Key Vault RBAC or
  access policy beyond the accepted tokens.
