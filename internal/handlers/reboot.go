// This file implements the graceful/hard server reboot trigger and its
// post-reboot status probe.
package handlers

import (
	"net/http"
	"os"
	"os/exec"

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

// ServeReboot handles GET/POST /server/reboot.
func (rb *Reboot) ServeReboot(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Stat(RebootDisableFlagPath); err == nil {
		http.Error(w, "Server Reboot access is disabled.", http.StatusForbidden)
		return
	}

	rebootStarted := false
	if r.Method == http.MethodPost {
		r.ParseForm()

		var err error
		switch r.PostFormValue("reboot_type") {
		case "graceful":
			err = rebootGracefulRun()
		case "hard":
			err = rebootHardRun()
		}
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		rebootStarted = true
	}

	webtemplates.Render(w, "settings_reboot.html", mergeChrome(map[string]interface{}{
		"RebootStarted": rebootStarted,
		"CSRFToken":     csrf.Token(r),
		"Flashes":       auth.PopFlashes(w, r, rb.Sessions),
	}, r, "Server Reboot"))
}

// ServeRebootStatus handles GET /server/reboot/status.
func (rb *Reboot) ServeRebootStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"status": "up"})
}
