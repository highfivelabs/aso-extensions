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
| `keyvault.azure.hfl.dev/v1` `VaultCertificate` | a Key Vault certificate imported from a certificate and key in Kubernetes Secrets (cert-manager's `kubernetes.io/tls`); renewals become new versions | ours, with `VaultSecret`'s create and delete modes; ASO has no certificate kind |
| `authorization.azure.hfl.dev/v1` `ManagementLock` | a management lock (`Microsoft.Authorization/locks`, `CanNotDelete` or `ReadOnly`) on a subscription, resource group or resource, owned by any ASO resource or an ARM ID; the one ARM kind here, by the builder's exception (`docs/plan.md`, section 1) | ours; ASO issue [#3756](https://github.com/Azure/azure-service-operator/issues/3756), open, needs a design for the lock's four scopes |

## Coverage

<!-- coverage:start -->
![Azure coverage by attributes and by resources: official, ours beyond official, uncovered](docs/coverage/bars/_total.svg)

Of the 43,369 writable attributes of the 2,424 ARM resources in the Azure REST API specs, ASO v2 covers 14.1% and our kinds cover a further 0.0%; 85.9% are in neither. Blue is ASO, green is what only we cover, grey is what neither does. Not every service matters alike, so the services are in three tiers by how much the foundational modules people migrate first use them ([`priority.csv`](docs/coverage/priority.csv)):

![Coverage by tier](docs/coverage/bars/_tiers.svg)

| Tier | Services | Official | Ours beyond | Uncovered |
| --- | ---: | ---: | ---: | ---: |
| 1 | 8 | 27.1% | 0.0% | 72.8% |
| 2 | 19 | 33.2% | 0.0% | 66.8% |
| 3 | 253 | 6.0% | 0.0% | 94.0% |

By service, tier first and then attributes covered (every tier 1 and 2 service, and any other with coverage); method, caveats and the data (service, resource and attribute level) are in [`docs/coverage/`](docs/coverage/README.md), all services in [`services.csv`](docs/coverage/services.csv).

| Tier | Service | Coverage | Official | Ours beyond | Uncovered |
| ---: | --- | --- | ---: | ---: | ---: |
| 1 | `Microsoft.Network` | ![Microsoft.Network](docs/coverage/bars/Microsoft.Network.svg) | 24.1% | 0.0% | 75.9% |
| 1 | `Microsoft.Compute` | ![Microsoft.Compute](docs/coverage/bars/Microsoft.Compute.svg) | 35.9% | 0.0% | 64.1% |
| 1 | `Microsoft.Storage` | ![Microsoft.Storage](docs/coverage/bars/Microsoft.Storage.svg) | 63.1% | 0.0% | 36.9% |
| 1 | `Microsoft.KeyVault` | ![Microsoft.KeyVault](docs/coverage/bars/Microsoft.KeyVault.svg) | 40.2% | 0.0% | 59.8% |
| 1 | `Microsoft.OperationalInsights` | ![Microsoft.OperationalInsights](docs/coverage/bars/Microsoft.OperationalInsights.svg) | 16.4% | 0.0% | 83.6% |
| 1 | `Microsoft.Authorization` | ![Microsoft.Authorization](docs/coverage/bars/Microsoft.Authorization.svg) | 5.5% | 0.8% | 93.7% |
| 1 | `Microsoft.ManagedIdentity` | ![Microsoft.ManagedIdentity](docs/coverage/bars/Microsoft.ManagedIdentity.svg) | 83.3% | 0.0% | 16.7% |
| 1 | `Microsoft.Resources` | ![Microsoft.Resources](docs/coverage/bars/Microsoft.Resources.svg) | 2.1% | 0.0% | 97.9% |

<details><summary>Tier 2: 19 services</summary>

| Tier | Service | Coverage | Official | Ours beyond | Uncovered |
| ---: | --- | --- | ---: | ---: | ---: |
| 2 | `Microsoft.ContainerService` | ![Microsoft.ContainerService](docs/coverage/bars/Microsoft.ContainerService.svg) | 70.2% | 0.0% | 29.8% |
| 2 | `Microsoft.Insights` | ![Microsoft.Insights](docs/coverage/bars/Microsoft.Insights.svg) | 72.5% | 0.0% | 27.5% |
| 2 | `Microsoft.Web` | ![Microsoft.Web](docs/coverage/bars/Microsoft.Web.svg) | 16.2% | 0.0% | 83.8% |
| 2 | `Microsoft.App` | ![Microsoft.App](docs/coverage/bars/Microsoft.App.svg) | 48.7% | 0.0% | 51.3% |
| 2 | `Microsoft.DocumentDB` | ![Microsoft.DocumentDB](docs/coverage/bars/Microsoft.DocumentDB.svg) | 45.1% | 0.0% | 54.9% |
| 2 | `Microsoft.Sql` | ![Microsoft.Sql](docs/coverage/bars/Microsoft.Sql.svg) | 44.5% | 0.0% | 55.5% |
| 2 | `Microsoft.CognitiveServices` | ![Microsoft.CognitiveServices](docs/coverage/bars/Microsoft.CognitiveServices.svg) | 24.0% | 0.0% | 76.0% |
| 2 | `Microsoft.Cache` | ![Microsoft.Cache](docs/coverage/bars/Microsoft.Cache.svg) | 80.4% | 0.0% | 19.6% |
| 2 | `Microsoft.DBforPostgreSQL` | ![Microsoft.DBforPostgreSQL](docs/coverage/bars/Microsoft.DBforPostgreSQL.svg) | 41.5% | 0.0% | 58.5% |
| 2 | `Microsoft.ContainerRegistry` | ![Microsoft.ContainerRegistry](docs/coverage/bars/Microsoft.ContainerRegistry.svg) | 25.8% | 0.0% | 74.2% |
| 2 | `Microsoft.AlertsManagement` | ![Microsoft.AlertsManagement](docs/coverage/bars/Microsoft.AlertsManagement.svg) | 46.4% | 0.0% | 53.6% |
| 2 | `Microsoft.AppConfiguration` | ![Microsoft.AppConfiguration](docs/coverage/bars/Microsoft.AppConfiguration.svg) | 88.9% | 0.0% | 11.1% |
| 2 | `Microsoft.Subscription` | ![Microsoft.Subscription](docs/coverage/bars/Microsoft.Subscription.svg) | 60.0% | 0.0% | 40.0% |
| 2 | `Microsoft.Consumption` | ![Microsoft.Consumption](docs/coverage/bars/Microsoft.Consumption.svg) | 0.0% | 0.0% | 100.0% |
| 2 | `Microsoft.DevTestLab` | ![Microsoft.DevTestLab](docs/coverage/bars/Microsoft.DevTestLab.svg) | 0.0% | 0.0% | 100.0% |
| 2 | `Microsoft.Maintenance` | ![Microsoft.Maintenance](docs/coverage/bars/Microsoft.Maintenance.svg) | 0.0% | 0.0% | 100.0% |
| 2 | `Microsoft.Management` | ![Microsoft.Management](docs/coverage/bars/Microsoft.Management.svg) | 0.0% | 0.0% | 100.0% |
| 2 | `Microsoft.OperationsManagement` | ![Microsoft.OperationsManagement](docs/coverage/bars/Microsoft.OperationsManagement.svg) | 0.0% | 0.0% | 100.0% |
| 2 | `Microsoft.RecoveryServices` | ![Microsoft.RecoveryServices](docs/coverage/bars/Microsoft.RecoveryServices.svg) | 0.0% | 0.0% | 100.0% |

</details>

<details><summary>Tier 3: 25 services</summary>

| Tier | Service | Coverage | Official | Ours beyond | Uncovered |
| ---: | --- | --- | ---: | ---: | ---: |
| 3 | `Microsoft.ApiManagement` | ![Microsoft.ApiManagement](docs/coverage/bars/Microsoft.ApiManagement.svg) | 25.0% | 0.0% | 75.0% |
| 3 | `Microsoft.Cdn` | ![Microsoft.Cdn](docs/coverage/bars/Microsoft.Cdn.svg) | 52.4% | 0.0% | 47.6% |
| 3 | `Microsoft.EventGrid` | ![Microsoft.EventGrid](docs/coverage/bars/Microsoft.EventGrid.svg) | 26.7% | 0.0% | 73.3% |
| 3 | `Microsoft.MachineLearningServices` | ![Microsoft.MachineLearningServices](docs/coverage/bars/Microsoft.MachineLearningServices.svg) | 11.0% | 0.0% | 89.0% |
| 3 | `Microsoft.RedHatOpenShift` | ![Microsoft.RedHatOpenShift](docs/coverage/bars/Microsoft.RedHatOpenShift.svg) | 94.7% | 0.0% | 5.3% |
| 3 | `Microsoft.Devices` | ![Microsoft.Devices](docs/coverage/bars/Microsoft.Devices.svg) | 58.6% | 0.0% | 41.4% |
| 3 | `Microsoft.ContainerInstance` | ![Microsoft.ContainerInstance](docs/coverage/bars/Microsoft.ContainerInstance.svg) | 31.0% | 0.0% | 69.0% |
| 3 | `Microsoft.KubernetesConfiguration` | ![Microsoft.KubernetesConfiguration](docs/coverage/bars/Microsoft.KubernetesConfiguration.svg) | 73.0% | 0.0% | 27.0% |
| 3 | `Microsoft.NotificationHubs` | ![Microsoft.NotificationHubs](docs/coverage/bars/Microsoft.NotificationHubs.svg) | 90.8% | 0.0% | 9.2% |
| 3 | `Microsoft.ServiceBus` | ![Microsoft.ServiceBus](docs/coverage/bars/Microsoft.ServiceBus.svg) | 69.9% | 0.0% | 30.1% |
| 3 | `Microsoft.DataProtection` | ![Microsoft.DataProtection](docs/coverage/bars/Microsoft.DataProtection.svg) | 72.1% | 0.0% | 27.9% |
| 3 | `Microsoft.Synapse` | ![Microsoft.Synapse](docs/coverage/bars/Microsoft.Synapse.svg) | 21.5% | 0.0% | 78.5% |
| 3 | `Microsoft.DBforMySQL` | ![Microsoft.DBforMySQL](docs/coverage/bars/Microsoft.DBforMySQL.svg) | 50.5% | 0.0% | 49.5% |
| 3 | `Microsoft.SignalRService` | ![Microsoft.SignalRService](docs/coverage/bars/Microsoft.SignalRService.svg) | 40.7% | 0.0% | 59.3% |
| 3 | `Microsoft.Kusto` | ![Microsoft.Kusto](docs/coverage/bars/Microsoft.Kusto.svg) | 51.1% | 0.0% | 48.9% |
| 3 | `Microsoft.ServiceLinker` | ![Microsoft.ServiceLinker](docs/coverage/bars/Microsoft.ServiceLinker.svg) | 24.7% | 0.0% | 75.3% |
| 3 | `Microsoft.EventHub` | ![Microsoft.EventHub](docs/coverage/bars/Microsoft.EventHub.svg) | 49.5% | 0.0% | 50.5% |
| 3 | `Microsoft.DatabaseWatcher` | ![Microsoft.DatabaseWatcher](docs/coverage/bars/Microsoft.DatabaseWatcher.svg) | 84.8% | 0.0% | 15.2% |
| 3 | `Microsoft.DataFactory` | ![Microsoft.DataFactory](docs/coverage/bars/Microsoft.DataFactory.svg) | 8.1% | 0.0% | 91.9% |
| 3 | `Microsoft.Batch` | ![Microsoft.Batch](docs/coverage/bars/Microsoft.Batch.svg) | 10.8% | 0.0% | 89.2% |
| 3 | `Microsoft.Portal` | ![Microsoft.Portal](docs/coverage/bars/Microsoft.Portal.svg) | 45.9% | 0.0% | 54.1% |
| 3 | `Microsoft.Communication` | ![Microsoft.Communication](docs/coverage/bars/Microsoft.Communication.svg) | 60.0% | 0.0% | 40.0% |
| 3 | `Microsoft.Search` | ![Microsoft.Search](docs/coverage/bars/Microsoft.Search.svg) | 39.4% | 0.0% | 60.6% |
| 3 | `Microsoft.Quota` | ![Microsoft.Quota](docs/coverage/bars/Microsoft.Quota.svg) | 75.0% | 0.0% | 25.0% |
| 3 | `Microsoft.Monitor` | ![Microsoft.Monitor](docs/coverage/bars/Microsoft.Monitor.svg) | 2.2% | 0.0% | 97.8% |

</details>

228 other services in the specs have no coverage from either and are in tier 3.
<!-- coverage:end -->

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
`VaultSecret`'s and a `VaultCertificate`'s is an ASO `keyvault.azure.com`
`Vault`, a `ManagementLock`'s
any ASO resource (or an ARM ID). ASO's annotations
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
---
apiVersion: keyvault.azure.hfl.dev/v1
kind: VaultCertificate
metadata:
  name: orders-tls            # the certificate's (and its secret's) name
spec:
  owner:
    name: orders-vault        # an ASO Vault
  certificate: {name: orders-tls, key: tls.crt}   # e.g. cert-manager's Secret
  privateKey: {name: orders-tls, key: tls.key}
  deleteMode: delete
---
apiVersion: authorization.azure.hfl.dev/v1
kind: ManagementLock
metadata:
  name: keep-orders           # the lock's name, or set spec.azureName
spec:
  owner:                      # any ASO resource here, or {armId: /subscriptions/...}
    group: resources.azure.com
    kind: ResourceGroup
    name: orders-rg
  level: CanNotDelete         # or ReadOnly
  notes: production data
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
credentials against an in-process fake Entra, a fake Key Vault and a fake
Resource Manager over TLS, and PostgreSQL in Docker set up like a flexible
server (`docs/fake-cloud.md`).

## Layout

| Path | What |
| --- | --- |
| `apis/<service>/v1/` | each kind's types |
| `config/crds/` | generated CRDs (`hack/generate.sh`) |
| `config/default/`, `config/install/`, `config/rbac/`, `config/manager/` | the install |
| `config/share-aso-identity/` | the install as ASO's own service account (opt-in) |
| `config/fake-cloud/`, `config/test/` | tests only: the fake cloud on kind, ASO's owner CRDs |
| `pkg/reconcilers/<service>/` | each kind's reconciler |
| `pkg/keyvault/` | the Key Vault data plane client (secrets and certificates) |
| `pkg/armlocks/` | the Resource Manager client for management locks |
| `pkg/util/postgresql/` | PostgreSQL helpers (copied from ASO and extended) |
| `pkg/controllers/`, `pkg/manager/` | registration on ASO's generic reconciler; wiring as ASO's manager |
| `pkg/fakecloud/` | tests only: fake Entra, fake Key Vault, the flexible-server stand-in |
| `cmd/manager/` | the controller manager (`-tags fakecloud` adds the test switch) |
| `cmd/fakevault/`, `cmd/fakearm/` | tests only: the fake vault and the fake Resource Manager as servers |
| `test/e2e/`, `tests/operator/` | end-to-end tests; the install on kind |
| `patches/azure-service-operator/` | our one change to the ASO checkout |
| `docs/coverage/`, `hack/coverage/` | how much of the Azure API ASO and we cover, by service, resource and attribute (generated; `hack/coverage/run.sh`) |
| `UPSTREAM.md` | the pin and what is copied from where |
| `docs/plan.md` | how ASO is built, how a kind is built here, candidates, identity, what is left |

## Licence

MIT (`LICENSE`), the same as ASO. Files copied from ASO keep Microsoft's
copyright and licence header and say how they were changed; see `NOTICE`.
