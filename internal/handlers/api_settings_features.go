// This file implements the JSON REST API's feature-set management
// endpoint: listing/creating feature sets and viewing/updating which
// features are enabled within one, reusing the same on-disk layout and
// plugin/module helpers as the HTML /features* pages (features.go,
// modules.go) -- only the request/response shape differs.
package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"openadmin/internal/admindb"
)

// APISettingsFeatures bundles the /api/settings/features(/{plan}) handler.
type APISettingsFeatures struct {
	MySQL *sql.DB
	Auth  *APIAuth
}

// ServeSettingsFeatures handles GET/POST /api/settings/features and
// /api/settings/features/{plan} (the {plan} path value is "" for the
// no-segment route). Wrap with (*APIAuth).RequireAPIToken -- this handler
// resolves the acting user itself.
func (f *APISettingsFeatures) ServeSettingsFeatures(w http.ResponseWriter, r *http.Request) {
	plan := r.PathValue("plan")
	if plan != "" && !featuresNameRe.MatchString(plan) {
		writeJSONError(w, http.StatusBadRequest, "Invalid feature set name.")
		return
	}

	actingUser, ok := f.Auth.ActingAPIUserOr404(w, r)
	if !ok {
		return
	}

	resellerDir := FeaturesDir + actingUser.Username + "/"
	var dirsToRead []string
	if actingUser.Role == "reseller" {
		dirsToRead = []string{resellerDir}
		os.MkdirAll(resellerDir, 0755)
	} else {
		dirsToRead = []string{FeaturesDir, resellerDir}
	}

	if plan == "" {
		f.serveIndex(w, r, actingUser, dirsToRead, resellerDir)
		return
	}
	f.servePlan(w, r, plan, actingUser, resellerDir)
}

func (f *APISettingsFeatures) serveIndex(w http.ResponseWriter, r *http.Request, actingUser *admindb.User, dirsToRead []string, resellerDir string) {
	if r.Method == http.MethodPost {
		var body struct {
			FeatureName string `json:"feature_name"`
		}
		if !apiDecodeJSONBody(r, &body) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}
		if body.FeatureName == "" {
			writeJSONError(w, http.StatusBadRequest, "feature_name is required.")
			return
		}
		if !featuresNameRe.MatchString(body.FeatureName) {
			writeJSONError(w, http.StatusBadRequest, "Invalid feature set name.")
			return
		}

		dir := FeaturesDir
		if actingUser.Role == "reseller" {
			dir = resellerDir
		}
		filePath := dir + body.FeatureName + ".txt"
		if _, err := os.Stat(filePath); err == nil {
			writeJSONError(w, http.StatusBadRequest, "Feature set already exists.")
			return
		}
		if err := os.WriteFile(filePath, []byte(""), 0644); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "message": "Feature set created successfully."})
		return
	}

	files := []string{}
	for _, dir := range dirsToRead {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".txt") {
				files = append(files, strings.TrimSuffix(e.Name(), ".txt"))
			}
		}
	}
	writeJSON(w, files)
}

func (f *APISettingsFeatures) servePlan(w http.ResponseWriter, r *http.Request, plan string, actingUser *admindb.User, resellerDir string) {
	configFilePath := FeaturesDir + plan + ".txt"
	if actingUser.Role == "reseller" {
		configFilePath = resellerDir + plan + ".txt"
	}

	if _, err := os.Stat(configFilePath); err != nil {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("Feature set %s not found", plan))
		return
	}

	if r.Method == http.MethodPost {
		var body struct {
			Action   string   `json:"action"`
			Features []string `json:"features"`
		}
		if !apiDecodeJSONBody(r, &body) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}
		action := strings.ToLower(body.Action)
		if action != "enable_all" && action != "disable_all" && action != "update" && action != "delete" {
			writeJSONError(w, http.StatusBadRequest, "Invalid action.")
			return
		}

		switch action {
		case "update":
			var b strings.Builder
			for _, feature := range body.Features {
				b.WriteString(feature + "\n")
			}
			if err := os.WriteFile(configFilePath, []byte(b.String()), 0644); err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}

		case "disable_all":
			if err := os.WriteFile(configFilePath, []byte(""), 0644); err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}

		case "enable_all":
			rawFeatures, err := os.ReadFile(FeaturesJSONPath)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			var allFeatures []map[string]interface{}
			if err := json.Unmarshal(rawFeatures, &allFeatures); err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			var b strings.Builder
			for _, feat := range allFeatures {
				name, _ := feat["name"].(string)
				b.WriteString(name + "\n")
			}
			if err := os.WriteFile(configFilePath, []byte(b.String()), 0644); err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}

		case "delete":
			if plan == "default" {
				writeJSONError(w, http.StatusBadRequest, "default feature set can not be deleted.")
				return
			}
			inUse, err := (&Features{MySQL: f.MySQL}).checkIfFeatureInUse(plan)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if inUse {
				writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Feature set %s can not be deleted as it is used by a hosting package.", plan))
				return
			}
			if _, err := os.Stat(configFilePath); err == nil {
				os.Remove(configFilePath)
			}
			writeJSON(w, map[string]interface{}{"success": true, "message": fmt.Sprintf("Feature set %s deleted successfully.", plan)})
			return
		}

		if !invalidateOpenpanelUserFeaturesCacheRun() {
			os.WriteFile(FeaturesOpenpanelRestartFlagPath, []byte("Restart needed for OpenPanel service."), 0644)
		}
		writeJSON(w, map[string]interface{}{"success": true, "message": fmt.Sprintf("Feature set %s updated (%s).", plan, action)})
		return
	}

	enabledModules := []string{}
	if raw, err := os.ReadFile(configFilePath); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				enabledModules = append(enabledModules, trimmed)
			}
		}
	}

	plugins := getAllPlugins(ModulesPluginsBaseDir)

	rawFeatures, err := os.ReadFile(FeaturesJSONPath)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var allFeatures []map[string]interface{}
	if err := json.Unmarshal(rawFeatures, &allFeatures); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	moduleEnabled := modulesEnabledList(ModulesConfigFilePath)
	moduleEnabledSet := make(map[string]bool, len(moduleEnabled))
	for _, name := range moduleEnabled {
		moduleEnabledSet[name] = true
	}
	enabledSet := make(map[string]bool, len(enabledModules))
	for _, name := range enabledModules {
		enabledSet[name] = true
	}

	for _, feat := range allFeatures {
		name, _ := feat["name"].(string)
		feat["status"] = enabledSet[name]
		feat["module_enabled"] = moduleEnabledSet[name]
	}

	pluginsOut := make([]map[string]interface{}, 0, len(plugins))
	for _, p := range plugins {
		m := make(map[string]interface{}, len(p)+2)
		for k, v := range p {
			m[k] = v
		}
		name := p["name"]
		m["status"] = enabledSet[name]
		m["module_enabled"] = moduleEnabledSet[name]
		pluginsOut = append(pluginsOut, m)
	}

	writeJSON(w, map[string]interface{}{
		"enabled_modules": enabledModules,
		"plan":            plan,
		"features":        allFeatures,
		"plugins":         pluginsOut,
	})
}
