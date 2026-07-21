// This file implements service status (docker + systemd), start/stop/
// restart control, and the monitored-services config editor.
// Deliberately out of scope for this pass (see the migration backlog):
// per-service version-string CLI probes and per-service docker-compose
// port lookups -- both are display-only, non-actionable detail, and each
// service's unique parsing heuristic is its own dedicated, long-tail
// piece of work.
package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/bootstrap"
	"openadmin/internal/paneldb"
	"openadmin/internal/podman"
	"openadmin/internal/webtemplates"
)

// Services bundles the /services and /service/{action}/{name} handlers.
type Services struct {
	MySQL    *sql.DB
	Sessions *auth.Manager
}

var ServicesConfigPath = "/etc/openpanel/openadmin/config/services.json"

// controlServiceRun/manageServiceComposeRun/manageServiceSystemRun are vars
// (not direct calls) so tests can stub out the actual service-mutating
// commands -- consistent with the pattern used for other real,
// system-mutating actions elsewhere in this package (dropCacheRun,
// timedatectlRun, ...). Read-only status checks (podman ps, systemctl
// is-active) are left as real calls since they're harmless even when they
// hit whatever podman/systemd state actually exists on the host running
// the tests.
var controlServiceRun = realControlService

func realControlService(serviceName, serviceType, action string) (bool, string) {
	if serviceName == "admin" && action == "restart" {
		// Cleared here rather than left to the new process's own startup
		// (bootstrap.RemoveRestartFlag): the restart happens in a detached
		// background command below, and the UI polls /services/admin/status
		// and reloads as soon as *something* answers on the port again --
		// waiting on the next process to reach that startup line is one
		// more race than this needs.
		os.Remove(bootstrap.RestartFlagPath)
		exec.Command("bash", "-c", "sleep 2 && systemctl restart admin").Start()
		return true, "OpenAdmin restart scheduled"
	}

	if serviceType != "docker" {
		cmd := exec.Command("systemctl", action, serviceName)
		cmd.Dir = "/root"
		out, err := cmd.CombinedOutput()
		return err == nil, string(out)
	}

	dockerServices := map[string]string{
		"openpanel_mysql": "openpanel_mysql", "caddy": "caddy", "openpanel": "openpanel",
		"openpanel_dns": "bind9", "openadmin_mailserver": "openadmin_mailserver",
		"openadmin_ftp": "openadmin_ftp", "clamav": "clamav",
		"openadmin_roundcube": "roundcube", "phpmyadmin": "phpmyadmin",
	}

	svc, known := dockerServices[serviceName]
	if !known {
		cmd, err := podman.Command("default", action, serviceName)
		if err != nil {
			return false, err.Error()
		}
		cmd.Dir = "/root"
		out, runErr := cmd.CombinedOutput()
		return runErr == nil, string(out)
	}

	workingDir := "/root"
	if serviceName == "openadmin_mailserver" || serviceName == "openadmin_roundcube" {
		workingDir = "/usr/local/mail/openmail"
		if _, err := os.Stat(workingDir); err != nil {
			return false, "Error: Mail server is not installed! Emails are only available on OpenPanel Enterprise version and can be enabled from Emails page."
		}
	}
	if serviceName == "openadmin_mailserver" {
		svc = "mailserver"
	}

	runCompose := func(args ...string) ([]byte, error) {
		cmd, err := podman.ComposeCommand("default", args...)
		if err != nil {
			return nil, err
		}
		cmd.Dir = workingDir
		return cmd.CombinedOutput()
	}

	switch action {
	case "start":
		out, err := runCompose("up", "-d", svc)
		return err == nil, string(out)
	case "stop":
		out, err := runCompose("down", svc)
		return err == nil, string(out)
	case "restart":
		runCompose("down", svc)
		out, err := runCompose("up", "-d", svc)
		if err == nil && serviceName == "openpanel" {
			// Emptied, not removed: the chrome banner checks for
			// non-empty content (os.ReadFile), unlike the openadmin
			// flag which is checked with os.Stat for mere existence.
			os.WriteFile(OpenpanelRestartFlagPath, []byte(""), 0644)
		}
		return err == nil, string(out)
	default:
		return false, "No command defined for the service action."
	}
}

