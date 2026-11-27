# Foundation gap report

The work queue for the kind agents: what a platform team needs from Azure and
what neither ASO nor we cover yet. The builder, 2026-11-09: "the really
baseline, the foundational APIs of all cloud providers really need to be
covered fairly thoroughly. Everything up through a managed Kubernetes cluster
is table stakes for a platform team establishing a target. The adjacent
foundational things next to it are important for security teams. Beyond that,
it's application primitives. The OSS module space should kind of guide where we
put effort."

It has two parts. **The queue** (below, written by hand) ranks the work, a row
being one or a few kinds an agent can build in one session. **The inventory**
(generated, at the end) lists every resource of the `platform-foundation` and
`security-adjacent` categories with its state, so nothing is missing from the
report; the queue's rows are groups of its lines. The categories are in
[`priority.csv`](priority.csv) (a service's, and a resource's where it differs),
the numbers in [`categories.csv`](categories.csv) and
[`resources.csv`](resources.csv); the method is in [README.md](README.md).

**Ranking.** Foundation importance first, in the builder's own order (management
groups, subscriptions, policy, RBAC, resource groups and locks, tags, budgets and
cost, diagnostic settings, networking, Key Vault, identities, Log Analytics, AKS
and what it needs); then the number of Azure Verified Modules that need it
([`avm-need.csv`](avm-need.csv), from the catalog's `docs/azure.md`, 33 modules
read); then size, smaller first. Security-adjacent rows follow the foundation
rows.

