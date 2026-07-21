// This file implements mail server settings, account management, the
// delivery queue, reports, and webmail autologin. Domain rate-limiting
// (postfwd) is in email_domain_limits.go.
package handlers

import (
	"bufio"
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"openadmin/internal/auth"
	"openadmin/internal/config"
	"openadmin/internal/podman"
	"openadmin/internal/webtemplates"
)

// Emails bundles all /emails/* handlers.
type Emails struct {
	Sessions   *auth.Manager
	PublicIP   string
	MasterPass string // dovecot master password, loaded once at startup
}

const emailsMailserverContainerName = "openadmin_mailserver"
const emailsMasterUser = "openpanel"

var emailsRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)

// EmailsDovecotSecretKeyPath is the dovecot master-password secret file
// (distinct from auth.SecretKeyPath, which is a different secret).
var EmailsDovecotSecretKeyPath = "/etc/openpanel/openpanel/secret.key"

// LoadDovecotMasterPass reads the dovecot master-password secret from disk.
// Like auth.LoadSecretKey, this is unique-per-server secret material
// normally provisioned by the installer, so a from-source/dev/fresh-order
// build generates and persists one instead of refusing to start.
func LoadDovecotMasterPass() (string, error) {
	raw, err := os.ReadFile(EmailsDovecotSecretKeyPath)
	if err == nil {
		return strings.TrimSpace(string(raw)), nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to read dovecot master secret file %s: %w", EmailsDovecotSecretKeyPath, err)
	}
	return auth.GenerateAndPersistSecret(EmailsDovecotSecretKeyPath)
}

// EmailsMailComposeFile / EmailsMailserverEnvFile / EmailsCaddyRedirectsPath
// / EmailsCaddyConfigDir are the hardcoded paths used by the mail server
// integration.
var (
	EmailsMailComposeFile    = "/usr/local/mail/openmail/compose.yml"
	EmailsMailserverEnvFile  = "/usr/local/mail/openmail/mailserver.env"
	EmailsCaddyRedirectsPath = "/etc/openpanel/caddy/redirects.conf"
	EmailsCaddyConfigDir     = "/etc/openpanel/caddy/"
	EmailsReportsDataDir     = "/usr/local/admin/templates/emails/data"
)

// emailsPodmanPsRun / emailsRestartMailserverRun are injectable so tests
// never shell out to real podman/bash.
var emailsPodmanPsRun = func(filterArgs ...string) (string, error) {
	args := append([]string{"ps"}, filterArgs...)
	cmd, err := podman.Command("default", args...)
	if err != nil {
		return "", err
	}
	out, err := cmd.Output()
	return string(out), err
}

var emailsRestartMailserverRun = func() {
	_ = exec.Command("/bin/bash", "-c",
		"cd /usr/local/mail/openmail/ && podman-compose down mailserver && podman-compose up -d mailserver").Start()
}

func checkMailserverStatus() string {
	if _, err := os.Stat(EmailsMailComposeFile); err != nil {
		return "not_installed"
	}
	out, err := emailsPodmanPsRun("--filter", "name="+emailsMailserverContainerName, "--filter", "status=running", "--format", "{{.Names}}")
	if err != nil {
		return "unknown"
	}
	if strings.Contains(out, emailsMailserverContainerName) {
		return "running"
	}
	return "stopped"
}

func checkWebmailStatus() (string, []string) {
	if _, err := os.Stat(EmailsMailComposeFile); err != nil {
		return "not_installed", nil
	}
	services := []string{"sogo", "roundcube", "snappymail"}
	out, err := emailsPodmanPsRun("--filter", "status=running", "--format", "{{.Names}}")
	if err != nil {
		return "unknown", nil
	}
	var running []string
	for _, s := range services {
		if strings.Contains(out, s) {
			running = append(running, s)
		}
	}
	if len(running) > 0 {
		return "running", running
	}
	return "stopped", nil
}

