// This file implements the JSON REST API's /api/settings/locales route:
// listing installed/available translation locales, installing, updating or
// deleting them, or setting the default. Reuses the same row builder and
// action runner as the HTML /settings/locales page in locales.go -- only
// the response shape and the strict JSON-content-type requirement on POST
// differ.
package handlers

import (
	"net/http"
)

// APISettingsLocales bundles the /api/settings/locales handler.
type APISettingsLocales struct{}

// Serve handles GET/POST /api/settings/locales.
func (a *APISettingsLocales) Serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		a.handlePost(w, r)
		return
	}
	a.handleGet(w, r)
}

func (a *APISettingsLocales) handlePost(w http.ResponseWriter, r *http.Request) {
	var data map[string]interface{}
	if !apiDecodeJSONBody(r, &data) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	field := func(k string) string { v, _ := data[k].(string); return v }

	res := runLocaleAction(field("locale"), field("update"), field("delete"), field("default"))
	if res.Batch {
		// partial failures still return 200 so callers get the installed/failed lists
		writeJSON(w, map[string]interface{}{
			"success":   res.OK,
			"message":   res.Message,
			"installed": res.Done,
			"failed":    res.Failed,
		})
		return
	}
	if !res.OK {
		writeJSONError(w, res.Status, res.Message)
		return
	}
	writeJSON(w, map[string]interface{}{"success": true, "message": res.Message})
}

func (a *APISettingsLocales) handleGet(w http.ResponseWriter, r *http.Request) {
	results, defaultLocaleBase, err := loadLocaleRows()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]interface{}{
		"default_locale": defaultLocaleBase,
		"translations":   results,
	})
}
