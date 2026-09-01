// This file implements the /backups/system page: view/edit the system
// config backup destination (backups.ini), trigger a new backup, list
// existing backup archives with one-click restore, and view past run
// history. All of the actual work (what gets archived, retention pruning,
// the archive/restore themselves) lives in `opencli backup` (opencli's
// backup.sh) -- this file only edits its config file, shells out to it, and
// renders what it already wrote to disk (the destination directory's
// archive files, and its JSON-lines run log). /backups/user is a
// placeholder for now.
package handlers

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/config"
	"openadmin/internal/webtemplates"
)

// Backups bundles the /backups/* handlers.
type Backups struct {
	Sessions *auth.Manager
}

var (
	BackupsConfigPath = "/etc/openpanel/openadmin/config/backups.ini"
	BackupsRunsPath   = "/var/log/openpanel/admin/system-backup-runs.jsonl"
)

// defaultBackupDestination mirrors the destination field's placeholder in
// backups_system.html: if no destination has ever been configured, this is
// what a backup run/settings save falls back to, instead of silently
// running (or letting the admin "run" a backup) with no destination set.
const defaultBackupDestination = "/etc/openpanel/backups/system"

type backupRun struct {
	Timestamp string `json:"timestamp"`
	Action    string `json:"action"`
	Status    string `json:"status"`
	Archive   string `json:"archive"`
	SizeBytes int64  `json:"size_bytes"`
	Duration  int    `json:"duration_seconds"`
	Detail    string `json:"detail"`
}

type backupArchiveRow struct {
	Name    string
	Size    string
	ModTime string
}

// backupsListRuns reads BackupsRunsPath (one JSON object per line, oldest
// first as written) and returns them newest-first. A missing/unreadable
// file yields an empty (not nil-panic) list -- no runs yet is a normal
// state, not an error.
func backupsListRuns() []backupRun {
	f, err := os.Open(BackupsRunsPath)
	if err != nil {
		return []backupRun{}
	}
	defer f.Close()

	runs := []backupRun{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var run backupRun
		if err := json.Unmarshal([]byte(line), &run); err != nil {
			continue
		}
		runs = append(runs, run)
	}

	for i, j := 0, len(runs)-1; i < j; i, j = i+1, j-1 {
		runs[i], runs[j] = runs[j], runs[i]
	}
	return runs
}

// backupsListArchives lists the *.tar.gz files in destination (the same
// directory `opencli backup` writes into), newest-first. A missing
// destination (never configured, or not created yet) yields an empty list.
func backupsListArchives(destination string) []backupArchiveRow {
	if destination == "" {
		return []backupArchiveRow{}
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		return []backupArchiveRow{}
	}

	type withTime struct {
		row backupArchiveRow
		t   int64
	}
	rows := make([]withTime, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar.gz") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		rows = append(rows, withTime{
			row: backupArchiveRow{
				Name:    e.Name(),
				Size:    podmanFormatSize(float64(info.Size())),
				ModTime: info.ModTime().Format("2006-01-02 15:04:05"),
			},
			t: info.ModTime().Unix(),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].t > rows[j].t })

	out := make([]backupArchiveRow, len(rows))
	for i, r := range rows {
		out[i] = r.row
	}
	return out
}

var (
	BackupEnvPath       = "/etc/openpanel/backups/backup.env"
	DockerBackupLogPath = "/var/log/openpanel/admin/docker-backup.log"
)

// backupScheduleChoices maps the only four options the Settings tab's
// dropdown offers to the actual 5-field cron schedule each one writes to
// the `opencli docker-backup` crontab entry. "disabled" is the "never
// fires" schedule (Feb 31st doesn't exist) install.sh ships by default --
// syntactically valid, but a no-op -- which is also how this page decides
// whether per-user backup.env settings are what's meant to govern each
// user's own backup (nothing central scheduled) versus the central
// `opencli docker-backup` cron run being active on one of the other three.
var backupScheduleChoices = map[string]string{
	"disabled": "59 23 31 2 *",
	"daily":    "0 3 * * *",
	"weekly":   "0 3 * * 0",
	"monthly":  "0 3 1 * *",
}

