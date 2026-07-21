// This file implements the domain/port/proxy/dev-mode general settings
// page.
package handlers

import (
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// General bundles the /settings/general handler.
type General struct {
	Sessions *auth.Manager
	// DevMode is read from openpanel.config once at process startup (see
	// bootstrap.IsDevMode() in main.go) and never re-read afterwards. This
	// preserves a real (and, per the template's own on-page note,
	// *intentional*) quirk: after this page's dev-mode toggle writes the
	// new value via `opencli config update dev_mode ...`, both the page
	// and the POST-comparison logic keep using the OLD value until
	// OpenAdmin actually restarts (which the restart-flag mechanism
	// requests but doesn't perform inline).
	DevMode bool
}

var generalPortRe = regexp.MustCompile(`^[0-9]{1,5}$`)

// generalOpenpanelPortRun/generalOpenadminPortRun/generalAdminDomainRun/
// generalOpenpanelProxyRun are always queried fresh (no caching).
// Injectable so tests never shell out to a real opencli binary.
var (
	generalOpenpanelPortRun = func() (string, error) {
		out, err := exec.Command("opencli", "port").Output()
		return strings.TrimSpace(string(out)), err
	}
	generalOpenadminPortRun = func() (string, error) {
		out, err := exec.Command("opencli", "admin", "port").Output()
		return strings.TrimSpace(string(out)), err
	}
	generalAdminDomainRun = func() (string, error) {
		out, err := exec.Command("opencli", "domain").Output()
		return strings.TrimSpace(string(out)), err
	}
	generalOpenpanelProxyRun = func() (string, error) {
		out, err := exec.Command("opencli", "proxy").Output()
		return strings.TrimSpace(string(out)), err
	}
	generalHostnameRun = func() (string, error) {
		out, err := exec.Command("hostname").Output()
		return strings.TrimSpace(string(out)), err
	}

	generalSetOpenpanelPortRun = func(port string) { _ = exec.Command("opencli", "port", "set", port, "--no-restart").Run() }
	generalSetDomainRun        = func(domain string) { _ = exec.Command("opencli", "domain", "set", domain, "--no-restart").Run() }
	generalSetDevModeRun       = func(value string) { _ = exec.Command("opencli", "config", "update", "dev_mode", value).Run() }
	generalSetAdminPortRun     = func(port string) { _ = exec.Command("opencli", "admin", "port", port, "--no-restart").Run() }
	generalSetProxyRun         = func(proxy string) { _ = exec.Command("opencli", "proxy", "set", proxy, "--no-restart").Run() }
)

// GeneralOpenpanelRestartFlagPath / GeneralOpenadminRestartFlagPath are
// test seams for the two hardcoded restart-flag paths.
var (
	GeneralOpenpanelRestartFlagPath = "/root/openpanel_restart_needed"
	GeneralOpenadminRestartFlagPath = "/root/openadmin_restart_needed"
)

func generalValidatedPort(raw string, err error, fallback string) string {
	if err != nil || !generalPortRe.MatchString(raw) {
		return fallback
	}
	n, convErr := strconv.Atoi(raw)
	if convErr != nil || n < 0 || n > 65535 {
		return fallback
	}
	return raw
}

func generalOpenpanelPort() string {
	raw, err := generalOpenpanelPortRun()
	return generalValidatedPort(raw, err, "2083")
}

func generalOpenadminPort() string {
	raw, err := generalOpenadminPortRun()
	return generalValidatedPort(raw, err, "2087")
}

func generalAdminDomain() string {
	raw, err := generalAdminDomainRun()
	if err != nil {
		return ""
	}
	return raw
}

func generalOpenpanelProxy() string {
	raw, err := generalOpenpanelProxyRun()
	if err != nil {
		return "openpanel"
	}
	return raw
}

func generalHostname() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	h, _ := generalHostnameRun()
	return h
}

