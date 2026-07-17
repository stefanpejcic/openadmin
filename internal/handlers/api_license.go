// This file implements the JSON REST API's license routes: reading,
// setting, or deleting the license key on a single /api/license resource.
// Each wraps the same opencli commands as the /license admin page
// (license_page.go), through the shared licenseRunOpenCLI hook so tests
// never shell out for real. /api/license/info and /api/license/verify
// need no new logic at all -- their JSON shape already matches
// license_page.go's ServeLicenseInfo/ServeLicenseVerify exactly, so
// main.go wires those two straight to the existing LicensePage instance.
package handlers

import (
	"net/http"
	"strings"
)

// APILicense bundles the /api/license handler.
type APILicense struct{}

// ServeLicense handles GET/POST/DELETE /api/license: GET reads the current
// key, POST sets a new one (validating it against the licensing server
// along the way), DELETE removes it.
func (a *APILicense) ServeLicense(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		stdout, _, err := licenseRunOpenCLI("opencli", "license", "key", "--json")
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to read license key")
			return
		}
		writeJSON(w, map[string]string{"key": strings.TrimSpace(stdout)})

	case http.MethodDelete:
		stdout, _, err := licenseRunOpenCLI("opencli", "license", "delete", "--json")
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to delete license")
			return
		}
		writeJSON(w, map[string]string{"delete": strings.TrimSpace(stdout)})

	default: // POST
		var body map[string]string
		if !apiDecodeJSONBody(r, &body) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}
		key := body["key"]
		if key == "" {
			writeJSONError(w, http.StatusBadRequest, "Missing key in request")
			return
		}
		stdout, _, err := licenseRunOpenCLI("opencli", "license", key, "--json", "--no-restart")
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "License key validation failed")
			return
		}
		writeJSON(w, map[string]string{"response": strings.TrimSpace(stdout)})
	}
}
