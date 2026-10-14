// This file implements FTP service status detection, the opencli-backed
// account refresh endpoint, the account listing page with per-account
// quotas, and the vsftpd.conf settings editor.
package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
	"openadmin/internal/podman"
	"openadmin/internal/webtemplates"
)

// FTP bundles the /services/ftp* handlers.
type FTP struct {
	MySQL    *sql.DB
	Sessions *auth.Manager
}

// FTPUsersFilePath is the FTP accounts file used as a fallback source for
// getAllFTPAccounts.
var FTPUsersFilePath = "/etc/openpanel/ftp/all.users"

// FTPConfPath is the vsftpd configuration file edited by the settings page.
var FTPConfPath = "/etc/openpanel/ftp/vsftpd.conf"

// FTPUsersDir holds a users.list and quota.usage per OpenPanel account.
var FTPUsersDir = "/etc/openpanel/ftp/users"

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
	ftpQuotaRun = func(user, quota, owner string) (string, error) {
		out, err := exec.Command("opencli", "ftp-quota", user, quota, owner).CombinedOutput()
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
	User      string `json:"user"`
	Owner     string `json:"owner"`
	Password  string `json:"password"`
	Path      string `json:"path"`
	RealPath  string `json:"real_path"`
	UID       string `json:"uid"`
	GID       string `json:"gid"`
	QuotaMB   int    `json:"quota_mb"`
	UsedKB    int64  `json:"used_kb"`
	HasUsage  bool   `json:"has_usage"`
	OverQuota bool   `json:"over_quota"`
}

var ftpDataPathRe = regexp.MustCompile(`.*_data/`)
var ftpContextRe = regexp.MustCompile(`^/home/([^/]+)/`)

func parseFTPAccounts(raw string) []ftpAccount {
	raw = strings.TrimPrefix(raw, `USERS="`)
	raw = strings.TrimSuffix(strings.TrimSpace(raw), `"`)

	var accounts []ftpAccount
	for _, entry := range strings.Fields(raw) {
		fields := strings.Split(entry, "|")
		if len(fields) < 5 {
			continue // skip malformed entry
		}
		user, password, realPath, uid, gid := fields[0], fields[1], fields[2], fields[3], fields[4]
		quota := 0
		if len(fields) > 5 {
			if n, err := strconv.Atoi(fields[5]); err == nil && n > 0 {
				quota = n
			}
		}

		// legacy accounts are user.paneluser, new ones are user@domain and get resolved in setFTPOwnersFromDomains
		owner := user
		if strings.Contains(user, "@") {
			owner = ""
		} else if idx := strings.Index(user, "."); idx != -1 {
			owner = user[idx+1:]
		}

		path := ftpDataPathRe.ReplaceAllString(realPath, "/var/www/html/")

		accounts = append(accounts, ftpAccount{
			User: user, Owner: owner, Password: password,
			Path: path, RealPath: realPath, UID: uid, GID: gid, QuotaMB: quota,
		})
	}
	return accounts
}

// setFTPOwnersFromDomains fills the owner of user@domain accounts with the panel user owning that domain
func setFTPOwnersFromDomains(db *sql.DB, accounts []ftpAccount) {
	if db == nil {
		return
	}
	domains, err := paneldb.GetAllDomains(db)
	if err != nil {
		return
	}
	owners := map[string]string{}
	for _, dom := range domains {
		domainURL, _ := dom["domain_url"].(string)
		username, _ := dom["username"].(string)
		owners[strings.ToLower(domainURL)] = username
	}
	for i := range accounts {
		if idx := strings.LastIndex(accounts[i].User, "@"); idx != -1 {
			accounts[i].Owner = owners[strings.ToLower(accounts[i].User[idx+1:])]
		}
	}
}

// setFTPQuotaUsage fills disk usage from the quota.usage files the FTP container rewrites every minute
func setFTPQuotaUsage(accounts []ftpAccount) {
	cache := map[string]map[string][2]int64{}
	for i := range accounts {
		m := ftpContextRe.FindStringSubmatch(accounts[i].RealPath)
		if m == nil || accounts[i].QuotaMB == 0 {
			continue
		}
		usage, ok := cache[m[1]]
		if !ok {
			usage = readFTPQuotaUsage(filepath.Join(FTPUsersDir, m[1], "quota.usage"))
			cache[m[1]] = usage
		}
		if u, ok := usage[accounts[i].User]; ok {
			accounts[i].UsedKB, accounts[i].OverQuota, accounts[i].HasUsage = u[0], u[1] == 1, true
		}
	}
}

// readFTPQuotaUsage parses name|used_kb|quota_mb|over rows
func readFTPQuotaUsage(path string) map[string][2]int64 {
	usage := map[string][2]int64{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return usage
	}
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Split(strings.TrimSpace(line), "|")
		if len(f) < 4 || f[0] == "" {
			continue
		}
		used, _ := strconv.ParseInt(f[1], 10, 64)
		over := int64(0)
		if f[3] == "1" {
			over = 1
		}
		usage[f[0]] = [2]int64{used, over}
	}
	return usage
}

