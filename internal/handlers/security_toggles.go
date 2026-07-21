package handlers

import (
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/bootstrap"
	"openadmin/internal/config"
	"openadmin/internal/webtemplates"
)

// SecurityToggles bundles a handful of small self-contained config-toggle
// pages: disabling OpenAdmin, basic auth, and the useragent blacklist.
type SecurityToggles struct {
	Sessions *auth.Manager
}

// --- disable OpenAdmin ---

type disableAdminPageData struct {
	webtemplates.Chrome
	CSRFToken string
	Flashes   []auth.Flash
}

// ServeDisableAdmin handles GET/POST /security/disable-admin. Only the
// "admin" role may use this, enforced here in addition to the route's
// own role gate.
func (s *SecurityToggles) ServeDisableAdmin(w http.ResponseWriter, r *http.Request) {
	currentUser := auth.CurrentUser(r)
	if currentUser.Role != "admin" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if r.Method == http.MethodPost {
		_ = exec.Command("opencli", "admin", "off").Start() // fire-and-forget
		auth.AddFlash(w, r, s.Sessions, "OpenAdmin is now disabled and all further actions need to be performed via terminal.", "info")
	}

	webtemplates.Render(w, "disable_admin.html", disableAdminPageData{
		Chrome:    buildChrome(r, "Disable OpenAdmin"),
		CSRFToken: csrf.Token(r),
		Flashes:   auth.PopFlashes(w, r, s.Sessions),
	})
}

// --- basic auth ---

type basicAuthPageData struct {
	webtemplates.Chrome
	BasicAuth         string
	BasicAuthUsername string
	BasicAuthPassword string
	CSRFToken         string
	Flashes           []auth.Flash
}

// ServeBasicAuth handles GET/POST /security/basic_auth.
func (s *SecurityToggles) ServeBasicAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.handleBasicAuthPost(w, r)
		return
	}

	cfg := config.Load(config.AdminConfigPath)
	webtemplates.Render(w, "basic_auth.html", basicAuthPageData{
		Chrome:            buildChrome(r, "OpenAdmin Basic Auth"),
		BasicAuth:         cfg.Get("SECURITY", "basic_auth", ""),
		BasicAuthUsername: cfg.Get("SECURITY", "basic_auth_username", ""),
		BasicAuthPassword: cfg.Get("SECURITY", "basic_auth_password", ""),
		CSRFToken:         csrf.Token(r),
		Flashes:           auth.PopFlashes(w, r, s.Sessions),
	})
}

func (s *SecurityToggles) handleBasicAuthPost(w http.ResponseWriter, r *http.Request) {
	cfg := config.Load(config.AdminConfigPath)
	r.ParseForm()

	if v := formValueOrNil(r, "basic_auth"); v != nil {
		cfg.Set("SECURITY", "basic_auth", *v)
	}
	if v := formValueOrNil(r, "basic_auth_username"); v != nil {
		cfg.Set("SECURITY", "basic_auth_username", *v)
	}
	if v := formValueOrNil(r, "basic_auth_password"); v != nil {
		cfg.Set("SECURITY", "basic_auth_password", *v)
	}

	if err := config.Save(config.AdminConfigPath, cfg); err != nil {
		auth.AddFlash(w, r, s.Sessions, "Failed to write config: "+err.Error(), "error")
		http.Redirect(w, r, "/security/basic_auth", http.StatusSeeOther)
		return
	}

	os.WriteFile(bootstrap.RestartFlagPath, []byte("Restart needed"), 0644)
	auth.AddFlash(w, r, s.Sessions, "Basic_auth settings for OpenAdmin edited successfully.", "success")
	http.Redirect(w, r, "/security/basic_auth", http.StatusSeeOther)
}

// --- blacklist useragents ---

var BlacklistUseragentsFilePath = "/etc/openpanel/openpanel/conf/blacklist_useragents.txt"

// OpenpanelRestartFlagPath is the flag file OpenPanel's own UI checks for
// restart-needed state -- distinct from bootstrap.RestartFlagPath, which
// is OpenAdmin's own restart flag.
var OpenpanelRestartFlagPath = "/root/openpanel_restart_needed"

type blacklistUseragentsPageData struct {
	webtemplates.Chrome
	BlacklistUseragentsEnabled string
	BlacklistUseragents        string
	CSRFToken                  string
	Flashes                    []auth.Flash
}

// ServeBlacklistUseragents handles GET/POST /security/blacklist-useragents.
// Only the "admin" role may use this.
func (s *SecurityToggles) ServeBlacklistUseragents(w http.ResponseWriter, r *http.Request) {
	currentUser := auth.CurrentUser(r)
	if currentUser.Role != "admin" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	enabledOut, _, _ := runOpenCLICaptured("opencli", "config", "get", "blacklist_useragents")
	enabled := strings.TrimSpace(enabledOut)

	content, err := os.ReadFile(BlacklistUseragentsFilePath)
	list := string(content)
	if err != nil {
		list = ""
		auth.AddFlash(w, r, s.Sessions, "Blacklist file not found. A new one will be created on save.", "warning")
	}

	if r.Method == http.MethodPost {
		updated := false

		if newList := r.FormValue("blacklist_useragents"); newList != "" {
			updated = true
			if err := os.WriteFile(BlacklistUseragentsFilePath, []byte(newList), 0644); err == nil {
				list = newList
			}
		}

		action := r.FormValue("blacklist_useragents_enabled")
		if action == "" {
			action = "no"
		}
		if enabled != action {
			updated = true
			_ = exec.Command("opencli", "config", "update", "blacklist_useragents", action).Run()
			enabled = action
		}

		if updated {
			auth.AddFlash(w, r, s.Sessions, "Saved blacklisted useragents.", "info")
			os.WriteFile(OpenpanelRestartFlagPath, []byte("Restart needed for OpenPanel service."), 0644)
		}
	}

	webtemplates.Render(w, "blacklist_useragents.html", blacklistUseragentsPageData{
		Chrome:                     buildChrome(r, "Blacklist Useragents"),
		BlacklistUseragentsEnabled: enabled,
		BlacklistUseragents:        list,
		CSRFToken:                  csrf.Token(r),
		Flashes:                    auth.PopFlashes(w, r, s.Sessions),
	})
}

// runOpenCLICaptured is like runOpenCLI but always reports success/failure
// via the error return (runOpenCLI's bool return collapses stderr into the
// message, which callers that just want "opencli config get" output don't
// need).
func runOpenCLICaptured(args ...string) (stdout, stderr string, err error) {
	cmd := exec.Command(args[0], args[1:]...)
	outBuf, errBuf := &strings.Builder{}, &strings.Builder{}
	cmd.Stdout, cmd.Stderr = outBuf, errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}
