// This file implements per-plan (and per-reseller) feature-set management --
// the /features/ index (create/list feature sets) and /features/{plan}
// (enable/disable individual features within one set).
package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// Features bundles the /features* handlers.
type Features struct {
	MySQL    *sql.DB
	Sessions *auth.Manager
}

// FeaturesDir / FeaturesJSONPath / FeaturesRedisSocketPath /
// FeaturesOpenpanelRestartFlagPath are the well-known filesystem paths used
// by feature-set management.
var (
	FeaturesDir                      = "/etc/openpanel/openpanel/features/"
	FeaturesJSONPath                 = "/etc/openpanel/openadmin/config/features.json"
	FeaturesRedisSocketPath          = "/tmp/redis/redis.sock"
	FeaturesOpenpanelRestartFlagPath = "/root/openpanel_restart_needed"
)

const featuresRedisCacheKey = "openpanel_cache_app._load_user_features_cached_memver"

var featuresNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// invalidateOpenpanelUserFeaturesCacheRun is injectable so tests never
// touch a real redis socket. It issues a best-effort DEL against
// OpenPanel's redis cache key over a Unix socket, returning whether it
// succeeded (used to decide whether a full restart flag is needed as a
// fallback).
var invalidateOpenpanelUserFeaturesCacheRun = func() bool {
	conn, err := net.DialTimeout("unix", FeaturesRedisSocketPath, 2*time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()

	cmd := "*2\r\n$3\r\nDEL\r\n$" + strconv.Itoa(len(featuresRedisCacheKey)) + "\r\n" + featuresRedisCacheKey + "\r\n"
	if _, err := conn.Write([]byte(cmd)); err != nil {
		return false
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	reply := make([]byte, 64)
	n, err := conn.Read(reply)
	if err != nil || n == 0 {
		return false
	}
	// A RESP error reply starts with '-'; anything else (":", "+") is
	// treated as success -- only a connection failure is treated as an
	// error, the DEL's actual return value isn't otherwise validated.
	return reply[0] != '-'
}

// ServeFeatures handles both GET/POST /features/ (plan == "") and
// GET/POST /features/{plan}: listing/creating feature sets, and viewing or
// editing which features are enabled within one set.
func (f *Features) ServeFeatures(w http.ResponseWriter, r *http.Request) {
	plan := r.PathValue("plan")
	if plan != "" && !featuresNameRe.MatchString(plan) {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	currentUser := auth.CurrentUser(r)
	resellerDir := FeaturesDir + currentUser.Username + "/"

	var dirsToRead []string
	if currentUser.Role == "reseller" {
		dirsToRead = []string{resellerDir}
		os.MkdirAll(resellerDir, 0755)
	} else {
		dirsToRead = []string{FeaturesDir, resellerDir}
	}

	if plan == "" {
		f.serveIndex(w, r, currentUser, dirsToRead, resellerDir)
		return
	}
	f.servePlan(w, r, plan, currentUser, resellerDir)
}

func (f *Features) serveIndex(w http.ResponseWriter, r *http.Request, currentUser *admindb.User, dirsToRead []string, resellerDir string) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		featureName := r.PostFormValue("feature_name")

		if featureName != "" && !featuresNameRe.MatchString(featureName) {
			auth.AddFlash(w, r, f.Sessions, "Invalid feature set name.", "danger")
			http.Redirect(w, r, "/features/", http.StatusSeeOther)
			return
		}
		if featureName == "" {
			auth.AddFlash(w, r, f.Sessions, "Name for feature set is required.", "danger")
			http.Redirect(w, r, "/features/", http.StatusSeeOther)
			return
		}

		filePath := FeaturesDir + featureName + ".txt"
		if currentUser.Role == "reseller" {
			filePath = resellerDir + featureName + ".txt"
		}

		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			if err := os.WriteFile(filePath, []byte(""), 0644); err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			auth.AddFlash(w, r, f.Sessions, "Feature set created successfully.", "success")
		} else {
			auth.AddFlash(w, r, f.Sessions, "Feature set already exists.", "warning")
		}
		http.Redirect(w, r, "/features/", http.StatusSeeOther)
		return
	}

	// A naive suffix check on directory entries -- doesn't distinguish
	// files from directories, so a directory literally named "*.txt" would
	// also show up. Left as a plain suffix check rather than adding an
	// IsDir() filter.
	var files []string
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

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, files)
		return
	}

	webtemplates.Render(w, "settings_features_index.html", mergeChrome(map[string]interface{}{
		"Files":   files,
		"Flashes": auth.PopFlashes(w, r, f.Sessions),
	}, r, "Feature Manager"))
}

