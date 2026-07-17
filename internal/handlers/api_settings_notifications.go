// This file implements the JSON REST API's /api/settings/notifications
// route: viewing or updating notification thresholds, the webhook/email/SMTP
// settings, per-action toggles, and the SSH allowlist. Reuses the same
// config paths, opencli command plumbing, and validators as the HTML
// /settings/notifications page in notification_settings.go -- only the
// response shape and the JSON-body field sourcing differ (a POST field is
// only applied when its key is present in the body, mirroring the
// underlying route's use of `in data` / `.get(..., None)` checks instead of
// a form's always-present fields).
package handlers

import (
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"openadmin/internal/config"
)

// APISettingsNotifications bundles the /api/settings/notifications handler.
type APISettingsNotifications struct{}

// Serve handles GET/POST /api/settings/notifications.
func (a *APISettingsNotifications) Serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		a.handlePost(w, r)
		return
	}
	a.handleGet(w, r)
}

func (a *APISettingsNotifications) handleGet(w http.ResponseWriter, r *http.Request) {
	mainConfig := config.Load(config.OpenpanelConfigPath)
	notifConfig := config.Load(NotificationsConfigPath)

	writeJSON(w, map[string]interface{}{
		"email_address":       mainConfig.Get("DEFAULT", "email", ""),
		"mail_server":         mainConfig.Get("SMTP", "mail_server", ""),
		"mail_port":           mainConfig.Get("SMTP", "mail_port", "465"),
		"mail_use_tls":        mainConfig.Get("SMTP", "mail_use_tls", ""),
		"mail_use_ssl":        mainConfig.Get("SMTP", "mail_use_ssl", ""),
		"mail_debug":          mainConfig.Get("SMTP", "mail_debug", ""),
		"mail_username":       mainConfig.Get("SMTP", "mail_username", ""),
		"mail_password":       mainConfig.Get("SMTP", "mail_password", ""),
		"mail_default_sender": mainConfig.Get("SMTP", "mail_default_sender", ""),
		"ssh_whitelist":       readSSHWhitelist(),
		"settings":            notifConfig,
	})
}

// apiNotifSMTPFields lists each SMTP field this endpoint accepts along with
// the validator (nil means no validation) used to decide whether a
// submitted value gets applied -- mirrors the smtp_fields table in the
// underlying route exactly, including which fields go unvalidated.
var apiNotifSMTPFields = []struct {
	Key       string
	Validator func(string) bool
}{
	{"mail_server", nil},
	{"mail_port", isValidPort},
	{"mail_use_tls", isValidBool},
	{"mail_use_ssl", isValidBool},
	{"mail_debug", isValidBool},
	{"mail_username", isValidEmail},
	{"mail_password", nil},
	{"mail_default_sender", isValidEmail},
}

