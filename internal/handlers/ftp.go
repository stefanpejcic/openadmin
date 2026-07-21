// This file implements FTP service status detection, the opencli-backed
// account refresh endpoint, the account listing page, and the vsftpd.conf
// settings editor.
package handlers

import (
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/podman"
	"openadmin/internal/webtemplates"
)

// FTP bundles the /services/ftp* handlers.
type FTP struct {
	Sessions *auth.Manager
}

// FTPUsersFilePath is the FTP accounts file used as a fallback source for
// getAllFTPAccounts.
var FTPUsersFilePath = "/etc/openpanel/ftp/all.users"

// FTPConfPath is the vsftpd configuration file edited by the settings page.
var FTPConfPath = "/etc/openpanel/ftp/vsftpd.conf"

// ftpPsRun/ftpRefreshRun/ftpRestartRun are injectable so tests never shell
// out to a real podman/opencli binary, matching the pattern used throughout
// this package (controlServiceRun, dropCacheRun, ...).
var (
	ftpPsRun = func() (string, error) {
		cmd, err := podman.Command("default", "ps", "--filter", "name=openadmin_ftp", "--filter", "status=running", "--format", "{{.Names}}")
		if err != nil {
			return "", err
		}
		out, err := cmd.Output()
		return string(out), err
	}
	ftpRefreshRun = func() (string, error) {
		out, err := exec.Command("opencli", "ftp-users").CombinedOutput()
		return string(out), err
	}
	ftpRestartRun = func() error {
		cmd, err := podman.Command("default", "restart", "openadmin_ftp")
		if err != nil {
			return err
		}
		return cmd.Run()
	}
)

func checkFTPServerStatus() string {
	info, err := os.Stat(FTPUsersFilePath)
	if err != nil || info.Size() == 0 {
		return "not_installed"
	}

	out, err := ftpPsRun()
	if err != nil {
		return "unknown"
	}
	if strings.Contains(out, "openadmin_ftp") {
		return "running"
	}
	return "stopped"
}

type ftpAccount struct {
	User     string `json:"user"`
	Owner    string `json:"owner"`
	Password string `json:"password"`
	Path     string `json:"path"`
	RealPath string `json:"real_path"`
	UID      string `json:"uid"`
	GID      string `json:"gid"`
}

var ftpDataPathRe = regexp.MustCompile(`.*_data/`)

func parseFTPAccounts(raw string) []ftpAccount {
	raw = strings.TrimPrefix(raw, `USERS="`)
	raw = strings.TrimSuffix(strings.TrimSpace(raw), `"`)

	var accounts []ftpAccount
	for _, entry := range strings.Fields(raw) {
		fields := strings.Split(entry, "|")
		if len(fields) != 5 {
			continue // skip malformed entry
		}
		user, password, realPath, uid, gid := fields[0], fields[1], fields[2], fields[3], fields[4]

		owner := user
		if idx := strings.Index(user, "."); idx != -1 {
			owner = user[idx+1:]
		}

		path := ftpDataPathRe.ReplaceAllString(realPath, "/var/www/html/")

		accounts = append(accounts, ftpAccount{
			User: user, Owner: owner, Password: password,
			Path: path, RealPath: realPath, UID: uid, GID: gid,
		})
	}
	return accounts
}

// getAllFTPAccounts prefers the USERS environment variable, falling back
// to the all.users file.
func getAllFTPAccounts() []ftpAccount {
	if usersEnv := os.Getenv("USERS"); usersEnv != "" {
		return parseFTPAccounts(usersEnv)
	}
	raw, err := os.ReadFile(FTPUsersFilePath)
	if err != nil {
		return nil
	}
	return parseFTPAccounts(strings.TrimSpace(string(raw)))
}

// ServeRefresh handles GET/POST /services/ftp/refresh: runs opencli
// ftp-users and echoes its output as plain text.
func (f *FTP) ServeRefresh(w http.ResponseWriter, r *http.Request) {
	out, err := ftpRefreshRun()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Error executing opencli ftp-users: " + out))
		return
	}
	w.Write([]byte(out))
}

// ServeAccounts handles GET/POST /services/ftp.
func (f *FTP) ServeAccounts(w http.ResponseWriter, r *http.Request) {
	status := checkFTPServerStatus()
	jsonOut := r.URL.Query().Get("output") == "json"

	if status != "running" {
		if jsonOut {
			writeJSON(w, map[string]string{"status": status})
			return
		}
		webtemplates.Render(w, "services_ftp.html", mergeChrome(map[string]interface{}{
			"FTPServerStatus": status,
			"Accounts":        []ftpAccount{},
			"CSRFToken":       csrf.Token(r),
			"Flashes":         auth.PopFlashes(w, r, f.Sessions),
		}, r, "FTP"))
		return
	}

	accounts := getAllFTPAccounts()
	if jsonOut {
		writeJSON(w, map[string]interface{}{"ftpserver_status": status, "ftp_accounts": accounts})
		return
	}
	webtemplates.Render(w, "services_ftp.html", mergeChrome(map[string]interface{}{
		"FTPServerStatus": status,
		"Accounts":        accounts,
		"CSRFToken":       csrf.Token(r),
		"Flashes":         auth.PopFlashes(w, r, f.Sessions),
	}, r, "FTP"))
}

// ServeSettings handles GET/POST /services/ftp/settings.
func (f *FTP) ServeSettings(w http.ResponseWriter, r *http.Request) {
	status := checkFTPServerStatus()

	if r.Method == http.MethodPost {
		if !formHasKey(r, "config_content") {
			auth.AddFlash(w, r, f.Sessions, "Error saving FTP configuration - no content provided!", "error")
			f.renderSettings(w, r, status, "")
			return
		}
		newContent := r.FormValue("config_content")

		newContent = strings.ReplaceAll(newContent, "\r\n", "\n")
		newContent = strings.ReplaceAll(newContent, "\r", "\n")

		if _, err := os.Stat(FTPConfPath); err == nil {
			_ = os.Rename(FTPConfPath, FTPConfPath+".bak")
		}
		if err := os.WriteFile(FTPConfPath, []byte(newContent), 0644); err != nil {
			auth.AddFlash(w, r, f.Sessions, "Error reading or updating config file. - Check openadmin error log", "error")
			f.renderSettings(w, r, "error", "")
			return
		}

		if err := ftpRestartRun(); err != nil {
			auth.AddFlash(w, r, f.Sessions, "Config updated, but failed to restart FTP container.", "error")
		} else {
			auth.AddFlash(w, r, f.Sessions, "Config updated successfully. FTP container restarted to apply changes.", "success")
		}
		f.renderSettings(w, r, status, newContent)
		return
	}

	content := ""
	if raw, err := os.ReadFile(FTPConfPath); err == nil {
		content = string(raw)
	}
	f.renderSettings(w, r, status, content)
}

func (f *FTP) renderSettings(w http.ResponseWriter, r *http.Request, status, content string) {
	webtemplates.Render(w, "services_ftp_settings.html", mergeChrome(map[string]interface{}{
		"FTPServerStatus": status,
		"FTPContent":      content,
		"CSRFToken":       csrf.Token(r),
		"Flashes":         auth.PopFlashes(w, r, f.Sessions),
	}, r, "FTP Configuration"))
}

// formHasKey reports whether the parsed form actually contains key, so a
// present-but-empty textarea (a deliberate "clear the config" submission)
// is distinguished from the field being entirely absent.
func formHasKey(r *http.Request, key string) bool {
	if r.Form == nil {
		r.ParseForm()
	}
	_, ok := r.Form[key]
	return ok
}