// parseMailserverEnvFile is shared by parse_env_file() (all keys) and
// update_env_variable() (single-key rewrite).
func parseMailserverEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, found := strings.Cut(line, "="); found {
			data[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return data, scanner.Err()
}

// parseEnvFile returns every key from mailserver.env, plus a synthetic
// ENABLE_POSTFWD derived from whether postfwd.cf exists.
func parseEnvFile() (map[string]string, error) {
	data, err := parseMailserverEnvFile(EmailsMailserverEnvFile)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(PostfwdConfigPath); err == nil {
		data["ENABLE_POSTFWD"] = "1"
	} else {
		data["ENABLE_POSTFWD"] = "0"
	}
	return data, nil
}

// updateEnvVariable rewrites a single existing key in mailserver.env,
// erroring if the file or key is missing.
func updateEnvVariable(key, newValue string) error {
	data, err := parseMailserverEnvFile(EmailsMailserverEnvFile)
	if err != nil {
		return err
	}
	if _, ok := data[key]; !ok {
		return fmt.Errorf("%s not found in the configuration", key)
	}
	data[key] = newValue

	var b strings.Builder
	for k, v := range data {
		b.WriteString(k + "=" + v + "\n")
	}
	return os.WriteFile(EmailsMailserverEnvFile, []byte(b.String()), 0644)
}

// updateWebmailRedirect is unused -- like dnsSlaveReachableViaSSH in
// dns_cluster.go, it's defined but never called anywhere in this package.
// Kept in place rather than removed.
var webmailRedirectRe = regexp.MustCompile(`(redir @webmail\s+)https?://[^/\s]+(:\d+)?(/\S*?)?\s+301`)

func updateWebmailRedirect(domain string) error {
	raw, err := os.ReadFile(EmailsCaddyRedirectsPath)
	if err != nil {
		return err
	}
	protocol := "https"
	if isIPv4(domain) {
		protocol = "http"
	}
	updated := webmailRedirectRe.ReplaceAllString(string(raw), "${1}"+protocol+"://"+domain+" 301")
	return os.WriteFile(EmailsCaddyRedirectsPath, []byte(updated), 0644)
}

// getEmailStorageLocation / isValidEmailStorageLocation / updateEmailStorageLocation
// mirror the "added in 1.7.58" storage-path settings, added to admin.ini's
// [EMAIL] section.
func getEmailStorageLocation() string {
	return config.Load(config.AdminConfigPath).Get("EMAIL", "email_storage_location", "/var/mail/")
}

func isValidEmailStorageLocation(value string) bool {
	if value == "" {
		return false
	}
	if value == "user_dir" {
		return true
	}
	if filepath.IsAbs(value) {
		if strings.Contains(value, "\x00") || strings.Contains(value, "..") {
			return false
		}
		return true
	}
	return false
}

func updateEmailStorageLocation(newValue string) error {
	data := config.Load(config.AdminConfigPath)
	data.Set("EMAIL", "email_storage_location", newValue)
	return config.Save(config.AdminConfigPath, data)
}

type emailQuota struct {
	Email string `json:"email"`
	Quota string `json:"quota"`
}

// emailsListAccountsRun tries opencli first, falling back to reading
// postfix-accounts.cf directly if that fails.
var emailsListAccountsRun = func() []emailQuota {
	out, err := exec.Command("opencli", "email-setup", "email", "list").Output()
	if err == nil {
		var result []emailQuota
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.HasPrefix(line, "*") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			result = append(result, emailQuota{Email: fields[1], Quota: strings.Join(fields[2:], " ")})
		}
		return result
	}

	raw, ferr := os.ReadFile("/usr/local/mail/openmail/docker-data/dms/config/postfix-accounts.cf")
	if ferr != nil {
		return nil
	}
	var result []emailQuota
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, "|") {
			continue
		}
		email := strings.TrimSpace(strings.SplitN(line, "|", 2)[0])
		if email != "" {
			result = append(result, emailQuota{Email: email, Quota: ""})
		}
	}
	return result
}

