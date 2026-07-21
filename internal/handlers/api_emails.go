// This file implements the JSON REST API's mail server routes:
// GET/POST /api/emails/settings, GET/POST/DELETE /api/emails/accounts,
// GET/POST /api/emails/queue, and GET/POST /api/emails/domain-limits. All
// four reuse the same underlying mailserver/postfwd plumbing (env-file
// parsing, opencli/podman command wrappers) as the HTML admin pages in
// emails.go and email_domain_limits.go -- only the response shape (a JSON
// body instead of a rendered template or redirect+flash) differs here.
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"openadmin/internal/podman"
)

// APIEmails bundles the /api/emails/* handlers.
type APIEmails struct {
	PublicIP string
}

// emailsAPIOpencliRun invokes opencli for the webmail domain/software
// updates below; injectable so tests never shell out for real.
var emailsAPIOpencliRun = func(args ...string) error {
	return exec.Command(args[0], args[1:]...).Run()
}

// emailsAPIPostfwdToggleRun fires the postfwd enable/disable command in the
// background, mirroring the fire-and-forget subprocess call it's based on.
var emailsAPIPostfwdToggleRun = func(action string) {
	_ = exec.Command("opencli", "email-server", "postfwd", action).Start()
}

// emailsAPIQueuePodmanRun runs a single podman command against the
// mailserver container; injectable so tests never shell out for real.
var emailsAPIQueuePodmanRun = func(args ...string) error {
	cmd, err := podman.Command("default", args...)
	if err != nil {
		return err
	}
	return cmd.Run()
}