// --- monitored services config ---

func loadMonitoredServices() []map[string]interface{} {
	raw, err := os.ReadFile(ServicesConfigPath)
	if err != nil {
		return nil
	}
	var services []map[string]interface{}
	if json.Unmarshal(raw, &services) != nil {
		return nil
	}
	return services
}

// --- status fetching ---

func fetchAllDockerStatuses() map[string]bool {
	statuses := map[string]bool{}
	cmd, err := podman.Command("default", "ps", "-a", "--format", "{{.Names}}\t{{.Status}}")
	if err != nil {
		return statuses
	}
	out, err := cmd.Output()
	if err != nil {
		return statuses
	}
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 {
			name := strings.TrimSpace(parts[0])
			status := strings.ToLower(strings.TrimSpace(parts[1]))
			statuses[name] = strings.HasPrefix(status, "up")
		}
	}
	return statuses
}

func fetchAllSystemdStatuses(names []string) map[string]bool {
	if len(names) == 0 {
		return map[string]bool{}
	}
	args := append([]string{"is-active"}, names...)
	var out bytes.Buffer
	cmd := exec.Command("systemctl", args...)
	cmd.Stdout = &out
	cmd.Run() // non-zero exit is expected whenever any service is inactive

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	statuses := make(map[string]bool, len(names))
	for i, name := range names {
		statuses[name] = i < len(lines) && strings.TrimSpace(lines[i]) == "active"
	}
	return statuses
}

// getServiceStatusFromCache returns nil for "not yet initialized".
func getServiceStatusFromCache(service map[string]interface{}, dockerCache, systemdCache map[string]bool, userCount int) *bool {
	if service == nil {
		return nil
	}
	realName, _ := service["real_name"].(string)
	serviceType, _ := service["type"].(string)

	if serviceType == "docker" {
		coreDocker := map[string]bool{
			"openpanel_mysql": true, "caddy": true, "openpanel": true, "openpanel_dns": true,
			"openadmin_mailserver": true, "openadmin_ftp": true, "clamav": true, "openadmin_roundcube": true,
		}
		up, exists := dockerCache[realName]
		if !exists {
			if userCount == 0 && coreDocker[realName] {
				return nil
			}
			f := false
			return &f
		}
		return &up
	}

	up := systemdCache[realName]
	return &up
}

type serviceStatusEntry struct {
	RealName string `json:"real_name"`
	Type     string `json:"type"`
	Status   *bool  `json:"status"`
}

// ServeStatus handles GET/POST /services -- scoped to status + control,
// without the version/port enrichment (see the package doc comment).
func (s *Services) ServeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.handleStatusPost(w, r)
		return
	}

	services := loadMonitoredServices()

	var systemdNames []string
	for _, svc := range services {
		if t, _ := svc["type"].(string); t != "docker" {
			if name, ok := svc["real_name"].(string); ok {
				systemdNames = append(systemdNames, name)
			}
		}
	}

	dockerCache := fetchAllDockerStatuses()
	systemdCache := fetchAllSystemdStatuses(systemdNames)
	userCount, _, _ := paneldb.GetUserAndPlanCount(s.MySQL)

	statuses := map[string]serviceStatusEntry{}
	for _, svc := range services {
		displayName, _ := svc["name"].(string)
		realName, _ := svc["real_name"].(string)
		serviceType, _ := svc["type"].(string)
		status := getServiceStatusFromCache(svc, dockerCache, systemdCache, userCount)

		statuses[displayName] = serviceStatusEntry{RealName: realName, Type: serviceType, Status: status}
	}

	if r.URL.Query().Has("json") {
		writeJSON(w, statuses)
		return
	}

	// html/template can't cleanly branch on a *bool's pointed-to value (an
	// `if` on a non-nil pointer is always "truthy" regardless of what it
	// points to), so resolve the tri-state to a plain string label before
	// handing data to the template.
	type statusView struct {
		RealName string
		Type     string
		Label    string
	}
	views := make(map[string]statusView, len(statuses))
	for name, entry := range statuses {
		label := "unknown"
		if entry.Status != nil {
			if *entry.Status {
				label = "up"
			} else {
				label = "down"
			}
		}
		views[name] = statusView{RealName: entry.RealName, Type: entry.Type, Label: label}
	}

	webtemplates.Render(w, "services_status.html", mergeChrome(map[string]interface{}{
		"Statuses":  views,
		"CSRFToken": csrf.Token(r),
		"Flashes":   auth.PopFlashes(w, r, s.Sessions),
	}, r, "Services"))
}