// ServeEmailsSettings handles GET/POST /emails/settings.
func (e *Emails) ServeEmailsSettings(w http.ResponseWriter, r *http.Request) {
	accounts := emailsListAccountsRun()
	totalCount := len(accounts)

	if r.Method == http.MethodPost {
		r.ParseForm()
		webmailSoftware := r.PostFormValue("webmail-software")
		webmailDomain := r.PostFormValue("webmail-domain")
		storageType := r.PostFormValue("storage_type")
		emailStorageLocation := r.PostFormValue("email_storage_location")

		switch {
		case storageType != "" && emailStorageLocation != "":
			if storageType != "user_dir" && storageType != "custom" {
				http.Error(w, "Invalid storage_type", http.StatusBadRequest)
				return
			}
			if storageType == "user_dir" {
				emailStorageLocation = storageType
			}
			if totalCount != 0 {
				auth.AddFlash(w, r, e.Sessions, "Error: Email storage location cannot be changed when email accounts already exist.", "error")
			} else if !isValidEmailStorageLocation(emailStorageLocation) {
				auth.AddFlash(w, r, e.Sessions, "Error: Invalid email storage location. Provide either 'user_dir' or full path.", "error")
			} else if err := updateEmailStorageLocation(emailStorageLocation); err != nil {
				auth.AddFlash(w, r, e.Sessions, "Error: "+err.Error(), "error")
			} else {
				auth.AddFlash(w, r, e.Sessions, "Email storage location updated successfully. Make sure to stop and start Mailserver for new storage to apply", "success")
			}
			http.Redirect(w, r, "/emails/settings", http.StatusSeeOther)
			return

		case webmailDomain != "":
			webmailDomain = strings.TrimPrefix(webmailDomain, "http://")
			webmailDomain = strings.TrimPrefix(webmailDomain, "https://")
			webmailDomain = strings.TrimSuffix(webmailDomain, "/")

			domainExists := false
			_ = filepath.Walk(EmailsCaddyConfigDir, func(path string, info os.FileInfo, err error) error {
				if err != nil || info == nil || info.IsDir() || domainExists {
					return nil
				}
				content, rerr := os.ReadFile(path)
				if rerr == nil && strings.Contains(string(content), webmailDomain) {
					domainExists = true
				}
				return nil
			})

			if domainExists {
				auth.AddFlash(w, r, e.Sessions, fmt.Sprintf("Error setting webmail domain: '%s' already exists in Caddy configuration.", webmailDomain), "error")
			} else if err := exec.Command("opencli", "email-webmail", "domain", webmailDomain).Run(); err != nil {
				auth.AddFlash(w, r, e.Sessions, "Failed to update webmail domain.", "error")
			} else {
				auth.AddFlash(w, r, e.Sessions, "Webmail domain updated successfully!", "success")
			}
			http.Redirect(w, r, "/emails/settings", http.StatusSeeOther)
			return

		case webmailSoftware != "":
			if webmailSoftware == "roundcube" {
				if err := exec.Command("opencli", "email-webmail", webmailSoftware).Run(); err != nil {
					auth.AddFlash(w, r, e.Sessions, "Failed to restart webmail service.", "danger")
				} else {
					auth.AddFlash(w, r, e.Sessions, "Configuration updated successfully! Webmail service will be restarted in the background.", "success")
				}
			} else {
				auth.AddFlash(w, r, e.Sessions, "Invalid webmail client selected.", "danger")
			}
			http.Redirect(w, r, "/emails/settings", http.StatusSeeOther)
			return

		default:
			checkboxKeys := []string{
				"ENABLE_POSTFWD", "ENABLE_AMAVIS", "ENABLE_DNSBL", "ENABLE_RSPAMD",
				"ENABLE_SPAMASSASSIN", "ENABLE_MTA_STS", "ENABLE_OPENDKIM", "ENABLE_OPENDMARC",
				"ENABLE_POP3", "ENABLE_IMAP", "ENABLE_CLAMAV", "ENABLE_FAIL2BAN",
				"SMTP_ONLY", "ENABLE_SRS",
			}
			var triggeredServices []string
			for _, key := range checkboxKeys {
				value := r.PostFormValue(key)
				if value == "" {
					value = "0"
				}
				if key == "ENABLE_POSTFWD" {
					action := "disable"
					if value == "1" {
						action = "enable"
					}
					_ = exec.Command("opencli", "email-server", "postfwd", action).Start()
				} else {
					_ = updateEnvVariable(key, value)
				}
				if key == "ENABLE_FAIL2BAN" {
					triggeredServices = append(triggeredServices, "Fail2Ban")
				} else if key == "ENABLE_CLAMAV" {
					triggeredServices = append(triggeredServices, "ClamAV")
				}
			}

			emailsRestartMailserverRun()

			if len(triggeredServices) > 0 {
				auth.AddFlash(w, r, e.Sessions, fmt.Sprintf("Configuration updated! %s require Mailserver to be recreated. Make sure to stop&start the service.", strings.Join(triggeredServices, " and ")), "info")
			} else {
				auth.AddFlash(w, r, e.Sessions, "Configuration updated successfully! Mailserver will be restarted in the background to apply new configuration.", "success")
			}
		}
	}

	webmailStatus, webmailServices := checkWebmailStatus()
	mailserverStatus := checkMailserverStatus()
	webmailDomain := getWebmailDomain(e.PublicIP)
	configData, _ := parseEnvFile()
	emailStorageLocation := getEmailStorageLocation()

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, map[string]interface{}{
			"webmail-status":         webmailStatus,
			"webmail-domain":         webmailDomain,
			"webmail-selected":       nonNilStrings(webmailServices),
			"mailserver-status":      mailserverStatus,
			"emails":                 totalCount,
			"config_data":            configData,
			"email_storage_location": emailStorageLocation,
		})
		return
	}

	webtemplates.Render(w, "emails_settings.html", mergeChrome(map[string]interface{}{
		"MailserverStatus":     mailserverStatus,
		"WebmailStatus":        webmailStatus,
		"WebmailServices":      webmailServices,
		"WebmailDomain":        webmailDomain,
		"ConfigData":           configData,
		"Emails":               totalCount,
		"EmailStorageLocation": emailStorageLocation,
		"Flashes":              auth.PopFlashes(w, r, e.Sessions),
	}, r, "Email Settings"))
}

