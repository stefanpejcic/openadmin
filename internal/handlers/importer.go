// This file implements cPanel/CyberPanel/OpenPanel backup import and
// OpenPanel-to-OpenPanel account transfer tooling. This is an
// Enterprise-only feature, but no Enterprise gate is added here since no
// other module's routes are gated that way either -- see the license
// package's RequireEnterprise if that needs wiring in later.
package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
	"openadmin/internal/webtemplates"
)

// Importer bundles the /user/import, /import/*, and /json/{backup-files,transfers}
// handlers.
type Importer struct {
	Sessions *auth.Manager
	MySQL    *sql.DB
}

// ImporterOpenPanelImportLogDir / ImporterTransferLogDir / ImporterRestoreTempDir
// are the hardcoded paths used by the import/transfer tooling.
var (
	ImporterOpenPanelImportLogDir = "/var/log/openpanel/admin/imports/"
	ImporterTransferLogDir        = "/var/log/openpanel/admin/transfers/"
	ImporterRestoreTempDir        = "/home/temprestoreditforopbackup"
)

// isPidRunning is true only if `ps -p PID` exits zero. Any failure
// (including ps itself being unavailable) is treated as "not running".
func isPidRunning(pid int) bool {
	return exec.Command("ps", "-p", strconv.Itoa(pid)).Run() == nil
}

type importLogStatus struct {
	Filename string `json:"filename"`
	Status   string `json:"status"`
}

// determineLogStatus is the log-status logic shared by
// ServeImportFromBackup and ServeImportTransfer's GET branches.
func determineLogStatus(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "unknown"
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return "unknown"
	}

	pidLine := ""
	if len(lines) > 1 {
		pidLine = strings.TrimSpace(lines[1])
	}
	pid, havePID := 0, false
	if idx := strings.Index(pidLine, "PID:"); idx != -1 {
		if n, err := strconv.Atoi(strings.TrimSpace(pidLine[idx+len("PID:"):])); err == nil {
			pid, havePID = n, true
		}
	}

	if havePID && isPidRunning(pid) {
		return "running"
	}

	lastLine := strings.TrimSpace(lines[len(lines)-1])
	switch {
	case strings.Contains(lastLine, "SUCCESS:"):
		return "completed"
	case strings.Contains(lastLine, "FATAL ERROR:"):
		return "failed"
	default:
		return "unknown"
	}
}

func listLogFilesWithStatus(logDir string) []importLogStatus {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return []importLogStatus{}
	}
	result := []importLogStatus{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".log") {
			continue
		}
		result = append(result, importLogStatus{Filename: name, Status: determineLogStatus(filepath.Join(logDir, name))})
	}
	return result
}

// ServeImportUser handles GET /user/import.
func (im *Importer) ServeImportUser(w http.ResponseWriter, r *http.Request) {
	plans, _ := paneldb.GetAllPlans(im.MySQL, nil)
	webtemplates.Render(w, "users_import_import.html", mergeChrome(map[string]interface{}{
		"Plans":     plans,
		"CSRFToken": csrf.Token(r),
		"Flashes":   auth.PopFlashes(w, r, im.Sessions),
	}, r, "Import User"))
}

var importerBackupPanelDisplayNames = map[string]string{
	"cpanel":     "cPanel",
	"cyberpanel": "CyberPanel",
	"openpanel":  "OpenPanel",
}

// importerRestoreOpenPanelBackupRun starts a fire-and-forget background
// restore for the openpanel backup type.
var importerRestoreOpenPanelBackupRun = func(backupPath string) error {
	return exec.Command("opencli", "user-restore", "--file", backupPath,
		"--temp-dir="+ImporterRestoreTempDir).Start()
}