var backupScheduleLabels = map[string]string{
	"disabled": "Disabled",
	"daily":    "Daily",
	"weekly":   "Weekly",
	"monthly":  "Monthly",
}

// backupScheduleChoiceKey maps a raw crontab schedule string back to one
// of backupScheduleChoices' keys, or "" if it doesn't match any of the
// four (e.g. hand-edited directly on /server/crons).
func backupScheduleChoiceKey(schedule string) string {
	for key, val := range backupScheduleChoices {
		if val == schedule {
			return key
		}
	}
	return ""
}

// backupScheduleLabel renders schedule for the active-status badge: the
// preset's name if it matches one of the four known values, or the raw
// cron fields otherwise (a schedule set some other way, e.g. directly on
// /server/crons).
func backupScheduleLabel(schedule string) string {
	if key := backupScheduleChoiceKey(schedule); key != "" {
		return backupScheduleLabels[key]
	}
	return "Custom (" + schedule + ")"
}

// backupsUserCronJob finds the CronJob entry (from cronjobs.go's
// readCronJobs) whose command references docker-backup, if the crontab
// file exists and has one.
func backupsUserCronJob() (job CronJob, found bool) {
	jobs, fileMissing := readCronJobs()
	if fileMissing {
		return CronJob{}, false
	}
	for _, j := range jobs {
		if strings.Contains(j.Command, "docker-backup") {
			return j, true
		}
	}
	return CronJob{}, false
}

// backupsUserSchedule returns the schedule currently in the crontab for
// the docker-backup entry, or the disabled default if there's no matching
// entry at all.
func backupsUserSchedule() string {
	if job, found := backupsUserCronJob(); found {
		return job.Schedule
	}
	return backupScheduleChoices["disabled"]
}

// backupsReadLastLines reads at most the last n lines of path, oldest of
// those first (i.e. it trims from the front, not the back) -- used for the
// Runs tab's raw log view. A missing file returns "", not an error: no
// runs yet is a normal state.
func backupsReadLastLines(path string, n int) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// ServeUserBackups handles GET /backups/user.
func (b *Backups) ServeUserBackups(w http.ResponseWriter, r *http.Request) {
	schedule := backupsUserSchedule()
	scheduleChoice := backupScheduleChoiceKey(schedule)
	if scheduleChoice == "" {
		scheduleChoice = "disabled" // dropdown fallback; ScheduleLabel still shows the real raw value
	}
	backupEnv, err := os.ReadFile(BackupEnvPath)
	if err != nil {
		backupEnv = []byte{}
	}

	webtemplates.Render(w, "backups_user.html", mergeChrome(map[string]interface{}{
		"ScheduleChoice": scheduleChoice,
		"ScheduleLabel":  backupScheduleLabel(schedule),
		"BackupEnv":      string(backupEnv),
		"RunsLog":        backupsReadLastLines(DockerBackupLogPath, 500),
		"CSRFToken":      csrf.Token(r),
		"Flashes":        auth.PopFlashes(w, r, b.Sessions),
	}, r, "User Backups"))
}

