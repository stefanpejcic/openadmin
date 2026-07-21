// This file implements the default .env editor for new user accounts
// (with PHP-FPM/VARNISH grouping), autostart-service selection, the
// docker-compose/.env template file editor (with a podman-compose-based
// validation preview and a reset-from-GitHub action), and the per-user
// files endpoint.
package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/podman"
	"openadmin/internal/webtemplates"
)

// Defaults bundles the /settings/defaults* handlers.
type Defaults struct {
	MySQL    *sql.DB
	Sessions *auth.Manager
}

// DefaultsEnvPath / DefaultsComposeFilePath / DefaultsAutostartServicesPath /
// DefaultsRemoteComposeURL / DefaultsRemoteEnvURL / DefaultsTmpDir are the
// hardcoded paths for the defaults editor. The env/compose paths are
// shared with getAvailableServices() below, so one set of vars covers
// both.
var (
	DefaultsEnvPath               = "/etc/openpanel/docker/compose/1.0/.env"
	DefaultsComposeFilePath       = "/etc/openpanel/docker/compose/1.0/docker-compose.yml"
	DefaultsAutostartServicesPath = "/etc/openpanel/docker/compose/1.0/autostart.services"
	DefaultsRemoteComposeURL      = "https://raw.githubusercontent.com/stefanpejcic/openpanel-configuration/refs/heads/main/docker/compose/1.0/docker-compose.yml"
	DefaultsRemoteEnvURL          = "https://raw.githubusercontent.com/stefanpejcic/openpanel-configuration/refs/heads/main/docker/compose/1.0/.env"
	DefaultsTmpDir                = "/tmp/user_defaults"
)

var defaultsExcludeKeys = map[string]bool{
	"USERNAME": true, "USER_ID": true, "CONTEXT": true, "TOTAL_CPU": true, "TOTAL_RAM": true,
	"PIDS": true, "PGADMIN_MAIL": true, "OS": true, "HOSTNAME": true, "OS_CPU": true,
	"OS_RAM": true, "OS_PIDS": true, "BUSYBOX_RAM": true, "BUSYBOX_CPU": true,
}

func defaultsEnvKeyExcluded(key string) bool {
	if defaultsExcludeKeys[key] {
		return true
	}
	if strings.HasSuffix(key, "_PORT") && key != "PROXY_HTTP_PORT" {
		return true
	}
	return strings.HasSuffix(key, "_PW") || strings.HasSuffix(key, "_PASSWORD") || strings.HasSuffix(key, "_USER")
}

var phpFPMKeyRe = regexp.MustCompile(`^PHP_FPM_(\d+)_(\d+)_(.+)$`)

// readDefaultsEnvGroups parses DefaultsEnvPath into grouped key/value
// data. The DEFAULTS group holds a mix of string values (WEB_SERVER,
// PHP_VERSION, MYSQL_TYPE) and a bool (VARNISH), so it's represented as
// map[string]interface{} throughout. Returns nil if the file is missing.
func readDefaultsEnvGroups() map[string]map[string]interface{} {
	raw, err := os.ReadFile(DefaultsEnvPath)
	if err != nil {
		return nil
	}

	grouped := map[string]map[string]interface{}{"DEFAULTS": {}}

	for _, rawLine := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || !strings.Contains(trimmed, "=") {
			continue
		}

		key, value, _ := strings.Cut(trimmed, "=")
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"`)
		value = strings.Trim(value, `'`)

		if defaultsEnvKeyExcluded(key) {
			continue
		}

		if strings.Contains(rawLine, "PROXY_HTTP_PORT") && strings.HasPrefix(strings.TrimSpace(rawLine), "#") {
			grouped["DEFAULTS"]["VARNISH"] = false
			continue
		} else if key == "PROXY_HTTP_PORT" {
			grouped["DEFAULTS"]["VARNISH"] = true
			continue
		}

		switch key {
		case "WEB_SERVER":
			grouped["DEFAULTS"]["WEB_SERVER"] = value
			continue
		case "DEFAULT_PHP_VERSION":
			grouped["DEFAULTS"]["PHP_VERSION"] = value
			continue
		case "MYSQL_TYPE":
			grouped["DEFAULTS"]["MYSQL_TYPE"] = value
			continue
		}

		if m := phpFPMKeyRe.FindStringSubmatch(key); m != nil {
			version := m[1] + "." + m[2]
			subkey := m[3]
			if grouped["PHP_FPM"] == nil {
				grouped["PHP_FPM"] = map[string]interface{}{}
			}
			sub, _ := grouped["PHP_FPM"][version].(map[string]interface{})
			if sub == nil {
				sub = map[string]interface{}{}
			}
			sub[subkey] = value
			grouped["PHP_FPM"][version] = sub
			continue
		}

		prefix, suffix := key, ""
		if idx := strings.Index(key, "_"); idx != -1 {
			prefix, suffix = key[:idx], key[idx+1:]
		}
		if grouped[prefix] == nil {
			grouped[prefix] = map[string]interface{}{}
		}
		grouped[prefix][suffix] = value
	}

	return grouped
}

