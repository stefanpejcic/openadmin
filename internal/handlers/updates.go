// This file implements the OpenPanel Docker image version rollback/update
// flow, auto-update preference toggle, and the update-log listing feed for
// the updates settings page.
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/config"
	"openadmin/internal/podman"
	"openadmin/internal/webtemplates"
)

// Updates bundles the /settings/updates*, /api/docker-tags handlers.
type Updates struct {
	Sessions *auth.Manager
	// PanelVersion is computed once at process startup (see
	// openpanelVersion() in main.go) -- not re-read per request.
	PanelVersion string
}

// UpdatesConfigFilePath / UpdatesEnvPath / UpdatesLogDir are the hardcoded
// paths used by the updates settings feature.
var (
	UpdatesConfigFilePath = "/etc/openpanel/openpanel/conf/openpanel.config"
	UpdatesEnvPath        = "/root/.env"
	UpdatesLogDir         = "/var/log/openpanel/updates/"
)

var updatesPreferenceMap = map[string]map[string]string{
	"minor_and_major": {"autoupdate": "on", "autopatch": "on"},
	"minor_only":      {"autoupdate": "off", "autopatch": "on"},
	"major_only":      {"autoupdate": "on", "autopatch": "off"},
	"none":            {"autoupdate": "off", "autopatch": "off"},
}

var dockerTagVersionRe = regexp.MustCompile(`^(\d{1,3}|\d{1,2}(\.\d{1,2}){1,2})$`)

// updatesFetchTagsRun / updatesDockerHubTagsRun / updatesFallbackVersionRun /
// updatesPullImageRun / updatesComposeUpRun / updatesUpdateNowRun are
// injectable so tests never make real network calls or shell out to real
// binaries, matching the caddyFetchMetrics/localesFetchFolders pattern.
var updatesFetchTagsRun = func() ([]string, error) {
	return fetchTagNames("https://hub.docker.com/v2/repositories/openpanel/openpanel/tags?page_size=100")
}

var updatesDockerHubTagsRun = func() ([]string, error) {
	return fetchTagNames("https://hub.docker.com/v2/repositories/openpanel/openpanel/tags")
}

func fetchTagNames(url string) ([]string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	var parsed struct {
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(parsed.Results))
	for _, t := range parsed.Results {
		names = append(names, t.Name)
	}
	return names, nil
}

var updatesFallbackVersionRun = func() (string, error) {
	resp, err := http.Get("https://usage-api.openpanel.org/latest_version")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	var parsed struct {
		LatestVersion string `json:"latest_version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	return parsed.LatestVersion, nil
}

var updatesPullImageRun = func(version string) error {
	cmd, err := podman.Command("default", "pull", "openpanel/openpanel:"+version)
	if err != nil {
		return err
	}
	cmd.Dir = "/root"
	return cmd.Run()
}

var updatesComposeUpRun = func() error {
	cmd, err := podman.ComposeCommand("default", "up", "-d", "openpanel")
	if err != nil {
		return err
	}
	cmd.Dir = "/root"
	return cmd.Run()
}

var updatesUpdateNowRun = func() error {
	return exec.Command("timeout", "600s", "opencli", "update", "--force").Start()
}

// fetchDockerTags does a plain lexicographic string sort descending (not
// numeric) -- distinct from getLatestVersion's numeric comparison below.
// The two functions are intentionally inconsistent: this one just lists
// tags for a dropdown, while getLatestVersion needs an actual version
// ordering.
func fetchDockerTags() ([]string, error) {
	names, err := updatesFetchTagsRun()
	if err != nil {
		return nil, err
	}
	filtered := make([]string, 0, len(names))
	for _, n := range names {
		if n != "latest" {
			filtered = append(filtered, n)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(filtered)))
	return filtered, nil
}

func isVersionLikeTag(name string) bool {
	if name == "latest" || name == "" {
		return false
	}
	stripped := strings.ReplaceAll(name, ".", "")
	if stripped == "" {
		return false
	}
	for _, r := range stripped {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func compareVersionTags(a, b string) int {
	pa := strings.Split(a, ".")
	pb := strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x - y
		}
	}
	return 0
}

// getLatestVersion checks Docker Hub first (numeric version-tag sort,
// distinct from fetchDockerTags' string sort above), falling back to the
// usage API, then finally "0.0.0".
func getLatestVersion() string {
	if tags, err := updatesDockerHubTagsRun(); err == nil {
		versions := make([]string, 0, len(tags))
		for _, t := range tags {
			if isVersionLikeTag(t) {
				versions = append(versions, t)
			}
		}
		if len(versions) > 0 {
			sort.Slice(versions, func(i, j int) bool { return compareVersionTags(versions[i], versions[j]) < 0 })
			return versions[len(versions)-1]
		}
	}

	if v, err := updatesFallbackVersionRun(); err == nil {
		return v
	}
	return "0.0.0"
}

type updateLogEntry struct {
	File                   string `json:"file"`
	LogDir                 string `json:"log_dir"`
	Timestamp              int64  `json:"timestamp"`
	HumanReadableTimestamp string `json:"human_readable_timestamp"`
}

// getOpUpdateLogs lists the update log files. Timestamps are truncated to
// whole seconds (time.Time.Unix()) -- not behaviorally significant for a
// sort/display feature.
func getOpUpdateLogs() []updateLogEntry {
	entries, err := os.ReadDir(UpdatesLogDir)
	if err != nil {
		return nil
	}
	logs := make([]updateLogEntry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		logs = append(logs, updateLogEntry{
			File:                   e.Name(),
			LogDir:                 UpdatesLogDir,
			Timestamp:              info.ModTime().Unix(),
			HumanReadableTimestamp: info.ModTime().Format("2006-01-02 15:04:05"),
		})
	}
	sort.Slice(logs, func(i, j int) bool { return logs[i].Timestamp > logs[j].Timestamp })
	return logs
}

// ServeDockerTags handles GET/POST /api/docker-tags.
func (u *Updates) ServeDockerTags(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tags, err := fetchDockerTags()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, tags)

	case http.MethodPost:
		u.handleDockerTagsPost(w, r)
	}
}

func (u *Updates) handleDockerTagsPost(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	var version string
	if len(r.PostForm) > 0 {
		version = r.PostFormValue("version")
	} else {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		version = body["version"]
	}

	fail := func(message string) {
		auth.AddFlash(w, r, u.Sessions, message, "error")
		http.Redirect(w, r, "/settings/updates", http.StatusSeeOther)
	}

	if version == "" {
		fail("Version not provided")
		return
	}
	if !dockerTagVersionRe.MatchString(version) {
		fail("Invalid version format provided. Use NNN or N.N or N.N.N")
		return
	}

	if err := writeEnvVersion(version); err != nil {
		fail(err.Error())
		return
	}

	if err := updatesPullImageRun(version); err != nil {
		fail("Command failed: " + err.Error())
		return
	}
	if err := updatesComposeUpRun(); err != nil {
		fail("Command failed: " + err.Error())
		return
	}

	auth.AddFlash(w, r, u.Sessions, fmt.Sprintf("Downgraded to version %s successfully.", version), "success")
	http.Redirect(w, r, "/settings/updates", http.StatusSeeOther)
}

// writeEnvVersion rewrites the VERSION= line in the .env file: replace
// the existing line if present, append one if not, create the file fresh
// if it doesn't exist at all.
func writeEnvVersion(version string) error {
	raw, err := os.ReadFile(UpdatesEnvPath)
	if err != nil {
		if os.IsNotExist(err) {
			return os.WriteFile(UpdatesEnvPath, []byte(`VERSION="`+version+"\"\n"), 0644)
		}
		return err
	}

	lines := strings.SplitAfter(string(raw), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	updated := false
	for i, line := range lines {
		if strings.HasPrefix(line, "VERSION=") {
			lines[i] = `VERSION="` + version + "\"\n"
			updated = true
		}
	}
	if !updated {
		lines = append(lines, `VERSION="`+version+"\"\n")
	}
	return os.WriteFile(UpdatesEnvPath, []byte(strings.Join(lines, "")), 0644)
}

