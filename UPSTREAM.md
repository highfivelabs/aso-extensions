# Upstream

This repository builds on Azure Service Operator v2 (ASO) at one commit of
`main`. `hack/upstream.sh` reads the commit from the table below (the
"Commit" row), fetches it into `third_party/azure-service-operator` and
applies `patches/azure-service-operator/*.patch` in order;
`hack/upstream.sh --check` fails if the checkout is anything else: it
builds the expected tree (the pin with each patch applied) in a temporary
index and compares every file by content, so a stale copy that has the
right files changed but an older patch's content fails too
(`hack/test-upstream-check.sh`, in `task check`, tests the check).

`upstream.yaml` says the same as this file's tables in machine-readable
form (pins, pull requests, patches, copied files), for tools such as the
factory's daily watch. Change both together; `task upstream:yaml-check`
(part of `task check`) fails when they disagree.

| | |
| --- | --- |
| Repository | `github.com/Azure/azure-service-operator` |
| Commit | `b9dcbe48feaf1173f48ab28dd5edc48b514d3f4d` |
| Commit date | 2026-11-06T03:46:05Z ("fix: detect cycles in owner hierarchy resolution (#5825)") |
| Branch | `main` |
| Nearest release | `v2.22.0` (2026-10-30); the pin is 16 commits ahead of it |
| Pinned on | 2026-11-07 |
| Specs commit | `a2de2d1a53d195c987a30184b2ddd378417ff44e` (the pin's `v2/specs/azure-rest-api-specs` submodule, `github.com/Azure/azure-rest-api-specs`) |
| How | `git fetch --depth 1 origin <commit>`; used through `replace` in `go.mod`, never vendored or committed |

## What is copied, from where

All paths are in ASO at the commit above (under `v2/`), MIT, Copyright
Microsoft. Each copied file keeps its header and says how it was changed.

| Here | From | Changed |
| --- | --- | --- |
| `apis/dbforpostgresql/v1/user_types.go` | `api/dbforpostgresql/v1/user_types.go` | group `dbforpostgresql.azure.hfl.dev`; new `aadUser`, `databasePrivileges`, `status.appliedDatabasePrivileges`, `schemaPrivileges`, `defaultPrivileges`, `ownedDatabases`, `reassignOwnedTo` and their status; `localUser` optional, exactly one of `localUser`/`aadUser` (CEL, where ASO has a webhook); `AzureName()` falls back to `metadata.name` (ASO's defaulting webhook does that) |
| `apis/dbforpostgresql/v1/groupversion_info.go` | `api/dbforpostgresql/v1/groupversion_info.go`, `doc.go` | group |
| `pkg/reconcilers/postgresql/postgresql_user_reconciler.go` | `internal/reconcilers/postgresql/postgresql_user_reconciler.go` | our type; framework through `pkg/outoftree`; `aadUser` as `internal/reconcilers/mysql/aad_user.go` does it, roles created with `pgaadauth`; database privileges and their status; schema and default privileges in each database they name (`database_privileges.go`), so `connectToDB` takes the database; database ownership, and on delete `REASSIGN OWNED` to a `NOLOGIN` role (`ownership.go`); a missing `roleOptions` is ASO's default |
| `pkg/util/postgresql/postgresql.go`, `roles.go`, `role_option.go` and their tests | `internal/util/postgresql/` | `ServerPort` and `TLSRootCAs` variables for tests; `DropUser` revokes database privileges first; `ALTER ROLE` escapes the role name |
| `pkg/util/set/` | `internal/set/` | no |
| `apis/sql/v1/user_types.go` | `api/sql/v1/user_types.go`, with `spec.aadUser` from #5104 at its head | group `sql.azure.hfl.dev`; `localUser` optional, exactly one of `localUser`/`aadUser`, the user type, `azureName` and `aadUser.alias` unchanging (CEL, where #5104 has its webhook); `AzureName()` falls back to `metadata.name`; `aadUser.serverAdminUsername` optional (#5104 requires it and never reads it) |
| `apis/sql/v1/groupversion_info.go` | `api/sql/v1/groupversion_info.go`, `doc.go` | group |
| `pkg/reconcilers/azuresql/azuresql_user_reconciler.go`, `connector.go`, `local_user.go` | `internal/reconcilers/azuresql/` | our type; framework through `pkg/outoftree`; the `aadUser` connector of #5104; the user name is `User.AzureName()` |
| `pkg/reconcilers/azuresql/aad_user.go` | #5104 at its head: `v2/internal/reconcilers/azuresql/aad_user.go` | our type; framework through `pkg/outoftree`; the user name falls back to `metadata.name` |
| `pkg/util/azuresql/azuresql.go`, `role.go` and their tests | `internal/util/azuresql/` | `ServerPort` and `TLSRootCAs` variables for tests; `ConnectToDBUsingAAD` requires encryption and verifies the server's certificate (go-mssqldb's security-token connector over `ConnectToDB`'s configuration; ASO's left encryption at the driver's default); `DropUser` is `DROP USER IF EXISTS` |
| `pkg/controllers/controllers.go` | `internal/controllers/controller_resources.go`, `indexers.go` | our kinds only; lists with `apimeta.ExtractList` |
| `pkg/manager/manager.go` | `cmd/controller/app/setup.go` (`initializeClients`, `initializeWatchers`) | our kinds only; no CRD management, webhooks or ASO's ARM client |
| `generator/azure-arm/authorization.yaml` (the `2020-10-01` `RoleEligibilityScheduleRequest` entries) | #5638 at its head: `v2/azure-arm/authorization.yaml` | `$supportedFrom: v0.1.0`; `RoleAssignmentScheduleRequest` configured the same way |
| `generator/_extensions/authorization/customizations/role_eligibility_schedule_request_extensions.go` | #5638 at its head: `v2/api/authorization/customizations/role_eligibility_schedule_request_extensions.go`, `v2/api/authorization/v20201001/webhook/role_eligibility_schedule_request_defaults.go` | the object is our API version (the controller's), not ASO's storage hub; ASO's internal types through `pkg/outoftree`; the webhook's defaulting runs as an `extensions.Claimer` (no webhook here), so the name is recorded when the object is first claimed; copied into `apis/arm` by `hack/generate-arm.sh` |
| `generator/_extensions/authorization/customizations/role_assignment_schedule_request_extensions.go` | #5638 at its head: the same two files | the same, for `RoleAssignmentScheduleRequest` (not in the pull request) |
| `generator/_extensions/network/customizations/express_route_children.go` | `api/network/customizations/route_table_extensions.go` (`RouteTableExtension.ModifyARMResource`), `api/network/customizations/virtual_network_extensions.go` (`getChildCollectionField`, `getRawChildCollection`, `setChildCollection`, `fuzzySetResource`) | the modifier's body made a function of the collections to keep (a circuit's peerings and authorizations, a peering's Global Reach connections); a collection Azure did not return is left unset; ASO's internal types through `pkg/outoftree`; the helpers unchanged |

The rows that name pull requests #5638 and #5104 are copied from their heads (the section "Pull requests copied"), not from the pin.

Generated by ASO's generator (patched, `hack/generate-arm.sh`) and never
edited: `apis/arm/` (types, storage and wire types, extension points and
their generated tests, with the generator's own header), the registration
`pkg/controllers/arm_resources_gen.go` and `generator/reports/`. Their
configuration is ours: `generator/azure-arm.yaml`, `generator/azure-arm/`,
`generator/specs.txt`, `generator/external-owners.txt` (the top-level filters
and transformers are ASO's `v2/azure-arm.yaml`, trimmed).

New files of ours (no ASO source): `pkg/util/postgresql/aad.go`,
`privileges.go`, `schema_privileges.go`, `default_privileges.go`,
`object_privileges.go`, `ownership.go`, `borrow.go` (ASO has
no schema or default privileges; written the way `privileges.go` follows
ASO's MySQL `ReconcileUserDatabasePrivileges`);
`pkg/reconcilers/postgresql/database_privileges.go`, `ownership.go`; `apis/keyvault/v1/`; `pkg/keyvault/`;
`pkg/reconcilers/keyvault/`; `apis/authorization/v1/`; `pkg/armlocks/`;
`pkg/reconcilers/authorization/` (ManagementLock: no ASO source, as ASO
has no lock; its claiming follows ASO's `ARMOwnedResourceReconcilerCommon`
and its subscription check is ASO's `checkSubscription`, both rewritten
for an unstructured owner, and it uses ASO's exported
`genruntime.ArbitraryOwnerReference` and `CheckARMIDMatchesSubscription`);
`pkg/fakecloud/` (`pkg/fakecloud/azuresql`, a TDS server, among them); `cmd/`; `test/`; `tests/`. Generated from ASO at the pin
by `hack/generate.sh`: `config/test/aso-crds/` (test owner CRDs, now with
`ResourceGroup`) and `config/rbac/aso-owner-reader.yaml` (ASO's API
groups).

## Pull requests copied

| Pull request | Head | Read on | What |
| --- | --- | --- | --- |
| Azure/azure-service-operator#5638 (open, "Add support for Privileged Identity Management in ASO workflow") | `5d0e686e8b0a36b1a59b5ad4e34d68ccc27b949a` | 2026-11-09 | `RoleEligibilityScheduleRequest` (authorization 2020-10-01): its generator configuration and its two hand-written pieces (the rows above that name it) |
| Azure/azure-service-operator#5104 (open, "feat: Add support for Azure SQL AAD Users") | `1219643c9e2f53328681876769a79fba92379e99` | 2026-11-10 | Azure SQL `User` with `aadUser`: the type's new field, the `aadUser` connector and the webhook's checks (as CEL rules here); its webhook test and live test are not copied (no webhook here; no Azure) |

## Patches to the checkout

| Patch | What | Upstream candidate |
| --- | --- | --- |
| `0001-export-framework-for-out-of-tree-managers.patch` | adds `v2/pkg/outoftree/outoftree.go`: type aliases and variables re-exporting, unchanged, the generic reconciler, credential provider, resolver, kube client, config and predicates from `v2/internal/`, which Go forbids importing from another module; plus copies of `setup.go`'s `getDefaultAzureCredential` and `makeControllerOptions` (that package links every generated API) | maybe: an exported extension point is a design decision for ASO's maintainers |

| `0002-generator-own-group-domain-and-no-webhooks.patch` | ASO's generator (`v2/tools/generator`), each change switched by an environment variable `hack/generate-arm.sh` sets so the generator is otherwise ASO's: groups end in `.azure.hfl.dev` (`ASO_GENERATOR_GROUP_SUFFIX`) while owners listed in `generator/external-owners.txt` keep `.azure.com` (`ASO_GENERATOR_EXTERNAL_OWNERS`); the legacy and hybrid version-migration tables are emptied; no webhooks, and `AzureName()` falls back to the object's name (`ASO_GENERATOR_NO_WEBHOOKS`); the API version is the registered, storage version (`ASO_GENERATOR_API_STORAGE`) | maybe: a configurable group domain is a design decision for ASO's maintainers; the rest are ours |
| `0003-export-arm-reconciler-for-out-of-tree-managers.patch` | adds `v2/pkg/outoftree/arm.go`: re-exports, unchanged, ASO's generic ARM reconciler, an ARM connection factory built as `initializeClients` builds it, and the CEL expression evaluator; `v2/pkg/outoftree/extensions.go`: what a hand-written extension of a generated kind needs from `v2/internal/` (the ARM client an `ARMResourceModifier` receives, `reflecthelpers.SetProperty`, `randextensions`' UUID naming, and `set.Set`, which a `KubernetesSecretExporter` receives); `v2/api/authorization/customizations/outoftree.go`: exports `resolveBuiltInRoleDefinition` (a built-in role's ID from its well-known name) as `ResolveBuiltInRoleDefinition` | maybe: with 0001, an exported extension point |
| `0004-generator-no-nested-flatten.patch` | ASO's generator, `flatten_properties.go`: with `ASO_GENERATOR_NO_NESTED_FLATTEN` set (by `hack/generate-arm.sh`), a property flattened out of a flattened property stays an object. ASO's conversions handle one level of `x-ms-client-flatten` only ("need to implement multiple levels of flattening"); this lets Log Analytics data exports and Maintenance configurations be generated. Changes nothing for what generated before, which had no nested flattening | maybe: the real fix is multi-level conversions in ASO's ARM conversion builder, a design change for its maintainers |
| `0005-generator-tenant-scope-children.patch` | ASO's generator, `jsonast/swagger_type_extractor.go`: with `ASO_GENERATOR_TENANT_CHILDREN` set (by `hack/generate-arm.sh`), a resource nested under a tenant scope resource (`/providers/Microsoft.Management/managementGroups/{groupId}/subscriptions/{subscriptionId}`) is categorized as a resource group scope resource: it gets an owner (the parent) and the resolver, which already roots a hierarchy at a tenant scope resource, builds its ARM ID. Without it the generator calls it Tenant scope, gives it no owner and the child's name is taken for the parent's. Changes nothing else | maybe: ASO has no tenant scope parent with children, so no kind of its needs it yet |
| `0006-generator-multi-scope-extension.patch` | ASO's generator, `jsonast/swagger_type_extractor.go`: with `ASO_GENERATOR_MULTI_SCOPE` set (by `hack/generate-arm.sh`), (1) the resource of a path with several `/providers/` segments is the one after the last, so `/providers/Microsoft.Management/managementGroups/{g}/providers/Microsoft.Authorization/policyDefinitions/{name}` is a policy definition (ASO refused the path, "unexpected hard coded name", and the definition was a subscription-only resource); (2) a resource the spec defines under two or more scopes (the same `/providers/{namespace}/{type}/{name}` under a management group, a subscription, a resource group or a resource) is an extension resource, `/{scope}/providers/...`, owned by any of them. Without (2) the first path read wins, which is map order, so the output changed from run to run (Maintenance `configurationAssignments`). Changes nothing for the kinds generated before: their output is the same | maybe: it is one answer to ASO issue #3756's design question (one kind with an arbitrary owner rather than a kind per scope), which is the maintainers' call |
| `0007-generator-subresource-resources.patch` | ASO's generator, `jsonast/swagger_type_extractor.go` (`isMarkedAsARMResource`): with `ASO_GENERATOR_SUBRESOURCE_RESOURCES` set (by `hack/generate-arm.sh`), a named schema built on a `SubResource` with a string `id` is an ARM resource, as it was while the network specs marked `SubResource` `x-ms-azure-resource`. Their TypeSpec conversion (network 2025-05-01 on) dropped the marking, so ExpressRoute gateway connections, hub virtual network connections, hub route tables and VPN connections (a `name`, no `type`) stopped being resources. Changes nothing for the kinds generated before but where shared types are written (their CRDs are the same) | maybe: the specs could restore the marking instead; ASO meets it when it moves its network kinds past 2025-03-01 |
| `0009-generator-subscription-scope-extension.patch` | ASO's generator, `jsonast/swagger_type_extractor.go` (`categorizeResourceScope`): with `ASO_GENERATOR_SUBSCRIPTION_EXTENSION` set (by `hack/generate-arm.sh`), a resource kept directly under a subscription (`/subscriptions/{subscriptionId}/providers/{namespace}/{type}/{name}`, as activity log profiles are) is an extension resource whose owner is the subscription (an ARM ID), as a resource at several scopes is with 0006. Without it the generator gives it Location scope, which it cannot yet model (its own comment says so): the kind has no owner, and its ARM ID comes out as a resource group named after it. Changes no kind generated before (none had Location scope). Number 0008 is pull request #41's (`ASO_GENERATOR_RECEIVER_NOT_PARAMETER`); the two touch different code and apply in either order | maybe: ASO's own TODO ("Not currently possible to generate a resource with scope Location") |

## Moving the pin

Change the "Commit" row (and the "Specs commit" row, from the new pin's
`v2/specs/azure-rest-api-specs` submodule), run `hack/upstream.sh`, then
`task generate-arm` (on the Ubuntu machine: it regenerates `apis/arm/`) and
`task check`. If the
patch no longer applies, or ASO renamed what it exports, fix the patch.
`hack/check-go-mod.sh` fails when a module both `go.mod` files require is at
another version than ASO's; `go mod tidy` after moving the pin brings ours
in line. Re-check the copied files against their sources (the table above)
and port upstream fixes. When ASO ships a field or kind we carry, deprecate
ours.
