// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	aci "github.com/highfivelabs/aso-extensions/apis/arm/containerinstance/v20260701"
)

const containerInstanceAPI = "2026-07-01"

// A container group profile and an NGroup of none of it (docs/plan.md f67):
// created, the profile's memory changed, deleted. Free: a profile is a
// template, and an NGroup with a desired count of zero runs no container
// group. Not here: sandbox groups (new in 2026-07-01, their network profile
// not known well enough to make cheaply).
func TestLiveContainerGroupProfile(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.ContainerInstance")
	ns := namespace(t, "acip")
	container := func(memory float64) []aci.Container {
		return []aci.Container{{
			Name: ptr("web"), Image: ptr("mcr.microsoft.com/azuredocs/aci-helloworld"),
			Resources: &aci.ResourceRequirements{Requests: &aci.ResourceRequests{Cpu: ptr(0.5), MemoryInGB: ptr(memory)}},
		}}
	}
	profile := &aci.ContainerGroupProfile{}
	profile.Namespace, profile.Name = ns, "web"
	profile.Spec = aci.ContainerGroupProfile_Spec{
		AzureName: azName("web"), Owner: owner("rg"), Location: ptr(location),
		OsType: ptr(aci.OperatingSystemTypes_Linux), Sku: ptr(aci.ContainerGroupSku_Standard), Containers: container(0.5),
	}
	create(t, timeout, profile)
	profileID := rgID() + "/providers/Microsoft.ContainerInstance/containerGroupProfiles/" + profile.Spec.AzureName

	ngroup := &aci.NGroup{}
	ngroup.Namespace, ngroup.Name = ns, "fleet"
	ngroup.Spec = aci.NGroup_Spec{
		AzureName: azName("fleet"), Owner: owner("rg"), Location: ptr(location),
		ElasticProfile: &aci.ElasticProfile{DesiredCount: ptr(0)},
		ContainerGroupProfiles: []aci.ContainerGroupProfileStub{{Resource: &aci.ApiEntityReference{Reference: &genruntime.ResourceReference{
			Group: "containerinstance.azure.hfl.dev", Kind: "ContainerGroupProfile", Name: "web",
		}}}},
	}
	create(t, timeout, ngroup)
	ngroupID := rgID() + "/providers/Microsoft.ContainerInstance/ngroups/" + ngroup.Spec.AzureName
	expectResource(t, ngroupID, containerInstanceAPI, func(r map[string]any) error {
		profiles, _ := properties(r)["containerGroupProfiles"].([]any)
		if len(profiles) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		res, _ := profiles[0].(map[string]any)["resource"].(map[string]any)
		if !strings.EqualFold(fmt.Sprint(res["id"]), profileID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, profile, func(p *aci.ContainerGroupProfile) { p.Spec.Containers = container(1.0) })
	eventually(t, timeout, "the profile Ready after the change", ready(profile))
	expectResource(t, profileID, containerInstanceAPI, func(r map[string]any) error {
		containers, _ := properties(r)["containers"].([]any)
		if len(containers) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		cp, _ := containers[0].(map[string]any)["properties"].(map[string]any)
		res, _ := cp["resources"].(map[string]any)
		req, _ := res["requests"].(map[string]any)
		if req["memoryInGB"] != 1.0 {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, ngroup)
	expectGone(t, ngroupID, containerInstanceAPI)
	remove(t, timeout, profile)
	expectGone(t, profileID, containerInstanceAPI)
}
