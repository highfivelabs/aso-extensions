# The catalog's Azure resource types

Classified 2026-11-12 from the catalog repository's
`docs/coverage/resource-types.csv` on `origin/main` (`highfivelabs/catalog`,
the Terraform modules its Azure charts will be built from), where every Azure
row was still class `unclassified`: the Azure charts are not started there.
`catalog-azure.csv` beside this file is the same table as data. The catalog's
`docs/coverage/blocked-kinds.md` had not landed on `origin/main` that day.

Each Terraform resource type (and each ARM type the modules make through
`azapi_resource`, `azapi_update_resource` and `azapi_resource_action`, as
`azapi_<resource>:<ARM type>`) is matched to the Kubernetes kind that covers
it: one of Azure Service Operator's at the pin (`UPSTREAM.md`), one of ours,
or none. A Terraform association resource (a NIC's backend pool, a subnet's
route table) is covered when the owning kind carries the attribute.
"modules" is how many catalog modules use the type.

| Class | Types |
| --- | ---: |
| ASO has it | 94 |
| this repository has it | 29 |
| built here for the catalog (2026-11-12) | 5 |
| neither | 5 |
| generic azapi resource | 3 |
| total | 136 |

The most used types are covered: `azurerm_role_assignment` (19 modules) and
`azurerm_monitor_diagnostic_setting` (13) by ASO, `azurerm_management_lock`
(17) here, `azurerm_private_endpoint` and its application security group
association (6 each) and `azurerm_public_ip` (3) by ASO,
`azurerm_key_vault_secret` (3) here. Every type the catalog's modules use
in more than one module is covered. What was uncovered (one module each)
and could be generated was built on 2026-11-12: Cosmos DB Gremlin databases
and graphs and the SQL dedicated gateway (`documentdb.azure.hfl.dev`, with
the rest of Cosmos DB's children ASO lacks), Cognitive Services RAI policies
(`cognitiveservices.azure.hfl.dev`, with the account's other children) and
the Automation account (`automation.azure.hfl.dev`, with its children).

## azapi_resource by ARM type

`azapi_resource` (22 modules) is generic. Read from the modules' sources
(the catalog's `.cache/upstream/Azure`, examples and tests left out), the
ARM types they make through it beyond the rows below are:
`Microsoft.Resources/deployments` (17 modules: AVM's telemetry deployment,
an empty template, no platform need; ASO prunes deployments, so neither
covers it); locks, role assignments, diagnostic settings, private endpoints
and their DNS zone groups through `var.resource_types` (ASO and ours, as
above); and per module, the resource itself (a vault, a site, a storage
account: ASO). The Container Apps environment's HTTP route configs and
maintenance configurations are built here since 2026-11-12
(`app.azure.hfl.dev/v20260101`, `docs/plan.md` c3b), and since 2026-11-13
the environment's Dapr subscriptions and .NET components (c3c), Application
Insights linked storage accounts (`microsoft.insights/components/linkedStorageAccounts`,
`insights.azure.hfl.dev` `ComponentsLinkedStorageAccount`, c21) and the App
Service config sections `authsettingsV2`, `logs` and `slotConfigNames` with
the ftp and scm publishing policies (`web.azure.hfl.dev`, c4b). Uncovered
among them: the App Service **`config/metadata`** section and the slot swap
action (`metadata` is PUT-only, read with a POST, as `authsettings` and
`backup` are, open question 13).

## Actions

`azapi_resource_action` (5 modules) is an imperative call, not a resource:
AKS kubeconfig reads (ASO's `ManagedCluster` exports them to Secrets), a
Container App post-creation PATCH (ASO's `ContainerApp`), management group
placement (our `ManagementGroupsSubscription`), the App Service slot swap
and `config/metadata` PUT (not covered), and subscription actions:
resource provider and feature registration, rename, cancel (not covered;
`Microsoft.Features/featureProviders/subscriptionFeatureRegistrations` is a
PUT resource and a candidate).

## Every type

| Type | Modules | Class | Kind |
| --- | ---: | --- | --- |
| `azapi_resource` | 22 | generic azapi resource | generic: the ARM types it makes are listed as azapi_resource:<type> rows and in "azapi_resource by ARM type" below |
| `azurerm_role_assignment` | 19 | ASO has it | authorization RoleAssignment |
| `azurerm_management_lock` | 17 | this repository has it | authorization.azure.hfl.dev ManagementLock |
| `azurerm_monitor_diagnostic_setting` | 13 | ASO has it | insights DiagnosticSetting |
| `azurerm_private_endpoint_application_security_group_association` | 6 | ASO has it | network PrivateEndpoint (applicationSecurityGroups) |
| `azurerm_private_endpoint` | 6 | ASO has it | network PrivateEndpoint |
| `azapi_update_resource` | 5 | generic azapi resource | generic: see its azapi_update_resource:<type> rows |
| `azapi_resource_action` | 5 | generic azapi resource | generic: see its azapi_resource_action:<type> rows and "Actions" below |
| `azurerm_public_ip` | 3 | ASO has it | network PublicIPAddress |
| `azurerm_key_vault_secret` | 3 | this repository has it | keyvault.azure.hfl.dev VaultSecret |
| `azurerm_user_assigned_identity` | 2 | ASO has it | managedidentity UserAssignedIdentity |
| `azurerm_network_interface_backend_address_pool_association` | 2 | ASO has it | network NetworkInterface (ipConfigurations) |
| `azurerm_log_analytics_workspace` | 2 | ASO has it | operationalinsights Workspace |
| `azurerm_log_analytics_solution` | 2 | this repository has it | operationsmanagement.azure.hfl.dev Solution |
| `azurerm_log_analytics_linked_service` | 2 | this repository has it | operationalinsights.azure.hfl.dev WorkspacesLinkedService |
| `azapi_resource:Microsoft.Resources/resourceGroups` | 2 | ASO has it | resources ResourceGroup |
| `azapi_resource:Microsoft.Authorization/locks` | 2 | this repository has it | ManagementLock |
| `azurerm_windows_virtual_machine` | 1 | ASO has it | compute VirtualMachine |
| `azurerm_virtual_machine_run_command` | 1 | this repository has it | compute.azure.hfl.dev VirtualMachinesRunCommand |
| `azurerm_virtual_machine_extension` | 1 | ASO has it | compute VirtualMachinesExtension |
| `azurerm_virtual_machine_data_disk_attachment` | 1 | ASO has it | compute VirtualMachine (dataDisks) |
| `azurerm_subnet_route_table_association` | 1 | ASO has it | network VirtualNetworksSubnet (routeTable) |
| `azurerm_route_table` | 1 | ASO has it | network RouteTable |
| `azurerm_route` | 1 | ASO has it | network RouteTablesRoute |
| `azurerm_resource_group` | 1 | ASO has it | resources ResourceGroup |
| `azurerm_redis_linked_server` | 1 | ASO has it | cache RedisLinkedServer |
| `azurerm_redis_firewall_rule` | 1 | ASO has it | cache RedisFirewallRule |
| `azurerm_redis_cache_access_policy_assignment` | 1 | ASO has it | cache RedisAccessPolicyAssignment |
| `azurerm_redis_cache_access_policy` | 1 | ASO has it | cache RedisAccessPolicy |
| `azurerm_redis_cache` | 1 | ASO has it | cache Redis |
| `azurerm_private_dns_resolver_virtual_network_link` | 1 | ASO has it | network DnsForwardingRuleSetsVirtualNetworkLink |
| `azurerm_private_dns_resolver_outbound_endpoint` | 1 | ASO has it | network DnsResolversOutboundEndpoint |
| `azurerm_private_dns_resolver_inbound_endpoint` | 1 | ASO has it | network DnsResolversInboundEndpoint |
| `azurerm_private_dns_resolver_forwarding_rule` | 1 | ASO has it | network DnsForwardingRuleSetsForwardingRule |
| `azurerm_private_dns_resolver_dns_forwarding_ruleset` | 1 | ASO has it | network DnsForwardingRuleset |
| `azurerm_private_dns_resolver` | 1 | ASO has it | network DnsResolver |
| `azurerm_postgresql_flexible_server_virtual_endpoint` | 1 | ASO has it | dbforpostgresql FlexibleServersVirtualEndpoint |
| `azurerm_postgresql_flexible_server_firewall_rule` | 1 | ASO has it | dbforpostgresql FlexibleServersFirewallRule |
| `azurerm_postgresql_flexible_server_database` | 1 | ASO has it | dbforpostgresql FlexibleServersDatabase |
| `azurerm_postgresql_flexible_server_configuration` | 1 | ASO has it | dbforpostgresql FlexibleServersConfiguration |
| `azurerm_postgresql_flexible_server_active_directory_administrator` | 1 | ASO has it | dbforpostgresql FlexibleServersAdministrator |
| `azurerm_postgresql_flexible_server` | 1 | ASO has it | dbforpostgresql FlexibleServer |
| `azurerm_network_interface_security_group_association` | 1 | ASO has it | network NetworkInterface (networkSecurityGroup) |
| `azurerm_network_interface_nat_rule_association` | 1 | ASO has it | network NetworkInterface (loadBalancerInboundNatRules) |
| `azurerm_network_interface_application_security_group_association` | 1 | ASO has it | network NetworkInterface (applicationSecurityGroups) |
| `azurerm_network_interface_application_gateway_backend_address_pool_association` | 1 | ASO has it | network NetworkInterface (applicationGatewayBackendAddressPools) |
| `azurerm_network_interface` | 1 | ASO has it | network NetworkInterface |
| `azurerm_network_ddos_protection_plan` | 1 | ASO has it | network DdosProtectionPlan |
| `azurerm_mssql_server` | 1 | ASO has it | sql Server |
| `azurerm_mssql_firewall_rule` | 1 | ASO has it | sql ServersFirewallRule |
| `azurerm_mssql_elasticpool` | 1 | ASO has it | sql ServersElasticPool |
| `azurerm_mssql_database` | 1 | ASO has it | sql ServersDatabase |
| `azurerm_monitor_private_link_scoped_service` | 1 | this repository has it | insights.azure.hfl.dev PrivateLinkScopesScopedResource |
| `azurerm_managed_disk` | 1 | ASO has it | compute Disk |
| `azurerm_log_analytics_linked_storage_account` | 1 | this repository has it | operationalinsights.azure.hfl.dev WorkspacesLinkedStorageAccount |
| `azurerm_linux_virtual_machine` | 1 | ASO has it | compute VirtualMachine |
| `azurerm_lb_rule` | 1 | ASO has it | network LoadBalancer (loadBalancingRules) |
| `azurerm_lb_probe` | 1 | ASO has it | network LoadBalancer (probes) |
| `azurerm_lb_outbound_rule` | 1 | ASO has it | network LoadBalancer (outboundRules) |
| `azurerm_lb_nat_rule` | 1 | ASO has it | network LoadBalancersInboundNatRule |
| `azurerm_lb_nat_pool` | 1 | ASO has it | network LoadBalancer (inboundNatPools) |
| `azurerm_lb_backend_address_pool_address` | 1 | ASO has it | network LoadBalancer (backendAddressPools[].loadBalancerBackendAddresses) |
| `azurerm_lb_backend_address_pool` | 1 | ASO has it | network LoadBalancer (backendAddressPools) |
| `azurerm_lb` | 1 | ASO has it | network LoadBalancer |
| `azurerm_key_vault_key` | 1 | ASO has it | keyvault VaultKey (at the pin, #5564) |
| `azurerm_key_vault_certificate_contacts` | 1 | neither | Key Vault data plane (certificate contacts of a vault): a hand-written data-plane kind beside VaultCertificate; not built |
| `azurerm_firewall_policy_rule_collection_group` | 1 | ASO has it | network FirewallPoliciesRuleCollectionGroup |
| `azurerm_firewall_policy` | 1 | ASO has it | network FirewallPolicy |
| `azurerm_federated_identity_credential` | 1 | ASO has it | managedidentity FederatedIdentityCredential |
| `azurerm_dev_test_global_vm_shutdown_schedule` | 1 | this repository has it | devtestlab.azure.hfl.dev Schedule |
| `azurerm_cosmosdb_sql_trigger` | 1 | ASO has it | documentdb SqlDatabaseContainerTrigger |
| `azurerm_cosmosdb_sql_stored_procedure` | 1 | ASO has it | documentdb SqlDatabaseContainerStoredProcedure |
| `azurerm_cosmosdb_sql_function` | 1 | ASO has it | documentdb SqlDatabaseContainerUserDefinedFunction |
| `azurerm_cosmosdb_sql_dedicated_gateway` | 1 | built here for the catalog (2026-11-12) | documentdb.azure.hfl.dev DatabaseAccountsService |
| `azurerm_cosmosdb_sql_database` | 1 | ASO has it | documentdb SqlDatabase |
| `azurerm_cosmosdb_sql_container` | 1 | ASO has it | documentdb SqlDatabaseContainer |
| `azurerm_cosmosdb_mongo_database` | 1 | ASO has it | documentdb MongodbDatabase |
| `azurerm_cosmosdb_mongo_collection` | 1 | ASO has it | documentdb MongodbDatabaseCollection |
| `azurerm_cosmosdb_gremlin_graph` | 1 | built here for the catalog (2026-11-12) | documentdb.azure.hfl.dev DatabaseAccountsGremlinDatabasesGraph |
| `azurerm_cosmosdb_gremlin_database` | 1 | built here for the catalog (2026-11-12) | documentdb.azure.hfl.dev DatabaseAccountsGremlinDatabase |
| `azurerm_cosmosdb_account` | 1 | ASO has it | documentdb DatabaseAccount |
| `azurerm_container_registry_token_password` | 1 | neither | a token password is the generateCredentials action's answer, returned once (c9): a hand-written kind that writes it to a Secret; not built |
| `azurerm_container_registry_token` | 1 | this repository has it | containerregistry.azure.hfl.dev RegistriesToken |
| `azurerm_container_registry_scope_map` | 1 | this repository has it | containerregistry.azure.hfl.dev RegistriesScopeMap |
| `azurerm_container_registry` | 1 | ASO has it | containerregistry Registry |
| `azurerm_cognitive_account_customer_managed_key` | 1 | ASO has it | cognitiveservices Account (encryption) |
| `azurerm_automation_account` | 1 | built here for the catalog (2026-11-12) | automation.azure.hfl.dev AutomationAccount |
| `azuread_directory_role_assignment` | 1 | this repository has it | entra.azure.hfl.dev DirectoryRoleAssignment |
| `azuread_directory_role` | 1 | neither | activating a directory role from its template (Microsoft Graph directoryRoles): a hand-written Graph kind beside DirectoryRoleAssignment; Graph activates a built-in role on its first assignment, so it is rarely needed; not built |
| `azapi_update_resource:Microsoft.Resources/tags` | 1 | this repository has it | resources.azure.hfl.dev TagSet |
| `azapi_update_resource:Microsoft.OperationalInsights/workspaces/tables` | 1 | this repository has it | operationalinsights.azure.hfl.dev WorkspacesTable |
| `azapi_update_resource:Microsoft.OperationalInsights/workspaces` | 1 | ASO has it | operationalinsights Workspace |
| `azapi_update_resource:Microsoft.Network/virtualNetworks/virtualNetworkPeerings` | 1 | ASO has it | network VirtualNetworksVirtualNetworkPeering |
| `azapi_update_resource:Microsoft.Network/privateDnsZones/SOA` | 1 | this repository has it | network.azure.hfl.dev PrivateDnsZonesSOARecord |
| `azapi_update_resource:microsoft.insights/privateLinkScopes` | 1 | this repository has it | insights.azure.hfl.dev PrivateLinkScope |
| `azapi_update_resource:Microsoft.CognitiveServices/accounts` | 1 | ASO has it | cognitiveservices Account |
| `azapi_resource:Microsoft.Subscription/aliases` | 1 | ASO has it | subscription Alias |
| `azapi_resource:Microsoft.SecurityInsights/onboardingStates` | 1 | this repository has it | securityinsights.azure.hfl.dev OnboardingState |
| `azapi_resource:Microsoft.RecoveryServices/vaults/backupFabrics/protectionContainers/protectedItems` | 1 | neither | Recovery Services protected item (backup for a VM): its path has a fixed backupFabrics/Azure segment the generator cannot own through; a hand-written candidate (docs/handoff.md h1.2); not built |
| `azapi_resource:Microsoft.OperationalInsights/workspaces/tables` | 1 | this repository has it | WorkspacesTable |
| `azapi_resource:Microsoft.OperationalInsights/workspaces/dataExports` | 1 | this repository has it | operationalinsights.azure.hfl.dev WorkspacesDataExport |
| `azapi_resource:Microsoft.OperationalInsights/queryPacks/queries` | 1 | this repository has it | operationalinsights.azure.hfl.dev QueryPacksQuery |
| `azapi_resource:Microsoft.OperationalInsights/queryPacks` | 1 | this repository has it | operationalinsights.azure.hfl.dev QueryPack |
| `azapi_resource:Microsoft.Network/virtualNetworks/virtualNetworkPeerings` | 1 | ASO has it | network VirtualNetworksVirtualNetworkPeering |
| `azapi_resource:Microsoft.Network/virtualNetworks/subnets` | 1 | ASO has it | network VirtualNetworksSubnet |
| `azapi_resource:Microsoft.Network/virtualNetworks` | 1 | ASO has it | network VirtualNetwork |
| `azapi_resource:Microsoft.Network/virtualHubs/hubVirtualNetworkConnections` | 1 | this repository has it | network.azure.hfl.dev VirtualHubsHubVirtualNetworkConnection |
| `azapi_resource:Microsoft.Network/routeTables/routes` | 1 | ASO has it | network RouteTablesRoute |
| `azapi_resource:Microsoft.Network/publicIPAddresses` | 1 | ASO has it | network PublicIPAddress |
| `azapi_resource:Microsoft.Network/privateDnsZones/virtualNetworkLinks` | 1 | ASO has it | network PrivateDnsZonesVirtualNetworkLink |
| `azapi_resource:Microsoft.Network/privateDnsZones/TXT` | 1 | ASO has it | network PrivateDnsZonesTXTRecord |
| `azapi_resource:Microsoft.Network/privateDnsZones/SRV` | 1 | ASO has it | network PrivateDnsZonesSRVRecord |
| `azapi_resource:Microsoft.Network/privateDnsZones/SOA` | 1 | this repository has it | network.azure.hfl.dev PrivateDnsZonesSOARecord |
| `azapi_resource:Microsoft.Network/privateDnsZones/PTR` | 1 | ASO has it | network PrivateDnsZonesPTRRecord |
| `azapi_resource:Microsoft.Network/privateDnsZones/MX` | 1 | ASO has it | network PrivateDnsZonesMXRecord |
| `azapi_resource:Microsoft.Network/privateDnsZones/CNAME` | 1 | ASO has it | network PrivateDnsZonesCNAMERecord |
| `azapi_resource:Microsoft.Network/privateDnsZones/AAAA` | 1 | ASO has it | network PrivateDnsZonesAAAARecord |
| `azapi_resource:Microsoft.Network/privateDnsZones/A` | 1 | ASO has it | network PrivateDnsZonesARecord |
| `azapi_resource:Microsoft.Network/privateDnsZones` | 1 | ASO has it | network PrivateDnsZone |
| `azapi_resource:Microsoft.Network/networkSecurityPerimeters/resourceAssociations` | 1 | this repository has it | network.azure.hfl.dev NetworkSecurityPerimetersResourceAssociation |
| `azapi_resource:Microsoft.Network/natGateways` | 1 | ASO has it | network NatGateway |
| `azapi_resource:Microsoft.Network/bastionHosts` | 1 | ASO has it | network BastionHost |
| `azapi_resource:Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials` | 1 | ASO has it | managedidentity FederatedIdentityCredential |
| `azapi_resource:Microsoft.ManagedIdentity/userAssignedIdentities` | 1 | ASO has it | managedidentity UserAssignedIdentity |
| `azapi_resource:Microsoft.Maintenance/configurationAssignments` | 1 | this repository has it | maintenance.azure.hfl.dev ConfigurationAssignment |
| `azapi_resource:Microsoft.Insights/privateLinkScopes/scopedResources` | 1 | this repository has it | PrivateLinkScopesScopedResource |
| `azapi_resource:microsoft.insights/privateLinkScopes` | 1 | this repository has it | insights.azure.hfl.dev PrivateLinkScope |
| `azapi_resource:Microsoft.Consumption/budgets` | 1 | this repository has it | consumption.azure.hfl.dev Budget |
| `azapi_resource:Microsoft.CognitiveServices/accounts/raiPolicies` | 1 | built here for the catalog (2026-11-12) | cognitiveservices.azure.hfl.dev AccountsRaiPolicy |
| `azapi_resource:Microsoft.CognitiveServices/accounts/deployments` | 1 | ASO has it | cognitiveservices Deployment |
| `azapi_resource:Microsoft.CognitiveServices/accounts` | 1 | ASO has it | cognitiveservices Account |
| `azapi_resource:Microsoft.Authorization/roleAssignments` | 1 | ASO has it | authorization RoleAssignment |
| `azapi_resource:Microsoft.App/containerApps/authConfigs` | 1 | ASO has it | app AuthConfig |
| `azapi_resource:Microsoft.App/containerApps` | 1 | ASO has it | app ContainerApp |
| `azapi_resource_action:Microsoft.Resources/subscriptions` | 1 | neither | actions on a subscription: resource provider registration, rename, cancel (POST); no desired state to own; not built |
| `azapi_resource_action:Microsoft.Management/managementGroups/subscriptions` | 1 | this repository has it | management.azure.hfl.dev ManagementGroupsSubscription |
