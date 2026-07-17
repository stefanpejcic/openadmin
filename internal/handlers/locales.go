// This file implements installing translation locales (fetched from a
// GitHub repo listing) and setting the default one.
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// Locales bundles the /settings/locales handler.
type Locales struct {
	Sessions *auth.Manager
}

// LocalesGithubURL / TranslationsDir / DefaultLocaleFilePath are the
// GitHub repo listing, local translations directory, and default-locale
// marker file used by the locale installer.
var (
	LocalesGithubURL      = "https://api.github.com/repos/stefanpejcic/openpanel-translations/contents"
	TranslationsDir       = "/etc/openpanel/openpanel/translations"
	DefaultLocaleFilePath = "/etc/openpanel/openpanel/default_locale"
)

var localeFormatRe = regexp.MustCompile(`(?i)^[a-z]{2,3}-[a-z]{2,3}$`)

type githubContentItem struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type localeRow struct {
	Locale    string `json:"locale"`
	Provider  string `json:"provider"`
	Path      string `json:"path"`
	Installed bool   `json:"installed"`
	IsDefault bool   `json:"is_default"`
	// FlagCode is render-only (not part of the JSON API response): the
	// last two characters of the locale name, used for the flag icon
	// filename.
	FlagCode string `json:"-"`
}

// localesFetchFolders is injectable so tests never make a real GitHub API
// call, matching the caddyFetchMetrics/getDockerLogRun pattern used
// elsewhere.
var localesFetchFolders = func() ([]githubContentItem, int, error) {
	resp, err := http.Get(LocalesGithubURL)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, nil
	}
	var items []githubContentItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, resp.StatusCode, err
	}
	return items, resp.StatusCode, nil
}

// localesInstallRun is injectable so tests never shell out to a real
// opencli binary.
var localesInstallRun = func(locale string) error {
	return exec.Command("opencli", "locale", locale).Run()
}

// ServeLocales handles GET/POST /settings/locales.
func (l *Locales) ServeLocales(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		l.handlePost(w, r)
		return
	}
	l.handleGet(w, r)
}

// handlePost accepts either a form-encoded or a JSON body: r.ParseForm()
// only populates r.PostForm for an urlencoded/multipart body, leaving a
// JSON request's body untouched, so this checks PostForm first and falls
// back to decoding a JSON body when the form is empty.
func (l *Locales) handlePost(w http.ResponseWriter, r *http.Request) {
	isJSONRequest := strings.Contains(r.Header.Get("Content-Type"), "application/json")

	var localeToInstall, localeToSetDefault string
	r.ParseForm()
	if len(r.PostForm) > 0 {
		localeToInstall = r.PostFormValue("locale")
		localeToSetDefault = r.PostFormValue("default")
	} else {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		localeToInstall = body["locale"]
		localeToSetDefault = body["default"]
	}

	respondError := func(status int, message string) {
		writeJSONError(w, status, message)
	}
	respondSuccess := func(message string) {
		if isJSONRequest {
			writeJSON(w, map[string]string{"message": message})
			return
		}
		auth.AddFlash(w, r, l.Sessions, message, "success")
		http.Redirect(w, r, "/settings/locales", http.StatusSeeOther)
	}
	respondFailure := func(message string) {
		if isJSONRequest {
			writeJSONError(w, http.StatusInternalServerError, message)
			return
		}
		auth.AddFlash(w, r, l.Sessions, message, "error")
		http.Redirect(w, r, "/settings/locales", http.StatusSeeOther)
	}

	if localeToSetDefault != "" {
		if !localeFormatRe.MatchString(localeToSetDefault) {
			respondError(http.StatusBadRequest, "Invalid locale format.")
			return
		}
		baseLocale := strings.SplitN(localeToSetDefault, "-", 2)[0]
		info, statErr := os.Stat(filepath.Join(TranslationsDir, baseLocale))
		if statErr != nil || !info.IsDir() {
			respondError(http.StatusBadRequest, fmt.Sprintf("Locale '%s' is not installed.", localeToSetDefault))
			return
		}

		err := os.MkdirAll(filepath.Dir(DefaultLocaleFilePath), 0755)
		if err == nil {
			err = os.WriteFile(DefaultLocaleFilePath, []byte(baseLocale), 0644)
		}
		if err != nil {
			respondFailure(fmt.Sprintf("Failed to set default locale: %s", err))
			return
		}
		respondSuccess(fmt.Sprintf("Default locale set to '%s'.", baseLocale))
		return
	}

	if localeToInstall != "" {
		if !localeFormatRe.MatchString(localeToInstall) {
			respondError(http.StatusBadRequest, "Invalid locale format.")
			return
		}
		if err := localesInstallRun(localeToInstall); err != nil {
			respondFailure(fmt.Sprintf("Failed to install locale: %s", err))
			return
		}
		respondSuccess(fmt.Sprintf("Locale '%s' installed successfully.", localeToInstall))
		return
	}

	respondError(http.StatusBadRequest, "Missing 'locale' or 'default' parameter.")
}

// handleGet fetches the available locale list from GitHub and
// cross-references it against what's installed locally.
func (l *Locales) handleGet(w http.ResponseWriter, r *http.Request) {
	items, status, err := localesFetchFolders()
	if err != nil || status != http.StatusOK {
		http.Error(w, fmt.Sprintf("Failed to fetch data from GitHub: %d", status), http.StatusInternalServerError)
		return
	}

	defaultLocaleBase := "en"
	if raw, err := os.ReadFile(DefaultLocaleFilePath); err == nil {
		if trimmed := strings.TrimSpace(string(raw)); trimmed != "" {
			defaultLocaleBase = trimmed
		}
	}

	var results []localeRow
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
		flagCode := item.Name
		if len(flagCode) > 2 {
			flagCode = flagCode[len(flagCode)-2:]
		}
		results = append(results, localeRow{
			Locale:    item.Name,
			Provider:  provider,
			Path:      path,
			Installed: existsLocally,
			IsDefault: baseName == defaultLocaleBase,
			FlagCode:  flagCode,
		})
	}

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, map[string]interface{}{
			"default_locale": defaultLocaleBase,
			"translations":   results,
		})
		return
	}

	webtemplates.Render(w, "settings_locales.html", mergeChrome(map[string]interface{}{
		"Translations":  results,
		"DefaultLocale": defaultLocaleBase,
		"CSRFToken":     csrf.Token(r),
		"Flashes":       auth.PopFlashes(w, r, l.Sessions),
	}, r, "Locale Settings"))
}