// runEmailsCmd is a 30s-timeout subprocess wrapper shared by the
// /emails/api/* endpoints.
var runEmailsCmd = func(args []string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return false, "Command timed out."
	}
	if err != nil {
		if _, isExit := err.(*exec.ExitError); !isExit {
			return false, err.Error()
		}
	}
	return err == nil, strings.TrimSpace(string(out))
}

type emailsAPIRequest struct {
	Email    string   `json:"email"`
	Password string   `json:"password"`
	Quota    string   `json:"quota"`
	Action   string   `json:"action"`
	Type     string   `json:"type"`
	Emails   []string `json:"emails"`
}

// ServeUpdatePassword, ServeQuotaSet, ServeQuotaDel, ServeRestrict, and
// ServeDeleteEmails handle POST /emails/api/*.
//
// SECURITY: these 5 routes require a logged-in session (auth.RequireLogin),
// matching every sibling /emails/* route. Without that check they'd be
// reachable completely unauthenticated, letting anyone who can reach the
// admin panel change any mailbox's password, quota, or send/receive
// restrictions, or delete accounts outright.
func (e *Emails) ServeUpdatePassword(w http.ResponseWriter, r *http.Request) {
	var body emailsAPIRequest
	json.NewDecoder(r.Body).Decode(&body)
	email := strings.TrimSpace(body.Email)
	password := strings.TrimSpace(body.Password)
	if email == "" || password == "" {
		writeJSONMessage(w, http.StatusBadRequest, "Email and password are required.")
		return
	}
	ok, out := runEmailsCmd([]string{"opencli", "email-setup", "email", "update", email, password})
	if ok {
		writeJSON(w, map[string]string{"message": "Password updated."})
		return
	}
	writeJSONMessage(w, http.StatusInternalServerError, "Failed: "+out)
}

func (e *Emails) ServeQuotaSet(w http.ResponseWriter, r *http.Request) {
	var body emailsAPIRequest
	json.NewDecoder(r.Body).Decode(&body)
	email := strings.TrimSpace(body.Email)
	quota := strings.TrimSpace(body.Quota)
	if email == "" || quota == "" {
		writeJSONMessage(w, http.StatusBadRequest, "Email and quota are required.")
		return
	}
	ok, out := runEmailsCmd([]string{"opencli", "email-setup", "quota", "set", email, quota})
	if ok {
		writeJSON(w, map[string]string{"message": "Quota updated."})
		return
	}
	writeJSONMessage(w, http.StatusInternalServerError, "Failed: "+out)
}