**Upstream state** was read 2026-11-09, read-only: ASO `main` is the pin plus
three documentation commits, so what is released or on `main` is what the
inventory shows as ASO's; the open pull requests are the 15 listed by
`gh pr list` (only #5638 and #5783 add Azure kinds); issues by `gh search issues`.
Per the copy-upstream rule, a kind in an open pull request is copied from it,
and one with neither pull request nor issue is generated or written here.

**Route.** *generator*: ASO's generator, run by `hack/generate-arm.sh` from a
`generator/azure-arm/<group>.yaml` entry (`$exportAs`, `$supportedFrom`,
`$referenceType` for properties that hold resource IDs) over the pinned specs;
see `docs/plan.md`, "Generated ARM kinds". Resources in a resource group are
the plain case. An *extension resource* (created on any resource or scope)
takes an arbitrary owner, as ASO's DiagnosticSetting and RoleAssignment do. A
tenant- or subscription-scoped one has ASO's Alias and ResourceGroup as
precedent. A type that can be created at several scopes or at a management group
is one type at several paths, which ASO's generator cannot model without a design
decision (ASO issue #3756); our generator patch 0006 (2026-11-09, with Azure
Policy) makes it an extension resource with an arbitrary owner, so it is
generated too. Hand-written, on the pattern of `ManagementLock`
(`pkg/reconcilers/authorization`), is left for what the generator still cannot
do (a fixed non-resource segment, a polymorphic or empty schema).
The route in the inventory is this rule applied to the paths in the specs; the
queue's rows are my judgment where the rule is not enough.

**Size** is the attribute count the generator would have to carry (S up to 25,
M up to 80, L above). It overstates where the spec embeds a whole resource
where ASO would take a reference (`connections` has 242 attributes, mostly two
embedded gateways; a generated kind would have about 30).

## The queue

Spec paths are under `.cache/coverage/specs-a2de2d1a53d1/` (the pinned
`azure-rest-api-specs`) or `specification/` in that repository. "Version" is the
spec version to generate (the newest stable, or the newest preview when none is
stable); our generated kinds are pinned to the version they are generated from.

### Platform foundation

| # | Proposed kind(s) | Azure resources (attributes) | ASO today / ours today | What is missing | Upstream | AVMs | Route | Size |
| ---: | --- | --- | --- | --- | --- | ---: | --- | --- |
| 1 | `ManagementGroup`, `ManagementGroupSubscription`, `ManagementGroupSettings` (group `management`) | `Microsoft.Management/managementGroups` (2), `managementGroups/subscriptions` (0, a placement), `managementGroups/settings` (2); spec `management/resource-manager/Microsoft.Management/ManagementGroups/stable/2023-04-01` | none / none | all; the top of every landing zone and the parent of policy and RBAC scopes | ASO issue [#2249](https://github.com/Azure/azure-service-operator/issues/2249) open, 12 comments, no PR; not on `main` | 2 | **built 2026-11-09**: `management.azure.hfl.dev/v20230401` `ManagementGroup`, `ManagementGroupsSubscription`, `ManagementGroupsSetting`; generator, tenant owner (a `ManagementGroup` has an optional parent group; precedent ASO's `Alias`, no owner); the placement kind owns itself under a group and names a subscription ARM ID or an `Alias` | S |
| 2 | `SubscriptionTag` (`Tags`), `BillingSubscriptionAlias` | `Microsoft.Resources/tags` (1: `properties.tags{}`, extension scope), `Microsoft.Billing/billingAccounts/billingSubscriptionAliases` (17, tenant); `Microsoft.Subscription/policies` (3) | `subscription/Alias` covers `Microsoft.Subscription/aliases` / none | tags on a subscription, resource group or resource as one object; subscription creation under a billing scope (EA and MCA) | none found; ASO issue #4728 (renaming a subscription) open | 1 | **`TagSet` built 2026-11-09** (`resources.azure.hfl.dev/v20250401`); `BillingSubscriptionAlias` not built (ASO's `Alias` creates subscriptions under a billing scope); `Microsoft.Subscription/policies` built 2026-11-23, generated: `subscription.azure.hfl.dev/v20211001` `SubscriptionTenantPolicy` (a tenant singleton, `docs/plan.md` f28); generator, arbitrary owner (`Tags`); generator (`BillingSubscriptionAlias`) | S |
| 3 | `PolicyDefinition`, `PolicySetDefinition`, `PolicyAssignment`, `PolicyExemption` (group `authorization`, beside `ManagementLock`) | `Microsoft.Authorization/policyDefinitions` (22, at management group or subscription), `policySetDefinitions` (25, same), `policyAssignments` (27, extension scope), `policyExemptions` (12, extension scope); spec `resources/resource-manager/Microsoft.Authorization/policy/stable/2026-07-01` (exemptions: preview 2024-12-01-preview); later `policyDefinitions/versions` (21), `policySetDefinitions/versions` (24) | none / none | all of Azure Policy except remediation; `policyRule`, `parameters` and `policyDefinitions` are free-form JSON | ASO issues [#4975](https://github.com/Azure/azure-service-operator/issues/4975) and [#2767](https://github.com/Azure/azure-service-operator/issues/2767) open, no PR | 1 (avm-ptn-alz); every landing zone | **built 2026-11-09**, generated, not hand-written: `authorization.azure.hfl.dev/v20260701` `PolicyDefinition`, `PolicySetDefinition`, `PolicyAssignment`, `v20260101preview` `PolicyExemption`; all four extension resources (owner our `ManagementGroup`, an ASO resource or an ARM ID), the two definitions by generator patch 0006 (a resource at several scopes); not built: definition versions, variables, enrollments | M (86) |
| 4 | `PolicyRemediation` | `Microsoft.PolicyInsights/remediations` (8, subscription, resource group and extension scope); spec `policyinsights/.../PolicyInsights/stable/2024-10-01` | none / none | all | covered by #2767 (no PR) | 0 | **built 2026-11-09**, generated: `policyinsights.azure.hfl.dev/v20241001` `PolicyRemediation` (spec `stable/2024-10-01`, the newest), extension resource (patch 0006), with row 21 | S |
| 5 | `RoleEligibilityScheduleRequest`, `RoleAssignmentScheduleRequest`, `RoleManagementPolicyAssignment` (PIM, group `authorization`) | `Microsoft.Authorization/roleEligibilityScheduleRequests` (14), `roleAssignmentScheduleRequests` (15), `roleManagementPolicyAssignments` (3), all extension scope; spec `authorization/.../Authorization/stable` (the 2020-10-01 or preview 2024-09-01-preview) | `RoleAssignment`, `RoleDefinition` (7 of 7 and 8 of 8) / none | PIM eligibility and activation, role management policy; a request cannot be deleted, it is undone with another request (`AdminRemove`, `SelfDeactivate`) | **open PR [#5638](https://github.com/Azure/azure-service-operator/pull/5638)** adds `RoleEligibilityScheduleRequest` (issue #4335): copy it, then generate the other two | 0 | **built 2026-11-09**: `authorization.azure.hfl.dev/v20201001` `RoleEligibilityScheduleRequest` (configuration and extension copied from #5638, head `5d0e686e`), `RoleAssignmentScheduleRequest`, `RoleManagementPolicyAssignment` (generated the same way); extension resources; a request's GUID name is defaulted by the reconciler and recorded; not built: role management policies (PATCH only) | S (32) |
| 6 | `Budget`, `CostExport` (group `consumption`, `costmanagement`) | `Microsoft.Consumption/budgets` (26) and `Microsoft.CostManagement/exports` (28), extension scope (subscription, resource group, billing account, management group); `CostManagement/budgets` (27) is the same budget under another API; later `views` (34), `scheduledActions` (20); spec `consumption/.../Consumption/stable/2026-06-01`, `cost-management/.../CostManagement/stable/2026-06-01` | none / none | all | none found | 1 (sub-vending) | **built 2026-11-09**, generated: `consumption.azure.hfl.dev/v20260601` `Budget`, `costmanagement.azure.hfl.dev/v20260601` `Export` (named as the spec names it); extension resources (`/{scope}` in the spec), owner an ASO resource, our `ManagementGroup` or an ARM ID; the recursive query and view filters pruned by name; not built: `CostManagement/budgets`, views, scheduled actions | M each |
| 7 | `ManagementLock` completion | `Microsoft.Authorization/locks` (3) | none / `ManagementLock` covers `properties.level`, `properties.notes` | `properties.owners[].applicationId` (a legacy lock owner list) | #3756 open, a design question, no PR | 31 | **built 2026-11-09**: `owners[].applicationId` on the hand-written `ManagementLock` (spec and status), mapped in `hack/coverage/ours.yaml`; the lock's attributes are all covered | S (1) |
| 8 | `VirtualNetworkGatewayConnection`, `LocalNetworkGateway` (site-to-site VPN) | `Microsoft.Network/connections` (242 as the spec has it, about 30 as references), `localNetworkGateways` (12); spec `network/.../Network/stable/2026-03-01/networkGateway.json` | `VirtualNetworkGateway` (86 of 91; ASO v20250301) / none | the gateway's connection and the on-premises side; the gateway itself is ASO's | issue [#4486](https://github.com/Azure/azure-service-operator/issues/4486) open, no PR | 0 | **built 2026-11-09**, generated: `network.azure.hfl.dev/v20260301` `VirtualNetworkGatewayConnection`, `LocalNetworkGateway` (spec 2026-03-01); the gateways in a connection are references (ASO's `VirtualNetworkGateway` by ARM ID, our `LocalNetworkGateway` by object), `SubResource.Id` labelled `$referenceType: arm`, the shared key and authorization key Secret references; not built: the `sharedkey` singleton child and the connection actions | M |
| 9 | `ExpressRouteCircuit`, `ExpressRouteCircuitPeering`, `ExpressRouteCircuitAuthorization`, `ExpressRouteGateway`, `ExpressRouteConnection`, `ExpressRoutePort` | `expressRouteCircuits` (72), `expressRouteCircuits/peerings` (44), `.../authorizations` (2), `expressRouteGateways` (24), `expressRouteGateways/expressRouteConnections` (16), `ExpressRoutePorts` (15), `expressRouteCrossConnections` (35) and `/peerings` (28); spec `.../Network/stable/2026-03-01/expressRoute.json` | none / none | all | issue [#4371](https://github.com/Azure/azure-service-operator/issues/4371) open (`cx-request`), no PR | 0 | **built 2026-11-09** (circuits and ports, PR #28; gateways, the PR after), generated: `network.azure.hfl.dev/v20260301` `ExpressRouteCircuit`, `ExpressRouteCircuitsPeering`, `ExpressRouteCircuitsAuthorization`, `ExpressRouteCircuitsPeeringsConnection` (Global Reach), `ExpressRoutePort`, `ExpressRoutePortsAuthorization`; hand-written extensions keep a circuit's peerings and authorizations and a peering's connections on the parent's PUT (ASO's `RouteTable` code, copied) and export the keys Azure makes (service key, authorization keys) to Secrets; input keys are Secret references; `ExpressRouteGateway` (in a Virtual WAN hub: our `VirtualHub` by object since row 10, or an ARM ID; its PUT keeps its connections) and `ExpressRouteGatewaysExpressRouteConnection` (to a circuit's private peering by object or ARM ID, a cross-subscription authorization key from a Secret), from `virtualWan.json`, the connection by generator patch 0007 (the 2026-03-01 network specs no longer mark `SubResource` as a resource). Not built: cross-connections (the connectivity provider's side; no DELETE, and the generator does not take them as resources) | L (about 200) |
| 10 | `VirtualWan`, `VirtualHub`, `VirtualHubConnection` (`hubVirtualNetworkConnections`), `HubRouteTable`, `VirtualHubRouteTable`, `RoutingIntent`, `BgpConnection`, `VirtualHubIpConfiguration`; then `VpnGateway`, `VpnConnection`, `VpnSite`, `VpnServerConfiguration`, `P2sVpnGateway`, `VpnGatewayNatRule` | `virtualWans` (6), `virtualHubs` (28), `virtualHubs/hubVirtualNetworkConnections` (16), `hubRouteTables` (6), `routeTables` (5), `routingIntent` (3), `bgpConnections` (13), `ipConfigurations` (113, mostly embedded), `vpnGateways` (78), `vpnGateways/vpnConnections` (56), `vpnSites` (28), `vpnServerConfigurations` (36), `p2svpnGateways` (24); spec `.../virtualWan.json` | none / none | hubs and Virtual WAN, VPN gateways | none found | 1 (hubVirtualNetworkConnections, avm-ptn-alz-sub-vending) | **WAN and hub built 2026-11-09**, generated: `network.azure.hfl.dev/v20260301` `VirtualWan`, `VirtualHub` (in a WAN by object or ARM ID; its PUT keeps its route tables, the circuit's `ARMResourceModifier` pattern), `VirtualHubsHubVirtualNetworkConnection`, `VirtualHubsHubRouteTable`, `VirtualHubsRouteTable`, `VirtualHubsRoutingIntent`, `VirtualHubsBgpConnection`, `VirtualHubsIpConfiguration` (the connection and `routeTables` by patch 0007); route next hops, BGP peers' connections and IP configurations' subnets are references. **VPN half built 2026-11-09**, generated: `VpnGateway` (in our hub by object; its PUT keeps its connections and NAT rules), `VpnGatewaysVpnConnection` (to our `VpnSite` by object; its link connections embedded, each link connection's `sharedKey` a Secret reference, NAT rules by reference), `VpnGatewaysNatRule`, `VpnSite` (in our WAN; links embedded; `siteKey` a Secret reference), `VpnServerConfiguration` (policy groups embedded; the RADIUS secrets Secret references), `P2sVpnGateway` (in our hub, with our server configuration). Not built: hub `routeMaps` and `connectionPolicies` (not in the queue), the link connections' `sharedKeys/default` singleton, server configuration policy groups as their own kind (kept in the configuration's spec), the connection's deprecated `sharedKey` (removed), and the POST actions (reset, packet capture, VPN profiles) | L (about 400, less as references); split into two sessions: virtual WAN and hub, then VPN (both done) |
| 11 | `NetworkManager` and children (connectivity, security admin, routing configurations, network groups, IPAM pools) | `networkManagers` (6) and 19 child types (about 90); `networkManagerConnections` (2, subscription or management group scope) | none / none | all | none found | 0 | **first part built 2026-11-10**, generated: `network.azure.hfl.dev/v20260301` `NetworkManager`, `NetworkManagerConnection` (at a subscription or management group: an extension resource with an arbitrary owner, patch 0006), `NetworkManagersScopeConnection`, `NetworkManagersNetworkGroup`, `NetworkManagersNetworkGroupsStaticMember` (spec 2026-03-01, which needs no generator patch). **Second part built 2026-11-10**, generated: `NetworkManagersConnectivityConfiguration`, the security admin configuration, rule collection and rule (polymorphic: `adminRule` or `defaultAdminRule`, by `$resourceEmbeddedInParent` on the leaves), and the security user configuration, rule collection and rule. **Third part built 2026-11-10**, generated: `NetworkManagersRoutingConfiguration`, `NetworkManagersRoutingRuleCollection`, `NetworkManagersRoutingRule` and `NetworkManagersCommit`. Not in this row's plan: IPAM pools, verifier workspaces | L; split into three pull requests |
| 12 | `DnsZonesSOARecord`, `PrivateDnsZonesSOARecord`, `IpGroup`, `RouteFilter`, `ServiceEndpointPolicy`, `DnsResolverPolicy` (+ links, security rules) | `dnsZones/SOA` (26), `privateDnsZones/SOA` (21), `ipGroups` (3), `routeFilters` (8), `serviceEndpointPolicies` (9), `dnsResolverPolicies` (3) | none / none | the leftovers of DNS, firewall and routing | none found | 0 | generator | M together |
| 13 | `ManagedHsm` (+ keys), `VaultAccessPolicy`, `VaultSecret` (ARM) | `Microsoft.KeyVault/managedHSMs` (20), `managedHSMs/keys` (15), `vaults/accessPolicies` (7), `vaults/secrets` (6), `vaults/privateEndpointConnections` (5); spec `keyvault/.../KeyVault/stable/2026-05-15` | `Vault`, `VaultKey` (28 of 28, 15 of 15) / `VaultSecret` and `VaultCertificate` (data plane) | managed HSMs and the ARM-side secret | secrets declined (#1894); none for the rest | 4 (vaults), 5 (keys) | **built 2026-11-10**, generated: `keyvault.azure.hfl.dev/v20260515` `ManagedHSM`, `ManagedHSMsKey` (no DELETE in the spec: detach-on-delete; a PUT does not change an existing key), `ManagedHSMsPrivateEndpointConnection`, `VaultsPrivateEndpointConnection` (on ASO's `Vault`); not built: `vaults/accessPolicies` (declined upstream, #2940: the `{operationKind}` path), ARM `vaults/secrets` (for ARM deployments only, no DELETE; our data-plane `VaultSecret` covers it) | M |
| 14 | `OperationsManagementSolution`, `WorkspacesDataSource`, `WorkspacesStorageInsightConfig`, `LogAnalyticsCluster` | `OperationsManagement/solutions` (9, preview 2015-11-01-preview), `OperationalInsights/workspaces/dataSources` (4), `storageInsightConfigs` (6), `clusters` (18) | `Workspace` (20 of 23) / tables, data exports, linked services, linked storage, saved searches, query packs | solutions; the rest of a workspace | none found | 1 (solutions) | **first part built 2026-11-10**, generated: `operationalinsights.azure.hfl.dev/v20250701` `Cluster`, `WorkspacesDataSource` (generator patch 0008), `WorkspacesStorageInsightConfig` (the key a Secret reference). **Solutions built 2026-11-10**, generated: `operationsmanagement.azure.hfl.dev/v20151101preview` `Solution`, owned by a resource group, the workspace ASO's `Workspace` by object or an ARM ID (the spec marks `SolutionProperties` `x-ms-azure-resource`, which emptied it; `$resourceEmbeddedInParent: Solution` keeps it, no generator patch). | M |
| 15 | `ManagedClustersManagedNamespace`, `FleetsManagedNamespace`, `ManagedClustersPrivateEndpointConnection`, `ManagedClustersSnapshot` | `managedClusters/managedNamespaces` (12), `fleets/managedNamespaces` (26), `managedClusters/privateEndpointConnections` (3), `snapshots` (4); spec `containerservice/.../aks/stable/2026-08-01/managedClusters.json` | `ManagedCluster` (309 of 330, ASO 2026-05-01), `ManagedClustersAgentPool` (123 of 126), fleets (partly), maintenance, trusted access, identity bindings / none | managed namespaces; the missing cluster attributes are API-version lag (spec 2026-08-01) | none found | 1 (managed namespaces) | generator | M |
| 16 | `SourceControlConfiguration`, `KubernetesConfigurationPrivateLinkScope` (+ endpoint connection) | `KubernetesConfiguration/sourceControlConfigurations` (11, legacy Flux v1, extension scope), `privateLinkScopes` (4) + `privateEndpointConnections` (4) | `Extension` (19 of 32), `FluxConfiguration` (70 of 71) / none | the legacy GitOps kind; `Extension` is behind the spec | none found | 0 | generator, arbitrary owner | S |
| 17 | `RegistriesTask`, `RegistriesConnectedRegistry`, `RegistriesPrivateEndpointConnection` | `registries/tasks` (54), `connectedRegistries` (13), `privateEndpointConnections` (4); spec `containerregistry/.../RegistryTasks/stable/2019-04-01` | `Registry`, replications, cache rules (ASO); credential sets, scope maps, tokens, webhooks (ours) | tasks and connected registries | none found | 0 | **built 2026-11-10**, generated: `containerregistry.azure.hfl.dev/v20190401` `RegistriesTask` (tokens and credentials Secret references), `v20251101` `RegistriesConnectedRegistry` (its tokens our `RegistriesToken` by reference), `RegistriesPrivateEndpointConnection`; owned by ASO's `Registry` | M |
| 18 | Insights API lag: `MetricAlert`, `ScheduledQueryRule`, `DataCollectionEndpoint`, `ActivityLogAlert`, `Workbook`; `LogProfile`, `WorkbookTemplate` | `metricAlerts` (9 missing of 41), `scheduledQueryRules` (9 of 39), `dataCollectionEndpoints` (4 of 16) and others, `logprofiles` (8, subscription), `workbooktemplates` (16) | ASO has the kinds, at older API versions / none | newer properties; two more kinds | none found; belongs upstream (`$supportedFrom` a newer version) | 1 (metric alerts) | upstream; here only `LogProfile`, `WorkbookTemplate` by generator | S |

Already covered (nothing to build): virtual networks, subnets, peerings
(`VirtualNetwork`, `VirtualNetworksSubnet`, `VirtualNetworksVirtualNetworkPeering`), route
tables, NSGs and rules, NAT gateways, public IPs and prefixes, Azure Firewall
and firewall policies and rule collection groups, private endpoints and their
DNS zone groups, private DNS zones and links and records, DNS zones and records,
DNS resolvers, bastions, virtual network gateways, flow logs, managed identities
and federated credentials, role assignments and definitions, Key Vault and its
keys, diagnostic settings, data collection rules, action groups, Log Analytics
workspace, AKS cluster, node pools, maintenance configurations, Flux
configurations, registries, resource groups (ASO at 2020-06-01 against the spec's
2025-04-01; the one missing "attribute" is an empty `properties`). Deny
assignments are not a gap: `Microsoft.Authorization/denyAssignments` has no PUT
in the ARM specs (it is created by Azure Blueprints and managed applications).

### Security-adjacent

| # | Proposed kind(s) | Azure resources (attributes) | ASO today / ours today | What is missing | Upstream | AVMs | Route | Size |
| ---: | --- | --- | --- | --- | --- | ---: | --- | --- |
| 19 | `DefenderForStorageSetting` | `Microsoft.Security/defenderForStorageSettings` (10); spec `security/.../Security/stable/2025-06-01` | none / none | all | **open PR [#5783](https://github.com/Azure/azure-service-operator/pull/5783)** (issue #5759) adds `security.azure.com/v20250601` `DefenderForStorageSetting`: copy it | 0 | **built 2026-11-11**, generated: `security.azure.hfl.dev/v20250601` `DefenderForStorageSetting`, configuration copied from #5783 (head `d6842ab7`), its `/{resourceId}/` scope by generator patch 0010; an extension resource on a storage account | S |
| 20 | `SecurityPricing`, `SecurityContact`, `SecurityAutoProvisioningSetting`, `SecuritySetting`, `SecurityWorkspaceSetting`, `AdvancedThreatProtectionSetting` (group `security`) | `pricings` (8, the Defender plans per subscription), `securityContacts` (8), `autoProvisioningSettings` (1), `settings` (2), `workspaceSettings` (2), `advancedThreatProtectionSettings` (1); spec `security/.../Security/stable/2026-07-01/pricings.json`, `preview/2023-12-01-preview` (contacts) | none / none | all; the builder's "Defender for Cloud settings and pricings, security contacts" | none found | 0 | **built 2026-11-11**, generated: `security.azure.hfl.dev` `Pricing` (v20260701), `SecurityContact` (v20231201preview), `AutoProvisioningSetting`, `WorkspaceSetting` (v20170801preview), `Setting` (v20220501, polymorphic: `dataExport` or `alertSync`), `AdvancedThreatProtectionSetting` (v20190101), `ServerVulnerabilityAssessmentsSetting` (v20230501); extension resources owned by a subscription's ARM ID (or any resource), by generator patch 0010 | S (22) |
| 21 | `PolicyAttestation` | `Microsoft.PolicyInsights/attestations` (10, several scopes) | none / none | all | none found | 0 | **built 2026-11-09** with row 4: `policyinsights.azure.hfl.dev/v20241001` `PolicyAttestation`, extension resource (patch 0006) | S |
| 22 | Sentinel: `SentinelOnboardingState`, `SentinelSetting`, `AlertRule`, `AutomationRule`, `DataConnector`, `Watchlist` (+ items) first | `Microsoft.SecurityInsights/*`: 32 resources, 393 attributes; spec `securityinsights/.../SecurityInsights/stable/2025-09-01` | none / none | all | none found | 0 | **built 2026-11-11**, generated: `securityinsights.azure.hfl.dev/v20250901` `OnboardingState`, `AlertRule` (Fusion, Microsoft security incident creation, scheduled), `AlertRulesAction`, `AutomationRule`, `DataConnector` (eleven connector kinds), `DataConnectorDefinition`, `ContentPackage`, `ContentTemplate`, `SecurityMLAnalyticsSetting`, `Watchlist`, `WatchlistsWatchlistItem`; extension resources on a workspace, their children by generator patch 0012. Not built: incidents, bookmarks, threat intelligence, source controls (operational data) | L (393), start with 6 kinds of about 50 |
| 23 | `SecurityStandard`, `StandardAssignment`, `SecurityAutomation`, `SecurityConnector` (+ devops children) | `securityStandards` (6), `standardAssignments` (13), `automations` (20), `securityConnectors` (78) with 4 devops child types (33) | none / none | all | none found | 0 | **built 2026-11-11, 2026-11-17**, generated: `security.azure.hfl.dev` `SecurityStandard`, `StandardAssignment`, `CustomRecommendation` (v20240801), `Automation` (v20231201preview, in a resource group; actions name a Logic App, an Event Hub or a workspace by reference), `SecurityConnector` (v20260801preview), `SecurityConnectorsDevOpsConfiguration`, `SecurityConnectorsDevOpsAzureDevOpsOrg`, `SecurityConnectorsDevOpsAzureDevOpsProject`, `SecurityConnectorsDevOpsAzureDevOpsRepo` (v20250301; org/project/repo have no ARM DELETE) | M |
| 24 | `PurviewAccount`, `HsmCloudHsmCluster`, `HsmDedicatedHsm`, `NetworkDdosCustomPolicy`, `NetworkSecurityPartnerProvider`, `CdnWebApplicationFirewallPolicy` | `Purview/accounts` (9), `HardwareSecurityModules/cloudHsmClusters` (9) and `dedicatedHSMs`, `Network/ddosCustomPolicies` (16), `securityPartnerProviders` (4), `Cdn/cdnWebApplicationFirewallPolicies` | none / none | all | none found | 0 | **built 2026-11-11, 2026-11-17**, generated: `purview.azure.hfl.dev/v20211201` `Account`, `AccountsKafkaConfiguration`, `AccountsPrivateEndpointConnection`; `hardwaresecuritymodules.azure.hfl.dev/v20250331` `CloudHsmCluster`, `CloudHsmClustersPrivateEndpointConnection`, `DedicatedHsm`; `network.azure.hfl.dev/v20260301` `DdosCustomPolicy`, `SecurityPartnerProvider`; `cdn.azure.hfl.dev/v20261001` `CdnWebApplicationFirewallPolicy` | S each |

Covered already in this category: ASO has the WAF policies of Application Gateway
and Front Door (`network/WebApplicationFirewallPolicy`,
`network.frontdoor/WebApplicationFirewallPolicy`) and `network/DdosProtectionPlan`;
we have the network security perimeter and its five child kinds and the Azure CDN
WAF policy (`cdn/CdnWebApplicationFirewallPolicy`).

### Tier 3: application primitives

Ranked 2026-11-24, the way the two tables above are: the services in the
builder's order (the tier 3 rows of `priority.csv`, which the builder listed as
application primitives), then, inside a service, what an application needs
before what it may add, smaller first. No Azure Verified Module of the 33 read
needs a tier 3 service (that is what tier 3 means), so the AVM column is
replaced by whether Terraform's `azurerm` has a resource for it (the open
source module space, `inputs/terraform/terraform-resources.csv`; a heuristic
mapping, a tie-break only). Left out of every row: preview-only resources, the
retired products (Azure Database for MySQL single server, the `servers`
types; Batch account certificates; Logic Apps integration service
environments), and requests that start an operation rather than describe a
resource (migrations, on-demand backups, dry runs). Upstream was read
2026-11-24 (git, blob-less): ASO `main` `f451aaa`, release v2.22.0, open pull
request heads #5780 to #5857; none adds a kind to these services (#5805
refreshes the specs and renames a `cdn` type, #5843 adds `botservice`).
Each row is rechecked when it is built and recorded in `docs/plan.md` §3 (f61
on). "Live" says whether a live test can be cheap.

| # | Service: proposed kinds | Azure resources (attributes) | ASO today | Not proposed, and why | `azurerm` | Live | Size |
| ---: | --- | --- | --- | --- | --- | --- | --- |
| t1 | Service Bus: `NamespacesNetworkRuleSet`, `NamespacesPrivateEndpointConnection`, `NamespacesDisasterRecoveryConfig` (**built 2026-11-24**, `docs/plan.md` f61) | `namespaces/networkRuleSets` (7), `privateEndpointConnections` (4), `disasterRecoveryConfigs` (2); 2026-01-01 | `Namespace`, queues, topics, subscriptions, rules, authorization rules | `ipfilterrules`, `virtualnetworkrules` (2018 preview only, replaced by the network rule set); `migrationConfigurations` (starts a Standard to Premium migration) | disaster recovery | the rule set and connection on one namespace; geo pairing needs two Premium namespaces | S |
| t2 | Event Hubs: `NamespacesNetworkRuleSet`, `NamespacesPrivateEndpointConnection`, `NamespacesApplicationGroup`, `NamespacesSchemaGroup`, `NamespacesDisasterRecoveryConfig`, `Cluster` (**built 2026-11-24**, `docs/plan.md` f62) | `namespaces/networkRuleSets` (7), `privateEndpointConnections` (4), `applicationGroups` (6), `schemagroups` (3), `disasterRecoveryConfigs` (2), `clusters` (7); 2026-01-01 | `Namespace`, event hubs, consumer groups, authorization rules | `ipfilterrules`, `virtualnetworkrules` (as t1) | clusters, disaster recovery | a Standard namespace for most (application groups and geo pairing may need Premium); not the dedicated cluster (billed by the hour, with a minimum) | S |
| t3 | Event Grid: `SystemTopic`, `SystemTopicsEventSubscription`, `NamespacesTopicsEventSubscription`, the MQTT broker's `NamespacesClient`, `ClientGroup`, `TopicSpace`, `PermissionBinding`, `CaCertificate` (**built 2026-11-24**, `docs/plan.md` f63); private endpoint connections of topics, domains and namespaces (not generated: the path's parent type is a parameter) | `systemTopics` (9) and its `eventSubscriptions` (38), `namespaces/topics/eventSubscriptions` (39), five namespace children (16), three `privateEndpointConnections` (18); 2025-02-15 | `Topic`, `Domain`, `DomainsTopic`, `Namespace`, `NamespaceTopic`, and `EventSubscription` as an extension resource | `topics`, `domains`, `domains/topics/eventSubscriptions`: ASO's extension `EventSubscription` on the topic or domain makes the same subscription; the partner resources (`partnerRegistrations`, `partnerNamespaces` and channels, `partnerConfigurations`, `partnerTopics`): a SaaS partner's side, row t3b after this (**t3b built 2026-11-24**, `docs/plan.md` f77) | system topics and their subscriptions | a system topic on a storage account, free until events flow | M |
| t4 | API Management: `ServiceApiOperation` (+ its policy and tags), API schemas, tag descriptions, resolvers, tags and links, `ServiceWorkspace` and its children, identity and OpenID Connect providers, authorization servers, caches, global schemas (**first part built 2026-11-24**, `docs/plan.md` f64: operations, their policies and tags, schemas, tag descriptions, tags, providers, authorization servers, caches; **second part built 2026-11-24**, f71: resolvers and their policies, tag and product links, releases, wikis, documentations, policy restrictions, private endpoint connections, the v2 gateways and their configuration connections; **third part built 2026-11-24**, f72: workspaces and their children, not their notifications) | `service/apis/operations` (94) and about 70 other stable types (about 700); 2024-05-01 | `Service`, APIs, products, policies, backends, named values, loggers, diagnostics, gateways, subscriptions, users | `aigateways` and children (2026-11-01, new; a row of its own after the service's); the developer portal's content (`portalRevisions`, `contentTypes`, `templates`); `apis/issues` and their comments (user content) | most | a Consumption service (billed by call) for the service's children; workspaces need a Premium service (not live) | L, in several rows |
| t5 | Azure Front Door and CDN: `ProfilesEndpointsOriginGroup`, `ProfilesEndpointsOrigin`, `ProfilesEndpointsCustomDomain`; `EdgeAction` (+ versions, execution filters) (**not built**, `docs/plan.md` f65: ASO's endpoint holds its origins inline; the custom domain is the retiring classic product's; an edge action's code is deployed by a POST) | `profiles/endpoints/originGroups` (10), `origins` (11), `customDomains` (1); `edgeActions` (14 with children); 2026-10-01 | the Front Door Standard and Premium kinds, `Profile`, `ProfilesEndpoint` | none, but the endpoint children belong to Azure CDN Standard from Microsoft (classic), which Microsoft is retiring: they rank last in the row | custom domains | edge actions on a Front Door Standard profile (billed by the month, prorated); the classic children only if the subscription may still make a classic profile | S |
| t6 | MySQL flexible server: `FlexibleServersAdvancedThreatProtectionSetting`, `FlexibleServersPrivateEndpointConnection` (**built 2026-11-24**, `docs/plan.md` f66) | `flexibleServers/advancedThreatProtectionSettings` (1), `privateEndpointConnections` (4); 2024-12-30 | `FlexibleServer`, administrators, configurations, databases, firewall rules | the single server types (`servers/*`, retired 2024-09-16); `backups`, `backupsV2` (on-demand backups, operations) | none | a Burstable B1ms server (cents an hour) | S |
| t7 | Container Instances: `ContainerGroupProfile`, `NGroup`, `SandboxGroup` (**built 2026-11-24**, `docs/plan.md` f67) | `containerGroupProfiles` (124), `ngroups` (37), `sandboxGroups` (5); 2026-07-01 | `ContainerGroup` | none | none | a profile is a template (free); an NGroup of zero groups | M |
| t8 | Web PubSub and SignalR: `WebPubSub`, `WebPubSubHub`, `WebPubSubReplica`, `WebPubSubCustomDomain`, `WebPubSubCustomCertificate`, shared private link resources and private endpoint connections of both (**built 2026-11-24**, `docs/plan.md` f68) | `webPubSub` (28), `webPubSub/hubs` (13), `replicas` (7), `customDomains` (2), `customCertificates` (3), six `sharedPrivateLinkResources` and `privateEndpointConnections` types (20); 2024-03-01 | `SignalR`, its replicas, custom domains and certificates | none | shared private links | a Free_F1 Web PubSub with a hub (free) | M |
| t9 | Data Explorer: `ClustersAttachedDatabaseConfiguration`, `ClustersDatabasesScript`, `ClustersManagedPrivateEndpoint`, `ClustersPrivateEndpointConnection`, `ClustersSandboxCustomImage` (**built 2026-11-24**, `docs/plan.md` f69) | (32); 2025-02-14 | `Cluster`, databases, data connections, principal assignments | none | three | a Dev (no SLA) cluster, billed by the hour and slow to make: written, run rarely | S |
| t10 | Data Factory: `FactoriesManagedVirtualNetwork` (+ managed private endpoints), `FactoriesCredential`, `FactoriesGlobalParameter`, `FactoriesPrivateEndpointConnection`, then pipelines, datasets, linked services, triggers, data flows, integration runtimes, change data capture (**first part built 2026-11-24**, `docs/plan.md` f78; the polymorphic kinds overflow ASO's generator; f84 fixed the two overflows, generator patches 0018 and 0019, and found the linked service union's CRD 2.67 MB, over the limit; **second part built 2026-11-24**, f86: triggers, data flows, integration runtimes, change data capture; datasets and pipelines measured over the limit too: how to split linked services, datasets and pipelines is open question 17 in `docs/plan.md`) | `factories/*` (282 in the spec's count; the linked service, dataset and activity unions have hundreds of subtypes, which may not fit a CRD); 2018-06-01 | `Factory` | none yet; the polymorphic types are tried with the generator first and split if a CRD exceeds the size limit | most | definitions are free (Data Factory bills runs) | L |
| t11 | Synapse: firewall rules, Entra admins and Entra-only authentication, keys and encryption protector, private link hubs and endpoint connections, auditing and security settings, SQL pools and their children, integration runtimes (**size checked 2026-11-24**, `docs/plan.md` f79: the integration runtime's CRD is 50 KB; **first part built 2026-11-24**, f80: the workspace's children but SQL pools, and private link hubs; **SQL pools built 2026-11-24**, f81, but their sensitivity labels and assessment baselines, whose paths run through no resource) | `workspaces/*` (233 stable); 2021-06-01 | `Workspace`, `WorkspacesBigDataPool` | the Data Explorer pools (`kustoPools`, 2021-06-01-preview only); `sqlDatabases` (preview only) | none | workspace children on a workspace with no pool (storage cents); SQL pools by the hour (not live) | L |
| t12 | Machine Learning: datastores, code, component, data, environment and model containers and versions (workspace and registry), online and batch endpoints and deployments, serverless endpoints, outbound rules, private endpoint connections, then jobs and schedules (**size not measured 2026-11-24**, `docs/plan.md` f79: the job unions need labels and the datastore secrets a transformer first; **first part built 2026-11-24**, f82: containers and versions of workspaces and registries, datastores, outbound rules, connections; **second part built 2026-11-24**, f83: online, batch and serverless endpoints and deployments, feature sets and entities, capability hosts; **jobs and schedules built 2026-11-24**, f85, the schedule's CRD without descriptions to fit a client-side apply) | `workspaces/*`, `registries/*` (about 960); 2026-09-01 | `Workspace`, `Registry`, `WorkspacesCompute`, `WorkspacesConnection` | `labelingJobs` (2020 preview only); `marketplaceSubscriptions` (a purchase) | none mapped | containers and versions on a workspace (storage cents); endpoints bill compute | L |
| t13 | Data Protection: `ResourceGuard`, `BackupVaultsBackupResourceGuardProxy` (**built 2026-11-24**, `docs/plan.md` f70) | `resourceGuards` (4), `backupVaults/backupResourceGuardProxies` (5); 2026-07-01 | `BackupVault`, backup instances and policies | none | none | free (a guard and an empty vault) | S |
| t14 | Batch: `BatchAccountsApplication` (+ versions), `BatchAccountsPool` (**built 2026-11-24**, `docs/plan.md` f73: applications and pools; not the versions) | `batchAccounts/applications` (4), `versions` (2), `pools` (154); 2025-06-01 | `BatchAccount` | `certificates` (Batch account certificates, retired 2024-02-29) | none | an account and a pool of zero nodes (free) | M |
| t15 | Logic Apps: `Workflow`; `IntegrationAccount` and its partners, agreements, schemas, maps, certificates, assemblies, batch configurations, sessions (**built 2026-11-24**, `docs/plan.md` f74) | `workflows` (32), `integrationAccounts` and eight children (628); 2019-05-01 | none | `integrationServiceEnvironments` and their managed APIs (retired 2024-08-31); `automationProjects` (preview only) | none mapped | a Consumption workflow that never runs and a Free integration account (free) | M (agreements L) |
| t16 | IoT: `IotHubsCertificate`, `IotHubsEventHubEndpointsConsumerGroup`, `IotHubsPrivateEndpointConnection`; Device Provisioning: `ProvisioningService` (+ certificates, endpoint connections) (**built 2026-11-24**, `docs/plan.md` f75, but the consumer groups: their `eventHubEndpoints` segment is no resource) | (13) and `provisioningServices` (41); 2023-06-30 and 2026-11-01 | `IotHub` | none | none mapped | a Free F1 hub (one a subscription) and an S1 provisioning service (cents) | S |
| t17 | Notification Hubs: `NamespacesPrivateEndpointConnection` (**built 2026-11-24**, `docs/plan.md` f76) | `namespaces/privateEndpointConnections` (3); 2023-09-01 | the namespace, hubs and authorization rules | none | none | a connection needs a Standard namespace (about $200 a month): not live | S |
| t18 | Service Connector | `locations/connectors` (47), `dryruns` (48) | `Linker` | everything: connectors live under a location segment, which no generated kind can own (as f59's SQL trust groups), and dry runs are operations | | | |
| t19 | Azure Red Hat OpenShift | | the four kinds | nothing uncovered but six status attributes | | | |
| t20 | Billing | 17 types (181) | none | everything, for the builder: billing accounts, profiles, invoice sections, policies and billing role assignments change who pays and who may buy; `billingSubscriptionAliases` is foundation row 2 | | | |
| t21 | The 225 services `priority.csv` does not name (tier 3 by default, category `other`) | 1,262 resources | | not ranked one by one: third-party services (`PaloAltoNetworks.*`, `Oracle.*`, `Nginx.*`, `Dynatrace.*`), preview-only and retired ones come last; among the rest, those with the most `azurerm` resources ASO lacks first (`Microsoft.NetApp`, `Microsoft.AppPlatform` (retiring), `Microsoft.StreamAnalytics`, `Microsoft.DesktopVirtualization`, `Microsoft.DevCenter`, `Microsoft.Chaos`, `Microsoft.Databricks`, `Microsoft.HealthcareApis`, `Microsoft.SqlVirtualMachine`, `Microsoft.StorageSync`, `Microsoft.Relay`, `Microsoft.Dashboard`, `Microsoft.LoadTestService`), ranked when t1 to t17 are done | | | |

## The inventory

Generated: every resource of the two categories, by the order of the services in `priority.csv`, then no kind before partly covered before covered, then AVMs descending, then size. Status `partial` is attributes of the spec not covered (an API version behind, or a property ASO leaves out); `Attributes` is official / ours beyond / uncovered. `AVMs` is how many of the 33 modules read need the resource (`avm-need.csv`; 0 when none does).

<!-- foundation:start -->

| Category | Services | Resources | Attributes | Official | Ours beyond | Uncovered |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| platform-foundation | 18 | 246 | 4,675 | 43.2% | 39.3% | 17.5% |
| security-adjacent | 7 | 93 | 1,049 | 8.6% | 60.3% | 31.1% |
| application | 35 | 823 | 14,821 | 33.1% | 40.3% | 26.6% |
| other | 230 | 1,262 | 22,824 | 0.5% | 2.3% | 97.3% |

### platform-foundation: 246 resources (23 with no kind, 81 partly covered, 142 covered)

| # | Resource | Status | ASO | Ours | Attributes: official / ours / uncovered | Scope | Route | Size | AVMs |
| ---: | --- | --- | --- | --- | --- | --- | --- | --- | ---: |
| 1 | `Microsoft.Management/managementGroups` | covered |  | `management/ManagementGroup` | 0 / 2 / 0 | tenant | - | - | 2 |
| 2 | `Microsoft.Management/managementGroups/subscriptions` | covered |  | `management/ManagementGroupsSubscription` | 0 / 0 / 0 | tenant | - | - | 1 |
| 3 | `Microsoft.Management/managementGroups/settings` | covered |  | `management/ManagementGroupsSetting` | 0 / 2 / 0 | tenant | - | - | 0 |
| 4 | `Microsoft.Management/serviceGroups` | covered |  | `management/ServiceGroup` | 0 / 5 / 0 | tenant | - | - | 0 |
| 5 | `Microsoft.Subscription/subscriptionDefinitions` | no kind |  |  | 0 / 0 / 3 | tenant | generator, tenant or subscription owner (as ASO's Alias) | S (3) | 0 |
| 6 | `Microsoft.Subscription/aliases` | covered | `subscription/Alias` |  | 9 / 0 / 0 | tenant | - | - | 1 |
| 7 | `Microsoft.Subscription/policies` | covered |  | `subscription/SubscriptionTenantPolicy` | 0 / 3 / 0 | tenant | - | - | 0 |
| 8 | `Microsoft.Authorization/privilegedResourceFavorites` | no kind |  |  | 0 / 0 / 1 | tenant | generator, tenant or subscription owner (as ASO's Alias) | S (1) | 0 |
| 9 | `Microsoft.Authorization/variables/values` | no kind |  |  | 0 / 0 / 2 | managementGroup,subscription | generator, arbitrary owner (several scopes, patch 0006) | S (2) | 0 |
| 10 | `Microsoft.Authorization/roleAssignmentApprovals/stages` | no kind |  |  | 0 / 0 / 3 | extension,tenant | generator, arbitrary owner (several scopes, patch 0006) | S (3) | 0 |
| 11 | `Microsoft.Authorization/accessReviewScheduleDefinitions/instances` | no kind |  |  | 0 / 0 / 4 | extension,subscription | generator, arbitrary owner (several scopes, patch 0006) | S (4) | 0 |
| 12 | `Microsoft.Authorization/policydefinitions` | partial, 1 of 22 missing |  | `authorization/PolicyDefinition` | 0 / 21 / 1 | managementGroup,subscription | extend: newer API version or property config | S (1) | 1 |
| 13 | `Microsoft.Authorization/policySetDefinitions` | partial, 1 of 25 missing |  | `authorization/PolicySetDefinition` | 0 / 24 / 1 | managementGroup,subscription | extend: newer API version or property config | S (1) | 1 |
| 14 | `Microsoft.Authorization/accessReviewScheduleSettings` | partial, 6 of 15 missing |  | `authorization/AccessReviewScheduleSetting` | 0 / 9 / 6 | extension,subscription | extend: newer API version or property config | S (6) | 0 |
| 15 | `Microsoft.Authorization/accessReviewHistoryDefinitions` | partial, 6 of 20 missing |  | `authorization/AccessReviewHistoryDefinition` | 0 / 14 / 6 | extension,subscription | extend: newer API version or property config | S (6) | 0 |
| 16 | `Microsoft.Authorization/policyDefinitions/versions` | partial, 1 of 21 missing |  | `authorization/PolicyDefinitionsVersion` | 0 / 20 / 1 | managementGroup,subscription | extend: newer API version or property config | S (1) | 0 |
| 17 | `Microsoft.Authorization/accessReviewScheduleDefinitions` | partial, 19 of 24 missing |  | `authorization/AccessReviewScheduleDefinition` | 0 / 5 / 19 | extension,subscription | extend: newer API version or property config | S (19) | 0 |
| 18 | `Microsoft.Authorization/policySetDefinitions/versions` | partial, 1 of 24 missing |  | `authorization/PolicySetDefinitionsVersion` | 0 / 23 / 1 | managementGroup,subscription | extend: newer API version or property config | S (1) | 0 |
| 19 | `Microsoft.Authorization/roleAssignments` | covered | `authorization/RoleAssignment` |  | 7 / 0 / 0 | extension | - | - | 33 |
| 20 | `Microsoft.Authorization/locks` | covered |  | `authorization/ManagementLock` | 0 / 3 / 0 | extension,resourceGroup,subscription | - | - | 31 |
| 21 | `Microsoft.Authorization/roleDefinitions` | covered | `authorization/RoleDefinition` |  | 8 / 0 / 0 | extension | - | - | 2 |
| 22 | `Microsoft.Authorization/policyassignments` | covered |  | `authorization/PolicyAssignment` | 0 / 27 / 0 | extension | - | - | 1 |
| 23 | `Microsoft.Authorization/resourceManagementPrivateLinks` | covered |  | `authorization/ResourceManagementPrivateLink` | 0 / 1 / 0 | resourceGroup | - | - | 0 |
| 24 | `Microsoft.Authorization/variables` | covered |  | `authorization/PolicyVariable` | 0 / 1 / 0 | managementGroup,subscription | - | - | 0 |
| 25 | `Microsoft.Authorization/privateLinkAssociations` | covered |  | `authorization/PrivateLinkAssociation` | 0 / 2 / 0 | managementGroup | - | - | 0 |
| 26 | `Microsoft.Authorization/roleManagementPolicyAssignments` | covered |  | `authorization/RoleManagementPolicyAssignment` | 0 / 3 / 0 | extension | - | - | 0 |
| 27 | `Microsoft.Authorization/policyExemptions` | covered |  | `authorization/PolicyExemption` | 0 / 12 / 0 | extension | - | - | 0 |
| 28 | `Microsoft.Authorization/roleEligibilityScheduleRequests` | covered |  | `authorization/RoleEligibilityScheduleRequest` | 0 / 14 / 0 | extension | - | - | 0 |
| 29 | `Microsoft.Authorization/roleAssignmentScheduleRequests` | covered |  | `authorization/RoleAssignmentScheduleRequest` | 0 / 15 / 0 | extension | - | - | 0 |
| 30 | `Microsoft.PolicyInsights/remediations` | covered |  | `policyinsights/PolicyRemediation` | 0 / 8 / 0 | extension,resourceGroup,subscription | - | - | 0 |
| 31 | `Microsoft.Resources/resourceGroups` | partial, 1 of 4 missing | `resources/ResourceGroup` |  | 3 / 0 / 1 | subscription | extend: ASO's API version is older than the spec's | S (1) | 2 |
| 32 | `Microsoft.Resources/tags` | covered |  | `resources/TagSet` | 0 / 1 / 0 | extension | - | - | 1 |
| 33 | `Microsoft.Consumption/budgets` | covered |  | `consumption/Budget` | 0 / 26 / 0 | extension | - | - | 1 |
| 34 | `Microsoft.CostManagement/markupRules` | no kind |  |  | 0 / 0 / 6 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (6) | 0 |
| 35 | `Microsoft.CostManagement/costAllocationRules` | no kind |  |  | 0 / 0 / 10 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (10) | 0 |
| 36 | `Microsoft.CostManagement/views` | no kind |  |  | 0 / 0 / 34 | extension,tenant | generator, arbitrary owner (several scopes, patch 0006) | M (34) | 0 |
| 37 | `Microsoft.CostManagement/exports` | partial, 1 of 28 missing |  | `costmanagement/Export` | 0 / 27 / 1 | extension | extend: newer API version or property config | S (1) | 0 |
| 38 | `Microsoft.CostManagement/settings` | covered |  | `costmanagement/Setting` | 0 / 2 / 0 | extension | - | - | 0 |
| 39 | `Microsoft.CostManagement/scheduledActions` | covered |  | `costmanagement/ScheduledAction` | 0 / 20 / 0 | extension,tenant | - | - | 0 |
| 40 | `Microsoft.CostManagement/budgets` | covered |  | `costmanagement/Budget` | 0 / 27 / 0 | extension | - | - | 0 |
| 41 | `Microsoft.Insights/diagnosticSettings` | partial, 2 of 14 missing | `insights/DiagnosticSetting` |  | 12 / 0 / 2 | extension | extend: ASO's API version is older than the spec's | S (2) | 23 |
| 42 | `Microsoft.Insights/dataCollectionEndpoints` | partial, 4 of 16 missing | `insights/DataCollectionEndpoint` |  | 12 / 0 / 4 | resourceGroup | extend: newer API version or property config | S (4) | 1 |
| 43 | `Microsoft.Insights/metricAlerts` | partial, 9 of 41 missing | `insights/MetricAlert` |  | 32 / 0 / 9 | resourceGroup | extend: ASO's API version is older than the spec's | S (9) | 1 |
| 44 | `Microsoft.Insights/privateLinkScopes/privateEndpointConnections` | partial, 1 of 4 missing |  | `insights/PrivateLinkScopesPrivateEndpointConnection` | 0 / 3 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 45 | `Microsoft.Insights/myWorkbooks` | partial, 2 of 13 missing |  | `insights/MyWorkbook` | 0 / 11 / 2 | resourceGroup | extend: newer API version or property config | S (2) | 0 |
| 46 | `Microsoft.Insights/workbooks` | partial, 2 of 14 missing | `insights/Workbook` |  | 12 / 0 / 2 | resourceGroup | extend: newer API version or property config | S (2) | 0 |
| 47 | `Microsoft.Insights/activityLogAlerts` | partial, 2 of 15 missing | `insights/ActivityLogAlert` |  | 13 / 0 / 2 | resourceGroup | extend: ASO's API version is older than the spec's | S (2) | 0 |
| 48 | `Microsoft.Insights/scheduledQueryRules` | partial, 9 of 39 missing | `insights/ScheduledQueryRule` |  | 30 / 0 / 9 | resourceGroup | extend: ASO's API version is older than the spec's | S (9) | 0 |
| 49 | `Microsoft.Insights/privateLinkScopes/scopedResources` | covered |  | `insights/PrivateLinkScopesScopedResource` | 0 / 1 / 0 | resourceGroup | - | - | 2 |
| 50 | `Microsoft.Insights/privateLinkScopes` | covered |  | `insights/PrivateLinkScope` | 0 / 7 / 0 | resourceGroup | - | - | 2 |
| 51 | `Microsoft.Insights/dataCollectionRuleAssociations` | covered | `insights/DataCollectionRuleAssociation` |  | 3 / 0 / 0 | extension | - | - | 1 |
| 52 | `Microsoft.Insights/actionGroups` | covered | `insights/ActionGroup` |  | 52 / 0 / 0 | resourceGroup | - | - | 1 |
| 53 | `Microsoft.Insights/dataCollectionRules` | covered | `insights/DataCollectionRule` |  | 140 / 0 / 0 | resourceGroup | - | - | 1 |
| 54 | `Microsoft.Insights/guestDiagnosticSettingsAssociation` | covered |  | `insights/GuestDiagnosticSettingsAssociation` | 0 / 3 / 0 | extension | - | - | 0 |
| 55 | `Microsoft.Insights/logprofiles` | covered |  | `insights/LogProfile` | 0 / 8 / 0 | subscription | - | - | 0 |
| 56 | `Microsoft.Insights/guestDiagnosticSettings` | covered |  | `insights/GuestDiagnosticSetting` | 0 / 15 / 0 | resourceGroup | - | - | 0 |
| 57 | `Microsoft.Insights/workbooktemplates` | covered |  | `insights/WorkbookTemplate` | 0 / 16 / 0 | resourceGroup | - | - | 0 |
| 58 | `Microsoft.Insights/tenantActionGroups` | covered |  | `insights/TenantActionGroup` | 0 / 22 / 0 | managementGroup | - | - | 0 |
| 59 | `Microsoft.Insights/autoscalesettings` | covered | `insights/AutoscaleSetting` |  | 44 / 0 / 0 | resourceGroup | - | - | 0 |
| 60 | `Microsoft.Network/vpnGateways/vpnConnections/vpnLinkConnections/sharedKeys` | no kind |  |  | 0 / 0 / 2 | resourceGroup | generator | S (2) | 0 |
| 61 | `Microsoft.Network/vpnServerConfigurations/configurationPolicyGroups` | no kind |  |  | 0 / 0 / 5 | resourceGroup | generator | S (5) | 0 |
| 62 | `Microsoft.Network/virtualNetworkGateways/natRules` | no kind |  |  | 0 / 0 / 7 | resourceGroup | generator | S (7) | 0 |
| 63 | `Microsoft.Network/networkWatchers/packetCaptures` | no kind |  |  | 0 / 0 / 20 | resourceGroup | generator | S (20) | 0 |
| 64 | `Microsoft.Network/networkWatchers/connectionAnalyzers` | no kind |  |  | 0 / 0 / 23 | resourceGroup | generator | S (23) | 0 |
| 65 | `Microsoft.Network/expressRouteCrossConnections/peerings` | no kind |  |  | 0 / 0 / 28 | resourceGroup | generator | M (28) | 0 |
| 66 | `Microsoft.Network/expressRouteCrossConnections` | no kind |  |  | 0 / 0 / 35 | resourceGroup | generator | M (35) | 0 |
| 67 | `Microsoft.Network/privateEndpoints` | partial, 7 of 106 missing | `network/PrivateEndpoint` |  | 99 / 0 / 7 | resourceGroup | extend: ASO's API version is older than the spec's | S (7) | 12 |
| 68 | `Microsoft.Network/publicIPAddresses` | partial, 3 of 39 missing | `network/PublicIPAddress` |  | 36 / 0 / 3 | resourceGroup | extend: ASO's API version is older than the spec's | S (3) | 4 |
| 69 | `Microsoft.Network/privateDnsZones` | partial, 1 of 4 missing | `network/PrivateDnsZone` |  | 3 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 2 |
| 70 | `Microsoft.Network/routeTables` | partial, 7 of 10 missing | `network/RouteTable` |  | 3 / 0 / 7 | resourceGroup | extend: ASO's API version is older than the spec's | S (7) | 2 |
| 71 | `Microsoft.Network/networkSecurityGroups` | partial, 23 of 26 missing | `network/NetworkSecurityGroup` |  | 3 / 0 / 23 | resourceGroup | extend: ASO's API version is older than the spec's | S (23) | 2 |
| 72 | `Microsoft.Network/virtualNetworks/subnets` | partial, 3 of 69 missing | `network/VirtualNetworksSubnet` |  | 66 / 0 / 3 | resourceGroup | extend: ASO's API version is older than the spec's | S (3) | 2 |
| 73 | `Microsoft.Network/virtualNetworks` | partial, 101 of 118 missing | `network/VirtualNetwork` |  | 17 / 0 / 101 | resourceGroup | extend: ASO's API version is older than the spec's | L (101) | 2 |
| 74 | `Microsoft.Network/routeTables/routes` | partial, 1 of 4 missing | `network/RouteTablesRoute` |  | 3 / 0 / 1 | resourceGroup | extend: ASO's API version is older than the spec's | S (1) | 1 |
| 75 | `Microsoft.Network/natGateways` | partial, 2 of 12 missing | `network/NatGateway` |  | 10 / 0 / 2 | resourceGroup | extend: ASO's API version is older than the spec's | S (2) | 1 |
| 76 | `Microsoft.Network/bastionHosts` | partial, 6 of 26 missing | `network/BastionHost` |  | 20 / 0 / 6 | resourceGroup | extend: ASO's API version is older than the spec's | S (6) | 1 |
| 77 | `Microsoft.Network/firewallPolicies/ruleCollectionGroups` | partial, 3 of 30 missing | `network/FirewallPoliciesRuleCollectionGroup` |  | 27 / 0 / 3 | resourceGroup | extend: ASO's API version is older than the spec's | S (3) | 1 |
| 78 | `Microsoft.Network/azureFirewalls` | partial, 6 of 64 missing | `network/AzureFirewall` |  | 58 / 0 / 6 | resourceGroup | extend: ASO's API version is older than the spec's | S (6) | 1 |
| 79 | `Microsoft.Network/expressRouteLags/authorizations` | partial, 1 of 1 missing |  | `network/ExpressRouteLagsAuthorization` | 0 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 80 | `Microsoft.Network/expressRoutePorts/authorizations` | partial, 1 of 1 missing |  | `network/ExpressRoutePortsAuthorization` | 0 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 81 | `Microsoft.Network/expressRouteCircuits/authorizations` | partial, 1 of 2 missing |  | `network/ExpressRouteCircuitsAuthorization` | 0 / 1 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 82 | `Microsoft.Network/applicationSecurityGroups` | partial, 1 of 3 missing | `network/ApplicationSecurityGroup` |  | 2 / 0 / 1 | resourceGroup | extend: ASO's API version is older than the spec's | S (1) | 0 |
| 83 | `Microsoft.Network/dnsResolverPolicies` | partial, 1 of 3 missing |  | `network/DnsResolverPolicy` | 0 / 2 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 84 | `Microsoft.Network/networkWatchers` | partial, 1 of 3 missing | `network/NetworkWatcher` |  | 2 / 0 / 1 | resourceGroup | extend: ASO's API version is older than the spec's | S (1) | 0 |
| 85 | `Microsoft.Network/dnsZones` | partial, 1 of 6 missing | `network/DnsZone` |  | 5 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 86 | `Microsoft.Network/routeFilters` | partial, 6 of 8 missing |  | `network/RouteFilter` | 0 / 2 / 6 | resourceGroup | extend: newer API version or property config | S (6) | 0 |
| 87 | `Microsoft.Network/serviceEndpointPolicies` | partial, 5 of 9 missing |  | `network/ServiceEndpointPolicy` | 0 / 4 / 5 | resourceGroup | extend: newer API version or property config | S (5) | 0 |
| 88 | `Microsoft.Network/networkManagers/securityAdminConfigurations/ruleCollections/rules` | partial, 12 of 12 missing |  | `network/NetworkManagersSecurityAdminRule` | 0 / 0 / 12 | resourceGroup | extend: newer API version or property config | S (12) | 0 |
| 89 | `Microsoft.Network/expressRouteLags` | partial, 1 of 18 missing |  | `network/ExpressRouteLag` | 0 / 17 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 90 | `Microsoft.Network/networkWatchers/flowLogs` | partial, 2 of 18 missing | `network/NetworkWatchersFlowLog` |  | 16 / 0 / 2 | resourceGroup | extend: ASO's API version is older than the spec's | S (2) | 0 |
| 91 | `Microsoft.Network/expressRouteGateways` | partial, 18 of 24 missing |  | `network/ExpressRouteGateway` | 0 / 6 / 18 | resourceGroup | extend: newer API version or property config | S (18) | 0 |
| 92 | `Microsoft.Network/networkVirtualAppliances` | partial, 1 of 25 missing |  | `network/NetworkVirtualAppliance` | 0 / 24 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 93 | `Microsoft.Network/dnsZones/AAAA` | partial, 1 of 26 missing | `network/DnsZonesAAAARecord` |  | 25 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 94 | `Microsoft.Network/dnsZones/CAA` | partial, 1 of 26 missing | `network/DnsZonesCAARecord` |  | 25 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 95 | `Microsoft.Network/dnsZones/CNAME` | partial, 1 of 26 missing | `network/DnsZonesCNAMERecord` |  | 25 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 96 | `Microsoft.Network/dnsZones/MX` | partial, 1 of 26 missing | `network/DnsZonesMXRecord` |  | 25 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 97 | `Microsoft.Network/dnsZones/NS` | partial, 1 of 26 missing | `network/DnsZonesNSRecord` |  | 25 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 98 | `Microsoft.Network/dnsZones/PTR` | partial, 1 of 26 missing | `network/DnsZonesPTRRecord` |  | 25 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 99 | `Microsoft.Network/dnsZones/SOA` | partial, 1 of 26 missing |  | `network/DnsZonesSOARecord` | 0 / 25 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 100 | `Microsoft.Network/dnsZones/SRV` | partial, 1 of 26 missing | `network/DnsZonesSRVRecord` |  | 25 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 101 | `Microsoft.Network/dnsZones/TXT` | partial, 1 of 26 missing | `network/DnsZonesTXTRecord` |  | 25 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 102 | `Microsoft.Network/publicIPPrefixes` | partial, 1 of 26 missing | `network/PublicIPPrefix` |  | 25 / 0 / 1 | resourceGroup | extend: ASO's API version is older than the spec's | S (1) | 0 |
| 103 | `Microsoft.Network/virtualHubs` | partial, 7 of 28 missing |  | `network/VirtualHub` | 0 / 21 / 7 | resourceGroup | extend: newer API version or property config | S (7) | 0 |
| 104 | `Microsoft.Network/expressRouteCircuits/peerings` | partial, 8 of 44 missing |  | `network/ExpressRouteCircuitsPeering` | 0 / 36 / 8 | resourceGroup | extend: newer API version or property config | S (8) | 0 |
| 105 | `Microsoft.Network/vpnGateways/vpnConnections` | partial, 1 of 56 missing |  | `network/VpnGatewaysVpnConnection` | 0 / 55 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 106 | `Microsoft.Network/expressRouteCircuits` | partial, 51 of 72 missing |  | `network/ExpressRouteCircuit` | 0 / 21 / 51 | resourceGroup | extend: newer API version or property config | M (51) | 0 |
| 107 | `Microsoft.Network/vpnGateways` | partial, 67 of 78 missing |  | `network/VpnGateway` | 0 / 11 / 67 | resourceGroup | extend: newer API version or property config | M (67) | 0 |
| 108 | `Microsoft.Network/virtualNetworkGateways` | partial, 5 of 91 missing | `network/VirtualNetworkGateway` |  | 86 / 0 / 5 | resourceGroup | extend: ASO's API version is older than the spec's | S (5) | 0 |
| 109 | `Microsoft.Network/privateEndpoints/privateDnsZoneGroups` | covered | `network/PrivateEndpointsPrivateDnsZoneGroup` |  | 2 / 0 / 0 | resourceGroup | - | - | 12 |
| 110 | `Microsoft.Network/privateDnsZones/virtualNetworkLinks` | covered | `network/PrivateDnsZonesVirtualNetworkLink` |  | 6 / 0 / 0 | resourceGroup | - | - | 2 |
| 111 | `Microsoft.Network/dnsResolvers` | covered | `network/DnsResolver` |  | 3 / 0 / 0 | resourceGroup | - | - | 1 |
| 112 | `Microsoft.Network/virtualHubs/hubVirtualNetworkConnections` | covered |  | `network/VirtualHubsHubVirtualNetworkConnection` | 0 / 16 / 0 | resourceGroup | - | - | 1 |
| 113 | `Microsoft.Network/networkSecurityGroups/securityRules` | covered | `network/NetworkSecurityGroupsSecurityRule` |  | 21 / 0 / 0 | resourceGroup | - | - | 1 |
| 114 | `Microsoft.Network/virtualNetworks/virtualNetworkPeerings` | covered | `network/VirtualNetworksVirtualNetworkPeering` |  | 25 / 0 / 0 | resourceGroup | - | - | 1 |
| 115 | `Microsoft.Network/firewallPolicies` | covered | `network/FirewallPolicy` |  | 41 / 0 / 0 | resourceGroup | - | - | 1 |
| 116 | `Microsoft.Network/applicationSecurityGroups/addressPrefixSets` | covered |  | `network/ApplicationSecurityGroupsAddressPrefixSet` | 0 / 1 / 0 | resourceGroup | - | - | 0 |
| 117 | `Microsoft.Network/firewallPolicies/signatureOverrides` | covered |  | `network/FirewallPoliciesSignatureOverride` | 0 / 1 / 0 | resourceGroup | - | - | 0 |
| 118 | `Microsoft.Network/networkManagers/networkGroups/staticMembers` | covered |  | `network/NetworkManagersNetworkGroupsStaticMember` | 0 / 1 / 0 | resourceGroup | - | - | 0 |
| 119 | `Microsoft.Network/networkManagers/securityUserConfigurations` | covered |  | `network/NetworkManagersSecurityUserConfiguration` | 0 / 1 / 0 | resourceGroup | - | - | 0 |
| 120 | `Microsoft.Network/dnsForwardingRulesets/virtualNetworkLinks` | covered | `network/DnsForwardingRuleSetsVirtualNetworkLink` |  | 2 / 0 / 0 | resourceGroup | - | - | 0 |
| 121 | `Microsoft.Network/networkManagerConnections` | covered |  | `network/NetworkManagerConnection` | 0 / 2 / 0 | managementGroup,subscription | - | - | 0 |
| 122 | `Microsoft.Network/networkManagers/networkGroups` | covered |  | `network/NetworkManagersNetworkGroup` | 0 / 2 / 0 | resourceGroup | - | - | 0 |
| 123 | `Microsoft.Network/networkManagers/routingConfigurations` | covered |  | `network/NetworkManagersRoutingConfiguration` | 0 / 2 / 0 | resourceGroup | - | - | 0 |
| 124 | `Microsoft.Network/networkManagers/securityAdminConfigurations/ruleCollections` | covered |  | `network/NetworkManagersSecurityAdminRuleCollection` | 0 / 2 / 0 | resourceGroup | - | - | 0 |
| 125 | `Microsoft.Network/networkManagers/securityUserConfigurations/ruleCollections` | covered |  | `network/NetworkManagersSecurityUserRuleCollection` | 0 / 2 / 0 | resourceGroup | - | - | 0 |
| 126 | `Microsoft.Network/networkManagers/verifierWorkspaces/reachabilityAnalysisRuns` | covered |  | `network/NetworkManagersReachabilityAnalysisRun` | 0 / 2 / 0 | resourceGroup | - | - | 0 |
| 127 | `Microsoft.Network/virtualRouters/peerings` | covered |  | `network/VirtualRoutersPeering` | 0 / 2 / 0 | resourceGroup | - | - | 0 |
| 128 | `Microsoft.Network/dnsForwardingRulesets` | covered | `network/DnsForwardingRuleset` |  | 3 / 0 / 0 | resourceGroup | - | - | 0 |
| 129 | `Microsoft.Network/dnsResolverDomainLists` | covered |  | `network/DnsResolverDomainList` | 0 / 3 / 0 | resourceGroup | - | - | 0 |
| 130 | `Microsoft.Network/dnsResolverPolicies/virtualNetworkLinks` | covered |  | `network/DnsResolverPoliciesVirtualNetworkLink` | 0 / 3 / 0 | resourceGroup | - | - | 0 |
| 131 | `Microsoft.Network/dnsResolvers/outboundEndpoints` | covered | `network/DnsResolversOutboundEndpoint` |  | 3 / 0 / 0 | resourceGroup | - | - | 0 |
| 132 | `Microsoft.Network/ipGroups` | covered |  | `network/IpGroup` | 0 / 3 / 0 | resourceGroup | - | - | 0 |
| 133 | `Microsoft.Network/networkManagers/ipamPools/staticCidrs` | covered |  | `network/NetworkManagersIpamPoolsStaticCidr` | 0 / 3 / 0 | resourceGroup | - | - | 0 |
| 134 | `Microsoft.Network/networkManagers/routingConfigurations/ruleCollections` | covered |  | `network/NetworkManagersRoutingRuleCollection` | 0 / 3 / 0 | resourceGroup | - | - | 0 |
| 135 | `Microsoft.Network/networkManagers/scopeConnections` | covered |  | `network/NetworkManagersScopeConnection` | 0 / 3 / 0 | resourceGroup | - | - | 0 |
| 136 | `Microsoft.Network/networkManagers/securityAdminConfigurations` | covered |  | `network/NetworkManagersSecurityAdminConfiguration` | 0 / 3 / 0 | resourceGroup | - | - | 0 |
| 137 | `Microsoft.Network/networkManagers/verifierWorkspaces` | covered |  | `network/NetworkManagersVerifierWorkspace` | 0 / 3 / 0 | resourceGroup | - | - | 0 |
| 138 | `Microsoft.Network/serviceEndpointPolicies/serviceEndpointPolicyDefinitions` | covered |  | `network/ServiceEndpointPoliciesServiceEndpointPolicyDefinition` | 0 / 3 / 0 | resourceGroup | - | - | 0 |
| 139 | `Microsoft.Network/virtualHubs/routingIntent` | covered |  | `network/VirtualHubsRoutingIntent` | 0 / 3 / 0 | resourceGroup | - | - | 0 |
| 140 | `Microsoft.Network/networkVirtualAppliances/virtualApplianceSites` | covered |  | `network/NetworkVirtualAppliancesVirtualApplianceSite` | 0 / 4 / 0 | resourceGroup | - | - | 0 |
| 141 | `Microsoft.Network/routeFilters/routeFilterRules` | covered |  | `network/RouteFiltersRouteFilterRule` | 0 / 4 / 0 | resourceGroup | - | - | 0 |
| 142 | `Microsoft.Network/dnsForwardingRulesets/forwardingRules` | covered | `network/DnsForwardingRuleSetsForwardingRule` |  | 5 / 0 / 0 | resourceGroup | - | - | 0 |
| 143 | `Microsoft.Network/dnsResolvers/inboundEndpoints` | covered | `network/DnsResolversInboundEndpoint` |  | 5 / 0 / 0 | resourceGroup | - | - | 0 |
| 144 | `Microsoft.Network/expressRouteCircuits/peerings/connections` | covered |  | `network/ExpressRouteCircuitsPeeringsConnection` | 0 / 5 / 0 | resourceGroup | - | - | 0 |
| 145 | `Microsoft.Network/networkManagers/commits` | covered |  | `network/NetworkManagersCommit` | 0 / 5 / 0 | resourceGroup | - | - | 0 |
| 146 | `Microsoft.Network/networkManagers/routingConfigurations/ruleCollections/rules` | covered |  | `network/NetworkManagersRoutingRule` | 0 / 5 / 0 | resourceGroup | - | - | 0 |
| 147 | `Microsoft.Network/virtualHubs/routeTables` | covered |  | `network/VirtualHubsRouteTable` | 0 / 5 / 0 | resourceGroup | - | - | 0 |
| 148 | `Microsoft.Network/dnsResolverPolicies/dnsSecurityRules` | covered |  | `network/DnsResolverPoliciesDnsSecurityRule` | 0 / 6 / 0 | resourceGroup | - | - | 0 |
| 149 | `Microsoft.Network/networkManagers` | covered |  | `network/NetworkManager` | 0 / 6 / 0 | resourceGroup | - | - | 0 |
| 150 | `Microsoft.Network/virtualHubs/hubRouteTables` | covered |  | `network/VirtualHubsHubRouteTable` | 0 / 6 / 0 | resourceGroup | - | - | 0 |
| 151 | `Microsoft.Network/virtualRouters` | covered |  | `network/VirtualRouter` | 0 / 6 / 0 | resourceGroup | - | - | 0 |
| 152 | `Microsoft.Network/virtualWans` | covered |  | `network/VirtualWan` | 0 / 6 / 0 | resourceGroup | - | - | 0 |
| 153 | `Microsoft.Network/networkVirtualAppliances/inboundSecurityRules` | covered |  | `network/NetworkVirtualAppliancesInboundSecurityRule` | 0 / 7 / 0 | resourceGroup | - | - | 0 |
| 154 | `Microsoft.Network/vpnGateways/natRules` | covered |  | `network/VpnGatewaysNatRule` | 0 / 7 / 0 | resourceGroup | - | - | 0 |
| 155 | `Microsoft.Network/firewallPolicies/kubeSelectorGroups` | covered |  | `network/FirewallPoliciesKubeSelectorGroup` | 0 / 8 / 0 | resourceGroup | - | - | 0 |
| 156 | `Microsoft.Network/networkManagers/ipamPools` | covered |  | `network/NetworkManagersIpamPool` | 0 / 8 / 0 | resourceGroup | - | - | 0 |
| 157 | `Microsoft.Network/networkManagers/verifierWorkspaces/reachabilityAnalysisIntents` | covered |  | `network/NetworkManagersReachabilityAnalysisIntent` | 0 / 8 / 0 | resourceGroup | - | - | 0 |
| 158 | `Microsoft.Network/networkManagers/securityUserConfigurations/ruleCollections/rules` | covered |  | `network/NetworkManagersSecurityUserRule` | 0 / 9 / 0 | resourceGroup | - | - | 0 |
| 159 | `Microsoft.Network/virtualHubs/connectionPolicies` | covered |  | `network/VirtualHubsConnectionPolicy` | 0 / 11 / 0 | resourceGroup | - | - | 0 |
| 160 | `Microsoft.Network/localNetworkGateways` | covered |  | `network/LocalNetworkGateway` | 0 / 12 / 0 | resourceGroup | - | - | 0 |
| 161 | `Microsoft.Network/virtualHubs/routeMaps` | covered |  | `network/VirtualHubsRouteMap` | 0 / 12 / 0 | resourceGroup | - | - | 0 |
| 162 | `Microsoft.Network/networkManagers/connectivityConfigurations` | covered |  | `network/NetworkManagersConnectivityConfiguration` | 0 / 13 / 0 | resourceGroup | - | - | 0 |
| 163 | `Microsoft.Network/virtualHubs/bgpConnections` | covered |  | `network/VirtualHubsBgpConnection` | 0 / 13 / 0 | resourceGroup | - | - | 0 |
| 164 | `Microsoft.Network/ExpressRoutePorts` | covered |  | `network/ExpressRoutePort` | 0 / 15 / 0 | resourceGroup | - | - | 0 |
| 165 | `Microsoft.Network/networkVirtualAppliances/networkVirtualApplianceConnections` | covered |  | `network/NetworkVirtualAppliancesConnection` | 0 / 15 / 0 | resourceGroup | - | - | 0 |
| 166 | `Microsoft.Network/expressRouteGateways/expressRouteConnections` | covered |  | `network/ExpressRouteGatewaysExpressRouteConnection` | 0 / 16 / 0 | resourceGroup | - | - | 0 |
| 167 | `Microsoft.Network/privateDnsZones/A` | covered | `network/PrivateDnsZonesARecord` |  | 21 / 0 / 0 | resourceGroup | - | - | 0 |
| 168 | `Microsoft.Network/privateDnsZones/AAAA` | covered | `network/PrivateDnsZonesAAAARecord` |  | 21 / 0 / 0 | resourceGroup | - | - | 0 |
| 169 | `Microsoft.Network/privateDnsZones/CNAME` | covered | `network/PrivateDnsZonesCNAMERecord` |  | 21 / 0 / 0 | resourceGroup | - | - | 0 |
| 170 | `Microsoft.Network/privateDnsZones/MX` | covered | `network/PrivateDnsZonesMXRecord` |  | 21 / 0 / 0 | resourceGroup | - | - | 0 |
| 171 | `Microsoft.Network/privateDnsZones/PTR` | covered | `network/PrivateDnsZonesPTRRecord` |  | 21 / 0 / 0 | resourceGroup | - | - | 0 |
| 172 | `Microsoft.Network/privateDnsZones/SOA` | covered |  | `network/PrivateDnsZonesSOARecord` | 0 / 21 / 0 | resourceGroup | - | - | 0 |
| 173 | `Microsoft.Network/privateDnsZones/SRV` | covered | `network/PrivateDnsZonesSRVRecord` |  | 21 / 0 / 0 | resourceGroup | - | - | 0 |
| 174 | `Microsoft.Network/privateDnsZones/TXT` | covered | `network/PrivateDnsZonesTXTRecord` |  | 21 / 0 / 0 | resourceGroup | - | - | 0 |
| 175 | `Microsoft.Network/p2svpnGateways` | covered |  | `network/P2sVpnGateway` | 0 / 24 / 0 | resourceGroup | - | - | 0 |
| 176 | `Microsoft.Network/dnsZones/A` | covered | `network/DnsZonesARecord` |  | 26 / 0 / 0 | resourceGroup | - | - | 0 |
| 177 | `Microsoft.Network/vpnSites` | covered |  | `network/VpnSite` | 0 / 28 / 0 | resourceGroup | - | - | 0 |
| 178 | `Microsoft.Network/firewallPolicies/ruleCollectionGroups/ruleCollectionGroupDrafts` | covered |  | `network/FirewallPoliciesRuleCollectionGroupDraft` | 0 / 30 / 0 | resourceGroup | - | - | 0 |
| 179 | `Microsoft.Network/firewallPolicies/firewallPolicyDrafts` | covered |  | `network/FirewallPoliciesFirewallPolicyDraft` | 0 / 36 / 0 | resourceGroup | - | - | 0 |
| 180 | `Microsoft.Network/vpnServerConfigurations` | covered |  | `network/VpnServerConfiguration` | 0 / 36 / 0 | resourceGroup | - | - | 0 |
| 181 | `Microsoft.Network/networkWatchers/connectionMonitors` | covered |  | `network/NetworkWatchersConnectionMonitor` | 0 / 46 / 0 | resourceGroup | - | - | 0 |
| 182 | `Microsoft.Network/virtualHubs/ipConfigurations` | covered |  | `network/VirtualHubsIpConfiguration` | 0 / 113 / 0 | resourceGroup | - | - | 0 |
| 183 | `Microsoft.Network/connections` | covered |  | `network/VirtualNetworkGatewayConnection` | 0 / 242 / 0 | resourceGroup | - | - | 0 |
| 184 | `Microsoft.KeyVault/vaults/secrets` | no kind |  |  | 0 / 0 / 6 | resourceGroup | generator | S (6) | 0 |
| 185 | `Microsoft.KeyVault/vaults/accessPolicies` | no kind |  |  | 0 / 0 / 7 | resourceGroup | generator | S (7) | 0 |
| 186 | `Microsoft.KeyVault/vaults/privateEndpointConnections` | partial, 1 of 5 missing |  | `keyvault/VaultsPrivateEndpointConnection` | 0 / 4 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 187 | `Microsoft.KeyVault/managedHSMs/privateEndpointConnections` | partial, 1 of 11 missing |  | `keyvault/ManagedHSMsPrivateEndpointConnection` | 0 / 10 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 188 | `Microsoft.KeyVault/vaults/keys` | covered | `keyvault/VaultKey` |  | 15 / 0 / 0 | resourceGroup | - | - | 5 |
| 189 | `Microsoft.KeyVault/vaults` | covered | `keyvault/Vault` |  | 28 / 0 / 0 | resourceGroup | - | - | 4 |
| 190 | `Microsoft.KeyVault/managedHSMs/keys` | covered |  | `keyvault/ManagedHSMsKey` | 0 / 15 / 0 | resourceGroup | - | - | 0 |
| 191 | `Microsoft.KeyVault/managedHSMs` | covered |  | `keyvault/ManagedHSM` | 0 / 20 / 0 | resourceGroup | - | - | 0 |
| 192 | `Microsoft.ManagedIdentity/userAssignedIdentities` | covered | `managedidentity/UserAssignedIdentity` | `managedidentity/UserAssignedIdentity` | 2 / 1 / 0 | resourceGroup | - | - | 3 |
| 193 | `Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials` | covered | `managedidentity/FederatedIdentityCredential` |  | 3 / 0 / 0 | resourceGroup | - | - | 1 |
| 194 | `Microsoft.OperationalInsights/workspaces/features/machineGroups` | no kind |  |  | 0 / 0 / 8 | resourceGroup | generator | S (8) | 0 |
| 195 | `Microsoft.OperationalInsights/queryPacks` | partial, 1 of 3 missing |  | `operationalinsights/QueryPack` | 0 / 2 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 2 |
| 196 | `Microsoft.OperationalInsights/workspaces/dataExports` | partial, 1 of 7 missing |  | `operationalinsights/WorkspacesDataExport` | 0 / 6 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 2 |
| 197 | `Microsoft.OperationalInsights/workspaces/tables` | partial, 1 of 20 missing |  | `operationalinsights/WorkspacesTable` | 0 / 19 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 2 |
| 198 | `Microsoft.OperationalInsights/workspaces` | partial, 3 of 23 missing | `operationalinsights/Workspace` |  | 20 / 0 / 3 | resourceGroup | extend: ASO's API version is older than the spec's | S (3) | 2 |
| 199 | `Microsoft.OperationalInsights/workspaces/summaryLogs` | partial, 2 of 11 missing |  | `operationalinsights/WorkspacesSummaryLog` | 0 / 9 / 2 | resourceGroup | extend: newer API version or property config | S (2) | 0 |
| 200 | `Microsoft.OperationalInsights/clusters` | partial, 2 of 18 missing |  | `operationalinsights/Cluster` | 0 / 16 / 2 | resourceGroup | extend: newer API version or property config | S (2) | 0 |
| 201 | `Microsoft.OperationalInsights/workspaces/linkedStorageAccounts` | covered |  | `operationalinsights/WorkspacesLinkedStorageAccount` | 0 / 1 / 0 | resourceGroup | - | - | 2 |
| 202 | `Microsoft.OperationalInsights/workspaces/linkedServices` | covered |  | `operationalinsights/WorkspacesLinkedService` | 0 / 4 / 0 | resourceGroup | - | - | 2 |
| 203 | `Microsoft.OperationalInsights/workspaces/dataSources` | covered |  | `operationalinsights/WorkspacesDataSource` | 0 / 4 / 0 | resourceGroup | - | - | 0 |
| 204 | `Microsoft.OperationalInsights/workspaces/storageInsightConfigs` | covered |  | `operationalinsights/WorkspacesStorageInsightConfig` | 0 / 6 / 0 | resourceGroup | - | - | 0 |
| 205 | `Microsoft.OperationalInsights/queryPacks/queries` | covered |  | `operationalinsights/QueryPacksQuery` | 0 / 8 / 0 | resourceGroup | - | - | 0 |
| 206 | `Microsoft.OperationalInsights/workspaces/savedSearches` | covered |  | `operationalinsights/WorkspacesSavedSearch` | 0 / 9 / 0 | resourceGroup | - | - | 0 |
| 207 | `Microsoft.OperationsManagement/solutions` | covered |  | `operationsmanagement/Solution` | 0 / 9 / 0 | resourceGroup | - | - | 1 |
| 208 | `Microsoft.OperationsManagement/ManagementAssociations` | covered |  | `operationsmanagement/ManagementAssociation` | 0 / 2 / 0 | extension | - | - | 0 |
| 209 | `Microsoft.OperationsManagement/ManagementConfigurations` | covered |  | `operationsmanagement/ManagementConfiguration` | 0 / 6 / 0 | resourceGroup | - | - | 0 |
| 210 | `Microsoft.Monitor/accounts/metricsContainers` | no kind |  |  | 0 / 0 / 1 | resourceGroup | generator | S (1) | 0 |
| 211 | `Microsoft.Monitor/observabilityAgents` | no kind |  |  | 0 / 0 / 9 | resourceGroup | generator | S (9) | 0 |
| 212 | `Microsoft.Monitor/accounts/issues` | no kind |  |  | 0 / 0 / 12 | resourceGroup | generator | S (12) | 0 |
| 213 | `Microsoft.Monitor/slis` | no kind |  |  | 0 / 0 / 58 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | M (58) | 0 |
| 214 | `Microsoft.Monitor/accounts` | partial, 3 of 6 missing | `monitor/Account` |  | 3 / 0 / 3 | resourceGroup | extend: ASO's API version is older than the spec's | S (3) | 0 |
| 215 | `Microsoft.Monitor/pipelineGroups` | covered |  | `monitor/PipelineGroup` | 0 / 52 / 0 | resourceGroup | - | - | 0 |
| 216 | `Microsoft.ContainerService/managedClusters/agentPools` | partial, 3 of 126 missing | `containerservice/ManagedClustersAgentPool` |  | 123 / 0 / 3 | resourceGroup | extend: ASO's API version is older than the spec's | S (3) | 1 |
| 217 | `Microsoft.ContainerService/managedClusters` | partial, 21 of 330 missing | `containerservice/ManagedCluster` |  | 309 / 0 / 21 | resourceGroup | extend: ASO's API version is older than the spec's | S (21) | 1 |
| 218 | `Microsoft.ContainerService/fleets/members` | partial, 1 of 3 missing | `containerservice/FleetsMember` |  | 2 / 0 / 1 | resourceGroup | extend: ASO's API version is older than the spec's | S (1) | 0 |
| 219 | `Microsoft.ContainerService/fleets/autoUpgradeProfiles` | partial, 3 of 7 missing | `containerservice/FleetsAutoUpgradeProfile` |  | 4 / 0 / 3 | resourceGroup | extend: ASO's API version is older than the spec's | S (3) | 0 |
| 220 | `Microsoft.ContainerService/fleets/updateStrategies` | partial, 10 of 13 missing | `containerservice/FleetsUpdateStrategy` |  | 3 / 0 / 10 | resourceGroup | extend: ASO's API version is older than the spec's | S (10) | 0 |
| 221 | `Microsoft.ContainerService/fleets/updateRuns` | partial, 11 of 18 missing | `containerservice/FleetsUpdateRun` |  | 7 / 0 / 11 | resourceGroup | extend: ASO's API version is older than the spec's | S (11) | 0 |
| 222 | `Microsoft.ContainerService/managedClusters/managedNamespaces` | covered |  | `containerservice/ManagedClustersManagedNamespace` | 0 / 12 / 0 | resourceGroup | - | - | 1 |
| 223 | `Microsoft.ContainerService/managedClusters/maintenanceConfigurations` | covered | `containerservice/MaintenanceConfiguration` |  | 18 / 0 / 0 | resourceGroup | - | - | 1 |
| 224 | `Microsoft.ContainerService/managedClusters/identityBindings` | covered | `containerservice/IdentityBinding` |  | 1 / 0 / 0 | resourceGroup | - | - | 0 |
| 225 | `Microsoft.ContainerService/managedClusters/trustedAccessRoleBindings` | covered | `containerservice/TrustedAccessRoleBinding` |  | 2 / 0 / 0 | resourceGroup | - | - | 0 |
| 226 | `Microsoft.ContainerService/deploymentSafeguards` | covered |  | `containerservice/DeploymentSafeguard` | 0 / 3 / 0 | extension | - | - | 0 |
| 227 | `Microsoft.ContainerService/managedClusters/privateEndpointConnections` | covered |  | `containerservice/ManagedClustersPrivateEndpointConnection` | 0 / 3 / 0 | resourceGroup | - | - | 0 |
| 228 | `Microsoft.ContainerService/snapshots` | covered |  | `containerservice/Snapshot` | 0 / 4 / 0 | resourceGroup | - | - | 0 |
| 229 | `Microsoft.ContainerService/fleets` | covered | `containerservice/Fleet` |  | 10 / 0 / 0 | resourceGroup | - | - | 0 |
| 230 | `Microsoft.ContainerService/fleets/managedNamespaces` | covered |  | `containerservice/FleetsManagedNamespace` | 0 / 26 / 0 | resourceGroup | - | - | 0 |
| 231 | `Microsoft.KubernetesConfiguration/privateLinkScopes/privateEndpointConnections` | partial, 1 of 4 missing |  | `kubernetesconfiguration/PrivateLinkScopesPrivateEndpointConnection` | 0 / 3 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 232 | `Microsoft.KubernetesConfiguration/sourceControlConfigurations` | partial, 1 of 11 missing |  | `kubernetesconfiguration/SourceControlConfiguration` | 0 / 10 / 1 | extension | extend: newer API version or property config | S (1) | 0 |
| 233 | `Microsoft.KubernetesConfiguration/extensions` | partial, 13 of 32 missing | `kubernetesconfiguration/Extension` |  | 19 / 0 / 13 | extension | extend: ASO's API version is older than the spec's | S (13) | 0 |
| 234 | `Microsoft.KubernetesConfiguration/fluxConfigurations` | partial, 1 of 71 missing | `kubernetesconfiguration/FluxConfiguration` |  | 70 / 0 / 1 | extension | extend: ASO's API version is older than the spec's | S (1) | 0 |
| 235 | `Microsoft.KubernetesConfiguration/privateLinkScopes` | covered |  | `kubernetesconfiguration/PrivateLinkScope` | 0 / 4 / 0 | resourceGroup | - | - | 0 |
| 236 | `Microsoft.ContainerRegistry/registries/webhooks` | partial, 1 of 7 missing |  | `containerregistry/RegistriesWebhook` | 0 / 6 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 237 | `Microsoft.ContainerRegistry/registries/connectedRegistries` | partial, 1 of 13 missing |  | `containerregistry/RegistriesConnectedRegistry` | 0 / 12 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 238 | `Microsoft.ContainerRegistry/registries/tasks` | partial, 2 of 54 missing |  | `containerregistry/RegistriesTask` | 0 / 52 / 2 | resourceGroup | extend: newer API version or property config | S (2) | 0 |
| 239 | `Microsoft.ContainerRegistry/registries/scopeMaps` | covered |  | `containerregistry/RegistriesScopeMap` | 0 / 2 / 0 | resourceGroup | - | - | 1 |
| 240 | `Microsoft.ContainerRegistry/registries/cacheRules` | covered | `containerregistry/RegistryCacheRule` |  | 3 / 0 / 0 | resourceGroup | - | - | 1 |
| 241 | `Microsoft.ContainerRegistry/registries/replications` | covered | `containerregistry/RegistryReplication` |  | 4 / 0 / 0 | resourceGroup | - | - | 1 |
| 242 | `Microsoft.ContainerRegistry/registries/credentialSets` | covered |  | `containerregistry/RegistriesCredentialSet` | 0 / 6 / 0 | resourceGroup | - | - | 1 |
| 243 | `Microsoft.ContainerRegistry/registries/tokens` | covered |  | `containerregistry/RegistriesToken` | 0 / 9 / 0 | resourceGroup | - | - | 1 |
| 244 | `Microsoft.ContainerRegistry/registries` | covered | `containerregistry/Registry` |  | 26 / 0 / 0 | resourceGroup | - | - | 1 |
| 245 | `Microsoft.ContainerRegistry/registries/privateEndpointConnections` | covered |  | `containerregistry/RegistriesPrivateEndpointConnection` | 0 / 4 / 0 | resourceGroup | - | - | 0 |
| 246 | `Microsoft.Billing/billingAccounts/billingSubscriptionAliases` | no kind |  |  | 0 / 0 / 17 | tenant | generator, tenant or subscription owner (as ASO's Alias) | S (17) | 0 |

### security-adjacent: 93 resources (30 with no kind, 23 partly covered, 40 covered)

| # | Resource | Status | ASO | Ours | Attributes: official / ours / uncovered | Scope | Route | Size | AVMs |
| ---: | --- | --- | --- | --- | --- | --- | --- | --- | ---: |
| 1 | `Microsoft.PolicyInsights/attestations` | partial, 1 of 10 missing |  | `policyinsights/PolicyAttestation` | 0 / 9 / 1 | extension,resourceGroup,subscription | extend: newer API version or property config | S (1) | 0 |
| 2 | `Microsoft.Network/ddosProtectionPlans` | partial, 1 of 3 missing | `network/DdosProtectionPlan` |  | 2 / 0 / 1 | resourceGroup | extend: ASO's API version is older than the spec's | S (1) | 1 |
| 3 | `Microsoft.Network/networkSecurityPerimeters/profiles` | partial, 1 of 1 missing |  | `network/NetworkSecurityPerimetersProfile` | 0 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 4 | `Microsoft.Network/networkSecurityPerimeters` | partial, 1 of 3 missing |  | `network/NetworkSecurityPerimeter` | 0 / 2 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 5 | `Microsoft.Network/ApplicationGatewayWebApplicationFirewallPolicies` | partial, 12 of 57 missing | `network/WebApplicationFirewallPolicy` |  | 45 / 0 / 12 | resourceGroup | extend: ASO's API version is older than the spec's | S (12) | 0 |
| 6 | `Microsoft.Network/FrontDoorWebApplicationFirewallPolicies` | partial, 18 of 57 missing | `network.frontdoor/WebApplicationFirewallPolicy` |  | 39 / 0 / 18 | resourceGroup | extend: ASO's API version is older than the spec's | S (18) | 0 |
| 7 | `Microsoft.Network/networkSecurityPerimeters/resourceAssociations` | covered |  | `network/NetworkSecurityPerimetersResourceAssociation` | 0 / 3 / 0 | resourceGroup | - | - | 2 |
| 8 | `Microsoft.Network/networkSecurityPerimeters/loggingConfigurations` | covered |  | `network/NetworkSecurityPerimetersLoggingConfiguration` | 0 / 2 / 0 | resourceGroup | - | - | 0 |
| 9 | `Microsoft.Network/networkSecurityPerimeters/links` | covered |  | `network/NetworkSecurityPerimetersLink` | 0 / 4 / 0 | resourceGroup | - | - | 0 |
| 10 | `Microsoft.Network/securityPartnerProviders` | covered |  | `network/SecurityPartnerProvider` | 0 / 4 / 0 | resourceGroup | - | - | 0 |
| 11 | `Microsoft.Network/networkSecurityPerimeters/profiles/accessRules` | covered |  | `network/NetworkSecurityPerimetersProfilesAccessRule` | 0 / 7 / 0 | resourceGroup | - | - | 0 |
| 12 | `Microsoft.Network/ddosCustomPolicies` | covered |  | `network/DdosCustomPolicy` | 0 / 16 / 0 | resourceGroup | - | - | 0 |
| 13 | `Microsoft.Security/apiCollections` | no kind |  |  | 0 / 0 / 0 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (0) | 0 |
| 14 | `Microsoft.Security/pricings/securityOperators` | no kind |  |  | 0 / 0 / 0 | subscription | generator, tenant or subscription owner (as ASO's Alias) | S (0) | 0 |
| 15 | `Microsoft.Security/serverVulnerabilityAssessments` | no kind |  |  | 0 / 0 / 0 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (0) | 0 |
| 16 | `Microsoft.Security/dataScanners` | no kind |  |  | 0 / 0 / 2 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (2) | 0 |
| 17 | `Microsoft.Security/sensitivitySettings` | no kind |  |  | 0 / 0 / 3 | tenant | generator, tenant or subscription owner (as ASO's Alias) | S (3) | 0 |
| 18 | `Microsoft.Security/serviceEntitlementSettings` | no kind |  |  | 0 / 0 / 3 | resourceGroup | generator | S (3) | 0 |
| 19 | `Microsoft.Security/applications` | no kind |  |  | 0 / 0 / 4 | extension,subscription | generator, arbitrary owner (several scopes, patch 0006) | S (4) | 0 |
| 20 | `Microsoft.Security/serviceEntitlements` | no kind |  |  | 0 / 0 / 4 | resourceGroup | generator | S (4) | 0 |
| 21 | `Microsoft.Security/batchPricings` | no kind |  |  | 0 / 0 / 9 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (9) | 0 |
| 22 | `Microsoft.Security/standards` | no kind |  |  | 0 / 0 / 9 | resourceGroup | generator | S (9) | 0 |
| 23 | `Microsoft.Security/assessments/governanceAssignments` | no kind |  |  | 0 / 0 / 10 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (10) | 0 |
| 24 | `Microsoft.Security/assignments` | no kind |  |  | 0 / 0 / 13 | resourceGroup | generator | S (13) | 0 |
| 25 | `Microsoft.Security/deviceSecurityGroups` | no kind |  |  | 0 / 0 / 15 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (15) | 0 |
| 26 | `Microsoft.Security/informationProtectionPolicies` | no kind |  |  | 0 / 0 / 15 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (15) | 0 |
| 27 | `Microsoft.Security/iotSecuritySolutions` | no kind |  |  | 0 / 0 / 16 | resourceGroup | generator | S (16) | 0 |
| 28 | `Microsoft.Security/locations/jitNetworkAccessPolicies` | no kind |  |  | 0 / 0 / 19 | resourceGroup | generator | S (19) | 0 |
| 29 | `Microsoft.Security/settings` | partial, 2 of 2 missing |  | `security/Setting` | 0 / 0 / 2 | subscription | extend: newer API version or property config | S (2) | 0 |
| 30 | `Microsoft.Security/privateLinks/privateEndpointConnections` | partial, 1 of 4 missing |  | `security/PrivateLinksPrivateEndpointConnection` | 0 / 3 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 31 | `Microsoft.Security/securityStandards` | partial, 1 of 6 missing |  | `security/SecurityStandard` | 0 / 5 / 1 | extension | extend: newer API version or property config | S (1) | 0 |
| 32 | `Microsoft.Security/alertsSuppressionRules` | partial, 1 of 7 missing |  | `security/AlertsSuppressionRule` | 0 / 6 / 1 | subscription | extend: newer API version or property config | S (1) | 0 |
| 33 | `Microsoft.Security/standardAssignments` | partial, 1 of 13 missing |  | `security/StandardAssignment` | 0 / 12 / 1 | extension | extend: newer API version or property config | S (1) | 0 |
| 34 | `Microsoft.Security/governanceRules` | partial, 1 of 16 missing |  | `security/GovernanceRule` | 0 / 15 / 1 | extension | extend: newer API version or property config | S (1) | 0 |
| 35 | `Microsoft.Security/advancedThreatProtectionSettings` | covered |  | `security/AdvancedThreatProtectionSetting` | 0 / 1 / 0 | extension | - | - | 0 |
| 36 | `Microsoft.Security/autoProvisioningSettings` | covered |  | `security/AutoProvisioningSetting` | 0 / 1 / 0 | subscription | - | - | 0 |
| 37 | `Microsoft.Security/sqlVulnerabilityAssessments` | covered |  | `security/SqlVulnerabilityAssessment` | 0 / 1 / 0 | extension | - | - | 0 |
| 38 | `Microsoft.Security/serverVulnerabilityAssessmentsSettings` | covered |  | `security/ServerVulnerabilityAssessmentsSetting` | 0 / 2 / 0 | subscription | - | - | 0 |
| 39 | `Microsoft.Security/sqlVulnerabilityAssessments/baselineRules` | covered |  | `security/SqlVulnerabilityAssessmentsBaselineRule` | 0 / 2 / 0 | extension | - | - | 0 |
| 40 | `Microsoft.Security/workspaceSettings` | covered |  | `security/WorkspaceSetting` | 0 / 2 / 0 | subscription | - | - | 0 |
| 41 | `Microsoft.Security/privateLinks` | covered |  | `security/PrivateLink` | 0 / 3 / 0 | resourceGroup | - | - | 0 |
| 42 | `Microsoft.Security/customRecommendations` | covered |  | `security/CustomRecommendation` | 0 / 7 / 0 | extension | - | - | 0 |
| 43 | `Microsoft.Security/securityConnectors/devops/azureDevOpsOrgs` | covered |  | `security/SecurityConnectorsDevOpsAzureDevOpsOrg` | 0 / 7 / 0 | resourceGroup | - | - | 0 |
| 44 | `Microsoft.Security/pricings` | covered |  | `security/Pricing` | 0 / 8 / 0 | extension | - | - | 0 |
| 45 | `Microsoft.Security/securityConnectors/devops/azureDevOpsOrgs/projects` | covered |  | `security/SecurityConnectorsDevOpsAzureDevOpsProject` | 0 / 8 / 0 | resourceGroup | - | - | 0 |
| 46 | `Microsoft.Security/securityContacts` | covered |  | `security/SecurityContact` | 0 / 8 / 0 | subscription | - | - | 0 |
| 47 | `Microsoft.Security/securityConnectors/devops` | covered |  | `security/SecurityConnectorsDevOpsConfiguration` | 0 / 9 / 0 | resourceGroup | - | - | 0 |
| 48 | `Microsoft.Security/securityConnectors/devops/azureDevOpsOrgs/projects/repos` | covered |  | `security/SecurityConnectorsDevOpsAzureDevOpsRepo` | 0 / 9 / 0 | resourceGroup | - | - | 0 |
| 49 | `Microsoft.Security/defenderForStorageSettings` | covered |  | `security/DefenderForStorageSetting` | 0 / 10 / 0 | extension | - | - | 0 |
| 50 | `Microsoft.Security/assessmentMetadata` | covered |  | `security/AssessmentMetadata` | 0 / 18 / 0 | subscription | - | - | 0 |
| 51 | `Microsoft.Security/automations` | covered |  | `security/Automation` | 0 / 20 / 0 | resourceGroup | - | - | 0 |
| 52 | `Microsoft.Security/assessments` | covered |  | `security/Assessment` | 0 / 36 / 0 | extension | - | - | 0 |
| 53 | `Microsoft.Security/securityConnectors` | covered |  | `security/SecurityConnector` | 0 / 78 / 0 | resourceGroup | - | - | 0 |
| 54 | `Microsoft.SecurityInsights/cases/comments` | no kind |  |  | 0 / 0 / 1 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (1) | 0 |
| 55 | `Microsoft.SecurityInsights/hunts/comments` | no kind |  |  | 0 / 0 / 2 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (2) | 0 |
| 56 | `Microsoft.SecurityInsights/incidents/comments` | no kind |  |  | 0 / 0 / 2 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (2) | 0 |
| 57 | `Microsoft.SecurityInsights/incidents/relations` | no kind |  |  | 0 / 0 / 2 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (2) | 0 |
| 58 | `Microsoft.SecurityInsights/hunts/relations` | no kind |  |  | 0 / 0 / 3 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (3) | 0 |
| 59 | `Microsoft.SecurityInsights/fileImports` | no kind |  |  | 0 / 0 / 6 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (6) | 0 |
| 60 | `Microsoft.SecurityInsights/cases/relations` | no kind |  |  | 0 / 0 / 8 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (8) | 0 |
| 61 | `Microsoft.SecurityInsights/cases` | no kind |  |  | 0 / 0 / 11 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (11) | 0 |
| 62 | `Microsoft.SecurityInsights/entityQueries` | no kind |  |  | 0 / 0 / 11 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (11) | 0 |
| 63 | `Microsoft.SecurityInsights/incidents/tasks` | no kind |  |  | 0 / 0 / 12 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (12) | 0 |
| 64 | `Microsoft.SecurityInsights/hunts` | no kind |  |  | 0 / 0 / 13 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (13) | 0 |
| 65 | `Microsoft.SecurityInsights/incidents` | no kind |  |  | 0 / 0 / 16 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (16) | 0 |
| 66 | `Microsoft.SecurityInsights/bookmarks` | no kind |  |  | 0 / 0 / 17 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (17) | 0 |
| 67 | `Microsoft.SecurityInsights/threatIntelligence/indicators` | no kind |  |  | 0 / 0 / 39 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | M (39) | 0 |
| 68 | `Microsoft.SecurityInsights/settings` | partial, 3 of 3 missing |  | `securityinsights/Setting` | 0 / 0 / 3 | extension | extend: newer API version or property config | S (3) | 0 |
| 69 | `Microsoft.SecurityInsights/alertRules` | partial, 4 of 4 missing |  | `securityinsights/AlertRule` | 0 / 0 / 4 | extension | extend: newer API version or property config | S (4) | 0 |
| 70 | `Microsoft.SecurityInsights/dataConnectors` | partial, 4 of 4 missing |  | `securityinsights/DataConnector` | 0 / 0 / 4 | extension | extend: newer API version or property config | S (4) | 0 |
| 71 | `Microsoft.SecurityInsights/automationRules` | partial, 1 of 14 missing |  | `securityinsights/AutomationRule` | 0 / 13 / 1 | extension | extend: newer API version or property config | S (1) | 0 |
| 72 | `Microsoft.SecurityInsights/metadata` | partial, 1 of 33 missing |  | `securityinsights/ContentMetadata` | 0 / 32 / 1 | extension | extend: newer API version or property config | S (1) | 0 |
| 73 | `Microsoft.SecurityInsights/contentPackages` | partial, 1 of 37 missing |  | `securityinsights/ContentPackage` | 0 / 36 / 1 | extension | extend: newer API version or property config | S (1) | 0 |
| 74 | `Microsoft.SecurityInsights/dataConnectorDefinitions` | partial, 1 of 38 missing |  | `securityinsights/DataConnectorDefinition` | 0 / 37 / 1 | extension | extend: newer API version or property config | S (1) | 0 |
| 75 | `Microsoft.SecurityInsights/contentTemplates` | partial, 1 of 39 missing |  | `securityinsights/ContentTemplate` | 0 / 38 / 1 | extension | extend: newer API version or property config | S (1) | 0 |
| 76 | `Microsoft.SecurityInsights/workspaceManagerConfigurations` | covered |  | `securityinsights/WorkspaceManagerConfiguration` | 0 / 1 / 0 | extension | - | - | 0 |
| 77 | `Microsoft.SecurityInsights/onboardingStates` | covered |  | `securityinsights/OnboardingState` | 0 / 2 / 0 | extension | - | - | 0 |
| 78 | `Microsoft.SecurityInsights/workspaceManagerAssignments` | covered |  | `securityinsights/WorkspaceManagerAssignment` | 0 / 2 / 0 | extension | - | - | 0 |
| 79 | `Microsoft.SecurityInsights/workspaceManagerMembers` | covered |  | `securityinsights/WorkspaceManagerMember` | 0 / 2 / 0 | extension | - | - | 0 |
| 80 | `Microsoft.SecurityInsights/alertRules/actions` | covered |  | `securityinsights/AlertRulesAction` | 0 / 3 / 0 | extension | - | - | 0 |
| 81 | `Microsoft.SecurityInsights/workspaceManagerGroups` | covered |  | `securityinsights/WorkspaceManagerGroup` | 0 / 3 / 0 | extension | - | - | 0 |
| 82 | `Microsoft.SecurityInsights/watchlists/watchlistItems` | covered |  | `securityinsights/WatchlistsWatchlistItem` | 0 / 11 / 0 | extension | - | - | 0 |
| 83 | `Microsoft.SecurityInsights/securityMLAnalyticsSettings` | covered |  | `securityinsights/SecurityMLAnalyticsSetting` | 0 / 16 / 0 | extension | - | - | 0 |
| 84 | `Microsoft.SecurityInsights/sourcecontrols` | covered |  | `securityinsights/SourceControl` | 0 / 16 / 0 | extension | - | - | 0 |
| 85 | `Microsoft.SecurityInsights/watchlists` | covered |  | `securityinsights/Watchlist` | 0 / 22 / 0 | extension | - | - | 0 |
| 86 | `Microsoft.Purview/accounts` | partial, 1 of 9 missing |  | `purview/Account` | 0 / 8 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 87 | `Microsoft.Purview/accounts/privateEndpointConnections` | covered |  | `purview/AccountsPrivateEndpointConnection` | 0 / 4 / 0 | resourceGroup | - | - | 0 |
| 88 | `Microsoft.Purview/accounts/kafkaConfigurations` | covered |  | `purview/AccountsKafkaConfiguration` | 0 / 8 / 0 | resourceGroup | - | - | 0 |
| 89 | `Microsoft.HardwareSecurityModules/cloudHsmClusters/privateEndpointConnections` | partial, 1 of 5 missing |  | `hardwaresecuritymodules/CloudHsmClustersPrivateEndpointConnection` | 0 / 4 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 90 | `Microsoft.HardwareSecurityModules/cloudHsmClusters` | covered |  | `hardwaresecuritymodules/CloudHsmCluster` | 0 / 9 / 0 | resourceGroup | - | - | 0 |
| 91 | `Microsoft.HardwareSecurityModules/dedicatedHSMs` | covered |  | `hardwaresecuritymodules/DedicatedHsm` | 0 / 9 / 0 | resourceGroup | - | - | 0 |
| 92 | `Microsoft.Cdn/profiles/securityPolicies` | partial, 2 of 6 missing | `cdn/SecurityPolicy` |  | 4 / 0 / 2 | resourceGroup | extend: ASO's API version is older than the spec's | S (2) | 0 |
| 93 | `Microsoft.Cdn/cdnWebApplicationFirewallPolicies` | covered |  | `cdn/CdnWebApplicationFirewallPolicy` | 0 / 39 / 0 | resourceGroup | - | - | 0 |
<!-- foundation:end -->

## Upjet has it, we do not

Generated from the Upjet slice (`hack/upjet/provider-upjet-azure.json`,
HFL-697): the resources Crossplane's provider-upjet-azure serves that
neither ASO nor this repository has a kind for, ranked by the tier of
their service (`priority.csv`), then by how many references to them the
resources we already serve carry in Upjet (what users of our kinds reach
for next), then tested by Upjet's maintainers before not, then by hand
examples. A list for the queue above, not a plan: each still goes
through "Choosing" (ASO first). The facts for each row are in
`upjet-gaps.csv` with the slice line.

<!-- upjet-gaps:start -->
| # | Resource | Tier | Upjet resource | Referenced by what we serve | Tested by Upjet | Hand examples |
| ---: | --- | ---: | --- | ---: | --- | ---: |
| 1 | `Microsoft.Network/networkProfiles` | 1 | `azurerm_network_profile` | 0 | true | 2 |
| 2 | `Microsoft.Resources/deployments` | 1 | `azurerm_resource_group_template_deployment` | 0 | true | 1 |
| 3 | `Microsoft.Resources/deployments` | 1 | `azurerm_subscription_template_deployment` | 0 | true | 1 |
| 4 | `Microsoft.Insights/components/analyticsItems` | 1 | `azurerm_application_insights_analytics_item` | 0 | true | 1 |
| 5 | `Microsoft.Insights/components/myanalyticsItems` | 1 | `azurerm_application_insights_analytics_item` | 0 | true | 1 |
| 6 | `Microsoft.Network/networkWatchers/packetCaptures` | 1 | `azurerm_virtual_machine_packet_capture` | 0 | false | 0 |
| 7 | `Microsoft.Network/networkWatchers/packetCaptures` | 1 | `azurerm_virtual_machine_scale_set_packet_capture` | 0 | false | 0 |
| 8 | `Microsoft.RecoveryServices/vaults/backupFabrics/protectionContainers` | 2 | `azurerm_backup_container_storage_account` | 1 | true | 1 |
| 9 | `Microsoft.Compute/virtualMachineScaleSets/virtualMachines/diagnosticRunCommands` | 2 | `azurerm_virtual_machine_run_command` | 0 | true | 2 |
| 10 | `Microsoft.Compute/virtualMachineScaleSets/virtualMachines/runCommands` | 2 | `azurerm_virtual_machine_run_command` | 0 | true | 2 |
| 11 | `Microsoft.Storage/storageAccounts/blobServices/containers/immutabilityPolicies` | 2 | `azurerm_storage_container_immutability_policy` | 0 | true | 1 |
| 12 | `Microsoft.RecoveryServices/vaults/backupFabrics/protectionContainers` | 2 | `azurerm_backup_protected_file_share` | 0 | true | 1 |
| 13 | `Microsoft.RecoveryServices/vaults/replicationFabrics/replicationNetworks/replicationNetworkMappings` | 2 | `azurerm_site_recovery_network_mapping` | 0 | true | 1 |
| 14 | `Microsoft.DevTestLab/labs/policysets/policies` | 2 | `azurerm_dev_test_policy` | 0 | true | 1 |
| 15 | `Microsoft.DBforMySQL/servers/databases` | 3 | `azurerm_mssql_database` | 4 | true | 1 |
| 16 | `Microsoft.DataFactory/factories/pipelines` | 3 | `azurerm_data_factory_pipeline` | 3 | true | 1 |
| 17 | `Microsoft.AppPlatform/Spring/apps` | 3 | `azurerm_spring_cloud_app` | 1 | true | 2 |
| 18 | `Microsoft.EventGrid/domains/eventSubscriptions` | 3 | `azurerm_eventgrid_event_subscription` | 0 | true | 2 |
| 19 | `Microsoft.EventGrid/domains/topics/eventSubscriptions` | 3 | `azurerm_eventgrid_event_subscription` | 0 | true | 2 |
| 20 | `Microsoft.EventGrid/topics/eventSubscriptions` | 3 | `azurerm_eventgrid_event_subscription` | 0 | true | 2 |
| 21 | `Microsoft.Cdn/profiles/endpoints/customDomains` | 3 | `azurerm_cdn_frontdoor_custom_domain` | 0 | true | 2 |
| 22 | `Microsoft.DataFactory/factories/datasets` | 3 | `azurerm_data_factory_custom_dataset` | 0 | true | 2 |
| 23 | `Microsoft.AppPlatform/Spring/applicationAccelerators/customizedAccelerators` | 3 | `azurerm_spring_cloud_customized_accelerator` | 0 | true | 2 |
| 24 | `Microsoft.AppPlatform/spring` | 3 | `azurerm_spring_cloud_service` | 0 | true | 2 |
| 25 | `Microsoft.AzureStackHCI/virtualMachines/extensions` | 3 | `azurerm_virtual_machine_extension` | 0 | true | 2 |

166 in all: [`upjet-gaps.csv`](upjet-gaps.csv).
<!-- upjet-gaps:end -->
