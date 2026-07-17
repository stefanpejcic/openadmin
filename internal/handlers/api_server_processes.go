// This file implements the JSON REST API's process-list and process-kill
// routes, reusing the same /proc-derived process snapshot and sort criteria
// as the HTML /server/processes page (process_manager.go).
package handlers

import (
	"fmt"
	"net/http"
	"strconv"
)

// APIServerProcesses bundles the /api/server/processes and
// /api/server/processes/{pid}/{action} handlers.
type APIServerProcesses struct{}

// ServeProcesses handles GET /api/server/processes.
func (a *APIServerProcesses) ServeProcesses(w http.ResponseWriter, r *http.Request) {
	sortBy := r.URL.Query().Get("sort")
	if sortBy == "" {
		sortBy = "cpu"
	}

	processes := listAllProcesses()
	sortProcesses(processes, sortBy)
	writeJSON(w, processes)
}

// ServeProcessAction handles POST /api/server/processes/{pid}/{action}.
// "kill" is the only permitted action -- there is no strace/streaming
// branch here, unlike the HTML page's version of this route.
func (a *APIServerProcesses) ServeProcessAction(w http.ResponseWriter, r *http.Request) {
	pid, err := strconv.Atoi(r.PathValue("pid"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Bad Request")
		return
	}
	action := r.PathValue("action")
	if action != "kill" {
		writeJSONError(w, http.StatusBadRequest, "Invalid action, only 'kill' is permitted.")
		return
	}

	if err := killProcess(pid); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"success": true, "message": fmt.Sprintf("Process with PID %d killed successfully", pid)})
}