// --- PHP version watch (api.openpanel.com) ---

type phpVersionStatus struct {
	StatusLabel          string `json:"statusLabel"`
	IsEOLVersion         bool   `json:"isEOLVersion"`
	IsSecureVersion      bool   `json:"isSecureVersion"`
	IsLatestVersion      bool   `json:"isLatestVersion"`
	IsFutureVersion      bool   `json:"isFutureVersion"`
	IsNextVersion        bool   `json:"isNextVersion"`
	ReleaseDate          string `json:"releaseDate"`
	ActiveSupportEndDate string `json:"activeSupportEndDate"`
	EOLDate              string `json:"eolDate"`
}

// defaultsPHPWatchRun is injectable so tests never make a real HTTP call.
// Only a *timeout* is treated as non-fatal here (falls back to an empty
// map); any other transport error (DNS failure, connection refused, ...)
// is returned uncaught, letting the caller surface a 500 -- that split is
// intentional, not an oversight.
var defaultsPHPWatchRun = func() (map[string]phpVersionStatus, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("https://api.openpanel.com/php-versions/")
	if err != nil {
		if netErr, ok := err.(interface{ Timeout() bool }); ok && netErr.Timeout() {
			return map[string]phpVersionStatus{}, nil
		}
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return map[string]phpVersionStatus{}, nil
	}

	var parsed struct {
		Data map[string]struct {
			Name                 string `json:"name"`
			StatusLabel          string `json:"statusLabel"`
			IsEOLVersion         bool   `json:"isEOLVersion"`
			IsSecureVersion      bool   `json:"isSecureVersion"`
			IsLatestVersion      bool   `json:"isLatestVersion"`
			IsFutureVersion      bool   `json:"isFutureVersion"`
			IsNextVersion        bool   `json:"isNextVersion"`
			ReleaseDate          string `json:"releaseDate"`
			ActiveSupportEndDate string `json:"activeSupportEndDate"`
			EOLDate              string `json:"eolDate"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	out := make(map[string]phpVersionStatus, len(parsed.Data))
	for _, v := range parsed.Data {
		out[v.Name] = phpVersionStatus{
			StatusLabel: v.StatusLabel, IsEOLVersion: v.IsEOLVersion, IsSecureVersion: v.IsSecureVersion,
			IsLatestVersion: v.IsLatestVersion, IsFutureVersion: v.IsFutureVersion, IsNextVersion: v.IsNextVersion,
			ReleaseDate: v.ReleaseDate, ActiveSupportEndDate: v.ActiveSupportEndDate, EOLDate: v.EOLDate,
		}
	}
	return out, nil
}

// --- normalize_ram ---

var (
	defaultsNumericRe     = regexp.MustCompile(`^\d+(\.\d+)?$`)
	defaultsRAMWithUnitRe = regexp.MustCompile(`(?i)^\d+(\.\d+)?[kmg]$`)
)

func normalizeRAM(value string) string {
	v := strings.TrimSpace(value)
	v = strings.Trim(v, `"`)
	v = strings.Trim(v, `'`)
	v = strings.ToUpper(v)

	if v == "" || v == "0" || v == "0.0" {
		return v
	}
	if defaultsRAMWithUnitRe.MatchString(v) {
		return v
	}
	if defaultsNumericRe.MatchString(v) {
		return v + "G"
	}
	return v
}

// --- available/active autostart services ---

var defaultsServiceLineRe = regexp.MustCompile(`^  ([a-zA-Z][a-zA-Z0-9_.-]*):`)

// getAvailableServices returns an empty (non-nil) slice rather than nil so
// JSON output is `[]`, not `null`.
func getAvailableServices() []string {
	services := []string{}
	raw, err := os.ReadFile(DefaultsComposeFilePath)
	if err != nil {
		return services
	}
	inServices := false
	for _, line := range strings.Split(string(raw), "\n") {
		stripped := strings.TrimRight(line, " \t\r")
		if stripped == "services:" {
			inServices = true
			continue
		}
		if inServices {
			if stripped != "" && !strings.HasPrefix(stripped, " ") {
				break
			}
			if m := defaultsServiceLineRe.FindStringSubmatch(stripped); m != nil && m[1] != "docker-proxy" {
				services = append(services, m[1])
			}
		}
	}
	return services
}

// getActiveServices reads the currently configured autostart services list.
func getActiveServices() []string {
	active := []string{}
	raw, err := os.ReadFile(DefaultsAutostartServicesPath)
	if err != nil {
		return active
	}
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			active = append(active, trimmed)
		}
	}
	return active
}

