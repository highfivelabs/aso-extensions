// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"testing"

	fleets "github.com/highfivelabs/aso-extensions/apis/arm/documentdb/v20260315"
)

const cosmosFleetAPI = "2026-03-15"

// A Cosmos DB fleet and a fleetspace in it with no throughput pool (both
// free: a fleet bills only for the pool a fleetspace is given). Each is
// made, the fleetspace's tier changed, and both deleted (docs/plan.md f47).
// A fleetspace account (it pools a provisioned-throughput account) and the
// MongoDB vCore cluster users (they need a cluster with Microsoft Entra
// authentication) are not made here.
func TestLiveCosmosFleet(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.DocumentDB")
	ns := namespace(t, "cosmosfleet")

	fleet := &fleets.Fleet{}
	fleet.Namespace, fleet.Name = ns, "tenants"
	fleet.Spec = fleets.Fleet_Spec{AzureName: azName("fleet"), Owner: owner("rg"), Location: ptr(location)}
	create(t, timeout, fleet)
	fleetID := rgID() + "/providers/Microsoft.DocumentDB/fleets/" + fleet.Spec.AzureName
	expectResource(t, fleetID, cosmosFleetAPI, func(r map[string]any) error { return nil })

	space := &fleets.FleetsFleetspace{}
	space.Namespace, space.Name = ns, "shared"
	space.Spec = fleets.FleetsFleetspace_Spec{
		AzureName: "shared", Owner: owner("tenants"),
		DataRegions:       []string{location},
		FleetspaceApiKind: ptr(fleets.FleetspacePropertiesFleetspaceApiKind_NoSQL),
		ServiceTier:       ptr(fleets.FleetspacePropertiesServiceTier_GeneralPurpose),
	}
	create(t, timeout, space)
	spaceID := fleetID + "/fleetspaces/shared"
	expectResource(t, spaceID, cosmosFleetAPI, func(r map[string]any) error {
		if properties(r)["serviceTier"] != "GeneralPurpose" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	update(t, space, func(s *fleets.FleetsFleetspace) {
		s.Spec.ServiceTier = ptr(fleets.FleetspacePropertiesServiceTier_BusinessCritical)
	})
	eventually(t, timeout, "the fleetspace Ready after the change", ready(space))
	expectResource(t, spaceID, cosmosFleetAPI, func(r map[string]any) error {
		if properties(r)["serviceTier"] != "BusinessCritical" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, space)
	expectGone(t, spaceID, cosmosFleetAPI)
	remove(t, timeout, fleet)
	expectGone(t, fleetID, cosmosFleetAPI)
}
