// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const computedRG = "/subscriptions/" + sub1 + "/resourceGroups/rg"

func props(t *testing.T, body string) map[string]any {
	t.Helper()
	var r map[string]any
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	p, _ := r["properties"].(map[string]any)
	return p
}

// The properties Azure computes come back on the PUT and every GET, the same
// each time.
func TestComputedProperties(t *testing.T) {
	a := New(func(string) bool { return true })
	a.Tenant = "11111111-2222-3333-4444-555555555555"
	a.AddResource(computedRG)
	cases := []struct {
		id, body string
		check    func(t *testing.T, p map[string]any, body string)
	}{
		{"/providers/Microsoft.KeyVault/vaults/My-KV", `{"location":"eastus2","properties":{"tenantId":"x"}}`, func(t *testing.T, p map[string]any, _ string) {
			if p["vaultUri"] != "https://my-kv.vault.azure.net/" {
				t.Errorf("vaultUri %v", p["vaultUri"])
			}
		}},
		{"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id1", `{"location":"eastus2"}`, func(t *testing.T, p map[string]any, _ string) {
			for _, f := range []string{"principalId", "clientId"} {
				if s, _ := p[f].(string); len(s) != 36 {
					t.Errorf("%s %v", f, p[f])
				}
			}
			if p["tenantId"] != a.Tenant {
				t.Errorf("tenantId %v", p["tenantId"])
			}
		}},
		{"/providers/Microsoft.Network/publicIPAddresses/pip", `{"location":"eastus2","properties":{"dnsSettings":{"domainNameLabel":"App1"}}}`, func(t *testing.T, p map[string]any, _ string) {
			if s, _ := p["ipAddress"].(string); !strings.HasPrefix(s, "20.") {
				t.Errorf("ipAddress %v", p["ipAddress"])
			}
			if dns := asMap(p["dnsSettings"]); dns["fqdn"] != "app1.eastus2.cloudapp.azure.com" {
				t.Errorf("fqdn %v", dns["fqdn"])
			}
		}},
		{"/providers/Microsoft.Network/publicIPPrefixes/pfx", `{"properties":{"prefixLength":30}}`, func(t *testing.T, p map[string]any, _ string) {
			if s, _ := p["ipPrefix"].(string); !strings.HasSuffix(s, "/30") {
				t.Errorf("ipPrefix %v", p["ipPrefix"])
			}
		}},
		{"/providers/Microsoft.ContainerRegistry/registries/MyReg", `{"location":"eastus2","identity":{"type":"SystemAssigned"},"properties":{"dataEndpointEnabled":true}}`, func(t *testing.T, p map[string]any, body string) {
			if p["loginServer"] != "myreg.azurecr.io" {
				t.Errorf("loginServer %v", p["loginServer"])
			}
			if hosts, _ := p["dataEndpointHostNames"].([]any); len(hosts) != 1 || hosts[0] != "myreg.eastus2.data.azurecr.io" {
				t.Errorf("dataEndpointHostNames %v", p["dataEndpointHostNames"])
			}
			if !strings.Contains(body, `"principalId"`) || !strings.Contains(body, `"tenantId":"`+a.Tenant+`"`) {
				t.Errorf("system-assigned identity: %s", body)
			}
		}},
		{"/providers/Microsoft.OperationalInsights/workspaces/law", `{"location":"eastus2"}`, func(t *testing.T, p map[string]any, _ string) {
			if s, _ := p["customerId"].(string); len(s) != 36 {
				t.Errorf("customerId %v", p["customerId"])
			}
		}},
		{"/providers/Microsoft.Storage/storageAccounts/st1", `{"location":"eastus2","kind":"StorageV2"}`, func(t *testing.T, p map[string]any, _ string) {
			if ep := asMap(p["primaryEndpoints"]); ep["blob"] != "https://st1.blob.core.windows.net/" {
				t.Errorf("primaryEndpoints %v", p["primaryEndpoints"])
			}
		}},
		{"/providers/Microsoft.ContainerService/managedClusters/aks", `{"location":"eastus2","properties":{"dnsPrefix":"Aks","kubernetesVersion":"1.33","oidcIssuerProfile":{"enabled":true}}}`, func(t *testing.T, p map[string]any, _ string) {
			if s, _ := p["fqdn"].(string); !strings.HasPrefix(s, "aks-") || !strings.HasSuffix(s, ".hcp.eastus2.azmk8s.io") {
				t.Errorf("fqdn %v", p["fqdn"])
			}
			if p["nodeResourceGroup"] != "MC_rg_aks_eastus2" || p["currentKubernetesVersion"] != "1.33" {
				t.Errorf("nodeResourceGroup %v, currentKubernetesVersion %v", p["nodeResourceGroup"], p["currentKubernetesVersion"])
			}
			if k := asMap(asMap(p["identityProfile"])["kubeletidentity"]); k["objectId"] == nil || k["clientId"] == nil {
				t.Errorf("kubelet identity %v", p["identityProfile"])
			}
			if s, _ := asMap(p["oidcIssuerProfile"])["issuerURL"].(string); !strings.HasPrefix(s, "https://eastus2.oic.prod-aks.azure.com/"+a.Tenant+"/") {
				t.Errorf("issuerURL %v", s)
			}
		}},
	}
	for _, c := range cases {
		id := computedRG + c.id
		code, body := do(t, a, http.MethodPut, id, c.body)
		expect(t, "put "+c.id, code, body, http.StatusCreated)
		c.check(t, props(t, body), body)
		code, again := do(t, a, http.MethodGet, id, "")
		expect(t, "get "+c.id, code, again, http.StatusOK)
		c.check(t, props(t, again), again)
		// A second PUT gives the same values.
		_, put2 := do(t, a, http.MethodPut, id, c.body)
		p1, p2 := props(t, body), props(t, put2)
		delete(p1, "provisioningState")
		delete(p2, "provisioningState")
		j1, _ := json.Marshal(p1)
		j2, _ := json.Marshal(p2)
		if string(j1) != string(j2) {
			t.Errorf("%s: a second PUT changed the computed values:\n%s\n%s", c.id, j1, j2)
		}
	}
}

