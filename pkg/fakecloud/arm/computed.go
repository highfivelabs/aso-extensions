// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"regexp"
	"strings"
)

// What Azure computes and what it makes beside a resource, as far as the fake
// models them (added 2026-11-12, for stock ASO and the catalog's charts):
//
//   - read-only properties a client reads back: a vault's vaultUri, a key's
//     keyUri and keyUriWithVersion (vaultkeys.go), a user-assigned identity's
//     principalId, clientId and tenantId, a system-assigned identity's
//     principalId and tenantId on any resource (and each user-assigned
//     identity's principalId and clientId in userAssignedIdentities), a
//     public IP address's ipAddress and fqdn, a public IP prefix's ipPrefix,
//     a registry's loginServer (and dataEndpointHostNames), a Log Analytics
//     workspace's customerId, a DNS record set's fqdn, a DNS zone's
//     nameServers, a storage account's primaryEndpoints, an AKS cluster's
//     fqdn, azurePortalFQDN, nodeResourceGroup, currentKubernetesVersion,
//     powerState, kubelet identity and OIDC issuer URL, an Application
//     Insights component's instrumentationKey, appId and connectionString,
//     an App Service site's or slot's defaultHostName, hostNames,
//     enabledHostNames and customDomainVerificationId (one per
//     subscription, as Azure's), an Azure SQL server's, a PostgreSQL or
//     MySQL flexible server's fullyQualifiedDomainName, and a Cosmos DB
//     account's documentEndpoint, its locations' id and documentEndpoint,
//     and its readLocations and writeLocations (every location reads; the
//     failover priority 0 location writes, or every one with
//     enableMultipleWriteLocations);
//   - children Azure makes with their parent: a storage account's default
//     blob, file, queue and table services (only the blob service for
//     BlockBlobStorage, only the file service for FileStorage), and a DNS
//     zone's (public or private) SOA record set @.
//
// Each value is derived from the resource's ID (a GUID, an address), so it is
// the same on every PUT and in every run, and a value the body already
// carries is kept. Not modelled: values that change (an address released
// and allocated again, a Basic SKU's dynamic address appearing only when it
// is attached), the real formats where Azure's own vary by region or SKU,
// and children a later API version adds.
var (
	vaultID          = regexp.MustCompile(`(?i)/providers/microsoft\.keyvault/vaults/[^/]+$`)
	userIdentity     = regexp.MustCompile(`(?i)/providers/microsoft\.managedidentity/userassignedidentities/[^/]+$`)
	publicIP         = regexp.MustCompile(`(?i)/providers/microsoft\.network/publicipaddresses/[^/]+$`)
	publicIPPrefix   = regexp.MustCompile(`(?i)/providers/microsoft\.network/publicipprefixes/[^/]+$`)
	registry         = regexp.MustCompile(`(?i)/providers/microsoft\.containerregistry/registries/[^/]+$`)
	workspace        = regexp.MustCompile(`(?i)/providers/microsoft\.operationalinsights/workspaces/[^/]+$`)
	dnsZone          = regexp.MustCompile(`(?i)/providers/microsoft\.network/(dnszones|privatednszones)/([^/]+)$`)
	dnsRecordSet     = regexp.MustCompile(`(?i)/providers/microsoft\.network/(dnszones|privatednszones)/([^/]+)/(a|aaaa|caa|cname|mx|ns|ptr|soa|srv|txt)/([^/]+)$`)
	storageAccount   = regexp.MustCompile(`(?i)/providers/microsoft\.storage/storageaccounts/[^/]+$`)
	managedCluster   = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/([^/]+)/providers/microsoft\.containerservice/managedclusters/[^/]+$`)
	insightsComponen = regexp.MustCompile(`(?i)/providers/microsoft\.insights/components/[^/]+$`)
	webSite          = regexp.MustCompile(`(?i)^/subscriptions/([^/]+)/.*/providers/microsoft\.web/sites/([^/]+)(?:/slots/([^/]+))?$`)
	sqlServer        = regexp.MustCompile(`(?i)/providers/microsoft\.sql/servers/[^/]+$`)
	flexibleServer   = regexp.MustCompile(`(?i)/providers/microsoft\.dbfor(postgresql|mysql)/flexibleservers/[^/]+$`)
	cosmosAccount    = regexp.MustCompile(`(?i)/providers/microsoft\.documentdb/databaseaccounts/[^/]+$`)
)

// DefaultTenant is the tenant ID the fake writes when it is not given one.
const DefaultTenant = "00000000-0000-0000-0000-00000000a5c0"

// DefaultVaultSuffix is the DNS suffix of Key Vault's data plane.
const DefaultVaultSuffix = "vault.azure.net"

func (a *ARM) tenant() string {
	if a.Tenant != "" {
		return a.Tenant
	}
	return DefaultTenant
}

func (a *ARM) vaultSuffix() string {
	if a.VaultSuffix != "" {
		return a.VaultSuffix
	}
	return DefaultVaultSuffix
}

// derived is n bytes derived from what (a lower-case ID and a purpose).
func derived(what string, n int) []byte {
	out := []byte{}
	for i := 0; len(out) < n; i++ {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s#%d", what, i)))
		out = append(out, sum[:]...)
	}
	return out[:n]
}

// derivedGUID is a GUID derived from an ID and a purpose.
func derivedGUID(id, purpose string) string {
	return formatGUID(derived(strings.ToLower(id)+"|"+purpose, 16))
}

// setIfMissing sets m[k] to v unless m already holds a non-empty value.
func setIfMissing(m map[string]any, k string, v any) {
	if cur, ok := m[k]; ok && cur != nil && cur != "" {
		return
	}
	m[k] = v
}

// compute fills in the properties Azure computes for the resource with the
// given ID (the list above). props is body's properties.
func (a *ARM) compute(id string, body, props map[string]any) {
	name := lastSegment(id)
	location, _ := body["location"].(string)
	a.computeIdentity(id, body)
	switch {
	case vaultID.MatchString(id):
		setIfMissing(props, "vaultUri", "https://"+strings.ToLower(name)+"."+a.vaultSuffix()+"/")
	case vaultKey.MatchString(id):
		a.computeVaultKey(id, props)
	case userIdentity.MatchString(id):
		setIfMissing(props, "principalId", derivedGUID(id, "principal"))
		setIfMissing(props, "clientId", derivedGUID(id, "client"))
		setIfMissing(props, "tenantId", a.tenant())
	case publicIP.MatchString(id):
		b := derived(strings.ToLower(id)+"|ip", 16)
		if strings.EqualFold(fmt.Sprint(props["publicIPAddressVersion"]), "IPv6") {
			setIfMissing(props, "ipAddress", fmt.Sprintf("2603:1030:%x:%x::%x", b[0:2], b[2:4], b[4:6]))
		} else {
			setIfMissing(props, "ipAddress", fmt.Sprintf("20.%d.%d.%d", b[0], b[1], 1+int(b[2])%254))
		}
		if dns, ok := props["dnsSettings"].(map[string]any); ok {
			if label, _ := dns["domainNameLabel"].(string); label != "" {
				setIfMissing(dns, "fqdn", strings.ToLower(label)+"."+strings.ToLower(location)+".cloudapp.azure.com")
			}
		}
	case publicIPPrefix.MatchString(id):
		b := derived(strings.ToLower(id)+"|prefix", 2)
		length := 28
		if l, ok := props["prefixLength"].(float64); ok && l >= 16 && l <= 31 {
			length = int(l)
		}
		setIfMissing(props, "ipPrefix", fmt.Sprintf("20.%d.%d.0/%d", b[0], b[1], length))
	case registry.MatchString(id):
		setIfMissing(props, "loginServer", strings.ToLower(name)+".azurecr.io")
		if props["dataEndpointEnabled"] == true {
			setIfMissing(props, "dataEndpointHostNames", []any{strings.ToLower(name) + "." + strings.ToLower(location) + ".data.azurecr.io"})
		}
	case workspace.MatchString(id):
		setIfMissing(props, "customerId", derivedGUID(id, "customer"))
	case dnsRecordSet.MatchString(id):
		m := dnsRecordSet.FindStringSubmatch(id)
		zone, rs := strings.ToLower(m[2]), m[4]
		if rs == "@" {
			props["fqdn"] = zone + "."
		} else {
			props["fqdn"] = strings.ToLower(rs) + "." + zone + "."
		}
	case dnsZone.MatchString(id):
		if m := dnsZone.FindStringSubmatch(id); strings.EqualFold(m[1], "dnszones") {
			n := derived(strings.ToLower(id)+"|ns", 1)[0] % 40
			setIfMissing(props, "nameServers", []any{
				fmt.Sprintf("ns1-%02d.azure-dns.com.", n), fmt.Sprintf("ns2-%02d.azure-dns.net.", n),
				fmt.Sprintf("ns3-%02d.azure-dns.org.", n), fmt.Sprintf("ns4-%02d.azure-dns.info.", n),
			})
		}
	case storageAccount.MatchString(id):
		n := strings.ToLower(name)
		setIfMissing(props, "primaryEndpoints", map[string]any{
			"blob": "https://" + n + ".blob.core.windows.net/", "queue": "https://" + n + ".queue.core.windows.net/",
			"table": "https://" + n + ".table.core.windows.net/", "file": "https://" + n + ".file.core.windows.net/",
			"dfs": "https://" + n + ".dfs.core.windows.net/", "web": "https://" + n + ".z13.web.core.windows.net/",
		})
		setIfMissing(props, "primaryLocation", location)
		setIfMissing(props, "statusOfPrimary", "available")
	case managedCluster.MatchString(id):
		a.computeManagedCluster(id, location, props)
	case insightsComponen.MatchString(id):
		key := derivedGUID(id, "instrumentation")
		setIfMissing(props, "instrumentationKey", key)
		setIfMissing(props, "appId", derivedGUID(id, "app"))
		setIfMissing(props, "connectionString", "InstrumentationKey="+key+";IngestionEndpoint=https://"+strings.ToLower(location)+"-0.in.applicationinsights.azure.com/")
	case webSite.MatchString(id):
		m := webSite.FindStringSubmatch(id)
		host := strings.ToLower(m[2])
		if m[3] != "" {
			host += "-" + strings.ToLower(m[3])
		}
		setIfMissing(props, "defaultHostName", host+".azurewebsites.net")
		setIfMissing(props, "hostNames", []any{host + ".azurewebsites.net"})
		setIfMissing(props, "enabledHostNames", []any{host + ".azurewebsites.net", host + ".scm.azurewebsites.net"})
		setIfMissing(props, "customDomainVerificationId",
			strings.ToUpper(hex.EncodeToString(derived("/subscriptions/"+strings.ToLower(m[1])+"|domain-verification", 32))))
	case sqlServer.MatchString(id):
		setIfMissing(props, "fullyQualifiedDomainName", strings.ToLower(name)+".database.windows.net")
	case flexibleServer.MatchString(id):
		engine := "mysql"
		if strings.EqualFold(flexibleServer.FindStringSubmatch(id)[1], "postgresql") {
			engine = "postgres"
		}
		setIfMissing(props, "fullyQualifiedDomainName", strings.ToLower(name)+"."+engine+".database.azure.com")
	case cosmosAccount.MatchString(id):
		computeCosmosAccount(name, location, props)
	}
}

// computeCosmosAccount fills a Cosmos DB account's endpoints: the account's
// documentEndpoint, each location's id and documentEndpoint, and the read
// and write locations Azure lists. With no locations given, the account is
// in its own location, as Azure places it.
func computeCosmosAccount(name, location string, props map[string]any) {
	account := strings.ToLower(name)
	setIfMissing(props, "documentEndpoint", "https://"+account+".documents.azure.com:443/")
	locations, _ := props["locations"].([]any)
	if len(locations) == 0 && location != "" {
		locations = []any{map[string]any{"locationName": location, "failoverPriority": float64(0)}}
		props["locations"] = locations
	}
	multiWrite := props["enableMultipleWriteLocations"] == true
	var reads, writes []any
	for _, l := range locations {
		loc, ok := l.(map[string]any)
		if !ok {
			continue
		}
		region, _ := loc["locationName"].(string)
		short := strings.ToLower(strings.ReplaceAll(region, " ", ""))
		setIfMissing(loc, "id", account+"-"+short)
		setIfMissing(loc, "documentEndpoint", "https://"+account+"-"+short+".documents.azure.com:443/")
		loc["provisioningState"] = "Succeeded"
		if _, ok := loc["isZoneRedundant"]; !ok {
			loc["isZoneRedundant"] = false
		}
		reads = append(reads, maps.Clone(loc))
		if p, _ := loc["failoverPriority"].(float64); p == 0 || multiWrite {
			writes = append(writes, maps.Clone(loc))
		}
	}
	if reads != nil {
		props["readLocations"] = reads
		props["writeLocations"] = writes
	}
}

// computeIdentity gives a system-assigned identity its principal and tenant
// IDs, and each user-assigned identity its principal and client IDs (the ones
// that identity's own resource carries).
func (a *ARM) computeIdentity(id string, body map[string]any) {
	identity, ok := body["identity"].(map[string]any)
	if !ok {
		return
	}
	kind, _ := identity["type"].(string)
	if strings.Contains(strings.ToLower(kind), "systemassigned") {
		setIfMissing(identity, "principalId", derivedGUID(id, "system-principal"))
		setIfMissing(identity, "tenantId", a.tenant())
	}
	users, ok := identity["userAssignedIdentities"].(map[string]any)
	if !ok {
		return
	}
	for uid, v := range users {
		entry, _ := v.(map[string]any)
		entry = maps.Clone(entry)
		if entry == nil {
			entry = map[string]any{}
		}
		setIfMissing(entry, "principalId", derivedGUID(uid, "principal"))
		setIfMissing(entry, "clientId", derivedGUID(uid, "client"))
		users[uid] = entry
	}
}

// computeManagedCluster fills an AKS cluster's computed properties.
func (a *ARM) computeManagedCluster(id, location string, props map[string]any) {
	m := managedCluster.FindStringSubmatch(id)
	name := lastSegment(id)
	prefix, _ := props["dnsPrefix"].(string)
	if prefix == "" {
		prefix, _ = props["fqdnSubdomain"].(string)
	}
	if prefix == "" {
		prefix = name
	}
	host := strings.ToLower(prefix) + "-" + hex.EncodeToString(derived(strings.ToLower(id)+"|fqdn", 4)) + ".hcp." + strings.ToLower(location) + ".azmk8s.io"
	setIfMissing(props, "fqdn", host)
	setIfMissing(props, "azurePortalFQDN", strings.Replace(host, ".hcp.", ".portal.hcp.", 1))
	if access, ok := props["apiServerAccessProfile"].(map[string]any); ok && access["enablePrivateCluster"] == true {
		setIfMissing(props, "privateFQDN", strings.Replace(host, ".hcp.", ".privatelink.", 1))
	}
	setIfMissing(props, "nodeResourceGroup", "MC_"+m[1]+"_"+name+"_"+strings.ToLower(location))
	if v, _ := props["kubernetesVersion"].(string); v != "" {
		props["currentKubernetesVersion"] = v
	}
	setIfMissing(props, "powerState", map[string]any{"code": "Running"})
	profiles, _ := props["identityProfile"].(map[string]any)
	if profiles == nil {
		profiles = map[string]any{}
		props["identityProfile"] = profiles
	}
	if _, ok := profiles["kubeletidentity"]; !ok {
		nodeGroup, _ := props["nodeResourceGroup"].(string)
		kubelet := "/subscriptions/" + strings.Split(id, "/")[2] + "/resourcegroups/" + nodeGroup + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/" + name + "-agentpool"
		profiles["kubeletidentity"] = map[string]any{
			"resourceId": kubelet, "clientId": derivedGUID(kubelet, "client"), "objectId": derivedGUID(kubelet, "principal"),
		}
	}
	if oidc, ok := props["oidcIssuerProfile"].(map[string]any); ok && oidc["enabled"] == true {
		setIfMissing(oidc, "issuerURL", "https://"+strings.ToLower(location)+".oic.prod-aks.azure.com/"+a.tenant()+"/"+derivedGUID(id, "oidc")+"/")
	}
}

// makeChildren puts in the store the children Azure makes with the resource
// just stored (the list above), unless they are there.
func (a *ARM) makeChildren(id string, body map[string]any) {
	add := func(childID string, props map[string]any) {
		k := strings.ToLower(childID)
		if _, ok := a.resources[k]; ok {
			return
		}
		r := resource{"id": childID, "name": lastSegment(childID), "type": typeOf(childID), "properties": props}
		a.setSystemData(k, r)
		a.resources[k] = &r
	}
	switch {
	case storageAccount.MatchString(id):
		services := []string{"blobServices", "fileServices", "queueServices", "tableServices"}
		switch kind, _ := body["kind"].(string); kind {
		case "BlockBlobStorage":
			services = []string{"blobServices"}
		case "FileStorage":
			services = []string{"fileServices"}
		}
		for _, s := range services {
			add(id+"/"+s+"/default", map[string]any{})
		}
	case dnsZone.MatchString(id):
		zone := lastSegment(id)
		host, email := "azureprivatedns.net", "azureprivatedns-host.microsoft.com"
		if props, _ := body["properties"].(map[string]any); props != nil {
			if ns, _ := props["nameServers"].([]any); len(ns) > 0 {
				host, _ = ns[0].(string)
				email = "azuredns-hostmaster.microsoft.com"
			}
		}
		add(id+"/SOA/@", map[string]any{
			"fqdn": strings.ToLower(zone) + ".", "ttl": float64(3600),
			"soaRecord": map[string]any{
				"host": host, "email": email,
				"serialNumber": float64(1), "refreshTime": float64(3600), "retryTime": float64(300),
				"expireTime": float64(2419200), "minimumTtl": float64(10),
			},
			"provisioningState": "Succeeded",
		})
	}
}
