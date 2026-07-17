// This file implements the per-domain GoAccess stats report viewer at
// GET /domains/stats/<current_username>/<domain_name>. The report is a
// pre-generated GoAccess HTML file meant to be served as a raw passthrough,
// not a page wrapped in the usual chrome layer, so this handler writes the
// file's bytes straight to the response instead of going through
// webtemplates.Render/the chrome skeleton.
//
// Not implemented: response caching. This is purely a backend performance
// concern (same bytes served either way, just re-read from disk each
// request here instead of served from a short-lived in-memory/redis cache)
// with no observable UI/UX difference, so it's out of scope.
package handlers

import (
	"net/http"
	"os"
	"path/filepath"

	"openadmin/internal/auth"
)

// GoAccessStatsDir is the directory holding per-domain GoAccess stats
// reports (/var/log/caddy/stats).
var GoAccessStatsDir = "/var/log/caddy/stats"

// GoAccessStats bundles the /domains/stats handler.
type GoAccessStats struct {
	Sessions *auth.Manager
}

// ServeStats handles GET /domains/stats/{current_username}/{domain_name}.
func (h *GoAccessStats) ServeStats(w http.ResponseWriter, r *http.Request) {
	currentUsername := r.PathValue("current_username")
	domainName := r.PathValue("domain_name")

	if !isDomain(domainName) {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	statsFilePath := filepath.Join(GoAccessStatsDir, currentUsername, domainName+".html")

	content, err := os.ReadFile(statsFilePath)
	if err != nil {
		auth.AddFlash(w, r, h.Sessions, "Stats file for domain "+domainName+" not found. Data is generated every 24h.", "error")
		http.Redirect(w, r, "/domains", http.StatusSeeOther)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(content)
}
