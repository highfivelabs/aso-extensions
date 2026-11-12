// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// Resource Manager returns systemData with a resource: createdAt is set when
// the resource is made and kept by every later PUT; a PUT after a DELETE
// makes a new resource with a new createdAt. A slot move (docs/plan.md,
// "Moving resources between slots") checks createdAt to show a resource was
// adopted, not made again.
func TestSystemDataCreatedAt(t *testing.T) {
	a := New(func(string) bool { return true })
	now := time.Date(2026, 11, 12, 1, 0, 0, 0, time.UTC)
	a.Now = func() time.Time { return now }
	rg := "/subscriptions/" + sub1 + "/resourceGroups/rg"
	a.AddResource(rg)
	id := rg + "/providers/Microsoft.Network/ipGroups/ips"

	createdAt := func(body string) (string, string) {
		t.Helper()
		var r struct {
			SystemData struct {
				CreatedAt      string `json:"createdAt"`
				LastModifiedAt string `json:"lastModifiedAt"`
			} `json:"systemData"`
		}
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatal(err)
		}
		return r.SystemData.CreatedAt, r.SystemData.LastModifiedAt
	}

	code, body := do(t, a, http.MethodPut, id, `{"location":"westus","properties":{}}`)
	expect(t, "create", code, body, http.StatusCreated)
	first, _ := createdAt(body)
	if first != "2026-11-12T01:00:00Z" {
		t.Fatalf("createdAt %q after the create", first)
	}

	now = now.Add(time.Hour)
	code, body = do(t, a, http.MethodPut, id, `{"location":"westus","tags":{"a":"b"},"properties":{}}`)
	expect(t, "update", code, body, http.StatusOK)
	if c, m := createdAt(body); c != first || m != "2026-11-12T02:00:00Z" {
		t.Fatalf("after an update: createdAt %q lastModifiedAt %q, want %q and 02:00", c, m, first)
	}
	code, body = do(t, a, http.MethodGet, id, "")
	expect(t, "get", code, body, http.StatusOK)
	if c, _ := createdAt(body); c != first {
		t.Fatalf("GET createdAt %q, want %q", c, first)
	}

	code, body = do(t, a, http.MethodDelete, id, "")
	expect(t, "delete", code, body, http.StatusOK)
	now = now.Add(time.Hour)
	code, body = do(t, a, http.MethodPut, id, `{"location":"westus","properties":{}}`)
	expect(t, "create again", code, body, http.StatusCreated)
	if c, _ := createdAt(body); c != "2026-11-12T03:00:00Z" {
		t.Fatalf("a resource made again has createdAt %q, want 03:00", c)
	}
}