// --- template view builders for settings_defaults.html ---

type defaultsFieldView struct {
	FieldName string
	Label     string
	Value     string
	Required  bool
}

type defaultsGroupView struct {
	Name     string
	IsPHPFPM bool
	Version  string
	Status   *phpVersionStatus
	Rows     []defaultsFieldView
}

type phpVersionOption struct {
	Version  string
	Label    string
	Selected bool
}

// buildDefaultsFields builds the field rows shared by the PHP_FPM and
// "other groups" sections. Go maps don't preserve insertion order, and
// that order isn't meaningful for display anyway, so subkeys are sorted
// alphabetically for deterministic output -- the same approach already
// used by buildLimitsView (services_limits.go).
func buildDefaultsFields(prefix string, items map[string]interface{}, isPHPFPM bool) []defaultsFieldView {
	keys := make([]string, 0, len(items))
	for k := range items {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	rows := make([]defaultsFieldView, 0, len(keys))
	for _, k := range keys {
		required := true
		if !isPHPFPM && prefix == "PGADMIN" && k == "MAIL" {
			required = false
		}
		rows = append(rows, defaultsFieldView{
			FieldName: prefix + "_" + k,
			Label:     k,
			Value:     fmt.Sprintf("%v", items[k]),
			Required:  required,
		})
	}
	return rows
}

// buildPHPFPMGroups groups PHP-FPM settings by version, newest first.
func buildPHPFPMGroups(phpFPM map[string]interface{}, phpVersionsData map[string]phpVersionStatus) []defaultsGroupView {
	versions := make([]string, 0, len(phpFPM))
	for v := range phpFPM {
		versions = append(versions, v)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(versions)))

	groups := make([]defaultsGroupView, 0, len(versions))
	for _, v := range versions {
		items, _ := phpFPM[v].(map[string]interface{})
		prefix := "PHP_FPM_" + strings.ReplaceAll(v, ".", "_")
		var status *phpVersionStatus
		if s, ok := phpVersionsData[v]; ok {
			s := s
			status = &s
		}
		groups = append(groups, defaultsGroupView{
			Name: v, IsPHPFPM: true, Version: v, Status: status,
			Rows: buildDefaultsFields(prefix, items, true),
		})
	}
	return groups
}

