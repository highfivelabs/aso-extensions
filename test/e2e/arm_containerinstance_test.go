// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	aci "github.com/highfivelabs/aso-extensions/apis/arm/containerinstance/v20260701"
)

// The Container Instances kinds of docs/plan.md f67: a container group
// profile whose container takes a secure environment variable and a secret
// volume from Secrets, an NGroup of it (by object) with a desired count, and
// a sandbox group. A profile of two containers of one name and an NGroup
// naming a missing profile are refused (the fake's rules,
// containerinstance.go); the count's change is PUT again; deleted, all go.
func TestContainerInstanceKinds(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "aci")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	armFake.AddResource(rgID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	for name, data := range map[string]map[string]string{
		"web-env":   {"token": "s3cret-token"},
		"web-files": {"settings.json": "eyJkZWJ1ZyI6ZmFsc2V9"},
	} {
		if err := k8s.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}, StringData: data}); err != nil {
			t.Fatal(err)
		}
	}
	rg := &genruntime.KnownResourceReference{Name: "rg"}
	container := func(requests *aci.ResourceRequests) aci.Container {
		return aci.Container{
			Name: ptr("web"), Image: ptr("mcr.microsoft.com/azuredocs/aci-helloworld"),
			Resources:            &aci.ResourceRequirements{Requests: requests},
			EnvironmentVariables: []aci.EnvironmentVariable{{Name: ptr("TOKEN"), SecureValue: &genruntime.SecretReference{Name: "web-env", Key: "token"}}},
			VolumeMounts:         []aci.VolumeMount{{Name: ptr("files"), MountPath: ptr("/etc/web")}},
		}
	}
	profile := &aci.ContainerGroupProfile{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "web"},
		Spec: aci.ContainerGroupProfile_Spec{
			Owner: rg, Location: ptr("westus"), OsType: ptr(aci.OperatingSystemTypes_Linux), Sku: ptr(aci.ContainerGroupSku_Standard),
			Containers: []aci.Container{container(&aci.ResourceRequests{Cpu: ptr(1.0), MemoryInGB: ptr(1.5)})},
			Volumes:    []aci.Volume{{Name: ptr("files"), Secret: &genruntime.SecretMapReference{Name: "web-files"}}},
		},
	}
	sized := &aci.ResourceRequests{Cpu: ptr(0.5), MemoryInGB: ptr(0.5)}
	twins := &aci.ContainerGroupProfile{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "twins"},
		Spec: aci.ContainerGroupProfile_Spec{
			Owner: rg, Location: ptr("westus"), OsType: ptr(aci.OperatingSystemTypes_Linux),
			Containers: []aci.Container{container(sized), container(sized)},
			Volumes:    []aci.Volume{{Name: ptr("files"), Secret: &genruntime.SecretMapReference{Name: "web-files"}}},
		},
	}
	sandbox := &aci.SandboxGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "builds"},
		Spec:       aci.SandboxGroup_Spec{Owner: rg, Location: ptr("westus")},
	}
	first := []conditioned{profile, twins, sandbox}
	for _, o := range first {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "the profile Ready", ready(profile))
	eventually(t, timeout, "the sandbox group Ready", ready(sandbox))
	eventually(t, timeout, "two containers of one name refused", notReadyWith(twins, "unique in the profile"))

	profileID := rgID + "/providers/Microsoft.ContainerInstance/containerGroupProfiles/web"
	expectResource(t, profileID, func(r map[string]any) error {
		p := properties(r)
		containers, _ := p["containers"].([]any)
		volumes, _ := p["volumes"].([]any)
		if len(containers) != 1 || len(volumes) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		cp, _ := containers[0].(map[string]any)["properties"].(map[string]any)
		env, _ := cp["environmentVariables"].([]any)
		secret, _ := volumes[0].(map[string]any)["secret"].(map[string]any)
		if len(env) != 1 || env[0].(map[string]any)["secureValue"] != "s3cret-token" || secret["settings.json"] != "eyJkZWJ1ZyI6ZmFsc2V9" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	ngroup := &aci.NGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "web-fleet"},
		Spec: aci.NGroup_Spec{
			Owner: rg, Location: ptr("westus"),
			ElasticProfile: &aci.ElasticProfile{DesiredCount: ptr(2), MaintainDesiredCount: ptr(true)},
			ContainerGroupProfiles: []aci.ContainerGroupProfileStub{{Resource: &aci.ApiEntityReference{Reference: &genruntime.ResourceReference{
				Group: "containerinstance.azure.hfl.dev", Kind: "ContainerGroupProfile", Name: "web",
			}}}},
		},
	}
	orphan := &aci.NGroup{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "orphan"},
		Spec: aci.NGroup_Spec{
			Owner: rg, Location: ptr("westus"),
			ContainerGroupProfiles: []aci.ContainerGroupProfileStub{{Resource: &aci.ApiEntityReference{Reference: &genruntime.ResourceReference{
				ARMID: rgID + "/providers/Microsoft.ContainerInstance/containerGroupProfiles/gone",
			}}}},
		},
	}
	second := []conditioned{ngroup, orphan}
	for _, o := range second {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, timeout, "the NGroup Ready", ready(ngroup))
	eventually(t, timeout, "a missing profile refused", notReadyWith(orphan, "was not found"))
	ngroupID := rgID + "/providers/Microsoft.ContainerInstance/ngroups/web-fleet"
	expectResource(t, ngroupID, func(r map[string]any) error {
		profiles, _ := properties(r)["containerGroupProfiles"].([]any)
		if len(profiles) != 1 || profiles[0].(map[string]any)["resource"].(map[string]any)["id"] != profileID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again: three groups.
	if err := change(t, ngroup, func(n *aci.NGroup) { n.Spec.ElasticProfile.DesiredCount = ptr(3) }); err != nil {
		t.Fatal(err)
	}
	expectResource(t, ngroupID, func(r map[string]any) error {
		elastic, _ := properties(r)["elasticProfile"].(map[string]any)
		if elastic["desiredCount"] != float64(3) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, objs := range [][]conditioned{second, first} {
		for _, o := range objs {
			if err := k8s.Delete(ctx, o); err != nil {
				t.Fatal(err)
			}
		}
		for _, o := range objs {
			eventually(t, timeout, o.GetName()+" gone", gone(o))
		}
	}
	for _, id := range []string{profileID, ngroupID, rgID + "/providers/Microsoft.ContainerInstance/sandboxGroups/builds"} {
		if armFake.HasResource(id) {
			t.Errorf("%s is still in the fake", id)
		}
	}
}
