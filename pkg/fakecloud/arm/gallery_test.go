// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// Gallery names, version names and sources; host group fault domains.
func TestGalleriesAndHostGroups(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/images"
	a.AddResource(rg)
	put := func(what, path, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
	}
	compute := rg + "/providers/Microsoft.Compute/"
	put("a gallery with a hyphen", compute+"galleries/golden-images", `{"location":"westus"}`, http.StatusBadRequest)
	put("a gallery", compute+"galleries/golden_images", `{"location":"westus"}`, http.StatusCreated)
	image := compute + "galleries/golden_images/images/ubuntu"
	put("an image definition", image, `{"location":"westus","properties":{"osType":"Linux","osState":"Generalized","identifier":{"publisher":"p","offer":"o","sku":"s"}}}`, http.StatusCreated)
	managed := compute + "images/base"
	put("a version named by a word", image+"/versions/latest", `{"location":"westus","properties":{"storageProfile":{"source":{"id":"`+managed+`"}}}}`, http.StatusBadRequest)
	put("a version with no source", image+"/versions/1.0.0", `{"location":"westus","properties":{"storageProfile":{}}}`, http.StatusBadRequest)
	put("a version of a missing image", image+"/versions/1.0.0", `{"location":"westus","properties":{"storageProfile":{"source":{"id":"`+managed+`"}}}}`, http.StatusNotFound)
	a.AddResource(managed)
	put("a version", image+"/versions/1.0.0", `{"location":"westus","properties":{"storageProfile":{"source":{"id":"`+managed+`"}}}}`, http.StatusCreated)
	app := compute + "galleries/golden_images/applications/agent"
	put("an application", app, `{"location":"westus","properties":{"supportedOSType":"Linux"}}`, http.StatusCreated)
	put("an application version with no package", app+"/versions/1.0.0", `{"location":"westus","properties":{"publishingProfile":{"source":{}}}}`, http.StatusBadRequest)
	put("an application version", app+"/versions/1.0.0", `{"location":"westus","properties":{"publishingProfile":{"source":{"mediaLink":"https://example.blob.core.windows.net/pkgs/agent.zip"},"manageActions":{"install":"i","remove":"r"}}}}`, http.StatusCreated)
	put("a host group of four fault domains", compute+"hostGroups/hg", `{"location":"westus","properties":{"platformFaultDomainCount":4}}`, http.StatusBadRequest)
	put("a host group", compute+"hostGroups/hg", `{"location":"westus","properties":{"platformFaultDomainCount":2}}`, http.StatusCreated)
}
