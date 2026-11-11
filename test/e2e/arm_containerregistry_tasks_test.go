// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	acrtasks "github.com/highfivelabs/aso-extensions/apis/arm/containerregistry/v20190401"
	acr "github.com/highfivelabs/aso-extensions/apis/arm/containerregistry/v20251101"
)

// A task of ASO's Registry (a Docker build, a commit trigger), its tokens
// and a custom registry's password from Secrets, reaching Resource Manager
// and neither the status nor a GET, its timeout changed; a connected
// registry that syncs with our RegistriesToken by object; a private
// endpoint connection approved; all deleted.
func TestRegistryTasksAndConnectedRegistry(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "acr-tasks")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	regID := registryFootprint(t, ns)
	secret(t, ns, "github", map[string]string{"pat": "gh-pat-1"})
	secret(t, ns, "dockerhub", map[string]string{"user": "contoso-bot", "password": "hub-pass-1"})

	task := &acrtasks.RegistriesTask{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "build-app"},
		Spec: acrtasks.RegistriesTask_Spec{
			Owner:    &genruntime.KnownResourceReference{Name: "reg"},
			Location: ptr("westus"),
			Platform: &acrtasks.PlatformProperties{Os: ptr(acrtasks.PlatformProperties_Os_Linux)},
			Status:   ptr(acrtasks.TaskProperties_Status_Enabled),
			Timeout:  ptr(600),
			Step: &acrtasks.TaskStepProperties{Docker: &acrtasks.DockerBuildStep{
				Type:               ptr(acrtasks.DockerBuildStep_Type_Docker),
				DockerFilePath:     ptr("Dockerfile"),
				ContextPath:        ptr("https://github.com/contoso/app.git"),
				ContextAccessToken: &genruntime.SecretReference{Name: "github", Key: "pat"},
				ImageNames:         []string{"app:{{.Run.ID}}"},
				IsPushEnabled:      ptr(true),
			}},
			Trigger: &acrtasks.TriggerProperties{SourceTriggers: []acrtasks.SourceTrigger{{
				Name:                ptr("main"),
				SourceTriggerEvents: []acrtasks.SourceTrigger_SourceTriggerEvents{acrtasks.SourceTrigger_SourceTriggerEvents_Commit},
				SourceRepository: &acrtasks.SourceProperties{
					SourceControlType: ptr(acrtasks.SourceProperties_SourceControlType_Github),
					RepositoryUrl:     ptr("https://github.com/contoso/app.git"),
					Branch:            ptr("main"),
					SourceControlAuthProperties: &acrtasks.AuthInfo{
						TokenType: ptr(acrtasks.AuthInfo_TokenType_PAT),
						Token:     &genruntime.SecretReference{Name: "github", Key: "pat"},
					},
				},
			}}},
			Credentials: &acrtasks.Credentials{CustomRegistries: map[string]acrtasks.CustomRegistryCredentials{
				"docker.io": {
					UserName: &acrtasks.SecretObject{Type: ptr(acrtasks.SecretObject_Type_Opaque), Value: &genruntime.SecretReference{Name: "dockerhub", Key: "user"}},
					Password: &acrtasks.SecretObject{Type: ptr(acrtasks.SecretObject_Type_Opaque), Value: &genruntime.SecretReference{Name: "dockerhub", Key: "password"}},
				},
			}},
		},
	}
	scopeMap := &acr.RegistriesScopeMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "edge-sync"},
		Spec: acr.RegistriesScopeMap_Spec{
			Owner:   &genruntime.KnownResourceReference{Name: "reg"},
			Actions: []string{"repositories/app/content/read", "repositories/app/metadata/read"},
		},
	}
	syncToken := &acr.RegistriesToken{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "edge-sync"},
		Spec: acr.RegistriesToken_Spec{
			Owner:             &genruntime.KnownResourceReference{Name: "reg"},
			ScopeMapReference: &genruntime.ResourceReference{ARMID: regID + "/scopeMaps/edge-sync"},
		},
	}
	for _, o := range []client.Object{task, scopeMap, syncToken} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []client.Object{task, scopeMap, syncToken} {
		eventually(t, timeout, fmt.Sprintf("%T Ready", o), ready(o.(conditioned)))
	}
	taskID := regID + "/tasks/build-app"
	expectResource(t, taskID, func(r map[string]any) error {
		p := properties(r)
		step, _ := p["step"].(map[string]any)
		if r["type"] != "Microsoft.ContainerRegistry/registries/tasks" || step["type"] != "Docker" || step["dockerFilePath"] != "Dockerfile" ||
			step["contextAccessToken"] != nil || !reflect.DeepEqual(step["imageNames"], []any{"app:{{.Run.ID}}"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	for path, want := range map[string]string{"/step/contextAccessToken": "gh-pat-1", "/trigger/main/token": "gh-pat-1", "/credentials/docker.io/password": "hub-pass-1", "/credentials/docker.io/userName": "contoso-bot"} {
		if k, ok := armFake.SharedKey(taskID + path); !ok || k != want {
			t.Fatalf("%s Resource Manager was given: %q %v", path, k, ok)
		}
	}
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(task), task); err != nil {
		t.Fatal(err)
	}
	if status, _ := json.Marshal(task.Status); strings.Contains(string(status), "gh-pat-1") || strings.Contains(string(status), "hub-pass-1") {
		t.Fatalf("a secret is in the status: %s", status)
	}
	task.Spec.Timeout = ptr(1200)
	if err := k8s.Update(ctx, task); err != nil {
		t.Fatal(err)
	}
	expectResource(t, taskID, func(r map[string]any) error {
		if properties(r)["timeout"] != float64(1200) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	edge := &acr.RegistriesConnectedRegistry{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "edge"},
		Spec: acr.RegistriesConnectedRegistry_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "reg"},
			Mode:  ptr(acr.ConnectedRegistryMode_ReadOnly),
			Parent: &acr.ParentProperties{SyncProperties: &acr.SyncProperties{
				TokenReference: &genruntime.ResourceReference{Group: "containerregistry.azure.hfl.dev", Kind: "RegistriesToken", Name: "edge-sync"},
				MessageTtl:     ptr("P2D"),
				Schedule:       ptr("0 9 * * *"),
				SyncWindow:     ptr("PT3H"),
			}},
			Logging: &acr.LoggingProperties{LogLevel: ptr(acr.LoggingProperties_LogLevel_Information)},
		},
	}
	connectionID := regID + "/privateEndpointConnections/from-hub"
	armFake.AddResource(connectionID)
	connection := &acr.RegistriesPrivateEndpointConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "from-hub"},
		Spec: acr.RegistriesPrivateEndpointConnection_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "reg"},
			PrivateLinkServiceConnectionState: &acr.PrivateLinkServiceConnectionState{
				Status: ptr(acr.ConnectionStatus_Approved), Description: ptr("the hub's endpoint"),
			},
		},
	}
	for _, o := range []client.Object{edge, connection} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []client.Object{edge, connection} {
		eventually(t, timeout, fmt.Sprintf("%T Ready", o), ready(o.(conditioned)))
	}
	edgeID := regID + "/connectedRegistries/edge"
	expectResource(t, edgeID, func(r map[string]any) error {
		p := properties(r)
		parent, _ := p["parent"].(map[string]any)
		sync, _ := parent["syncProperties"].(map[string]any)
		if r["type"] != "Microsoft.ContainerRegistry/registries/connectedRegistries" || p["mode"] != "ReadOnly" ||
			!strings.EqualFold(fmt.Sprint(sync["tokenId"]), regID+"/tokens/edge-sync") || sync["messageTtl"] != "P2D" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, connectionID, func(r map[string]any) error {
		state, _ := properties(r)["privateLinkServiceConnectionState"].(map[string]any)
		if state["status"] != "Approved" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range []client.Object{connection, edge, syncToken, scopeMap, task} {
		if err := k8s.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
		eventually(t, timeout, fmt.Sprintf("%T gone", o), gone(o))
	}
	for _, id := range []string{connectionID, edgeID, taskID} {
		if armFake.HasResource(id) {
			t.Fatalf("%s is still in Resource Manager", id)
		}
	}
	if _, ok := armFake.SharedKey(taskID + "/step/contextAccessToken"); ok {
		t.Fatal("the task's token outlived the task")
	}
}

// A connected registry the fake refuses: its sync token is not there.
func TestConnectedRegistryRefused(t *testing.T) {
	ctx := context.Background()
	ns := namespace(t, "acr-edge-no")
	strictCredential(t, ns, clientA)
	trustNamespace(ns)
	regID := registryFootprint(t, ns)
	edge := &acr.RegistriesConnectedRegistry{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "edge"},
		Spec: acr.RegistriesConnectedRegistry_Spec{
			Owner: &genruntime.KnownResourceReference{Name: "reg"},
			Mode:  ptr(acr.ConnectedRegistryMode_ReadOnly),
			Parent: &acr.ParentProperties{SyncProperties: &acr.SyncProperties{
				TokenReference: &genruntime.ResourceReference{ARMID: regID + "/tokens/none"},
				MessageTtl:     ptr("P2D"),
			}},
		},
	}
	if err := k8s.Create(ctx, edge); err != nil {
		t.Fatal(err)
	}
	eventually(t, timeout, "the refusal on the Ready condition", notReadyWith(edge, "tokens/none was not found"))
}
