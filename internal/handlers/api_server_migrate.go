// This file implements the JSON REST API's server-migration route: kicking
// off a background `opencli server-migrate` run and polling its progress,
// both under the same GET/POST route (unlike the HTML pages, which split
// these into /server/migrate and /server/migrate/status). Reuses the same
// log/pid-file paths and background-process primitives as those HTML pages
// (migrate.go).
package handlers

import (
	"net/http"
	"os"
	"strconv"
	"strings"
)

// APIServerMigrate bundles the /api/server/migrate handler.
type APIServerMigrate struct{}

// ServeMigrate handles GET/POST /api/server/migrate.
func (a *APIServerMigrate) ServeMigrate(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			Host     string `json:"host"`
			Root     string `json:"root"`
			Password string `json:"password"`
		}
		if !apiDecodeJSONBody(r, &body) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}
		if body.Host == "" || body.Root == "" || body.Password == "" {
			writeJSONError(w, http.StatusBadRequest, "host, root and password are all required.")
			return
		}
		if err := migrateStartRun(body.Host, body.Root, body.Password); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Internal Server Error")
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "message": "Migration process started."})
		return
	}

	status := "running"
	output := ""
	if raw, err := os.ReadFile(MigrateLogPath); err == nil {
		output = string(raw)
	}

	if raw, err := os.ReadFile(MigrateProcessPIDFile); err == nil {
		pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
		if convErr != nil {
			// A corrupt pid file produces a generic 500 rather than being
			// silently treated as some other status.
			writeJSONError(w, http.StatusInternalServerError, "Internal Server Error")
			return
		}
		if !processAlive(pid) {
			status = "finished"
		}
	} else {
		status = "unknown"
	}

	writeJSON(w, map[string]string{"status": status, "output": output})
}