// buildOtherDefaultsGroups builds the remaining groups, excluding
// DEFAULTS/PHP_FPM. No meaningful order exists for these config-derived
// groups, so group names are sorted alphabetically -- same approach as
// buildLimitsView.
func buildOtherDefaultsGroups(defaults map[string]map[string]interface{}) []defaultsGroupView {
	keys := make([]string, 0, len(defaults))
	for k := range defaults {
		if k == "DEFAULTS" || k == "PHP_FPM" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	groups := make([]defaultsGroupView, 0, len(keys))
	for _, k := range keys {
		groups = append(groups, defaultsGroupView{
			Name: k, Rows: buildDefaultsFields(k, defaults[k], false),
		})
	}
	return groups
}

// defaultsPHPVersionList is the hardcoded version list shown in the
// DEFAULT_PHP_VERSION <select>.
var defaultsPHPVersionList = []string{"8.5", "8.4", "8.3", "8.2", "8.1", "8.0", "7.4", "7.3", "7.2", "7.1", "7.0", "5.6"}

// buildPHPVersionOptions precomputes each <option>'s display label
// (version + status label + "Current Default" suffix) so the template
// doesn't need to do string composition.
func buildPHPVersionOptions(current string, phpVersionsData map[string]phpVersionStatus) []phpVersionOption {
	options := make([]phpVersionOption, 0, len(defaultsPHPVersionList))
	for _, v := range defaultsPHPVersionList {
		label := v
		if status, ok := phpVersionsData[v]; ok {
			label += " (" + status.StatusLabel + ")"
		}
		selected := current == v
		if selected {
			label += " - Current Default"
		}
		options = append(options, phpVersionOption{Version: v, Label: label, Selected: selected})
	}
	return options
}

func dedupeSorted(items []string) []string {
	set := map[string]bool{}
	for _, i := range items {
		set[i] = true
	}
	out := make([]string, 0, len(set))
	for i := range set {
		out = append(out, i)
	}
	sort.Strings(out)
	return out
}

// --- /settings/defaults ---

// ServeDefaults handles GET/POST /settings/defaults.
func (d *Defaults) ServeDefaults(w http.ResponseWriter, r *http.Request) {
	availableServices := getAvailableServices()

	if r.Method == http.MethodPost {
		r.ParseForm()

		raw, err := os.ReadFile(DefaultsEnvPath)
		if err != nil {
			auth.AddFlash(w, r, d.Sessions, "Environment file not found.", "error")
			http.Redirect(w, r, r.URL.String(), http.StatusSeeOther)
			return
		}

		varnishEnabled := strings.TrimSpace(r.PostFormValue("VARNISH")) == "1"

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

			if vals, ok := r.PostForm[key]; ok {
				newValue := strings.TrimSpace(vals[0])
				if strings.HasSuffix(key, "_RAM") {
					newValue = normalizeRAM(newValue)
				}
				newValue = strings.Trim(newValue, `"`)
				newValue = strings.Trim(newValue, `'`)
				newLines = append(newLines, key+`="`+newValue+"\"\n")
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
			auth.AddFlash(w, r, d.Sessions, "Failed to update defaults: "+err.Error(), "error")
		} else {
			auth.AddFlash(w, r, d.Sessions, "New defaults saved successfully!", "success")
		}

		// added in 1.7.58 to allow admin to set autostart services
		rawServices := r.PostFormValue("services")
		var selected []string
		for _, s := range strings.Split(rawServices, ",") {
			if s = strings.TrimSpace(s); s != "" {
				selected = append(selected, s)
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

	defaults := readDefaultsEnvGroups()
	phpVersionsData, err := defaultsPHPWatchRun()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	activeServices := getActiveServices()

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, map[string]interface{}{
			"defaults":                     defaults,
			"php_versions_data":            phpVersionsData,
			"autostart_available_services": availableServices,
			"autostart_active_services":    activeServices,
		})
		return
	}

	var webServer, mysqlType, phpVersion string
	var varnishEnabled bool
	var phpFPM map[string]interface{}
	if defaults != nil {
		if defGroup := defaults["DEFAULTS"]; defGroup != nil {
			webServer, _ = defGroup["WEB_SERVER"].(string)
			mysqlType, _ = defGroup["MYSQL_TYPE"].(string)
			phpVersion, _ = defGroup["PHP_VERSION"].(string)
			varnishEnabled, _ = defGroup["VARNISH"].(bool)
		}
		phpFPM = defaults["PHP_FPM"]
	}

	activeJSON, _ := json.Marshal(activeServices)

	webtemplates.Render(w, "settings_defaults.html", mergeChrome(map[string]interface{}{
		"WebServer":          webServer,
		"MySQLType":          mysqlType,
		"VarnishEnabled":     varnishEnabled,
		"PHPVersionOptions":  buildPHPVersionOptions(phpVersion, phpVersionsData),
		"AvailableServices":  availableServices,
		"ActiveServicesJSON": string(activeJSON),
		"PHPFPMGroups":       buildPHPFPMGroups(phpFPM, phpVersionsData),
		"OtherGroups":        buildOtherDefaultsGroups(defaults),
		"CSRFToken":          csrf.Token(r),
		"Flashes":            auth.PopFlashes(w, r, d.Sessions),
	}, r, "Edit defaults"))
}

// --- /settings/defaults/files ---

var defaultsSupportFiles = map[string]string{
	"/etc/openpanel/mysql/user.cnf":        "custom.cnf",
	"/etc/openpanel/nginx/user-nginx.conf": "nginx.conf",
	"/etc/openpanel/openresty/nginx.conf":  "openresty.conf",
	"/etc/openpanel/apache/httpd.conf":     "httpd.conf",
	"/etc/openpanel/varnish/default.vcl":   "default.vcl",
	"/etc/openpanel/ofelia/users.ini":      "crons.ini",
	"/etc/openpanel/backups/backup.env":    "backup.env",
	"/etc/openpanel/php/ini":               "php.ini",
}

// defaultsComposeConfigRun is injectable so tests never shell out to a real
// podman-compose binary.
var defaultsComposeConfigRun = func(composePath, tmpDir string) (stdout, stderr string, exitCode int, err error) {
	cmd, cmdErr := podman.ComposeCommand("default", "-f", composePath, "config")
	if cmdErr != nil {
		return "", "", 0, cmdErr
	}
	cmd.Dir = tmpDir
	cmd.Env = append(cmd.Env, "COMPOSE_FILE="+composePath)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			return outBuf.String(), errBuf.String(), exitErr.ExitCode(), nil
		}
		return outBuf.String(), errBuf.String(), 0, runErr
	}
	return outBuf.String(), errBuf.String(), 0, nil
}