// A user-assigned identity on a resource carries the principal and client
// IDs of the identity's own resource.
func TestUserAssignedIdentityIDs(t *testing.T) {
	a := New(func(string) bool { return true })
	a.AddResource(computedRG)
	uai := computedRG + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id1"
	_, body := do(t, a, http.MethodPut, uai, `{"location":"eastus2"}`)
	want := props(t, body)
	_, body = do(t, a, http.MethodPut, computedRG+"/providers/Microsoft.ContainerRegistry/registries/r",
		`{"identity":{"type":"UserAssigned","userAssignedIdentities":{"`+uai+`":{}}}}`)
	var r struct {
		Identity struct {
			UserAssignedIdentities map[string]map[string]any `json:"userAssignedIdentities"`
		} `json:"identity"`
	}
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	got := r.Identity.UserAssignedIdentities[uai]
	if got["principalId"] != want["principalId"] || got["clientId"] != want["clientId"] {
		t.Errorf("user-assigned identity %v, want the identity's %v", got, want)
	}
}

// A value the body carries is kept; a computed value is not added over it.
func TestComputedKeepsGivenValues(t *testing.T) {
	a := New(func(string) bool { return true })
	a.AddResource(computedRG)
	_, body := do(t, a, http.MethodPut, computedRG+"/providers/Microsoft.ContainerService/managedClusters/aks",
		`{"location":"eastus2","properties":{"dnsPrefix":"aks","nodeResourceGroup":"my-nodes"}}`)
	if p := props(t, body); p["nodeResourceGroup"] != "my-nodes" {
		t.Errorf("nodeResourceGroup %v", p["nodeResourceGroup"])
	}
}

// A storage account comes with its default services, a DNS zone with its
// SOA record set; both go with their parent.
func TestChildrenAzureMakes(t *testing.T) {
	a := New(func(string) bool { return true })
	a.AddResource(computedRG)
	st := computedRG + "/providers/Microsoft.Storage/storageAccounts/st1"
	code, body := do(t, a, http.MethodPut, st, `{"location":"eastus2","kind":"StorageV2"}`)
	expect(t, "account", code, body, http.StatusCreated)
	for _, s := range []string{"blobServices", "fileServices", "queueServices", "tableServices"} {
		code, body = do(t, a, http.MethodGet, st+"/"+s+"/default", "")
		expect(t, s, code, body, http.StatusOK)
		if !strings.Contains(body, `"type":"Microsoft.Storage/storageAccounts/`+s+`"`) {
			t.Errorf("%s type: %s", s, body)
		}
	}
	// A container in the default blob service, as ASO makes it.
	code, body = do(t, a, http.MethodPut, st+"/blobServices/default/containers/c", `{"properties":{}}`)
	expect(t, "container", code, body, http.StatusCreated)
	// The default service's own PUT updates it.
	code, body = do(t, a, http.MethodPut, st+"/blobServices/default", `{"properties":{"isVersioningEnabled":true}}`)
	expect(t, "blob service update", code, body, http.StatusOK)

	bb := computedRG + "/providers/Microsoft.Storage/storageAccounts/bb1"
	do(t, a, http.MethodPut, bb, `{"kind":"BlockBlobStorage"}`)
	if a.HasResource(bb+"/queueServices/default") || !a.HasResource(bb+"/blobServices/default") {
		t.Error("a BlockBlobStorage account has a blob service only")
	}

	for _, zone := range []string{"privateDnsZones/example.internal", "dnsZones/example.com"} {
		z := computedRG + "/providers/Microsoft.Network/" + zone
		code, body = do(t, a, http.MethodPut, z, `{"location":"global"}`)
		expect(t, zone, code, body, http.StatusCreated)
		code, body = do(t, a, http.MethodGet, z+"/SOA/@", "")
		expect(t, zone+" SOA", code, body, http.StatusOK)
		if p := props(t, body); p["fqdn"] != lastSegment(zone)+"." {
			t.Errorf("%s SOA fqdn %v", zone, p["fqdn"])
		}
		code, body = do(t, a, http.MethodPut, z+"/A/www", `{"properties":{"ttl":300}}`)
		expect(t, zone+" A", code, body, http.StatusCreated)
		if p := props(t, body); p["fqdn"] != "www."+lastSegment(zone)+"." {
			t.Errorf("%s record fqdn %v", zone, p["fqdn"])
		}
	}
	code, body = do(t, a, http.MethodDelete, st, "")
	expect(t, "delete account", code, body, http.StatusOK)
	if a.HasResource(st + "/blobServices/default") {
		t.Error("the blob service outlived its account")
	}
}
