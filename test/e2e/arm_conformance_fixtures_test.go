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
	// A governance rule gives a condition set (its key, a GUID, is the
	// Claimer's); a summary rule a query and a bin size Azure allows (the
	// fake's rules, governance.go and loganalytics.go).
	f["GovernanceRule"] = conformanceFixture{spec: func(c *conformanceContext) map[string]any {
		return map[string]any{"conditionSets": []any{map[string]any{"conditions": []any{}}}}
	}}
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
	for k, v := range f {
		conformanceFixtures[k] = v
	}
}