// ServeUpdateNow handles POST /settings/updates/update_now.
func (u *Updates) ServeUpdateNow(w http.ResponseWriter, r *http.Request) {
	if err := updatesUpdateNowRun(); err != nil {
		auth.AddFlash(w, r, u.Sessions, "Error: Failed to start the update process. Details: "+err.Error(), "error")
	} else {
		auth.AddFlash(w, r, u.Sessions, "Update process started successfully.", "info")
	}
	http.Redirect(w, r, "/settings/updates", http.StatusSeeOther)
}

// ServeUpdates handles GET/POST /settings/updates.
func (u *Updates) ServeUpdates(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		preference := r.PostFormValue("preference")

		// An unrecognized/missing preference value has no entry in
		// updatesPreferenceMap, so it's reported gracefully instead of
		// panicking on a nil map lookup downstream.
		updates, ok := updatesPreferenceMap[preference]
		if !ok {
			auth.AddFlash(w, r, u.Sessions, "Invalid update preference selected.", "error")
		} else {
			raw, err := os.ReadFile(UpdatesConfigFilePath)
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			content := string(raw)
			for key, value := range updates {
				content = strings.ReplaceAll(content, key+"=on", key+"="+value)
				content = strings.ReplaceAll(content, key+"=off", key+"="+value)
			}
			if err := os.WriteFile(UpdatesConfigFilePath, []byte(content), 0644); err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			auth.AddFlash(w, r, u.Sessions, "Update preferences saved successfully.", "success")
		}
	}

	// Read fresh (not through config.Openpanel()'s process-lifetime cache)
	// so a just-saved preference change is reflected immediately.
	configData := config.Load(UpdatesConfigFilePath)
	latestVersion := getLatestVersion()
	updateLogs := getOpUpdateLogs()

	webtemplates.Render(w, "settings_updates.html", mergeChrome(map[string]interface{}{
		"PanelVersion":  u.PanelVersion,
		"LatestVersion": latestVersion,
		"Autoupdate":    configData.Get("PANEL", "autoupdate", ""),
		"Autopatch":     configData.Get("PANEL", "autopatch", ""),
		"UpdateLogs":    updateLogs,
		// A naive lexicographic STRING comparison here, instead of a
		// semantic version comparison (e.g. "9.0" > "10.0" as strings), could
		// hide or show the "Update Now" button incorrectly around version 10+,
		// so this uses the same numeric per-component comparison as
		// getLatestVersion.
		"ShowUpdateNow": compareVersionTags(latestVersion, u.PanelVersion) > 0,
		"CSRFToken":     csrf.Token(r),
		"Flashes":       auth.PopFlashes(w, r, u.Sessions),
	}, r, "Update Settings"))
}
