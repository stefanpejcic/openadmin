// This file implements the /settings/api* HTML admin pages: the toggle
// page for turning the JSON REST API on/off, its request-log viewer, and
// the plain-text /settings/api/endpoints listing used by the "View
// Examples" button on that page.
package handlers

import (
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/config"
	"openadmin/internal/webtemplates"
)

// APISettings bundles the /settings/api* handlers.
type APISettings struct {
	Sessions *auth.Manager
}

// APILogPath is where API requests/responses get logged.
var APILogPath = "/var/log/openpanel/admin/api.log"

// AvailableEndpointsPath is the plain-text catalogue of API endpoints and
// curl examples shown by the "View Examples" button on /settings/api.
var AvailableEndpointsPath = "/usr/local/admin/modules/api/available_endpoints.txt"

// ServeAPIEndpointsList handles GET /settings/api/endpoints: the plain-text
// endpoint catalogue the page's "View Examples" button fetches, with the
// example curl commands' placeholder host rewritten to the scheme/host the
// request actually came in on.
func (a *APISettings) ServeAPIEndpointsList(w http.ResponseWriter, r *http.Request) {
	content, err := os.ReadFile(AvailableEndpointsPath)
	w.Header().Set("Content-Type", "text/plain")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to read the available endpoints file: " + err.Error()))
		return
	}

	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}

	rewritten := strings.ReplaceAll(string(content), "http://localhost:2087", scheme+"://"+host)
	w.Write([]byte(rewritten))
}

type apiSettingsPageData struct {
	webtemplates.Chrome
	CSRFToken        string
	Flashes          []auth.Flash
	BasicAuthEnabled string
	APIStatus        string
}

// ServeAPISettings handles GET/POST /settings/api and /settings/api/.
func (a *APISettings) ServeAPISettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		action := strings.ToLower(r.PostFormValue("action"))
		if action != "on" && action != "off" {
			writeJSONError(w, http.StatusBadRequest, "Invalid action.")
			return
		}

		out, err := exec.Command("opencli", "config", "update", "api", action).CombinedOutput()
		if err != nil {
			cmdRepr := "['opencli', 'config', 'update', 'api', '" + action + "']"
			auth.AddFlash(w, r, a.Sessions, "Error executing opencli: '"+cmdRepr+"': "+string(out), "error")
		} else if strings.Contains(string(out), "Updated api to") {
			notifySentinel("admin_api", "OpenAdmin API is $action", "API access for the administrator-level panel is now: $action.")

			if _, err := exec.Command("service", "admin", "reload").CombinedOutput(); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "API was updated, but reloading admin service encountered an error: "+err.Error())
				return
			}
			http.Redirect(w, r, "/settings/api", http.StatusSeeOther)
			return
		} else {
			auth.AddFlash(w, r, a.Sessions, "API could not be updated.", "warning")
		}
	}

	if r.URL.Query().Get("view") == "api_log" {
		content, err := os.ReadFile(APILogPath)
		w.Header().Set("Content-Type", "text/plain")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Unable to read the api log file: " + err.Error()))
			return
		}
		w.Write(content)
		return
	}

	cfg := config.Load(config.OpenpanelConfigPath)
	basicAuthEnabled := "off"
	if cfg.Get("PANEL", "basic_auth", "no") == "yes" {
		basicAuthEnabled = "on"
	}
	apiStatus := cfg.Get("PANEL", "api", "on")

	webtemplates.Render(w, "settings_api.html", apiSettingsPageData{
		Chrome:           buildChrome(r, "API"),
		CSRFToken:        csrf.Token(r),
		Flashes:          auth.PopFlashes(w, r, a.Sessions),
		BasicAuthEnabled: basicAuthEnabled,
		APIStatus:        apiStatus,
	})
}
