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

	compute2025 "github.com/highfivelabs/aso-extensions/apis/arm/compute/v20250401"
	gallery "github.com/highfivelabs/aso-extensions/apis/arm/compute/v20260303"
)

// The compute kinds of docs/plan.md f45: a restore point collection of ASO's
// VirtualMachine (a stand-in, named by object) and a crash-consistent restore
// point in it; a gallery script and a version of it whose script is a blob
// link. A script version named by a word is refused by the CRD, a
// collection of a VM that is not there by the fake (gallery.go); a change is
// PUT again; all are deleted.
func TestComputeRestorePointsAndGalleryScripts(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "restorepoints")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	rgID := "/subscriptions/" + subID + "/resourceGroups/" + ns + "-rg"
	vmID := rgID + "/providers/Microsoft.Compute/virtualMachines/app"
	armFake.AddResource(rgID)
	armFake.AddResource(vmID)
	resourceGroup(t, ns, "rg", ns+"-rg")
	virtualMachine(t, ns, "app", "rg", ns+"-rg", "app")
	inRG := &genruntime.KnownResourceReference{Name: "rg"}

	collection := &compute2025.RestorePointCollection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "app-points"},
		Spec: compute2025.RestorePointCollection_Spec{
			Owner: inRG, Location: ptr("westus"),
			Source: &compute2025.RestorePointCollectionSourceProperties{
				Reference: &genruntime.ResourceReference{Group: "compute.azure.com", Kind: "VirtualMachine", Name: "app"},
			},
		},
	}
	point := &compute2025.RestorePointCollectionsRestorePoint{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "nightly"},
		Spec: compute2025.RestorePointCollectionsRestorePoint_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "app-points"}, ConsistencyMode: ptr(compute2025.ConsistencyModeTypes_CrashConsistent),
		},
	}
	gal := &gallery.Gallery{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "golden"},
		Spec:       gallery.Gallery_Spec{AzureName: "golden_" + ns[len(ns)-6:], Owner: inRG, Location: ptr("westus")},
	}
	script := &gallery.GalleriesScript{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "harden"},
		Spec: gallery.GalleriesScript_Spec{
			AzureName: "harden", Owner: &genruntime.KnownResourceReference{Name: "golden"}, Location: ptr("westus"),
			Properties: &gallery.GalleryScriptProperties{SupportedOSType: ptr(gallery.CommonOperatingSystemTypes_Linux), Description: ptr("CIS hardening")},
		},
	}
	version := &gallery.GalleriesScriptsVersion{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "harden-1"},
		Spec: gallery.GalleriesScriptsVersion_Spec{
			AzureName: "1.0.0", Owner: &genruntime.KnownResourceReference{Name: "harden"}, Location: ptr("westus"),
			Properties: &gallery.GalleryScriptVersionProperties{PublishingProfile: &gallery.GalleryScriptVersionPublishingProfile{
				Source: &gallery.ScriptSource{ScriptLink: ptr("https://contosoimages.blob.core.windows.net/scripts/harden.sh")},
			}},
		},
	}
	objs := []conditioned{collection, point, gal, script, version}
	for _, o := range objs {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range objs {
		eventually(t, timeout, o.GetName()+" Ready", ready(o))
	}

	collectionID := rgID + "/providers/Microsoft.Compute/restorePointCollections/app-points"
	expectResource(t, collectionID, func(r map[string]any) error {
		source, _ := properties(r)["source"].(map[string]any)
		if source["id"] != vmID {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, collectionID+"/restorePoints/nightly", func(r map[string]any) error {
		if properties(r)["consistencyMode"] != "CrashConsistent" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	versionID := rgID + "/providers/Microsoft.Compute/galleries/" + gal.Spec.AzureName + "/scripts/harden/versions/1.0.0"
	expectResource(t, versionID, func(r map[string]any) error {
		profile, _ := properties(r)["publishingProfile"].(map[string]any)
		source, _ := profile["source"].(map[string]any)
		if source["scriptLink"] != "https://contosoimages.blob.core.windows.net/scripts/harden.sh" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A change is PUT again.
	if err := change(t, script, func(script *gallery.GalleriesScript) {
		script.Spec.Properties.Description = ptr("CIS level 2 hardening")
	}); err != nil {
		t.Fatal(err)
	}
	expectResource(t, rgID+"/providers/Microsoft.Compute/galleries/"+gal.Spec.AzureName+"/scripts/harden", func(r map[string]any) error {
		if properties(r)["description"] != "CIS level 2 hardening" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// A version named by a word is refused by the CRD (the spec's pattern);
	// restore points of a VM that is not there are refused by the fake.
	latest := &gallery.GalleriesScriptsVersion{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "harden-latest"},
		Spec: gallery.GalleriesScriptsVersion_Spec{
			AzureName: "latest", Owner: &genruntime.KnownResourceReference{Name: "harden"}, Location: ptr("westus"),
			Properties: version.Spec.Properties,
		},
	}
	if err := k8s.Create(ctx, latest); err == nil || !contains(err.Error(), "azureName") {
		t.Fatalf("a version named latest: %v", err)
	}
	orphan := &compute2025.RestorePointCollection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "orphan-points"},
		Spec: compute2025.RestorePointCollection_Spec{
			Owner: inRG, Location: ptr("westus"),
			Source: &compute2025.RestorePointCollectionSourceProperties{
				Reference: &genruntime.ResourceReference{ARMID: rgID + "/providers/Microsoft.Compute/virtualMachines/gone"},
			},
		},
	}
	if err := k8s.Create(ctx, orphan); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "restore points of a missing VM refused", notReadyWith(orphan, "was not found"))

	for _, o := range []client.Object{orphan, version, script, gal, point, collection} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, o.GetName()+" gone", gone(o))
	}
	if armFake.HasResource(collectionID) || armFake.HasResource(versionID) || !armFake.HasResource(vmID) {
		t.Fatalf("collection %v version %v vm %v", armFake.HasResource(collectionID), armFake.HasResource(versionID), armFake.HasResource(vmID))
	}
}