// ServeUserBackupsSettings handles POST /backups/user/settings: the
// schedule dropdown alone (Disabled/Daily/Weekly/Monthly) -- writing it
// updates the `opencli docker-backup` crontab entry via the same
// addOrUpdateCron cronjobs.go's own /server/crons page uses.
func (b *Backups) ServeUserBackupsSettings(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	choice := r.PostFormValue("schedule_choice")
	schedule, ok := backupScheduleChoices[choice]
	if !ok {
		auth.AddFlash(w, r, b.Sessions, "Invalid schedule choice.", "error")
		http.Redirect(w, r, "/backups/user#settings", http.StatusSeeOther)
		return
	}

	job, found := backupsUserCronJob()
	if !found {
		auth.AddFlash(w, r, b.Sessions, "Could not find the docker-backup cron entry to update.", "error")
		http.Redirect(w, r, "/backups/user#settings", http.StatusSeeOther)
		return
	}
	if err := addOrUpdateCron(job.LineNumber, schedule, true); err != nil {
		auth.AddFlash(w, r, b.Sessions, "Failed to update the cron schedule: "+err.Error(), "error")
		http.Redirect(w, r, "/backups/user#settings", http.StatusSeeOther)
		return
	}

	if choice == "disabled" {
		auth.AddFlash(w, r, b.Sessions, "Saved. Central backups disabled -- each user's own backup.env settings apply.", "success")
	} else {
		auth.AddFlash(w, r, b.Sessions, "Saved. Backups now run "+backupScheduleLabels[choice]+" for every user.", "success")
	}
	http.Redirect(w, r, "/backups/user#settings", http.StatusSeeOther)
}

// ServeUserBackupsConfiguration handles POST /backups/user/configuration:
// the backup.env textarea alone.
func (b *Backups) ServeUserBackupsConfiguration(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	backupEnv := r.PostFormValue("backup_env")

	if err := os.WriteFile(BackupEnvPath, []byte(backupEnv), 0644); err != nil {
		auth.AddFlash(w, r, b.Sessions, "Failed to save backup.env: "+err.Error(), "error")
	} else {
		auth.AddFlash(w, r, b.Sessions, "Saved. New users will be provisioned with this backup.env -- existing users' own settings are unaffected.", "success")
	}
	http.Redirect(w, r, "/backups/user#configuration", http.StatusSeeOther)
}

// pendingUserBackupAction mirrors pendingBackupAction (the system backups
// page's single-job-at-a-time pointer) but is kept separate so a running
// user-backup "Run Now" and a running system-backup run/restore don't
// stomp on each other's polled status.
var (
	pendingUserBackupActionMu sync.Mutex
	pendingUserBackupAction   *backupActionResult
)

// ServeUserBackupsRun handles POST /backups/user/run: fires `opencli
// docker-backup` in the background (it loops every active user, so this
// can take a while) and lets the browser poll
// ServeUserBackupsActionStatus, mirroring the system backups page's
// run/restore. Only meaningful in Admin Configured mode -- in Disabled
// (User Configured) mode there's no central config for `opencli
// docker-backup` to act on, since each user's own settings apply instead
// -- so this refuses to run there, even if called directly rather than
// through the (also-disabled) button.
func (b *Backups) ServeUserBackupsRun(w http.ResponseWriter, r *http.Request) {
	if backupsUserSchedule() == backupScheduleChoices["disabled"] {
		writeJSONError(w, http.StatusBadRequest, "Backups are Disabled (User Configured mode) -- switch to Daily/Weekly/Monthly on the Settings tab to run a central backup.")
		return
	}

	result := &backupActionResult{}
	pendingUserBackupActionMu.Lock()
	pendingUserBackupAction = result
	pendingUserBackupActionMu.Unlock()

	go func() {
		cmd := exec.Command("opencli", "docker-backup")
		out, err := cmd.CombinedOutput()

		pendingUserBackupActionMu.Lock()
		result.Done = true
		if err != nil {
			result.Success = false
			result.Message = "Backup failed: " + backupsLastLogLine(string(out))
		} else {
			result.Success = true
			result.Message = "User backups completed."
		}
		pendingUserBackupActionMu.Unlock()
	}()

	writeJSON(w, map[string]bool{"scheduled": true})
}

// ServeUserBackupsActionStatus handles GET /backups/user/action-status.
func (b *Backups) ServeUserBackupsActionStatus(w http.ResponseWriter, r *http.Request) {
	pendingUserBackupActionMu.Lock()
	defer pendingUserBackupActionMu.Unlock()
	if pendingUserBackupAction == nil {
		writeJSON(w, backupActionResult{Done: true, Success: false, Message: "No run has happened yet."})
		return
	}
	writeJSON(w, *pendingUserBackupAction)
}