func (e *Emails) ServeQuotaDel(w http.ResponseWriter, r *http.Request) {
	var body emailsAPIRequest
	json.NewDecoder(r.Body).Decode(&body)
	email := strings.TrimSpace(body.Email)
	if email == "" {
		writeJSONMessage(w, http.StatusBadRequest, "Email is required.")
		return
	}
	ok, out := runEmailsCmd([]string{"opencli", "email-setup", "quota", "del", email})
	if ok {
		writeJSON(w, map[string]string{"message": "Quota removed."})
		return
	}
	writeJSONMessage(w, http.StatusInternalServerError, "Failed: "+out)
}

func (e *Emails) ServeRestrict(w http.ResponseWriter, r *http.Request) {
	var body emailsAPIRequest
	json.NewDecoder(r.Body).Decode(&body)
	email := strings.TrimSpace(body.Email)
	action := strings.TrimSpace(body.Action)
	rtype := strings.TrimSpace(body.Type)
	if email == "" || (action != "add" && action != "del") || (rtype != "send" && rtype != "receive") {
		writeJSONMessage(w, http.StatusBadRequest, "Invalid parameters.")
		return
	}
	ok, out := runEmailsCmd([]string{"opencli", "email-setup", "email", "restrict", action, rtype, email})
	if ok {
		writeJSON(w, map[string]string{"message": "Restriction updated."})
		return
	}
	writeJSONMessage(w, http.StatusInternalServerError, "Failed: "+out)
}

func (e *Emails) ServeDeleteEmails(w http.ResponseWriter, r *http.Request) {
	var body emailsAPIRequest
	json.NewDecoder(r.Body).Decode(&body)
	if len(body.Emails) == 0 {
		writeJSONMessage(w, http.StatusBadRequest, "No emails provided.")
		return
	}
	args := append([]string{"opencli", "email-setup", "email", "del"}, body.Emails...)
	ok, out := runEmailsCmd(args)
	if ok {
		writeJSON(w, map[string]string{"message": fmt.Sprintf("Deleted %d account(s).", len(body.Emails))})
		return
	}
	writeJSONMessage(w, http.StatusInternalServerError, "Failed: "+out)
}

// ServeEmailsAccounts handles GET/POST /emails/accounts. Note that POST is
// accepted but never actually branches on request method -- both verbs run
// identical logic. Preserved as-is rather than trimmed to GET-only.
func (e *Emails) ServeEmailsAccounts(w http.ResponseWriter, r *http.Request) {
	outputJSON := r.URL.Query().Get("output") == "json"
	mailserverStatus := checkMailserverStatus()

	if mailserverStatus != "running" {
		if outputJSON {
			writeJSON(w, map[string]string{"status": mailserverStatus})
			return
		}
		webtemplates.Render(w, "emails_accounts.html", mergeChrome(map[string]interface{}{
			"MailserverStatus": mailserverStatus,
			"Emails":           nil,
			"EmailPairs":       [][2]string{},
			"Flashes":          auth.PopFlashes(w, r, e.Sessions),
		}, r, "Emails"))
		return
	}

	accounts := emailsListAccountsRun()
	if outputJSON {
		writeJSON(w, map[string]interface{}{"emails": accounts})
		return
	}
	pairs := make([][2]string, 0, len(accounts))
	for _, a := range accounts {
		pairs = append(pairs, [2]string{a.Email, a.Quota})
	}
	webtemplates.Render(w, "emails_accounts.html", mergeChrome(map[string]interface{}{
		"MailserverStatus": mailserverStatus,
		"Emails":           accounts,
		"EmailPairs":       pairs,
		"Flashes":          auth.PopFlashes(w, r, e.Sessions),
	}, r, "Emails"))
}

