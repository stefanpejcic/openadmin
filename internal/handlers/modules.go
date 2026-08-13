// This file implements the enable/disable module checklist, docker-compose
// service toggling side effects, and the third-party plugin listing.
package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/config"
	"openadmin/internal/podman"
	"openadmin/internal/webtemplates"
)

// Modules bundles the /settings/modules handler.
type Modules struct {
	Sessions *auth.Manager
}

// ModulesConfigFilePath / ModulesFeaturesJSONPath / ModulesDockerComposePath /
// ModulesPluginsBaseDir / ModulesRndcKeyPath / ModulesOpenpanelRestartFlagPath
// are kept as separate vars here rather than sharing one with
// general.go/limits.go, since each is used independently.
var (
	ModulesConfigFilePath           = "/etc/openpanel/openpanel/conf/openpanel.config"
	ModulesFeaturesJSONPath         = "/etc/openpanel/openadmin/config/features.json"
	ModulesDockerComposePath        = "/root/docker-compose.yml"
	ModulesPluginsBaseDir           = "/etc/openpanel/modules/"
	ModulesRndcKeyPath              = "/etc/bind/rndc.key"
	ModulesOpenpanelRestartFlagPath = "/root/openpanel_restart_needed"
)

// modulesRndcGenRun is injectable so tests never shell out to a real
// podman binary. Runs fire-and-forget, with stdout/stderr discarded.
var modulesRndcGenRun = func() {
	cmd, err := podman.Command("default", "run", "--rm", "-v", "/etc/bind/:/etc/bind/",
		"--entrypoint=/bin/sh", "ubuntu/bind9:latest", "-c",
		"rndc-confgen -a -A hmac-sha256 -b 256 -c /etc/bind/rndc.key")
	if err != nil {
		return
	}
	_ = cmd.Start() // fire-and-forget; we don't wait for or track this process
}

// ensureNotificationServiceMonitored adds serviceName to notifications.ini's
// [DEFAULT] services= list (via the same opencli path the notifications
// settings page uses) if it isn't already present.
func ensureNotificationServiceMonitored(serviceName string) {
	notifConfig := config.Load(NotificationsConfigPath)
	current := notifConfig.Get("DEFAULT", "services", "")
	for _, s := range strings.Split(current, ",") {
		if strings.TrimSpace(s) == serviceName {
			return
		}
	}
	newValue := serviceName
	if trimmed := strings.TrimSpace(current); trimmed != "" {
		newValue = trimmed + "," + serviceName
	}
	runOpenCLINotificationUpdate("services", newValue)
}

// updateServiceInDockerCompose comments/uncomments a `- service_name`
// docker-compose line in place.
func updateServiceInDockerCompose(serviceName string, enable bool) {
	raw, err := os.ReadFile(ModulesDockerComposePath)
	if err != nil {
		return
	}

	escaped := regexp.QuoteMeta(serviceName)
	var pattern *regexp.Regexp
	var replacement string
	if enable {
		pattern = regexp.MustCompile(`(?m)^(\s*)#\s*-\s*` + escaped)
		replacement = "${1}- " + serviceName
	} else {
		pattern = regexp.MustCompile(`(?m)^(\s*)-\s*` + escaped)
		replacement = "${1}# - " + serviceName
	}

	newContent := pattern.ReplaceAllString(string(raw), replacement)
	os.WriteFile(ModulesDockerComposePath, []byte(newContent), 0644)
}

func modulesEnabledList(configPath string) []string {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "enabled_modules=") {
			continue
		}
		value := strings.TrimPrefix(trimmed, "enabled_modules=")
		value = strings.Trim(value, `"`)
		if value == "" {
			return nil
		}
		return strings.Split(value, ",")
	}
	return nil
}

// parsePluginReadme parses simple key=value lines from a plugin readme file.
func parsePluginReadme(path string) map[string]string {
	meta := map[string]string{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return meta
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if idx := strings.Index(line, "="); idx != -1 {
			meta[strings.TrimSpace(line[:idx])] = strings.TrimSpace(line[idx+1:])
		}
	}
	return meta
}

