// This file implements SSH service status/control, sshd_config editing
// (both the quick "basic" settings and the raw "advanced" config), and
// authorized_keys management.
package handlers

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// SSH bundles the /server/ssh and /server/ssh/config handlers.
type SSH struct {
	Sessions *auth.Manager
}

// SSHDConfigPath / SSHAuthorizedKeysPath are vars (test-seams) so tests
// can point them at scratch files. SSHAuthorizedKeysPath is resolved
// once at package init rather than per request.
var (
	SSHDConfigPath        = "/etc/ssh/sshd_config"
	SSHAuthorizedKeysPath = sshDefaultAuthorizedKeysPath()
)

func sshDefaultAuthorizedKeysPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/root"
	}
	return home + "/.ssh/authorized_keys"
}

func isValidSSHPort(port string) bool {
	n, err := strconv.Atoi(port)
	if err != nil {
		return false
	}
	return n >= 22 && n <= 10000
}

func isValidSSHAuthParam(param string) bool {
	return param == "yes" || param == "no"
}

// sshStatusRun / sshExecuteActionRun / sshRestartServiceRun are injectable
// so tests never shell out to systemctl.
var sshStatusRun = func() string {
	out, _ := exec.Command("systemctl", "is-active", "ssh").Output()
	return strings.TrimSpace(string(out))
}

// sshExecuteActionRun runs the systemctl command and discards the
// result entirely, so a failing systemctl command is silently ignored --
// the caller always flashes success regardless. This (surprising but
// real) fire-and-forget behavior is kept as-is rather than turned into
// an error-checked call.
var sshExecuteActionRun = func(action string) {
	_ = exec.Command("systemctl", action, "ssh").Run()
}

var sshRestartServiceRun = func() {
	sshExecuteActionRun("restart")
}

type sshSettings struct {
	Port            string
	PasswordAuth    string
	PubkeyAuth      string
	PermitRootLogin string
}

func sshDefaultSettings() sshSettings {
	return sshSettings{Port: "22", PasswordAuth: "yes", PubkeyAuth: "no", PermitRootLogin: "yes"}
}

// sshParseSettings does a full top-to-bottom scan where the LAST
// matching line (commented or active) for each directive wins, not the
// first.
func sshParseSettings(raw string) sshSettings {
	settings := sshDefaultSettings()
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		stripped := strings.TrimSpace(scanner.Text())
		fields := strings.Fields(stripped)
		switch {
		case strings.HasPrefix(stripped, "#Port"):
			settings.Port = "22"
		case strings.HasPrefix(stripped, "Port"):
			if len(fields) > 1 {
				settings.Port = fields[1]
			}
		case strings.HasPrefix(stripped, "#PasswordAuthentication"):
			settings.PasswordAuth = "no"
		case strings.HasPrefix(stripped, "PasswordAuthentication"):
			if len(fields) > 1 {
				settings.PasswordAuth = fields[1]
			}
		case strings.HasPrefix(stripped, "#PubkeyAuthentication"):
			settings.PubkeyAuth = "no"
		case strings.HasPrefix(stripped, "PubkeyAuthentication"):
			if len(fields) > 1 {
				settings.PubkeyAuth = fields[1]
			}
		case strings.HasPrefix(stripped, "#PermitRootLogin"):
			settings.PermitRootLogin = "no"
		case strings.HasPrefix(stripped, "PermitRootLogin"):
			if len(fields) > 1 {
				settings.PermitRootLogin = fields[1]
			}
		}
	}
	return settings
}