// apiEmailsStringField reads a string field out of a decoded JSON object,
// returning "" for a missing key or a value that isn't a JSON string.
func apiEmailsStringField(data map[string]interface{}, key string) string {
	if v, ok := data[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// webmailDomainAlreadyConfigured walks the Caddy config tree looking for
// any file that already mentions domain, the same check the admin page
// performs before calling opencli to register a new webmail domain.
func webmailDomainAlreadyConfigured(domain string) bool {
	found := false
	_ = filepath.Walk(EmailsCaddyConfigDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || found {
			return nil
		}
		content, rerr := os.ReadFile(path)
		if rerr == nil && strings.Contains(string(content), domain) {
			found = true
		}
		return nil
	})
	return found
}

// ServeSettings handles GET/POST /api/emails/settings.
func (a *APIEmails) ServeSettings(w http.ResponseWriter, r *http.Request) {
	accounts := emailsListAccountsRun()
	totalCount := len(accounts)

	if r.Method == http.MethodPost {
		if !apiIsJSONContentType(r) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}
		var data map[string]interface{}
		// A malformed body despite a JSON content type is treated the same
		// as a missing/wrong content type -- both end up as the same 400
		// below once none of the recognized fields are found in a nil map.
		json.NewDecoder(r.Body).Decode(&data)

		storageType := apiEmailsStringField(data, "storage_type")
		emailStorageLocation := apiEmailsStringField(data, "email_storage_location")
		if storageType != "" && emailStorageLocation != "" {
			if storageType != "user_dir" && storageType != "custom" {
				writeJSONError(w, http.StatusBadRequest, "Invalid storage_type")
				return
			}
			if storageType == "user_dir" {
				emailStorageLocation = storageType
			}
			if totalCount != 0 {
				writeJSONError(w, http.StatusBadRequest, "Email storage location cannot be changed when email accounts already exist.")
				return
			}
			if !isValidEmailStorageLocation(emailStorageLocation) {
				writeJSONError(w, http.StatusBadRequest, "Invalid email storage location. Provide either 'user_dir' or full path.")
				return
			}
			if err := updateEmailStorageLocation(emailStorageLocation); err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, map[string]interface{}{"success": true, "message": "Email storage location updated successfully. Make sure to stop and start Mailserver for new storage to apply"})
			return
		}

		webmailDomain := apiEmailsStringField(data, "webmail_domain")
		if webmailDomain != "" {
			webmailDomain = strings.TrimPrefix(webmailDomain, "http://")
			webmailDomain = strings.TrimPrefix(webmailDomain, "https://")
			webmailDomain = strings.TrimSuffix(webmailDomain, "/")

			if webmailDomainAlreadyConfigured(webmailDomain) {
				writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("'%s' already exists in Caddy configuration.", webmailDomain))
				return
			}
			if err := emailsAPIOpencliRun("opencli", "email-webmail", "domain", webmailDomain); err != nil {
				writeJSONError2(w, http.StatusInternalServerError, "Failed to update webmail domain.")
				return
			}
			writeJSON(w, map[string]interface{}{"success": true, "message": "Webmail domain updated successfully!"})
			return
		}

		webmailSoftware := apiEmailsStringField(data, "webmail_software")
		if webmailSoftware != "" {
			if webmailSoftware != "roundcube" {
				writeJSONError(w, http.StatusBadRequest, "Invalid webmail client selected.")
				return
			}
			if err := emailsAPIOpencliRun("opencli", "email-webmail", webmailSoftware); err != nil {
				writeJSONError2(w, http.StatusInternalServerError, "Failed to restart webmail service.")
				return
			}
			writeJSON(w, map[string]interface{}{"success": true, "message": "Configuration updated successfully! Webmail service will be restarted in the background."})
			return
		}

		checkboxKeys := []string{
			"ENABLE_POSTFWD", "ENABLE_AMAVIS", "ENABLE_DNSBL", "ENABLE_RSPAMD", "ENABLE_SPAMASSASSIN",
			"ENABLE_MTA_STS", "ENABLE_OPENDKIM", "ENABLE_OPENDMARC", "ENABLE_POP3", "ENABLE_IMAP",
			"ENABLE_CLAMAV", "ENABLE_FAIL2BAN", "SMTP_ONLY", "ENABLE_SRS",
		}
		var triggeredServices []string
		touched := false
		for _, key := range checkboxKeys {
			v, present := data[key]
			if !present {
				continue
			}
			touched = true
			value := apiJSONValueToString(v)
			if key == "ENABLE_POSTFWD" {
				action := "disable"
				if value == "1" {
					action = "enable"
				}
				emailsAPIPostfwdToggleRun(action)
			} else {
				_ = updateEnvVariable(key, value)
			}
			if key == "ENABLE_FAIL2BAN" {
				triggeredServices = append(triggeredServices, "Fail2Ban")
			} else if key == "ENABLE_CLAMAV" {
				triggeredServices = append(triggeredServices, "ClamAV")
			}
		}

		if touched {
			emailsRestartMailserverRun()
			if len(triggeredServices) > 0 {
				writeJSON(w, map[string]interface{}{"success": true, "message": fmt.Sprintf("Configuration updated! %s require Mailserver to be recreated. Make sure to stop&start the service.", strings.Join(triggeredServices, " and "))})
				return
			}
			writeJSON(w, map[string]interface{}{"success": true, "message": "Configuration updated successfully! Mailserver will be restarted in the background to apply new configuration."})
			return
		}

		writeJSONError(w, http.StatusBadRequest, "No recognized settings provided.")
		return
	}

	webmailStatus, webmailServices := checkWebmailStatus()
	mailserverStatus := checkMailserverStatus()
	webmailDomain := getWebmailDomain(a.PublicIP)
	configData, _ := parseEnvFile()
	emailStorageLocation := getEmailStorageLocation()

	writeJSON(w, map[string]interface{}{
		"webmail-status":         webmailStatus,
		"webmail-domain":         webmailDomain,
		"webmail-selected":       nonNilStrings(webmailServices),
		"mailserver-status":      mailserverStatus,
		"emails":                 totalCount,
		"config_data":            configData,
		"email_storage_location": emailStorageLocation,
	})
}

