// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package recoveryservices

import (
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
)

func armError(status int, code string) error {
	return &azcore.ResponseError{StatusCode: status, ErrorCode: code}
}

// A bad request waits for a change of the spec; a conflict, an operation in
// progress, a refused right, a throttle or a server error is retried.
func TestClassify(t *testing.T) {
	for _, c := range []struct {
		err  error
		want conditions.ConditionSeverity
	}{
		{armError(http.StatusBadRequest, "BMSUserErrorInvalidInput"), conditions.ConditionSeverityError},
		{armError(http.StatusBadRequest, "AnotherOperationInProgress"), conditions.ConditionSeverityWarning},
		{armError(http.StatusConflict, "Conflict"), conditions.ConditionSeverityWarning},
		{armError(http.StatusForbidden, "AuthorizationFailed"), conditions.ConditionSeverityWarning},
		{armError(http.StatusNotFound, "ResourceNotFound"), conditions.ConditionSeverityWarning},
	} {
		got, ok := conditions.AsReadyConditionImpactingError(classify(c.err, "x"))
		if !ok || got.Severity != c.want {
			t.Errorf("%v: %+v, want severity %s", c.err, got, c.want)
		}
	}
	if _, ok := conditions.AsReadyConditionImpactingError(classify(armError(http.StatusServiceUnavailable, ""), "x")); ok {
		t.Error("a server error should be retried as a plain error")
	}
}

// A pending operation is forgotten only when Azure answered with its end.
func TestTerminal(t *testing.T) {
	if !terminal(armError(http.StatusBadRequest, "x")) || !terminal(armError(http.StatusConflict, "x")) {
		t.Error("a refusal ends the operation")
	}
	if terminal(armError(http.StatusTooManyRequests, "x")) || terminal(armError(http.StatusBadGateway, "x")) || terminal(errors.New("timeout")) {
		t.Error("a throttle, a server error or a transport error keeps the operation")
	}
}

// The names are the machine's; anything else than a machine is refused.
func TestDefaultNames(t *testing.T) {
	c, i, err := DefaultNames("/subscriptions/s/resourceGroups/RG1/providers/Microsoft.Compute/virtualMachines/app1")
	if err != nil || c != "iaasvmcontainer;iaasvmcontainerv2;RG1;app1" || i != "vm;iaasvmcontainerv2;RG1;app1" {
		t.Errorf("names %q %q %v", c, i, err)
	}
	if _, _, err := DefaultNames("/subscriptions/s/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/a"); err == nil {
		t.Error("a storage account was named")
	}
}
