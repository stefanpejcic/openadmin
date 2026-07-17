// This file implements the JSON REST API's /api/settings/php route: viewing
// or updating the global PHP options.txt and per-version php.ini files.
// Reuses the same on-disk paths as the HTML /settings/php page in php.go.
package handlers

import (
	"net/http"
	"os"
)

// APISettingsPHP bundles the /api/settings/php handler.
type APISettingsPHP struct{}

// Serve handles GET/POST /api/settings/php.
func (a *APISettingsPHP) Serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		a.handlePost(w, r)
		return
	}
	a.handleGet(w, r)
}

func (a *APISettingsPHP) handlePost(w http.ResponseWriter, r *http.Request) {
	var data map[string]interface{}
	if !apiDecodeJSONBody(r, &data) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	updated := []string{}

	if optionsVal, present := data["options"]; present && optionsVal != nil {
		if s, isString := optionsVal.(string); isString {
			if err := os.WriteFile(phpOptionsPath, []byte(s), 0644); err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			updated = append(updated, "options")
		}
	}

	for _, version := range phpSavableVersions {
		val, present := data[version]
		if !present || val == nil {
			continue
		}
		s, isString := val.(string)
		if !isString {
			continue
		}
		if err := os.WriteFile(phpIniPaths[version], []byte(s), 0644); err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		updated = append(updated, version)
	}

	writeJSON(w, map[string]interface{}{"success": true, "updated": updated})
}

func (a *APISettingsPHP) handleGet(w http.ResponseWriter, r *http.Request) {
	fileContents := map[string]string{}
	optionsContent, err := readFileOrEmpty(phpOptionsPath)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	fileContents["options"] = optionsContent

	for _, key := range phpVersionKeys {
		content, err := readFileOrEmpty(phpIniPaths[key])
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		fileContents[key] = content
	}

	writeJSON(w, fileContents)
}