// importerCloneAndRunImportScriptRun handles the cpanel/cyberpanel case:
// removes any stale clone dir, clones the panel-specific import repo (a
// clone failure is reported distinctly, under a "warning" category with
// the git stderr), then fires off the import script in the background.
var importerCloneAndRunImportScriptRun = func(displayName, backupPath, planName string) (cloneFailed bool, err error) {
	tempDir := "/tmp/" + displayName + "-to-OpenPanel/"
	repoURL := "https://github.com/stefanpejcic/" + displayName + "-to-OpenPanel"
	importScript := filepath.Join(tempDir, "cp-import.sh")

	if _, statErr := os.Stat(tempDir); statErr == nil {
		exec.Command("rm", "-rf", tempDir).Run()
	}

	var stderr strings.Builder
	cloneCmd := exec.Command("git", "clone", repoURL, tempDir)
	cloneCmd.Stderr = &stderr
	if err := cloneCmd.Run(); err != nil {
		return true, errors.New(stderr.String())
	}

	importCmd := exec.Command("bash", importScript,
		fmt.Sprintf("--backup-location='%s'", backupPath),
		fmt.Sprintf("--plan-name='%s'", planName))
	if err := importCmd.Start(); err != nil {
		return false, err
	}
	return false, nil
}

// ServeImportFromBackup handles GET/POST /import/{panel_type}.
func (im *Importer) ServeImportFromBackup(w http.ResponseWriter, r *http.Request) {
	panelType := strings.ToLower(r.PathValue("panel_type"))
	displayName, ok := importerBackupPanelDisplayNames[panelType]
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Unsupported panel type: "+panelType)
		return
	}

	if r.Method == http.MethodPost {
		r.ParseForm()
		backupPath := r.PostFormValue("path")
		planName := r.PostFormValue("plan_name")

		if panelType == "openpanel" {
			if backupPath == "" {
				auth.AddFlash(w, r, im.Sessions, "Backup file is required.", "danger")
				http.Redirect(w, r, "/user/import", http.StatusSeeOther)
				return
			}
			if err := importerRestoreOpenPanelBackupRun(backupPath); err != nil {
				auth.AddFlash(w, r, im.Sessions, err.Error(), "danger")
				http.Redirect(w, r, "/user/import", http.StatusSeeOther)
				return
			}
			auth.AddFlash(w, r, im.Sessions, fmt.Sprintf("Import process from %s (OpenPanel backup) has started.", backupPath), "success")
			http.Redirect(w, r, "/import/"+panelType, http.StatusSeeOther)
			return
		}

		if backupPath == "" || planName == "" {
			auth.AddFlash(w, r, im.Sessions, "Backup path and plan name are required.", "danger")
			http.Redirect(w, r, "/user/import", http.StatusSeeOther)
			return
		}

		cloneFailed, err := importerCloneAndRunImportScriptRun(displayName, backupPath, planName)
		if err != nil {
			if cloneFailed {
				auth.AddFlash(w, r, im.Sessions, "Error during execution: "+err.Error(), "warning")
			} else {
				auth.AddFlash(w, r, im.Sessions, err.Error(), "danger")
			}
			http.Redirect(w, r, "/user/import", http.StatusSeeOther)
			return
		}
		auth.AddFlash(w, r, im.Sessions, fmt.Sprintf("Import process from %s (%s backup) has started.", backupPath, displayName), "success")
		http.Redirect(w, r, "/import/"+panelType, http.StatusSeeOther)
		return
	}

	webtemplates.Render(w, "users_import_list_import_logs.html", mergeChrome(map[string]interface{}{
		"LogFiles": listLogFilesWithStatus(ImporterOpenPanelImportLogDir),
		"Flashes":  auth.PopFlashes(w, r, im.Sessions),
	}, r, "Account Imports"))
}

// serveImportLog is shared by ServeViewTransferImportLog and
// ServeViewAccountImportLog, which are otherwise identical aside from
// which log directory they read from.
func serveImportLog(w http.ResponseWriter, r *http.Request, logDir string) {
	logFilename := r.PathValue("log_filename")
	wantJSON := r.URL.Query().Get("output") == "json"

	logPath, ok := safeJoinOr400(logDir, logFilename)
	if !ok {
		if wantJSON {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"status": "error", "message": "Invalid log filename."})
			return
		}
		http.Error(w, "Invalid log filename.", http.StatusBadRequest)
		return
	}

	info, err := os.Stat(logPath)
	if err != nil || info.IsDir() {
		if wantJSON {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"status": "error", "message": "Log file does not exist."})
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Log file does not exist."))
		return
	}

	content, err := os.ReadFile(logPath)
	if err != nil {
		if wantJSON {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"status": "error", "message": "Error accessing log file: " + err.Error()})
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Error accessing log file: " + err.Error()))
		return
	}

	if wantJSON {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "success", "log": string(content)})
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write(content)
}

