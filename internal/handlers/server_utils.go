package handlers

import (
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/config"
	"openadmin/internal/webtemplates"
)

// ServerUtils bundles the small self-contained server-admin endpoints
// (timezone, root password, RAM/swap cache dropping), mirroring
// modules/server/timezone.py, root_password.py, and drop_ram.py.
//
// The command runners below (timedatectlRun, passwdRun, dropCacheRun,
// swapCycleRun) are package-level function vars specifically so tests can
// stub them out -- passwd root, drop_caches, and swapoff/swapon are real,
// system-mutating commands that must never actually run against this
// sandbox (or any host) as a side effect of `go test`.
type ServerUtils struct {
	Sessions *auth.Manager
}

var (
	timedatectlRun = func(args ...string) (stdout, stderr string, err error) {
		return runCaptured("timedatectl", args...)
	}
	passwdRun = func(stdin string, args ...string) (stdout, stderr string, err error) {
		return runCapturedWithStdin(stdin, "passwd", args...)
	}
	dropCacheRun = func() error {
		return exec.Command("sh", "-c", "sync; echo 3 > /proc/sys/vm/drop_caches").Run()
	}
	swapCycleRun = func() error {
		return exec.Command("sh", "-c", "swapoff -a && swapon -a").Start() // fire-and-forget, matches subprocess.Popen
	}
)

func runCaptured(name string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.Command(name, args...)
	outBuf, errBuf := &strings.Builder{}, &strings.Builder{}
	cmd.Stdout, cmd.Stderr = outBuf, errBuf
	err = cmd.Run()
	return strings.TrimSpace(outBuf.String()), strings.TrimSpace(errBuf.String()), err
}

func runCapturedWithStdin(stdin, name string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	outBuf, errBuf := &strings.Builder{}, &strings.Builder{}
	cmd.Stdout, cmd.Stderr = outBuf, errBuf
	err = cmd.Run()
	return strings.TrimSpace(outBuf.String()), strings.TrimSpace(errBuf.String()), err
}

// --- timezone ---

// AllTimezones is a var (not a function-local) so tests can substitute a
// small fixture list instead of walking the real /usr/share/zoneinfo tree,
// mirroring pytz.all_timezones.
var AllTimezones = loadSystemTimezones

const zoneinfoRoot = "/usr/share/zoneinfo"

var zoneinfoExcluded = map[string]bool{
	"posixrules": true, "Factory": true, "localtime": true,
}

func loadSystemTimezones() []string {
	var zones []string
	_ = filepath.WalkDir(zoneinfoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(zoneinfoRoot, path)
		if err != nil {
			return nil
		}
		base := filepath.Base(rel)
		if zoneinfoExcluded[base] || strings.HasPrefix(base, ".") || strings.ToUpper(base) == base {
			return nil
		}
		zones = append(zones, rel)
		return nil
	})
	sort.Strings(zones)
	return zones
}

func currentTimezone() (string, error) {
	out, _, err := timedatectlRun("show", "--property=Timezone", "--value")
	if err == nil && out != "" {
		return out, nil
	}
	link, linkErr := os.Readlink("/etc/localtime")
	if linkErr != nil {
		return "", linkErr
	}
	if idx := strings.Index(link, "zoneinfo/"); idx != -1 {
		return link[idx+len("zoneinfo/"):], nil
	}
	return link, nil
}

type timezonePageData struct {
	webtemplates.Chrome
	AvailableTimezones []string
	CurrentTimezone    string
	CSRFToken          string
	Flashes            []auth.Flash
}

// ServeTimezone handles GET/POST /server/timezone, mirroring
// server_timezone_settings().
func (s *ServerUtils) ServeTimezone(w http.ResponseWriter, r *http.Request) {
	zones := AllTimezones()
	current, err := currentTimezone()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Error reading current timezone from the server: "+err.Error())
		return
	}

	if r.Method == http.MethodPost {
		selected := r.FormValue("timezone")
		if !containsString(zones, selected) {
			writeJSONError(w, http.StatusBadRequest, "Invalid timezone: "+selected)
			return
		}
		if _, stderr, err := timedatectlRun("set-timezone", selected); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Error changing timezone to "+selected+": "+stderr)
			return
		}
		http.Redirect(w, r, "/server/timezone", http.StatusSeeOther)
		return
	}

	webtemplates.Render(w, "timezone.html", timezonePageData{
		Chrome:             buildChrome(r, "Timezone"),
		AvailableTimezones: zones,
		CurrentTimezone:    current,
		CSRFToken:          csrf.Token(r),
		Flashes:            auth.PopFlashes(w, r, s.Sessions),
	})
}