func sshReadConfig() (string, error) {
	raw, err := os.ReadFile(SSHDConfigPath)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// sshUpdateConfigRun overwrites the whole file with client-supplied
// content, then restarts sshd.
var sshUpdateConfigRun = func(newConfig string) error {
	if err := os.WriteFile(SSHDConfigPath, []byte(newConfig), 0644); err != nil {
		return err
	}
	sshRestartServiceRun()
	return nil
}

// sshUpdateSettingsRun rewrites every line matching one of the 4
// directives (commented or active) in place.
//
// The real UI's one <form> always submits all 4 fields together, so a
// partial update is only reachable via a direct API call -- e.g.
// submitting only "port". This merges only the fields that were
// actually submitted on top of the settings currently on disk, leaving
// any omitted directive's line untouched, rather than writing an empty
// value into the other 3 directives and potentially locking out SSH
// access entirely.
var sshUpdateSettingsRun = func(newSettings sshSettings) error {
	raw, err := sshReadConfig()
	if err != nil {
		return err
	}
	current := sshParseSettings(raw)
	merged := sshSettings{
		Port:            firstNonEmpty(newSettings.Port, current.Port),
		PasswordAuth:    firstNonEmpty(newSettings.PasswordAuth, current.PasswordAuth),
		PubkeyAuth:      firstNonEmpty(newSettings.PubkeyAuth, current.PubkeyAuth),
		PermitRootLogin: firstNonEmpty(newSettings.PermitRootLogin, current.PermitRootLogin),
	}

	var out strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		stripped := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(stripped, "#Port") || strings.HasPrefix(stripped, "Port"):
			out.WriteString("Port " + merged.Port + "\n")
		case strings.HasPrefix(stripped, "#PasswordAuthentication") || strings.HasPrefix(stripped, "PasswordAuthentication"):
			out.WriteString("PasswordAuthentication " + merged.PasswordAuth + "\n")
		case strings.HasPrefix(stripped, "#PubkeyAuthentication") || strings.HasPrefix(stripped, "PubkeyAuthentication"):
			out.WriteString("PubkeyAuthentication " + merged.PubkeyAuth + "\n")
		case strings.HasPrefix(stripped, "#PermitRootLogin") || strings.HasPrefix(stripped, "PermitRootLogin"):
			out.WriteString("PermitRootLogin " + merged.PermitRootLogin + "\n")
		default:
			out.WriteString(line + "\n")
		}
	}
	if err := os.WriteFile(SSHDConfigPath, []byte(out.String()), 0644); err != nil {
		return err
	}
	sshRestartServiceRun()
	return nil
}

type sshAuthorizedKey struct {
	Comment string `json:"comment"`
	Key     string `json:"key"`
}

// sshGetAuthorizedKeys treats a "#"-prefixed line as the pending comment
// for the NEXT key line; a comment with no following key line (e.g. a
// trailing comment) is discarded.
func sshGetAuthorizedKeys() []sshAuthorizedKey {
	f, err := os.Open(SSHAuthorizedKeysPath)
	if err != nil {
		return []sshAuthorizedKey{}
	}
	defer f.Close()

	keys := []sshAuthorizedKey{}
	pendingComment := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			pendingComment = strings.TrimSpace(line[1:])
			continue
		}
		keys = append(keys, sshAuthorizedKey{Comment: pendingComment, Key: line})
		pendingComment = ""
	}
	return keys
}

