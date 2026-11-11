// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	asokeyvault "github.com/Azure/azure-service-operator/v2/api/keyvault/v1api20230701/storage"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"

	kv "github.com/highfivelabs/aso-extensions/apis/arm/keyvault/v20260515"
)

const (
	hsmTenant = "00000000-0000-0000-0000-0000000000aa"
	hsmAdmin  = "00000000-0000-0000-0000-0000000000bb"
)

// A managed HSM in a resource group; a key in it, which a change does not
// reach (Azure's PUT on an existing key returns it unchanged) and which
// Resource Manager cannot delete (the object goes with detach-on-delete);
// a private endpoint connection a private endpoint made, approved through
// the kind and then removed; the HSM changed and deleted.
func TestKeyVaultManagedHSM(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "kv-hsm")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")

	hsm := &kv.ManagedHSM{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "hsm"},
		Spec: kv.ManagedHSM_Spec{
			AzureName: ns + "-hsm",
			Owner:     &genruntime.KnownResourceReference{Name: "rg"},
			Location:  ptr("westus"),
			Sku:       &kv.ManagedHsmSku{Family: ptr(kv.ManagedHsmSkuFamily_B), Name: ptr(kv.ManagedHsmSkuNameV2_Standard_B1)},
			Properties: &kv.ManagedHsmProperties{
				TenantId:                  ptr(kv.AzureCoreUuid(hsmTenant)),
				InitialAdminObjectIds:     []string{hsmAdmin},
				SoftDeleteRetentionInDays: ptr(30),
				EnablePurgeProtection:     ptr(true),
				PublicNetworkAccess:       ptr(kv.ManagedHsmProperties_PublicNetworkAccess_Enabled),
			},
		},
	}
	if err := k8s.Create(ctx, hsm); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "ManagedHSM Ready", ready(hsm))
	hsmID := rgID + "/providers/Microsoft.KeyVault/managedHSMs/" + ns + "-hsm"
	expectResource(t, hsmID, func(r map[string]any) error {
		p := properties(r)
		sku, _ := r["sku"].(map[string]any)
		if r["type"] != "Microsoft.KeyVault/managedHSMs" || p["tenantId"] != hsmTenant ||
			!reflect.DeepEqual(p["initialAdminObjectIds"], []any{hsmAdmin}) || p["softDeleteRetentionInDays"] != float64(30) ||
			p["enablePurgeProtection"] != true || sku["name"] != "Standard_B1" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A key in the HSM.
	key := &kv.ManagedHSMsKey{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cmk"},
		Spec: kv.ManagedHSMsKey_Spec{
			AzureName: "storage-cmk",
			Owner:     &genruntime.KnownResourceReference{Name: "hsm"},
			Properties: &kv.ManagedHsmKeyProperties{
				Kty:     ptr(kv.JsonWebKeyType_RSAHSM),
				KeySize: ptr(3072),
				KeyOps:  []kv.JsonWebKeyOperation{kv.JsonWebKeyOperation_WrapKey, kv.JsonWebKeyOperation_UnwrapKey},
			},
		},
	}
	if err := k8s.Create(ctx, key); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "ManagedHSMsKey Ready", ready(key))
	keyID := hsmID + "/keys/storage-cmk"
	expectResource(t, keyID, func(r map[string]any) error {
		p := properties(r)
		if r["type"] != "Microsoft.KeyVault/managedHSMs/keys" || p["kty"] != "RSA-HSM" || p["keySize"] != float64(3072) ||
			!reflect.DeepEqual(p["keyOps"], []any{"wrapKey", "unwrapKey"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change of the key is sent, and Azure returns the key unchanged: the
	// object is Ready again with the key as it was. A key's properties are
	// set when it is made; another key (another name) is the way to change
	// them.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(key), key); err != nil {
		t.Fatal(err)
	}
	key.Spec.Properties.KeySize = ptr(4096)
	if err := k8s.Update(ctx, key); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the changed key Ready with Azure's key", func() error {
		if err := ready(key)(); err != nil {
			return err
		}
		if key.Status.KeySize == nil || *key.Status.KeySize != 3072 {
			return fmt.Errorf("status keySize: %v", key.Status.KeySize)
		}
		return nil
	})
	expectResource(t, keyID, func(r map[string]any) error {
		if properties(r)["keySize"] != float64(3072) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// Deleting the key: the spec has no DELETE, so the object waits with
	// that on its Ready condition, and detach-on-delete lets it go with the
	// key staying in the HSM.
	if err := k8s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the refused delete on the Ready condition", notReadyWith(key, "does not support deletion"))
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(key), key); err != nil {
		t.Fatal(err)
	}
	key.Annotations = map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}
	key.Spec.Tags = map[string]string{"detached": "true"} // a spec change reconciles at once
	if err := k8s.Update(ctx, key); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the key object gone", gone(key))
	if !armFake.HasResource(keyID) {
		t.Fatal("detaching the key removed it from the HSM")
	}

	// A private endpoint connection, made by a private endpoint in another
	// subscription (the fake's AddResource), approved by the kind.
	connID := hsmID + "/privateEndpointConnections/pe-app"
	armFake.AddResource(connID)
	conn := &kv.ManagedHSMsPrivateEndpointConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "pe-app"},
		Spec: kv.ManagedHSMsPrivateEndpointConnection_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "hsm"},
			PrivateLinkServiceConnectionState: &kv.MHSMPrivateLinkServiceConnectionState{
				Status:      ptr(kv.PrivateEndpointServiceConnectionStatus_Approved),
				Description: ptr("the app's endpoint"),
			},
		},
	}
	if err := k8s.Create(ctx, conn); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "ManagedHSMsPrivateEndpointConnection Ready", ready(conn))
	expectResource(t, connID, func(r map[string]any) error {
		state, _ := properties(r)["privateLinkServiceConnectionState"].(map[string]any)
		if state["status"] != "Approved" || state["description"] != "the app's endpoint" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// The HSM changed: public access off.
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(hsm), hsm); err != nil {
		t.Fatal(err)
	}
	hsm.Spec.Properties.PublicNetworkAccess = ptr(kv.ManagedHsmProperties_PublicNetworkAccess_Disabled)
	if err := k8s.Update(ctx, hsm); err != nil {
		t.Fatal(err)
	}
	expectResource(t, hsmID, func(r map[string]any) error {
		if properties(r)["publicNetworkAccess"] != "Disabled" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range []client.Object{conn, hsm} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T gone", o), gone(o))
	}
	for _, id := range []string{connID, hsmID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
}

// A managed HSM the fake refuses: soft delete cannot be turned off; the
// refusal is on the Ready condition.
func TestKeyVaultManagedHSMRefused(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "kv-hsm-no")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	armFake.AddResource("/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg")
	resourceGroup(t, ns, "rg", ns+"-rg")
	hsm := &kv.ManagedHSM{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "hsm"},
		Spec: kv.ManagedHSM_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
			Sku:      &kv.ManagedHsmSku{Family: ptr(kv.ManagedHsmSkuFamily_B), Name: ptr(kv.ManagedHsmSkuNameV2_Standard_B1)},
			Properties: &kv.ManagedHsmProperties{
				TenantId:              ptr(kv.AzureCoreUuid(hsmTenant)),
				InitialAdminObjectIds: []string{hsmAdmin},
				EnableSoftDelete:      ptr(false),
			},
		},
	}
	if err := k8s.Create(ctx, hsm); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the refusal on the Ready condition", notReadyWith(hsm, "Soft delete is always on"))
}

