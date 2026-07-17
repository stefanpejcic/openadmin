// This file implements the JSON REST API's update-management routes:
// GET/POST /api/settings/updates (auto-update preference plus the latest
// available version and update logs), POST /api/settings/updates/now
// (kick off an immediate update in the background), and
// GET/POST /api/settings/updates/tags (list Docker image tags, or pin the
// panel to a specific version). All three reuse the same injectable
// network/podman calls as the HTML /settings/updates page and the
// pre-existing /api/docker-tags route in updates.go -- only the response
// shape differs, and this tags route lives at a different, JSON-API-only
// path with its own JWT auth.
package handlers

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"openadmin/internal/config"
)

// APISettingsUpdates bundles the /api/settings/updates* handlers.
type APISettingsUpdates struct{}

// Serve handles GET/POST /api/settings/updates.
func (a *APISettingsUpdates) Serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		a.handlePost(w, r)
		return
	}
	a.handleGet(w, r)
}

func (a *APISettingsUpdates) handlePost(w http.ResponseWriter, r *http.Request) {
	var data map[string]interface{}
	if !apiDecodeJSONBody(r, &data) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	preference, _ := data["preference"].(string)

	updates, ok := updatesPreferenceMap[preference]
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid preference.")
		return
	}

	raw, err := os.ReadFile(UpdatesConfigFilePath)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	content := string(raw)
	for key, value := range updates {
		content = strings.ReplaceAll(content, key+"=on", key+"="+value)
		content = strings.ReplaceAll(content, key+"=off", key+"="+value)
	}
	if err := os.WriteFile(UpdatesConfigFilePath, []byte(content), 0644); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]interface{}{"success": true, "message": "Update preferences saved successfully."})
}

func (a *APISettingsUpdates) handleGet(w http.ResponseWriter, r *http.Request) {
	configData := config.Load(UpdatesConfigFilePath)
	updateLogs := getOpUpdateLogs()
	if updateLogs == nil {
		updateLogs = []updateLogEntry{}
	}
	writeJSON(w, map[string]interface{}{
		"config_data":    configData,
		"latest_version": getLatestVersion(),
		"update_logs":    updateLogs,
	})
}

// ServeUpdateNow handles POST /api/settings/updates/now.
func (a *APISettingsUpdates) ServeUpdateNow(w http.ResponseWriter, r *http.Request) {
	if err := updatesUpdateNowRun(); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   "Failed to start the update process. Details: " + err.Error(),
		})
		return
	}
	writeJSON(w, map[string]interface{}{"success": true, "message": "Update process started successfully."})
}

// ServeTags handles GET/POST /api/settings/updates/tags.
func (a *APISettingsUpdates) ServeTags(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		tags, err := fetchDockerTags()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if tags == nil {
			tags = []string{}
		}
		writeJSON(w, tags)
		return
	}

	var data map[string]interface{}
	if !apiDecodeJSONBody(r, &data) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	version, _ := data["version"].(string)
	if version == "" {
		writeJSONError(w, http.StatusBadRequest, "version is required")
		return
	}
	if !dockerTagVersionRe.MatchString(version) {
		writeJSONError(w, http.StatusBadRequest, "Invalid version format provided. Use NNN or N.N or N.N.N")
		return
	}

	if err := writeEnvVersion(version); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	if err := updatesPullImageRun(version); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": "Command failed: " + err.Error()})
		return
	}
	if err := updatesComposeUpRun(); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": "Command failed: " + err.Error()})
		return
	}

	writeJSON(w, map[string]interface{}{"success": true, "message": fmt.Sprintf("Downgraded to version %s successfully.", version)})
}
