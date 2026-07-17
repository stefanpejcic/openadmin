// This file implements the JSON REST API's /api/settings/custom-code route:
// viewing or updating the custom CSS/JS/header/footer snippets, startup
// scripts, and other custom-code files. Reuses the same file-path map as the
// HTML /settings/custom-code page in custom_code.go.
//
// SECURITY NOTE: unlike the HTML page, which gates the Enterprise-only
// fields (custom_css, custom_js, in_header, in_footer, custom_section,
// howto_guides) behind an active license and non-reseller role, this API
// endpoint applies no such gating -- any admin-role caller can write any
// field. This is kept as-is rather than silently tightened, since it's a
// genuine existing behavior real callers may already depend on.
package handlers

import (
	"net/http"
	"os"
)

// APISettingsCustomCode bundles the /api/settings/custom-code handler.
type APISettingsCustomCode struct{}

// Serve handles GET/POST /api/settings/custom-code.
func (a *APISettingsCustomCode) Serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		a.handlePost(w, r)
		return
	}
	a.handleGet(w, r)
}

func (a *APISettingsCustomCode) handlePost(w http.ResponseWriter, r *http.Request) {
	var data map[string]interface{}
	if !apiDecodeJSONBody(r, &data) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	for _, key := range customCodeFieldOrder {
		value, present := data[key]
		if !present || value == nil {
			continue
		}
		strVal, isString := value.(string)
		if !isString {
			continue
		}
		if err := os.WriteFile(customCodeFilePaths[key], []byte(strVal), 0644); err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}

	if err := os.WriteFile(CustomCodeRestartFlagPath, []byte("Restart needed"), 0644); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]interface{}{"success": true, "message": "Files updated successfully!"})
}

func (a *APISettingsCustomCode) handleGet(w http.ResponseWriter, r *http.Request) {
	fileContents := make(map[string]string, len(customCodeFieldOrder))
	for _, key := range customCodeFieldOrder {
		content, err := readFileOrEmpty(customCodeFilePaths[key])
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		fileContents[key] = content
	}
	writeJSON(w, fileContents)
}
