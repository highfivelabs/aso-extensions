# aso-extensions

Kubernetes kinds for Azure that
[Azure Service Operator v2](https://github.com/Azure/azure-service-operator)
(ASO) does not have, or has without a field an application needs. They run
on ASO's own reconciler framework and act with the same Azure identity ASO
uses for the same object, so they sit beside an ASO install as if they were
part of it.

Not affiliated with or endorsed by Microsoft.

| Kind | Gap it fills | Upstream |
| --- | --- | --- |
| `dbforpostgresql.azure.hfl.dev/v1` `User` | ASO's PostgreSQL `User` can neither be a Microsoft Entra principal (`aadUser`, which ASO's MySQL `User` has) nor hold privileges on a database (`databasePrivileges`, which ASO's MySQL `User` also has), on a schema (`schemaPrivileges`) or on objects another role creates later (`defaultPrivileges`), nor own a database (`ownedDatabases`; given to a `NOLOGIN` role, `hfl_owner`, rather than orphaned when the user goes) | ASO's PostgreSQL `User`, copied and extended; per-database privileges are ASO issue [#4694](https://github.com/Azure/azure-service-operator/issues/4694), still open; ASO has nothing for schema or default privileges or database ownership |
| `keyvault.azure.hfl.dev/v1` `VaultSecret` | a Key Vault secret whose value comes from a Kubernetes Secret; ASO has `Vault` and `VaultKey` but declined to manage secrets ([#1894](https://github.com/Azure/azure-service-operator/issues/1894)) | ours, with ASO's `VaultKey` create and delete modes |

## Where it sits

| | ASO | here |
| --- | --- | --- |
| API groups | `<service>.azure.com` | `<service>.azure.hfl.dev` |
| Process | ASO's controller manager | our manager, in ASO's namespace, under its own service account |
| Reconciler | ASO's generic reconciler | the same, imported unchanged |
| Credentials | `aso-controller-settings`, a namespace's `aso-credential`, `serviceoperator.azure.com/credential-from` | the same, read by ASO's own credential provider |

The groups never clash with ASO's, so both install on one cluster. Our kinds
name ASO kinds as owners: a `User`'s `spec.owner` is an ASO
`dbforpostgresql.azure.com` `FlexibleServer` in the same namespace, a
`VaultSecret`'s is an ASO `keyvault.azure.com` `Vault`. ASO's annotations
mean what they mean in ASO (`serviceoperator.azure.com/reconcile-policy`,
`.../credential-from`), and the conditions are ASO's.

```yaml
apiVersion: dbforpostgresql.azure.hfl.dev/v1
kind: User
metadata:
  name: orders-api            # the role name, or set spec.azureName
spec:
  owner:
    name: orders-db           # an ASO FlexibleServer
  aadUser:
    serverAdminUsername: aso-admin   # the server's Entra admin = ASO's identity here
  roles: [app_readers]
  databasePrivileges:
    orders: [CONNECT, CREATE]
  schemaPrivileges:           # per database, per schema
    orders:
      public: [USAGE]
  defaultPrivileges:          # on tables orders-migrator creates later
  - database: orders
    schema: public
    forRole: orders-migrator
    objectType: tables
    privileges: [SELECT, INSERT, UPDATE, DELETE]
  ownedDatabases: [orders]    # on delete, orders goes to hfl_owner (NOLOGIN)
---
apiVersion: keyvault.azure.hfl.dev/v1
kind: VaultSecret
metadata:
  name: orders-db-url
spec:
  owner:
    name: orders-vault        # an ASO Vault
  value:
    name: orders-config       # a Kubernetes Secret in this namespace
    key: url
  deleteMode: delete          # detach (default), delete or disable
```

## Identity

The manager reads ASO's settings (`aso-controller-settings`, through the
same environment variables) and uses ASO's credential provider: a
per-resource `credential-from` secret, else the namespace's `aso-credential`,
else the global credential. It runs under its own narrow service account in
ASO's namespace, and acts as exactly the Azure identity ASO would:

| ASO's credential | Extra Azure setup for this manager |
| --- | --- |
| client secret or certificate (global or per namespace) | none: the same secret |
| strict workload identity (`AZURE_WORKLOAD_IDENTITY_AUTH_MODE=strict`) | none: like ASO, it requests a token for `<namespace>/aso-workload` |
| default (relaxed) workload identity | one federated credential on the managed identity, for `system:serviceaccount:azureserviceoperator-system:aso-extensions-manager` |

`config/share-aso-identity` runs the manager as ASO's own service account
instead, for quick trials; it then also holds all of ASO's Kubernetes
rights. `docs/plan.md`, "Identity".

## Installing it

Beside ASO, in ASO's namespace:

```sh
ko apply -k config/default    # CRDs, the manager (azureserviceoperator-system)
```

There is no webhook: validation is CEL rules in the CRDs.

## Developing it

```sh
make upstream      # fetch ASO at the pin (~675 MB) and apply patches/
make check         # pin, go.mod versions, generated files, gofmt, vet, licence
                   # headers, no fake cloud in the production build, tests
make operator-e2e  # the install on kind with the fake cloud (tests/operator/run.sh)
```

`make test` runs the kinds end to end without Azure: a real kube-apiserver
(envtest), the manager wired as in production, azidentity's real
credentials against an in-process fake Entra, a fake Key Vault over TLS, and
PostgreSQL in Docker set up like a flexible server (`docs/fake-cloud.md`).

## Layout

| Path | What |
| --- | --- |
| `apis/<service>/v1/` | each kind's types |
| `config/crds/` | generated CRDs (`hack/generate.sh`) |
| `config/default/`, `config/install/`, `config/rbac/`, `config/manager/` | the install |
| `config/share-aso-identity/` | the install as ASO's own service account (opt-in) |
| `config/fake-cloud/`, `config/test/` | tests only: the fake cloud on kind, ASO's owner CRDs |
| `pkg/reconcilers/<service>/` | each kind's reconciler |
| `pkg/keyvault/` | the Key Vault secrets data plane client |
| `pkg/util/postgresql/` | PostgreSQL helpers (copied from ASO and extended) |
| `pkg/controllers/`, `pkg/manager/` | registration on ASO's generic reconciler; wiring as ASO's manager |
| `pkg/fakecloud/` | tests only: fake Entra, fake Key Vault, the flexible-server stand-in |
| `cmd/manager/` | the controller manager (`-tags fakecloud` adds the test switch) |
| `cmd/fakevault/` | tests only: the fake vault as a server |
| `test/e2e/`, `tests/operator/` | end-to-end tests; the install on kind |
| `patches/azure-service-operator/` | our one change to the ASO checkout |
| `UPSTREAM.md` | the pin and what is copied from where |
| `docs/plan.md` | how ASO is built, how a kind is built here, candidates, identity, what is left |

## Licence

MIT (`LICENSE`), the same as ASO. Files copied from ASO keep Microsoft's
copyright and licence header and say how they were changed; see `NOTICE`.