// ServeSystemBackups handles GET/POST /backups/system. POST here is only
// the Settings tab's form (destination/retention_days) -- run/restore are
// separate async endpoints (ServeSystemBackupsRun/Restore) since those can
// take a while and shouldn't block/redirect the page.
func (b *Backups) ServeSystemBackups(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		destination := strings.TrimSpace(r.PostFormValue("destination"))
		if destination == "" {
			destination = defaultBackupDestination
		}
		retentionDays := strings.TrimSpace(r.PostFormValue("retention_days"))
		if retentionDays == "" {
			retentionDays = "-1"
		}

		data := config.Load(BackupsConfigPath)
		data.Set("BACKUP", "destination", destination)
		data.Set("BACKUP", "retention_days", retentionDays)
		if err := config.Save(BackupsConfigPath, data); err != nil {
			auth.AddFlash(w, r, b.Sessions, "Failed to save backup settings: "+err.Error(), "error")
		} else {
			auth.AddFlash(w, r, b.Sessions, "Backup settings saved.", "success")
		}
		http.Redirect(w, r, "/backups/system#settings", http.StatusSeeOther)
		return
	}

	data := config.Load(BackupsConfigPath)
	destination := data.Get("BACKUP", "destination", defaultBackupDestination)
	retentionDays := data.Get("BACKUP", "retention_days", "-1")

	webtemplates.Render(w, "backups_system.html", mergeChrome(map[string]interface{}{
		"Destination":   destination,
		"RetentionDays": retentionDays,
		"Runs":          backupsListRuns(),
		"Archives":      backupsListArchives(destination),
		"CSRFToken":     csrf.Token(r),
		"Flashes":       auth.PopFlashes(w, r, b.Sessions),
	}, r, "System Backups"))
}

// backupActionResult / pendingBackupAction mirror
// podmanImageActionResult/pendingPodmanImageActions in podman.go: a single
// shared pointer (not a map) is enough here, since only one backup or
// restore job makes sense running at a time for the whole page.
type backupActionResult struct {
	Done    bool   `json:"done"`
	Success bool   `json:"success"`
	Message string `json:"message"`
}

var (
	pendingBackupActionMu sync.Mutex
	pendingBackupAction   *backupActionResult
)