func getEmailQueue() []map[string]interface{} {
	cmd, err := podman.Command("default", "exec", emailsMailserverContainerName, "postqueue", "-j")
	if err != nil {
		return nil
	}
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var queue []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]interface{}
		if json.Unmarshal([]byte(line), &entry) == nil {
			queue = append(queue, entry)
		}
	}
	return queue
}

// ServeEmailsQueue handles GET/POST /emails/queue. Like ServeEmailsAccounts,
// POST is accepted but never actually distinguished from GET.
func (e *Emails) ServeEmailsQueue(w http.ResponseWriter, r *http.Request) {
	mailserverStatus := checkMailserverStatus()
	queue := getEmailQueue()

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, map[string]interface{}{"queue": queue, "mailserver_status": mailserverStatus})
		return
	}
	queueIDs := make([]string, 0, len(queue))
	for _, msg := range queue {
		if id, ok := msg["queue_id"].(string); ok {
			queueIDs = append(queueIDs, id)
		}
	}
	webtemplates.Render(w, "emails_queue.html", mergeChrome(map[string]interface{}{
		"MailserverStatus": mailserverStatus,
		"Queue":            queue,
		"QueueIDs":         queueIDs,
		"Flashes":          auth.PopFlashes(w, r, e.Sessions),
	}, r, "Email Queue"))
}

// ServeEmailsQueueAction handles POST /emails/queue/action.
func (e *Emails) ServeEmailsQueueAction(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	action := r.PostFormValue("action")
	scope := r.PostFormValue("scope")
	queueIDs := r.PostForm["queue_ids"]

	run := func(args ...string) error {
		cmd, err := podman.Command("default", args...)
		if err != nil {
			return err
		}
		return cmd.Run()
	}

	var err error
	switch {
	case action == "retry" && scope == "all":
		err = run("exec", emailsMailserverContainerName, "postqueue", "-f")
	case action == "retry" && scope == "selected":
		for _, qid := range queueIDs {
			if e := run("exec", emailsMailserverContainerName, "postqueue", "-i", qid); e != nil {
				err = e
			}
		}
	case action == "delete" && scope == "all":
		err = run("exec", emailsMailserverContainerName, "postsuper", "-d", "ALL")
	case action == "delete" && scope == "selected":
		for _, qid := range queueIDs {
			if e := run("exec", emailsMailserverContainerName, "postsuper", "-d", qid); e != nil {
				err = e
			}
		}
	}

	if err != nil {
		writeJSONError2(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"success": true})
}

// writeJSONError2 writes {"success": false, "error": ...} -- a distinct
// shape from writeJSONError's {"error": ...}.
func writeJSONError2(w http.ResponseWriter, status int, errMsg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": errMsg})
}

// writeJSONMessage writes {"message": ...} -- the shape used by the
// /emails/api/* endpoints (distinct from writeJSONError's {"error": ...}
// shape used elsewhere in this package).
func writeJSONMessage(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"message": message})
}

// ServeEmailsReports handles GET /emails/reports.
func (e *Emails) ServeEmailsReports(w http.ResponseWriter, r *http.Request) {
	status := checkMailserverStatus()
	flashes := auth.PopFlashes(w, r, e.Sessions)
	switch status {
	case "not_installed":
		webtemplates.Render(w, "emails_mailserver_not_installed_with_header.html", mergeChrome(map[string]interface{}{
			"Flashes": flashes,
		}, r, "Email Reports"))
	case "stopped":
		webtemplates.Render(w, "emails_mailserver_stopped_with_header.html", mergeChrome(map[string]interface{}{
			"Flashes": flashes,
		}, r, "Email Reports"))
	default:
		webtemplates.Render(w, "emails_reports.html", mergeChrome(map[string]interface{}{
			"Flashes": flashes,
		}, r, "Email Reports"))
	}
}

// ServeShowReport handles GET /emails/data/{filename}. No response caching
// is applied, since these reports should always be served fresh. Reports
// are dynamically generated HTML files (not part of this Go binary's
// embedded template set), so they're served as raw bytes rather than run
// through html/template.
func (e *Emails) ServeShowReport(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("filename")
	safeName := filepath.Base(filename)
	if safeName != filename {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	raw, err := os.ReadFile(filepath.Join(EmailsReportsDataDir, safeName))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(raw)
}

func isIPv4(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 255 {
			return false
		}
	}
	return true
}