func (a *APISettingsNotifications) handlePost(w http.ResponseWriter, r *http.Request) {
	var data map[string]interface{}
	if !apiDecodeJSONBody(r, &data) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	mainConfig := config.Load(config.OpenpanelConfigPath)
	notifConfig := config.Load(NotificationsConfigPath)
	notifDefault := notifConfig["DEFAULT"]
	actionsConfig := notifConfig["ACTIONS"]
	httpConfig := notifConfig["HTTP"]
	smtpConfig := mainConfig["SMTP"]
	currentEmail := mainConfig.Get("DEFAULT", "email", "")

	for _, key := range notifNumericKeys {
		apiNotifUpdateNumericIfChanged(data, key, notifDefault[key])
	}
	for _, key := range httpNumericKeys {
		apiNotifUpdateNumericIfChanged(data, key, httpConfig[key])
	}

	if newWebhook, ok := apiNotifFieldValue(data, "webhook_url"); ok && apiNotifChanged(notifDefault["webhook_url"], newWebhook) {
		runOpenCLINotificationUpdate("webhook_url", newWebhook)
	}

	if newEmail, ok := apiNotifFieldValue(data, "email"); ok && apiNotifChanged(currentEmail, newEmail) {
		runOpenCLI("", "opencli", "config", "update", "email", newEmail)
	}

	for _, f := range apiNotifSMTPFields {
		newVal, ok := apiNotifFieldValue(data, f.Key)
		if !ok || !apiNotifChanged(smtpConfig[f.Key], newVal) {
			continue
		}
		if f.Validator != nil && !f.Validator(newVal) {
			continue
		}
		runOpenCLI("", "opencli", "config", "update", f.Key, newVal)
	}

	if newServices, ok := apiNotifFieldValue(data, "services"); ok && apiNotifChanged(notifDefault["services"], newServices) {
		runOpenCLINotificationUpdate("services", newServices)
	}

	for _, toggle := range defaultToggles {
		val, present := data[toggle]
		if !present {
			continue
		}
		newVal := "no"
		if apiNotifToggleTruthy(val) {
			newVal = "yes"
		}
		if apiNotifChanged(notifDefault[toggle], newVal) {
			runOpenCLINotificationUpdate(toggle, newVal)
		}
	}
	for _, toggle := range actionToggles {
		val, present := data[toggle]
		if !present {
			continue
		}
		newVal := "no"
		if apiNotifToggleTruthy(val) {
			newVal = "yes"
		}
		if apiNotifChanged(actionsConfig[toggle], newVal) {
			runOpenCLINotificationUpdate(toggle, newVal)
		}
	}

	if rawWhitelist, present := data["ssh_whitelist"]; present {
		newWhitelist, _ := rawWhitelist.(string)
		var validIPs []string
		for _, line := range strings.Split(newWhitelist, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if _, _, err := net.ParseCIDR(withCIDRSuffix(line)); err != nil {
				writeJSONError(w, http.StatusBadRequest, "Invalid IP/CIDR: "+line)
				return
			}
			validIPs = append(validIPs, line)
		}

		if !slicesEqual(validIPs, apiSSHWhitelistLines()) {
			var b strings.Builder
			b.WriteString("# SSH Whitelist\n")
			for _, ip := range validIPs {
				b.WriteString(ip + "\n")
			}
			if err := os.WriteFile(SSHWhitelistPath, []byte(b.String()), 0644); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "Error updating SSH whitelist: "+err.Error())
				return
			}
		}
	}

	writeJSON(w, map[string]interface{}{"success": true, "message": "Notification settings updated."})
}

// apiSSHWhitelistLines returns the currently-saved whitelist as individual
// trimmed, non-comment lines -- unlike readSSHWhitelist (which joins them
// with "\n" for display), this is used for an exact line-by-line comparison
// against the submitted list.
func apiSSHWhitelistLines() []string {
	raw, err := os.ReadFile(SSHWhitelistPath)
	if err != nil {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(string(raw), "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	return lines
}

// apiNotifFieldValue reports whether key is present in data with a non-null
// value, returning its text form -- mirrors `data.get(field) is not None`.
func apiNotifFieldValue(data map[string]interface{}, key string) (string, bool) {
	v, ok := data[key]
	if !ok || v == nil {
		return "", false
	}
	return apiJSONValueToString(v), true
}

// apiNotifChanged mirrors the underlying route's changed() helper: both
// sides are compared as trimmed strings.
func apiNotifChanged(current, newVal string) bool {
	return strings.TrimSpace(current) != strings.TrimSpace(newVal)
}

// apiNotifToggleTruthy reports whether a decoded JSON value should turn a
// toggle on -- only the boolean true or the strings "on"/"yes" count,
// mirroring `data.get(toggle) in ('on', True, 'yes')` exactly (a JSON
// number or any other value is not truthy here even though it might be for
// other endpoints).
func apiNotifToggleTruthy(v interface{}) bool {
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val == "on" || val == "yes"
	default:
		return false
	}
}

// apiNotifToInt converts a decoded JSON value to an int: a JSON number
// truncates toward zero, a numeric string parses, anything else fails
// silently -- the caller then leaves that field unchanged.
func apiNotifToInt(v interface{}) (int, bool) {
	switch val := v.(type) {
	case float64:
		return int(val), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil {
			return 0, false
		}
		return n, true
	case bool:
		if val {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}

func apiNotifUpdateNumericIfChanged(data map[string]interface{}, key, currentVal string) {
	v, ok := data[key]
	if !ok || v == nil {
		return
	}
	n, ok := apiNotifToInt(v)
	if !ok {
		return
	}
	if apiNotifChanged(currentVal, strconv.Itoa(n)) {
		runOpenCLINotificationUpdate(key, strconv.Itoa(n))
	}
}
