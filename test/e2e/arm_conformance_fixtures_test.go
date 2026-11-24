// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

// What a kind needs beyond the smallest object its CRD accepts, because
// the fake's rules for it (pkg/fakecloud/arm) ask for more, as Azure does.
// A new kind needs an entry only when the conformance test fails for it
// with a refusal the fake documents; see docs/testing.md.

const conformancePrincipal = "00000000-0000-0000-0000-0000000000a1"

func init() {
	detach := map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}
	f := map[string]conformanceFixture{
		// Log Analytics.
		"Cluster": {spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"sku": map[string]any{"name": "CapacityReservation", "capacity": int64(100)}}
		}},
		"Solution": {coveredBy: "TestOperationsManagementSolution"},

		// Maintenance.
		// On a virtual machine (at a subscription or resource group it would
		// need an InGuestPatch configuration and a filter).
		"ConfigurationAssignment": {
			owner: func(c *conformanceContext) string { return c.add(c.in("Microsoft.Compute/virtualMachines/vm")) },
			spec: func(c *conformanceContext) map[string]any {
				return map[string]any{"maintenanceConfigurationReference": armRef(c.add(c.in("Microsoft.Maintenance/maintenanceConfigurations/mc")))}
			}},

		// Cost Management: a destination storage account by ARM ID.
		"Export": {spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"deliveryInfo": map[string]any{"destination": map[string]any{
				"container": "exports", "resourceReference": armRef(c.ref)}}}
		}},

		// Key Vault.
		"ManagedHSM": {spec: func(c *conformanceContext) map[string]any {
			return map[string]any{
				"sku":        map[string]any{"family": "B", "name": "Standard_B1"},
				"properties": map[string]any{"tenantId": tenantID, "initialAdminObjectIds": []any{conformancePrincipal}},
			}
		}},
		// A PUT on a key that exists returns it unchanged, and Resource
		// Manager cannot delete a key: the object is detached.
		"ManagedHSMsKey": {keeps: true, annotations: detach, stays: true, spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"properties": map[string]any{"kty": "RSA-HSM"}}
		}},
		// A private endpoint makes the connection; a PUT only changes it.
		"ManagedHSMsPrivateEndpointConnection": {setup: func(c *conformanceContext) {
			c.add(c.owner + "/privateEndpointConnections/conformance")
		}},
		"VaultsPrivateEndpointConnection": {setup: func(c *conformanceContext) {
			c.add(c.owner + "/privateEndpointConnections/conformance")
		}},

		// Azure Policy: definitions live at a subscription or management
		// group; assignments name what exists.
		"PolicyDefinition":    {owner: func(c *conformanceContext) string { return "/subscriptions/" + subID }, coveredBy: "TestAzurePolicy"},
		"PolicySetDefinition": {coveredBy: "TestAzurePolicy"},
		"PolicyAssignment":    {coveredBy: "TestAzurePolicy"},
		"PolicyExemption":     {coveredBy: "TestAzurePolicy"},
		"PolicyAttestation":   {coveredBy: "TestPolicyRemediationAndAttestation"},
		"PolicyRemediation":   {coveredBy: "TestPolicyRemediationAndAttestation"},

		// Privileged Identity Management: a request cannot be deleted, so the
		// object is detached and the request stays.
		"RoleAssignmentScheduleRequest": {annotations: detach, stays: true, spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"principalId": conformancePrincipal, "roleDefinitionReference": map[string]any{"wellKnownName": "Reader"}}
		}},
		"RoleEligibilityScheduleRequest": {annotations: detach, stays: true, spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"principalId": conformancePrincipal, "roleDefinitionReference": map[string]any{"wellKnownName": "Reader"}}
		}},
		"RoleManagementPolicyAssignment": {coveredBy: "TestPrivilegedIdentityManagement"},

		// Site-to-site VPN and ExpressRoute.
		"LocalNetworkGateway": {spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"gatewayIpAddress": "203.0.113.1",
				"localNetworkAddressSpace": map[string]any{"addressPrefixes": []any{"10.1.0.0/16"}}}
		}},
		"VirtualNetworkGatewayConnection":            {coveredBy: "TestVPNConnection"},
		"ExpressRouteCircuit":                        {coveredBy: "TestExpressRouteCircuit"},
		"ExpressRouteCircuitsPeering":                {coveredBy: "TestExpressRouteCircuit"},
		"ExpressRouteCircuitsPeeringsConnection":     {coveredBy: "TestExpressRouteCircuit"},
		"ExpressRoutePort":                           {coveredBy: "TestExpressRouteCircuit"},
		"ExpressRouteGateway":                        {coveredBy: "TestExpressRouteGateway"},
		"ExpressRouteGatewaysExpressRouteConnection": {coveredBy: "TestExpressRouteGateway"},

		// Virtual WAN.
		"VirtualHub": {spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"virtualWan": embedRef(c.add(c.in("Microsoft.Network/virtualWans/wan"))), "addressPrefix": "10.100.0.0/24"}
		}},
		"VpnSite": {spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"virtualWan": embedRef(c.add(c.in("Microsoft.Network/virtualWans/wan"))), "ipAddress": "203.0.113.2"}
		}},
		"VirtualHubsHubVirtualNetworkConnection": {spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"remoteVirtualNetwork": embedRef(c.add(c.in("Microsoft.Network/virtualNetworks/vnet")))}
		}},
		"VirtualHubsIpConfiguration": {spec: func(c *conformanceContext) map[string]any {
			vnet := c.add(c.in("Microsoft.Network/virtualNetworks/vnet"))
			return map[string]any{"subnet": embedRef(c.add(vnet + "/subnets/RouteServerSubnet"))}
		}},
		"VirtualHubsBgpConnection": {spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"peerAsn": int64(65010), "peerIp": "10.0.0.4"}
		}},
		"VpnGateway":               {coveredBy: "TestVPNGateway"},
		"P2sVpnGateway":            {coveredBy: "TestVPNGateway"},
		"VpnGatewaysVpnConnection": {coveredBy: "TestVPNGateway"},
		"VpnGatewaysNatRule": {spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"mode": "EgressSnat", "type": "Static",
				"internalMappings": []any{map[string]any{"addressSpace": "10.0.0.0/24"}},
				"externalMappings": []any{map[string]any{"addressSpace": "192.168.0.0/24"}}}
		}},
		"VpnServerConfiguration": {coveredBy: "TestVPNGateway"},

		// Virtual Network Manager.
		"NetworkManager": {spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"networkManagerScopes": map[string]any{"subscriptions": []any{"/subscriptions/" + subID}}}
		}},
		"NetworkManagersScopeConnection": {spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"tenantId": tenantID, "resourceReference": armRef("/subscriptions/" + subID)}
		}},
		"NetworkManagersNetworkGroupsStaticMember": {spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"resourceReference": armRef(c.add(c.in("Microsoft.Network/virtualNetworks/vnet")))}
		}},
		"NetworkManagersConnectivityConfiguration": {spec: func(c *conformanceContext) map[string]any {
			group := c.add(c.owner + "/networkGroups/g")
			return map[string]any{"connectivityTopology": "Mesh",
				"appliesToGroups": []any{map[string]any{"groupConnectivity": "None", "networkGroupReference": armRef(group)}}}
		}},
		"NetworkManagersSecurityAdminRuleCollection": {spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"appliesToGroups": []any{map[string]any{"networkGroupReference": armRef(c.add(c.parent() + "/networkGroups/g"))}}}
		}},
		"NetworkManagersSecurityUserRuleCollection": {spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"appliesToGroups": []any{map[string]any{"networkGroupReference": armRef(c.add(c.parent() + "/networkGroups/g"))}}}
		}},
		"NetworkManagersRoutingRuleCollection": {spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"appliesTo": []any{map[string]any{"networkGroupReference": armRef(c.add(c.parent() + "/networkGroups/g"))}}}
		}},
		// Polymorphic: exactly one of adminRule and defaultAdminRule.
		"NetworkManagersSecurityAdminRule": {spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"adminRule": map[string]any{"kind": "Custom", "access": "Allow", "direction": "Inbound", "priority": int64(100), "protocol": "Tcp"}}
		}},
		"NetworkManagersCommit": {coveredBy: "TestNetworkManagerRouting"},

		// Defender for Cloud: a setting is polymorphic, exactly one leaf set.
		"Setting": {spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"azureName": "MCAS", "dataExportSettings": map[string]any{"kind": "DataExportSettings", "enabled": true}}
		}},
	}
	// Microsoft Sentinel: polymorphic resources and actions, exactly one
	// leaf set.
	sentinel := map[string]conformanceFixture{
		"AlertRule": {spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"fusionAlertRule": map[string]any{"kind": "Fusion", "enabled": true,
				"alertRuleTemplateName": "f71aba3d-28fb-450b-b192-4e76a83015c8"}}
		}},
		"DataConnector": {spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"ascDataConnector": map[string]any{"kind": "AzureSecurityCenter"}}
		}},
		"AutomationRule": {spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"actions": []any{map[string]any{"modifyProperties": map[string]any{
				"actionType": "ModifyProperties", "order": int64(1),
				"actionConfiguration": map[string]any{"status": "Closed"}}}}}
		}},
	}
	// Deployment scripts: polymorphic (Azure PowerShell or Azure CLI).
	sentinel["DeploymentScript"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"azureCliScript": map[string]any{"kind": "AzureCLI", "location": "westus",
			"azCliVersion": "2.60.0", "retentionInterval": "PT1H", "scriptContent": "echo conformance"}}
	}}
	for k, v := range sentinel {
		f[k] = v
	}
	// AKS: a node pool snapshot names the agent pool it is taken of, which
	// must exist.
	f["Snapshot"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		cluster := c.add(c.in("Microsoft.ContainerService/managedClusters/aks"))
		return map[string]any{"creationData": map[string]any{"sourceResourceReference": armRef(c.add(cluster + "/agentPools/system"))}}
	}}
	// Container registry tasks: a step is polymorphic (Docker, encoded or
	// file task), exactly one set.
	f["RegistriesTask"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"platform": map[string]any{"os": "Linux"},
			"step": map[string]any{"encodedTask": map[string]any{"type": "EncodedTask", "encodedTaskContent": "dmVyc2lvbjogdjEuMS4wCnN0ZXBzOiBbXQo="}}}
	}}
	// Cognitive Services: a connection's properties are polymorphic on its
	// authType, exactly one set (with none set the payload cannot be built).
	connection := conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"properties": map[string]any{"none": map[string]any{
			"authType": "None", "category": "GenericHttp", "target": "https://example.com"}}}
	}}
	f["AccountsConnection"] = connection
	f["AccountsProjectsConnection"] = connection
	// Kubernetes configuration: a source control configuration is on a
	// managed or Arc-enabled cluster.
	f["SourceControlConfiguration"] = conformanceFixture{
		owner: func(c *conformanceContext) string {
			return c.add(c.in("Microsoft.ContainerService/managedClusters/aks"))
		},
		spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"repositoryUrl": "https://github.com/example/cluster-config"}
		}}
	// Kinds whose smallest valid object names others the fake checks (a
	// destination, domain lists, service resources): their own tests run
	// them. A SOA record set is never deleted alone (Azure deletes it with
	// its zone): the resource stays.
	f["LogProfile"] = conformanceFixture{coveredBy: "TestLogProfileAndWorkbookTemplate"}
	soa := conformanceFixture{stays: true, spec: func(c *conformanceContext) map[string]any { return map[string]any{"azureName": "@"} }}
	f["DnsZonesSOARecord"] = soa
	f["PrivateDnsZonesSOARecord"] = soa
	f["DnsResolverPoliciesDnsSecurityRule"] = conformanceFixture{coveredBy: "TestDNSSecurityPolicy"}
	f["RouteFiltersRouteFilterRule"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"access": "Allow", "routeFilterRuleType": "Community", "communities": []any{"12076:5010"}}
	}}
	f["ServiceEndpointPoliciesServiceEndpointPolicyDefinition"] = conformanceFixture{coveredBy: "TestNetworkFilters"}
	// Security connector Azure DevOps children have no DELETE operation in Azure:
	// detach on delete so the conformance test does not expect removal.
	f["SecurityConnectorsDevOpsAzureDevOpsOrg"] = conformanceFixture{annotations: detach, stays: true}
	f["SecurityConnectorsDevOpsAzureDevOpsProject"] = conformanceFixture{annotations: detach, stays: true}
	f["SecurityConnectorsDevOpsAzureDevOpsRepo"] = conformanceFixture{annotations: detach, stays: true}
	// A connection monitor has a test group naming its own endpoints and
	// test configuration; an address prefix set at least one CIDR (the
	// fake's rules, networkwatcher.go).
	f["NetworkWatchersConnectionMonitor"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{
			"endpoints":          []any{map[string]any{"name": "a", "address": "10.0.0.4"}, map[string]any{"name": "b", "address": "www.example.com"}},
			"testConfigurations": []any{map[string]any{"name": "t", "protocol": "Tcp", "tcpConfiguration": map[string]any{"port": int64(443)}}},
			"testGroups":         []any{map[string]any{"name": "g", "sources": []any{"a"}, "destinations": []any{"b"}, "testConfigurations": []any{"t"}}},
		}
	}}
	f["ApplicationSecurityGroupsAddressPrefixSet"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"properties": map[string]any{"addressPrefixes": []any{"10.0.0.0/16"}}}
	}}
	// A virtual network TAP has one destination that exists; a NIC's tap
	// configuration names a TAP that exists (vnettap.go).
	f["VirtualNetworkTap"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"destinationNetworkInterfaceIPConfiguration": embedRef(
			c.add(c.in("Microsoft.Network/networkInterfaces/collector/ipConfigurations/ipconfig1")))}
	}}
	// A Service Bus disaster recovery alias names a partner namespace of
	// the service that exists; a private endpoint makes a connection
	// (messaging.go, privateendpoints.go).
	f["servicebus.azure.hfl.dev/NamespacesDisasterRecoveryConfig"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"partnerNamespaceReference": armRef(c.add(c.in("Microsoft.ServiceBus/namespaces/partner")))}
	}}
	f["servicebus.azure.hfl.dev/NamespacesPrivateEndpointConnection"] = conformanceFixture{setup: func(c *conformanceContext) {
		c.add(c.owner + "/privateEndpointConnections/conformance")
	}}
	// The same for Event Hubs, whose application groups name their clients
	// as SASKeyName= or AADAppID= (messaging.go).
	f["eventhub.azure.hfl.dev/NamespacesDisasterRecoveryConfig"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"partnerNamespaceReference": armRef(c.add(c.in("Microsoft.EventHub/namespaces/partner")))}
	}}
	f["eventhub.azure.hfl.dev/NamespacesPrivateEndpointConnection"] = conformanceFixture{setup: func(c *conformanceContext) {
		c.add(c.owner + "/privateEndpointConnections/conformance")
	}}
	// Log Analytics' "Cluster" fixture is another kind's: a dedicated Event
	// Hubs cluster's SKU is Dedicated.
	f["eventhub.azure.hfl.dev/Cluster"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"sku": map[string]any{"name": "Dedicated", "capacity": int64(1)}}
	}}
	f["NamespacesApplicationGroup"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"clientAppGroupIdentifier": "SASKeyName=conformance"}
	}}
	// Event Grid (eventgrid.go): a system topic's source and type; an event
	// subscription's destination; a namespace topic subscription's delivery
	// mode; a topic space's templates, a client group's query, a CA
	// certificate's certificate; a permission binding's topic space and
	// client group, which exist.
	f["SystemTopic"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"sourceReference": armRef(c.ref), "topicType": "Microsoft.Storage.StorageAccounts"}
	}}
	f["SystemTopicsEventSubscription"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"destination": map[string]any{"eventHub": map[string]any{"endpointType": "EventHub", "resourceReference": armRef(c.ref)}}}
	}}
	f["NamespacesTopicsEventSubscription"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"deliveryConfiguration": map[string]any{"deliveryMode": "Queue", "queue": map[string]any{"maxDeliveryCount": int64(1)}}}
	}}
	f["NamespacesTopicSpace"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"topicTemplates": []any{"conformance/#"}}
	}}
	f["NamespacesClientGroup"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"query": "attributes.type = 'conformance'"}
	}}
	f["NamespacesCaCertificate"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"encodedCertificate": "conformance"}
	}}
	f["NamespacesPermissionBinding"] = conformanceFixture{
		setup: func(c *conformanceContext) { c.add(c.owner+"/topicSpaces/ts", c.owner+"/clientGroups/cg") },
		spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"topicSpaceName": "ts", "clientGroupName": "cg", "permission": "Subscriber"}
		}}
	// API Management (apimanagement.go): what the spec requires of each
	// kind, declared template parameters, <policies> XML, tag links naming a
	// tag of the service that exists, an identity provider named by its type.
	g := "apimanagement.azure.hfl.dev/"
	secretRef := map[string]any{"name": "conformance", "key": "value"}
	f[g+"ApiOperation"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"displayName": "Conformance", "method": "GET", "urlTemplate": "/conformance"}
	}}
	f[g+"ApiOperationPolicy"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"value": "<policies><inbound><base /></inbound></policies>"}
	}}
	f[g+"Tag"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"displayName": "Conformance"}
	}}
	serviceTag := conformanceFixture{setup: func(c *conformanceContext) { c.add(c.parent() + "/tags/conformance") }}
	f[g+"ApiTag"], f[g+"ProductTag"] = serviceTag, serviceTag
	f[g+"ApiOperationTag"] = conformanceFixture{setup: func(c *conformanceContext) { c.add(parentOf(c.parent()) + "/tags/conformance") }}
	f[g+"ApiSchema"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"contentType": "application/vnd.oai.openapi.components+json"}
	}}
	f[g+"GlobalSchema"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"schemaType": "json"}
	}}
	f[g+"IdentityProvider"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"azureName": "aad", "clientId": "conformance", "clientSecret": secretRef}
	}}
	f[g+"OpenIdConnectProvider"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"displayName": "Conformance", "metadataEndpoint": "https://idp.example.com/.well-known/openid-configuration", "clientId": "conformance"}
	}}
	f[g+"AuthorizationServer"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{
			"displayName": "Conformance", "clientId": "conformance", "grantTypes": []any{"authorizationCode"},
			"clientRegistrationEndpoint": "https://idp.example.com/register", "authorizationEndpoint": "https://idp.example.com/authorize",
		}
	}}
	f[g+"Cache"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"connectionString": secretRef, "useFromLocation": "default"}
	}}
	// The second part (f71): a resolver's path is Type/field and its policy
	// <policies> XML; a link names what it links, in its own service; a
	// configuration connection is a workspace gateway's (a SKU the fake's
	// stand-in owner has not), so its own test runs it.
	f[g+"ApiResolver"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"displayName": "Conformance", "path": "Query/conformance"}
	}}
	f[g+"ApiResolverPolicy"] = f[g+"ApiOperationPolicy"]
	link := func(prop, rest string) conformanceFixture {
		return conformanceFixture{spec: func(c *conformanceContext) map[string]any {
			return map[string]any{prop: armRef(c.add(c.parent() + rest))}
		}}
	}
	f[g+"TagApiLink"], f[g+"ProductApiLink"] = link("apiReference", "/apis/conformance"), link("apiReference", "/apis/conformance")
	f[g+"TagOperationLink"] = link("operationReference", "/apis/conformance/operations/conformance")
	f[g+"TagProductLink"] = link("productReference", "/products/conformance")
	f[g+"ProductGroupLink"] = link("groupReference", "/groups/conformance")
	f[g+"GatewayConfigConnection"] = conformanceFixture{coveredBy: "TestAPIManagementLinksAndGateways"}
	// The workspace kinds (f72): the same rules, scoped to the workspace
	// (the owner's parent).
	f[g+"WorkspaceApiOperation"], f[g+"WorkspaceApiOperationPolicy"], f[g+"WorkspaceTag"] = f[g+"ApiOperation"], f[g+"ApiOperationPolicy"], f[g+"Tag"]
	f[g+"WorkspaceApiSchema"], f[g+"WorkspaceSchema"] = f[g+"ApiSchema"], f[g+"GlobalSchema"]
	f[g+"WorkspaceTagApiLink"], f[g+"WorkspaceProductApiLink"] = link("apiReference", "/apis/conformance"), link("apiReference", "/apis/conformance")
	f[g+"WorkspaceTagOperationLink"] = link("operationReference", "/apis/conformance/operations/conformance")
	f[g+"WorkspaceTagProductLink"] = link("productReference", "/products/conformance")
	f[g+"WorkspaceProductGroupLink"] = link("groupReference", "/groups/conformance")
	// A Batch pool has a VM size and a virtual machine configuration
	// (batch.go).
	f["BatchAccountsPool"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"vmSize": "STANDARD_D2S_V3", "deploymentConfiguration": map[string]any{"virtualMachineConfiguration": map[string]any{
			"imageReference": map[string]any{"publisher": "canonical", "offer": "ubuntu-24_04-lts", "sku": "server"}, "nodeAgentSkuId": "batch.node.ubuntu 24.04"}}}
	}}
	// Logic Apps (logic.go): an integration account has a SKU (the first
	// enum value is NotSpecified); a schema and a map their content; a
	// batch configuration a release criterion; an assembly its content; an
	// agreement names partners of its account and carries its type's
	// content, so its own test runs it.
	f["IntegrationAccount"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"sku": map[string]any{"name": "Free"}}
	}}
	f["IntegrationAccountsSchema"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"schemaType": "Xml", "content": "<xs:schema xmlns:xs=\"http://www.w3.org/2001/XMLSchema\"/>"}
	}}
	f["IntegrationAccountsMap"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"mapType": "Liquid", "content": "{{ content }}"}
	}}
	f["IntegrationAccountsBatchConfiguration"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"properties": map[string]any{"batchGroupName": "conformance", "releaseCriteria": map[string]any{"messageCount": int64(1)}}}
	}}
	f["IntegrationAccountsAssembly"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"properties": map[string]any{"assemblyName": "Conformance", "content": "TVqQAAMAAAAEAAAA"}}
	}}
	f["IntegrationAccountsCertificate"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"publicCertificate": "MIIBconformance"}
	}}
	f["IntegrationAccountsAgreement"] = conformanceFixture{coveredBy: "TestLogicWorkflowAndIntegrationAccount"}
	// A MySQL flexible server's threat protection setting is Default and
	// Enabled or Disabled; a private endpoint makes a connection (mysql.go).
	f["FlexibleServersAdvancedThreatProtectionSetting"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"azureName": "Default", "state": "Enabled"}
	}}
	f["dbformysql.azure.hfl.dev/FlexibleServersPrivateEndpointConnection"] = conformanceFixture{setup: func(c *conformanceContext) {
		c.add(c.owner + "/privateEndpointConnections/conformance")
	}}
	// A container group profile has an OS type and a container with an image
	// and requests; an NGroup's profiles exist (containerinstance.go).
	f["ContainerGroupProfile"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"osType": "Linux", "containers": []any{map[string]any{
			"name": "conformance", "image": "mcr.microsoft.com/azuredocs/aci-helloworld",
			"resources": map[string]any{"requests": map[string]any{"cpu": 1.0, "memoryInGB": 1.0}},
		}}}
	}}
	f["NGroup"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"containerGroupProfiles": []any{map[string]any{"resource": embedRef(c.add(c.in("Microsoft.ContainerInstance/containerGroupProfiles/conformance")))}}}
	}}
	// Web PubSub and SignalR (signalr.go): a known SKU; certificates and
	// domains naming what they need; shared private links to what exists; a
	// private endpoint makes a connection. A replica needs a Premium service
	// in another region, which the conformance owner is not.
	f["WebPubSub"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"sku": map[string]any{"name": "Free_F1", "capacity": int64(1)}}
	}}
	f["WebPubSubReplica"] = conformanceFixture{coveredBy: "TestWebPubSubAndSignalRKinds"}
	f["WebPubSubCustomCertificate"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"keyVaultBaseUri": "https://conformance.vault.azure.net/", "keyVaultSecretName": "conformance"}
	}}
	f["WebPubSubCustomDomain"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"domainName": "conformance.example.com", "customCertificate": embedRef(c.add(c.owner + "/customCertificates/conformance"))}
	}}
	sharedLink := conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"groupId": "sites", "privateLinkResourceReference": armRef(c.ref)}
	}}
	for _, k := range []string{"WebPubSubSharedPrivateLinkResource", "WebPubSubReplicasSharedPrivateLinkResource", "SignalRSharedPrivateLinkResource", "SignalRReplicasSharedPrivateLinkResource"} {
		f[k] = sharedLink
	}
	for _, k := range []string{"WebPubSubPrivateEndpointConnection", "SignalRPrivateEndpointConnection"} {
		f[k] = conformanceFixture{setup: func(c *conformanceContext) { c.add(c.owner + "/privateEndpointConnections/conformance") }}
	}
	// Data Explorer (kusto.go): a follower names another cluster; a managed
	// private endpoint its target and groupId; an image its language and
	// version; a script one source; a private endpoint makes a connection.
	f["ClustersAttachedDatabaseConfiguration"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"databaseName": "*", "defaultPrincipalsModificationKind": "Union", "clusterResourceReference": armRef(c.add(c.in("Microsoft.Kusto/clusters/leader")))}
	}}
	f["ClustersManagedPrivateEndpoint"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"groupId": "blob", "privateLinkResourceReference": armRef(c.ref)}
	}}
	f["ClustersSandboxCustomImage"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"language": "Python", "languageVersion": "3.10.8"}
	}}
	f["ClustersDatabasesScript"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"scriptContent": map[string]any{"name": "conformance", "key": "value"}}
	}}
	f["kusto.azure.hfl.dev/ClustersPrivateEndpointConnection"] = conformanceFixture{setup: func(c *conformanceContext) {
		c.add(c.owner + "/privateEndpointConnections/conformance")
	}}
	// A backup vault's guard proxy names a guard; its DELETE needs an
	// unlock, which the kind's extension sends (backup.go).
	f["BackupVaultsBackupResourceGuardProxy"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"properties": map[string]any{"resourceGuardResourceReference": armRef(c.add(c.in("Microsoft.DataProtection/resourceGuards/conformance")))}}
	}}
	f["NetworkInterfacesTapConfiguration"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"virtualNetworkTap": embedRef(c.add(c.in("Microsoft.Network/virtualNetworkTaps/conformance")))}
	}}
	// A governance rule gives a condition set (its key, a GUID, is the
	// Claimer's); a summary rule a query and a bin size Azure allows (the
	// fake's rules, governance.go and loganalytics.go).
	f["GovernanceRule"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"conditionSets": []any{map[string]any{"conditions": []any{}}}}
	}}
	// Sentinel's workspace manager: a group names members, and an
	// assignment a group, of the same workspace manager, which the fake
	// checks exist (sentinel.go); the conformance owner is a resource group.
	insights := func(c *conformanceContext) string { return c.resourceGroup + "/providers/Microsoft.SecurityInsights/" }
	f["WorkspaceManagerGroup"] = conformanceFixture{
		setup: func(c *conformanceContext) { c.add(insights(c) + "workspaceManagerMembers/m1") },
		spec:  func(c *conformanceContext) map[string]any { return map[string]any{"memberResourceNames": []any{"m1"}} },
	}
	// Three groups have a kind named Setting: a key "group/Kind" is looked up
	// before "Kind". Sentinel's is polymorphic and named after its kind.
	f["securityinsights.azure.hfl.dev/Setting"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"azureName": "Ueba", "ueba": map[string]any{"kind": "Ueba", "dataSources": []any{"AuditLogs"}}}
	}}
	f["costmanagement.azure.hfl.dev/Setting"] = conformanceFixture{}
	f["WorkspaceManagerAssignment"] = conformanceFixture{
		setup: func(c *conformanceContext) { c.add(insights(c) + "workspaceManagerGroups/g1") },
		spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"targetResourceName": "g1", "items": []any{map[string]any{"resourceReference": armRef(c.ref)}}}
		},
	}
	// A custom assessment is named after assessment metadata in the
	// resource's subscription, which the fake checks exists (assessments.go).
	f["Assessment"] = conformanceFixture{
		setup: func(c *conformanceContext) {
			c.add("/subscriptions/" + subID + "/providers/Microsoft.Security/assessmentMetadata/" + conformanceAssessmentKey)
		},
		spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"azureName": conformanceAssessmentKey, "resourceDetails": map[string]any{"azure": map[string]any{"source": "Azure"}}}
		},
	}
	// A private endpoint makes the connection; a PUT only changes it.
	f["PrivateLinksPrivateEndpointConnection"] = conformanceFixture{setup: func(c *conformanceContext) {
		c.add(c.owner + "/privateEndpointConnections/conformance")
	}}
	// A Cosmos DB role assignment names a role definition of its API in its
	// account, which the fake checks exists, and is scoped to the account
	// (cosmosrbac.go).
	for kind, collection := range map[string]string{
		"DatabaseAccountsCassandraRoleAssignment": "cassandraRoleDefinitions",
		"DatabaseAccountsGremlinRoleAssignment":   "gremlinRoleDefinitions",
		"DatabaseAccountsMongoMIRoleAssignment":   "mongoMIRoleDefinitions",
		"DatabaseAccountsTableRoleAssignment":     "tableRoleDefinitions",
	} {
		f[kind] = conformanceFixture{
			setup: func(c *conformanceContext) { c.add(c.owner + "/" + collection + "/" + conformanceRoleDefinition) },
			spec: func(c *conformanceContext) map[string]any {
				return map[string]any{"roleDefinitionId": c.owner + "/" + collection + "/" + conformanceRoleDefinition, "scope": c.owner, "principalId": conformancePrincipal}
			},
		}
	}
	// Gallery versions are named MajorVersion.MinorVersion.Patch; an image
	// version names a source that exists, an application version its
	// package's blob; a host group has 1 to 3 fault domains (gallery.go).
	f["GalleriesImagesVersion"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"azureName": "1.0.0", "storageProfile": map[string]any{"source": map[string]any{"reference": armRef(c.add(c.in("Microsoft.Compute/images/base")))}}}
	}}
	f["GalleriesApplicationsVersion"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"azureName": "1.0.0", "publishingProfile": map[string]any{"source": map[string]any{"mediaLink": "https://example.blob.core.windows.net/pkgs/agent.zip"}}}
	}}
	f["GalleriesScriptsVersion"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"azureName": "1.0.0", "properties": map[string]any{"publishingProfile": map[string]any{"source": map[string]any{"scriptLink": "https://example.blob.core.windows.net/scripts/harden.sh"}}}}
	}}
	f["HostGroup"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"platformFaultDomainCount": int64(1)}
	}}
	// Static Web Apps' links and an app's network connection name a
	// resource the fake holds (staticsites.go); a private endpoint makes a
	// connection, which a PUT only changes.
	f["StaticSitesLinkedBackend"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"backendResourceReference": armRef(c.ref)}
	}}
	f["StaticSitesUserProvidedFunctionApp"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"functionAppResourceReference": armRef(c.ref)}
	}}
	f["SitesVirtualNetworkConnection"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"vnetResourceReference": armRef(c.ref)}
	}}
	pec := conformanceFixture{setup: func(c *conformanceContext) { c.add(c.owner + "/privateEndpointConnections/conformance") }}
	f["SitesPrivateEndpointConnection"] = pec
	f["SitesSlotsPrivateEndpointConnection"] = pec
	f["HostingEnvironmentsPrivateEndpointConnection"] = pec
	// Azure Cosmos DB for PostgreSQL (cosmospostgres.go): a new cluster
	// has its password, coordinator and node count; a firewall rule IPv4
	// addresses; a role a password; a configuration a value.
	secret := map[string]any{"name": "conformance", "key": "value"}
	f["ServerGroupsv2"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"administratorLoginPassword": secret, "coordinatorServerEdition": "GeneralPurpose",
			"coordinatorStorageQuotaInMb": int64(131072), "coordinatorVCores": int64(2), "nodeCount": int64(0)}
	}}
	f["ServerGroupsv2FirewallRule"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"startIpAddress": "10.0.0.1", "endIpAddress": "10.0.0.2"}
	}}
	f["ServerGroupsv2Role"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"password": secret}
	}}
	pgConfig := conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"value": "on"}
	}}
	f["ServerGroupsv2CoordinatorConfiguration"] = pgConfig
	f["ServerGroupsv2NodeConfiguration"] = pgConfig
	f["ServerGroupsv2PrivateEndpointConnection"] = pec
	// Cosmos DB fleets and MongoDB vCore users (cosmosfleet.go): a
	// fleetspace account names a Cosmos DB account; a user is an Entra
	// object ID with an identity provider.
	f["FleetsFleetspacesFleetspaceAccount"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"globalDatabaseAccountProperties": map[string]any{
			"resourceReference": armRef(c.add(c.in("Microsoft.DocumentDB/databaseAccounts/conformance"))), "armLocation": "westus"}}
	}}
	f["MongoClustersUser"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"azureName": conformancePrincipal, "properties": map[string]any{
			"identityProvider": map[string]any{"microsoftEntraID": map[string]any{"type": "MicrosoftEntraID",
				"properties": map[string]any{"principalType": "servicePrincipal"}}},
			"roles": []any{map[string]any{"db": "admin", "role": "root"}}}}
	}}
	// A storage connector and a blob access point configuration are one
	// of several source types: an empty choice cannot be sent.
	f["StorageAccountsConnector"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"properties": map[string]any{"dataSourceType": "Azure_DataShare", "source": map[string]any{"dataShare": map[string]any{
			"type":           "DataShare",
			"connection":     map[string]any{"dataShare": map[string]any{"type": "DataShare", "dataShareUri": "azds://westus:share-1:00000000-0000-0000-0000-000000000001"}},
			"authProperties": map[string]any{"managedIdentity": map[string]any{"type": "ManagedIdentity", "identityResourceReference": armRef(c.ref)}},
		}}}}
	}}
	f["StorageAccountsBlobAccessPointConfiguration"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"properties": map[string]any{"source": map[string]any{"s3Compatible": map[string]any{
			"sourceType": "S3Compatible",
			"connection": map[string]any{"endpoint": map[string]any{"connectionType": "Endpoint", "endpoint": "https://nas.example.com"}},
			"auth":       map[string]any{"accessKey": map[string]any{"authType": "AccessKey", "accessKeyId": "conformance", "secretAccessKey": secret}},
		}}}}
	}}
	// A sandbox group's network connection names a subnet (appextras.go).
	f["SandboxGroupsVnetConnection"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"properties": map[string]any{"subnetReference": armRef(c.add(c.in("Microsoft.Network/virtualNetworks/conformance/subnets/sandboxes")))}}
	}}
	// An account's managed network is named default (foundry.go).
	f["AccountsManagedNetwork"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"azureName": "default"}
	}}
	// An outbound rule and an agent deployment are one of several types:
	// an empty choice cannot be sent.
	f["AccountsManagedNetworksOutboundRule"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"properties": map[string]any{"fqdn": map[string]any{"type": "FQDN", "destination": "pypi.org"}}}
	}}
	f["AccountsProjectsApplicationsAgentDeployment"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"properties": map[string]any{"managed": map[string]any{"deploymentType": "Managed"}}}
	}}
	// An App Service Environment names a network the fake holds
	// (hostingenvironments.go).
	f["HostingEnvironment"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"virtualNetwork": map[string]any{"reference": armRef(c.add(c.in("Microsoft.Network/virtualNetworks/ase")))}}
	}}
	// A slot's network connection names a network the fake holds, as an
	// app's does (sitechildren.go).
	f["SitesSlotsVirtualNetworkConnection"] = f["SitesVirtualNetworkConnection"]
	f["StaticSitesPrivateEndpointConnection"] = pec
	// The private endpoint connections of docs/plan.md f37 and f38: the fake refuses
	// to make one (privateendpoints.go).
	f["ServersPrivateEndpointConnection"] = pec
	f["ContainerAppsPrivateEndpointConnection"] = pec
	f["DiskAccessesPrivateEndpointConnection"] = pec
	f["RedisPrivateEndpointConnection"] = pec
	f["RedisEnterprisePrivateEndpointConnection"] = pec
	f["ConfigurationStoresPrivateEndpointConnection"] = pec
	f["FlexibleServersPrivateEndpointConnection"] = pec
	f["MongoClustersPrivateEndpointConnection"] = pec
	// Elastic Jobs: an agent names its job database, which the fake checks
	// exists; a credential needs a username and password, and a step its
	// target group in its own agent (sqljobs.go).
	f["ServersJobAgent"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"databaseReference": armRef(c.add(c.owner + "/databases/jobs"))}
	}}
	f["ServersJobAgentsCredential"] = conformanceFixture{coveredBy: "TestSQLElasticJobs"}
	// SQL Managed Instance children (sqlmanagedinstance.go): a Key Vault key
	// gives its URI, the protector is service-managed here (one naming a key
	// is TestSQLManagedInstanceChildren's), an assessment gives its
	// container and a SAS, a pool names a subnet the fake holds.
	f["ManagedInstancesPrivateEndpointConnection"] = pec
	f["ManagedInstancesKey"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"azureName": "vault_key_0123456789abcdef", "serverKeyType": "AzureKeyVault", "uri": "https://vault.vault.azure.net/keys/key/0123456789abcdef"}
	}}
	f["ManagedInstancesEncryptionProtector"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"serverKeyType": "ServiceManaged"}
	}}
	assessment := conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"storageContainerPath": "https://st.blob.core.windows.net/va/", "storageContainerSasKey": map[string]any{"name": "conformance", "key": "value"}}
	}}
	f["ManagedInstancesVulnerabilityAssessment"] = assessment
	f["ManagedInstancesDatabasesVulnerabilityAssessment"] = assessment
	f["InstancePool"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"subnetReference": armRef(c.add(c.in("Microsoft.Network/virtualNetworks/pool"), c.in("Microsoft.Network/virtualNetworks/pool/subnets/pool")))}
	}}
	f["ServersJobAgentsJobsStep"] = conformanceFixture{coveredBy: "TestSQLElasticJobs"}
	f["WorkspacesSummaryLog"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"ruleDefinition": map[string]any{"query": "AppRequests | summarize count() by bin(TimeGenerated, 1h)", "binSize": int64(60), "destinationTable": "Hourly_CL"}}
	}}
	// Foundation batch 2: kinds with required fields or specific owners.
	f["DeploymentSafeguard"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"level": "Warn"}
	}}
	f["ManagementAssociation"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"properties": map[string]any{"applicationId": c.add(c.in("Microsoft.Compute/virtualMachines/vm"))}}
	}}
	f["Budget"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{
			"category":   "Cost",
			"amount":     100.0,
			"timeGrain":  "Monthly",
			"timePeriod": map[string]any{"startDate": "2026-01-01T00:00:00Z"},
		}
	}}
	f["MyWorkbook"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"displayName": "Conformance", "category": "workbook", "serializedData": "{}"}
	}}
	f["GuestDiagnosticSettingsAssociation"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"guestDiagnosticSettingsName": "default"}
	}}
	f["TenantActionGroup"] = conformanceFixture{
		owner: func(c *conformanceContext) string {
			return c.add("/providers/Microsoft.Management/managementGroups/conformance-mg")
		},
		spec: func(c *conformanceContext) map[string]any {
			return map[string]any{"location": "Global", "enabled": true, "groupShortName": "conf"}
		}}
	// Foundation batch 3: NetworkManager IPAM/Verifier kinds with required fields.
	f["NetworkManagersIpamPool"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"properties": map[string]any{"addressPrefixes": []any{"10.0.0.0/16"}}}
	}}
	f["NetworkManagersReachabilityAnalysisIntent"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		src := c.add(c.in("Microsoft.Network/virtualNetworks/src-vnet"))
		dst := c.add(c.in("Microsoft.Network/virtualNetworks/dst-vnet"))
		return map[string]any{"properties": map[string]any{
			"sourceResourceReference":      armRef(src),
			"destinationResourceReference": armRef(dst),
			"ipTraffic": map[string]any{
				"sourceIps":        []any{"10.0.0.0/24"},
				"destinationIps":   []any{"10.1.0.0/24"},
				"sourcePorts":      []any{"0-65535"},
				"destinationPorts": []any{"443"},
				"protocols":        []any{"TCP"},
			},
		}}
	}}
	f["NetworkManagersReachabilityAnalysisRun"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"properties": map[string]any{
			"intentReference": armRef(c.add(c.in("Microsoft.Network/networkManagers/nm/verifierWorkspaces/vw/reachabilityAnalysisIntents/intent"))),
		}}
	}}
	// An ExpressRoute Direct LAG gives its peering location and bandwidth
	// (the fake's rule, as for a port).
	f["ExpressRouteLag"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"location": "westus", "properties": map[string]any{"peeringLocation": "Equinix-Silicon-Valley", "bandwidthInGbps": 100}}
	}}
	// DevTest Labs (devtestlab.go): a lab's and a lab VM's schedules and a
	// lab's costs have fixed names; a lab VM signs in with a password and a
	// user's secret has a value, both from the test's Secret.
	f["LabsSchedule"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"azureName": "LabVmsShutdown", "taskType": "LabVmsShutdownTask", "dailyRecurrence": map[string]any{"time": "1900"}}
	}}
	f["LabsVirtualmachinesSchedule"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"azureName": "LabVmsShutdown", "taskType": "LabVmsShutdownTask", "dailyRecurrence": map[string]any{"time": "1900"}}
	}}
	f["LabsCost"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"azureName": "targetCost", "currencyCode": "USD"}
	}}
	f["LabsVirtualMachine"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"userName": "labadmin", "password": map[string]any{"name": "conformance", "key": "value"}}
	}}
	f["LabsUsersSecret"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"value": map[string]any{"name": "conformance", "key": "value"}}
	}}
	// Azure Site Recovery: a migration item's only scenario is VMware (its
	// provider-specific input is a one-of with nothing set otherwise).
	f["VaultsReplicationMigrationItem"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"properties": map[string]any{
			"policyReference": armRef(c.ref),
			"providerSpecificDetails": map[string]any{"vMwareCbt": map[string]any{
				"instanceType": "VMwareCbt", "dataMoverRunAsAccountId": "mover", "snapshotRunAsAccountId": "snapshot",
				"vmwareMachineReference": armRef(c.ref), "targetResourceGroupReference": armRef(c.ref), "targetNetworkReference": armRef(c.ref),
				"disksToInclude": []any{map[string]any{
					"diskId": "disk0", "isOSDisk": "true", "logStorageAccountReference": armRef(c.ref), "logStorageAccountSasSecretName": "sas",
				}},
			}},
		}}
	}}
	// App Service leftovers (webextras.go): a certificate's PFX and password
	// from the test's Secret, the integration gateway named primary, a slot's
	// repository, basic authentication's scope and password, and a
	// Kubernetes environment's custom location.
	cert := conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"pfxBlob": secret, "password": secret}
	}}
	f["SitesCertificate"], f["SitesSlotsCertificate"] = cert, cert
	gateway := conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"azureName": "primary", "vnetName": "vnet"}
	}}
	f["SitesVirtualNetworkConnectionsGateway"], f["SitesSlotsVirtualNetworkConnectionsGateway"] = gateway, gateway
	f["SitesSlotsSourcecontrol"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"repoUrl": "https://github.com/contoso/app", "branch": "main", "isManualIntegration": true}
	}}
	f["StaticSitesBasicAuth"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"applicableEnvironmentsMode": "AllEnvironments", "password": secret}
	}}
	f["KubeEnvironment"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"extendedLocation": map[string]any{"name": c.add(c.in("Microsoft.ExtendedLocation/customLocations/arc"))}}
	}}
	// Azure Monitor pipeline groups run on a custom location and their
	// pipelines name what they declare (monitorpipeline.go).
	f["PipelineGroup"] = conformanceFixture{coveredBy: "TestMonitorPipelineGroup"}
	// An alert processing rule is global, applies to a scope and has one
	// action (alertprocessing.go).
	f["ActionRule"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"location": "Global", "properties": map[string]any{
			"scopesReferences": []any{armRef(c.resourceGroup)},
			"actions":          []any{map[string]any{"removeAllActionGroups": map[string]any{"actionType": "RemoveAllActionGroups"}}},
		}}
	}}
	// A smart detection rule is one Application Insights defines and has no
	// DELETE (smartdetection.go): the object is detached and the rule stays.
	f["ComponentsProactiveDetectionConfig"] = conformanceFixture{annotations: detach, stays: true, spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"azureName": "slowpageloadtime"}
	}}
	// Logic Apps connections name an API, custom APIs carry a definition and
	// gateways name their installation (logicconnections.go).
	f["ApiConnection"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"properties": map[string]any{"api": embedRef(c.add(c.in("Microsoft.Web/customApis/conformance")))}}
	}}
	f["CustomApi"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"properties": map[string]any{"apiDefinitions": map[string]any{"originalSwaggerUrl": "https://contoso.example/openapi.json"}}}
	}}
	f["ConnectionGateway"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		install := c.add("/subscriptions/" + subID + "/providers/Microsoft.Web/locations/westus/connectionGatewayInstallations/" + c.ns)
		return map[string]any{"properties": map[string]any{"connectionGatewayInstallation": embedRef(install)}}
	}}
	// A diagnostic run command has one source (diagnosticruncommands.go).
	f["VirtualMachinesDiagnosticRunCommand"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"properties": map[string]any{"source": map[string]any{"script": "uptime"}}}
	}}
	for k, v := range f {
		conformanceFixtures[k] = v
	}
}

// conformanceAssessmentKey is the assessment metadata a conformance
// Assessment reports on.
const conformanceAssessmentKey = "3b5c7a1e-0d2f-4c3b-9a8e-6f1e2d3c4b5a"

// conformanceRoleDefinition is the Cosmos DB role definition a conformance
// role assignment names.
const conformanceRoleDefinition = "6b4a2d3c-1e5f-4a7b-8c9d-0e1f2a3b4c5d"