// ServeViewTransferImportLog handles GET /import/user/log/{log_filename}.
func (im *Importer) ServeViewTransferImportLog(w http.ResponseWriter, r *http.Request) {
	serveImportLog(w, r, ImporterTransferLogDir)
}

// ServeViewAccountImportLog handles GET /import/account/log/{log_filename}.
func (im *Importer) ServeViewAccountImportLog(w http.ResponseWriter, r *http.Request) {
	serveImportLog(w, r, ImporterOpenPanelImportLogDir)
}

var importerBackupSearchDirs = []string{"/", "/home", "/root"}
var importerBackupPatterns = []string{`^backup-.*\.tar\.gz$`, `^.*_\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}\.tar\.gz$`}

// ServeListBackupFiles handles GET /json/backup-files: a shallow
// (non-recursive) scan of /, /home, and /root for cPanel/CyberPanel
// (backup-*.tar.gz) or OpenPanel (<user>_<date>_<time>.tar.gz) backups.
func (im *Importer) ServeListBackupFiles(w http.ResponseWriter, r *http.Request) {
	var backupRes []*regexp.Regexp
	for _, p := range importerBackupPatterns {
		backupRes = append(backupRes, regexp.MustCompile(p))
	}

	seen := map[string]bool{}
	backups := []string{}
	for _, dir := range importerBackupSearchDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			matched := false
			for _, re := range backupRes {
				if re.MatchString(name) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
			full := filepath.Join(dir, name)
			if !seen[full] {
				seen[full] = true
				backups = append(backups, full)
			}
		}
	}
	writeJSON(w, backups)
}

// ServeListTransfers handles GET /json/transfers: every *.log file
// directly under the transfers dir.
func (im *Importer) ServeListTransfers(w http.ResponseWriter, r *http.Request) {
	matches, _ := filepath.Glob(filepath.Join(ImporterTransferLogDir, "*.log"))
	if matches == nil {
		matches = []string{}
	}
	writeJSON(w, matches)
}

type transferStatus struct {
	Filename string `json:"filename"`
	File     string `json:"file"`
	Status   string `json:"status"`
	PID      *int   `json:"pid"`
	Error    string `json:"error,omitempty"`
}

// importerPidAlive has a 3-way outcome: nil (alive, signalable), ESRCH
// ("no such process"), or any other error (e.g. EPERM: the process exists
// but belongs to another user). Only ESRCH is treated as "not running"
// below; any other error falls through to t.Status = "error" instead.
// Injectable for tests.
var importerPidAlive = func(pid int) error {
	return syscall.Kill(pid, 0)
}

