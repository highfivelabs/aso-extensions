// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	cdn "github.com/highfivelabs/aso-extensions/apis/arm/cdn/v20261001"
	network "github.com/highfivelabs/aso-extensions/apis/arm/network/v20260301"
	securitydevops "github.com/highfivelabs/aso-extensions/apis/arm/security/v20250301"
	securityconn "github.com/highfivelabs/aso-extensions/apis/arm/security/v20260801preview"
)

// SecurityConnector with its DevOps configuration and Azure DevOps org,
// project and repo hierarchy; the org/project/repo have no ARM DELETE and are
// detached on Kubernetes delete.
func TestSecurityConnectorAndDevOps(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "sec-conn")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	resourceGroup(t, ns, "rg", ns+"-rg")
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)

	detach := map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}
	conn := &securityconn.SecurityConnector{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "azdo-conn"},
		Spec: securityconn.SecurityConnector_Spec{
			Owner:               &genruntime.KnownResourceReference{Name: "rg"},
			Location:            ptr("centralus"),
			EnvironmentName:     ptr(securityconn.CloudName_AzureDevOps),
			HierarchyIdentifier: ptr("00000000-0000-0000-0000-000000000001"),
			Tags:                map[string]string{"env": "test"},
		},
	}
	if err := k8s.Create(ctx, conn); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "SecurityConnector Ready", ready(conn))
	connID := rgID + "/providers/Microsoft.Security/securityConnectors/azdo-conn"
	expectResource(t, connID, func(r map[string]any) error {
		if r["type"] != "Microsoft.Security/securityConnectors" {
			return fmt.Errorf("unexpected resource: %v", r)
		}
		return nil
	})

	devops := &securitydevops.SecurityConnectorsDevOpsConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "default"},
		Spec: securitydevops.SecurityConnectorsDevOpsConfiguration_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "azdo-conn"},
		},
	}
	if err := k8s.Create(ctx, devops); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "SecurityConnectorsDevOpsConfiguration Ready", ready(devops))

	org := &securitydevops.SecurityConnectorsDevOpsAzureDevOpsOrg{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "my-org", Annotations: detach},
		Spec: securitydevops.SecurityConnectorsDevOpsAzureDevOpsOrg_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "default"},
		},
	}
	if err := k8s.Create(ctx, org); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "SecurityConnectorsDevOpsAzureDevOpsOrg Ready", ready(org))

	proj := &securitydevops.SecurityConnectorsDevOpsAzureDevOpsProject{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "my-proj", Annotations: detach},
		Spec: securitydevops.SecurityConnectorsDevOpsAzureDevOpsProject_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "my-org"},
		},
	}
	if err := k8s.Create(ctx, proj); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "SecurityConnectorsDevOpsAzureDevOpsProject Ready", ready(proj))

	repo := &securitydevops.SecurityConnectorsDevOpsAzureDevOpsRepo{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "my-repo", Annotations: detach},
		Spec: securitydevops.SecurityConnectorsDevOpsAzureDevOpsRepo_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "my-proj"},
		},
	}
	if err := k8s.Create(ctx, repo); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "SecurityConnectorsDevOpsAzureDevOpsRepo Ready", ready(repo))

	repoID := connID + "/devops/default/azureDevOpsOrgs/my-org/projects/my-proj/repos/my-repo"
	if !armFake.HasResource(repoID) {
		t.Fatalf("%s missing from Resource Manager", repoID)
	}

	for _, obj := range []client.Object{repo, proj, org, devops, conn} {
		if err := k8s.Delete(ctx, obj); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, obj.GetName()+" gone", gone(obj))
	}
	if armFake.HasResource(connID) {
		t.Fatalf("%s is still in Resource Manager", connID)
	}
}

// Row 24 remainder: DdosCustomPolicy, SecurityPartnerProvider, and
// CdnWebApplicationFirewallPolicy in a resource group.
func TestRow24DdosPartnerCdnWaf(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "row24")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	resourceGroup(t, ns, "rg", ns+"-rg")
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)

	ddos := &network.DdosCustomPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "ddos-policy"},
		Spec: network.DdosCustomPolicy_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
			Tags:     map[string]string{"team": "sec"},
		},
	}
	partner := &network.SecurityPartnerProvider{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "sec-partner"},
		Spec: network.SecurityPartnerProvider_Spec{
			Owner:                &genruntime.KnownResourceReference{Name: "rg"},
			Location:             ptr("westus"),
			SecurityProviderName: ptr(network.SecurityProviderName_ZScaler),
		},
	}
	waf := &cdn.CdnWebApplicationFirewallPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cdn-waf"},
		Spec: cdn.CdnWebApplicationFirewallPolicy_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("Global"),
			Sku:      &cdn.Sku{Name: ptr(cdn.SkuName_Standard_Microsoft)},
			Tags:     map[string]string{"team": "sec"},
		},
	}
	for _, obj := range []client.Object{ddos, partner, waf} {
		if err := k8s.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "DdosCustomPolicy Ready", ready(ddos))
	eventually(t, timeout, "SecurityPartnerProvider Ready", ready(partner))
	eventually(t, timeout, "CdnWebApplicationFirewallPolicy Ready", ready(waf))

	wafID := rgID + "/providers/Microsoft.Cdn/cdnWebApplicationFirewallPolicies/cdn-waf"
	expectResource(t, wafID, func(r map[string]any) error {
		if r["type"] != "Microsoft.Cdn/cdnWebApplicationFirewallPolicies" {
			return fmt.Errorf("unexpected resource: %v", r)
		}
		return nil
	})

	if err := k8s.Get(ctx, client.ObjectKeyFromObject(waf), waf); err != nil {
		t.Fatal(err)
	}
	waf.Spec.Tags = map[string]string{"team": "sec", "tier": "1"}
	if err := k8s.Update(ctx, waf); err != nil {
		t.Fatal(err)
	}
	expectResource(t, wafID, func(r map[string]any) error {
		if tags, _ := r["tags"].(map[string]any); tags["tier"] != "1" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, obj := range []client.Object{ddos, partner, waf} {
		if err := k8s.Delete(ctx, obj); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, obj.GetName()+" gone", gone(obj))
	}
}
