// This file implements the JSON REST API's crontab-editing route: reading
// every parsed line of the managed crontab, or rewriting the schedule/
// logging flag of one or more lines by number.
package handlers

import "net/http"

// APIServerCrons bundles the /api/server/crons handler.
type APIServerCrons struct{}

// ServeCrons handles GET/POST /api/server/crons. Reuses the same
// cron-line parsing/rewriting primitives (readCronJobs, addOrUpdateCron)
// as the HTML /server/crons page in cronjobs.go, just with a JSON body
// instead of an HTML form as the write path's input.
func (a *APIServerCrons) ServeCrons(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			Jobs []struct {
				LineNumber int    `json:"line_number"`
				Schedule   string `json:"schedule"`
				Logging    bool   `json:"logging"`
			} `json:"jobs"`
		}
		// A "jobs" value that parses as JSON but isn't an array (e.g. a
		// string or object) fails this typed decode and is reported as the
		// same "Invalid JSON format" error as a malformed body, rather than
		// the more specific "jobs must be a non-empty list" message.
		if !apiDecodeJSONBody(r, &body) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}
		if len(body.Jobs) == 0 {
			writeJSONError(w, http.StatusBadRequest, "jobs must be a non-empty list")
			return
		}
		for _, job := range body.Jobs {
			if err := addOrUpdateCron(job.LineNumber, job.Schedule, job.Logging); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "Failed to update cron jobs: "+err.Error())
				return
			}
		}
		writeJSON(w, map[string]interface{}{"success": true, "message": "Cron jobs updated successfully"})
		return
	}

	jobs, fileMissing := readCronJobs()
	if fileMissing {
		writeJSON(w, nil)
		return
	}
	writeJSON(w, jobs)
}
