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
| 2 | `SubscriptionTag` (`Tags`), `BillingSubscriptionAlias` | `Microsoft.Resources/tags` (1: `properties.tags{}`, extension scope), `Microsoft.Billing/billingAccounts/billingSubscriptionAliases` (17, tenant); `Microsoft.Subscription/policies` (3) | `subscription/Alias` covers `Microsoft.Subscription/aliases` / none | tags on a subscription, resource group or resource as one object; subscription creation under a billing scope (EA and MCA) | none found; ASO issue #4728 (renaming a subscription) open | 1 | **`TagSet` built 2026-11-09** (`resources.azure.hfl.dev/v20250401`); `BillingSubscriptionAlias` not built (ASO's `Alias` creates subscriptions under a billing scope); generator, arbitrary owner (`Tags`); generator (`BillingSubscriptionAlias`) | S |
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
| 13 | `ManagedHsm` (+ keys), `VaultAccessPolicy`, `VaultSecret` (ARM) | `Microsoft.KeyVault/managedHSMs` (20), `managedHSMs/keys` (15), `vaults/accessPolicies` (7), `vaults/secrets` (6), `vaults/privateEndpointConnections` (5); spec `keyvault/.../KeyVault/stable/2026-05-15` | `Vault`, `VaultKey` (28 of 28, 15 of 15) / `VaultSecret` and `VaultCertificate` (data plane) | managed HSMs and the ARM-side secret | secrets declined (#1894); none for the rest | 4 (vaults), 5 (keys) | generator | M |
| 14 | `OperationsManagementSolution`, `WorkspacesDataSource`, `WorkspacesStorageInsightConfig`, `LogAnalyticsCluster` | `OperationsManagement/solutions` (9, preview 2015-11-01-preview), `OperationalInsights/workspaces/dataSources` (4), `storageInsightConfigs` (6), `clusters` (18) | `Workspace` (20 of 23) / tables, data exports, linked services, linked storage, saved searches, query packs | solutions; the rest of a workspace | none found | 1 (solutions) | generator | M |
| 15 | `ManagedClustersManagedNamespace`, `FleetsManagedNamespace`, `ManagedClustersPrivateEndpointConnection`, `ManagedClustersSnapshot` | `managedClusters/managedNamespaces` (12), `fleets/managedNamespaces` (26), `managedClusters/privateEndpointConnections` (3), `snapshots` (4); spec `containerservice/.../aks/stable/2026-08-01/managedClusters.json` | `ManagedCluster` (309 of 330, ASO 2026-05-01), `ManagedClustersAgentPool` (123 of 126), fleets (partly), maintenance, trusted access, identity bindings / none | managed namespaces; the missing cluster attributes are API-version lag (spec 2026-08-01) | none found | 1 (managed namespaces) | generator | M |
| 16 | `SourceControlConfiguration`, `KubernetesConfigurationPrivateLinkScope` (+ endpoint connection) | `KubernetesConfiguration/sourceControlConfigurations` (11, legacy Flux v1, extension scope), `privateLinkScopes` (4) + `privateEndpointConnections` (4) | `Extension` (19 of 32), `FluxConfiguration` (70 of 71) / none | the legacy GitOps kind; `Extension` is behind the spec | none found | 0 | generator, arbitrary owner | S |
| 17 | `RegistriesTask`, `RegistriesConnectedRegistry`, `RegistriesPrivateEndpointConnection` | `registries/tasks` (54), `connectedRegistries` (13), `privateEndpointConnections` (4); spec `containerregistry/.../RegistryTasks/stable/2019-04-01` | `Registry`, replications, cache rules (ASO); credential sets, scope maps, tokens, webhooks (ours) | tasks and connected registries | none found | 0 | generator | M |
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
| 19 | `DefenderForStorageSetting` | `Microsoft.Security/defenderForStorageSettings` (10); spec `security/.../Security/stable/2025-06-01` | none / none | all | **open PR [#5783](https://github.com/Azure/azure-service-operator/pull/5783)** (issue #5759) adds `security.azure.com/v20250601` `DefenderForStorageSetting`: copy it | 0 | copy from PR | S |
| 20 | `SecurityPricing`, `SecurityContact`, `SecurityAutoProvisioningSetting`, `SecuritySetting`, `SecurityWorkspaceSetting`, `AdvancedThreatProtectionSetting` (group `security`) | `pricings` (8, the Defender plans per subscription), `securityContacts` (8), `autoProvisioningSettings` (1), `settings` (2), `workspaceSettings` (2), `advancedThreatProtectionSettings` (1); spec `security/.../Security/stable/2026-07-01/pricings.json`, `preview/2023-12-01-preview` (contacts) | none / none | all; the builder's "Defender for Cloud settings and pricings, security contacts" | none found | 0 | generator, subscription or extension owner (as `Alias`, `DiagnosticSetting`) | S (22) |
| 21 | `PolicyAttestation` | `Microsoft.PolicyInsights/attestations` (10, several scopes) | none / none | all | none found | 0 | **built 2026-11-09** with row 4: `policyinsights.azure.hfl.dev/v20241001` `PolicyAttestation`, extension resource (patch 0006) | S |
| 22 | Sentinel: `SentinelOnboardingState`, `SentinelSetting`, `AlertRule`, `AutomationRule`, `DataConnector`, `Watchlist` (+ items) first | `Microsoft.SecurityInsights/*`: 32 resources, 393 attributes; spec `securityinsights/.../SecurityInsights/stable/2025-09-01` | none / none | all | none found | 0 | generator, extension owner (a Log Analytics workspace) | L (393), start with 6 kinds of about 50 |
| 23 | `SecurityStandard`, `StandardAssignment`, `SecurityAutomation`, `SecurityConnector` (+ devops children) | `securityStandards` (6), `standardAssignments` (13), `automations` (20), `securityConnectors` (78) with 4 devops child types (33) | none / none | all | none found | 0 | generator | M |
| 24 | `PurviewAccount`, `HsmCloudHsmCluster`, `HsmDedicatedHsm`, `NetworkDdosCustomPolicy`, `NetworkSecurityPartnerProvider`, `CdnWebApplicationFirewallPolicy` | `Purview/accounts` (9), `HardwareSecurityModules/cloudHsmClusters` (9) and `dedicatedHSMs`, `Network/ddosCustomPolicies` (16), `securityPartnerProviders` (4), `Cdn/cdnWebApplicationFirewallPolicies` | none / none | all | none found | 0 | generator | S each |

Covered already in this category: ASO has the WAF policies of Application Gateway
and Front Door (`network/WebApplicationFirewallPolicy`,
`network.frontdoor/WebApplicationFirewallPolicy`) and `network/DdosProtectionPlan`;
we have the network security perimeter and its five child kinds. Not covered:
the Azure CDN WAF policy (`Microsoft.Cdn/cdnWebApplicationFirewallPolicies`) and
its `securityPolicies` association, to be generated with row 24.

## The inventory

Generated: every resource of the two categories, by the order of the services in `priority.csv`, then no kind before partly covered before covered, then AVMs descending, then size. Status `partial` is attributes of the spec not covered (an API version behind, or a property ASO leaves out); `Attributes` is official / ours beyond / uncovered. `AVMs` is how many of the 33 modules read need the resource (`avm-need.csv`; 0 when none does).

<!-- foundation:start -->

| Category | Services | Resources | Attributes | Official | Ours beyond | Uncovered |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| platform-foundation | 18 | 246 | 4,675 | 43.2% | 21.6% | 35.2% |
| security-adjacent | 7 | 93 | 1,049 | 8.6% | 2.6% | 88.8% |
| application | 35 | 823 | 14,821 | 33.1% | 3.8% | 63.2% |
| other | 230 | 1,262 | 22,824 | 0.5% | 0.0% | 99.5% |

### platform-foundation: 246 resources (101 with no kind, 61 partly covered, 84 covered)

| # | Resource | Status | ASO | Ours | Attributes: official / ours / uncovered | Scope | Route | Size | AVMs |
| ---: | --- | --- | --- | --- | --- | --- | --- | --- | ---: |
| 1 | `Microsoft.Management/serviceGroups` | no kind |  |  | 0 / 0 / 5 | tenant | generator, tenant or subscription owner (as ASO's Alias) | S (5) | 0 |
| 2 | `Microsoft.Management/managementGroups` | covered |  | `management/ManagementGroup` | 0 / 2 / 0 | tenant | - | - | 2 |
| 3 | `Microsoft.Management/managementGroups/subscriptions` | covered |  | `management/ManagementGroupsSubscription` | 0 / 0 / 0 | tenant | - | - | 1 |
| 4 | `Microsoft.Management/managementGroups/settings` | covered |  | `management/ManagementGroupsSetting` | 0 / 2 / 0 | tenant | - | - | 0 |
| 5 | `Microsoft.Subscription/policies` | no kind |  |  | 0 / 0 / 3 | tenant | generator, tenant or subscription owner (as ASO's Alias) | S (3) | 0 |
| 6 | `Microsoft.Subscription/subscriptionDefinitions` | no kind |  |  | 0 / 0 / 3 | tenant | generator, tenant or subscription owner (as ASO's Alias) | S (3) | 0 |
| 7 | `Microsoft.Subscription/aliases` | covered | `subscription/Alias` |  | 9 / 0 / 0 | tenant | - | - | 1 |
| 8 | `Microsoft.Authorization/privilegedResourceFavorites` | no kind |  |  | 0 / 0 / 1 | tenant | generator, tenant or subscription owner (as ASO's Alias) | S (1) | 0 |
| 9 | `Microsoft.Authorization/resourceManagementPrivateLinks` | no kind |  |  | 0 / 0 / 1 | resourceGroup | generator | S (1) | 0 |
| 10 | `Microsoft.Authorization/variables` | no kind |  |  | 0 / 0 / 1 | managementGroup,subscription | generator, arbitrary owner (several scopes, patch 0006) | S (1) | 0 |
| 11 | `Microsoft.Authorization/privateLinkAssociations` | no kind |  |  | 0 / 0 / 2 | managementGroup | generator, arbitrary owner (several scopes, patch 0006) | S (2) | 0 |
| 12 | `Microsoft.Authorization/variables/values` | no kind |  |  | 0 / 0 / 2 | managementGroup,subscription | generator, arbitrary owner (several scopes, patch 0006) | S (2) | 0 |
| 13 | `Microsoft.Authorization/roleAssignmentApprovals/stages` | no kind |  |  | 0 / 0 / 3 | extension,tenant | generator, arbitrary owner (several scopes, patch 0006) | S (3) | 0 |
| 14 | `Microsoft.Authorization/accessReviewScheduleDefinitions/instances` | no kind |  |  | 0 / 0 / 4 | extension,subscription | generator, arbitrary owner (several scopes, patch 0006) | S (4) | 0 |
| 15 | `Microsoft.Authorization/accessReviewScheduleSettings` | no kind |  |  | 0 / 0 / 15 | extension,subscription | generator, arbitrary owner (several scopes, patch 0006) | S (15) | 0 |
| 16 | `Microsoft.Authorization/accessReviewHistoryDefinitions` | no kind |  |  | 0 / 0 / 20 | extension,subscription | generator, arbitrary owner (several scopes, patch 0006) | S (20) | 0 |
| 17 | `Microsoft.Authorization/policyDefinitions/versions` | no kind |  |  | 0 / 0 / 21 | managementGroup,subscription | generator, arbitrary owner (several scopes, patch 0006) | S (21) | 0 |
| 18 | `Microsoft.Authorization/accessReviewScheduleDefinitions` | no kind |  |  | 0 / 0 / 24 | extension,subscription | generator, arbitrary owner (several scopes, patch 0006) | S (24) | 0 |
| 19 | `Microsoft.Authorization/policySetDefinitions/versions` | no kind |  |  | 0 / 0 / 24 | managementGroup,subscription | generator, arbitrary owner (several scopes, patch 0006) | S (24) | 0 |
| 20 | `Microsoft.Authorization/policydefinitions` | partial, 1 of 22 missing |  | `authorization/PolicyDefinition` | 0 / 21 / 1 | managementGroup,subscription | extend: newer API version or property config | S (1) | 1 |
| 21 | `Microsoft.Authorization/policySetDefinitions` | partial, 1 of 25 missing |  | `authorization/PolicySetDefinition` | 0 / 24 / 1 | managementGroup,subscription | extend: newer API version or property config | S (1) | 1 |
| 22 | `Microsoft.Authorization/roleAssignments` | covered | `authorization/RoleAssignment` |  | 7 / 0 / 0 | extension | - | - | 33 |
| 23 | `Microsoft.Authorization/locks` | covered |  | `authorization/ManagementLock` | 0 / 3 / 0 | extension,resourceGroup,subscription | - | - | 31 |
| 24 | `Microsoft.Authorization/roleDefinitions` | covered | `authorization/RoleDefinition` |  | 8 / 0 / 0 | extension | - | - | 2 |
| 25 | `Microsoft.Authorization/policyassignments` | covered |  | `authorization/PolicyAssignment` | 0 / 27 / 0 | extension | - | - | 1 |
| 26 | `Microsoft.Authorization/roleManagementPolicyAssignments` | covered |  | `authorization/RoleManagementPolicyAssignment` | 0 / 3 / 0 | extension | - | - | 0 |
| 27 | `Microsoft.Authorization/policyExemptions` | covered |  | `authorization/PolicyExemption` | 0 / 12 / 0 | extension | - | - | 0 |
| 28 | `Microsoft.Authorization/roleEligibilityScheduleRequests` | covered |  | `authorization/RoleEligibilityScheduleRequest` | 0 / 14 / 0 | extension | - | - | 0 |
| 29 | `Microsoft.Authorization/roleAssignmentScheduleRequests` | covered |  | `authorization/RoleAssignmentScheduleRequest` | 0 / 15 / 0 | extension | - | - | 0 |
| 30 | `Microsoft.PolicyInsights/remediations` | covered |  | `policyinsights/PolicyRemediation` | 0 / 8 / 0 | extension,resourceGroup,subscription | - | - | 0 |
| 31 | `Microsoft.Resources/resourceGroups` | partial, 1 of 4 missing | `resources/ResourceGroup` |  | 3 / 0 / 1 | subscription | extend: ASO's API version is older than the spec's | S (1) | 2 |
| 32 | `Microsoft.Resources/tags` | covered |  | `resources/TagSet` | 0 / 1 / 0 | extension | - | - | 1 |
| 33 | `Microsoft.Consumption/budgets` | covered |  | `consumption/Budget` | 0 / 26 / 0 | extension | - | - | 1 |
| 34 | `Microsoft.CostManagement/settings` | no kind |  |  | 0 / 0 / 2 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (2) | 0 |
| 35 | `Microsoft.CostManagement/markupRules` | no kind |  |  | 0 / 0 / 6 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (6) | 0 |
| 36 | `Microsoft.CostManagement/costAllocationRules` | no kind |  |  | 0 / 0 / 10 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (10) | 0 |
| 37 | `Microsoft.CostManagement/scheduledActions` | no kind |  |  | 0 / 0 / 20 | extension,tenant | generator, arbitrary owner (several scopes, patch 0006) | S (20) | 0 |
| 38 | `Microsoft.CostManagement/budgets` | no kind |  |  | 0 / 0 / 27 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | M (27) | 0 |
| 39 | `Microsoft.CostManagement/views` | no kind |  |  | 0 / 0 / 34 | extension,tenant | generator, arbitrary owner (several scopes, patch 0006) | M (34) | 0 |
| 40 | `Microsoft.CostManagement/exports` | partial, 1 of 28 missing |  | `costmanagement/Export` | 0 / 27 / 1 | extension | extend: newer API version or property config | S (1) | 0 |
| 41 | `Microsoft.Insights/guestDiagnosticSettingsAssociation` | no kind |  |  | 0 / 0 / 3 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (3) | 0 |
| 42 | `Microsoft.Insights/logprofiles` | no kind |  |  | 0 / 0 / 8 | subscription | generator, tenant or subscription owner (as ASO's Alias) | S (8) | 0 |
| 43 | `Microsoft.Insights/myWorkbooks` | no kind |  |  | 0 / 0 / 13 | resourceGroup | generator | S (13) | 0 |
| 44 | `Microsoft.Insights/guestDiagnosticSettings` | no kind |  |  | 0 / 0 / 15 | resourceGroup | generator | S (15) | 0 |
| 45 | `Microsoft.Insights/workbooktemplates` | no kind |  |  | 0 / 0 / 16 | resourceGroup | generator | S (16) | 0 |
| 46 | `Microsoft.Insights/tenantActionGroups` | no kind |  |  | 0 / 0 / 22 | managementGroup | generator, arbitrary owner (several scopes, patch 0006) | S (22) | 0 |
| 47 | `Microsoft.Insights/diagnosticSettings` | partial, 2 of 14 missing | `insights/DiagnosticSetting` |  | 12 / 0 / 2 | extension | extend: ASO's API version is older than the spec's | S (2) | 23 |
| 48 | `Microsoft.Insights/dataCollectionEndpoints` | partial, 4 of 16 missing | `insights/DataCollectionEndpoint` |  | 12 / 0 / 4 | resourceGroup | extend: newer API version or property config | S (4) | 1 |
| 49 | `Microsoft.Insights/metricAlerts` | partial, 9 of 41 missing | `insights/MetricAlert` |  | 32 / 0 / 9 | resourceGroup | extend: ASO's API version is older than the spec's | S (9) | 1 |
| 50 | `Microsoft.Insights/privateLinkScopes/privateEndpointConnections` | partial, 1 of 4 missing |  | `insights/PrivateLinkScopesPrivateEndpointConnection` | 0 / 3 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 51 | `Microsoft.Insights/workbooks` | partial, 2 of 14 missing | `insights/Workbook` |  | 12 / 0 / 2 | resourceGroup | extend: newer API version or property config | S (2) | 0 |
| 52 | `Microsoft.Insights/activityLogAlerts` | partial, 2 of 15 missing | `insights/ActivityLogAlert` |  | 13 / 0 / 2 | resourceGroup | extend: ASO's API version is older than the spec's | S (2) | 0 |
| 53 | `Microsoft.Insights/scheduledQueryRules` | partial, 9 of 39 missing | `insights/ScheduledQueryRule` |  | 30 / 0 / 9 | resourceGroup | extend: ASO's API version is older than the spec's | S (9) | 0 |
| 54 | `Microsoft.Insights/privateLinkScopes/scopedResources` | covered |  | `insights/PrivateLinkScopesScopedResource` | 0 / 1 / 0 | resourceGroup | - | - | 2 |
| 55 | `Microsoft.Insights/privateLinkScopes` | covered |  | `insights/PrivateLinkScope` | 0 / 7 / 0 | resourceGroup | - | - | 2 |
| 56 | `Microsoft.Insights/dataCollectionRuleAssociations` | covered | `insights/DataCollectionRuleAssociation` |  | 3 / 0 / 0 | extension | - | - | 1 |
| 57 | `Microsoft.Insights/actionGroups` | covered | `insights/ActionGroup` |  | 52 / 0 / 0 | resourceGroup | - | - | 1 |
| 58 | `Microsoft.Insights/dataCollectionRules` | covered | `insights/DataCollectionRule` |  | 140 / 0 / 0 | resourceGroup | - | - | 1 |
| 59 | `Microsoft.Insights/autoscalesettings` | covered | `insights/AutoscaleSetting` |  | 44 / 0 / 0 | resourceGroup | - | - | 0 |
| 60 | `Microsoft.Network/applicationSecurityGroups/addressPrefixSets` | no kind |  |  | 0 / 0 / 1 | resourceGroup | generator | S (1) | 0 |
| 61 | `Microsoft.Network/expressRouteLags/authorizations` | no kind |  |  | 0 / 0 / 1 | resourceGroup | generator | S (1) | 0 |
| 62 | `Microsoft.Network/firewallPolicies/signatureOverrides` | no kind |  |  | 0 / 0 / 1 | resourceGroup | generator | S (1) | 0 |
| 63 | `Microsoft.Network/networkManagers/routingConfigurations` | no kind |  |  | 0 / 0 / 2 | resourceGroup | generator | S (2) | 0 |
| 64 | `Microsoft.Network/networkManagers/verifierWorkspaces/reachabilityAnalysisRuns` | no kind |  |  | 0 / 0 / 2 | resourceGroup | generator | S (2) | 0 |
| 65 | `Microsoft.Network/virtualRouters/peerings` | no kind |  |  | 0 / 0 / 2 | resourceGroup | generator | S (2) | 0 |
| 66 | `Microsoft.Network/vpnGateways/vpnConnections/vpnLinkConnections/sharedKeys` | no kind |  |  | 0 / 0 / 2 | resourceGroup | generator | S (2) | 0 |
| 67 | `Microsoft.Network/dnsResolverDomainLists` | no kind |  |  | 0 / 0 / 3 | resourceGroup | generator | S (3) | 0 |
| 68 | `Microsoft.Network/dnsResolverPolicies` | no kind |  |  | 0 / 0 / 3 | resourceGroup | generator | S (3) | 0 |
| 69 | `Microsoft.Network/dnsResolverPolicies/virtualNetworkLinks` | no kind |  |  | 0 / 0 / 3 | resourceGroup | generator | S (3) | 0 |
| 70 | `Microsoft.Network/ipGroups` | no kind |  |  | 0 / 0 / 3 | resourceGroup | generator | S (3) | 0 |
| 71 | `Microsoft.Network/networkManagers/ipamPools/staticCidrs` | no kind |  |  | 0 / 0 / 3 | resourceGroup | generator | S (3) | 0 |
| 72 | `Microsoft.Network/networkManagers/routingConfigurations/ruleCollections` | no kind |  |  | 0 / 0 / 3 | resourceGroup | generator | S (3) | 0 |
| 73 | `Microsoft.Network/networkManagers/verifierWorkspaces` | no kind |  |  | 0 / 0 / 3 | resourceGroup | generator | S (3) | 0 |
| 74 | `Microsoft.Network/serviceEndpointPolicies/serviceEndpointPolicyDefinitions` | no kind |  |  | 0 / 0 / 3 | resourceGroup | generator | S (3) | 0 |
| 75 | `Microsoft.Network/networkVirtualAppliances/virtualApplianceSites` | no kind |  |  | 0 / 0 / 4 | resourceGroup | generator | S (4) | 0 |
| 76 | `Microsoft.Network/routeFilters/routeFilterRules` | no kind |  |  | 0 / 0 / 4 | resourceGroup | generator | S (4) | 0 |
| 77 | `Microsoft.Network/networkManagers/commits` | no kind |  |  | 0 / 0 / 5 | resourceGroup | generator | S (5) | 0 |
| 78 | `Microsoft.Network/networkManagers/routingConfigurations/ruleCollections/rules` | no kind |  |  | 0 / 0 / 5 | resourceGroup | generator | S (5) | 0 |
| 79 | `Microsoft.Network/vpnServerConfigurations/configurationPolicyGroups` | no kind |  |  | 0 / 0 / 5 | resourceGroup | generator | S (5) | 0 |
| 80 | `Microsoft.Network/dnsResolverPolicies/dnsSecurityRules` | no kind |  |  | 0 / 0 / 6 | resourceGroup | generator | S (6) | 0 |
| 81 | `Microsoft.Network/virtualRouters` | no kind |  |  | 0 / 0 / 6 | resourceGroup | generator | S (6) | 0 |
| 82 | `Microsoft.Network/networkVirtualAppliances/inboundSecurityRules` | no kind |  |  | 0 / 0 / 7 | resourceGroup | generator | S (7) | 0 |
| 83 | `Microsoft.Network/virtualNetworkGateways/natRules` | no kind |  |  | 0 / 0 / 7 | resourceGroup | generator | S (7) | 0 |
| 84 | `Microsoft.Network/firewallPolicies/kubeSelectorGroups` | no kind |  |  | 0 / 0 / 8 | resourceGroup | generator | S (8) | 0 |
| 85 | `Microsoft.Network/networkManagers/ipamPools` | no kind |  |  | 0 / 0 / 8 | resourceGroup | generator | S (8) | 0 |
| 86 | `Microsoft.Network/networkManagers/verifierWorkspaces/reachabilityAnalysisIntents` | no kind |  |  | 0 / 0 / 8 | resourceGroup | generator | S (8) | 0 |
| 87 | `Microsoft.Network/routeFilters` | no kind |  |  | 0 / 0 / 8 | resourceGroup | generator | S (8) | 0 |
| 88 | `Microsoft.Network/serviceEndpointPolicies` | no kind |  |  | 0 / 0 / 9 | resourceGroup | generator | S (9) | 0 |
| 89 | `Microsoft.Network/virtualHubs/connectionPolicies` | no kind |  |  | 0 / 0 / 11 | resourceGroup | generator | S (11) | 0 |
| 90 | `Microsoft.Network/virtualHubs/routeMaps` | no kind |  |  | 0 / 0 / 12 | resourceGroup | generator | S (12) | 0 |
| 91 | `Microsoft.Network/networkVirtualAppliances/networkVirtualApplianceConnections` | no kind |  |  | 0 / 0 / 15 | resourceGroup | generator | S (15) | 0 |
| 92 | `Microsoft.Network/expressRouteLags` | no kind |  |  | 0 / 0 / 18 | resourceGroup | generator | S (18) | 0 |
| 93 | `Microsoft.Network/networkWatchers/packetCaptures` | no kind |  |  | 0 / 0 / 20 | resourceGroup | generator | S (20) | 0 |
| 94 | `Microsoft.Network/privateDnsZones/SOA` | no kind |  |  | 0 / 0 / 21 | resourceGroup | generator | S (21) | 0 |
| 95 | `Microsoft.Network/networkWatchers/connectionAnalyzers` | no kind |  |  | 0 / 0 / 23 | resourceGroup | generator | S (23) | 0 |
| 96 | `Microsoft.Network/networkVirtualAppliances` | no kind |  |  | 0 / 0 / 25 | resourceGroup | generator | S (25) | 0 |
| 97 | `Microsoft.Network/dnsZones/SOA` | no kind |  |  | 0 / 0 / 26 | resourceGroup | generator | M (26) | 0 |
| 98 | `Microsoft.Network/expressRouteCrossConnections/peerings` | no kind |  |  | 0 / 0 / 28 | resourceGroup | generator | M (28) | 0 |
| 99 | `Microsoft.Network/firewallPolicies/ruleCollectionGroups/ruleCollectionGroupDrafts` | no kind |  |  | 0 / 0 / 30 | resourceGroup | generator | M (30) | 0 |
| 100 | `Microsoft.Network/expressRouteCrossConnections` | no kind |  |  | 0 / 0 / 35 | resourceGroup | generator | M (35) | 0 |
| 101 | `Microsoft.Network/firewallPolicies/firewallPolicyDrafts` | no kind |  |  | 0 / 0 / 36 | resourceGroup | generator | M (36) | 0 |
| 102 | `Microsoft.Network/networkWatchers/connectionMonitors` | no kind |  |  | 0 / 0 / 46 | resourceGroup | generator | M (46) | 0 |
| 103 | `Microsoft.Network/privateEndpoints` | partial, 7 of 106 missing | `network/PrivateEndpoint` |  | 99 / 0 / 7 | resourceGroup | extend: ASO's API version is older than the spec's | S (7) | 12 |
| 104 | `Microsoft.Network/publicIPAddresses` | partial, 3 of 39 missing | `network/PublicIPAddress` |  | 36 / 0 / 3 | resourceGroup | extend: ASO's API version is older than the spec's | S (3) | 4 |
| 105 | `Microsoft.Network/privateDnsZones` | partial, 1 of 4 missing | `network/PrivateDnsZone` |  | 3 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 2 |
| 106 | `Microsoft.Network/routeTables` | partial, 7 of 10 missing | `network/RouteTable` |  | 3 / 0 / 7 | resourceGroup | extend: ASO's API version is older than the spec's | S (7) | 2 |
| 107 | `Microsoft.Network/networkSecurityGroups` | partial, 23 of 26 missing | `network/NetworkSecurityGroup` |  | 3 / 0 / 23 | resourceGroup | extend: ASO's API version is older than the spec's | S (23) | 2 |
| 108 | `Microsoft.Network/virtualNetworks/subnets` | partial, 3 of 69 missing | `network/VirtualNetworksSubnet` |  | 66 / 0 / 3 | resourceGroup | extend: ASO's API version is older than the spec's | S (3) | 2 |
| 109 | `Microsoft.Network/virtualNetworks` | partial, 101 of 118 missing | `network/VirtualNetwork` |  | 17 / 0 / 101 | resourceGroup | extend: ASO's API version is older than the spec's | L (101) | 2 |
| 110 | `Microsoft.Network/routeTables/routes` | partial, 1 of 4 missing | `network/RouteTablesRoute` |  | 3 / 0 / 1 | resourceGroup | extend: ASO's API version is older than the spec's | S (1) | 1 |
| 111 | `Microsoft.Network/natGateways` | partial, 2 of 12 missing | `network/NatGateway` |  | 10 / 0 / 2 | resourceGroup | extend: ASO's API version is older than the spec's | S (2) | 1 |
| 112 | `Microsoft.Network/bastionHosts` | partial, 6 of 26 missing | `network/BastionHost` |  | 20 / 0 / 6 | resourceGroup | extend: ASO's API version is older than the spec's | S (6) | 1 |
| 113 | `Microsoft.Network/firewallPolicies/ruleCollectionGroups` | partial, 3 of 30 missing | `network/FirewallPoliciesRuleCollectionGroup` |  | 27 / 0 / 3 | resourceGroup | extend: ASO's API version is older than the spec's | S (3) | 1 |
| 114 | `Microsoft.Network/azureFirewalls` | partial, 6 of 64 missing | `network/AzureFirewall` |  | 58 / 0 / 6 | resourceGroup | extend: ASO's API version is older than the spec's | S (6) | 1 |
| 115 | `Microsoft.Network/expressRoutePorts/authorizations` | partial, 1 of 1 missing |  | `network/ExpressRoutePortsAuthorization` | 0 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 116 | `Microsoft.Network/expressRouteCircuits/authorizations` | partial, 1 of 2 missing |  | `network/ExpressRouteCircuitsAuthorization` | 0 / 1 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 117 | `Microsoft.Network/applicationSecurityGroups` | partial, 1 of 3 missing | `network/ApplicationSecurityGroup` |  | 2 / 0 / 1 | resourceGroup | extend: ASO's API version is older than the spec's | S (1) | 0 |
| 118 | `Microsoft.Network/networkWatchers` | partial, 1 of 3 missing | `network/NetworkWatcher` |  | 2 / 0 / 1 | resourceGroup | extend: ASO's API version is older than the spec's | S (1) | 0 |
| 119 | `Microsoft.Network/dnsZones` | partial, 1 of 6 missing | `network/DnsZone` |  | 5 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 120 | `Microsoft.Network/networkManagers/securityAdminConfigurations/ruleCollections/rules` | partial, 12 of 12 missing |  | `network/NetworkManagersSecurityAdminRule` | 0 / 0 / 12 | resourceGroup | extend: newer API version or property config | S (12) | 0 |
| 121 | `Microsoft.Network/networkWatchers/flowLogs` | partial, 2 of 18 missing | `network/NetworkWatchersFlowLog` |  | 16 / 0 / 2 | resourceGroup | extend: ASO's API version is older than the spec's | S (2) | 0 |
| 122 | `Microsoft.Network/expressRouteGateways` | partial, 18 of 24 missing |  | `network/ExpressRouteGateway` | 0 / 6 / 18 | resourceGroup | extend: newer API version or property config | S (18) | 0 |
| 123 | `Microsoft.Network/dnsZones/AAAA` | partial, 1 of 26 missing | `network/DnsZonesAAAARecord` |  | 25 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 124 | `Microsoft.Network/dnsZones/CAA` | partial, 1 of 26 missing | `network/DnsZonesCAARecord` |  | 25 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 125 | `Microsoft.Network/dnsZones/CNAME` | partial, 1 of 26 missing | `network/DnsZonesCNAMERecord` |  | 25 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 126 | `Microsoft.Network/dnsZones/MX` | partial, 1 of 26 missing | `network/DnsZonesMXRecord` |  | 25 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 127 | `Microsoft.Network/dnsZones/NS` | partial, 1 of 26 missing | `network/DnsZonesNSRecord` |  | 25 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 128 | `Microsoft.Network/dnsZones/PTR` | partial, 1 of 26 missing | `network/DnsZonesPTRRecord` |  | 25 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 129 | `Microsoft.Network/dnsZones/SRV` | partial, 1 of 26 missing | `network/DnsZonesSRVRecord` |  | 25 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 130 | `Microsoft.Network/dnsZones/TXT` | partial, 1 of 26 missing | `network/DnsZonesTXTRecord` |  | 25 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 131 | `Microsoft.Network/publicIPPrefixes` | partial, 1 of 26 missing | `network/PublicIPPrefix` |  | 25 / 0 / 1 | resourceGroup | extend: ASO's API version is older than the spec's | S (1) | 0 |
| 132 | `Microsoft.Network/virtualHubs` | partial, 7 of 28 missing |  | `network/VirtualHub` | 0 / 21 / 7 | resourceGroup | extend: newer API version or property config | S (7) | 0 |
| 133 | `Microsoft.Network/expressRouteCircuits/peerings` | partial, 8 of 44 missing |  | `network/ExpressRouteCircuitsPeering` | 0 / 36 / 8 | resourceGroup | extend: newer API version or property config | S (8) | 0 |
| 134 | `Microsoft.Network/vpnGateways/vpnConnections` | partial, 1 of 56 missing |  | `network/VpnGatewaysVpnConnection` | 0 / 55 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 135 | `Microsoft.Network/expressRouteCircuits` | partial, 51 of 72 missing |  | `network/ExpressRouteCircuit` | 0 / 21 / 51 | resourceGroup | extend: newer API version or property config | M (51) | 0 |
| 136 | `Microsoft.Network/vpnGateways` | partial, 67 of 78 missing |  | `network/VpnGateway` | 0 / 11 / 67 | resourceGroup | extend: newer API version or property config | M (67) | 0 |
| 137 | `Microsoft.Network/virtualNetworkGateways` | partial, 5 of 91 missing | `network/VirtualNetworkGateway` |  | 86 / 0 / 5 | resourceGroup | extend: ASO's API version is older than the spec's | S (5) | 0 |
| 138 | `Microsoft.Network/privateEndpoints/privateDnsZoneGroups` | covered | `network/PrivateEndpointsPrivateDnsZoneGroup` |  | 2 / 0 / 0 | resourceGroup | - | - | 12 |
| 139 | `Microsoft.Network/privateDnsZones/virtualNetworkLinks` | covered | `network/PrivateDnsZonesVirtualNetworkLink` |  | 6 / 0 / 0 | resourceGroup | - | - | 2 |
| 140 | `Microsoft.Network/dnsResolvers` | covered | `network/DnsResolver` |  | 3 / 0 / 0 | resourceGroup | - | - | 1 |
| 141 | `Microsoft.Network/virtualHubs/hubVirtualNetworkConnections` | covered |  | `network/VirtualHubsHubVirtualNetworkConnection` | 0 / 16 / 0 | resourceGroup | - | - | 1 |
| 142 | `Microsoft.Network/networkSecurityGroups/securityRules` | covered | `network/NetworkSecurityGroupsSecurityRule` |  | 21 / 0 / 0 | resourceGroup | - | - | 1 |
| 143 | `Microsoft.Network/virtualNetworks/virtualNetworkPeerings` | covered | `network/VirtualNetworksVirtualNetworkPeering` |  | 25 / 0 / 0 | resourceGroup | - | - | 1 |
| 144 | `Microsoft.Network/firewallPolicies` | covered | `network/FirewallPolicy` |  | 41 / 0 / 0 | resourceGroup | - | - | 1 |
| 145 | `Microsoft.Network/networkManagers/networkGroups/staticMembers` | covered |  | `network/NetworkManagersNetworkGroupsStaticMember` | 0 / 1 / 0 | resourceGroup | - | - | 0 |
| 146 | `Microsoft.Network/networkManagers/securityUserConfigurations` | covered |  | `network/NetworkManagersSecurityUserConfiguration` | 0 / 1 / 0 | resourceGroup | - | - | 0 |
| 147 | `Microsoft.Network/dnsForwardingRulesets/virtualNetworkLinks` | covered | `network/DnsForwardingRuleSetsVirtualNetworkLink` |  | 2 / 0 / 0 | resourceGroup | - | - | 0 |
| 148 | `Microsoft.Network/networkManagerConnections` | covered |  | `network/NetworkManagerConnection` | 0 / 2 / 0 | managementGroup,subscription | - | - | 0 |
| 149 | `Microsoft.Network/networkManagers/networkGroups` | covered |  | `network/NetworkManagersNetworkGroup` | 0 / 2 / 0 | resourceGroup | - | - | 0 |
| 150 | `Microsoft.Network/networkManagers/securityAdminConfigurations/ruleCollections` | covered |  | `network/NetworkManagersSecurityAdminRuleCollection` | 0 / 2 / 0 | resourceGroup | - | - | 0 |
| 151 | `Microsoft.Network/networkManagers/securityUserConfigurations/ruleCollections` | covered |  | `network/NetworkManagersSecurityUserRuleCollection` | 0 / 2 / 0 | resourceGroup | - | - | 0 |
| 152 | `Microsoft.Network/dnsForwardingRulesets` | covered | `network/DnsForwardingRuleset` |  | 3 / 0 / 0 | resourceGroup | - | - | 0 |
| 153 | `Microsoft.Network/dnsResolvers/outboundEndpoints` | covered | `network/DnsResolversOutboundEndpoint` |  | 3 / 0 / 0 | resourceGroup | - | - | 0 |
| 154 | `Microsoft.Network/networkManagers/scopeConnections` | covered |  | `network/NetworkManagersScopeConnection` | 0 / 3 / 0 | resourceGroup | - | - | 0 |
| 155 | `Microsoft.Network/networkManagers/securityAdminConfigurations` | covered |  | `network/NetworkManagersSecurityAdminConfiguration` | 0 / 3 / 0 | resourceGroup | - | - | 0 |
| 156 | `Microsoft.Network/virtualHubs/routingIntent` | covered |  | `network/VirtualHubsRoutingIntent` | 0 / 3 / 0 | resourceGroup | - | - | 0 |
| 157 | `Microsoft.Network/dnsForwardingRulesets/forwardingRules` | covered | `network/DnsForwardingRuleSetsForwardingRule` |  | 5 / 0 / 0 | resourceGroup | - | - | 0 |
| 158 | `Microsoft.Network/dnsResolvers/inboundEndpoints` | covered | `network/DnsResolversInboundEndpoint` |  | 5 / 0 / 0 | resourceGroup | - | - | 0 |
| 159 | `Microsoft.Network/expressRouteCircuits/peerings/connections` | covered |  | `network/ExpressRouteCircuitsPeeringsConnection` | 0 / 5 / 0 | resourceGroup | - | - | 0 |
| 160 | `Microsoft.Network/virtualHubs/routeTables` | covered |  | `network/VirtualHubsRouteTable` | 0 / 5 / 0 | resourceGroup | - | - | 0 |
| 161 | `Microsoft.Network/networkManagers` | covered |  | `network/NetworkManager` | 0 / 6 / 0 | resourceGroup | - | - | 0 |
| 162 | `Microsoft.Network/virtualHubs/hubRouteTables` | covered |  | `network/VirtualHubsHubRouteTable` | 0 / 6 / 0 | resourceGroup | - | - | 0 |
| 163 | `Microsoft.Network/virtualWans` | covered |  | `network/VirtualWan` | 0 / 6 / 0 | resourceGroup | - | - | 0 |
| 164 | `Microsoft.Network/vpnGateways/natRules` | covered |  | `network/VpnGatewaysNatRule` | 0 / 7 / 0 | resourceGroup | - | - | 0 |
| 165 | `Microsoft.Network/networkManagers/securityUserConfigurations/ruleCollections/rules` | covered |  | `network/NetworkManagersSecurityUserRule` | 0 / 9 / 0 | resourceGroup | - | - | 0 |
| 166 | `Microsoft.Network/localNetworkGateways` | covered |  | `network/LocalNetworkGateway` | 0 / 12 / 0 | resourceGroup | - | - | 0 |
| 167 | `Microsoft.Network/networkManagers/connectivityConfigurations` | covered |  | `network/NetworkManagersConnectivityConfiguration` | 0 / 13 / 0 | resourceGroup | - | - | 0 |
| 168 | `Microsoft.Network/virtualHubs/bgpConnections` | covered |  | `network/VirtualHubsBgpConnection` | 0 / 13 / 0 | resourceGroup | - | - | 0 |
| 169 | `Microsoft.Network/ExpressRoutePorts` | covered |  | `network/ExpressRoutePort` | 0 / 15 / 0 | resourceGroup | - | - | 0 |
| 170 | `Microsoft.Network/expressRouteGateways/expressRouteConnections` | covered |  | `network/ExpressRouteGatewaysExpressRouteConnection` | 0 / 16 / 0 | resourceGroup | - | - | 0 |
| 171 | `Microsoft.Network/privateDnsZones/A` | covered | `network/PrivateDnsZonesARecord` |  | 21 / 0 / 0 | resourceGroup | - | - | 0 |
| 172 | `Microsoft.Network/privateDnsZones/AAAA` | covered | `network/PrivateDnsZonesAAAARecord` |  | 21 / 0 / 0 | resourceGroup | - | - | 0 |
| 173 | `Microsoft.Network/privateDnsZones/CNAME` | covered | `network/PrivateDnsZonesCNAMERecord` |  | 21 / 0 / 0 | resourceGroup | - | - | 0 |
| 174 | `Microsoft.Network/privateDnsZones/MX` | covered | `network/PrivateDnsZonesMXRecord` |  | 21 / 0 / 0 | resourceGroup | - | - | 0 |
| 175 | `Microsoft.Network/privateDnsZones/PTR` | covered | `network/PrivateDnsZonesPTRRecord` |  | 21 / 0 / 0 | resourceGroup | - | - | 0 |
| 176 | `Microsoft.Network/privateDnsZones/SRV` | covered | `network/PrivateDnsZonesSRVRecord` |  | 21 / 0 / 0 | resourceGroup | - | - | 0 |
| 177 | `Microsoft.Network/privateDnsZones/TXT` | covered | `network/PrivateDnsZonesTXTRecord` |  | 21 / 0 / 0 | resourceGroup | - | - | 0 |
| 178 | `Microsoft.Network/p2svpnGateways` | covered |  | `network/P2sVpnGateway` | 0 / 24 / 0 | resourceGroup | - | - | 0 |
| 179 | `Microsoft.Network/dnsZones/A` | covered | `network/DnsZonesARecord` |  | 26 / 0 / 0 | resourceGroup | - | - | 0 |
| 180 | `Microsoft.Network/vpnSites` | covered |  | `network/VpnSite` | 0 / 28 / 0 | resourceGroup | - | - | 0 |
| 181 | `Microsoft.Network/vpnServerConfigurations` | covered |  | `network/VpnServerConfiguration` | 0 / 36 / 0 | resourceGroup | - | - | 0 |
| 182 | `Microsoft.Network/virtualHubs/ipConfigurations` | covered |  | `network/VirtualHubsIpConfiguration` | 0 / 113 / 0 | resourceGroup | - | - | 0 |
| 183 | `Microsoft.Network/connections` | covered |  | `network/VirtualNetworkGatewayConnection` | 0 / 242 / 0 | resourceGroup | - | - | 0 |
| 184 | `Microsoft.KeyVault/vaults/privateEndpointConnections` | no kind |  |  | 0 / 0 / 5 | resourceGroup | generator | S (5) | 0 |
| 185 | `Microsoft.KeyVault/vaults/secrets` | no kind |  |  | 0 / 0 / 6 | resourceGroup | generator | S (6) | 0 |
| 186 | `Microsoft.KeyVault/vaults/accessPolicies` | no kind |  |  | 0 / 0 / 7 | resourceGroup | generator | S (7) | 0 |
| 187 | `Microsoft.KeyVault/managedHSMs/privateEndpointConnections` | no kind |  |  | 0 / 0 / 11 | resourceGroup | generator | S (11) | 0 |
| 188 | `Microsoft.KeyVault/managedHSMs/keys` | no kind |  |  | 0 / 0 / 15 | resourceGroup | generator | S (15) | 0 |
| 189 | `Microsoft.KeyVault/managedHSMs` | no kind |  |  | 0 / 0 / 20 | resourceGroup | generator | S (20) | 0 |
| 190 | `Microsoft.KeyVault/vaults/keys` | covered | `keyvault/VaultKey` |  | 15 / 0 / 0 | resourceGroup | - | - | 5 |
| 191 | `Microsoft.KeyVault/vaults` | covered | `keyvault/Vault` |  | 28 / 0 / 0 | resourceGroup | - | - | 4 |
| 192 | `Microsoft.ManagedIdentity/userAssignedIdentities` | partial, 1 of 3 missing | `managedidentity/UserAssignedIdentity` |  | 2 / 0 / 1 | resourceGroup | extend: ASO's API version is older than the spec's | S (1) | 3 |
| 193 | `Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials` | covered | `managedidentity/FederatedIdentityCredential` |  | 3 / 0 / 0 | resourceGroup | - | - | 1 |
| 194 | `Microsoft.OperationalInsights/workspaces/dataSources` | no kind |  |  | 0 / 0 / 4 | resourceGroup | generator | S (4) | 0 |
| 195 | `Microsoft.OperationalInsights/workspaces/storageInsightConfigs` | no kind |  |  | 0 / 0 / 6 | resourceGroup | generator | S (6) | 0 |
| 196 | `Microsoft.OperationalInsights/workspaces/features/machineGroups` | no kind |  |  | 0 / 0 / 8 | resourceGroup | generator | S (8) | 0 |
| 197 | `Microsoft.OperationalInsights/workspaces/summaryLogs` | no kind |  |  | 0 / 0 / 11 | resourceGroup | generator | S (11) | 0 |
| 198 | `Microsoft.OperationalInsights/clusters` | no kind |  |  | 0 / 0 / 18 | resourceGroup | generator | S (18) | 0 |
| 199 | `Microsoft.OperationalInsights/queryPacks` | partial, 1 of 3 missing |  | `operationalinsights/QueryPack` | 0 / 2 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 2 |
| 200 | `Microsoft.OperationalInsights/workspaces/dataExports` | partial, 1 of 7 missing |  | `operationalinsights/WorkspacesDataExport` | 0 / 6 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 2 |
| 201 | `Microsoft.OperationalInsights/workspaces/tables` | partial, 1 of 20 missing |  | `operationalinsights/WorkspacesTable` | 0 / 19 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 2 |
| 202 | `Microsoft.OperationalInsights/workspaces` | partial, 3 of 23 missing | `operationalinsights/Workspace` |  | 20 / 0 / 3 | resourceGroup | extend: ASO's API version is older than the spec's | S (3) | 2 |
| 203 | `Microsoft.OperationalInsights/workspaces/linkedStorageAccounts` | covered |  | `operationalinsights/WorkspacesLinkedStorageAccount` | 0 / 1 / 0 | resourceGroup | - | - | 2 |
| 204 | `Microsoft.OperationalInsights/workspaces/linkedServices` | covered |  | `operationalinsights/WorkspacesLinkedService` | 0 / 4 / 0 | resourceGroup | - | - | 2 |
| 205 | `Microsoft.OperationalInsights/queryPacks/queries` | covered |  | `operationalinsights/QueryPacksQuery` | 0 / 8 / 0 | resourceGroup | - | - | 0 |
| 206 | `Microsoft.OperationalInsights/workspaces/savedSearches` | covered |  | `operationalinsights/WorkspacesSavedSearch` | 0 / 9 / 0 | resourceGroup | - | - | 0 |
| 207 | `Microsoft.OperationsManagement/solutions` | no kind |  |  | 0 / 0 / 9 | resourceGroup | generator | S (9) | 1 |
| 208 | `Microsoft.OperationsManagement/ManagementAssociations` | no kind |  |  | 0 / 0 / 2 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (2) | 0 |
| 209 | `Microsoft.OperationsManagement/ManagementConfigurations` | no kind |  |  | 0 / 0 / 6 | resourceGroup | generator | S (6) | 0 |
| 210 | `Microsoft.Monitor/accounts/metricsContainers` | no kind |  |  | 0 / 0 / 1 | resourceGroup | generator | S (1) | 0 |
| 211 | `Microsoft.Monitor/observabilityAgents` | no kind |  |  | 0 / 0 / 9 | resourceGroup | generator | S (9) | 0 |
| 212 | `Microsoft.Monitor/accounts/issues` | no kind |  |  | 0 / 0 / 12 | resourceGroup | generator | S (12) | 0 |
| 213 | `Microsoft.Monitor/pipelineGroups` | no kind |  |  | 0 / 0 / 52 | resourceGroup | generator | M (52) | 0 |
| 214 | `Microsoft.Monitor/slis` | no kind |  |  | 0 / 0 / 58 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | M (58) | 0 |
| 215 | `Microsoft.Monitor/accounts` | partial, 3 of 6 missing | `monitor/Account` |  | 3 / 0 / 3 | resourceGroup | extend: ASO's API version is older than the spec's | S (3) | 0 |
| 216 | `Microsoft.ContainerService/managedClusters/managedNamespaces` | no kind |  |  | 0 / 0 / 12 | resourceGroup | generator | S (12) | 1 |
| 217 | `Microsoft.ContainerService/deploymentSafeguards` | no kind |  |  | 0 / 0 / 3 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (3) | 0 |
| 218 | `Microsoft.ContainerService/managedClusters/privateEndpointConnections` | no kind |  |  | 0 / 0 / 3 | resourceGroup | generator | S (3) | 0 |
| 219 | `Microsoft.ContainerService/snapshots` | no kind |  |  | 0 / 0 / 4 | resourceGroup | generator | S (4) | 0 |
| 220 | `Microsoft.ContainerService/fleets/managedNamespaces` | no kind |  |  | 0 / 0 / 26 | resourceGroup | generator | M (26) | 0 |
| 221 | `Microsoft.ContainerService/managedClusters/agentPools` | partial, 3 of 126 missing | `containerservice/ManagedClustersAgentPool` |  | 123 / 0 / 3 | resourceGroup | extend: ASO's API version is older than the spec's | S (3) | 1 |
| 222 | `Microsoft.ContainerService/managedClusters` | partial, 21 of 330 missing | `containerservice/ManagedCluster` |  | 309 / 0 / 21 | resourceGroup | extend: ASO's API version is older than the spec's | S (21) | 1 |
| 223 | `Microsoft.ContainerService/fleets/members` | partial, 1 of 3 missing | `containerservice/FleetsMember` |  | 2 / 0 / 1 | resourceGroup | extend: ASO's API version is older than the spec's | S (1) | 0 |
| 224 | `Microsoft.ContainerService/fleets/autoUpgradeProfiles` | partial, 3 of 7 missing | `containerservice/FleetsAutoUpgradeProfile` |  | 4 / 0 / 3 | resourceGroup | extend: ASO's API version is older than the spec's | S (3) | 0 |
| 225 | `Microsoft.ContainerService/fleets/updateStrategies` | partial, 10 of 13 missing | `containerservice/FleetsUpdateStrategy` |  | 3 / 0 / 10 | resourceGroup | extend: ASO's API version is older than the spec's | S (10) | 0 |
| 226 | `Microsoft.ContainerService/fleets/updateRuns` | partial, 11 of 18 missing | `containerservice/FleetsUpdateRun` |  | 7 / 0 / 11 | resourceGroup | extend: ASO's API version is older than the spec's | S (11) | 0 |
| 227 | `Microsoft.ContainerService/managedClusters/maintenanceConfigurations` | covered | `containerservice/MaintenanceConfiguration` |  | 18 / 0 / 0 | resourceGroup | - | - | 1 |
| 228 | `Microsoft.ContainerService/managedClusters/identityBindings` | covered | `containerservice/IdentityBinding` |  | 1 / 0 / 0 | resourceGroup | - | - | 0 |
| 229 | `Microsoft.ContainerService/managedClusters/trustedAccessRoleBindings` | covered | `containerservice/TrustedAccessRoleBinding` |  | 2 / 0 / 0 | resourceGroup | - | - | 0 |
| 230 | `Microsoft.ContainerService/fleets` | covered | `containerservice/Fleet` |  | 10 / 0 / 0 | resourceGroup | - | - | 0 |
| 231 | `Microsoft.KubernetesConfiguration/privateLinkScopes` | no kind |  |  | 0 / 0 / 4 | resourceGroup | generator | S (4) | 0 |
| 232 | `Microsoft.KubernetesConfiguration/privateLinkScopes/privateEndpointConnections` | no kind |  |  | 0 / 0 / 4 | resourceGroup | generator | S (4) | 0 |
| 233 | `Microsoft.KubernetesConfiguration/sourceControlConfigurations` | no kind |  |  | 0 / 0 / 11 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (11) | 0 |
| 234 | `Microsoft.KubernetesConfiguration/extensions` | partial, 13 of 32 missing | `kubernetesconfiguration/Extension` |  | 19 / 0 / 13 | extension | extend: ASO's API version is older than the spec's | S (13) | 0 |
| 235 | `Microsoft.KubernetesConfiguration/fluxConfigurations` | partial, 1 of 71 missing | `kubernetesconfiguration/FluxConfiguration` |  | 70 / 0 / 1 | extension | extend: ASO's API version is older than the spec's | S (1) | 0 |
| 236 | `Microsoft.ContainerRegistry/registries/privateEndpointConnections` | no kind |  |  | 0 / 0 / 4 | resourceGroup | generator | S (4) | 0 |
| 237 | `Microsoft.ContainerRegistry/registries/connectedRegistries` | no kind |  |  | 0 / 0 / 13 | resourceGroup | generator | S (13) | 0 |
| 238 | `Microsoft.ContainerRegistry/registries/tasks` | no kind |  |  | 0 / 0 / 54 | resourceGroup | generator | M (54) | 0 |
| 239 | `Microsoft.ContainerRegistry/registries/webhooks` | partial, 1 of 7 missing |  | `containerregistry/RegistriesWebhook` | 0 / 6 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 240 | `Microsoft.ContainerRegistry/registries/scopeMaps` | covered |  | `containerregistry/RegistriesScopeMap` | 0 / 2 / 0 | resourceGroup | - | - | 1 |
| 241 | `Microsoft.ContainerRegistry/registries/cacheRules` | covered | `containerregistry/RegistryCacheRule` |  | 3 / 0 / 0 | resourceGroup | - | - | 1 |
| 242 | `Microsoft.ContainerRegistry/registries/replications` | covered | `containerregistry/RegistryReplication` |  | 4 / 0 / 0 | resourceGroup | - | - | 1 |
| 243 | `Microsoft.ContainerRegistry/registries/credentialSets` | covered |  | `containerregistry/RegistriesCredentialSet` | 0 / 6 / 0 | resourceGroup | - | - | 1 |
| 244 | `Microsoft.ContainerRegistry/registries/tokens` | covered |  | `containerregistry/RegistriesToken` | 0 / 9 / 0 | resourceGroup | - | - | 1 |
| 245 | `Microsoft.ContainerRegistry/registries` | covered | `containerregistry/Registry` |  | 26 / 0 / 0 | resourceGroup | - | - | 1 |
| 246 | `Microsoft.Billing/billingAccounts/billingSubscriptionAliases` | no kind |  |  | 0 / 0 / 17 | tenant | generator, tenant or subscription owner (as ASO's Alias) | S (17) | 0 |

### security-adjacent: 93 resources (82 with no kind, 7 partly covered, 4 covered)

| # | Resource | Status | ASO | Ours | Attributes: official / ours / uncovered | Scope | Route | Size | AVMs |
| ---: | --- | --- | --- | --- | --- | --- | --- | --- | ---: |
| 1 | `Microsoft.PolicyInsights/attestations` | partial, 1 of 10 missing |  | `policyinsights/PolicyAttestation` | 0 / 9 / 1 | extension,resourceGroup,subscription | extend: newer API version or property config | S (1) | 0 |
| 2 | `Microsoft.Network/securityPartnerProviders` | no kind |  |  | 0 / 0 / 4 | resourceGroup | generator | S (4) | 0 |
| 3 | `Microsoft.Network/ddosCustomPolicies` | no kind |  |  | 0 / 0 / 16 | resourceGroup | generator | S (16) | 0 |
| 4 | `Microsoft.Network/ddosProtectionPlans` | partial, 1 of 3 missing | `network/DdosProtectionPlan` |  | 2 / 0 / 1 | resourceGroup | extend: ASO's API version is older than the spec's | S (1) | 1 |
| 5 | `Microsoft.Network/networkSecurityPerimeters/profiles` | partial, 1 of 1 missing |  | `network/NetworkSecurityPerimetersProfile` | 0 / 0 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 6 | `Microsoft.Network/networkSecurityPerimeters` | partial, 1 of 3 missing |  | `network/NetworkSecurityPerimeter` | 0 / 2 / 1 | resourceGroup | extend: newer API version or property config | S (1) | 0 |
| 7 | `Microsoft.Network/ApplicationGatewayWebApplicationFirewallPolicies` | partial, 12 of 57 missing | `network/WebApplicationFirewallPolicy` |  | 45 / 0 / 12 | resourceGroup | extend: ASO's API version is older than the spec's | S (12) | 0 |
| 8 | `Microsoft.Network/FrontDoorWebApplicationFirewallPolicies` | partial, 18 of 57 missing | `network.frontdoor/WebApplicationFirewallPolicy` |  | 39 / 0 / 18 | resourceGroup | extend: ASO's API version is older than the spec's | S (18) | 0 |
| 9 | `Microsoft.Network/networkSecurityPerimeters/resourceAssociations` | covered |  | `network/NetworkSecurityPerimetersResourceAssociation` | 0 / 3 / 0 | resourceGroup | - | - | 2 |
| 10 | `Microsoft.Network/networkSecurityPerimeters/loggingConfigurations` | covered |  | `network/NetworkSecurityPerimetersLoggingConfiguration` | 0 / 2 / 0 | resourceGroup | - | - | 0 |
| 11 | `Microsoft.Network/networkSecurityPerimeters/links` | covered |  | `network/NetworkSecurityPerimetersLink` | 0 / 4 / 0 | resourceGroup | - | - | 0 |
| 12 | `Microsoft.Network/networkSecurityPerimeters/profiles/accessRules` | covered |  | `network/NetworkSecurityPerimetersProfilesAccessRule` | 0 / 7 / 0 | resourceGroup | - | - | 0 |
| 13 | `Microsoft.Security/apiCollections` | no kind |  |  | 0 / 0 / 0 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (0) | 0 |
| 14 | `Microsoft.Security/pricings/securityOperators` | no kind |  |  | 0 / 0 / 0 | subscription | generator, tenant or subscription owner (as ASO's Alias) | S (0) | 0 |
| 15 | `Microsoft.Security/serverVulnerabilityAssessments` | no kind |  |  | 0 / 0 / 0 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (0) | 0 |
| 16 | `Microsoft.Security/advancedThreatProtectionSettings` | no kind |  |  | 0 / 0 / 1 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (1) | 0 |
| 17 | `Microsoft.Security/autoProvisioningSettings` | no kind |  |  | 0 / 0 / 1 | subscription | generator, tenant or subscription owner (as ASO's Alias) | S (1) | 0 |
| 18 | `Microsoft.Security/sqlVulnerabilityAssessments` | no kind |  |  | 0 / 0 / 1 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (1) | 0 |
| 19 | `Microsoft.Security/dataScanners` | no kind |  |  | 0 / 0 / 2 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (2) | 0 |
| 20 | `Microsoft.Security/serverVulnerabilityAssessmentsSettings` | no kind |  |  | 0 / 0 / 2 | subscription | generator, tenant or subscription owner (as ASO's Alias) | S (2) | 0 |
| 21 | `Microsoft.Security/settings` | no kind |  |  | 0 / 0 / 2 | subscription | generator, tenant or subscription owner (as ASO's Alias) | S (2) | 0 |
| 22 | `Microsoft.Security/sqlVulnerabilityAssessments/baselineRules` | no kind |  |  | 0 / 0 / 2 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (2) | 0 |
| 23 | `Microsoft.Security/workspaceSettings` | no kind |  |  | 0 / 0 / 2 | subscription | generator, tenant or subscription owner (as ASO's Alias) | S (2) | 0 |
| 24 | `Microsoft.Security/privateLinks` | no kind |  |  | 0 / 0 / 3 | resourceGroup | generator | S (3) | 0 |
| 25 | `Microsoft.Security/sensitivitySettings` | no kind |  |  | 0 / 0 / 3 | tenant | generator, tenant or subscription owner (as ASO's Alias) | S (3) | 0 |
| 26 | `Microsoft.Security/serviceEntitlementSettings` | no kind |  |  | 0 / 0 / 3 | resourceGroup | generator | S (3) | 0 |
| 27 | `Microsoft.Security/applications` | no kind |  |  | 0 / 0 / 4 | extension,subscription | generator, arbitrary owner (several scopes, patch 0006) | S (4) | 0 |
| 28 | `Microsoft.Security/privateLinks/privateEndpointConnections` | no kind |  |  | 0 / 0 / 4 | resourceGroup | generator | S (4) | 0 |
| 29 | `Microsoft.Security/serviceEntitlements` | no kind |  |  | 0 / 0 / 4 | resourceGroup | generator | S (4) | 0 |
| 30 | `Microsoft.Security/securityStandards` | no kind |  |  | 0 / 0 / 6 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (6) | 0 |
| 31 | `Microsoft.Security/alertsSuppressionRules` | no kind |  |  | 0 / 0 / 7 | subscription | generator, tenant or subscription owner (as ASO's Alias) | S (7) | 0 |
| 32 | `Microsoft.Security/customRecommendations` | no kind |  |  | 0 / 0 / 7 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (7) | 0 |
| 33 | `Microsoft.Security/securityConnectors/devops/azureDevOpsOrgs` | no kind |  |  | 0 / 0 / 7 | resourceGroup | generator | S (7) | 0 |
| 34 | `Microsoft.Security/pricings` | no kind |  |  | 0 / 0 / 8 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (8) | 0 |
| 35 | `Microsoft.Security/securityConnectors/devops/azureDevOpsOrgs/projects` | no kind |  |  | 0 / 0 / 8 | resourceGroup | generator | S (8) | 0 |
| 36 | `Microsoft.Security/securityContacts` | no kind |  |  | 0 / 0 / 8 | subscription | generator, tenant or subscription owner (as ASO's Alias) | S (8) | 0 |
| 37 | `Microsoft.Security/batchPricings` | no kind |  |  | 0 / 0 / 9 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (9) | 0 |
| 38 | `Microsoft.Security/securityConnectors/devops` | no kind |  |  | 0 / 0 / 9 | resourceGroup | generator | S (9) | 0 |
| 39 | `Microsoft.Security/securityConnectors/devops/azureDevOpsOrgs/projects/repos` | no kind |  |  | 0 / 0 / 9 | resourceGroup | generator | S (9) | 0 |
| 40 | `Microsoft.Security/standards` | no kind |  |  | 0 / 0 / 9 | resourceGroup | generator | S (9) | 0 |
| 41 | `Microsoft.Security/assessments/governanceAssignments` | no kind |  |  | 0 / 0 / 10 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (10) | 0 |
| 42 | `Microsoft.Security/defenderForStorageSettings` | no kind |  |  | 0 / 0 / 10 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (10) | 0 |
| 43 | `Microsoft.Security/assignments` | no kind |  |  | 0 / 0 / 13 | resourceGroup | generator | S (13) | 0 |
| 44 | `Microsoft.Security/standardAssignments` | no kind |  |  | 0 / 0 / 13 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (13) | 0 |
| 45 | `Microsoft.Security/deviceSecurityGroups` | no kind |  |  | 0 / 0 / 15 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (15) | 0 |
| 46 | `Microsoft.Security/informationProtectionPolicies` | no kind |  |  | 0 / 0 / 15 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (15) | 0 |
| 47 | `Microsoft.Security/governanceRules` | no kind |  |  | 0 / 0 / 16 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (16) | 0 |
| 48 | `Microsoft.Security/iotSecuritySolutions` | no kind |  |  | 0 / 0 / 16 | resourceGroup | generator | S (16) | 0 |
| 49 | `Microsoft.Security/assessmentMetadata` | no kind |  |  | 0 / 0 / 18 | subscription | generator, tenant or subscription owner (as ASO's Alias) | S (18) | 0 |
| 50 | `Microsoft.Security/locations/jitNetworkAccessPolicies` | no kind |  |  | 0 / 0 / 19 | resourceGroup | generator | S (19) | 0 |
| 51 | `Microsoft.Security/automations` | no kind |  |  | 0 / 0 / 20 | resourceGroup | generator | S (20) | 0 |
| 52 | `Microsoft.Security/assessments` | no kind |  |  | 0 / 0 / 36 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | M (36) | 0 |
| 53 | `Microsoft.Security/securityConnectors` | no kind |  |  | 0 / 0 / 78 | resourceGroup | generator | M (78) | 0 |
| 54 | `Microsoft.SecurityInsights/cases/comments` | no kind |  |  | 0 / 0 / 1 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (1) | 0 |
| 55 | `Microsoft.SecurityInsights/workspaceManagerConfigurations` | no kind |  |  | 0 / 0 / 1 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (1) | 0 |
| 56 | `Microsoft.SecurityInsights/hunts/comments` | no kind |  |  | 0 / 0 / 2 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (2) | 0 |
| 57 | `Microsoft.SecurityInsights/incidents/comments` | no kind |  |  | 0 / 0 / 2 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (2) | 0 |
| 58 | `Microsoft.SecurityInsights/incidents/relations` | no kind |  |  | 0 / 0 / 2 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (2) | 0 |
| 59 | `Microsoft.SecurityInsights/onboardingStates` | no kind |  |  | 0 / 0 / 2 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (2) | 0 |
| 60 | `Microsoft.SecurityInsights/workspaceManagerAssignments` | no kind |  |  | 0 / 0 / 2 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (2) | 0 |
| 61 | `Microsoft.SecurityInsights/workspaceManagerMembers` | no kind |  |  | 0 / 0 / 2 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (2) | 0 |
| 62 | `Microsoft.SecurityInsights/alertRules/actions` | no kind |  |  | 0 / 0 / 3 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (3) | 0 |
| 63 | `Microsoft.SecurityInsights/hunts/relations` | no kind |  |  | 0 / 0 / 3 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (3) | 0 |
| 64 | `Microsoft.SecurityInsights/settings` | no kind |  |  | 0 / 0 / 3 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (3) | 0 |
| 65 | `Microsoft.SecurityInsights/workspaceManagerGroups` | no kind |  |  | 0 / 0 / 3 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (3) | 0 |
| 66 | `Microsoft.SecurityInsights/alertRules` | no kind |  |  | 0 / 0 / 4 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (4) | 0 |
| 67 | `Microsoft.SecurityInsights/dataConnectors` | no kind |  |  | 0 / 0 / 4 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (4) | 0 |
| 68 | `Microsoft.SecurityInsights/fileImports` | no kind |  |  | 0 / 0 / 6 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (6) | 0 |
| 69 | `Microsoft.SecurityInsights/cases/relations` | no kind |  |  | 0 / 0 / 8 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (8) | 0 |
| 70 | `Microsoft.SecurityInsights/cases` | no kind |  |  | 0 / 0 / 11 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (11) | 0 |
| 71 | `Microsoft.SecurityInsights/entityQueries` | no kind |  |  | 0 / 0 / 11 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (11) | 0 |
| 72 | `Microsoft.SecurityInsights/watchlists/watchlistItems` | no kind |  |  | 0 / 0 / 11 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (11) | 0 |
| 73 | `Microsoft.SecurityInsights/incidents/tasks` | no kind |  |  | 0 / 0 / 12 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (12) | 0 |
| 74 | `Microsoft.SecurityInsights/hunts` | no kind |  |  | 0 / 0 / 13 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (13) | 0 |
| 75 | `Microsoft.SecurityInsights/automationRules` | no kind |  |  | 0 / 0 / 14 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (14) | 0 |
| 76 | `Microsoft.SecurityInsights/incidents` | no kind |  |  | 0 / 0 / 16 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (16) | 0 |
| 77 | `Microsoft.SecurityInsights/securityMLAnalyticsSettings` | no kind |  |  | 0 / 0 / 16 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (16) | 0 |
| 78 | `Microsoft.SecurityInsights/sourcecontrols` | no kind |  |  | 0 / 0 / 16 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (16) | 0 |
| 79 | `Microsoft.SecurityInsights/bookmarks` | no kind |  |  | 0 / 0 / 17 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (17) | 0 |
| 80 | `Microsoft.SecurityInsights/watchlists` | no kind |  |  | 0 / 0 / 22 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | S (22) | 0 |
| 81 | `Microsoft.SecurityInsights/metadata` | no kind |  |  | 0 / 0 / 33 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | M (33) | 0 |
| 82 | `Microsoft.SecurityInsights/contentPackages` | no kind |  |  | 0 / 0 / 37 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | M (37) | 0 |
| 83 | `Microsoft.SecurityInsights/dataConnectorDefinitions` | no kind |  |  | 0 / 0 / 38 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | M (38) | 0 |
| 84 | `Microsoft.SecurityInsights/contentTemplates` | no kind |  |  | 0 / 0 / 39 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | M (39) | 0 |
| 85 | `Microsoft.SecurityInsights/threatIntelligence/indicators` | no kind |  |  | 0 / 0 / 39 | extension | generator, arbitrary owner (as ASO's DiagnosticSetting) | M (39) | 0 |
| 86 | `Microsoft.Purview/accounts/privateEndpointConnections` | no kind |  |  | 0 / 0 / 4 | resourceGroup | generator | S (4) | 0 |
| 87 | `Microsoft.Purview/accounts/kafkaConfigurations` | no kind |  |  | 0 / 0 / 8 | resourceGroup | generator | S (8) | 0 |
| 88 | `Microsoft.Purview/accounts` | no kind |  |  | 0 / 0 / 9 | resourceGroup | generator | S (9) | 0 |
| 89 | `Microsoft.HardwareSecurityModules/cloudHsmClusters/privateEndpointConnections` | no kind |  |  | 0 / 0 / 5 | resourceGroup | generator | S (5) | 0 |
| 90 | `Microsoft.HardwareSecurityModules/cloudHsmClusters` | no kind |  |  | 0 / 0 / 9 | resourceGroup | generator | S (9) | 0 |
| 91 | `Microsoft.HardwareSecurityModules/dedicatedHSMs` | no kind |  |  | 0 / 0 / 9 | resourceGroup | generator | S (9) | 0 |
| 92 | `Microsoft.Cdn/cdnWebApplicationFirewallPolicies` | no kind |  |  | 0 / 0 / 39 | resourceGroup | generator | M (39) | 0 |
| 93 | `Microsoft.Cdn/profiles/securityPolicies` | partial, 2 of 6 missing | `cdn/SecurityPolicy` |  | 4 / 0 / 2 | resourceGroup | extend: ASO's API version is older than the spec's | S (2) | 0 |
<!-- foundation:end -->
