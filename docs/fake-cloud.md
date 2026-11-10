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
| Resource Manager | a store of resources by ARM ID (PUT with the parent existing, GET, DELETE; the body is kept as sent with `id`, `name`, `type` and `provisioningState: Succeeded` added, no properties checked but the management group hierarchy's, Azure Policy's, Privileged Identity Management's, budgets' and cost exports' references and scopes; built-in role definitions listed by name; ASO's own ARM client, for the generated kinds, talks to it) and management locks (`Microsoft.Authorization/locks`, 2020-05-01) at any scope that exists; a lock covers its scope and everything under it: `CanNotDelete` refuses deletes (and deleting a scope that holds a locked resource), `ReadOnly` also refuses writes, each with 409 `ScopeLocked`; locks themselves can always be changed; requests carry a bearer token the test decides to accept | `pkg/fakecloud/arm`, `cmd/fakearm` |
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
- Management groups (`Microsoft.Management/managementGroups`, `.../subscriptions/<id>`,
  `.../settings/default`) are stored like any resource, plus a hierarchy: the
  tenant root (`/`) is always a valid parent; a group's `properties.details.parent.id`
  must name an existing group and not a descendant; a group with a child group
  or a subscription cannot be deleted (409); a subscription is in one group at
  a time. Not modelled: the tenant root group (a group with no parent is just at
  the root), the asynchronous create and delete Azure returns, the
  subscriptions a group holds in its own `children`, and the error codes (the
  fake's own).
- Azure Policy (`Microsoft.Authorization/policyDefinitions`,
  `policySetDefinitions`, `policyAssignments`, `policyExemptions`;
  `pkg/fakecloud/arm/policy.go`): a definition or set definition is kept only
  at a management group or a subscription (else 400); a set's definitions and
  an assignment's definition must exist and be kept at or above where they are
  used, following the management group hierarchy (a subscription placed in a
  group is under it and every group above), else 404, as for a reference not
  there yet; a built-in (`/providers/Microsoft.Authorization/policyDefinitions/x`)
  exists only when a test adds it and then serves every scope; an exemption's
  assignment must exist (404) and the exemption must be at or under its scope
  unless `assignmentScopeValidation` is `DoNotValidate` (400). Not modelled:
  evaluation and compliance (no rule is run, nothing is denied), the
  validation of `policyRule`, `mode` and parameter types and values, deletes
  Azure refuses (a definition a set still names), definition versions, an
  assignment's managed identity, and the error codes (the fake's own).
- Policy remediations and attestations (`Microsoft.PolicyInsights/remediations`,
  `attestations`; `pkg/fakecloud/arm/policyinsights.go`): a remediation is kept
  at a management group, subscription, resource group or resource, an
  attestation at a subscription, resource group or resource (400 at a group,
  as the spec has no such path); either one's assignment must exist (404) and
  be at or above it, following the management group hierarchy (400); a
  remediation of a set assignment must name the set's definition
  (`policyDefinitionReferenceId`), and its `filters.resourceIds` cannot be
  used with `ReEvaluateCompliance` (400; both from the spec's descriptions).
  Not modelled: running a remediation (the fake's is `Succeeded` at once with
  no deployments; Azure's is `Evaluating`, then `Complete` or `Failed`),
  cancelling one, whether the definition's effect can be remediated or
  attested, an attestation changing compliance, and the error codes (the
  fake's own).
- Role definitions and Privileged Identity Management (`pkg/fakecloud/arm/pim.go`):
  four built-in roles (`Owner`, `Contributor`, `Reader`, `User Access
  Administrator`, by their real IDs) exist at every scope and are listed, with
  any role definition a test stores, by `GET {scope}/providers/Microsoft.Authorization/roleDefinitions`
  (optionally `$filter=roleName eq '...'`), which is how ASO resolves
  `wellKnownName: Reader`; a role eligibility or assignment schedule request's
  name must be a GUID, its `principalId` is required, its `requestType` is one
  of Azure's (400 each), and its role must exist (404); a request is never
  deleted (405, as Azure has no DELETE for one; ASO's reconciler, knowing that from the spec, sends none); an eligibility request
  (`AdminAssign` and the like) makes the eligibility schedule of its principal,
  role and scope and names it in `targetRoleEligibilityScheduleId`, and
  `AdminRemove` needs and removes it (404); an assignment request does the same
  with assignment schedules, `SelfActivate` needing an eligibility at or above
  its scope and `SelfDeactivate` the assignment (404); every request is
  `Provisioned` (`Revoked` for a removal), and a PUT of a request that exists
  (ASO PUTs on every reconcile) keeps that outcome and applies nothing again
  (what Azure does with a second PUT of a request name is not known here); a role management policy assignment
  is named `{policy GUID}_{role GUID}` (400) and its policy and role must exist
  (404). Not modelled: approvals, justification, ticket and MFA rules (no
  request is ever `PendingApproval`), schedule start and expiry, whether the
  principal exists, the cancel and validate actions, the role management
  policies themselves (a test stores the ones it names), and the error codes
  (the fake's own; what Azure answers to a DELETE of a request is not known
  here).
- Budgets and cost exports (`Microsoft.Consumption/budgets`,
  `Microsoft.CostManagement/exports`; `pkg/fakecloud/arm/cost.go`), each rule
  from the spec's descriptions: either is kept at a subscription, resource
  group, management group or billing scope, not on a resource (400); a budget
  has at most five notifications, thresholds from 0 to 1000, contact groups
  only at a subscription or resource group and each one existing (404), a
  contact email (or there, a group) per notification, a start on the first
  of a month from 2017-06-01 and an end after it, and at least two items in
  a filter's `and` (400 each); an export's storage account by `resourceId`
  must exist (404), or it gives `storageAccount` with a `sasToken` (400 when
  neither); a `Custom` timeframe needs a `timePeriod`, periods end after they
  start, and a managed identity needs a location (400); the SAS token is kept
  and returned obfuscated, and a system-assigned identity gets a principal
  and tenant ID. Not modelled: spend and notifications, an export running
  (no run history or next run time), the rules that depend on the day (a
  budget's start at most a year ahead, a recurrence starting in the future),
  the storage subscription's registration with
  `Microsoft.CostManagementExports`, what an agreement type allows, and the
  error codes (the fake's own).
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
