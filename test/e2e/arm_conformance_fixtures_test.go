// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

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
	for k, v := range f {
		conformanceFixtures[k] = v
	}
}
