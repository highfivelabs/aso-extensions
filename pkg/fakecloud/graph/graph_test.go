// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package graph

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	absauth "github.com/microsoft/kiota-abstractions-go/authentication"
	khttp "github.com/microsoft/kiota-http-go"
	msgraphsdk "github.com/microsoftgraph/msgraph-beta-sdk-go"
	"github.com/microsoftgraph/msgraph-beta-sdk-go/models"
	"github.com/microsoftgraph/msgraph-beta-sdk-go/models/odataerrors"
	"github.com/microsoftgraph/msgraph-beta-sdk-go/rolemanagement"
	msgraphcore "github.com/microsoftgraph/msgraph-sdk-go-core"
)

type staticToken string

func (t staticToken) GetAuthorizationToken(context.Context, *url.URL, map[string]any) (string, error) {
	return string(t), nil
}

func (t staticToken) GetAllowedHostsValidator() *absauth.AllowedHostsValidator {
	v, _ := absauth.NewAllowedHostsValidatorErrorCheck(nil)
	return v
}

// client is the Graph SDK, as the reconciler uses it (with the SDK's
// default middleware, which gzips request bodies), against the fake.
func client(t *testing.T, g *Graph, token string) *msgraphsdk.GraphServiceClient {
	t.Helper()
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	auth := absauth.NewBaseBearerTokenAuthenticationProvider(staticToken(token))
	httpClient := &http.Client{Transport: khttp.NewCustomTransportWithParentTransport(srv.Client().Transport,
		msgraphcore.GetDefaultMiddlewaresWithOptions(&msgraphcore.GraphClientOptions{})...)}
	adapter, err := msgraphsdk.NewGraphRequestAdapterWithParseNodeFactoryAndSerializationWriterFactoryAndHttpClient(auth, nil, nil, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	adapter.SetBaseUrl(srv.URL + "/beta")
	return msgraphsdk.NewGraphServiceClient(adapter)
}

func status(err error) int {
	var odata *odataerrors.ODataError
	if errors.As(err, &odata) {
		return odata.ResponseStatusCode
	}
	return 0
}

func newAssignment(principal, role, scope string) models.UnifiedRoleAssignmentable {
	a := models.NewUnifiedRoleAssignment()
	a.SetPrincipalId(&principal)
	a.SetRoleDefinitionId(&role)
	a.SetDirectoryScopeId(&scope)
	return a
}

func TestRoleAssignments(t *testing.T) {
	ctx := context.Background()
	g := New(func(token string) bool { return token == "good" })
	const principal = "6a2f0e1b-0000-4000-8000-000000000001"
	g.AddPrincipal(principal)
	g.AddAdministrativeUnit("au-1")
	c := client(t, g, "good")
	assignments := c.RoleManagement().Directory().RoleAssignments()

	created, err := assignments.Post(ctx, newAssignment(principal, GlobalReader, "/"), nil)
	if err != nil {
		t.Fatal(err)
	}
	id := *created.GetId()
	if !g.HasAssignment(id) || *created.GetDirectoryScopeId() != "/" {
		t.Fatalf("created %v", g.Assignments())
	}
	got, err := assignments.ByUnifiedRoleAssignmentId(id).Get(ctx, nil)
	if err != nil || *got.GetPrincipalId() != principal || *got.GetRoleDefinitionId() != GlobalReader {
		t.Fatalf("get: %v %v", got, err)
	}

	filter := "principalId eq '" + principal + "' and roleDefinitionId eq '" + GlobalReader + "'"
	page, err := assignments.Get(ctx, &rolemanagement.DirectoryRoleAssignmentsRequestBuilderGetRequestConfiguration{
		QueryParameters: &rolemanagement.DirectoryRoleAssignmentsRequestBuilderGetQueryParameters{Filter: &filter},
	})
	if err != nil || len(page.GetValue()) != 1 || *page.GetValue()[0].GetId() != id {
		t.Fatalf("list: %v %v", page, err)
	}

	for _, tc := range []struct {
		what string
		body models.UnifiedRoleAssignmentable
		want int
	}{
		{"the same assignment again", newAssignment(principal, GlobalReader, "/"), http.StatusConflict},
		{"an unknown principal", newAssignment("6a2f0e1b-0000-4000-8000-0000000000ff", GlobalReader, "/"), http.StatusNotFound},
		{"an unknown role", newAssignment(principal, "00000000-0000-0000-0000-00000000dead", "/"), http.StatusNotFound},
		{"an unknown administrative unit", newAssignment(principal, GlobalReader, "/administrativeUnits/au-2"), http.StatusNotFound},
		{"a scope that is no scope", newAssignment(principal, GlobalReader, "/subscriptions/x"), http.StatusBadRequest},
	} {
		if _, err := assignments.Post(ctx, tc.body, nil); status(err) != tc.want {
			t.Errorf("%s: %v (status %d), want %d", tc.what, err, status(err), tc.want)
		}
	}
	if _, err := assignments.Post(ctx, newAssignment(principal, GlobalReader, "/administrativeUnits/au-1"), nil); err != nil {
		t.Fatalf("at an administrative unit: %v", err)
	}

	if _, err := client(t, g, "bad").RoleManagement().Directory().RoleAssignments().ByUnifiedRoleAssignmentId(id).Get(ctx, nil); status(err) != http.StatusUnauthorized {
		t.Fatalf("a token the fake does not accept: %v", err)
	}

	if err := assignments.ByUnifiedRoleAssignmentId(id).Delete(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := assignments.ByUnifiedRoleAssignmentId(id).Get(ctx, nil); status(err) != http.StatusNotFound {
		t.Fatalf("get after delete: %v", err)
	}
	if err := assignments.ByUnifiedRoleAssignmentId(id).Delete(ctx, nil); status(err) != http.StatusNotFound {
		t.Fatalf("delete again: %v", err)
	}
}
