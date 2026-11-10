# Handoff

For an agent that takes this repository over with nothing but the
repository. Read `CLAUDE.md` first (the standing rules; `AGENTS.md` is the
same), then this file, then `docs/plan.md` for the reasons. Where this file
and the plan disagree, the plan is newer or this file is wrong: fix this
file.

State at the writing: 2026-11-09, after management groups, `TagSet`, Azure Policy, policy remediations and attestations, Privileged Identity Management, budgets and cost exports (PR #25), lock owners, site-to-site VPN connections and local network gateways (PR #27), and ExpressRoute circuits, peerings, authorizations, Global Reach and ports (foundation rows 1 to 8, the first part of 9, and 21; PIM is the first kind with hand-written extensions of a generated kind, see "Facts learned the hard way"; the policy kinds came with generator patch 0006, resources at several scopes, which also changes how later rows are built: see "Facts learned the hard way"). **The queue changed that day: the builder put platform foundations first** (see "The queue"). The next agent finishes row 9 of `docs/coverage/foundation.md` with ExpressRoute gateways and their connections (`virtualWan.json`: `expressRouteGateways`, `expressRouteGateways/expressRouteConnections`; the gateway needs a virtual hub, by ARM ID until row 10 builds `VirtualHub`; the connection's `authorizationKey` a Secret reference), then 10 (Virtual WAN), 11 (network manager connections), Maintenance `ConfigurationAssignment` (h1 below: generate it with patch 0006); `BillingSubscriptionAlias` and `Microsoft.Subscription/policies` of row 2 are left (reasons in `docs/plan.md` c8b).

## What this repository is

Kubernetes kinds for Azure that Azure Service Operator v2 (ASO) does not
have, run as one manager beside ASO. Three sorts, all in groups
`<service>.azure.hfl.dev`:

| Sort | Kinds | Built by |
| --- | --- | --- |
| Data plane, hand-written | PostgreSQL `User`, `VaultSecret`, `VaultCertificate` | copied from ASO or written new; `pkg/reconcilers/` |
| ARM, hand-written | `ManagementLock` | `pkg/reconcilers/authorization/` (a lock is one type at four scopes, which ASO's generator cannot yet model) |
| ARM, generated | `authorization.azure.hfl.dev` Azure Policy definitions, set definitions, assignments, exemptions, and PIM eligibility and assignment schedule requests and role management policy assignments (two hand-written extension files, `generator/_extensions/`); `policyinsights.azure.hfl.dev` policy remediations and attestations; `consumption.azure.hfl.dev` `Budget`; `costmanagement.azure.hfl.dev` cost `Export`; `management.azure.hfl.dev` management groups; `resources.azure.hfl.dev` `TagSet`; `app.azure.hfl.dev` Container Apps environment children; `web.azure.hfl.dev` App Service slots, host name bindings, certificates; `operationalinsights.azure.hfl.dev` Log Analytics tables, saved searches, linked services, linked storage accounts, query packs and queries; `insights.azure.hfl.dev` Monitor private link scopes; `network.azure.hfl.dev` network security perimeters, site-to-site VPN connections and local network gateways, ExpressRoute circuits, peerings, authorizations, Global Reach connections, ports and port authorizations (hand-written extensions in `generator/_extensions/network/`); `containerregistry.azure.hfl.dev` registry tokens, scope maps, credential sets, webhooks; `compute`, `devtestlab`, `maintenance` VM extras | ASO's own generator, patched, over the pinned specs; `apis/arm/` |

ARM resources are in scope; the builder said so ("Build them or pull them
from upstream, whichever is easier", `docs/plan.md` §1, §8 Q6). Prefer, in
order: skip what ASO has; copy upstream where it exists; generate; hand-write.

## The queue

**The builder's priority, 2026-11-09:** "everything up through a managed
Kubernetes cluster is table stakes for a platform team; the adjacent
foundational things are important for security teams; beyond that,
application primitives. The OSS module space should guide where we put effort."
The ranked list is **`docs/coverage/foundation.md`** (its "queue" tables, rows
1 to 24; the generated inventory below them lists every resource so nothing is
missing). Work its rows in order, one pull request per row or a few related
rows. The order the coordinator named: management groups; subscription tags and
billing aliases; Azure Policy (definition, set, assignment, exemption); policy
remediation; PIM (copy open ASO PR #5638, never generate it); budgets and cost
exports; lock owners (`ManagementLock` completion); VPN connections and local
gateways; ExpressRoute; Virtual WAN and VPN gateways; then the rest of the
foundation table, then the security-adjacent table. The old catalog queue
(c11 to c14: storage local users, Cognitive Services RAI policies, Cosmos
Gremlin and dedicated gateway, AKS managed namespaces) is application
primitives now: after the foundation rows. Already built: c1, c3, c4, c5, c6,
c9, c10 (locks, Container Apps environment children, App Service children, Log
Analytics children, Monitor private link scopes and network security
perimeters, container registry tokens and webhooks, VM extras, Recovery
Services vault and policies). Skipped: c7 (Key Vault keys: ASO's `VaultKey`
#5564 ships in its next release; recheck).

**Tenant-scope resources** (row 1 is built: management groups; row 2 subscription
tags and billing aliases, and policy at management group scope, come next).
`docs/plan.md` §2, "Tenant-scope resources", says what ASO's resolver and
identity do. The fake accepts a tenant root and models the management group
hierarchy now (`pkg/fakecloud/arm`). **A child of a tenant scope resource**
(`.../managementGroups/{g}/subscriptions/{s}`) needs generator patch 0005
(`ASO_GENERATOR_TENANT_CHILDREN`, set by `hack/generate-arm.sh`); without it the
generator gives the child no owner.

| Rank | Gap | State |
| --- | --- | --- |
| h1 | **Hand-written candidates** (the generator cannot model them; write each as `ManagementLock` was, `pkg/reconcilers/authorization/`, on ASO's generic reconciler): ~~1. Maintenance `ConfigurationAssignment`~~ (four scopes: **generate it now**, patch 0006 makes it an extension resource; try it first, it is small); 2. Recovery Services **protection container, protected item (turns backup on for a VM) and protection intent** (a fixed `backupFabrics/Azure` segment is no resource, so the owner chain cannot name the ARM ID); 3. the Recovery Services singletons (`backupstorageconfig`, `backupconfig`, encryption config, vault extended info); 4. Log Analytics solutions (the spec's `SolutionProperties` is dropped as empty). `foundation.md` marks which of its rows are hand-written | as the foundation rows reach them |
| c8 | management groups, policy, budgets, vWAN, subscription tags | now foundation rows 1 to 10 |
| c18 | stale ARM versions (VirtualMachine, ApplicationGateway, DNS zones, resource groups) | not a new kind |

**PIM (built, row 5; what follows is what copying #5638 took)** (read 2026-11-09, head `5d0e686e`, open,
review required). Done as: (1) copied as is; (2) a `Claimer` extension defaults the GUID name and the claim records it (`docs/plan.md` "Names that must be GUIDs"); (3) the extensions live in `generator/_extensions/` and `hack/generate-arm.sh` copies them in; patch 0003 exports what they need; the fake lists role definitions and refuses a request's DELETE (405). The PR is ASO's generator output for
`authorization/v20201001` `RoleEligibilityScheduleRequest` plus three
hand-written pieces: (1) configuration (`$referenceType: arm+wellknown` on
`RoleDefinitionId`, so a built-in role is named `wellKnownName: Reader`;
`$importConfigMapMode: optional` on `PrincipalId`; `$defaultAzureName: false`
and a `NamingConvention` operator-spec property); (2) a webhook defaulter that
makes `azureName` a stable or random UUID (a request's name must be a GUID);
(3) an `ARMResourceModifier` extension in `customizations` that resolves the
well-known role name to the built-in role definition ID through ARM (ASO's
`resolveBuiltInRoleDefinition`, unexported, in
`api/authorization/customizations/role_assignment_extensions.go`). Here: (1)
copies into `generator/azure-arm/authorization.yaml` as is (a 2020-10-01
include beside the policy versions); (2) has no webhook to live in: either
require `azureName` (or `metadata.name`) to be a GUID and say so, or default
it in a generator patch; (3) needs a place for hand-written files in a
generated `customizations` package, which `hack/generate-arm.sh` deletes
(`rm -rf apis/arm`) on every run: copy them in from outside `apis/arm` after
generating, and the extension's internal ASO types (`genericarmclient`,
`reflecthelpers`) may need exporting in patch 0003; the fake needs role
definitions to list by name. A request cannot be deleted: the PR's own test
uses `reconcile-policy: detach-on-delete` and a second `AdminRemove` request,
and the fake should refuse a DELETE (Azure does) so that is what is tested.
Then generate `roleAssignmentScheduleRequests` and
`roleManagementPolicyAssignments` the same way.

Not in the catalog's order but waiting: Azure SQL Entra users (a1, ASO PR
#5104, open question 5) and Entra directory roles (c15, "if wanted").

For every kind, before building: recheck upstream read-only (`gh release
list`, `gh api repos/Azure/azure-service-operator/compare/<pin>...main`,
open and draft pull requests, issues) and record what you found in
`docs/plan.md` §3. Skip what is released or in `main`, and record the skip.

## The generator workflow (a new generated kind)

Everything is in `docs/plan.md` §2, "Generated ARM kinds". The short form:

1. **Specs.** Add the service to `generator/specs.txt`
   (`specification/<service>`). `hack/specs.sh` fetches them at the commit
   ASO pins (`UPSTREAM.md`, "Specs commit") into `.cache/`.
2. **Configuration.** `generator/azure-arm/<group>.yaml`, one per group, and an
   `imports:` line in `generator/azure-arm.yaml`:
   - `typeFilters`: include the one API version (prefer ASO's own latest for
     that group, so owners and children share a vintage). Then prune the
     group's other versions **only for a small group**: for a big one
     (`insights`, `network`, `compute`, `recoveryservices`) pruning breaks the
     loading of the specs (see "Facts learned the hard way"); rely on
     `$exportAs` there;
   - `groupModelConfiguration.<version>`: `$exportAs` and `$supportedFrom: v0.1.0`
     per resource, plus labels the generator demands (`$referenceType: arm` for
     ARM IDs, `$importSecretMode: required|never` for secrets; copy them from
     ASO's `third_party/azure-service-operator/v2/azure-arm/<group>.yaml`).
   - A group must have a `groupModelConfiguration` entry for its version, or
     the generator does not load that version's specs at all.
3. **Owners.** If a kind is owned by an ASO kind (an ASO `Site`, `Workspace`,
   `ResourceGroup`), list it in `generator/external-owners.txt` (`group/Kind`),
   add its storage (hub) type to `controllers.OwnerTypes()` and the scheme in
   `pkg/controllers/controllers.go`, add `list`/`watch` for it in
   `config/rbac/role.yaml`, and add its CRD to the test CRDs in
   `hack/generate.sh` (ASO's hub package for a group is the one
   `internal/controllers/controller_resources_gen.go` registers).
4. **Generate.** `task generate-arm` (on the Ubuntu machine, see below). The
   generator reports the next missing label one error at a time; fix the
   configuration and repeat; a run is about 30 seconds. To find a resource's
   generated name, run the generator with `--verbose` and read the
   `Skipping resource` lines; names look like `Sites_Slot` and become kinds like
   `SitesSlot`.
5. **Bring the output back.** The run writes `apis/arm/`,
   `pkg/controllers/arm_resources_gen.go` and `generator/reports/`; with
   `offload`, tar them into `offload-artifacts/` (it is copied back), extract
   in the repository, then run `hack/generate.sh` locally (deepcopy and CRDs;
   light). Never edit generated files.
6. **Tests.** `test/e2e/arm_*_test.go` are the pattern: stand-ins for the ASO
   owners (`resourceGroup`, `managedEnvironment`, `webSite` in
   `helpers_test.go`, which carry the `serviceoperator.azure.com/resource-id`
   annotation ASO writes), the ARM IDs added to the fake Resource Manager
   (`armFake.AddResource`), then create, wait for Ready, check what the fake
   holds, change, delete. Extend `tests/operator/run.sh` with one kind per
   group, and the fake's initial resources in its `fakearm-resources` list.
7. **Docs.** `docs/plan.md` §3 (c) row, §7 (proven), `README.md` table.

What the generator does not give you: the fixed-name singleton resources
(for example `sites/config/appsettings`) and anything the specs model as
polymorphic; say so in the plan rather than hand-writing around it.

Facts learned the hard way:
- CRDs have one version; the controller is registered on the API type; there
  are no webhooks (so no admission validation beyond the CRD schema, and
  `AzureName()` falls back to `metadata.name`). `docs/plan.md` §2 lists the
  assumption to revisit before the repository goes public.
- `hack/generate.sh` sets `LC_ALL=C`: the order of the CRD kustomization's list
  must not depend on the machine (the laptop and the Ubuntu machine differ).
- ASO's generator cannot do everything the specs describe; find out with a
  run before designing around it (a run is 30 seconds, `--verbose` shows why).
  Limits met so far: (**one level of `x-ms-client-flatten` only**, "need to
  implement multiple levels of flattening", is handled by patch 0004: a
  nested flattened property stays an object, as `MaintenanceWindow` of a
  maintenance configuration does); **an object schema without `type: object` is dropped as empty** (the
  solutions' `SolutionProperties`); **a resource whose kind ends in `Source`
  fails to compile** (the receiver `source` collides with the conversion
  parameter; Log Analytics data sources); **an enum-valued name** (a
  multi-valued `Name` parameter) puts the enum in the storage version, which
  then imports the API version, an import cycle: add a `typeTransformers`
  entry turning the enum (`<Resource>_Name_Spec`) into `string`, as
  `generator/azure-arm/operationalinsights.yaml` does. A `typeTransformers`
  `remove: true` on a property is the usual way past a property the
  generator cannot handle.
- The generator's label errors come all at once (every unlabelled reference
  in one run); a list of ARM IDs (`storageAccountIds`) is labelled the same
  way as a single one and becomes a `[]ResourceReference`.
- **A directly recursive status type** (a management group's `children`, each
  with `children`) stops the run at stage 12 ("cannot be unrolled"); remove the
  inner property with a `typeTransformers` `remove: true` on
  `<Type>_STATUS` (`generator/azure-arm/management.yaml`). A group with an
  `include` for one version and no `prune` loads the other stable versions too:
  add `- action: prune group: <group>` for a small group.
- **A tenant scope resource's children** (`/providers/X/type/{a}/child/{b}`)
  are made Tenant scope with no owner unless `ASO_GENERATOR_TENANT_CHILDREN` is
  set (patch 0005, set by `hack/generate-arm.sh`; set it too in a quick
  generator iteration). Read `structure.txt` for `Owner:` on the child, and the
  generated `Owner()` function: "returns nil as Tenant scoped resources never
  have an owner" means it did not work.
- `task generate-arm` on the Ubuntu machine runs `hack/generate.sh` too;
  tar `apis pkg/controllers/arm_resources_gen.go generator/reports config`
  into `offload-artifacts/`, then **delete `apis/arm`, `config/crds`,
  `config/test/aso-crds` and `generator/reports` locally before extracting**,
  or a kind you dropped leaves stale files. For quick generator iterations,
  `rsync` `generator/` to the machine's checkout
  (`~/Development/git/github.com/highfivelabs/aso-extensions`; the path
  there is under `/home/bbjelland`, not `/Users`) and run `bin/generator
  gen-types --verbose generator/azure-arm.yaml` over `ssh` with the
  environment `hack/generate-arm.sh` sets.
- **Do not `prune` the whole group when ASO's own group already is large
  (`insights`, `network`).** Previews are pruned by default, which removes
  types that stable files refer to and then fails the *loading* of the spec
  ("expected object type for properties of MetricAlertCriteria, got MapType").
  For such groups include the preview versions ASO includes (ASO's
  `insights.yaml` says why) and rely on `$exportAs`: a resource without it is
  not exported. A run over `specification/network` takes about five minutes.
- A generated kind can lose its whole spec side in one API version and keep
  it in the next (network security perimeter 2025-03-01 versus 2025-05-01):
  read `apis/arm/<group>/<version>/structure.txt` (Spec lists) before trusting
  a run that exits 0.
- A generated test package may need a module `go mod tidy` adds (rapid, for
  JSON properties); run `go mod tidy` locally after extracting and commit it.
- **Coverage dashboard (since PR #9):** every PR that adds or changes a kind
  runs `task coverage` (heavy: first run fetches ~100 MB; run it on the Ubuntu
  machine, tar `docs/coverage` and `README.md` back) and commits the output;
  `task coverage-check` must pass.
- **A resource the spec defines at several scopes** (policy definitions at a
  management group and a subscription; Maintenance `configurationAssignments`
  at four scopes) was picked at random by ASO's generator (map order: the
  output changed from run to run and `task check` failed), and a path through
  a management group (`/providers/Microsoft.Management/managementGroups/{g}/providers/...`)
  was refused outright ("unexpected hard coded name", visible with
  `--verbose` as `Error extracting resource name`). **Patch 0006**
  (`ASO_GENERATOR_MULTI_SCOPE`, set by `hack/generate-arm.sh`) takes the
  resource after the last `/providers/` and makes a resource at several
  scopes an extension resource (`ARM URI: /{scope}/providers/...` in the
  generated type's comment; `Owner *genruntime.ArbitraryOwnerReference`). So
  multi-scope and management-group resources are generated now, with an
  arbitrary owner; `hack/coverage`'s route rule says so. Check the `ARM URI`
  comment of what you generate: a subscription-only URI on a resource Azure
  also takes at a group means the group path was lost.
- Azure Policy needed three generator labels beyond `$exportAs`:
  `$referenceType: arm` on the definition and assignment IDs (so they are
  Kubernetes references, `policyDefinitionReference`), and
  `$importSecretMode: never` on `MissingTokenAction` (a name with "Token" is
  taken for a secret). `policyRule`, `metadata` and parameter values became
  `apiextensionsv1.JSON` without help; the recursion a maintainer feared
  (#2767) did not appear.
- The fake models Azure Policy's scope rules (`pkg/fakecloud/arm/policy.go`).
  ASO retries a 404 soon and an unknown 400 very slowly (minutes): a fake
  refusal that a later reconcile should get past (a reference not there yet)
  must be a 404, as Azure's `...NotFound` errors are.
- **`task operator-e2e` must print `all checks passed`, and now fails if it does
  not.** `tests/operator/run.sh` was once emptied by a careless script
  (`open(p,'w').write(open(p).read())` truncates before reading) and the task
  still exited 0, so PR #13's kind test was never run. The task now runs
  `hack/operator-e2e.sh`, which fails if the script is missing, empty or under
  400 lines, exits non-zero, does not end with `all checks passed`, or lacks an
  `ok:` line listed in `tests/operator/required-ok.txt`. **A PR that adds a kind
  adds its `ok:` lines to that file.** `hack/test-operator-e2e-guard.sh` (in
  `task check`) tests the wrapper; `hack/check-tests-present.sh` (in `task test`)
  fails when `test/e2e` loses test functions. Write files through a temp file and
  `wc -l` the result.
- **A path with a fixed non-resource segment** (Recovery Services
  `backupFabrics/{fabricName}`) makes the generator pick the resource group as
  owner and the Kubernetes object cannot name the ARM ID ("had 1 azureNames and
  3 resourceTypes"). A test that creates the object finds it; the generated
  CRD looks fine. Do not ship such a kind.
- Quick generator iteration helper (used throughout, kept out of the
  repository): rsync `generator/` to the machine's checkout, run
  `bin/generator gen-types --verbose generator/azure-arm.yaml` over `ssh` with
  the environment `hack/generate-arm.sh` sets, grep `ERR` in the log. The
  "looks like a resource reference but was not labelled" errors can be
  turned into `$referenceType: arm` labels mechanically (a script that reads
  them from the error and rewrites the group file saves a dozen rounds).
- ASO's token scope is `<audience>/.default`: give the fake audience no
  trailing slash.
- A generated kind's delete under a `CanNotDelete` lock fails with
  `ScopeLocked` and ASO retries slowly; tests allow minutes.
- `kubectl logs | grep -q` under `pipefail` fails when the log is long:
  capture first (`logged` in `tests/operator/run.sh`).
- `upstream.yaml` must list every patch `UPSTREAM.md` lists
  (`task upstream:yaml-check`, in `task check`): PR #18 added the check while
  PRs #17 and #20 added patches 0005 and 0006, so `main` failed `task check`
  until the remediation PR listed them. A new patch goes in both files.
- `offload` does not copy `offload-artifacts/` to the machine: a command that
  writes there must `mkdir -p offload-artifacts` first.
- **Hand-written extensions of a generated kind** (an `ARMResourceModifier`, a
  `Claimer`) go in `generator/_extensions/<group>/customizations/`, mirroring
  `apis/arm`, and `hack/generate-arm.sh` copies them in after generating
  (`docs/plan.md` "Hand-written extensions of generated kinds"). Cast the
  object to the API version (the controller is registered on it), not ASO's
  storage hub. ASO's internals they need come through `pkg/outoftree`
  (patch 0003); add an export there rather than copying ASO code.
- **A name that must be a GUID**: give the kind a `Claimer` that sets an empty
  `spec.azureName` (ASO's webhook code, `outoftree.MakeUUIDName`) before
  calling `next`; the claim commits it. See the PIM extensions.
- **A role definition listed by the Azure SDK has its kind at
  `properties.type`** (`BuiltInRole`), not `roleType`; ASO dereferences it
  without a nil check, so a fake that omits it panics the reconciler (no Ready
  condition at all; run envtest with `E2E_VERBOSE=1` to see the manager's log).
- **A copied `third_party/` was once stale and still passed `hack/upstream.sh
  --check`**, which then compared the list of changed files, not their
  contents: a copy of an older checkout lacked patch 0006's code (the same file
  is changed by 0005) and the generator failed at random on policy
  attestations. The check now compares content (the pin plus the patches,
  built in a temporary index; `hack/test-upstream-check.sh` tests it), so a
  stale copy fails `task check`. In a new clone run `hack/upstream.sh` (it
  resets and re-applies the series); copying another clone's `third_party/`
  first only saves the fetch.
- **A directly recursive type stops the generator at stage 12 even when no
  exported resource uses it** (Cost Management's `QueryFilter` and
  `ReportConfigFilter`, used only by queries and views): ASO prunes the whole
  `costmanagement` group for it. Prune the types by name before the group's
  `include` instead (`generator/azure-arm/costmanagement.yaml`), and the
  resources that do not use them generate.
- **An indirectly recursive status type passes the generator and fails
  later**: an export run's `runSettings` holds the export's properties, which
  hold the run history. The generator exits 0; the generated property tests
  overflow the stack and envtest refuses the CRD (an object field with no
  type). Run `task check`'s tests before trusting a run; remove the property
  with a `typeTransformers` entry on the `*Properties_STATUS` type (before
  flattening the property is not on the resource's own type).
- A resource whose spec path already is `/{scope}/providers/...` (budgets,
  exports) is an extension resource without patch 0006.
- A reference to an ASO kind the manager does not read (Monitor
  `ActionGroup`, `StorageAccount`) works by `armId`; by group and kind it
  would need the kind in the scheme and `controllers.OwnerTypes()`, as an
  owner does. The tests use `armId`.
- **A resource embedded whole in another** (a VPN connection's
  `virtualNetworkGateway1`, `localNetworkGateway2`: the spec puts the full
  gateway objects there) is reduced by the generator to a `reference` (an
  ARM ID or an object) once the embedded type is a resource; nothing to
  configure. Exporting one more version of a big group (`network` 2026-03-01
  beside 2025-05-01) only needs its `include` and a `groupModelConfiguration`
  entry; the run over `specification/network` took about a minute on the
  Ubuntu machine, and the shared `SubResource` gains conversions in the other
  version's storage package (generated, expected).
- **A parent whose PUT carries its children** (an ExpressRoute circuit's
  `peerings` and `authorizations`, a peering's `connections`; ASO's route
  tables, NSGs and virtual networks): label the list
  `$resourceLifecycleOwnedByParent: <the generator's resource name>` (for a
  child resource that is `ExpressRouteCircuits_Peering`, not the exported
  name; "not consumed" otherwise) so it leaves the Kubernetes spec and stays
  in the ARM payload, and give the parent an `ARMResourceModifier` that copies
  the list from a GET (`generator/_extensions/network/customizations/express_route_children.go`,
  ASO's code). The embedded child types then need their own `Id` labelled
  (`$referenceType: simple`), under the names the generator reports
  (`ExpressRouteCircuitConnection_SubResourceEmbedded`). A child resource
  whose spec inherits `SubResource` gets an `Id` in its own spec: remove it
  with a `typeTransformers` entry on `<Resource>_Spec` (pre-export name).
- **Keys Azure makes** (a service key, an authorization key):
  `$azureGeneratedSecrets` on the resource gives `operatorSpec.secrets`; a
  `typeTransformers` `remove` keeps the key out of spec and `_STATUS`; a
  hand-written `KubernetesSecretExporter` reads it with a GET
  (`express_route_keys.go`) and writes the Secret. The default install's
  manager may not create Secrets, so the export fails there (`forbidden` on
  the Ready condition); the opt-in component `config/secret-writer` grants
  `create`, `update` and `delete` on Secrets, and `tests/operator/run.sh`
  shows both (open question 9 in `docs/plan.md`, recommended, awaiting the
  builder). Keys the user gives are Secret references
  (`$importSecretMode: required`).
- An extension that imports ASO's `api/<group>/customizations` builds in that
  group's API packages and their Azure SDK modules (`armnetwork` for
  network): copy the few helpers instead and record them in `UPSTREAM.md`.
- A resource with PUT and no DELETE under a parent the generator does not take
  as a resource (ExpressRoute cross-connections and their peerings) fails
  `$exportAs` with "type not seen"; record it as not built.
- A property named `owner` in the spec (an attestation's responsible person)
  collides with the Kubernetes `owner`; ASO's generator renames it to
  `properties_owner` (Go `PropertiesOwner`), still sent as `owner`.

## Running things: offload and the Ubuntu machine

Nothing heavy runs on the laptop: no `task check`, no envtest, no Docker, no
kind, no generator. From the repository root:

```sh
../offload/offload run --prep 'task tools' --step-skippable 'task check' --step 'task operator-e2e'
```

That is a Spot VM, where both checks run (the operator e2e too, since
2026-11-09). The Ubuntu machine is the other place, when its load is under 25:
`../offload/offload run --ubuntu -- bash -c 'task check && task operator-e2e'`.

- The Ubuntu machine is free; it keeps a kind cluster warm
  (`aso-extensions-operator`) and has go, ko, kind, helm and task. It is shared
  with other work (a Bundt mutation run, other agents): run at most one kind
  cluster at a time and never touch other clusters (`bundt-checks` and the
  other repositories' `*-operator`).
- When it is unreachable or busy (load above 25), or for any heavy run:
  `../offload/offload run --prep 'task tools' --step-skippable 'task check'
  --step 'task operator-e2e'` (a Spot VM that deletes itself; `offload ls` and
  `offload reap` if something is left). Both checks run there.
- **On a Spot VM:** the source is a tarball of tracked and
  untracked-but-not-ignored files, so `third_party/` is not there: `task
  tools` (`--prep`) fetches it and sets up envtest. Pass each task as its own
  `--step` (a wrapper that ends in `echo` hides the failure: one run "passed"
  in 0 s having run nothing). Artifacts are not copied back: `gsutil cp
  gs://hfl-dev-514205-offload/runs/<id>/offload-artifacts/...`. `task check`,
  `task coverage` and, since 2026-11-09, `task operator-e2e` run there (16
  CPUs, 62 GB): the image once had Docker 29's containerd image store, which
  could not `kind load` `postgres:17` (`ctr ... content digest ... not
  found`); the image now has that store off, and the operator e2e passes on a
  VM. When the project's CPU quota is used up, offload waits up to 20
  minutes for room (`OFFLOAD_QUOTA_WAIT`).
- The Ubuntu machine has 15 GB and is shared: a Bundt mutation run (gremlins)
  took its load near 400 and `systemd-oomd` killed a generated test package
  (`signal: killed`), and kind's etcd timed out (`etcdserver: request timed
  out`). Check `uptime` and `pgrep -fc gremlins`; go to a Spot VM when it is
  busy, and rerun a kind timeout once.
- `offload` copies the working tree (tracked or not, `third_party/` included;
  `bin/`, `.cache/` and `offload-artifacts/` are excluded and persist on the
  machine) to the same path there, runs the command, and copies
  `offload-artifacts/` back. `task check` compares generated files to the git
  index, so `git add -A` before a run.
- Run long commands in the background and wait for the notification; redirect
  their output to a file and read the end of it.
- A test that times out: rerun once before treating it as a failure. State
  the real result, failures included.
- Light local work is fine: editing, `go build ./...`, `go vet`,
  `hack/generate.sh` (controller-gen), `task --list`.

## Git and pull requests

- Branch `agent/<short-name>` from the latest `main`; one change per commit;
  `git commit -s` (DCO) with the repository's configured noreply author; the
  attribution lines the session names at the end of the message. Push the
  branch, open a pull request against `main` of this repository with `gh pr
  create`; never push to `main`, never force-push, never touch upstream
  pull requests or issues (reading them is fine).
- The description says what was built, what upstream it copies, what was
  proven against the fake cloud and what was not, and new open questions.
  It states the real results of `task check` and `task operator-e2e`, and any
  flake it found.
- `docs/coverage/` and the README's coverage section are generated by `task
  coverage` (every kind PR regenerates and commits them); the queue tables of
  `docs/coverage/foundation.md` are written by hand (mark a row built there).
- Never edit `third_party/`. A change to ASO is a numbered patch in
  `patches/azure-service-operator/` (0001 exports the framework, 0002 changes
  the generator, 0003 exports the ARM reconciler, 0004 lets the generator handle nested `x-ms-client-flatten`, 0005 gives children of a tenant scope resource an owner, 0006 makes a resource at several scopes an extension resource; 0003 also exports what hand-written extensions need); make the change in the
  checkout, `git diff` it into the patch, then `hack/upstream.sh` re-applies the
  series and `hack/upstream.sh --check` verifies it.

## Open decisions (the builder's)

From `docs/plan.md` §8 and the pull requests; none blocks the queue.
1. **One CRD version, no conversion webhook** for generated kinds: assumed,
   to revisit before the repository goes public (§2 says what the revisit
   involves: a webhook, or per-version kinds).
2. **Sharing `taskfiles/common.yml` across repositories**: a vendored copy
   with a sync script is recommended (PR #7); the builder has not decided.
3. Open questions 1 to 5 and 7 of §8: licence (MIT vs Apache-2.0), offering b1
   and b2 upstream, relaxed workload identity as the default, reading Secrets
   cluster-wide, Azure SQL Entra users (a1), the `User` kind name.
4. **May the manager write Secrets** (§8 question 9): the opt-in
   `config/secret-writer` is built (option b, recommended); whether the
   default install should grant it (option a) is the builder's.
5. **Webhook-less validation**: generated kinds lack ASO's checks (reference
   format, immutable `owner` and `azureName`). If wanted, add CEL rules by a
   generator patch; not done.

## Where to look

| What | Where |
| --- | --- |
| The rules | `CLAUDE.md`, `AGENTS.md` |
| The design and every decision | `docs/plan.md` |
| The pin, the patches, what is copied | `UPSTREAM.md` |
| The fake cloud and what it cannot show | `docs/fake-cloud.md` |
| Tasks | `Taskfile.yml`, `taskfiles/common.yml` (`task --list`) |
| Generator configuration | `generator/` |
| The manager | `pkg/manager/`, `pkg/controllers/`, `cmd/manager/` |
| Tests | `test/e2e/` (envtest), `tests/operator/` (kind) |
