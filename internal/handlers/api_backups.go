// This file implements the JSON REST API's Backups endpoints: the JSON
// counterpart to backups.go's HTML /backups/system and /backups/user
// pages. Reuses the exact same plumbing -- only the request/response
// shape differs.
package handlers

import (
	"net/http"
	"os"

	"openadmin/internal/config"
)

// APIBackups bundles the /api/backups/* handlers.
type APIBackups struct{}

// ServeSystemBackups handles GET /api/backups/system.
func (a *APIBackups) ServeSystemBackups(w http.ResponseWriter, r *http.Request) {
	data := config.Load(BackupsConfigPath)
	destination := data.Get("BACKUP", "destination", "")
	retentionDays := data.Get("BACKUP", "retention_days", "-1")

	writeJSON(w, map[string]interface{}{
		"destination":    destination,
		"retention_days": retentionDays,
		"runs":           backupsListRuns(),
		"archives":       backupsListArchives(destination),
	})
}

// ServeSystemBackupsSettings handles POST /api/backups/system/settings:
// destination/retention_days.
func (a *APIBackups) ServeSystemBackupsSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Destination   string `json:"destination"`
		RetentionDays string `json:"retention_days"`
	}
	if !apiDecodeJSONBody(r, &body) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	retentionDays := body.RetentionDays
	if retentionDays == "" {
		retentionDays = "-1"
	}

	data := config.Load(BackupsConfigPath)
	data.Set("BACKUP", "destination", body.Destination)
	data.Set("BACKUP", "retention_days", retentionDays)
	if err := config.Save(BackupsConfigPath, data); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to save backup settings: "+err.Error())
		return
	}
	writeJSON(w, map[string]interface{}{"success": true, "message": "Backup settings saved."})
}

// ServeSystemBackupsRun handles POST /api/backups/system/run.
func (a *APIBackups) ServeSystemBackupsRun(w http.ResponseWriter, r *http.Request) {
	b := &Backups{}
	b.ServeSystemBackupsRun(w, r)
}

// ServeSystemBackupsRestore handles POST
// /api/backups/system/restore/{filename...}.
func (a *APIBackups) ServeSystemBackupsRestore(w http.ResponseWriter, r *http.Request) {
	b := &Backups{}
	b.ServeSystemBackupsRestore(w, r)
}

// ServeSystemBackupsDelete handles POST
// /api/backups/system/delete/{filename...}.
func (a *APIBackups) ServeSystemBackupsDelete(w http.ResponseWriter, r *http.Request) {
	b := &Backups{}
	b.ServeSystemBackupsDelete(w, r)
}

// ServeSystemBackupsActionStatus handles GET
// /api/backups/system/action-status.
func (a *APIBackups) ServeSystemBackupsActionStatus(w http.ResponseWriter, r *http.Request) {
	b := &Backups{}
	b.ServeSystemBackupsActionStatus(w, r)
}

// ServeUserBackups handles GET /api/backups/user.
func (a *APIBackups) ServeUserBackups(w http.ResponseWriter, r *http.Request) {
	schedule := backupsUserSchedule()
	scheduleChoice := backupScheduleChoiceKey(schedule)
	if scheduleChoice == "" {
		scheduleChoice = "disabled"
	}
	writeJSON(w, map[string]interface{}{
		"schedule_choice": scheduleChoice,
		"schedule_label":  backupScheduleLabel(schedule),
	})
}

// ServeUserBackupsSettings handles POST /api/backups/user/settings:
// {"schedule_choice": "disabled"|"daily"|"weekly"|"monthly"}.
func (a *APIBackups) ServeUserBackupsSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ScheduleChoice string `json:"schedule_choice"`
	}
	if !apiDecodeJSONBody(r, &body) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	schedule, ok := backupScheduleChoices[body.ScheduleChoice]
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid schedule choice.")
		return
	}

	job, found := backupsUserCronJob()
	if !found {
		writeJSONError(w, http.StatusInternalServerError, "Could not find the docker-backup cron entry to update.")
		return
	}
	if err := addOrUpdateCron(job.LineNumber, schedule, true); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to update the cron schedule: "+err.Error())
		return
	}
	writeJSON(w, map[string]interface{}{"success": true, "schedule_choice": body.ScheduleChoice})
}

// ServeUserBackupsConfiguration handles GET/POST
// /api/backups/user/configuration: the backup.env contents.
func (a *APIBackups) ServeUserBackupsConfiguration(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			BackupEnv string `json:"backup_env"`
		}
		if !apiDecodeJSONBody(r, &body) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}
		if err := os.WriteFile(BackupEnvPath, []byte(body.BackupEnv), 0644); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to save backup.env: "+err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"success": true})
		return
	}

	backupEnv, err := os.ReadFile(BackupEnvPath)
	if err != nil {
		backupEnv = []byte{}
	}
	writeJSON(w, map[string]interface{}{"backup_env": string(backupEnv)})
}

// ServeUserBackupsRun handles POST /api/backups/user/run.
func (a *APIBackups) ServeUserBackupsRun(w http.ResponseWriter, r *http.Request) {
	b := &Backups{}
	b.ServeUserBackupsRun(w, r)
}

// ServeUserBackupsActionStatus handles GET /api/backups/user/action-status.
func (a *APIBackups) ServeUserBackupsActionStatus(w http.ResponseWriter, r *http.Request) {
	b := &Backups{}
	b.ServeUserBackupsActionStatus(w, r)
}
