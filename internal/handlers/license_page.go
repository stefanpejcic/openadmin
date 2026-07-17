// This file implements the /license admin page and its key/info/verify/
// delete opencli wrappers, plus /support/report.
package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// LicensePage bundles the /license*, /support/report handlers. Named
// LicensePage (not License) to avoid colliding with internal/license's
// Checker-based feature-gating type used elsewhere in this package.
type LicensePage struct {
	Sessions *auth.Manager
}

// licenseRunOpenCLI is injectable so tests never shell out to a real
// opencli binary, matching runOpenCLICaptured's usage elsewhere.
var licenseRunOpenCLI = func(args ...string) (stdout, stderr string, err error) {
	return runOpenCLICaptured(args...)
}

// ServeLicense handles GET /license.
func (l *LicensePage) ServeLicense(w http.ResponseWriter, r *http.Request) {
	stdout, _, err := licenseRunOpenCLI("opencli", "license", "key", "--json")
	var key interface{}
	if err == nil {
		key = strings.TrimSpace(stdout)
	}

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, map[string]interface{}{"key": key})
		return
	}

	webtemplates.Render(w, "settings_license.html", mergeChrome(map[string]interface{}{
		"Key":       key,
		"CSRFToken": csrf.Token(r),
		"Flashes":   auth.PopFlashes(w, r, l.Sessions),
	}, r, "License"))
}

// ServeLicenseKey handles GET/POST /license/key.
func (l *LicensePage) ServeLicenseKey(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		stdout, _, err := licenseRunOpenCLI("opencli", "license", "key", "--json")
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to read license key")
			return
		}
		writeJSON(w, map[string]string{"key": strings.TrimSpace(stdout)})

	case http.MethodPost:
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
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

// ServeLicenseInfo handles GET /license/info.
func (l *LicensePage) ServeLicenseInfo(w http.ResponseWriter, r *http.Request) {
	stdout, _, err := licenseRunOpenCLI("opencli", "license", "info", "--json")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to retrieve license info")
		return
	}
	writeJSON(w, map[string]string{"info": strings.TrimSpace(stdout)})
}

// ServeLicenseVerify handles POST /license/verify.
func (l *LicensePage) ServeLicenseVerify(w http.ResponseWriter, r *http.Request) {
	stdout, _, err := licenseRunOpenCLI("opencli", "license", "verify", "--json")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to verify license")
		return
	}
	writeJSON(w, map[string]string{"verify": strings.TrimSpace(stdout)})
}

// ServeLicenseDelete handles DELETE /license/delete.
func (l *LicensePage) ServeLicenseDelete(w http.ResponseWriter, r *http.Request) {
	stdout, _, err := licenseRunOpenCLI("opencli", "license", "delete", "--json")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to delete license")
		return
	}
	writeJSON(w, map[string]string{"delete": strings.TrimSpace(stdout)})
}

// ServeSupportReport handles GET /support/report.
func (l *LicensePage) ServeSupportReport(w http.ResponseWriter, r *http.Request) {
	stdout, _, err := licenseRunOpenCLI("opencli", "report", "--public", "--non-interactive")
	if err != nil {
		auth.AddFlash(w, r, l.Sessions, "Generating report failed. Please try running from the terminal: 'opencli report --public --non-interactive'.", "error")
		http.Redirect(w, r, "/license", http.StatusSeeOther)
		return
	}

	result := strings.TrimSpace(stdout)
	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, map[string]interface{}{"success": true, "message": result})
		return
	}

	auth.AddFlash(w, r, l.Sessions, result, "info")
	http.Redirect(w, r, "/license", http.StatusSeeOther)
}
