// This file implements the PHP options.txt and per-version php.ini editor.
package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
	"openadmin/internal/webtemplates"
)

// PHP bundles the /settings/php and /json/php/default_version handlers.
type PHP struct {
	Sessions *auth.Manager
	MySQL    *sql.DB
}

// phpVersionKeys lists all PHP version keys in display order (dropping
// "options", which is handled separately).
var phpVersionKeys = []string{
	"php56", "php70", "php71", "php72", "php73", "php74",
	"php80", "php81", "php82", "php83", "php84",
}

var phpOptionsPath = "/etc/openpanel/php/options.txt"
var phpIniPaths = map[string]string{
	"php56": "/etc/openpanel/php/ini/5.6.ini",
	"php70": "/etc/openpanel/php/ini/7.0.ini",
	"php71": "/etc/openpanel/php/ini/7.1.ini",
	"php72": "/etc/openpanel/php/ini/7.2.ini",
	"php73": "/etc/openpanel/php/ini/7.3.ini",
	"php74": "/etc/openpanel/php/ini/7.4.ini",
	"php80": "/etc/openpanel/php/ini/8.0.ini",
	"php81": "/etc/openpanel/php/ini/8.1.ini",
	"php82": "/etc/openpanel/php/ini/8.2.ini",
	"php83": "/etc/openpanel/php/ini/8.3.ini",
	"php84": "/etc/openpanel/php/ini/8.4.ini",
}

// phpVersionLabels maps each key to its human-readable version string
// (inserting a "." between the two trailing digits, e.g. "php74" -> "7.4").
var phpVersionLabels = map[string]string{
	"php56": "5.6", "php70": "7.0", "php71": "7.1", "php72": "7.2",
	"php73": "7.3", "php74": "7.4", "php80": "8.0", "php81": "8.1",
	"php82": "8.2", "php83": "8.3", "php84": "8.4",
}

// phpSavableVersions lists every version key accepted by the POST handler
// below. This must stay in sync with phpVersionKeys above -- omitting a
// version here would let its ini textarea render fine but make submitting
// it silently a no-op, silently dropping edits.
var phpSavableVersions = []string{
	"php56", "php70", "php71", "php72", "php73", "php74",
	"php80", "php81", "php82", "php83", "php84",
}

// readFileOrEmpty returns "" for a missing file; any other read error
// propagates to the caller, which turns it into a 500.
func readFileOrEmpty(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(raw), nil
}

// ServePHP handles GET/POST /settings/php.
func (p *PHP) ServePHP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()

		if options := r.PostFormValue("options"); options != "" {
			if err := os.WriteFile(phpOptionsPath, []byte(options), 0644); err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			auth.AddFlash(w, r, p.Sessions, "PHP options saved successfully!", "success")
		} else {
			for _, version := range phpSavableVersions {
				if content, ok := r.PostForm[version]; ok {
					if err := os.WriteFile(phpIniPaths[version], []byte(content[0]), 0644); err != nil {
						http.Error(w, "Internal Server Error", http.StatusInternalServerError)
						return
					}
					auth.AddFlash(w, r, p.Sessions, version+" INI file saved successfully!", "success")
				}
			}
		}
	}

	fileContents := map[string]string{}
	optionsContent, err := readFileOrEmpty(phpOptionsPath)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	fileContents["options"] = optionsContent

	type versionFile struct{ Key, Label, Content string }
	versions := make([]versionFile, 0, len(phpVersionKeys))
	for _, key := range phpVersionKeys {
		content, err := readFileOrEmpty(phpIniPaths[key])
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		fileContents[key] = content
		versions = append(versions, versionFile{Key: key, Label: phpVersionLabels[key], Content: content})
	}

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, fileContents)
		return
	}

	webtemplates.Render(w, "settings_php.html", mergeChrome(map[string]interface{}{
		"Options":   fileContents["options"],
		"Versions":  versions,
		"CSRFToken": csrf.Token(r),
		"Flashes":   auth.PopFlashes(w, r, p.Sessions),
	}, r, "PHP Settings"))
}

// phpDefaultVersionGetRun / phpDefaultVersionSetRun are injectable so
// tests never shell out to a real opencli binary.
//
// The GET side treats a nonzero exit as NOT an error -- only a failure to
// even start the process counts as an error there. The POST side treats
// any nonzero exit as an error.
var phpDefaultVersionGetRun = func(username string) (string, error) {
	out, err := exec.Command("opencli", "php-default", username).CombinedOutput()
	if _, isExit := err.(*exec.ExitError); isExit {
		return string(out), nil
	}
	return string(out), err
}

var phpDefaultVersionSetRun = func(username, version string) error {
	return exec.Command("opencli", "php-default", username, "--update", version).Run()
}

// ServePHPDefaultVersion handles GET/POST /json/php/default_version/{username}.
func (p *PHP) ServePHPDefaultVersion(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")

	cu := auth.CurrentUser(r)
	actingUsername, actingRole := "", ""
	if cu != nil {
		actingUsername, actingRole = cu.Username, cu.Role
	}
	if !paneldb.CheckIfOwnerForUser(p.MySQL, username, actingUsername, actingRole) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if r.Method == http.MethodPost {
		var body struct {
			Version string `json:"version"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Version == "" {
			writeJSONError(w, http.StatusBadRequest, "Version must be provided")
			return
		}
		if err := phpDefaultVersionSetRun(username, body.Version); err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
				"error":   "Failed to retrieve or update default PHP version",
				"details": err.Error(),
			})
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]string{
			"message": "Default PHP version for user '" + username + "' updated to: " + body.Version,
		})
		return
	}

	output, err := phpDefaultVersionGetRun(username)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
			"error":   "Failed to retrieve or update default PHP version",
			"details": err.Error(),
		})
		return
	}
	output = strings.TrimSpace(output)
	prefix := "Default PHP version for user '" + username + "' is: "
	if strings.HasPrefix(output, prefix) {
		parts := strings.SplitN(output, ": ", 2)
		writeJSON(w, map[string]string{"default_version": parts[1]})
		return
	}
	writeJSONError(w, http.StatusBadRequest, "Unexpected output format")
}

func writeJSONStatus(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
