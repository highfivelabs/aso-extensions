// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"testing"
)

// A Web PubSub service has a known SKU; a replica is a Premium service's,
// Premium, elsewhere; a hub's name is an identifier; a custom domain names a
// certificate of its service; a shared private link names a groupId and a
// resource that exists (and a replica's has no DELETE).
func TestWebPubSubAndSignalR(t *testing.T) {
	a := New(func(string) bool { return true })
	rg := "/subscriptions/" + sub1 + "/resourceGroups/rt"
	signalr := rg + "/providers/Microsoft.SignalRService/signalR/chat"
	fn := rg + "/providers/Microsoft.Web/sites/handler"
	for _, id := range []string{rg, signalr, fn} {
		a.AddResource(id)
	}
	put := func(what, id, body string, want int) {
		t.Helper()
		code, resp := do(t, a, http.MethodPut, id, body)
		expect(t, what, code, resp, want)
	}
	wps := rg + "/providers/Microsoft.SignalRService/webPubSub/events"
	put("an unknown SKU", wps, `{"location":"westus","sku":{"name":"Basic_B1"}}`, http.StatusBadRequest)
	put("a Free service", wps, `{"location":"westus","sku":{"name":"Free_F1","capacity":1}}`, http.StatusCreated)
	put("a replica of a Free service", wps+"/replicas/east", `{"location":"eastus","sku":{"name":"Premium_P1","capacity":1}}`, http.StatusBadRequest)
	put("the service made Premium", wps, `{"location":"westus","sku":{"name":"Premium_P1","capacity":1}}`, http.StatusOK)
	put("a replica in the service's region", wps+"/replicas/east", `{"location":"West US","sku":{"name":"Premium_P1","capacity":1}}`, http.StatusBadRequest)
	put("a replica", wps+"/replicas/east", `{"location":"eastus","sku":{"name":"Premium_P1","capacity":1}}`, http.StatusCreated)

	put("a hub named with a dash", wps+"/hubs/chat-room", `{"properties":{}}`, http.StatusBadRequest)
	put("a hub", wps+"/hubs/chat_room1", `{"properties":{"anonymousConnectPolicy":"deny"}}`, http.StatusCreated)
	put("a certificate with no secret", wps+"/customCertificates/tls", `{"properties":{"keyVaultBaseUri":"https://kv.vault.azure.net/"}}`, http.StatusBadRequest)
	put("a certificate", wps+"/customCertificates/tls", `{"properties":{"keyVaultBaseUri":"https://kv.vault.azure.net/","keyVaultSecretName":"tls"}}`, http.StatusCreated)
	put("a domain with another service's certificate", wps+"/customDomains/www", `{"properties":{"domainName":"ws.example.com","customCertificate":{"id":"`+signalr+`/customCertificates/tls"}}}`, http.StatusNotFound)
	put("a domain", wps+"/customDomains/www", `{"properties":{"domainName":"ws.example.com","customCertificate":{"id":"`+wps+`/customCertificates/tls"}}}`, http.StatusCreated)

	put("a shared link with no groupId", signalr+"/sharedPrivateLinkResources/fn", `{"properties":{"privateLinkResourceId":"`+fn+`"}}`, http.StatusBadRequest)
	put("a shared link to a missing resource", signalr+"/sharedPrivateLinkResources/fn", `{"properties":{"groupId":"sites","privateLinkResourceId":"`+fn+`x"}}`, http.StatusNotFound)
	put("a shared link", signalr+"/sharedPrivateLinkResources/fn", `{"properties":{"groupId":"sites","privateLinkResourceId":"`+fn+`"}}`, http.StatusCreated)
	replicaLink := wps + "/replicas/east/sharedPrivateLinkResources/fn"
	put("a replica's shared link", replicaLink, `{"properties":{"groupId":"sites","privateLinkResourceId":"`+fn+`"}}`, http.StatusCreated)
	code, resp := do(t, a, http.MethodDelete, replicaLink, "")
	expect(t, "deleting a replica's shared link", code, resp, http.StatusMethodNotAllowed)
	put("a connection no private endpoint made", wps+"/privateEndpointConnections/pe", `{"properties":{"privateLinkServiceConnectionState":{"status":"Approved"}}}`, http.StatusNotFound)
}
