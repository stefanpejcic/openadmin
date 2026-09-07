// This file implements the PHP options.txt and per-version php.ini editor.
package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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

var phpOptionsPath = "/etc/openpanel/php/options.txt"

// phpIniDir holds one "<major>.<minor>.ini" file per installed PHP version
// (e.g. "8.5.ini"). The settings/php page and every other PHP-version
// picker discover their version list by scanning this directory instead of
// a hardcoded list, so a new PHP release shows up as soon as its ini file
// is dropped in -- no code change needed.
var phpIniDir = "/etc/openpanel/php/ini"

// phpIniFileRE matches an ini filename directly in phpIniDir, e.g. "8.5.ini".
var phpIniFileRE = regexp.MustCompile(`^(\d+)\.(\d+)\.ini$`)

// phpVersionKey derives the form/query key used for a version throughout
// this file (e.g. "8.5" -> "php85") by stripping the dot -- matches the
// scheme the templates and JS already use.
func phpVersionKey(label string) string {
	return "php" + strings.ReplaceAll(label, ".", "")
}

// discoverPHPVersions scans dir for "<major>.<minor>.ini" files and returns
// their version labels (e.g. "8.5"), sorted oldest to newest. A missing
// directory yields an empty slice rather than an error.
func discoverPHPVersions(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	type version struct{ major, minor int }
	found := map[string]version{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		m := phpIniFileRE.FindStringSubmatch(entry.Name())
		if m == nil {
			continue
		}
		major, _ := strconv.Atoi(m[1])
		minor, _ := strconv.Atoi(m[2])
		found[m[1]+"."+m[2]] = version{major, minor}
	}

	labels := make([]string, 0, len(found))
	for label := range found {
		labels = append(labels, label)
	}
	sort.Slice(labels, func(i, j int) bool {
		vi, vj := found[labels[i]], found[labels[j]]
		if vi.major != vj.major {
			return vi.major < vj.major
		}
		return vi.minor < vj.minor
	})
	return labels, nil
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
	versionLabels, err := discoverPHPVersions(phpIniDir)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if r.Method == http.MethodPost {
		r.ParseForm()

		if options := r.PostFormValue("options"); options != "" {
			if err := os.WriteFile(phpOptionsPath, []byte(options), 0644); err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			auth.AddFlash(w, r, p.Sessions, "PHP options saved successfully!", "success")
		} else {
			for _, label := range versionLabels {
				key := phpVersionKey(label)
				if content, ok := r.PostForm[key]; ok {
					path := filepath.Join(phpIniDir, label+".ini")
					if err := os.WriteFile(path, []byte(content[0]), 0644); err != nil {
						http.Error(w, "Internal Server Error", http.StatusInternalServerError)
						return
					}
					auth.AddFlash(w, r, p.Sessions, key+" INI file saved successfully!", "success")
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
	versions := make([]versionFile, 0, len(versionLabels))
	for _, label := range versionLabels {
		key := phpVersionKey(label)
		path := filepath.Join(phpIniDir, label+".ini")
		content, err := readFileOrEmpty(path)
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		fileContents[key] = content
		versions = append(versions, versionFile{Key: key, Label: label, Content: content})
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
