# Testing

How every kind here is tested, and how that grows over time. The builder,
2026-11-11: "ensure we have a testing story and we'll test everything over
time also in parallel."

## Three layers

| Layer | What it shows | Runs | Covers |
| --- | --- | --- | --- |
| 1. Conformance (`test/e2e/arm_conformance_test.go`) | a generated kind's smallest object, owned through a parent chain, becomes Ready; the fake holds a resource of the kind's ARM type at the ID ASO wrote; a change of tags is PUT again; a delete deletes (or, for a kind Azure cannot delete, leaves) | `task check`, every pull request that touches a kind | **every generated kind, automatically**: a new kind needs no test written for it to merge |
| 2. Kind tests (`test/e2e/arm_*_test.go`, `tests/operator/run.sh`) with fake rules (`pkg/fakecloud/arm/<area>.go`) | a kind's own behaviour as Azure documents it: references the service checks, keys Azure makes, children a parent's PUT keeps, refusals, what cannot be deleted | `task check`; `task operator-e2e` on kind | kinds with such behaviour, written when the kind has it (most of rows 1 to 14 have one); never needed only to merge a kind |
| 3. Live runs (`test/live`, `docs/live-runs.md`) | the kind against real Azure | by hand or on a schedule, never in `task check` | sampled over time, one ledger row per kind |

The fake cloud (`docs/fake-cloud.md`) is the test of record: `task check`
reaches no real Azure endpoint and needs no credential.

### Layer 1: conformance

The test reads `config/crds`, keeps the kinds whose Go types are in
`apis/arm` (the generator's), and for each builds the object from the CRD
schema alone: the required fields (an enum's first value, a string the
pattern accepts, a reference by ARM ID to a resource the fake holds, a
Secret or ConfigMap key the test makes), `location` and `tags` when the
kind has them, and an owner by ARM ID derived from the kind's ARM type and
scope (`Microsoft.Network/virtualHubs/routeTables` is owned by
`.../providers/Microsoft.Network/virtualHubs/p1`, which the test adds to
the fake with its parents; an extension resource by a resource group; a
tenant resource has none). Each kind runs in its own namespace, in
parallel.

When the fake's rules for a kind ask for more than the smallest object (a
managed HSM's tenant, a gallery solution's name), the kind gets an entry in
`conformanceFixtures` in the same file: fields merged over the spec, a
different owner, resources to add first, or, rarely, `skip` with the
reason. An entry is a few lines; it is the only test a new kind needs.

What it cannot show: anything the fake does not check (most properties are
kept as sent), and anything Azure does that the fake does not model
(`docs/fake-cloud.md`, "Where the fakes differ from Azure"). That is layers
2 and 3.

### Layer 2: kind tests and fake rules

Write one when a kind has behaviour beyond create, read, change and delete:
an `ARMResourceModifier` or a `KubernetesSecretExporter` in
`generator/_extensions/`, a reference the service resolves, a name that
must be a GUID, a resource Azure refuses to delete, polymorphism. The fake
rule goes in `pkg/fakecloud/arm/<area>.go` with the Azure documentation it
follows; the test names what it proves. `tests/operator/run.sh` takes one
kind per group on kind (the install and the manager's RBAC), with its `ok:`
lines in `tests/operator/required-ok.txt`.

## Live runs: real Azure, sampled

Real Azure only through the live harness (PR #54, `docs/live-runs.md`):

- **Where:** the subscription in `AZURE_SUBSCRIPTION_ID`, resource
  group `aso-extensions-live-tests` (eastus2), nothing else. The target
  list (`hack/live/targets.txt`) is a reviewed change.
- **As whom:** the app registration `aso-extensions-live-tests`
  (Contributor on that group only), through workload identity federation
  from an offload VM's Google service account: no secret, certificate or
  password exists or is accepted.
- **Opt-in:** `ASO_EXT_LIVE=create-real-azure-resources` on every run;
  the code is compiled only with `-tags live`, and `task check` only vets
  it.
- **How often:** sampled over time, not per pull request. Each run takes a
  few kinds, **cheapest first** (free resources such as settings, tags,
  policy and network manager configuration before anything billed by the
  hour; an ExpressRoute circuit or a managed HSM only when asked), sweeps
  what it made and records the result.
- **Ledger:** one row per kind in `docs/live-runs.md` (the kind, the run ID,
  the date, pass or fail and what a failure taught). A kind with no row is
  untested on real Azure, and the README says so.
- **Providers not registered:** a live test of a kind whose resource
  provider the subscription has not registered skips and names the
  provider; agents never register providers or change roles. Since
  2026-11-12 every provider the tests use is registered (register O36).
- **Actions Contributor excludes:** a test whose kind needs a
  `Microsoft.Authorization` write (a Resource Manager private link, a role
  assignment) reads the identity's permissions on the group and skips,
  naming the action, until a role grants it.
- **Scope the group cannot give:** a kind at a subscription, management
  group or tenant scope (policy definitions, Defender plans, management
  groups) needs a role the identity does not have; its live test is
  written and skips with the role it needs, until the builder decides.

## Rechecking the fake against Azure

The fake drifts from Azure as Azure changes. A periodic recheck keeps it
honest:

1. **Monthly, or when a live run fails where the fake passed:** for the
   kinds with a ledger row, the live run's requests and responses
   (`offload-artifacts/live/<run>/live.log`) are compared with what the
   fake answers for the same requests. A difference is either a fake rule
   to add (with the Azure behaviour it follows) or a line in
   `docs/fake-cloud.md`, "Where the fakes differ from Azure".
2. **When the pin moves** (a new ASO commit or specs commit): the
   conformance test runs every kind again at the new specs; a kind that
   lost its spec side or changed type fails there first.
3. The ledger's oldest rows are rerun first, so every kind is rechecked
   against Azure in turn.

## What runs where

| Command | Where | Reaches |
| --- | --- | --- |
| `task check` | an offload Spot VM or CI | the fakes only |
| `task test:pkgs PKGS=./test/e2e RUN=TestGeneratedKindsConformance` | an offload Spot VM | the fakes only (layer 1 alone, about a minute after the build) |
| `task operator-e2e` | an offload Spot VM | the fakes on kind |
| `task test-live TESTS=...` | an offload VM with the live service account | the live group only |
