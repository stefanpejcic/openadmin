// This file implements the log viewer (with its admin-configurable
// log_paths.json), the log-path editor, and the separate update-logs /
// crash-logs viewer pages.
package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/podman"
	"openadmin/internal/webtemplates"
)

// Logs bundles the /services/logs* and /settings/updates/log* handlers.
type Logs struct {
	Sessions *auth.Manager
}

// LogPathsFile is the admin-configurable JSON file mapping log names to
// paths.
var LogPathsFile = "/etc/openpanel/openadmin/config/log_paths.json"

// UpdateLogsDir / CrashLogsDir are the fixed directories the update-logs
// and crash-logs viewer pages glob over.
var (
	UpdateLogsDir = "/var/log/openpanel/updates"
	CrashLogsDir  = "/var/log/openpanel/admin/crashlog"
)

// fallbackLogFiles is used when LogPathsFile is missing or invalid.
var fallbackLogFiles = map[string]string{
	"OpenAdmin Access Log":    "/var/log/openpanel/admin/access.log",
	"OpenAdmin Error Log":     "/var/log/openpanel/admin/error.log",
	"OpenAdmin API Log":       "/var/log/openpanel/admin/api.log",
	"OpenAdmin Login Log":     "/var/log/openpanel/admin/login.log",
	"OpenAdmin Notifications": "/var/log/openpanel/admin/notifications.log",
	"OpenAdmin Crons Log":     "/var/log/openpanel/admin/cron.log",
	"OpenPanel Access Log":    "/var/log/openpanel/user/access.log",
	"OpenPanel Error Log":     "openpanel",
	"OpenCLI Logs":            "/var/log/openpanel/admin/opencli.log",
	"MailServer Log":          "openadmin_mailserver",
	"Caddy Logs":              "caddy",
	"MySQL Logs":              "openpanel_mysql",
	"FTP Logs":                "openadmin_ftp",
	"CSF Deny Log":            "/etc/csf/csf.deny",
	"AuthLog":                 "/var/log/auth.log",
	"DPKG Log":                "/var/log/dpkg.log",
	"Syslog":                  "/var/log/syslog",
}

// protectedContainerLogs mirrors the DELETE branch's hardcoded name list --
// these are backed by auto-rotated podman container logs, not plain files.
var protectedContainerLogs = map[string]bool{
	"MySQL Logs":          true,
	"Caddy Logs":          true,
	"OpenPanel Error Log": true,
	"FTP Logs":            true,
}

// loadLogPaths always reads fresh from LogPathsFile rather than caching --
// caching would be a performance detail, not a behavioral contract, and
// this file is small and infrequently read.
func loadLogPaths() map[string]string {
	raw, err := os.ReadFile(LogPathsFile)
	if err != nil {
		return fallbackLogFiles
	}
	var parsed map[string]string
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return fallbackLogFiles
	}
	return parsed
}