// defaultsFetchRemoteRun is injectable so tests never make a real HTTP call.
var defaultsFetchRemoteRun = func(url string) (body string, status int, err error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, err
	}
	return string(raw), resp.StatusCode, nil
}

// ServeDefaultsFiles handles GET/POST/PUT/DELETE /settings/defaults/files.
func (d *Defaults) ServeDefaultsFiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		r.ParseForm()
		if formHasKey(r, "env") {
			os.WriteFile(DefaultsEnvPath, []byte(r.PostFormValue("env")), 0644)
		}
		if formHasKey(r, "compose") {
			os.WriteFile(DefaultsComposeFilePath, []byte(r.PostFormValue("compose")), 0644)
		}
		auth.AddFlash(w, r, d.Sessions, "Files updated successfully!", "success")

	case http.MethodPut:
		d.handleDefaultsFilesPreview(w, r)
		return

	case http.MethodDelete:
		d.handleDefaultsFilesReset(w, r)
	}

	envContent, _ := readFileOrEmpty(DefaultsEnvPath)
	composeContent, _ := readFileOrEmpty(DefaultsComposeFilePath)
	fileContents := map[string]string{"env": envContent, "compose": composeContent}

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, fileContents)
		return
	}

	webtemplates.Render(w, "settings_defaults_templates.html", mergeChrome(map[string]interface{}{
		"Env":     fileContents["env"],
		"Compose": fileContents["compose"],
		"Flashes": auth.PopFlashes(w, r, d.Sessions),
	}, r, "Edit Defaults"))
}