var sshAddAuthorizedKeyRun = func(newKey string) error {
	f, err := os.OpenFile(SSHAuthorizedKeysPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(newKey + "\n")
	return err
}

var sshRemoveAuthorizedKeyRun = func(keyToRemove string) error {
	raw, err := os.ReadFile(SSHAuthorizedKeysPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var out strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	trimmedTarget := strings.TrimSpace(keyToRemove)
	for scanner.Scan() {
		line := scanner.Text()
		stripped := strings.TrimSpace(line)
		if stripped == "" || strings.HasPrefix(stripped, "#") || stripped != trimmedTarget {
			out.WriteString(line + "\n")
		}
	}
	return os.WriteFile(SSHAuthorizedKeysPath, []byte(out.String()), 0600)
}

// ServeSSH handles GET/POST /server/ssh.
func (s *SSH) ServeSSH(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		action := r.PostFormValue("action")
		newConfig := r.PostFormValue("config")
		newKey := r.PostFormValue("new_key")
		keyToRemove := r.PostFormValue("key_to_remove")
		port := r.PostFormValue("port")
		passwordAuth := r.PostFormValue("password_auth")
		pubkeyAuth := r.PostFormValue("pubkey_auth")
		permitRootLogin := r.PostFormValue("permit_root_login")

		if port != "" && !isValidSSHPort(port) {
			writeJSONError(w, http.StatusBadRequest, "Invalid SSH port. It must be a number between 22 and 10000.")
			return
		}
		if passwordAuth != "" && !isValidSSHAuthParam(passwordAuth) {
			writeJSONError(w, http.StatusBadRequest, `Invalid value for password_auth. It must be "yes", "no".`)
			return
		}
		if pubkeyAuth != "" && !isValidSSHAuthParam(pubkeyAuth) {
			writeJSONError(w, http.StatusBadRequest, `Invalid value for pubkey_auth. It must be "yes" or "no".`)
			return
		}
		if permitRootLogin != "" && !isValidSSHAuthParam(permitRootLogin) {
			writeJSONError(w, http.StatusBadRequest, `Invalid value for permit_root_login. It must be "yes", "no".`)
			return
		}

		// This is a cascading if-chain (not else-if): an "action" flash
		// doesn't return early, so a request that also carries e.g.
		// "config" or the basic settings fields performs BOTH operations
		// in one round trip.
		if action != "" {
			sshExecuteActionRun(action)
			auth.AddFlash(w, r, s.Sessions, "SSH service has been "+action+"ed.", "success")
		}

		if newConfig != "" {
			if err := sshUpdateConfigRun(newConfig); err != nil {
				auth.AddFlash(w, r, s.Sessions, "Failed to update SSH configuration: "+err.Error(), "error")
			} else {
				auth.AddFlash(w, r, s.Sessions, "SSH configuration updated and service restarted.", "success")
			}
			http.Redirect(w, r, "/server/ssh#advanced", http.StatusSeeOther)
			return
		}

		if newKey != "" {
			if err := sshAddAuthorizedKeyRun(newKey); err != nil {
				auth.AddFlash(w, r, s.Sessions, "Failed to add SSH key: "+err.Error(), "error")
			} else {
				auth.AddFlash(w, r, s.Sessions, "New SSH key added.", "success")
			}
			http.Redirect(w, r, "/server/ssh#keys", http.StatusSeeOther)
			return
		}

		if keyToRemove != "" {
			if err := sshRemoveAuthorizedKeyRun(keyToRemove); err != nil {
				auth.AddFlash(w, r, s.Sessions, "Failed to remove SSH key: "+err.Error(), "error")
			} else {
				auth.AddFlash(w, r, s.Sessions, "SSH key removed.", "success")
			}
			http.Redirect(w, r, "/server/ssh#keys", http.StatusSeeOther)
			return
		}

		if port != "" || passwordAuth != "" || pubkeyAuth != "" || permitRootLogin != "" {
			if err := sshUpdateSettingsRun(sshSettings{
				Port:            port,
				PasswordAuth:    passwordAuth,
				PubkeyAuth:      pubkeyAuth,
				PermitRootLogin: permitRootLogin,
			}); err != nil {
				auth.AddFlash(w, r, s.Sessions, "Failed to update SSH settings: "+err.Error(), "error")
			} else {
				auth.AddFlash(w, r, s.Sessions, "SSH settings updated.", "success")
			}
			http.Redirect(w, r, "/server/ssh#basic", http.StatusSeeOther)
			return
		}

		http.Redirect(w, r, "/server/ssh#basic", http.StatusSeeOther)
		return
	}

	status := sshStatusRun()
	config, err := sshReadConfig()
	if err != nil {
		config = ""
	}
	keys := sshGetAuthorizedKeys()
	settings := sshDefaultSettings()
	if config != "" {
		settings = sshParseSettings(config)
	}

	if r.URL.Query().Get("output") == "json" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":            status,
			"config":            config,
			"keys":              keys,
			"port":              settings.Port,
			"password_auth":     settings.PasswordAuth,
			"pubkey_auth":       settings.PubkeyAuth,
			"permit_root_login": settings.PermitRootLogin,
		})
		return
	}

	webtemplates.Render(w, "server_ssh.html", mergeChrome(map[string]interface{}{
		"Status":          status,
		"Config":          config,
		"Keys":            keys,
		"Port":            settings.Port,
		"PasswordAuth":    settings.PasswordAuth,
		"PubkeyAuth":      settings.PubkeyAuth,
		"PermitRootLogin": settings.PermitRootLogin,
		"CSRFToken":       csrf.Token(r),
		"Flashes":         auth.PopFlashes(w, r, s.Sessions),
	}, r, "SSH"))
}

// ServeSSHFullConfig handles GET/POST /server/ssh/config.
func (s *SSH) ServeSSHFullConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		newConfig := r.PostFormValue("config")
		if newConfig != "" {
			if err := sshUpdateConfigRun(newConfig); err != nil {
				auth.AddFlash(w, r, s.Sessions, "Failed to update SSH configuration: "+err.Error(), "error")
			} else {
				auth.AddFlash(w, r, s.Sessions, "SSH configuration updated and service restarted.", "success")
			}
		}
		http.Redirect(w, r, "/server/ssh#advanced", http.StatusSeeOther)
		return
	}

	config, err := sshReadConfig()
	if err != nil {
		config = ""
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"config": config})
}
