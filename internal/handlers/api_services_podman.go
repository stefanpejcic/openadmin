// This file implements the JSON REST API's Podman endpoints: the JSON
// counterpart to podman.go's HTML /server/podman page (info/images/
// volumes/networks/disk-usage, per-image pull/delete/check-update, the
// bulk actions, and the Trivy-based vulnerability scan/details). Reuses
// the exact same podman.go plumbing -- only the request/response shape
// differs.
package handlers

import (
	"database/sql"
	"net/http"
)

// APIServicesPodman bundles the /api/services/podman/* handlers.
type APIServicesPodman struct {
	MySQL *sql.DB
}

// ServeInfo handles GET /api/services/podman.
func (a *APIServicesPodman) ServeInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]interface{}{
		"info":       podmanInfoText(),
		"images":     podmanListImages(podmanImageUsageCounts(a.MySQL), podmanStackImages()),
		"volumes":    podmanListVolumes(),
		"networks":   podmanListNetworks(),
		"disk_usage": podmanSystemDiskUsage(),
	})
}

// ServeImages handles GET /api/services/podman/images.
func (a *APIServicesPodman) ServeImages(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, podmanListImages(podmanImageUsageCounts(a.MySQL), podmanStackImages()))
}

// ServeVolumes handles GET /api/services/podman/volumes.
func (a *APIServicesPodman) ServeVolumes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, podmanListVolumes())
}

// ServeNetworks handles GET /api/services/podman/networks.
func (a *APIServicesPodman) ServeNetworks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, podmanListNetworks())
}

// ServeDiskUsage handles GET /api/services/podman/disk-usage.
func (a *APIServicesPodman) ServeDiskUsage(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, podmanSystemDiskUsage())
}

// ServeImageAction handles POST
// /api/services/podman/images/{action}/{id...}: "delete" (id is the
// image's full ID) or "pull" (id is instead the repository:tag ref).
// Fires the same async goroutine + pendingPodmanImageActions map
// podman.go's HTML page uses, so a caller polls ServeImageActionStatus
// the same way the page itself does.
func (a *APIServicesPodman) ServeImageAction(w http.ResponseWriter, r *http.Request) {
	p := &Podman{MySQL: a.MySQL}
	p.ServePodmanImageAction(w, r)
}

// ServeImageActionStatus handles GET
// /api/services/podman/images/action-status.
func (a *APIServicesPodman) ServeImageActionStatus(w http.ResponseWriter, r *http.Request) {
	p := &Podman{}
	p.ServePodmanImageActionStatus(w, r)
}

// ServeImageCheckUpdate handles GET
// /api/services/podman/images/check-update?ref=....
func (a *APIServicesPodman) ServeImageCheckUpdate(w http.ResponseWriter, r *http.Request) {
	p := &Podman{}
	p.ServePodmanImageCheckUpdate(w, r)
}

// ServeImagesBulkAction handles POST
// /api/services/podman/images/bulk/{action}: "pull-missing",
// "delete-unused", "check-updates", or "check-vulnerabilities". All run in
// the background; poll ServeImagesBulkStatus for progress.
func (a *APIServicesPodman) ServeImagesBulkAction(w http.ResponseWriter, r *http.Request) {
	p := &Podman{MySQL: a.MySQL}
	p.ServePodmanImagesBulkAction(w, r)
}

// ServeImagesBulkStatus handles GET /api/services/podman/images/bulk-status.
func (a *APIServicesPodman) ServeImagesBulkStatus(w http.ResponseWriter, r *http.Request) {
	p := &Podman{}
	p.ServePodmanImagesBulkStatus(w, r)
}

// ServeImageVulnerabilities handles GET
// /api/services/podman/images/vulnerabilities?ref=...: the cached Trivy
// findings for one image ref, populated by the last "check-vulnerabilities"
// bulk run. Read-only -- never triggers a scan itself.
func (a *APIServicesPodman) ServeImageVulnerabilities(w http.ResponseWriter, r *http.Request) {
	p := &Podman{}
	p.ServePodmanImageVulnerabilities(w, r)
}
