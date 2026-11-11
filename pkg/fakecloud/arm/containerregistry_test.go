// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package arm

import (
	"net/http"
	"strings"
	"testing"
)

// What a task and a connected registry must give; a task's tokens and
// credentials kept aside.
func TestContainerRegistry(t *testing.T) {
	a := New(func(string) bool { return true })
	put := func(what, path, body string, want int) string {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, path, body)
		expect(t, what, code, resp, want)
		return resp
	}
	rg := "/subscriptions/" + sub1 + "/resourceGroups/acr"
	put("resource group", rg, `{"location":"westus"}`, http.StatusCreated)
	reg := rg + "/providers/Microsoft.ContainerRegistry/registries/reg1"
	a.AddResource(reg)

	task := reg + "/tasks/build"
	put("task without a platform", task, `{"location":"westus","properties":{"step":{"type":"Docker","dockerFilePath":"Dockerfile"}}}`, http.StatusBadRequest)
	put("task without a step", task, `{"location":"westus","properties":{"platform":{"os":"Linux"}}}`, http.StatusBadRequest)
	put("Docker step without a Dockerfile", task, `{"location":"westus","properties":{"platform":{"os":"Linux"},"step":{"type":"Docker"}}}`, http.StatusBadRequest)
	put("task with a short timeout", task, `{"location":"westus","properties":{"platform":{"os":"Linux"},"timeout":60,"step":{"type":"Docker","dockerFilePath":"Dockerfile"}}}`, http.StatusBadRequest)
	body := `{"location":"westus","properties":{"platform":{"os":"Linux"},"timeout":600,` +
		`"step":{"type":"Docker","dockerFilePath":"Dockerfile","contextPath":"https://github.com/contoso/app","contextAccessToken":"ctx-pat"},` +
		`"trigger":{"sourceTriggers":[{"name":"main","sourceTriggerEvents":["commit"],"sourceRepository":{"sourceControlType":"Github","repositoryUrl":"https://github.com/contoso/app","branch":"main","sourceControlAuthProperties":{"tokenType":"PAT","token":"trigger-pat"}}}]},` +
		`"credentials":{"customRegistries":{"docker.io":{"userName":{"type":"Opaque","value":"bot"},"password":{"type":"Opaque","value":"hub-pass"}}}}}}`
	resp := put("task", task, body, http.StatusCreated)
	for _, s := range []string{"ctx-pat", "trigger-pat", "hub-pass"} {
		if strings.Contains(resp, s) {
			t.Fatalf("%s was returned: %s", s, resp)
		}
	}
	for path, want := range map[string]string{"/step/contextAccessToken": "ctx-pat", "/trigger/main/token": "trigger-pat", "/credentials/docker.io/password": "hub-pass"} {
		if k, ok := a.SharedKey(task + path); !ok || k != want {
			t.Fatalf("%s kept: %q %v", path, k, ok)
		}
	}

	cr := reg + "/connectedRegistries/edge"
	token := reg + "/tokens/edge-sync"
	conn := `{"properties":{"mode":"ReadOnly","parent":{"syncProperties":{"tokenId":"` + token + `","messageTtl":"P2D","schedule":"* * * * *"}}}}`
	put("connected registry with no mode", cr, `{"properties":{"parent":{"syncProperties":{"tokenId":"`+token+`"}}}}`, http.StatusBadRequest)
	put("connected registry without a sync token", cr, `{"properties":{"mode":"ReadOnly","parent":{"syncProperties":{}}}}`, http.StatusBadRequest)
	put("connected registry with a missing token", cr, conn, http.StatusNotFound)
	a.AddResource(token)
	put("connected registry", cr, conn, http.StatusCreated)
}
