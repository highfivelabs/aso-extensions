// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package web

import "testing"

// isApp takes a site's or a slot's ARM ID and nothing that could change the
// request URL it is put into.
func TestIsApp(t *testing.T) {
	sub := "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-1.(a)/providers/Microsoft.Web/sites/app-1"
	for id, want := range map[string]bool{
		sub:                    true,
		sub + "/slots/staging": true,
		sub + "/":              false,
		sub + "/config/x":      false,
		sub + "?api-version=1": false,
		sub + "#x":             false,
		sub + "%2Fslots%2Fx":   false,
		sub + "/slots/..":      false,
		"/subscriptions/s/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/st": false,
	} {
		if got := isApp(id); got != want {
			t.Errorf("isApp(%q) = %v, want %v", id, got, want)
		}
	}
}
