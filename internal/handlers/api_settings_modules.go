// This file implements the JSON REST API's /api/settings/modules route:
// viewing the enabled-modules/plugins list, or replacing it. Reuses the same
// config file, features.json, docker-compose toggling, and plugin scan as
// the HTML /settings/modules page in modules.go -- only the response shape
// differs, plus the POST body here is a JSON array (not sorted form keys).
package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

// APISettingsModules bundles the /api/settings/modules handler.
type APISettingsModules struct{}

// Serve handles GET/POST /api/settings/modules.
func (a *APISettingsModules) Serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		a.handlePost(w, r)
		return
	}
	a.handleGet(w, r)
}

func (a *APISettingsModules) handlePost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EnabledModules []string `json:"enabled_modules"`
	}
	if !apiDecodeJSONBody(r, &body) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	enabledModulesValue := strings.Join(body.EnabledModules, ",")

	raw, err := os.ReadFile(ModulesConfigFilePath)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	lines := strings.SplitAfter(string(raw), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	for i, line := range lines {
		if strings.HasPrefix(line, "enabled_modules=") {
			lines[i] = `enabled_modules="` + enabledModulesValue + "\"\n"
		}
	}
	if err := os.WriteFile(ModulesConfigFilePath, []byte(strings.Join(lines, "")), 0644); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	updateServiceInDockerCompose("phpmyadmin", strings.Contains(enabledModulesValue, "phpmyadmin"))
	updateServiceInDockerCompose("clamav", strings.Contains(enabledModulesValue, "malware_scan"))

	// A raw substring check against the whole comma-joined value, same as
	// the HTML admin page's handler -- would also trigger for any
	// hypothetical module whose name merely contains "dns".
	if strings.Contains(enabledModulesValue, "dns") {
		if _, err := os.Stat(ModulesRndcKeyPath); os.IsNotExist(err) {
			modulesRndcGenRun()
		}
	}

	os.WriteFile(ModulesOpenpanelRestartFlagPath, []byte("Restart needed for OpenPanel service."), 0644)
	writeJSON(w, map[string]interface{}{"success": true, "message": "Modules updated successfully."})
}

func (a *APISettingsModules) handleGet(w http.ResponseWriter, r *http.Request) {
	enabledModules := modulesEnabledList(ModulesConfigFilePath)
	enabledSet := make(map[string]bool, len(enabledModules))
	for _, name := range enabledModules {
		enabledSet[name] = true
	}

	rawFeatures, err := os.ReadFile(ModulesFeaturesJSONPath)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	allFeatures := []map[string]interface{}{}
	if err := json.Unmarshal(rawFeatures, &allFeatures); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	for _, feature := range allFeatures {
		name, _ := feature["name"].(string)
		feature["status"] = enabledSet[name]
	}

	plugins := getAllPlugins(ModulesPluginsBaseDir)
	if plugins == nil {
		plugins = []map[string]string{}
	}

	writeJSON(w, map[string]interface{}{"features": allFeatures, "plugins": plugins})
}
