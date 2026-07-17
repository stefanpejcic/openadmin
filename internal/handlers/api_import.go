// This file implements the JSON REST API's backup-import and
// OpenPanel-to-OpenPanel transfer routes. Every handler here reuses the
// same on-disk log directories, opencli/git plumbing, and helper
// functions as the /import/*, /json/backup-files, and /json/transfers*
// HTML admin routes in importer.go -- only the response shape (always
// JSON, no flash-and-redirect) and, for the transfers-for-user route, the
// authorization mechanism (JWT bearer token instead of a session cookie)
// differ.
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// APIImport bundles the /api/import/* and /api/support-adjacent import
// handlers.
type APIImport struct{}

// apiImportPanelDisplayNames lists the backup panel types the JSON API
// accepts for /api/import/{panel_type} -- narrower than the HTML
// /import/{panel_type} route, which also accepts "openpanel" (a restore
// from an OpenPanel-native backup, exposed instead via /user/import).
var apiImportPanelDisplayNames = map[string]string{
	"cpanel":     "cPanel",
	"cyberpanel": "CyberPanel",
}

// ServeImportFromBackup handles GET/POST /api/import/{panel_type}.
func (a *APIImport) ServeImportFromBackup(w http.ResponseWriter, r *http.Request) {
	panelType := strings.ToLower(r.PathValue("panel_type"))
	displayName, ok := apiImportPanelDisplayNames[panelType]
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Unsupported panel type: "+panelType)
		return
	}

	if r.Method == http.MethodPost {
		var body struct {
			Path     string `json:"path"`
			PlanName string `json:"plan_name"`
		}
		if !apiDecodeJSONBody(r, &body) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}
		if body.Path == "" || body.PlanName == "" {
			writeJSONError(w, http.StatusBadRequest, "Backup path and plan name are required.")
			return
		}

		cloneFailed, err := importerCloneAndRunImportScriptRun(displayName, body.Path, body.PlanName)
		if err != nil {
			msg := err.Error()
			if cloneFailed {
				msg = "Error during execution: " + msg
			}
			writeJSONFailure(w, http.StatusInternalServerError, msg)
			return
		}
		writeJSON(w, map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("Import process from %s (%s backup) has started.", body.Path, displayName),
		})
		return
	}

	writeJSON(w, listLogFilesWithStatus(ImporterOpenPanelImportLogDir))
}

// writeJSONFailure writes the {"success": false, "error": message} shape
// used by every import/transfer action route below on failure.
func writeJSONFailure(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": message})
}

// apiServeImportLog reads a log file under logDir named by the
// {log_filename...} path wildcard and reports its contents as JSON. A
// path that escapes logDir is treated the same as a missing file (404),
// matching the underlying safe_join_or_400 check's "no path, or not a
// file" outcome.
func apiServeImportLog(w http.ResponseWriter, r *http.Request, logDir string) {
	logFilename := r.PathValue("log_filename")

	logPath, ok := safeJoinOr400(logDir, logFilename)
	if ok {
		info, err := os.Stat(logPath)
		if err != nil || info.IsDir() {
			ok = false
		}
	}
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"status": "error", "message": "Log file does not exist."})
		return
	}

	content, err := os.ReadFile(logPath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"status": "error", "message": "Error accessing log file: " + err.Error()})
		return
	}
	writeJSON(w, map[string]string{"status": "success", "log": string(content)})
}

// ServeAccountImportLog handles GET /api/import/logs/account/{log_filename...}.
func (a *APIImport) ServeAccountImportLog(w http.ResponseWriter, r *http.Request) {
	apiServeImportLog(w, r, ImporterOpenPanelImportLogDir)
}

// ServeTransferImportLog handles GET /api/import/logs/transfer/{log_filename...}.
func (a *APIImport) ServeTransferImportLog(w http.ResponseWriter, r *http.Request) {
	apiServeImportLog(w, r, ImporterTransferLogDir)
}

// ServeListBackupFiles handles GET /api/import/backup-files: identical to
// the HTML admin route's response shape (a flat array of matched paths),
// so it delegates straight to it.
func (a *APIImport) ServeListBackupFiles(w http.ResponseWriter, r *http.Request) {
	(&Importer{}).ServeListBackupFiles(w, r)
}

// ServeTransfers handles GET/POST /api/import/transfers: GET lists every
// transfer log file (delegating to the HTML admin route's identical
// response shape), POST starts a new transfer from a JSON body instead of
// a form post.
func (a *APIImport) ServeTransfers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		(&Importer{}).ServeListTransfers(w, r)
		return
	}

	var data map[string]interface{}
	if !apiDecodeJSONBody(r, &data) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	openpanelUsername, _ := data["openpanel_username"].(string)
	server, _ := data["server"].(string)
	if openpanelUsername == "" || server == "" {
		writeJSONError(w, http.StatusBadRequest, "Server IP and OpenPanel username are required.")
		return
	}

	username := "root"
	if v, present := data["username"]; present {
		username = apiJSONValueToString(v)
	}

	args := []string{"opencli", "user-transfer", "--account", openpanelUsername, "--host", server, "--username", username}
	if v, present := data["password"]; present && apiJSONTruthy(v) {
		args = append(args, "--password", apiJSONValueToString(v))
	}
	if v, present := data["port"]; present && apiJSONTruthy(v) {
		args = append(args, "--port", apiJSONValueToString(v))
	}
	if v, present := data["live_transfer"]; present && apiJSONTruthy(v) {
		args = append(args, "--live-transfer")
	}
	if !isDomain(server) {
		configureIptablesRun(server)
	}

	if err := importerStartTransferRun(args); err != nil {
		writeJSONFailure(w, http.StatusInternalServerError, "Error starting transfer: "+err.Error())
		return
	}
	writeJSON(w, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Transfer process for account %s started in the background.", openpanelUsername),
	})
}

// ServeTransfersForUser handles GET /api/import/transfers/{username}. Wrap
// with (*APIAuth).RequireAPIOwnerOrAdmin("username", ...) -- ownership is
// already enforced by that middleware, so this only strips a
// "SUSPENDED_" prefix (mirroring apiContainersDisplayUsername's handling
// of the same suspended-account naming convention elsewhere in the API)
// before scanning the transfer logs.
func (a *APIImport) ServeTransfersForUser(w http.ResponseWriter, r *http.Request) {
	username := apiContainersDisplayUsername(r.PathValue("username"))
	writeJSON(w, scanTransfersForUsername(username))
}