func (f *Features) servePlan(w http.ResponseWriter, r *http.Request, plan string, currentUser *admindb.User, resellerDir string) {
	configFilePath := FeaturesDir + plan + ".txt"
	if currentUser.Role == "reseller" {
		configFilePath = resellerDir + plan + ".txt"
	}

	if _, err := os.Stat(configFilePath); err != nil {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	if r.Method == http.MethodPost {
		r.ParseForm()

		if !formHasKey(r, "action") {
			// The UI's buttons always submit an "action" field, so this is
			// only reachable via a malformed direct request; treated as a
			// server error rather than validated with a friendlier message.
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		action := strings.ToLower(r.PostFormValue("action"))

		if action != "enable_all" && action != "disable_all" && action != "update" && action != "delete" {
			auth.AddFlash(w, r, f.Sessions, "Invalid action.", "danger")
			http.Redirect(w, r, "/features/"+plan, http.StatusSeeOther)
			return
		}

		switch action {
		case "update":
			// Excludes both "csrf_token" and "action" (the update form
			// submits a hidden action=update field alongside the feature
			// checkboxes) so neither ends up written as a spurious line in
			// the feature-set file.
			keys := make([]string, 0, len(r.PostForm))
			for k := range r.PostForm {
				if k == "csrf_token" || k == "action" {
					continue
				}
				keys = append(keys, k)
			}
			sort.Strings(keys)
			var b strings.Builder
			for _, k := range keys {
				b.WriteString(k + "\n")
			}
			if err := os.WriteFile(configFilePath, []byte(b.String()), 0644); err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			auth.AddFlash(w, r, f.Sessions, "Features updated successfully.", "success")

		case "disable_all":
			if err := os.WriteFile(configFilePath, []byte(""), 0644); err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			auth.AddFlash(w, r, f.Sessions, "All features disabled successfully.", "success")

		case "enable_all":
			rawFeatures, err := os.ReadFile(FeaturesJSONPath)
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			var allFeatures []map[string]interface{}
			if err := json.Unmarshal(rawFeatures, &allFeatures); err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			var b strings.Builder
			for _, feat := range allFeatures {
				name, _ := feat["name"].(string)
				b.WriteString(name + "\n")
			}
			if err := os.WriteFile(configFilePath, []byte(b.String()), 0644); err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			auth.AddFlash(w, r, f.Sessions, "All features enabled successfully.", "success")

		case "delete":
			if plan == "default" {
				auth.AddFlash(w, r, f.Sessions, "Error: default features set can not be deleted.", "error")
				http.Redirect(w, r, "/features/default", http.StatusSeeOther)
				return
			}
			inUse, err := f.checkIfFeatureInUse(plan)
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			if inUse {
				auth.AddFlash(w, r, f.Sessions, fmt.Sprintf("Error: feature set %s can not be deleted as it is used by a hosting package.", plan), "error")
				http.Redirect(w, r, "/features/"+plan, http.StatusSeeOther)
				return
			}
			if _, err := os.Stat(configFilePath); err == nil {
				os.Remove(configFilePath)
			}
			auth.AddFlash(w, r, f.Sessions, fmt.Sprintf("features set %s deleted successfully.", plan), "success")
			http.Redirect(w, r, "/features/", http.StatusSeeOther)
			return
		}

		if !invalidateOpenpanelUserFeaturesCacheRun() {
			os.WriteFile(FeaturesOpenpanelRestartFlagPath, []byte("Restart needed for OpenPanel service."), 0644)
		}
	}

	var enabledModules []string
	if raw, err := os.ReadFile(configFilePath); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				enabledModules = append(enabledModules, trimmed)
			}
		}
	}

	rawFeatures, err := os.ReadFile(FeaturesJSONPath)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	var allFeatures []map[string]interface{}
	if err := json.Unmarshal(rawFeatures, &allFeatures); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Reuses the module-enablement helper from modules.go.
	moduleEnabled := modulesEnabledList(ModulesConfigFilePath)

	enabledSet := make(map[string]bool, len(enabledModules))
	for _, name := range enabledModules {
		enabledSet[name] = true
	}
	moduleEnabledSet := make(map[string]bool, len(moduleEnabled))
	for _, name := range moduleEnabled {
		moduleEnabledSet[name] = true
	}
	for _, feat := range allFeatures {
		name, _ := feat["name"].(string)
		feat["status"] = enabledSet[name]
		feat["module_enabled"] = moduleEnabledSet[name]
	}

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, map[string]interface{}{
			"enabled_modules": enabledModules,
			"features":        allFeatures,
		})
		return
	}

	webtemplates.Render(w, "settings_features_plan.html", mergeChrome(map[string]interface{}{
		"Plan":     plan,
		"Features": allFeatures,
		"Flashes":  auth.PopFlashes(w, r, f.Sessions),
	}, r, "Feature Manager"))
}

