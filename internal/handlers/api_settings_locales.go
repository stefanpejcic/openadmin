// This file implements the JSON REST API's /api/settings/locales route:
// listing installed/available translation locales, installing a new one, or
// setting the default. Reuses the same GitHub-listing fetch, translations
// directory, and default-locale marker file as the HTML /settings/locales
// page in locales.go -- only the response shape and the strict
// JSON-content-type requirement on POST differ.
package handlers

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// APISettingsLocales bundles the /api/settings/locales handler.
type APISettingsLocales struct{}

// Serve handles GET/POST /api/settings/locales.
func (a *APISettingsLocales) Serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		a.handlePost(w, r)
		return
	}
	a.handleGet(w, r)
}

func (a *APISettingsLocales) handlePost(w http.ResponseWriter, r *http.Request) {
	var data map[string]interface{}
	if !apiDecodeJSONBody(r, &data) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	localeToInstall, _ := data["locale"].(string)
	localeToSetDefault, _ := data["default"].(string)

	if localeToSetDefault != "" {
		if !localeFormatRe.MatchString(localeToSetDefault) {
			writeJSONError(w, http.StatusBadRequest, "Invalid locale format.")
			return
		}
		baseLocale := strings.SplitN(localeToSetDefault, "-", 2)[0]
		info, statErr := os.Stat(filepath.Join(TranslationsDir, baseLocale))
		if statErr != nil || !info.IsDir() {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Locale '%s' is not installed.", localeToSetDefault))
			return
		}

		err := os.MkdirAll(filepath.Dir(DefaultLocaleFilePath), 0755)
		if err == nil {
			err = os.WriteFile(DefaultLocaleFilePath, []byte(baseLocale), 0644)
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to set default locale: %s", err))
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "message": fmt.Sprintf("Default locale set to '%s'.", baseLocale)})
		return
	}

	if localeToInstall != "" {
		if !localeFormatRe.MatchString(localeToInstall) {
			writeJSONError(w, http.StatusBadRequest, "Invalid locale format.")
			return
		}
		if err := localesInstallRun(localeToInstall); err != nil {
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to install locale: %s", err))
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "message": fmt.Sprintf("Locale '%s' installed successfully.", localeToInstall)})
		return
	}

	writeJSONError(w, http.StatusBadRequest, "Missing 'locale' or 'default' parameter.")
}

func (a *APISettingsLocales) handleGet(w http.ResponseWriter, r *http.Request) {
	items, status, err := localesFetchFolders()
	if err != nil || status != http.StatusOK {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to fetch data from GitHub: %d", status))
		return
	}

	defaultLocaleBase := "en"
	if raw, err := os.ReadFile(DefaultLocaleFilePath); err == nil {
		if trimmed := strings.TrimSpace(string(raw)); trimmed != "" {
			defaultLocaleBase = trimmed
		}
	}

	results := []localeRow{}
	for _, item := range items {
		if item.Type != "dir" {
			continue
		}
		baseName := strings.SplitN(item.Name, "-", 2)[0]
		localPath := filepath.Join(TranslationsDir, baseName)
		info, statErr := os.Stat(localPath)
		existsLocally := statErr == nil && info.IsDir()

		provider := "Community"
		if baseName == "en" {
			provider = "OpenPanel"
		}
		path := ""
		if existsLocally {
			path = localPath
		}
		results = append(results, localeRow{
			Locale:    item.Name,
			Provider:  provider,
			Path:      path,
			Installed: existsLocally,
			IsDefault: baseName == defaultLocaleBase,
		})
	}

	writeJSON(w, map[string]interface{}{
		"default_locale": defaultLocaleBase,
		"translations":   results,
	})
}