func (d *Defaults) handleDefaultsFilesPreview(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	if err := os.MkdirAll(DefaultsTmpDir, 0755); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	envPath := filepath.Join(DefaultsTmpDir, ".env")
	composePath := filepath.Join(DefaultsTmpDir, "docker-compose.yml")

	if formHasKey(r, "env") {
		if err := os.WriteFile(envPath, []byte(r.PostFormValue("env")), 0644); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if formHasKey(r, "compose") {
		if err := os.WriteFile(composePath, []byte(r.PostFormValue("compose")), 0644); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	for src, dst := range defaultsSupportFiles {
		info, statErr := os.Stat(src)
		if statErr != nil {
			continue // a missing source file is a silent no-op
		}
		dstPath := filepath.Join(DefaultsTmpDir, dst)
		if info.IsDir() {
			_ = exec.Command("cp", "-r", src, dstPath).Run()
		} else {
			_ = exec.Command("cp", src, dstPath).Run()
		}
	}

	for _, sub := range []string{"mysqld", "postgres", "redis", "memcached"} {
		os.MkdirAll(filepath.Join(DefaultsTmpDir, "sockets", sub), 0755)
	}

	stdout, stderr, exitCode, err := defaultsComposeConfigRun(composePath, DefaultsTmpDir)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	status := http.StatusOK
	if exitCode != 0 {
		status = http.StatusBadRequest
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   exitCode == 0,
		"stdout":    stdout,
		"stderr":    stderr,
		"exit_code": exitCode,
	})
}

func (d *Defaults) handleDefaultsFilesReset(w http.ResponseWriter, r *http.Request) {
	allSuccess := true
	for _, item := range []struct{ key, url, path string }{
		{"compose", DefaultsRemoteComposeURL, DefaultsComposeFilePath},
		{"env", DefaultsRemoteEnvURL, DefaultsEnvPath},
	} {
		body, status, err := defaultsFetchRemoteRun(item.url)
		if err != nil {
			auth.AddFlash(w, r, d.Sessions, "Failed to update defaults: "+err.Error(), "error")
			return
		}
		if status == http.StatusOK {
			os.WriteFile(item.path, []byte(body), 0644)
		} else {
			allSuccess = false
			auth.AddFlash(w, r, d.Sessions, fmt.Sprintf("Failed to fetch %s file from Github. Status code: %d", item.key, status), "error")
		}
	}
	if allSuccess {
		auth.AddFlash(w, r, d.Sessions, "Defaults reset successfully from remote source!", "success")
	}
}

// --- /settings/defaults/files/{username} ---

func queryContextByUsername(db *sql.DB, username string) (string, error) {
	if db == nil {
		return "", fmt.Errorf("database unavailable")
	}
	var context string
	err := db.QueryRow("SELECT server FROM users WHERE username = ?", username).Scan(&context)
	return context, err
}

// ServeUserFiles handles GET/POST /settings/defaults/files/{username}.
func (d *Defaults) ServeUserFiles(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	context, _ := queryContextByUsername(d.MySQL, username)

	envPath := "/home/" + context + "/.env"
	composePath := "/home/" + context + "/docker-compose.yml"

	if r.Method == http.MethodPost {
		r.ParseForm()
		if formHasKey(r, "env") {
			os.WriteFile(envPath, []byte(r.PostFormValue("env")), 0644)
		}
		if formHasKey(r, "compose") {
			os.WriteFile(composePath, []byte(r.PostFormValue("compose")), 0644)
		}
	}

	envContent, _ := readFileOrEmpty(envPath)
	composeContent, _ := readFileOrEmpty(composePath)
	writeJSON(w, map[string]string{"env": envContent, "compose": composeContent})
}