// FeatureSetPathForPlan resolves the feature-set .txt file for a plan,
// preferring a reseller-owned copy (FeaturesDir/<owner>/<set>.txt) over the
// top-level one when the owning reseller has one.
func FeatureSetPathForPlan(featureSet, owner string) string {
	if owner != "" {
		resellerPath := FeaturesDir + owner + "/" + featureSet + ".txt"
		if _, err := os.Stat(resellerPath); err == nil {
			return resellerPath
		}
	}
	return FeaturesDir + featureSet + ".txt"
}

// FeaturesForSet returns every known feature (from FeaturesJSONPath) marked
// with "status" (enabled in configFilePath) and "module_enabled". A missing
// configFilePath is treated as "nothing enabled", not an error.
func FeaturesForSet(configFilePath string) ([]map[string]interface{}, error) {
	var enabled []string
	if raw, err := os.ReadFile(configFilePath); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				enabled = append(enabled, trimmed)
			}
		}
	}

	rawFeatures, err := os.ReadFile(FeaturesJSONPath)
	if err != nil {
		return nil, err
	}
	var allFeatures []map[string]interface{}
	if err := json.Unmarshal(rawFeatures, &allFeatures); err != nil {
		return nil, err
	}

	moduleEnabled := modulesEnabledList(ModulesConfigFilePath)
	enabledSet := make(map[string]bool, len(enabled))
	for _, name := range enabled {
		enabledSet[name] = true
	}
	moduleEnabledSet := make(map[string]bool, len(moduleEnabled))
	for _, name := range moduleEnabled {
		moduleEnabledSet[name] = true
	}
	for _, feat := range allFeatures {
		name, _ := feat["name"].(string)
		feat["status"] = enabledSet[name]
		feat["module_enabled"] = moduleEnabledSet[name]
	}
	return allFeatures, nil
}

// checkIfFeatureInUse reports whether any plan currently references the
// given feature set, so it can be blocked from deletion while in use.
func (f *Features) checkIfFeatureInUse(featureName string) (bool, error) {
	if f.MySQL == nil {
		return false, nil
	}
	var dummy int
	err := f.MySQL.QueryRow("SELECT 1 FROM plans WHERE feature_set = ? LIMIT 1", featureName).Scan(&dummy)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