// isDigitsOnly reports whether s is non-empty and contains only digits (no sign).
func isDigitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// tailLogLines keeps only the last n lines, each with its own original
// line ending preserved, rather than a whole-file re-join.
func tailLogLines(raw string, n int) string {
	lines := strings.SplitAfter(raw, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if n < len(lines) {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "")
}

// getDockerLogRun is injectable so tests never shell out to a real podman
// binary, matching the ftpPsRun/ftpRefreshRun pattern used elsewhere.
var getDockerLogRun = func(container, lines string) (string, error) {
	args := []string{"logs"}
	if isDigitsOnly(lines) {
		args = append(args, "--tail", lines)
	}
	args = append(args, container)
	cmd, err := podman.Command("default", args...)
	if err != nil {
		return "", err
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// getDockerLog captures errors into the returned string rather than
// propagating them, so the caller always gets HTTP 200 with a "content"
// field, even on failure.
func getDockerLog(container, lines string) string {
	out, err := getDockerLogRun(container, lines)
	if err != nil {
		return fmt.Sprintf("Error retrieving logs from %s: %s", container, out)
	}
	return out
}

type logEntry struct{ Name, Path string }

func sortedLogEntries(logFiles map[string]string, prefixes ...string) []logEntry {
	names := make([]string, 0, len(logFiles))
	for name := range logFiles {
		match := len(prefixes) == 0
		for _, p := range prefixes {
			if strings.HasPrefix(name, p) {
				match = true
			}
		}
		if match {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	entries := make([]logEntry, 0, len(names))
	for _, name := range names {
		entries = append(entries, logEntry{Name: name, Path: logFiles[name]})
	}
	return entries
}

// ServeIndex handles GET /services/logs.
func (l *Logs) ServeIndex(w http.ResponseWriter, r *http.Request) {
	logFiles := loadLogPaths()

	allNames := make([]string, 0, len(logFiles))
	for name := range logFiles {
		if !strings.HasPrefix(name, "OpenAdmin") && !strings.HasPrefix(name, "OpenPanel") {
			allNames = append(allNames, name)
		}
	}

	webtemplates.Render(w, "services_logs.html", mergeChrome(map[string]interface{}{
		"OpenAdminLogs": sortedLogEntries(logFiles, "OpenAdmin"),
		"OpenPanelLogs": sortedLogEntries(logFiles, "OpenPanel"),
		"OtherLogs":     sortedLogEntries(logFiles, allNames...),
		"CSRFToken":     csrf.Token(r),
		"Flashes":       auth.PopFlashes(w, r, l.Sessions),
	}, r, "Log Viewer"))
}

// ServeEditLogs handles GET/POST /services/logs/edit.
func (l *Logs) ServeEditLogs(w http.ResponseWriter, r *http.Request) {
	var data interface{} = map[string]interface{}{}

	if r.Method == http.MethodPost {
		r.ParseForm()
		newData := strings.TrimSpace(r.PostFormValue("data"))

		var parsed interface{}
		if err := json.Unmarshal([]byte(newData), &parsed); err != nil {
			auth.AddFlash(w, r, l.Sessions, "Invalid JSON data: "+err.Error(), "error")
			// Redirects back to this page rather than to the unrelated
			// /services/edit config editor.
			http.Redirect(w, r, "/services/logs/edit", http.StatusSeeOther)
			return
		}

		encoded, err := json.MarshalIndent(parsed, "", "    ")
		if err == nil {
			err = os.WriteFile(LogPathsFile, encoded, 0644)
		}
		if err != nil {
			auth.AddFlash(w, r, l.Sessions, "Error saving the file: "+err.Error()+". Please edit via terminal: "+LogPathsFile, "error")
		} else {
			auth.AddFlash(w, r, l.Sessions, "Config file updated successfully.", "success")
		}
	}

	if raw, err := os.ReadFile(LogPathsFile); err == nil {
		var parsed interface{}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			auth.AddFlash(w, r, l.Sessions, "Error: Invalid JSON format in config file.", "error")
		} else {
			data = parsed
		}
	}

	if r.URL.Query().Has("json") {
		writeJSON(w, data)
		return
	}

	pretty, _ := json.MarshalIndent(data, "", "  ")
	webtemplates.Render(w, "services_edit_logs.html", mergeChrome(map[string]interface{}{
		"Data":      string(pretty),
		"CSRFToken": csrf.Token(r),
		"Flashes":   auth.PopFlashes(w, r, l.Sessions),
	}, r, "Edit Log Paths"))
}

// ServeViewLog handles GET/POST/DELETE /services/logs/raw.
func (l *Logs) ServeViewLog(w http.ResponseWriter, r *http.Request) {
	logName := r.URL.Query().Get("log_name")
	linesParam := r.URL.Query().Get("lines")

	logFiles := loadLogPaths()
	logPath, ok := logFiles[logName]
	if !ok || logPath == "" {
		http.Error(w, "Unauthorized or unknown log", http.StatusForbidden)
		return
	}

	isContainer := !strings.Contains(logPath, "/") && len(strings.Fields(logPath)) == 1

	switch r.Method {
	case http.MethodGet:
		if isContainer {
			writeJSON(w, map[string]string{"content": getDockerLog(logPath, linesParam)})
			return
		}

		raw, err := os.ReadFile(logPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeJSONError(w, http.StatusNotFound, "Log file not found")
			} else {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}

		var content string
		if linesParam == "ALL" {
			content = string(raw)
		} else {
			n, err := strconv.Atoi(strings.TrimSpace(linesParam))
			if err == nil && n < 0 {
				err = errors.New("maxlen must be non-negative")
			}
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			content = tailLogLines(string(raw), n)
		}
		writeJSON(w, map[string]string{"content": content})

	case http.MethodPost:
		serveLogDownload(w, r, logPath)

	case http.MethodDelete:
		if protectedContainerLogs[logName] {
			writeJSONError(w, http.StatusForbidden, "Cannot delete logs from Podman container - they are auto-rotated.")
			return
		}
		// Container-backed entries are rejected outright here (there's no
		// real file to truncate), and O_CREATE is deliberately not passed
		// to os.OpenFile below, so a missing plain-file entry reports 404
		// instead of being silently created as an empty file.
		if isContainer {
			writeJSONError(w, http.StatusForbidden, "Cannot delete logs from Podman container - they are auto-rotated.")
			return
		}
		f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			if os.IsNotExist(err) {
				writeJSONError(w, http.StatusNotFound, "Log file not found")
			} else {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		f.Close()
		writeJSON(w, map[string]string{"message": fmt.Sprintf("Log file %s emptied.", logPath)})
	}
}

func serveLogDownload(w http.ResponseWriter, r *http.Request, path string) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSONError(w, http.StatusNotFound, "Log file not found")
		} else {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)
}

// safeJoinOr400 joins baseDir and filename while blocking ".." traversal
// out of baseDir. Go's filepath.EvalSymlinks errors on missing paths, so
// this only resolves symlinks on the (expected-to-exist) base directory
// and filepath.Clean-joins the filename on top, without requiring the
// target log file to already exist.
func safeJoinOr400(baseDir, filename string) (string, bool) {
	if filename == "" {
		return "", false
	}
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return "", false
	}
	resolvedBase, err := filepath.EvalSymlinks(baseAbs)
	if err != nil {
		resolvedBase = baseAbs
	}
	candidate := filepath.Clean(filepath.Join(resolvedBase, filename))
	if candidate != resolvedBase && !strings.HasPrefix(candidate, resolvedBase+string(os.PathSeparator)) {
		return "", false
	}
	return candidate, true
}

func loadGlobLogFiles(dir, pattern string) []string {
	if _, err := os.Stat(dir); err != nil {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(dir, pattern))
	sort.Strings(matches)
	return matches
}

// ServeUpdateLogsSettings handles GET/POST /settings/updates/log/.
func (l *Logs) ServeUpdateLogsSettings(w http.ResponseWriter, r *http.Request) {
	webtemplates.Render(w, "services_update_logs.html", mergeChrome(map[string]interface{}{
		"LogFiles":  loadGlobLogFiles(UpdateLogsDir, "*.log"),
		"CSRFToken": csrf.Token(r),
		"Flashes":   auth.PopFlashes(w, r, l.Sessions),
	}, r, "Update Logs Viewer"))
}

// ServeCrashlogsSettings handles GET/POST /services/crashlogs/log/.
func (l *Logs) ServeCrashlogsSettings(w http.ResponseWriter, r *http.Request) {
	webtemplates.Render(w, "services_crash_logs.html", mergeChrome(map[string]interface{}{
		"LogFiles":  loadGlobLogFiles(CrashLogsDir, "*.txt"),
		"CSRFToken": csrf.Token(r),
		"Flashes":   auth.PopFlashes(w, r, l.Sessions),
	}, r, "Crash Logs Viewer"))
}

// serveRawInDir is the shared GET/POST/DELETE implementation behind
// ServeViewUpdateLog and ServeViewCrashlogsLog, which are identical except
// for the directory they're jailed to.
func serveRawInDir(w http.ResponseWriter, r *http.Request, baseDir string) {
	logName := r.URL.Query().Get("log_name")
	logPath, ok := safeJoinOr400(baseDir, logName)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid log file")
		return
	}

	switch r.Method {
	case http.MethodGet:
		raw, err := os.ReadFile(logPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeJSONError(w, http.StatusNotFound, "Log file not found")
			} else {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		writeJSON(w, map[string]string{"content": string(raw)})

	case http.MethodPost:
		serveLogDownload(w, r, logPath)

	case http.MethodDelete:
		// This genuinely deletes the file, unlike ServeViewLog's DELETE
		// (which truncates to 0 bytes). The "emptied" wording in the
		// success message below is inherited from that other handler and
		// describes the wrong operation, but is left as-is.
		if _, err := os.Stat(logPath); err != nil {
			writeJSONError(w, http.StatusNotFound, "Log file not found")
			return
		}
		if err := os.Remove(logPath); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]string{"message": fmt.Sprintf("Log file %s emptied.", logPath)})
	}
}

// ServeViewUpdateLog handles GET/POST/DELETE /services/updates/log/raw.
func (l *Logs) ServeViewUpdateLog(w http.ResponseWriter, r *http.Request) {
	serveRawInDir(w, r, UpdateLogsDir)
}

// ServeViewCrashlogsLog handles GET/POST/DELETE /services/crashlogs/log/raw.
func (l *Logs) ServeViewCrashlogsLog(w http.ResponseWriter, r *http.Request) {
	serveRawInDir(w, r, CrashLogsDir)
}
