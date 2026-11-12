// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

//go:build live

package live

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-service-operator/v2/pkg/common/annotations"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	asoapp "github.com/Azure/azure-service-operator/v2/api/app/v20250101/storage"
	asoregistry "github.com/Azure/azure-service-operator/v2/api/containerregistry/v20251101/storage"
	asoweb "github.com/Azure/azure-service-operator/v2/api/web/v20260715/storage"

	appv1 "github.com/highfivelabs/aso-extensions/apis/arm/app/v20250101"
	acr "github.com/highfivelabs/aso-extensions/apis/arm/containerregistry/v20251101"
	webv1 "github.com/highfivelabs/aso-extensions/apis/arm/web/v20260715"
)

const (
	acrAPI = "2025-11-01"
	appAPI = "2025-01-01"
	webAPI = "2024-11-01"
)

// A registry's scope map, token, webhook and credential set, on a Basic
// registry fixture (about $0.17 a day).
func TestLiveContainerRegistry(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.ContainerRegistry")
	ns := namespace(t, "acr")
	regName := strings.ReplaceAll(azName("acr"), "-", "")
	regID := rgID() + "/providers/Microsoft.ContainerRegistry/registries/" + regName
	fixture(t, regID, "2023-07-01", map[string]any{
		"location": location, "sku": map[string]any{"name": "Basic"}, "properties": map[string]any{"adminUserEnabled": false},
	})
	reg := &asoregistry.Registry{}
	reg.Namespace, reg.Name = ns, "reg"
	reg.Spec.AzureName, reg.Spec.Owner = regName, owner("rg")
	standIn(t, reg, regID, nil)
	secret(t, ns, "hook", map[string]string{"uri": "https://example.com/acr-hook", "Authorization": "Bearer hook-token-1"})

	scopeMap := &acr.RegistriesScopeMap{}
	scopeMap.Namespace, scopeMap.Name = ns, "pull-apps"
	scopeMap.Spec = acr.RegistriesScopeMap_Spec{
		AzureName: "pull-apps", Owner: owner("reg"), Description: ptr("read the apps repository"),
		Actions: []string{"repositories/apps/content/read", "repositories/apps/metadata/read"},
	}
	scopeMapID := regID + "/scopeMaps/pull-apps"
	token := &acr.RegistriesToken{}
	token.Namespace, token.Name = ns, "ci"
	token.Spec = acr.RegistriesToken_Spec{
		AzureName: "ci-puller", Owner: owner("reg"),
		ScopeMapReference: &genruntime.ResourceReference{ARMID: scopeMapID}, Status: ptr(acr.TokenStatus("enabled")),
	}
	hook := &acr.RegistriesWebhook{}
	hook.Namespace, hook.Name = ns, "ci-hook"
	hook.Spec = acr.RegistriesWebhook_Spec{
		AzureName: "cihook", Owner: owner("reg"), Location: ptr(location),
		Actions: []acr.WebhookAction{acr.WebhookAction_Push}, Scope: ptr("apps:*"), Status: ptr(acr.WebhookStatus("enabled")),
		ServiceUri:    &genruntime.SecretReference{Name: "hook", Key: "uri"},
		CustomHeaders: &genruntime.SecretMapReference{Name: "hook"},
	}
	create(t, timeout, scopeMap, token, hook)
	tokenID := regID + "/tokens/ci-puller"
	hookID := regID + "/webhooks/cihook"
	expectResource(t, scopeMapID, acrAPI, func(r map[string]any) error {
		if p := properties(r); p["description"] != "read the apps repository" ||
			!reflect.DeepEqual(p["actions"], []any{"repositories/apps/content/read", "repositories/apps/metadata/read"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, tokenID, acrAPI, func(r map[string]any) error {
		if p := properties(r); !strings.EqualFold(fmt.Sprint(p["scopeMapId"]), scopeMapID) || p["status"] != "enabled" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, hookID, acrAPI, func(r map[string]any) error {
		if p := properties(r); p["scope"] != "apps:*" || !reflect.DeepEqual(p["actions"], []any{"push"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, token, func(tk *acr.RegistriesToken) { tk.Spec.Status = ptr(acr.TokenStatus("disabled")) })
	eventually(t, timeout, "RegistriesToken Ready after the change", ready(token))
	expectResource(t, tokenID, acrAPI, func(r map[string]any) error {
		if properties(r)["status"] != "disabled" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// The credential set's secrets are named, not read, when it is made: the
	// registry reads them (with the set's identity) only when it pulls.
	vaultURI := "https://" + strings.ReplaceAll(azName("kv"), "-", "") + ".vault.azure.net/secrets/"
	cs := &acr.RegistriesCredentialSet{}
	cs.Namespace, cs.Name = ns, "ghcr"
	cs.Spec = acr.RegistriesCredentialSet_Spec{
		AzureName: "ghcr-hub", Owner: owner("reg"), LoginServer: ptr("ghcr.io"),
		Identity: &acr.IdentityProperties{Type: ptr(acr.ResourceIdentityType_SystemAssigned)},
		AuthCredentials: []acr.AuthCredential{{
			Name:                     ptr(acr.CredentialName("Credential1")),
			UsernameSecretIdentifier: ptr(vaultURI + "ghcr-user"),
			PasswordSecretIdentifier: ptr(vaultURI + "ghcr-token"),
		}},
	}
	create(t, timeout, cs)
	csID := regID + "/credentialSets/ghcr-hub"
	expectResource(t, csID, acrAPI, func(r map[string]any) error {
		p := properties(r)
		creds, _ := p["authCredentials"].([]any)
		if p["loginServer"] != "ghcr.io" || len(creds) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range []conditioned{cs, hook, token, scopeMap} {
		remove(t, timeout, o)
	}
	for _, id := range []string{csID, hookID, tokenID, scopeMapID} {
		expectGone(t, id, acrAPI)
	}
}

// A Container Apps environment's Dapr component, Azure Files storage,
// certificate (an uploaded self-signed PFX) and Java component (a Eureka
// server, billed by the second while it runs), on a Consumption environment
// fixture (free while idle).
func TestLiveContainerApps(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.App")
	ns := namespace(t, "aca")
	envName := azName("env")
	envID := rgID() + "/providers/Microsoft.App/managedEnvironments/" + envName
	fixture(t, envID, "2024-03-01", map[string]any{
		"location": location,
		"properties": map[string]any{
			"workloadProfiles": []any{map[string]any{"name": "Consumption", "workloadProfileType": "Consumption"}},
		},
	})
	env := &asoapp.ManagedEnvironment{}
	env.Namespace, env.Name = ns, "env"
	env.Spec.AzureName, env.Spec.Owner = envName, owner("rg")
	standIn(t, env, envID, nil)

	storageID, storageName := storageAccount(t, "acast")
	fixture(t, storageID+"/fileServices/default/shares/data", "2023-05-01", map[string]any{"properties": map[string]any{"shareQuota": 1}})
	keys, _ := post(t, storageID+"/listKeys", "2023-05-01")["keys"].([]any)
	if len(keys) == 0 {
		t.Fatal("no storage account keys")
	}
	key, _ := keys[0].(map[string]any)["value"].(string)
	pfxBlob, pfxPass := pfx(t, "live.example.com")
	secret(t, ns, "app", map[string]string{"redis": "redis-password-1", "share": key, "pfx": pfxBlob, "pfx-password": pfxPass})

	dapr := &appv1.ManagedEnvironmentsDaprComponent{}
	dapr.Namespace, dapr.Name = ns, "statestore"
	dapr.Spec = appv1.ManagedEnvironmentsDaprComponent_Spec{
		Owner: owner("env"), ComponentType: ptr("state.redis"), Version: ptr("v1"),
		Metadata: []appv1.DaprMetadata{
			{Name: ptr("redisHost"), Value: ptr("redis.example.com:6379")},
			{Name: ptr("redisPassword"), SecretRef: ptr("redis-password")},
		},
		Secrets: []appv1.Secret{{Name: ptr("redis-password"), Value: &genruntime.SecretReference{Name: "app", Key: "redis"}}},
	}
	files := &appv1.ManagedEnvironmentsStorage{}
	files.Namespace, files.Name = ns, "files"
	files.Spec = appv1.ManagedEnvironmentsStorage_Spec{Owner: owner("env"), Properties: &appv1.ManagedEnvironments_Storage_Properties_Spec{
		AzureFile: &appv1.AzureFileProperties{
			AccountName: ptr(storageName), ShareName: ptr("data"),
			AccountKey: &genruntime.SecretReference{Name: "app", Key: "share"},
			AccessMode: ptr(appv1.AzureFileProperties_AccessMode("ReadOnly")),
		},
	}}
	cert := &appv1.ManagedEnvironmentsCertificate{}
	cert.Namespace, cert.Name = ns, "tls"
	cert.Spec = appv1.ManagedEnvironmentsCertificate_Spec{Owner: owner("env"), Location: ptr(location), Properties: &appv1.ManagedEnvironments_Certificate_Properties_Spec{
		Value:    &genruntime.SecretReference{Name: "app", Key: "pfx"},
		Password: &genruntime.SecretReference{Name: "app", Key: "pfx-password"},
	}}
	eureka := &appv1.ManagedEnvironmentsJavaComponent{}
	eureka.Namespace, eureka.Name = ns, "eureka"
	eureka.Spec = appv1.ManagedEnvironmentsJavaComponent_Spec{Owner: owner("env"), Properties: &appv1.JavaComponentProperties{
		SpringCloudEureka: &appv1.SpringCloudEurekaComponent{ComponentType: ptr(appv1.SpringCloudEurekaComponent_ComponentType_SpringCloudEureka)},
	}}
	create(t, timeout, dapr, files, cert, eureka)

	daprID := envID + "/daprComponents/statestore"
	expectResource(t, daprID, appAPI, func(r map[string]any) error {
		if properties(r)["componentType"] != "state.redis" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, envID+"/storages/files", appAPI, func(r map[string]any) error {
		af, _ := properties(r)["azureFile"].(map[string]any)
		if af["accountName"] != storageName || af["shareName"] != "data" || af["accessMode"] != "ReadOnly" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, envID+"/certificates/tls", appAPI, func(r map[string]any) error {
		if p := properties(r); p["subjectName"] != "CN=live.example.com" && p["subjectName"] != "live.example.com" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, envID+"/javaComponents/eureka", appAPI, func(r map[string]any) error {
		if properties(r)["componentType"] != "SpringCloudEureka" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	update(t, dapr, func(d *appv1.ManagedEnvironmentsDaprComponent) { d.Spec.Scopes = []string{"orders"} })
	eventually(t, timeout, "DaprComponent Ready after the change", ready(dapr))
	expectResource(t, daprID, appAPI, func(r map[string]any) error {
		if !reflect.DeepEqual(properties(r)["scopes"], []any{"orders"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range []conditioned{eureka, cert, files, dapr} {
		remove(t, timeout, o)
	}
	for _, s := range []string{"/javaComponents/eureka", "/certificates/tls", "/storages/files", "/daprComponents/statestore"} {
		expectGone(t, envID+s, appAPI)
	}
}

// An App Service deployment slot, host name bindings on the site's and the
// slot's own azurewebsites.net names (no custom domain to verify), and an
// uploaded certificate, on a Linux plan: Standard S1 (about $0.10 an hour)
// or Premium v3 P0v3 (about $0.08 an hour), which have slots, else Basic B1
// (about $0.02 an hour), which has certificates and host name bindings but
// no slots. The sandbox subscription had no S1 quota in eastus2
// (2026-11-12), so the plan goes to the first SKU and region it takes.
func TestLiveWebSlotAndCertificate(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.Web")
	ns := namespace(t, "web")
	type plan struct{ name, tier string }
	var cands []candidate
	var plans []plan
	for _, p := range []plan{{"S1", "Standard"}, {"P0v3", "PremiumV3"}, {"B1", "Basic"}} {
		for _, region := range regions() {
			cands = append(cands, candidate{
				id: rgID() + "/providers/Microsoft.Web/serverfarms/" + azName(fmt.Sprintf("plan%d", len(cands))),
				body: map[string]any{
					"location": region, "kind": "linux", "sku": map[string]any{"name": p.name, "tier": p.tier},
					"properties": map[string]any{"reserved": true},
				},
			})
			plans = append(plans, p)
		}
	}
	i, _ := fixtureFirst(t, timeout, "2023-12-01", cands)
	planID, webRegion, sku := cands[i].id, fmt.Sprint(cands[i].body["location"]), plans[i]
	t.Logf("App Service plan %s (%s) in %s", sku.name, sku.tier, webRegion)
	siteName := azName("site")
	siteID := rgID() + "/providers/Microsoft.Web/sites/" + siteName
	fixture(t, siteID, "2023-12-01", map[string]any{
		"location": webRegion, "kind": "app,linux",
		"properties": map[string]any{"serverFarmId": planID, "httpsOnly": true, "siteConfig": map[string]any{"linuxFxVersion": "NODE|20-lts"}},
	})
	site := &asoweb.Site{}
	site.Namespace, site.Name = ns, "site"
	site.Spec.AzureName, site.Spec.Owner = siteName, owner("rg")
	standIn(t, site, siteID, nil)

	pfxBlob, pfxPass := pfx(t, "live.example.com")
	secret(t, ns, "pfx", map[string]string{"blob": pfxBlob, "password": pfxPass})
	cert := &webv1.Certificate{}
	cert.Namespace, cert.Name = ns, "www"
	cert.Spec = webv1.Certificate_Spec{
		AzureName: azName("www"), Owner: owner("rg"), Location: ptr(webRegion),
		PfxBlob:             &genruntime.SecretReference{Name: "pfx", Key: "blob"},
		Password:            &genruntime.SecretReference{Name: "pfx", Key: "password"},
		ServerFarmReference: &genruntime.ResourceReference{ARMID: planID},
	}
	create(t, timeout, cert)
	certID := rgID() + "/providers/Microsoft.Web/certificates/" + cert.Spec.AzureName
	expectResource(t, certID, webAPI, func(r map[string]any) error {
		if p := properties(r); p["subjectName"] != "live.example.com" || p["thumbprint"] == nil {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, cert, func(c *webv1.Certificate) { c.Spec.Tags = map[string]string{"team": "platform"} })
	eventually(t, timeout, "Certificate Ready after the change", ready(cert))
	expectResource(t, certID, webAPI, func(r map[string]any) error {
		if tags, _ := r["tags"].(map[string]any); tags["team"] != "platform" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, cert)
	expectGone(t, certID, webAPI)

	// Host name bindings: a custom domain needs DNS records Azure checks, in a
	// zone we do not have; the site's own azurewebsites.net name needs none.
	t.Run("HostNameBinding", func(t *testing.T) {
		host := siteName + ".azurewebsites.net"
		b := &webv1.SitesHostNameBinding{}
		b.Namespace, b.Name = ns, "default-host"
		b.Spec = webv1.SitesHostNameBinding_Spec{AzureName: host, Owner: owner("site"), SiteName: ptr(siteName), HostNameType: ptr(webv1.HostNameType("Verified"))}
		create(t, 5*time.Minute, b)
		expectResource(t, siteID+"/hostNameBindings/"+host, webAPI, func(r map[string]any) error {
			if properties(r)["siteName"] != siteName {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})
		// The default host name's binding is Azure's: made with the site and
		// never deleted (a DELETE of it is refused, and the object waited in
		// deletion, run ax12160408f5). Detach it, and Azure keeps it.
		setReconcilePolicy(t, b, annotations.ReconcilePolicyDetachOnDelete)
		remove(t, 5*time.Minute, b)
		expectResource(t, siteID+"/hostNameBindings/"+host, webAPI, func(r map[string]any) error { return nil })
	})

	t.Run("Slot", func(t *testing.T) {
		if sku.tier == "Basic" {
			t.Skipf("a Basic plan has no deployment slots, and the subscription took no Standard S1 or Premium v3 P0v3 plan in %v (quota): the builder requests App Service quota for one of them", regions())
		}
		slot := &webv1.SitesSlot{}
		slot.Namespace, slot.Name = ns, "staging"
		slot.Spec = webv1.SitesSlot_Spec{
			AzureName: "staging", Owner: owner("site"), Location: ptr(webRegion), Kind: ptr("app,linux"), HttpsOnly: ptr(true),
			ServerFarmReference: &genruntime.ResourceReference{ARMID: planID},
		}
		create(t, timeout, slot)
		slotID := siteID + "/slots/staging"
		expectResource(t, slotID, webAPI, func(r map[string]any) error {
			if p := properties(r); p["httpsOnly"] != true || !strings.EqualFold(fmt.Sprint(p["serverFarmId"]), planID) {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})
		update(t, slot, func(s *webv1.SitesSlot) { s.Spec.HttpsOnly = ptr(false) })
		eventually(t, timeout, "SitesSlot Ready after the change", ready(slot))
		expectResource(t, slotID, webAPI, func(r map[string]any) error {
			if properties(r)["httpsOnly"] != false {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})

		slotHost := siteName + "-staging.azurewebsites.net"
		sb := &webv1.SitesSlotsHostNameBinding{}
		sb.Namespace, sb.Name = ns, "slot-host"
		sb.Spec = webv1.SitesSlotsHostNameBinding_Spec{AzureName: slotHost, Owner: owner("staging"), SiteName: ptr(siteName), HostNameType: ptr(webv1.HostNameType("Verified"))}
		create(t, 5*time.Minute, sb)
		expectResource(t, slotID+"/hostNameBindings/"+slotHost, webAPI, func(r map[string]any) error { return nil })
		setReconcilePolicy(t, sb, annotations.ReconcilePolicyDetachOnDelete) // the slot's default host name, as the site's
		remove(t, 5*time.Minute, sb)
		expectResource(t, slotID+"/hostNameBindings/"+slotHost, webAPI, func(r map[string]any) error { return nil })

		remove(t, timeout, slot)
		expectGone(t, slotID, webAPI)
	})
}
