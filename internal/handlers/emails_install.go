package handlers

import (
	"net/http"
	"os"
	"strconv"
	"strings"
)

var (
	// live output of the last mailserver install started from the gui
	EmailsInstallLogPath = "/tmp/openpanel_mailserver_install.log"
	EmailsInstallPIDFile = "/tmp/openpanel_mailserver_install.pid"
)

var emailsInstallRun = func() error {
	pid, err := startDetached("openpanel-mailserver-install", EmailsInstallLogPath, []string{"timeout", "1800s", "opencli", "email-server", "install"})
	if err != nil {
		return err
	}
	return os.WriteFile(EmailsInstallPIDFile, []byte(strconv.Itoa(pid)), 0644)
}

func emailsInstallRunning() bool {
	raw, err := os.ReadFile(EmailsInstallPIDFile)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	return err == nil && processAlive(pid)
}

func mailserverInstalled() bool { return checkMailserverStatus() != "not_installed" }

// ServeEmailsInstall handles POST /emails/install, same flow as the ImunifyAV installer
func (e *Emails) ServeEmailsInstall(w http.ResponseWriter, r *http.Request) {
	if emailsInstallRunning() {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"success": false, "error": "Mailserver installation is already running."})
		return
	}
	if mailserverInstalled() {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"success": false, "error": "Mailserver is already installed."})
		return
	}
	if err := emailsInstallRun(); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": "Failed to start the installation. Details: " + err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"success": true})
}

// ServeEmailsInstallStatus handles GET /emails/install/status
func (e *Emails) ServeEmailsInstallStatus(w http.ResponseWriter, r *http.Request) {
	output := ""
	if raw, err := os.ReadFile(EmailsInstallLogPath); err == nil {
		output = string(raw)
	}
	status := "idle"
	if emailsInstallRunning() {
		status = "running"
	} else if _, err := os.Stat(EmailsInstallPIDFile); err == nil {
		status = "finished"
	}
	writeJSON(w, map[string]interface{}{"status": status, "output": output, "installed": mailserverInstalled()})
}