func getWebmailDomain(publicIP string) string {
	out, err := exec.Command("opencli", "email-webmail").Output()
	if err != nil {
		return ""
	}
	output := strings.TrimSpace(string(out))
	if output != "" {
		if isIPv4(output) {
			return "http://" + output + ":8080/"
		}
		return "https://" + output + "/"
	}
	return publicIP + ":8080"
}

func mailserverExistsAndRunning() bool {
	out, err := emailsPodmanPsRun("-a", "--format", "{{.Names}}")
	if err != nil {
		return false
	}
	found := false
	for _, line := range strings.Split(out, "\n") {
		if line == emailsMailserverContainerName {
			found = true
		}
	}
	if !found {
		return false
	}
	cmd, err := podman.Command("default", "inspect", "-f", "{{.State.Running}}", emailsMailserverContainerName)
	if err != nil {
		return false
	}
	out2, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.ToLower(strings.TrimSpace(string(out2))) == "true"
}

// EnsureMasterUser is called once at process startup. It returns false
// (and logs) on any failure rather than propagating an error -- there is
// nothing to propagate to at startup.
func EnsureMasterUser(masterPass string) bool {
	if !mailserverExistsAndRunning() {
		return false
	}
	listCmd, err := podman.Command("default", "exec", emailsMailserverContainerName, "setup", "dovecot-master", "list")
	if err != nil {
		return false
	}
	out, err := listCmd.Output()
	if err != nil {
		return false
	}

	action := "add"
	if strings.Contains(string(out), emailsMasterUser) {
		action = "update"
	}

	setCmd, err := podman.Command("default", "exec", emailsMailserverContainerName, "setup", "dovecot-master", action, emailsMasterUser, masterPass)
	if err != nil {
		return false
	}
	return setCmd.Run() == nil
}

// webmailToken generates 32 cryptographically random bytes, base64url-
// encoded with no padding. (autologin.go's generateRandomToken is
// intentionally NOT reused here -- it's math/rand-based, which is fine for
// its own non-cryptographic use case, but this token guards an actual IMAP
// credential handoff and must stay CSPRNG-backed.)
func webmailToken() (string, error) {
	b := make([]byte, 32)
	if _, err := cryptorand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// createWebmailToken writes a short-lived signed session payload into a
// file inside the roundcube container for its autologin.php to consume.
var createWebmailToken = func(email, masterPass string) (string, bool) {
	token, err := webmailToken()
	if err != nil {
		return "", false
	}
	payload := map[string]interface{}{
		"email":     email,
		"imap_user": email + "*" + emailsMasterUser,
		"imap_pass": masterPass,
		"expires":   time.Now().UTC().Add(30 * time.Second).Format(time.RFC3339),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", false
	}
	tokenFile := "/tmp/wmt_" + token

	cmd, err := podman.Command("default", "exec", "-i", "openadmin_roundcube", "sh", "-c",
		fmt.Sprintf("tee %s && chown www-data:www-data %s", tokenFile, tokenFile))
	if err != nil {
		return "", false
	}
	cmd.Stdin = strings.NewReader(string(body))
	if err := cmd.Run(); err != nil {
		return "", false
	}
	return token, true
}

// ServeEmailsWebmailLink handles GET /emails/webmail/{email}.
func (e *Emails) ServeEmailsWebmailLink(w http.ResponseWriter, r *http.Request) {
	email := r.PathValue("email")
	webmailURL := getWebmailDomain(e.PublicIP)

	if !emailsRegex.MatchString(email) {
		http.Error(w, "Invalid email format", http.StatusInternalServerError)
		return
	}

	if e.MasterPass == "" {
		http.Redirect(w, r, webmailURL, http.StatusSeeOther)
		return
	}

	token, ok := createWebmailToken(email, e.MasterPass)
	if !ok {
		auth.AddFlash(w, r, e.Sessions, "Error creating a session for user. Check if the webmail service is running.", "danger")
		http.Redirect(w, r, webmailURL, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, webmailURL+"/autologin.php?token="+token, http.StatusSeeOther)
}
