// This file implements a handful of small, self-contained JSON REST API
// server-admin routes: timezone, the clustering default-node config, the
// root SSH password, and the reboot trigger. Each reuses the same on-disk
// paths and command runners as its HTML admin-page equivalent
// (server_utils.go, slave.go, reboot.go) -- only the request/response shape
// differs.
package handlers

import (
	"net/http"
	"os"
	"os/exec"
	"strings"
)

// APIServerOps bundles the /api/server/timezone,
// /api/server/root-password, and /api/server/reboot handlers.
type APIServerOps struct{}

// --- timezone ---

// ServeTimezone handles GET/POST /api/server/timezone.
func (a *APIServerOps) ServeTimezone(w http.ResponseWriter, r *http.Request) {
	zones := AllTimezones()
	current, err := currentTimezone()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Error reading current timezone from the server: "+err.Error())
		return
	}

	if r.Method == http.MethodPost {
		var body struct {
			Timezone string `json:"timezone"`
		}
		if !apiDecodeJSONBody(r, &body) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}
		if !containsString(zones, body.Timezone) {
			writeJSONError(w, http.StatusBadRequest, "Invalid timezone: "+body.Timezone)
			return
		}
		if _, stderr, err := timedatectlRun("set-timezone", body.Timezone); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Error changing timezone to "+body.Timezone+": "+stderr)
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "timezone": body.Timezone})
		return
	}

	writeJSON(w, map[string]interface{}{"current_timezone": current, "available_timezones": zones})
}

// --- root SSH password ---

// ServeRootPassword handles POST /api/server/root-password. Wrapped with
// (*APIAuth).RequireAPISuperAdmin, so the role check root_password.py does
// inline (current_user.role != "admin") is already enforced before this
// runs.
func (a *APIServerOps) ServeRootPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if !apiDecodeJSONBody(r, &body) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	if body.Password == "" {
		writeJSONError(w, http.StatusBadRequest, "Password cannot be empty.")
		return
	}

	if _, stderr, err := passwdRun(body.Password+"\n"+body.Password+"\n", "root"); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": "Error changing password: " + stderr})
		return
	}
	verifyOut, verifyStderr, err := passwdRun("", "--status", "root")
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": "Error changing password: " + verifyStderr})
		return
	}
	if strings.Contains(verifyOut, "P") {
		writeJSON(w, map[string]interface{}{"success": true, "message": "SSH password changed successfully!"})
		return
	}
	writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": "Password change verification failed."})
}

// --- reboot ---

// apiRebootGracefulRun/apiRebootHardRun are injectable so tests never
// actually reboot the machine. Unlike the HTML /server/reboot page's
// rebootGracefulRun/rebootHardRun (reboot.go), which use Start() so the
// "reboot in progress" page can be flushed to the browser first, this route
// runs the command with Run() and blocks until it returns (i.e. until the
// 15s/10s sleep elapses and the reboot/sysrq write happens) before
// responding -- there's no HTML page to render first here, so there's
// nothing gained by returning early.
var (
	apiRebootGracefulRun = func() error {
		return exec.Command("sh", "-c", "sleep 15 && reboot").Run()
	}
	apiRebootHardRun = func() error {
		return exec.Command("sh", "-c", "sleep 10 && echo b > /proc/sysrq-trigger").Run()
	}
)

// ServeReboot handles POST /api/server/reboot.
func (a *APIServerOps) ServeReboot(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Stat(RebootDisableFlagPath); err == nil {
		writeJSONError(w, http.StatusForbidden, "Server Reboot access is disabled.")
		return
	}

	var body struct {
		RebootType string `json:"reboot_type"`
	}
	if !apiDecodeJSONBody(r, &body) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	var runErr error
	switch body.RebootType {
	case "graceful":
		runErr = apiRebootGracefulRun()
	case "hard":
		runErr = apiRebootHardRun()
	default:
		writeJSONError(w, http.StatusBadRequest, "reboot_type must be 'graceful' or 'hard'")
		return
	}
	if runErr != nil {
		writeJSONError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	writeJSON(w, map[string]interface{}{"success": true, "reboot_started": true})
}
