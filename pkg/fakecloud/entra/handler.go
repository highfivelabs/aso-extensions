// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package entra

import (
	"io"
	"net/http"
)

// Handler serves t's endpoints over HTTP(S), so a process that cannot take
// the Transport in process (stock Azure Service Operator, any azidentity
// client) reaches the fake Entra as it reaches Entra: point its authority
// host (ASO's AZURE_AUTHORITY_HOST) at the server and trust the server's CA
// (SSL_CERT_FILE). The answers are the Transport's: instance discovery,
// OpenID metadata and the token endpoint, with the host the client asked
// for in every URL they hold.
func Handler(t *Transport) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := r.Clone(r.Context())
		req.URL.Scheme, req.URL.Host = "https", r.Host
		resp, err := t.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
}