// backupRunOpencli is injectable so tests never shell out to the real
// opencli binary.
var backupRunOpencli = func(args ...string) (string, error) {
	cmd := exec.Command("opencli", append([]string{"backup"}, args...)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ServeSystemBackupsRun handles POST /backups/system/run: fires `opencli
// backup` in the background (it can take a while for a large config set,
// though in practice this is usually seconds) and lets the browser poll
// ServeSystemBackupsActionStatus, mirroring the images tab's async
// pull/delete in podman.go.
func (b *Backups) ServeSystemBackupsRun(w http.ResponseWriter, r *http.Request) {
	data := config.Load(BackupsConfigPath)
	if data.Get("BACKUP", "destination", "") == "" {
		data.Set("BACKUP", "destination", defaultBackupDestination)
		if err := config.Save(BackupsConfigPath, data); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to set default backup destination: "+err.Error())
			return
		}
	}

	result := &backupActionResult{}
	pendingBackupActionMu.Lock()
	pendingBackupAction = result
	pendingBackupActionMu.Unlock()

	go func() {
		out, err := backupRunOpencli()

		pendingBackupActionMu.Lock()
		result.Done = true
		if err != nil {
			result.Success = false
			result.Message = "Backup failed: " + backupsLastLogLine(out)
		} else {
			result.Success = true
			result.Message = "Backup completed successfully."
		}
		pendingBackupActionMu.Unlock()
	}()

	writeJSON(w, map[string]bool{"scheduled": true})
}

// ServeSystemBackupsRestore handles POST
// /backups/system/restore/{filename...}.
func (b *Backups) ServeSystemBackupsRestore(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("filename")
	if filename == "" {
		writeJSONError(w, http.StatusBadRequest, "filename is required")
		return
	}

	result := &backupActionResult{}
	pendingBackupActionMu.Lock()
	pendingBackupAction = result
	pendingBackupActionMu.Unlock()

	go func() {
		out, err := backupRunOpencli("--restore", filename)

		pendingBackupActionMu.Lock()
		result.Done = true
		if err != nil {
			result.Success = false
			result.Message = "Restore failed: " + backupsLastLogLine(out)
		} else {
			result.Success = true
			result.Message = "Restored " + filename + ". Affected services may need a restart to pick up the restored config."
		}
		pendingBackupActionMu.Unlock()
	}()

	writeJSON(w, map[string]bool{"scheduled": true})
}

// ServeSystemBackupsDelete handles POST /backups/system/delete/{filename...}.
// Deletion is a plain file remove (not an opencli action), so unlike
// run/restore this responds synchronously -- there's nothing worth polling
// for.
func (b *Backups) ServeSystemBackupsDelete(w http.ResponseWriter, r *http.Request) {
	filename := filepath.Base(r.PathValue("filename"))
	if filename == "" || filename == "." || filename == "/" {
		writeJSONError(w, http.StatusBadRequest, "filename is required")
		return
	}
	if !strings.HasSuffix(filename, ".tar.gz") {
		writeJSONError(w, http.StatusBadRequest, "Refusing to delete a file that isn't a backup archive.")
		return
	}

	destination := config.Load(BackupsConfigPath).Get("BACKUP", "destination", defaultBackupDestination)

	path := filepath.Join(destination, filename)
	var sizeBytes int64
	if info, err := os.Stat(path); err == nil {
		sizeBytes = info.Size()
	}

	if err := os.Remove(path); err != nil {
		backupsRecordRun("delete", "failed", filename, sizeBytes, err.Error())
		writeJSON(w, map[string]interface{}{"success": false, "message": "Failed to delete " + filename + ": " + err.Error()})
		return
	}
	backupsRecordRun("delete", "success", filename, sizeBytes, "deleted")
	writeJSON(w, map[string]interface{}{"success": true, "message": "Deleted " + filename + "."})
}

// backupsRecordRun appends one JSON-line run summary to BackupsRunsPath, in
// the same shape `opencli backup`'s own record_run() writes -- so a delete
// done from the GUI (which never shells out to opencli, unlike run/restore)
// still shows up in the Runs tab alongside backup/restore entries.
func backupsRecordRun(action, status, archive string, sizeBytes int64, detail string) {
	run := backupRun{
		Timestamp: time.Now().Format("2006-01-02T15:04:05-0700"),
		Action:    action,
		Status:    status,
		Archive:   archive,
		SizeBytes: sizeBytes,
		Duration:  0,
		Detail:    detail,
	}
	line, err := json.Marshal(run)
	if err != nil {
		return
	}

	if dir := filepath.Dir(BackupsRunsPath); dir != "" {
		_ = os.MkdirAll(dir, 0755)
	}
	f, err := os.OpenFile(BackupsRunsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(line, '\n'))
}

// ServeSystemBackupsActionStatus handles GET /backups/system/action-status.
func (b *Backups) ServeSystemBackupsActionStatus(w http.ResponseWriter, r *http.Request) {
	pendingBackupActionMu.Lock()
	defer pendingBackupActionMu.Unlock()
	if pendingBackupAction == nil {
		writeJSON(w, backupActionResult{Done: true, Success: false, Message: "No backup/restore action has run yet."})
		return
	}
	writeJSON(w, *pendingBackupAction)
}

// backupsLastLogLine returns the last non-empty line of opencli backup's
// combined output -- typically its final "[✘] ..." error line -- so the
// toast shows something more useful than a blank/huge dump. Falls back to
// the raw output if it's short enough to just show as-is.
func backupsLastLogLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line
		}
	}
	if len(out) > 0 && len(out) < 200 {
		return out
	}
	return "see /var/log/openpanel/admin/system-backup.log for details"
}
