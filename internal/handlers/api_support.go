// This file implements the JSON REST API's diagnostics-bundle route.
package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
)

// APISupport bundles the /api/support/report handler.
type APISupport struct{}

// ServeSupportReport handles GET /api/support/report: runs the same
// `opencli report --public --non-interactive` command the /support/report
// admin action uses (see license_page.go's ServeSupportReport), through
// the shared licenseRunOpenCLI hook. Unlike that admin action, a failure
// here is reported as a JSON body instead of a flash message plus
// redirect, since there's no browser session to redirect.
func (a *APISupport) ServeSupportReport(w http.ResponseWriter, r *http.Request) {
	stdout, _, err := licenseRunOpenCLI("opencli", "report", "--public", "--non-interactive")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Generating report failed. Please try running from the terminal: 'opencli report --public --non-interactive'.",
		})
		return
	}
	writeJSON(w, map[string]interface{}{"success": true, "message": strings.TrimSpace(stdout)})
}
