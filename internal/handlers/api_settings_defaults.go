// This file implements the JSON REST API's default-account-template
// endpoints: the grouped .env editor and autostart-service selection, the
// raw docker-compose.yml/.env template files (with a reset-from-GitHub
// action), and the per-user compose/.env file editor. All reuse the same
// on-disk paths and read/write helpers as the HTML /settings/defaults*
// pages (defaults.go) -- only the request/response shape differs.
package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// APISettingsDefaults bundles the /api/settings/defaults* handlers.
type APISettingsDefaults struct {
	MySQL *sql.DB
}

// ServeSettingsDefaults handles GET/POST /api/settings/defaults. Wrap with
// (*APIAuth).RequireAPIAdmin.
func (d *APISettingsDefaults) ServeSettingsDefaults(w http.ResponseWriter, r *http.Request) {
	availableServices := getAvailableServices()

	if r.Method == http.MethodPost {
		d.handlePost(w, r, availableServices)
		return
	}

	phpVersionsData, err := defaultsPHPWatchRun()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	writeJSON(w, map[string]interface{}{
		"defaults":                     readDefaultsEnvGroups(),
		"php_versions_data":            phpVersionsData,
		"autostart_available_services": availableServices,
		"autostart_active_services":    getActiveServices(),
	})
}

func (d *APISettingsDefaults) handlePost(w http.ResponseWriter, r *http.Request, availableServices []string) {
	var data map[string]interface{}
	if !apiDecodeJSONBody(r, &data) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	valuesRaw, _ := data["values"].(map[string]interface{})

	raw, err := os.ReadFile(DefaultsEnvPath)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "Environment file not found.")
		return
	}

	varnishEnabled := false
	if v, ok := valuesRaw["VARNISH"]; ok {
		varnishEnabled = strings.TrimSpace(fmt.Sprintf("%v", v)) == "1"
	}

	lines := strings.SplitAfter(string(raw), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}

	newLines := make([]string, 0, len(lines))
	for _, line := range lines {
		stripped := strings.TrimSpace(line)
		if stripped == "" || strings.HasPrefix(stripped, "#") || !strings.Contains(line, "=") {
			newLines = append(newLines, line)
			continue
		}

		key, _, _ := strings.Cut(line, "=")
		key = strings.TrimSpace(key)

		if v, ok := valuesRaw[key]; ok {
			newValue := strings.TrimSpace(fmt.Sprintf("%v", v))
			if strings.HasSuffix(key, "_RAM") {
				newValue = normalizeRAM(newValue)
			}
			newValue = strings.Trim(newValue, `"`)
			newValue = strings.Trim(newValue, `'`)
			newLines = append(newLines, key+`="`+newValue+"\"\n")
			if key == "VARNISH" {
				varnishEnabled = newValue == "1"
			}
		} else {
			newLines = append(newLines, line)
		}
	}

	finalLines := make([]string, 0, len(newLines))
	for _, line := range newLines {
		if strings.Contains(line, "PROXY_HTTP_PORT=") {
			uncommented := strings.TrimSpace(strings.TrimLeft(line, "#"))
			if strings.Contains(uncommented, "=") {
				if varnishEnabled {
					finalLines = append(finalLines, uncommented+"\n")
				} else {
					finalLines = append(finalLines, "#"+uncommented+"\n")
				}
			} else {
				finalLines = append(finalLines, line)
			}
		} else {
			finalLines = append(finalLines, line)
		}
	}

	if err := os.WriteFile(DefaultsEnvPath, []byte(strings.Join(finalLines, "")), 0644); err != nil {
		writeJSONFailure(w, http.StatusInternalServerError, "Failed to update defaults: "+err.Error())
		return
	}

	if composeRaw, composeErr := os.ReadFile(DefaultsComposeFilePath); composeErr == nil {
		updated := rewriteDefaultsComposeVarnishPort(string(composeRaw), varnishEnabled)
		os.WriteFile(DefaultsComposeFilePath, []byte(updated), 0644)
	}

	if rawServices, ok := data["services"]; ok {
		var selected []string
		if list, ok := rawServices.([]interface{}); ok {
			for _, item := range list {
				if s := strings.TrimSpace(fmt.Sprintf("%v", item)); s != "" {
					selected = append(selected, s)
				}
			}
		}
		validSet := make(map[string]bool, len(availableServices))
		for _, s := range availableServices {
			validSet[s] = true
		}
		var valid []string
		for _, s := range selected {
			if validSet[s] {
				valid = append(valid, s)
			}
		}
		uniqueSorted := dedupeSorted(valid)
		content := strings.Join(uniqueSorted, "\n")
		if len(uniqueSorted) > 0 {
			content += "\n"
		}
		os.WriteFile(DefaultsAutostartServicesPath, []byte(content), 0644)
	}

	writeJSON(w, map[string]interface{}{"success": true, "message": "New defaults saved successfully!"})
}

