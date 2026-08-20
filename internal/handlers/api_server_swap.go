// This file implements the JSON REST API's Swap endpoint: the JSON
// counterpart to swap.go's HTML /server/swap page. Reuses the exact same
// plumbing -- only the request/response shape differs.
package handlers

import (
	"net/http"
	"net/url"
	"strconv"
)

// APIServerSwap bundles the /api/server/swap* handlers.
type APIServerSwap struct{}

// ServeSwap handles GET /api/server/swap.
func (a *APIServerSwap) ServeSwap(w http.ResponseWriter, r *http.Request) {
	status := getSwapStatus()
	writeJSON(w, map[string]interface{}{
		"total_mb":            status.TotalMB,
		"used_mb":             status.UsedMB,
		"free_mb":             status.FreeMB,
		"used_percent":        status.UsedPercent,
		"devices":             status.Devices,
		"threshold_percent":   status.ThresholdPercent,
		"managed_file_exists": status.ManagedFileExists,
		"managed_file_path":   SwapFilePath,
	})
}

// ServeSwapAction handles POST /api/server/swap/action/{action}: "resize"
// (JSON body {"size_mb": <int>}) or "drop". Fires the same async
// goroutine podman.go's own async actions use; poll ServeSwapActionStatus
// for progress.
func (a *APIServerSwap) ServeSwapAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if action == "resize" {
		var body struct {
			SizeMB int64 `json:"size_mb"`
		}
		if !apiDecodeJSONBody(r, &body) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}
		// swap.go's ServeSwapAction reads size_mb from a POST form value
		// (via r.ParseForm()), not a JSON body -- translate here so both
		// the HTML page and this JSON endpoint can share the exact same
		// handler/goroutine logic. Pre-populating BOTH r.Form and
		// r.PostForm makes r.ParseForm() a no-op (it only (re)parses
		// either when nil), so this value survives that call intact.
		values := url.Values{"size_mb": {strconv.FormatInt(body.SizeMB, 10)}}
		r.Form = values
		r.PostForm = values
	}

	s := &Swap{}
	s.ServeSwapAction(w, r)
}

// ServeSwapActionStatus handles GET /api/server/swap/action-status.
func (a *APIServerSwap) ServeSwapActionStatus(w http.ResponseWriter, r *http.Request) {
	s := &Swap{}
	s.ServeSwapActionStatus(w, r)
}
