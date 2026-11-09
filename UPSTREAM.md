# Upstream

This repository builds on Azure Service Operator v2 (ASO) at one commit of
`main`. `hack/upstream.sh` reads the commit from the table below (the
"Commit" row), fetches it into `third_party/azure-service-operator` and
applies `patches/azure-service-operator/*.patch` in order;
`hack/upstream.sh --check` fails if the checkout is anything else.

| | |
| --- | --- |
| Repository | `github.com/Azure/azure-service-operator` |
| Commit | `b9dcbe48feaf1173f48ab28dd5edc48b514d3f4d` |
| Commit date | 2026-11-06T03:46:05Z ("fix: detect cycles in owner hierarchy resolution (#5825)") |
| Branch | `main` |
| Nearest release | `v2.22.0` (2026-10-30); the pin is 16 commits ahead of it |
| Pinned on | 2026-11-07 |
| How | `git fetch --depth 1 origin <commit>`; used through `replace` in `go.mod`, never vendored or committed |

## What is copied, from where

All paths are in ASO at the commit above (under `v2/`), MIT, Copyright
Microsoft. Each copied file keeps its header and says how it was changed.

| Here | From | Changed |
| --- | --- | --- |
| `apis/dbforpostgresql/v1/user_types.go` | `api/dbforpostgresql/v1/user_types.go` | group `dbforpostgresql.azure.hfl.dev`; new `aadUser`, `databasePrivileges`, `status.appliedDatabasePrivileges`, `schemaPrivileges`, `defaultPrivileges` and their status; `localUser` optional, exactly one of `localUser`/`aadUser` (CEL, where ASO has a webhook); `AzureName()` falls back to `metadata.name` (ASO's defaulting webhook does that) |
| `apis/dbforpostgresql/v1/groupversion_info.go` | `api/dbforpostgresql/v1/groupversion_info.go`, `doc.go` | group |
| `pkg/reconcilers/postgresql/postgresql_user_reconciler.go` | `internal/reconcilers/postgresql/postgresql_user_reconciler.go` | our type; framework through `pkg/outoftree`; `aadUser` as `internal/reconcilers/mysql/aad_user.go` does it, roles created with `pgaadauth`; database privileges and their status; schema and default privileges in each database they name (`database_privileges.go`), so `connectToDB` takes the database; a missing `roleOptions` is ASO's default |
| `pkg/util/postgresql/postgresql.go`, `roles.go`, `role_option.go` and their tests | `internal/util/postgresql/` | `ServerPort` and `TLSRootCAs` variables for tests; `DropUser` revokes database privileges first; `ALTER ROLE` escapes the role name |
| `pkg/util/set/` | `internal/set/` | no |
| `pkg/controllers/controllers.go` | `internal/controllers/controller_resources.go`, `indexers.go` | our kinds only; lists with `apimeta.ExtractList` |
| `pkg/manager/manager.go` | `cmd/controller/app/setup.go` (`initializeClients`, `initializeWatchers`) | our kinds only; no CRD management, webhooks or ARM client |

New files of ours (no ASO source): `pkg/util/postgresql/aad.go`,
`privileges.go`, `schema_privileges.go`, `default_privileges.go`,
`object_privileges.go` (ASO has
no schema or default privileges; written the way `privileges.go` follows
ASO's MySQL `ReconcileUserDatabasePrivileges`);
`pkg/reconcilers/postgresql/database_privileges.go`; `apis/keyvault/v1/`; `pkg/keyvault/`;
`pkg/reconcilers/keyvault/`; `pkg/fakecloud/`; `cmd/`; `test/`; `tests/`.

## Patches to the checkout

| Patch | What | Upstream candidate |
| --- | --- | --- |
| `0001-export-framework-for-out-of-tree-managers.patch` | adds `v2/pkg/outoftree/outoftree.go`: type aliases and variables re-exporting, unchanged, the generic reconciler, credential provider, resolver, kube client, config and predicates from `v2/internal/`, which Go forbids importing from another module; plus copies of `setup.go`'s `getDefaultAzureCredential` and `makeControllerOptions` (that package links every generated API) | maybe: an exported extension point is a design decision for ASO's maintainers |

## Moving the pin

Change the "Commit" row, run `hack/upstream.sh`, then `make check`. If the
patch no longer applies, or ASO renamed what it exports, fix the patch.
`hack/check-go-mod.sh` fails when a module both `go.mod` files require is at
another version than ASO's; `go mod tidy` after moving the pin brings ours
in line. Re-check the copied files against their sources (the table above)
and port upstream fixes. When ASO ships a field or kind we carry, deprecate
ours.
