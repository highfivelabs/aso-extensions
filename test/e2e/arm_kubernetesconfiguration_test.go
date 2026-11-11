// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	kc "github.com/highfivelabs/aso-extensions/apis/arm/kubernetesconfiguration/v20230501"
	kcpls "github.com/highfivelabs/aso-extensions/apis/arm/kubernetesconfiguration/v20241101preview"
)

// A source control configuration (legacy Flux v1 GitOps) on an AKS cluster
// by ARM ID, its SSH key a protected setting from a Secret that reaches
// Resource Manager and neither the status nor a GET, its repository
// changed; a private link scope for an Arc-enabled cluster and a private
// endpoint connection to it approved; all deleted.
func TestSourceControlConfigurationAndPrivateLinkScope(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "gitops")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	clusterID := rgID + "/providers/Microsoft.ContainerService/managedClusters/" + ns + "-aks"
	arcID := rgID + "/providers/Microsoft.Kubernetes/connectedClusters/" + ns + "-arc"
	for _, id := range []string{rgID, clusterID, arcID} {
		armFake.AddResource(id)
	}
	resourceGroup(t, ns, "rg", ns+"-rg")
	secret(t, ns, "flux-ssh", map[string]string{"sshPrivateKey": "c3NoLWtleQ=="})

	config := &kc.SourceControlConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cluster-config"},
		Spec: kc.SourceControlConfiguration_Spec{
			Owner:                          &genruntime.ArbitraryOwnerReference{ARMID: clusterID},
			RepositoryUrl:                  ptr("git@github.com:contoso/cluster-config.git"),
			OperatorType:                   ptr(kc.OperatorTypeDefinition_Flux),
			OperatorScope:                  ptr(kc.OperatorScopeDefinition_Cluster),
			OperatorNamespace:              ptr("flux"),
			OperatorInstanceName:           ptr("cluster-config"),
			OperatorParams:                 ptr("--git-readonly"),
			ConfigurationProtectedSettings: &genruntime.SecretMapReference{Name: "flux-ssh"},
		},
	}
	scope := &kcpls.PrivateLinkScope{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "arc"},
		Spec: kcpls.PrivateLinkScope_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "rg"},
			Location: ptr("westus"),
			Properties: &kcpls.KubernetesConfigurationPrivateLinkScopeProperties{
				ClusterResourceReference: &genruntime.ResourceReference{ARMID: arcID},
				PublicNetworkAccess:      ptr(kcpls.KubernetesConfigurationPrivateLinkScopeProperties_PublicNetworkAccess_Disabled),
			},
		},
	}
	for _, o := range []client.Object{config, scope} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []client.Object{config, scope} {
		eventually(t, timeout, fmt.Sprintf("%T Ready", o), ready(o.(conditioned)))
	}
	configID := clusterID + "/providers/Microsoft.KubernetesConfiguration/sourceControlConfigurations/cluster-config"
	expectResource(t, configID, func(r map[string]any) error {
		p := properties(r)
		if r["type"] != "Microsoft.KubernetesConfiguration/sourceControlConfigurations" || p["repositoryUrl"] != "git@github.com:contoso/cluster-config.git" ||
			p["operatorScope"] != "cluster" || p["operatorNamespace"] != "flux" || p["configurationProtectedSettings"] != nil {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	if k, ok := armFake.SharedKey(configID + "/sshPrivateKey"); !ok || k != "c3NoLWtleQ==" {
		t.Fatalf("the protected setting Resource Manager was given: %q %v", k, ok)
	}
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	if status, _ := json.Marshal(config.Status); strings.Contains(string(status), "c3NoLWtleQ==") {
		t.Fatalf("the protected setting is in the status: %s", status)
	}
	config.Spec.RepositoryUrl = ptr("git@github.com:contoso/cluster-config-v2.git")
	if err := k8s.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	expectResource(t, configID, func(r map[string]any) error {
		if properties(r)["repositoryUrl"] != "git@github.com:contoso/cluster-config-v2.git" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	scopeID := rgID + "/providers/Microsoft.KubernetesConfiguration/privateLinkScopes/arc"
	expectResource(t, scopeID, func(r map[string]any) error {
		p := properties(r)
		if r["type"] != "Microsoft.KubernetesConfiguration/privateLinkScopes" || p["clusterResourceId"] != arcID || p["publicNetworkAccess"] != "Disabled" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	connectionID := scopeID + "/privateEndpointConnections/from-hub"
	armFake.AddResource(connectionID)
	connection := &kcpls.PrivateLinkScopesPrivateEndpointConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "from-hub"},
		Spec: kcpls.PrivateLinkScopesPrivateEndpointConnection_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "arc"},
			PrivateLinkServiceConnectionState: &kcpls.PrivateLinkServiceConnectionState{
				Status: ptr(kcpls.PrivateEndpointServiceConnectionStatus_Approved), Description: ptr("the hub's endpoint"),
			},
		},
	}
	if err := k8s.Create(ctx, connection); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "connection Ready", ready(connection))
	expectResource(t, connectionID, func(r map[string]any) error {
		state, _ := properties(r)["privateLinkServiceConnectionState"].(map[string]any)
		if state["status"] != "Approved" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range []client.Object{connection, config, scope} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T gone", o), gone(o))
	}
	for _, id := range []string{connectionID, configID, scopeID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
	if _, ok := armFake.SharedKey(configID + "/sshPrivateKey"); ok {
		t.Fatal("the protected setting outlived its configuration")
	}
}

// A source control configuration the fake refuses: on a virtual machine.
func TestSourceControlConfigurationRefused(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "gitops-no")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	vmID := rgID + "/providers/Microsoft.Compute/virtualMachines/vm1"
	armFake.AddResource(rgID)
	armFake.AddResource(vmID)
	config := &kc.SourceControlConfiguration{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cluster-config"},
		Spec: kc.SourceControlConfiguration_Spec{
			Owner:         &genruntime.ArbitraryOwnerReference{ARMID: vmID},
			RepositoryUrl: ptr("https://github.com/contoso/cluster-config"),
		},
	}
	if err := k8s.Create(ctx, config); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the refusal on the Ready condition", notReadyWith(config, "managed or Arc-enabled Kubernetes cluster"))
}