// ServeSettingsDefaultsFiles handles GET/POST/DELETE
// /api/settings/defaults/files. Wrap with (*APIAuth).RequireAPIAdmin.
func (d *APISettingsDefaults) ServeSettingsDefaultsFiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var data map[string]interface{}
		if !apiDecodeJSONBody(r, &data) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}
		if v, ok := data["env"]; ok && v != nil {
			os.WriteFile(DefaultsEnvPath, []byte(fmt.Sprintf("%v", v)), 0644)
		}
		if v, ok := data["compose"]; ok && v != nil {
			os.WriteFile(DefaultsComposeFilePath, []byte(fmt.Sprintf("%v", v)), 0644)
		}
		writeJSON(w, map[string]interface{}{"success": true, "message": "Files updated successfully!"})
		return

	case http.MethodDelete:
		allSuccess := true
		errs := []string{}
		for _, item := range []struct{ key, url, path string }{
			{"compose", DefaultsRemoteComposeURL, DefaultsComposeFilePath},
			{"env", DefaultsRemoteEnvURL, DefaultsEnvPath},
		} {
			body, status, err := defaultsFetchRemoteRun(item.url)
			if err != nil {
				allSuccess = false
				errs = append(errs, fmt.Sprintf("Failed to fetch %s file from Github: %s", item.key, err.Error()))
				continue
			}
			if status == http.StatusOK {
				os.WriteFile(item.path, []byte(body), 0644)
			} else {
				allSuccess = false
				errs = append(errs, fmt.Sprintf("Failed to fetch %s file from Github. Status code: %d", item.key, status))
			}
		}
		if allSuccess {
			writeJSON(w, map[string]interface{}{"success": true, "message": "Defaults reset successfully from remote source!"})
			return
		}
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "errors": errs})
		return
	}

	envContent, _ := readFileOrEmpty(DefaultsEnvPath)
	composeContent, _ := readFileOrEmpty(DefaultsComposeFilePath)
	writeJSON(w, map[string]string{"env": envContent, "compose": composeContent})
}

// ServeSettingsDefaultsFilesForUser handles GET/POST
// /api/settings/defaults/files/{username}. Wrap with
// (*APIAuth).RequireAPIOwnerOrAdmin("username", handler).
func (d *APISettingsDefaults) ServeSettingsDefaultsFilesForUser(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	context, err := queryContextByUsername(d.MySQL, username)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "No context found for user")
		return
	}

	envPath := "/home/" + context + "/.env"
	composePath := "/home/" + context + "/docker-compose.yml"

	if r.Method == http.MethodPost {
		var data map[string]interface{}
		if !apiDecodeJSONBody(r, &data) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}
		if v, ok := data["env"]; ok && v != nil {
			os.WriteFile(envPath, []byte(fmt.Sprintf("%v", v)), 0644)
		}
		if v, ok := data["compose"]; ok && v != nil {
			os.WriteFile(composePath, []byte(fmt.Sprintf("%v", v)), 0644)
		}
	}

	envContent, _ := readFileOrEmpty(envPath)
	composeContent, _ := readFileOrEmpty(composePath)
	writeJSON(w, map[string]string{"env": envContent, "compose": composeContent})
}