// ServeGeneral handles GET/POST /settings/general.
func (g *General) ServeGeneral(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()

		var changes []string
		openpanelRestartNeeded := false
		openadminRestartNeeded := false

		forceDomain := r.PostFormValue("force_domain")
		adminPortValue := r.PostFormValue("2087_port")
		openpanelPortValue := r.PostFormValue("2083_port")
		openpanelProxy := r.PostFormValue("openpanel_proxy")

		adminPort := 2087
		if adminPortValue != "" {
			if n, err := strconv.Atoi(adminPortValue); err == nil {
				adminPort = n
			}
		}
		openpanelPort := 2083
		if openpanelPortValue != "" {
			if n, err := strconv.Atoi(openpanelPortValue); err == nil {
				openpanelPort = n
			}
		}

		forceDomainCurrentValue := generalAdminDomain()
		openpanelPortCurrentValue, _ := strconv.Atoi(generalOpenpanelPort())

		if openpanelPort != openpanelPortCurrentValue {
			generalSetOpenpanelPortRun(strconv.Itoa(openpanelPort))
			changes = append(changes, "user-panel port")
			openpanelRestartNeeded = true
		}

		if forceDomain != "" && forceDomainCurrentValue != forceDomain {
			generalSetDomainRun(forceDomain)
			changes = append(changes, "domain")
			openpanelRestartNeeded = true
			openadminRestartNeeded = true
		}

		devModeNewValue := r.PostFormValue("dev_mode")
		devModeCurrent := "off"
		if g.DevMode {
			devModeCurrent = "on"
		}
		if (devModeCurrent == "on" && devModeNewValue == "off") || (devModeCurrent == "off" && devModeNewValue == "on") {
			generalSetDevModeRun(devModeNewValue)
			changes = append(changes, "dev_mode")
			openpanelRestartNeeded = true
			openadminRestartNeeded = true
		}

		openadminPortCurrentValue, _ := strconv.Atoi(generalOpenadminPort())
		if adminPort != openadminPortCurrentValue {
			generalSetAdminPortRun(strconv.Itoa(adminPort))
			changes = append(changes, "admin-panel port")
			openadminRestartNeeded = true
		}

		// Only acts (and only reports/restarts) when the value actually
		// changed, rather than unconditionally re-running "opencli proxy
		// set" and forcing a restart on every POST even when nothing
		// changed.
		newProxy := openpanelProxy
		if newProxy == "" {
			newProxy = "openpanel"
		}
		if newProxy != generalOpenpanelProxy() {
			generalSetProxyRun(newProxy)
			changes = append(changes, "proxy")
			openadminRestartNeeded = true
		}

		if openpanelRestartNeeded {
			os.WriteFile(GeneralOpenpanelRestartFlagPath, []byte("Restart needed"), 0644)
		}
		if openadminRestartNeeded {
			os.WriteFile(GeneralOpenadminRestartFlagPath, []byte("Restart needed"), 0644)
		}

		if len(changes) > 0 {
			auth.AddFlash(w, r, g.Sessions, "Settings updated: "+strings.Join(changes, ", "), "success")
		} else {
			auth.AddFlash(w, r, g.Sessions, "No changes made.", "info")
		}
	}

	// The template gets the raw "on"/"off" string (not a JSON/template
	// boolean) -- it does its own conversion to a 'true'/'false' string
	// for the Alpine.js toggle.
	devModeStr := "off"
	if g.DevMode {
		devModeStr = "on"
	}

	data := map[string]interface{}{
		"ServerHostname": generalHostname(),
		"Port":           generalOpenpanelPort(),
		"AdminPort":      generalOpenadminPort(),
		"Proxy":          generalOpenpanelProxy(),
		"ForceDomain":    generalAdminDomain(),
		"DevMode":        devModeStr,
	}

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, data)
		return
	}

	data["Flashes"] = auth.PopFlashes(w, r, g.Sessions)
	merged := mergeChrome(data, r, "General Settings")
	// mergeChrome's own DevMode (the site-wide dev-mode bool) would
	// otherwise clobber this page's "on"/"off" setting value above.
	merged["DevMode"] = devModeStr
	webtemplates.Render(w, "settings_general.html", merged)
}
