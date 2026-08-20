// This file implements the Users > <username> > Export tab's "Generate
// full account backup" option: the same single-click whole-account backup
// flow as OpenPanel's self-service Backup Wizard (opencli user-backup),
// triggered here by an Administrator for a specific hosting account, plus
// a delete option OpenPanel's own wizard doesn't have.
package handlers

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
)

// userExportHomeRoot is injectable so tests don't touch the real /home.
var userExportHomeRoot = "/home"

func userExportBackupDir(context string) string {
	return userExportHomeRoot + "/" + context + "/docker-data/volumes/" + context + "_html_data/_data/_backups"
}

// userExportIsBackupInProgressRun is injectable for tests.
var userExportIsBackupInProgressRun = func(username string) bool {
	return exec.Command("pgrep", "-f", "user-backup.*--account.*"+username).Run() == nil
}

func userExportFormatSize(numBytes float64) string {
	for _, unit := range []string{"B", "KB", "MB", "GB"} {
		if numBytes < 1024.0 {
			return fmt.Sprintf("%.1f %s", numBytes, unit)
		}
		numBytes /= 1024.0
	}
	return fmt.Sprintf("%.1f TB", numBytes)
}

var userExportInProgressLogNameRE = regexp.MustCompile(`_backup_(\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2})\.log$`)

// userExportInProgressInfo mirrors OpenPanel's backupwizard.inProgressInfo:
// (started, currentSize, inProgressFilename) for the currently-running
// backup, best-effort from the newest matching log file and the newest
// .tar.gz on disk.
func userExportInProgressInfo(username, context string) (started, currentSize, inProgressFile string) {
	logDir := "/var/log/openpanel/admin/backups"
	if entries, err := os.ReadDir(logDir); err == nil {
		type logEntry struct {
			name    string
			modTime time.Time
		}
		var logs []logEntry
		prefix := username + "_backup_"
		for _, e := range entries {
			if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) || !strings.HasSuffix(e.Name(), ".log") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			logs = append(logs, logEntry{name: e.Name(), modTime: info.ModTime()})
		}
		sort.Slice(logs, func(i, j int) bool { return logs[i].modTime.After(logs[j].modTime) })
		if len(logs) > 0 {
			if m := userExportInProgressLogNameRE.FindStringSubmatch(logs[0].name); m != nil {
				datePart, timePart, _ := strings.Cut(m[1], "_")
				started = datePart + " " + strings.ReplaceAll(timePart, "-", ":")
			}
		}
	}

	dir := userExportBackupDir(context)
	if entries, err := os.ReadDir(dir); err == nil {
		type tarEntry struct {
			name    string
			modTime time.Time
			size    int64
		}
		var tars []tarEntry
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar.gz") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			tars = append(tars, tarEntry{name: e.Name(), modTime: info.ModTime(), size: info.Size()})
		}
		sort.Slice(tars, func(i, j int) bool { return tars[i].modTime.After(tars[j].modTime) })
		if len(tars) > 0 {
			inProgressFile = tars[0].name
			currentSize = userExportFormatSize(float64(tars[0].size))
		}
	}
	return started, currentSize, inProgressFile
}

// userExportBackupFile is one entry of the Export tab's backup list.
type userExportBackupFile struct {
	Name       string `json:"name"`
	SizeRaw    int64  `json:"size_raw"`
	Size       string `json:"size"`
	Mtime      string `json:"mtime"`
	InProgress bool   `json:"in_progress"`
}

func userExportListBackups(context, inProgressFile string) []userExportBackupFile {
	dir := userExportBackupDir(context)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []userExportBackupFile{}
	}

	type fileInfo struct {
		entry   os.DirEntry
		modTime time.Time
	}
	var files []fileInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar.gz") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{entry: e, modTime: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.After(files[j].modTime) })

	result := make([]userExportBackupFile, 0, len(files))
	for _, f := range files {
		info, err := f.entry.Info()
		if err != nil {
			continue
		}
		result = append(result, userExportBackupFile{
			Name: f.entry.Name(), SizeRaw: info.Size(), Size: userExportFormatSize(float64(info.Size())),
			Mtime: info.ModTime().Format("2006-01-02 15:04:05"), InProgress: f.entry.Name() == inProgressFile,
		})
	}
	return result
}

type userExportStatusPayload struct {
	InProgress        bool                   `json:"in_progress"`
	InProgressStarted string                 `json:"in_progress_started,omitempty"`
	InProgressSize    string                 `json:"in_progress_size,omitempty"`
	Backups           []userExportBackupFile `json:"backups"`
}

func userExportStatus(username, context string) userExportStatusPayload {
	inProgress := userExportIsBackupInProgressRun(username)
	var started, size, inProgressFile string
	if inProgress {
		started, size, inProgressFile = userExportInProgressInfo(username, context)
	}
	return userExportStatusPayload{
		InProgress: inProgress, InProgressStarted: started, InProgressSize: size,
		Backups: userExportListBackups(context, inProgressFile),
	}
}

// ServeUserExportStatus handles GET /user/export/status/{username}.
func (u *Users) ServeUserExportStatus(w http.ResponseWriter, r *http.Request) {
	username := stripSuspendedPrefix(r.PathValue("username"))
	currentUser := auth.CurrentUser(r)
	if !paneldb.CheckIfOwnerForUser(u.MySQL, username, currentUser.Username, currentUser.Role) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	userData, err := paneldb.GetUserDataByUsername(u.MySQL, username)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "User not found.")
		return
	}
	writeJSON(w, userExportStatus(username, userData.Context))
}