// A private endpoint connection of ASO's Vault (keyvault.azure.com), at our
// API version: approved, then rejected, then removed.
func TestKeyVaultVaultPrivateEndpointConnection(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "kv-pec")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	vaultID := rgID + "/providers/Microsoft.KeyVault/vaults/" + ns + "-kv"
	armFake.AddResource(rgID)
	armFake.AddResource(vaultID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	v := &asokeyvault.Vault{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "kv",
		Annotations: map[string]string{genruntime.ResourceIDAnnotation: vaultID}}}
	v.Spec.AzureName = ns + "-kv"
	v.Spec.Owner = &genruntime.KnownResourceReference{Name: "rg"}
	if err := k8s.Create(ctx, v); err != nil {
		t.Fatal(err)
	}
	v.Status.Conditions = []conditions.Condition{asoReady(v.Generation)}
	if err := k8s.Status().Update(ctx, v); err != nil {
		t.Fatal(err)
	}

	connID := vaultID + "/privateEndpointConnections/pe-ci"
	conn := &kv.VaultsPrivateEndpointConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "pe"},
		Spec: kv.VaultsPrivateEndpointConnection_Spec{
			AzureName: "pe-ci",
			Owner:     &genruntime.KnownResourceReference{Name: "kv"},
			PrivateLinkServiceConnectionState: &kv.PrivateLinkServiceConnectionState{
				Status:      ptr(kv.PrivateEndpointServiceConnectionStatus_Approved),
				Description: ptr("CI runners"),
			},
		},
	}
	if err := k8s.Create(ctx, conn); err != nil {
		t.Fatal(err)
	}
	// No private endpoint has asked yet: Azure has no connection to update.
	eventually(t, timeout, "no connection yet on the Ready condition", notReadyWith(conn, "a private endpoint makes it"))
	armFake.AddResource(connID)
	eventually(t, timeout, "VaultsPrivateEndpointConnection Ready", ready(conn))
	expectResource(t, connID, func(r map[string]any) error {
		state, _ := properties(r)["privateLinkServiceConnectionState"].(map[string]any)
		if r["type"] != "Microsoft.KeyVault/vaults/privateEndpointConnections" || state["status"] != "Approved" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if refs := conn.GetOwnerReferences(); len(refs) != 1 || refs[0].Name != "kv" || refs[0].Kind != "Vault" {
		t.Fatalf("owner references: %+v", refs)
	}

	if err := k8s.Get(ctx, client.ObjectKeyFromObject(conn), conn); err != nil {
		t.Fatal(err)
	}
	conn.Spec.PrivateLinkServiceConnectionState.Status = ptr(kv.PrivateEndpointServiceConnectionStatus_Rejected)
	if err := k8s.Update(ctx, conn); err != nil {
		t.Fatal(err)
	}
	expectResource(t, connID, func(r map[string]any) error {
		state, _ := properties(r)["privateLinkServiceConnectionState"].(map[string]any)
		if state["status"] != "Rejected" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if err := k8s.Delete(ctx, conn); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "connection gone", gone(conn))
	if armFake.HasResource(connID) {
		t.Fatal("the connection is still in Resource Manager")
	}
	if !armFake.HasResource(vaultID) {
		t.Fatal("the vault is gone from Resource Manager")
	}
}