func (s *Services) handleStatusPost(w http.ResponseWriter, r *http.Request) {
	realName := r.FormValue("real_name")
	action := r.FormValue("action")
	container := r.FormValue("container")

	validAction := action == "start" || action == "stop" || action == "restart"
	if !validAction {
		auth.AddFlash(w, r, s.Sessions, "Invalid action, please use one of: 'start', 'stop' or 'restart'.", "error")
	}
	if container != "system" && container != "docker" {
		auth.AddFlash(w, r, s.Sessions, "Invalid type, please set: 'system' or 'docker' as type.", "error")
	}

	success, message := controlServiceRun(realName, container, action)
	if success {
		auth.AddFlash(w, r, s.Sessions, "Successfully "+action+"ed service '"+realName+"'.", "success")
	} else {
		switch {
		case realName == "openpanel":
			cmdMap := map[string]string{
				"start":   "cd /root && podman-compose up -d openpanel",
				"stop":    "cd /root && podman-compose down openpanel",
				"restart": "cd /root && podman-compose down openpanel && podman-compose up -d openpanel",
			}
			auth.AddFlash(w, r, s.Sessions, "Failed to "+action+" service '"+realName+"'. Try from terminal: '"+cmdMap[action]+"'", "error")
		case container == "system":
			auth.AddFlash(w, r, s.Sessions, "Failed to "+action+" service '"+realName+"'. Try from terminal: 'systemctl "+action+" "+realName+"'", "error")
		default:
			auth.AddFlash(w, r, s.Sessions, "Failed to "+action+" service '"+realName+"': "+message, "error")
		}
	}

	if redirectTo := r.FormValue("redirect"); redirectTo != "" {
		if u, err := url.Parse(redirectTo); err == nil && isSameOrigin(r, u) {
			http.Redirect(w, r, u.Path, http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/services", http.StatusSeeOther)
}

// isSameOrigin decides whether to honor a caller-supplied redirect
// target.
func isSameOrigin(r *http.Request, target *url.URL) bool {
	return target.Host == "" || target.Host == r.Host
}

// ServeAdminStatus handles GET /services/admin/status: reaching this at
// all means the admin service is up.
func (s *Services) ServeAdminStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"status": "up"})
}

// ServeMonitored handles GET /services/monitored.
func (s *Services) ServeMonitored(w http.ResponseWriter, r *http.Request) {
	cfg := NotificationsConfigPath // /etc/openpanel/openadmin/config/notifications.ini, shared with notification_settings.go

	raw, err := os.ReadFile(cfg)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "Config file "+cfg+" does not exist")
		return
	}

	var monitored []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "services=") {
			monitored = strings.Split(strings.TrimPrefix(line, "services="), ",")
			break
		}
	}
	writeJSON(w, map[string]interface{}{"monitored_services": monitored})
}

// --- monitored-services config editor ---

// ServeEdit handles GET/POST /services/edit.
func (s *Services) ServeEdit(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		newData := strings.TrimSpace(r.FormValue("data"))
		var parsed interface{}
		if err := json.Unmarshal([]byte(newData), &parsed); err != nil {
			auth.AddFlash(w, r, s.Sessions, "Invalid JSON data: "+err.Error(), "error")
			http.Redirect(w, r, "/services/edit", http.StatusSeeOther)
			return
		}
		pretty, _ := json.MarshalIndent(parsed, "", "    ")
		if err := os.WriteFile(ServicesConfigPath, pretty, 0644); err != nil {
			auth.AddFlash(w, r, s.Sessions, "Error saving the file: "+err.Error()+". Please edit via terminal: "+ServicesConfigPath, "error")
		} else {
			auth.AddFlash(w, r, s.Sessions, "Config file updated successfully.", "success")
		}
	}

	var data interface{} = map[string]interface{}{}
	if raw, err := os.ReadFile(ServicesConfigPath); err == nil {
		if json.Unmarshal(raw, &data) != nil {
			auth.AddFlash(w, r, s.Sessions, "Error: Invalid JSON format in config file.", "error")
		}
	}

	if r.URL.Query().Has("json") {
		writeJSON(w, data)
		return
	}

	pretty, _ := json.MarshalIndent(data, "", "  ")
	webtemplates.Render(w, "services_edit.html", mergeChrome(map[string]interface{}{
		"Data":      string(pretty),
		"CSRFToken": csrf.Token(r),
		"Flashes":   auth.PopFlashes(w, r, s.Sessions),
	}, r, "Edit Services"))
}

