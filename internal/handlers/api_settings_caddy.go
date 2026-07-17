// This file implements the JSON REST API's /api/settings/caddy/metrics
// route: a plaintext passthrough of Caddy's local admin API metrics
// endpoint. Reuses the same injectable fetch as the HTML /settings/caddy/
// metrics route in caddy.go.
package handlers

import (
	"fmt"
	"io"
	"net/http"
)

// APISettingsCaddy bundles the /api/settings/caddy/metrics handler.
type APISettingsCaddy struct{}

// ServeMetrics handles GET /api/settings/caddy/metrics: any fetch failure or
// non-2xx status is reported as a 500 plaintext body (connection errors and
// bad status codes are treated the same way).
func (a *APISettingsCaddy) ServeMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")

	body, status, err := caddyFetchMetrics()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Error fetching metrics: %s", err)
		return
	}
	if status >= 400 {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Error fetching metrics: %d %s", status, http.StatusText(status))
		return
	}
	io.WriteString(w, body)
}
