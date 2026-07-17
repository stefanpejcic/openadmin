// This file implements the (nearly empty) Caddy settings page and its
// /metrics reverse-proxy passthrough.
package handlers

import (
	"fmt"
	"io"
	"net/http"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// Caddy bundles the /settings/caddy* handlers.
type Caddy struct {
	Sessions *auth.Manager
}

// CaddyMetricsURL is Caddy's local admin API metrics endpoint.
var CaddyMetricsURL = "http://localhost:2019/metrics"

// caddyFetchMetrics is injectable so tests never make a real HTTP call,
// matching the ftpPsRun/getDockerLogRun pattern used elsewhere.
var caddyFetchMetrics = func() (body string, status int, err error) {
	resp, err := http.Get(CaddyMetricsURL)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, err
	}
	return string(b), resp.StatusCode, nil
}

// ServeSettings handles GET/POST /settings/caddy. The POST branch is a
// no-op, so both methods just render the same near-empty page.
func (c *Caddy) ServeSettings(w http.ResponseWriter, r *http.Request) {
	webtemplates.Render(w, "settings_caddy.html", map[string]interface{}{
		"CSRFToken": csrf.Token(r),
		"Flashes":   auth.PopFlashes(w, r, c.Sessions),
	})
}

// ServeMetrics handles GET /settings/caddy/metrics: proxies Caddy's local
// admin API metrics endpoint, returning any fetch failure or non-2xx
// status as a 500 plaintext body (connection errors and bad status codes
// are treated the same way).
func (c *Caddy) ServeMetrics(w http.ResponseWriter, r *http.Request) {
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