// ServeAccounts handles GET/POST/DELETE /api/emails/accounts.
func (a *APIEmails) ServeAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		mailserverStatus := checkMailserverStatus()
		if mailserverStatus != "running" {
			writeJSON(w, map[string]interface{}{"status": mailserverStatus, "emails": []interface{}{}})
			return
		}
		writeJSON(w, map[string]interface{}{"status": mailserverStatus, "emails": emailsListAccountsRun()})
		return
	}

	if !apiIsJSONContentType(r) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	var data map[string]interface{}
	json.NewDecoder(r.Body).Decode(&data)

	if r.Method == http.MethodDelete {
		emailsList, ok := data["emails"].([]interface{})
		if !ok || len(emailsList) == 0 {
			writeJSONMessage(w, http.StatusBadRequest, "No emails provided.")
			return
		}
		args := []string{"opencli", "email-setup", "email", "del"}
		for _, e := range emailsList {
			if s, ok := e.(string); ok {
				args = append(args, s)
			} else {
				args = append(args, apiJSONValueToString(e))
			}
		}
		success, output := runEmailsCmd(args)
		if success {
			writeJSON(w, map[string]string{"message": fmt.Sprintf("Deleted %d account(s).", len(emailsList))})
			return
		}
		writeJSONMessage(w, http.StatusInternalServerError, "Failed: "+output)
		return
	}

	action := strings.TrimSpace(apiEmailsStringField(data, "action"))
	email := strings.TrimSpace(apiEmailsStringField(data, "email"))

	switch action {
	case "password":
		password := strings.TrimSpace(apiEmailsStringField(data, "password"))
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

	case "quota-set":
		quota := strings.TrimSpace(apiEmailsStringField(data, "quota"))
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

	case "quota-del":
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

	case "restrict":
		restrictAction := strings.TrimSpace(apiEmailsStringField(data, "restrict_action"))
		rtype := strings.TrimSpace(apiEmailsStringField(data, "type"))
		if email == "" || (restrictAction != "add" && restrictAction != "del") || (rtype != "send" && rtype != "receive") {
			writeJSONMessage(w, http.StatusBadRequest, "Invalid parameters.")
			return
		}
		ok, out := runEmailsCmd([]string{"opencli", "email-setup", "email", "restrict", restrictAction, rtype, email})
		if ok {
			writeJSON(w, map[string]string{"message": "Restriction updated."})
			return
		}
		writeJSONMessage(w, http.StatusInternalServerError, "Failed: "+out)

	default:
		writeJSONError(w, http.StatusBadRequest, "Invalid action. Use 'password', 'quota-set', 'quota-del', or 'restrict'.")
	}
}

