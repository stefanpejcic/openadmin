// This file implements kicking off a background `opencli server-migrate`
// run and polling its progress/completion.
package handlers

import (
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// Migrate bundles the /server/migrate* handlers.
type Migrate struct {
	Sessions *auth.Manager
}

// MigrateLogPath / MigrateProcessPIDFile are the paths used to track the
// background migration process.
var (
	MigrateLogPath        = "/tmp/server_migrate.log"
	MigrateProcessPIDFile = "/tmp/server_migrate.pid"
)

// migrateStartRun is injectable so tests never spawn a real opencli
// process, matching the ftpPsRun/rebootGracefulRun pattern used elsewhere.
// It starts the migration non-blocking (the handler returns immediately),
// with stdout/stderr redirected to MigrateLogPath and the child's PID
// recorded to MigrateProcessPIDFile.
var migrateStartRun = func(host, root, password string) error {
	logFile, err := os.Create(MigrateLogPath)
	if err != nil {
		return err
	}
	defer logFile.Close()

	cmd := exec.Command("opencli", "server-migrate", "-h", host, "--user", root, "--password", password)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return err
	}

	return os.WriteFile(MigrateProcessPIDFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0644)
}

// processAlive is true if the pid belongs to a live process, even one we
// don't have permission to signal.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrProcessDone) {
		return false
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && errno == syscall.EPERM {
		return true
	}
	return false
}

// ServeMigrate handles GET/POST /server/migrate.
func (m *Migrate) ServeMigrate(w http.ResponseWriter, r *http.Request) {
	migrateStarted := false

	if r.Method == http.MethodPost {
		r.ParseForm()
		host := r.PostFormValue("host")
		root := r.PostFormValue("root")
		password := r.PostFormValue("password")

		if host == "" || root == "" || password == "" {
			auth.AddFlash(w, r, m.Sessions, "All fields are required.", "error")
			http.Redirect(w, r, "/server/migrate", http.StatusSeeOther)
			return
		}

		if err := migrateStartRun(host, root, password); err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		migrateStarted = true
		auth.AddFlash(w, r, m.Sessions, "Migration process started.", "success")
	}

	webtemplates.Render(w, "server_migrate.html", mergeChrome(map[string]interface{}{
		"MigrateStarted": migrateStarted,
		"CSRFToken":      csrf.Token(r),
		"Flashes":        auth.PopFlashes(w, r, m.Sessions),
	}, r, "Server Migration"))
}

// ServeMigrateStatus handles GET /server/migrate/status.
func (m *Migrate) ServeMigrateStatus(w http.ResponseWriter, r *http.Request) {
	status := "running"
	output := ""

	if raw, err := os.ReadFile(MigrateLogPath); err == nil {
		output = string(raw)
	}

	if raw, err := os.ReadFile(MigrateProcessPIDFile); err == nil {
		pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
		if convErr != nil {
			// A corrupt pid file produces a generic 500 rather than being
			// silently treated as some other status.
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		if !processAlive(pid) {
			status = "finished"
		}
	} else {
		status = "unknown"
	}

	writeJSON(w, map[string]string{"status": status, "output": output})
}