// QuotaLabel is the quota for display, e.g. "500 MB" or "2 GB".
func (a ftpAccount) QuotaLabel() string {
	if a.QuotaMB >= 1024 && a.QuotaMB%1024 == 0 {
		return fmt.Sprintf("%d GB", a.QuotaMB/1024)
	}
	if a.QuotaMB >= 1024 {
		return fmt.Sprintf("%.1f GB", float64(a.QuotaMB)/1024)
	}
	return fmt.Sprintf("%d MB", a.QuotaMB)
}

// QuotaValue is the quota as typed in the quota form, e.g. "500M" or "2G".
func (a ftpAccount) QuotaValue() string {
	if a.QuotaMB == 0 {
		return ""
	}
	if a.QuotaMB%1024 == 0 {
		return strconv.Itoa(a.QuotaMB/1024) + "G"
	}
	return strconv.Itoa(a.QuotaMB) + "M"
}

// UsedLabel is the disk usage for display.
func (a ftpAccount) UsedLabel() string {
	switch {
	case a.UsedKB < 1024:
		return fmt.Sprintf("%d KB", a.UsedKB)
	case a.UsedKB < 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(a.UsedKB)/1024)
	default:
		return fmt.Sprintf("%.2f GB", float64(a.UsedKB)/1024/1024)
	}
}

// UsedPercent is the share of the quota in use, capped at 100.
func (a ftpAccount) UsedPercent() int {
	if a.QuotaMB == 0 {
		return 0
	}
	pct := int(a.UsedKB * 100 / (int64(a.QuotaMB) * 1024))
	if pct > 100 {
		return 100
	}
	return pct
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
	setFTPOwnersFromDomains(f.MySQL, accounts)
	setFTPQuotaUsage(accounts)
	if jsonOut {
		writeJSON(w, map[string]interface{}{"ftpserver_status": status, "ftp_accounts": accounts})
		return
	}
	webtemplates.Render(w, "services_ftp.html", mergeChrome(map[string]interface{}{
		"FTPServerStatus": status,
		"Accounts":        accounts,
		"BulkActions":     FTPBulkActions(),
		"CSRFToken":       csrf.Token(r),
		"Flashes":         auth.PopFlashes(w, r, f.Sessions),
	}, r, "FTP"))
}

var ftpUserRe = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+$`)
var ftpQuotaRe = regexp.MustCompile(`^([0-9]+)([MG]?)$`)

// normalizeFTPQuota turns 500, 500M, 2G, 0 or unlimited into what opencli ftp-quota takes
func normalizeFTPQuota(v string) (string, bool) {
	v = strings.ToUpper(strings.TrimSpace(v))
	if v == "" || v == "UNLIMITED" {
		return "unlimited", true
	}
	m := ftpQuotaRe.FindStringSubmatch(strings.TrimSuffix(v, "B"))
	if m == nil {
		return "", false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n > 1024*1024*100 {
		return "", false
	}
	if n == 0 {
		return "unlimited", true
	}
	if m[2] == "" {
		m[2] = "M"
	}
	return strconv.Itoa(n) + m[2], true
}

// ServeQuota handles POST /services/ftp/quota: sets or removes one account's disk quota.
func (f *FTP) ServeQuota(w http.ResponseWriter, r *http.Request) {
	user := strings.TrimSpace(r.FormValue("user"))
	back := func(msg, category string) {
		auth.AddFlash(w, r, f.Sessions, msg, category)
		http.Redirect(w, r, "/services/ftp", http.StatusSeeOther)
	}
	if !ftpUserRe.MatchString(user) {
		back("Invalid FTP username.", "error")
		return
	}
	quota, ok := normalizeFTPQuota(r.FormValue("quota"))
	if !ok {
		back("Invalid quota, use a number of MB or a value like 500M or 2G.", "error")
		return
	}

	owner := ""
	accounts := getAllFTPAccounts()
	setFTPOwnersFromDomains(f.MySQL, accounts)
	for _, a := range accounts {
		if a.User == user {
			owner = a.Owner
		}
	}
	if owner == "" {
		back("FTP account "+user+" was not found or its domain has no owner.", "error")
		return
	}

	out, err := ftpQuotaRun(user, quota, owner)
	if err != nil || !strings.Contains(out, "Success:") {
		back("Failed to set quota for "+user+": "+strings.TrimSpace(out), "error")
		return
	}
	if quota == "unlimited" {
		back("Quota removed for FTP account "+user+".", "success")
	} else {
		back("Quota for FTP account "+user+" set to "+quota+".", "success")
	}
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