// --- /service/{action}/{name} (a second, narrower control endpoint) ---

func loadAllowedServiceNames() map[string]bool {
	allowed := map[string]bool{}
	for _, svc := range loadMonitoredServices() {
		if name, ok := svc["real_name"].(string); ok {
			allowed[name] = true
		}
	}
	return allowed
}

var manageServiceRun = realManageService

// realManageService dispatches a service action against a narrower
// docker-services set than realControlService's, plus a generic
// `service <name> <action>` fallback for anything not in that set.
func realManageService(serviceName, action string) (bool, string) {
	dockerServices := map[string]string{
		"openpanel_mysql": "openpanel_mysql", "caddy": "caddy", "openpanel": "openpanel",
		"openpanel_dns": "bind9", "openadmin_mailserver": "openadmin_mailserver",
		"openadmin_ftp": "openadmin_ftp", "openadmin_roundcube": "roundcube",
	}

	svc, known := dockerServices[serviceName]
	if !known {
		cmd := exec.Command("service", serviceName, action)
		cmd.Dir = "/root"
		out, err := cmd.CombinedOutput()
		return err == nil, string(out)
	}

	workingDir := "/root"
	if serviceName == "openadmin_mailserver" || serviceName == "openadmin_roundcube" {
		workingDir = "/usr/local/mail/openmail"
		if _, err := os.Stat(workingDir); err != nil {
			return false, "Error: Mail server is not installed! Emails are only available on OpenPanel Enterprise version and can be enabled from Emails page."
		}
	}
	if serviceName == "openadmin_mailserver" {
		svc = "mailserver"
	}

	runCompose := func(args ...string) ([]byte, error) {
		cmd, err := podman.ComposeCommand("default", args...)
		if err != nil {
			return nil, err
		}
		cmd.Dir = workingDir
		return cmd.CombinedOutput()
	}

	switch action {
	case "start":
		out, err := runCompose("up", "-d", svc)
		return err == nil, string(out)
	case "stop":
		out, err := runCompose("down", svc)
		return err == nil, string(out)
	case "restart":
		runCompose("down", svc)
		out, err := runCompose("up", "-d", svc)
		if err == nil && serviceName == "openpanel" {
			// Emptied, not removed: the chrome banner checks for
			// non-empty content (os.ReadFile), unlike the openadmin
			// flag which is checked with os.Stat for mere existence.
			os.WriteFile(OpenpanelRestartFlagPath, []byte(""), 0644)
		}
		return err == nil, string(out)
	default:
		return false, "Invalid action: " + action
	}
}

// HandleManageService handles GET/POST /service/{action}/{service_name}.
func (s *Services) HandleManageService(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	serviceName := r.PathValue("service_name")

	allowed := map[string]bool{"start": true, "restart": true, "stop": true}
	if !allowed[action] {
		auth.AddFlash(w, r, s.Sessions, "Invalid action: "+action, "error")
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	if !loadAllowedServiceNames()[serviceName] {
		auth.AddFlash(w, r, s.Sessions, "Invalid service: "+serviceName, "error")
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	success, message := manageServiceRun(serviceName, action)
	if success {
		auth.AddFlash(w, r, s.Sessions, capitalize(serviceName)+" "+action+"ed successfully", "success")
	} else {
		auth.AddFlash(w, r, s.Sessions, "Error "+action+"ing "+serviceName+": "+message, "error")
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
