// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	asocompute "github.com/Azure/azure-service-operator/v2/api/compute/v20220301/storage"

	compute2025 "github.com/highfivelabs/aso-extensions/apis/arm/compute/v20250401"
	gallery "github.com/highfivelabs/aso-extensions/apis/arm/compute/v20260303"
)

const (
	restorePointAPI = "2025-04-01"
	galleryAPI      = "2026-03-03"
)

// A restore point collection of a small Linux VM fixture (about $0.03 an
// hour) and a crash-consistent restore point in it (its disk snapshot is
// billed per GB-month: a fraction of a cent for the run), then a gallery
// with a script definition and an in-VM access control profile (definitions
// only, free; a script version needs a script blob, not made here). Each is
// made, the collection's tags changed, and all deleted (docs/plan.md f45).
func TestLiveRestorePoints(t *testing.T) {
	t.Parallel()
	ns := namespace(t, "restorepoints")
	vmID, vmName := linuxVM(t, "rpvm", "10.84.0.0/16")
	vm := &asocompute.VirtualMachine{}
	vm.Namespace, vm.Name = ns, "vm"
	vm.Spec.AzureName, vm.Spec.Owner = vmName, owner("rg")
	standIn(t, vm, vmID, nil)

	collection := &compute2025.RestorePointCollection{}
	collection.Namespace, collection.Name = ns, "points"
	collection.Spec = compute2025.RestorePointCollection_Spec{
		AzureName: azName("points"), Owner: owner("rg"), Location: ptr(location),
		Source: &compute2025.RestorePointCollectionSourceProperties{Reference: &genruntime.ResourceReference{Group: "compute.azure.com", Kind: "VirtualMachine", Name: "vm"}},
	}
	create(t, timeout, collection)
	collectionID := rgID() + "/providers/Microsoft.Compute/restorePointCollections/" + collection.Spec.AzureName
	expectResource(t, collectionID, restorePointAPI, func(r map[string]any) error {
		source, _ := properties(r)["source"].(map[string]any)
		if !strings.EqualFold(fmt.Sprint(source["id"]), vmID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	point := &compute2025.RestorePointCollectionsRestorePoint{}
	point.Namespace, point.Name = ns, "first"
	point.Spec = compute2025.RestorePointCollectionsRestorePoint_Spec{
		AzureName: "first", Owner: owner("points"), ConsistencyMode: ptr(compute2025.ConsistencyModeTypes_CrashConsistent),
	}
	create(t, 20*time.Minute, point)
	pointID := collectionID + "/restorePoints/first"
	expectResource(t, pointID, restorePointAPI, func(r map[string]any) error {
		if p := properties(r); p["consistencyMode"] != "CrashConsistent" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	update(t, collection, func(c *compute2025.RestorePointCollection) { c.Spec.Tags = map[string]string{"team": "platform"} })
	eventually(t, timeout, "RestorePointCollection Ready after the change", ready(collection))
	expectResource(t, collectionID, restorePointAPI, func(r map[string]any) error {
		if tags, _ := r["tags"].(map[string]any); tags["team"] != "platform" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, 20*time.Minute, point)
	expectGone(t, pointID, restorePointAPI)
	remove(t, timeout, collection)
	expectGone(t, collectionID, restorePointAPI)

	t.Run("GalleryScriptAndAccessProfile", func(t *testing.T) {
		gal := &gallery.Gallery{}
		gal.Namespace, gal.Name = ns, "gallery"
		gal.Spec = gallery.Gallery_Spec{AzureName: strings.ReplaceAll(azName("gal"), "-", "_"), Owner: owner("rg"), Location: ptr(location)}
		create(t, timeout, gal)
		script := &gallery.GalleriesScript{}
		script.Namespace, script.Name = ns, "harden"
		script.Spec = gallery.GalleriesScript_Spec{
			AzureName: "harden", Owner: owner("gallery"), Location: ptr(location),
			Properties: &gallery.GalleryScriptProperties{SupportedOSType: ptr(gallery.CommonOperatingSystemTypes_Linux), Description: ptr("live test")},
		}
		profile := &gallery.GalleriesInVMAccessControlProfile{}
		profile.Namespace, profile.Name = ns, "metadata-access"
		profile.Spec = gallery.GalleriesInVMAccessControlProfile_Spec{
			AzureName: "metadata_access", Owner: owner("gallery"), Location: ptr(location),
			Properties: &gallery.GalleryInVMAccessControlProfileProperties{
				OsType: ptr(gallery.CommonOperatingSystemTypes_Linux), ApplicableHostEndpoint: ptr(gallery.EndpointTypes_IMDS),
			},
		}
		create(t, timeout, script, profile)
		galleryID := rgID() + "/providers/Microsoft.Compute/galleries/" + gal.Spec.AzureName
		expectResource(t, galleryID+"/scripts/harden", galleryAPI, func(r map[string]any) error { return nil })
		expectResource(t, galleryID+"/inVMAccessControlProfiles/metadata_access", galleryAPI, func(r map[string]any) error { return nil })
		remove(t, timeout, script)
		remove(t, timeout, profile)
		remove(t, timeout, gal)
		expectGone(t, galleryID, galleryAPI)
	})
}
