// This file implements the graceful/hard server reboot trigger and its
// post-reboot status probe.
package handlers

import (
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// Reboot bundles the /server/reboot* handlers.
type Reboot struct {
	Sessions *auth.Manager
}

var RebootDisableFlagPath = "/root/disable_openadmin_reboot_ui"

// rebootGracefulRun/rebootHardRun are injectable so tests never actually
// reboot the machine, matching the ftpPsRun/controlServiceRun pattern used
// throughout this package.
//
// A *blocking* subprocess call here would mean the handler never
// returns, so the "reboot in progress" HTML response (whose own JS is
// supposed to immediately start polling /server/reboot/status) would
// never actually get flushed to the browser before the reboot/sysrq
// command ran and the machine went down. Using Start() instead of Run()
// launches the command in the background and returns immediately, so
// the response can reach the browser before the machine restarts.
var (
	rebootGracefulRun = func() error {
		return exec.Command("sh", "-c", "sleep 15 && reboot").Start()
	}
	rebootHardRun = func() error {
		return exec.Command("sh", "-c", "sleep 10 && echo b > /proc/sysrq-trigger").Start()
	}
)

// changes on every boot, so the UI can tell the server actually came back up
var rebootBootIDPath = "/proc/sys/kernel/random/boot_id"

func currentBootID() string {
	b, err := os.ReadFile(rebootBootIDPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ServeReboot handles GET/POST /server/reboot.
func (rb *Reboot) ServeReboot(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Stat(RebootDisableFlagPath); err == nil {
		http.Error(w, "Server reboot access is disabled.", http.StatusForbidden)
		return
	}

	rebootStarted := false
	fieldError, pageError := "", ""
	status := http.StatusOK
	if r.Method == http.MethodPost {
		r.ParseForm()

		var err error
		switch r.PostFormValue("reboot_type") {
		case "graceful":
			err = rebootGracefulRun()
		case "hard":
			err = rebootHardRun()
		default:
			fieldError = "Pick graceful or hard reboot."
			status = http.StatusBadRequest
		}
		if err != nil {
			pageError = "The reboot didn't start: " + err.Error() + ". Check the server's logs, or reboot it from the terminal."
			status = http.StatusInternalServerError
		}
		rebootStarted = fieldError == "" && pageError == ""
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	webtemplates.Render(w, "settings_reboot.html", mergeChrome(map[string]interface{}{
		"RebootStarted": rebootStarted,
		"FieldError":    fieldError,
		"Error":         pageError,
		"BootID":        currentBootID(),
		"CSRFToken":     csrf.Token(r),
		"Flashes":       auth.PopFlashes(w, r, rb.Sessions),
	}, r, "Server reboot"))
}

// ServeRebootStatus handles GET /server/reboot/status.
func (rb *Reboot) ServeRebootStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"status": "up", "boot_id": currentBootID()})
}
