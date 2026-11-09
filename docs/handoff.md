# Handoff

For an agent that takes this repository over with nothing but the
repository. Read `CLAUDE.md` first (the standing rules; `AGENTS.md` is the
same), then this file, then `docs/plan.md` for the reasons. Where this file
and the plan disagree, the plan is newer or this file is wrong: fix this
file.

State at the writing: 2026-11-09, after Recovery Services (c10 part two). The next agent takes c11 onward and the hand-written candidates below.

## What this repository is

Kubernetes kinds for Azure that Azure Service Operator v2 (ASO) does not
have, run as one manager beside ASO. Three sorts, all in groups
`<service>.azure.hfl.dev`:

| Sort | Kinds | Built by |
| --- | --- | --- |
| Data plane, hand-written | PostgreSQL `User`, `VaultSecret`, `VaultCertificate` | copied from ASO or written new; `pkg/reconcilers/` |
| ARM, hand-written | `ManagementLock` | `pkg/reconcilers/authorization/` (a lock is one type at four scopes, which ASO's generator cannot yet model) |
| ARM, generated | `app.azure.hfl.dev` Container Apps environment children; `web.azure.hfl.dev` App Service slots, host name bindings, certificates; `operationalinsights.azure.hfl.dev` Log Analytics tables, saved searches, linked services, linked storage accounts, query packs and queries; `insights.azure.hfl.dev` Monitor private link scopes; `network.azure.hfl.dev` network security perimeters; `containerregistry.azure.hfl.dev` registry tokens, scope maps, credential sets, webhooks; `compute`, `devtestlab`, `maintenance` VM extras | ASO's own generator, patched, over the pinned specs; `apis/arm/` |

ARM resources are in scope; the builder said so ("Build them or pull them
from upstream, whichever is easier", `docs/plan.md` §1, §8 Q6). Prefer, in
order: skip what ASO has; copy upstream where it exists; generate; hand-write.

## The queue, in order

The ranking is `catalog/docs/azure.md` §5 (the `highfivelabs/catalog`
repository, read-only for this one); `docs/plan.md` §3 (c) is the live table.
One pull request per gap, cut from the latest `main`.

| Rank | Gap | State |
| --- | --- | --- |
| c1, c3, c4, c5, c6, c9, c10 | locks, Container Apps environment children, App Service children, Log Analytics children, Monitor private link scopes and network security perimeters, container registry tokens and webhooks, VM extras, Recovery Services vault and policies | built |
| c7 | Key Vault keys | skip: ASO's `VaultKey` (#5564) ships in its next release; recheck |
| c11 to c14 | storage local users; Cognitive Services RAI policies; Cosmos Gremlin and SQL dedicated gateway; AKS managed namespaces | **next**, in this order |
| c8 | management groups, policy, budgets, vWAN, subscription tags | queued, low priority (platform) |
| h1 | **Hand-written candidates** (the generator cannot model them; write each as `ManagementLock` was, `pkg/reconcilers/authorization/`, on ASO's generic reconciler): **1. Maintenance `ConfigurationAssignment`** (assign a maintenance configuration to a VM or scale set; the spec has it at four scopes, so the generator's output changes from run to run); 2. Recovery Services **protection container, protected item (turns backup on for a VM) and protection intent** (a fixed `backupFabrics/Azure` segment is no resource, so the owner chain cannot name the ARM ID); 3. the Recovery Services singletons (`backupstorageconfig`, `backupconfig`, encryption config, vault extended info); 4. Log Analytics solutions (the spec's `SolutionProperties` is dropped as empty) | queued, after c11 to c14 |
| c18 | stale ARM versions (VirtualMachine, ApplicationGateway, DNS zones) | not a new kind |

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
- **A resource the spec defines at several scopes is picked at random** by the
  generator (Maintenance `configurationAssignments`: subscription, resource
  group, resource, nested resource): the output changes from run to run and
  `task check` fails. Look for it when `check` says "stale" on a kind you
  generated fine; such a kind cannot be generated (hand-write it as
  `ManagementLock` was).
- **`task operator-e2e` must print `all checks passed`.** `tests/operator/run.sh`
  was once emptied by a careless script (`open(p,'w').write(open(p).read())`
  truncates before reading) and the task still exited 0, so PR #13's kind test
  was never run; it was restored in the Recovery Services PR. Read the tail of
  the output and `wc -l tests/operator/run.sh` (about 500) before you trust it.
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

## Running things: offload and the Ubuntu machine

Nothing heavy runs on the laptop: no `task check`, no envtest, no Docker, no
kind, no generator. From the repository root:

```sh
../offload/offload run --ubuntu -- bash -c 'task check && task operator-e2e'
```

- The Ubuntu machine is free and preferred; it keeps a kind cluster warm
  (`aso-extensions-operator`) and has go, ko, kind, helm and task. It is shared
  with other work (a Bundt mutation run, other agents): run at most one kind
  cluster at a time and never touch other clusters (`bundt-checks` and the
  other repositories' `*-operator`).
- If it is unreachable: `../offload/offload run --hours 2 -- ...` (a Spot VM
  that deletes itself); `offload ls` and `offload reap` if something is left.
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
- Do not touch `docs/coverage/`, `hack/coverage/` or the README's coverage
  section: another agent owns them (branch `agent/coverage`); expect README
  conflicts.
- Never edit `third_party/`. A change to ASO is a numbered patch in
  `patches/azure-service-operator/` (0001 exports the framework, 0002 changes
  the generator, 0003 exports the ARM reconciler, 0004 lets the generator handle nested `x-ms-client-flatten`); make the change in the
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
4. **Webhook-less validation**: generated kinds lack ASO's checks (reference
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