// ServeQueue handles GET/POST /api/emails/queue.
func (a *APIEmails) ServeQueue(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		mailserverStatus := checkMailserverStatus()
		queue := getEmailQueue()
		if queue == nil {
			queue = []map[string]interface{}{}
		}
		writeJSON(w, map[string]interface{}{"queue": queue, "mailserver_status": mailserverStatus})
		return
	}

	if !apiIsJSONContentType(r) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	var body struct {
		Action   string   `json:"action"`
		Scope    string   `json:"scope"`
		QueueIDs []string `json:"queue_ids"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	var err error
	switch {
	case body.Action == "retry" && body.Scope == "all":
		err = emailsAPIQueuePodmanRun("exec", emailsMailserverContainerName, "postqueue", "-f")
	case body.Action == "retry" && body.Scope == "selected":
		for _, qid := range body.QueueIDs {
			if e := emailsAPIQueuePodmanRun("exec", emailsMailserverContainerName, "postqueue", "-i", qid); e != nil {
				err = e
			}
		}
	case body.Action == "delete" && body.Scope == "all":
		err = emailsAPIQueuePodmanRun("exec", emailsMailserverContainerName, "postsuper", "-d", "ALL")
	case body.Action == "delete" && body.Scope == "selected":
		for _, qid := range body.QueueIDs {
			if e := emailsAPIQueuePodmanRun("exec", emailsMailserverContainerName, "postsuper", "-d", qid); e != nil {
				err = e
			}
		}
	default:
		writeJSONError(w, http.StatusBadRequest, "Invalid action/scope. Use action=retry|delete, scope=all|selected.")
		return
	}

	if err != nil {
		writeJSONError2(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"success": true})
}

// ServeDomainLimits handles GET/POST /api/emails/domain-limits.
func (a *APIEmails) ServeDomainLimits(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		hitsDomain := strings.TrimSpace(r.URL.Query().Get("hits"))
		if hitsDomain != "" {
			lines := getLimitHitsRun(hitsDomain, 30)
			writeJSON(w, map[string]interface{}{"ok": true, "domain": hitsDomain, "lines": lines})
			return
		}

		rawContent := readPostfwdRaw()
		rules := parsePostfwdRules(rawContent)

		usernameSet := map[string]bool{}
		for _, rule := range rules {
			usernameSet[rule.Username] = true
		}
		usernames := make([]string, 0, len(usernameSet))
		for u := range usernameSet {
			usernames = append(usernames, u)
		}
		sort.Strings(usernames)

		counters := getPostfwdCountersRun()
		for i := range rules {
			if v, ok := counters[rules[i].Username]; ok {
				rules[i].Current = &v
			}
		}

		writeJSON(w, map[string]interface{}{"rules": rules, "usernames": usernames, "raw_content": rawContent})
		return
	}

	if !apiIsJSONContentType(r) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	var data map[string]interface{}
	json.NewDecoder(r.Body).Decode(&data)

	if rawContentVal, ok := data["raw_content"]; ok {
		content, _ := rawContentVal.(string)
		if err := os.WriteFile(PostfwdConfigPath, []byte(content), 0644); err != nil {
			writeJSONError2(w, http.StatusInternalServerError, "Save failed: "+err.Error())
			return
		}
		hupPostfwdRun()
		writeJSON(w, map[string]interface{}{"success": true, "message": "File saved and postfwd reloaded."})
		return
	}

	action := apiEmailsStringField(data, "action")
	domain := strings.TrimSpace(apiEmailsStringField(data, "domain"))
	username := strings.TrimSpace(apiEmailsStringField(data, "username"))

	switch action {
	case "update-domain":
		if domain == "" || username == "" {
			writeJSONOkMsg(w, http.StatusBadRequest, false, "domain and username are required")
			return
		}
		limit, ok := domainLimitAsPositiveInt(data["limit"])
		if !ok {
			writeJSONOkMsg(w, http.StatusBadRequest, false, "limit must be a positive integer")
			return
		}
		ok2, out := writeDomainRule(username, limit, domain)
		writeJSONOkMsg(w, http.StatusOK, ok2, out)

	case "reset-domain":
		if domain == "" {
			writeJSONOkMsg(w, http.StatusBadRequest, false, "domain is required")
			return
		}
		ok, out := runRatelimitScriptRun(false, "--domain="+domain)
		writeJSONOkMsg(w, http.StatusOK, ok, out)

	case "reset-user":
		if username == "" {
			writeJSONOkMsg(w, http.StatusBadRequest, false, "username is required")
			return
		}
		ok, out := runRatelimitScriptRun(false, "--username="+username)
		writeJSONOkMsg(w, http.StatusOK, ok, out)

	case "reset-all":
		ok, out := runRatelimitScriptRun(false, "--all-users")
		writeJSONOkMsg(w, http.StatusOK, ok, out)

	case "delete-domain":
		if domain == "" {
			writeJSONOkMsg(w, http.StatusBadRequest, false, "domain is required")
			return
		}
		ok, out := runRatelimitScriptRun(false, "--delete-domain="+domain)
		writeJSONOkMsg(w, http.StatusOK, ok, out)

	case "delete-user":
		if username == "" {
			writeJSONOkMsg(w, http.StatusBadRequest, false, "username is required")
			return
		}
		ok, out := runRatelimitScriptRun(false, "--delete-user="+username)
		writeJSONOkMsg(w, http.StatusOK, ok, out)

	case "delete-all":
		if err := os.WriteFile(PostfwdConfigPath, []byte(""), 0644); err != nil {
			writeJSONOkMsg(w, http.StatusOK, false, err.Error())
			return
		}
		hupPostfwdRun()
		writeJSONOkMsg(w, http.StatusOK, true, "All rate-limit rules removed.")

	default:
		writeJSONError(w, http.StatusBadRequest, "Invalid action.")
	}
}