func containsString(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// --- root password ---

type rootPasswordPageData struct {
	webtemplates.Chrome
	CSRFToken string
	Flashes   []auth.Flash
}

// ServeRootPassword handles GET/POST /server/root-password, mirroring
// root_password(). Only the "admin" role (Super Administrator) may use
// this, enforced in addition to the route's admin_required_route gate.
func (s *ServerUtils) ServeRootPassword(w http.ResponseWriter, r *http.Request) {
	currentUser := auth.CurrentUser(r)
	if currentUser.Role != "admin" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if r.Method == http.MethodPost {
		s.handleRootPasswordPost(w, r)
		return
	}

	webtemplates.Render(w, "root_password.html", rootPasswordPageData{
		Chrome:    buildChrome(r, "Root Password"),
		CSRFToken: csrf.Token(r),
		Flashes:   auth.PopFlashes(w, r, s.Sessions),
	})
}

func (s *ServerUtils) handleRootPasswordPost(w http.ResponseWriter, r *http.Request) {
	password := r.FormValue("password")
	if password == "" {
		auth.AddFlash(w, r, s.Sessions, "Password cannot be empty.", "error")
		http.Redirect(w, r, "/server/root-password", http.StatusSeeOther)
		return
	}

	if _, stderr, err := passwdRun(password+"\n"+password+"\n", "root"); err != nil {
		auth.AddFlash(w, r, s.Sessions, "Error changing password: "+stderr, "error")
		http.Redirect(w, r, "/server/root-password", http.StatusSeeOther)
		return
	}

	verifyOut, _, err := passwdRun("", "--status", "root")
	if err == nil && strings.Contains(verifyOut, "P") {
		auth.AddFlash(w, r, s.Sessions, "SSH password changed successfully!", "success")
	} else {
		auth.AddFlash(w, r, s.Sessions, "Password change verification failed.", "error")
	}
	http.Redirect(w, r, "/server/root-password", http.StatusSeeOther)
}

// --- drop RAM / swap ---

// HandleDropMemoryCache handles POST /server/memory_usage/drop, mirroring
// drop_memory_cache().
func (s *ServerUtils) HandleDropMemoryCache(w http.ResponseWriter, r *http.Request) {
	if err := dropCacheRun(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to drop cache.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"success","message":"Cache dropped successfully."}`))
}

// HandleDropSwapCache handles POST /server/memory_usage/drop-swap,
// mirroring drop_swap_cache().
func (s *ServerUtils) HandleDropSwapCache(w http.ResponseWriter, r *http.Request) {
	if err := swapCycleRun(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to clear swap.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(`{"status":"success","message":"Swap clearing started in background."}`))
}

// --- demo mode ---

var demoModeRun = func() error {
	return exec.Command("opencli", "config", "update", "demo_mode", "on").Start() // fire-and-forget, matches subprocess.Popen
}

type demoModePageData struct {
	webtemplates.Chrome
	DemoMode  string
	CSRFToken string
	Flashes   []auth.Flash
}

// ServeDemoMode handles GET/POST /server/demo-mode, mirroring
// enable_demo_mode().
func (s *ServerUtils) ServeDemoMode(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if err := demoModeRun(); err != nil {
			auth.AddFlash(w, r, s.Sessions, "Failed to enable demo mode: "+err.Error(), "error")
		} else {
			auth.AddFlash(w, r, s.Sessions, "Demo mode is enabled. Restart OpenPanel and OpenAdmin services to apply.", "info")
		}
	}

	webtemplates.Render(w, "demo_mode.html", demoModePageData{
		Chrome:    buildChrome(r, "Demo Mode"),
		DemoMode:  config.Openpanel().Get("PANEL", "demo_mode", "off"),
		CSRFToken: csrf.Token(r),
		Flashes:   auth.PopFlashes(w, r, s.Sessions),
	})
}