// scanTransfersForUsername scans the transfer log directory for logs
// belonging to username and reports each one's current status. Shared by
// the HTML transfers-for-user route and its JSON API counterpart -- the
// two differ only in how they authorize the caller, not in how they read
// the logs.
func scanTransfersForUsername(username string) []transferStatus {
	matches, _ := filepath.Glob(filepath.Join(ImporterTransferLogDir, username+"_*.log"))
	transfers := make([]transferStatus, 0, len(matches))
	for _, path := range matches {
		t := transferStatus{Filename: filepath.Base(path), File: path, Status: "unknown"}

		raw, err := os.ReadFile(path)
		if err != nil {
			t.Status = "error"
			t.Error = err.Error()
			transfers = append(transfers, t)
			continue
		}
		lines := strings.Split(string(raw), "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}

		var pidLine string
		for _, l := range lines {
			if strings.Contains(l, "PID:") {
				pidLine = l
				break
			}
		}

		if pidLine == "" {
			t.Status = "failed"
			transfers = append(transfers, t)
			continue
		}

		idx := strings.Index(pidLine, "PID:")
		pid, perr := strconv.Atoi(strings.TrimSpace(pidLine[idx+len("PID:"):]))
		if perr != nil {
			t.Status = "error"
			t.Error = perr.Error()
			transfers = append(transfers, t)
			continue
		}
		t.PID = &pid

		switch err := importerPidAlive(pid); {
		case err == nil:
			t.Status = "in progress"
		case errors.Is(err, syscall.ESRCH):
			if len(lines) > 0 && strings.Contains(lines[len(lines)-1], "SUCCESS:") {
				t.Status = "success"
			} else {
				t.Status = "failed"
			}
		default:
			t.Status = "error"
			t.Error = err.Error()
		}
		transfers = append(transfers, t)
	}
	return transfers
}

// ServeListTransfersFor handles GET /json/transfers/{username}.
func (im *Importer) ServeListTransfersFor(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	if strings.Contains(username, "SUSPENDED_") {
		if idx := strings.LastIndex(username, "_"); idx != -1 {
			username = username[idx+1:]
		}
	}

	cu := auth.CurrentUser(r)
	actingUsername, actingRole := "", ""
	if cu != nil {
		actingUsername, actingRole = cu.Username, cu.Role
	}
	if !paneldb.CheckIfOwnerForUser(im.MySQL, username, actingUsername, actingRole) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	writeJSON(w, scanTransfersForUsername(username))
}

var importerDomainRe = regexp.MustCompile(`^(?:[a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}$`)

func isDomain(server string) bool {
	return importerDomainRe.MatchString(server)
}

// configureIptablesRun sets a temporary ConfigServer Firewall allow rule
// for an IP-addressed (non-domain) transfer target.
var configureIptablesRun = func(server string) bool {
	return exec.Command("csf", "-ta", server, "3600").Run() == nil
}

// importerStartTransferRun fires off `opencli user-transfer` in the
// background without waiting for it to complete.
var importerStartTransferRun = func(args []string) error {
	return exec.Command(args[0], args[1:]...).Start()
}

// ServeImportTransfer handles GET/POST /import/transfer/.
func (im *Importer) ServeImportTransfer(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		openpanelUsername := r.PostFormValue("openpanel_username")
		server := r.PostFormValue("server")
		username := r.PostFormValue("username")
		if username == "" {
			username = "root"
		}
		password := r.PostFormValue("password")
		port := r.PostFormValue("port")
		liveTransfer := r.PostFormValue("live_transfer")

		if openpanelUsername == "" || server == "" {
			auth.AddFlash(w, r, im.Sessions, "Error: Server IP and OpenPanel username are required.", "danger")
			http.Redirect(w, r, "/users/"+openpanelUsername+"#transfer", http.StatusSeeOther)
			return
		}

		args := []string{"opencli", "user-transfer", "--account", openpanelUsername, "--host", server, "--username", username}
		if password != "" {
			args = append(args, "--password", password)
		}
		if port != "" {
			args = append(args, "--port", port)
		}
		if liveTransfer != "" {
			args = append(args, "--live-transfer")
		}

		if !isDomain(server) {
			configureIptablesRun(server)
		}

		if err := importerStartTransferRun(args); err != nil {
			auth.AddFlash(w, r, im.Sessions, "Error starting transfer: "+err.Error(), "danger")
		} else {
			auth.AddFlash(w, r, im.Sessions, fmt.Sprintf("Transfer process for account %s started in the background.", openpanelUsername), "success")
		}
		http.Redirect(w, r, "/users/"+openpanelUsername+"#transfer", http.StatusSeeOther)
		return
	}

	webtemplates.Render(w, "users_import_import_users.html", mergeChrome(map[string]interface{}{
		"LogFiles": listLogFilesWithStatus(ImporterTransferLogDir),
		"Flashes":  auth.PopFlashes(w, r, im.Sessions),
	}, r, "Account Transfer Logs"))
}