// getAllPlugins enumerates plugin directories under baseDir. os.ReadDir
// sorts entries by filename, giving deterministic output; order doesn't
// affect which plugins are found, only the order they're listed in.
func getAllPlugins(baseDir string) []map[string]string {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil
	}
	var plugins []map[string]string
	for _, e := range entries {
		folderPath := filepath.Join(baseDir, e.Name())
		info, err := os.Stat(folderPath)
		if err != nil || !info.IsDir() {
			continue
		}
		readmePath := filepath.Join(folderPath, "readme.txt")
		if _, err := os.Stat(readmePath); err != nil {
			continue
		}
		meta := parsePluginReadme(readmePath)
		meta["folder"] = e.Name()
		plugins = append(plugins, meta)
	}
	return plugins
}

// ServeModules handles GET/POST /settings/modules.
func (m *Modules) ServeModules(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()

		// r.PostForm is an unordered map, so keys are sorted here for
		// determinism. Order doesn't affect membership checks below or
		// the resulting comma-list's semantics, so this isn't
		// behaviorally significant.
		keys := make([]string, 0, len(r.PostForm))
		for k := range r.PostForm {
			if k == "csrf_token" {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		enabledModulesValue := strings.Join(keys, ",")

		raw, err := os.ReadFile(ModulesConfigFilePath)
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		lines := strings.SplitAfter(string(raw), "\n")
		if n := len(lines); n > 0 && lines[n-1] == "" {
			lines = lines[:n-1]
		}
		for i, line := range lines {
			if strings.HasPrefix(line, "enabled_modules=") {
				lines[i] = `enabled_modules="` + enabledModulesValue + "\"\n"
			}
		}
		if err := os.WriteFile(ModulesConfigFilePath, []byte(strings.Join(lines, "")), 0644); err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		// added in 1.7.60 to autostart phpmyadmin when enabled module
		updateServiceInDockerCompose("phpmyadmin", strings.Contains(enabledModulesValue, "phpmyadmin"))
		if strings.Contains(enabledModulesValue, "malware_scan") {
			updateServiceInDockerCompose("clamav", true)
		} else {
			updateServiceInDockerCompose("clamav", false)
		}

		// added in 1.7.61 to generate rndc.key when dns module is enabled --
		// this is a raw substring check against the whole comma-joined
		// value (not a proper membership check like the one used just
		// below for rendering feature status), so it would also trigger
		// for any hypothetical enabled module whose name merely contains
		// "dns" as a substring. This is intentional, not a bug -- kept as a
		// simple substring check rather than a proper membership check.
		if strings.Contains(enabledModulesValue, "dns") {
			if _, err := os.Stat(ModulesRndcKeyPath); os.IsNotExist(err) {
				modulesRndcGenRun()
			}
			ensureNotificationServiceMonitored("named")
		}

		os.WriteFile(ModulesOpenpanelRestartFlagPath, []byte("Restart needed for OpenPanel service."), 0644)
	}

	enabledModules := modulesEnabledList(ModulesConfigFilePath)
	enabledSet := make(map[string]bool, len(enabledModules))
	for _, name := range enabledModules {
		enabledSet[name] = true
	}

	rawFeatures, err := os.ReadFile(ModulesFeaturesJSONPath)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	var allFeatures []map[string]interface{}
	if err := json.Unmarshal(rawFeatures, &allFeatures); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	for _, feature := range allFeatures {
		name, _ := feature["name"].(string)
		feature["status"] = enabledSet[name]
	}

	plugins := getAllPlugins(ModulesPluginsBaseDir)

	if r.Method == http.MethodGet && r.URL.Query().Get("output") == "json" {
		writeJSON(w, map[string]interface{}{"features": allFeatures, "plugins": plugins})
		return
	}

	webtemplates.Render(w, "settings_modules.html", mergeChrome(map[string]interface{}{
		"Features":  allFeatures,
		"Plugins":   plugins,
		"CSRFToken": csrf.Token(r),
		"Flashes":   auth.PopFlashes(w, r, m.Sessions),
	}, r, "Manage Modules"))
}