// userExportBackupCmdRun is injectable so tests never actually shell out.
var userExportBackupCmdRun = func(username string) error {
	cmd := exec.Command("opencli", "user-backup", "--account", username)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// ServeUserExportCreate handles POST /user/export/create/{username}: fires
// `opencli user-backup --account <username>` in the background, matching
// OpenPanel's own self-service Backup Wizard.
func (u *Users) ServeUserExportCreate(w http.ResponseWriter, r *http.Request) {
	username := stripSuspendedPrefix(r.PathValue("username"))
	currentUser := auth.CurrentUser(r)
	if !paneldb.CheckIfOwnerForUser(u.MySQL, username, currentUser.Username, currentUser.Role) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	userData, err := paneldb.GetUserDataByUsername(u.MySQL, username)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "User not found.")
		return
	}

	if userExportIsBackupInProgressRun(username) {
		writeJSONError(w, http.StatusConflict, "A backup is already in progress for this account. Please wait for it to finish.")
		return
	}

	if err := os.MkdirAll(userExportBackupDir(userData.Context), 0755); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to start backup.")
		return
	}

	if err := userExportBackupCmdRun(username); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to start backup.")
		return
	}

	logUserAction(username, clientIP(r), "Administrator "+currentUser.Username+" started a full account backup for user "+username)
	writeJSON(w, map[string]interface{}{"scheduled": true})
}

// ServeUserExportDownload handles GET
// /user/export/download/{username}/{filename...}.
func (u *Users) ServeUserExportDownload(w http.ResponseWriter, r *http.Request) {
	username := stripSuspendedPrefix(r.PathValue("username"))
	currentUser := auth.CurrentUser(r)
	if !paneldb.CheckIfOwnerForUser(u.MySQL, username, currentUser.Username, currentUser.Role) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	userData, err := paneldb.GetUserDataByUsername(u.MySQL, username)
	if err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	filename := r.PathValue("filename")
	safeName := filepath.Base(filename)
	if safeName != filename || safeName == "." || safeName == "/" {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}

	dir := userExportBackupDir(userData.Context)
	filePath := filepath.Join(dir, safeName)
	resolvedDir, dirErr := filepath.Abs(dir)
	resolvedFile, fileErr := filepath.Abs(filePath)
	if dirErr != nil || fileErr != nil || !userExportIsWithin(resolvedFile, resolvedDir) {
		http.Error(w, "Invalid file path", http.StatusBadRequest)
		return
	}

	info, statErr := os.Stat(filePath)
	if statErr != nil {
		http.Error(w, "Backup file not found", http.StatusNotFound)
		return
	}

	if userExportIsBackupInProgressRun(username) {
		_, _, inProgressFile := userExportInProgressInfo(username, userData.Context)
		if inProgressFile == safeName {
			http.Error(w, "This backup is still being created. Please wait until it completes before downloading.", http.StatusConflict)
			return
		}
	}

	logUserAction(username, clientIP(r), "Administrator "+currentUser.Username+" downloaded backup "+safeName+" for user "+username)

	f, openErr := os.Open(filePath)
	if openErr != nil {
		http.Error(w, "Backup file not found", http.StatusNotFound)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+safeName+"\"")
	http.ServeContent(w, r, safeName, info.ModTime(), f)
}

// ServeUserExportDelete handles POST /user/export/delete/{username} (form
// value "filename"). OpenPanel's own self-service wizard has no delete
// option; this is Administrator-only cleanup.
func (u *Users) ServeUserExportDelete(w http.ResponseWriter, r *http.Request) {
	username := stripSuspendedPrefix(r.PathValue("username"))
	currentUser := auth.CurrentUser(r)
	if !paneldb.CheckIfOwnerForUser(u.MySQL, username, currentUser.Username, currentUser.Role) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	userData, err := paneldb.GetUserDataByUsername(u.MySQL, username)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "User not found.")
		return
	}

	r.ParseForm()
	filename := r.PostFormValue("filename")
	safeName := filepath.Base(filename)
	if safeName != filename || safeName == "." || safeName == "/" {
		writeJSONError(w, http.StatusBadRequest, "Invalid filename.")
		return
	}

	dir := userExportBackupDir(userData.Context)
	filePath := filepath.Join(dir, safeName)
	resolvedDir, dirErr := filepath.Abs(dir)
	resolvedFile, fileErr := filepath.Abs(filePath)
	if dirErr != nil || fileErr != nil || !userExportIsWithin(resolvedFile, resolvedDir) {
		writeJSONError(w, http.StatusBadRequest, "Invalid file path.")
		return
	}

	if userExportIsBackupInProgressRun(username) {
		_, _, inProgressFile := userExportInProgressInfo(username, userData.Context)
		if inProgressFile == safeName {
			writeJSONError(w, http.StatusConflict, "This backup is still being created and cannot be deleted yet.")
			return
		}
	}

	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			writeJSONError(w, http.StatusNotFound, "Backup file not found.")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "Failed to delete backup file.")
		return
	}

	logUserAction(username, clientIP(r), "Administrator "+currentUser.Username+" deleted backup "+safeName+" for user "+username)
	writeJSON(w, map[string]bool{"success": true})
}

func userExportIsWithin(candidate, base string) bool {
	rel, err := filepath.Rel(base, candidate)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && rel != "..")
}
