package handlers

import (
	"bufio"
	"net"
	"net/http"
	"strings"

	"openadmin/internal/activity"
	"openadmin/internal/auth"
	"openadmin/internal/server"
)

// statusRecorder remembers the status code so failed requests can be marked in the log
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(s.ResponseWriter).Hijack()
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// activityIgnored are POSTs that don't change anything an admin did, logins have their own log
var activityIgnored = map[string]bool{
	"POST /login":                            true,
	"POST /login/":                           true,
	"POST /login/2fa":                        true,
	"POST /login/passkey/begin":              true,
	"POST /login/passkey/complete":           true,
	"POST /send_email":                       true,
	"POST /security/passkeys/register/begin": true,
	"POST /settings/notifications/test-smtp": true,
	"POST /api/tour/complete":                true,
	"POST /api/quickstart/dismiss":           true,
}

// ActivityMiddleware writes one line to the acting account's activity log for every request that changes something
func ActivityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		r = r.WithContext(activity.WithRequest(r.Context()))
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		apiUser, described, skip, failed := activity.Result(r.Context())
		if skip || r.Pattern == "" || activityIgnored[r.Pattern] {
			return
		}
		// auth failures and unknown routes aren't something the admin did
		switch rec.status {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed:
			return
		}

		username := apiUser
		if user := auth.CurrentUser(r); username == "" && user != nil {
			username = user.Username
		}
		if username == "" {
			return
		}

		action := described
		if action == "" {
			action = describeActivity(r)
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			action += " (via API)"
		}
		if failed || rec.status >= 400 {
			action += " (failed)"
		}
		activity.Record(username, server.GetClientIP(r), action)
	})
}

// actPath and actForm read request data for descriptions, the form is only there if the handler parsed it
func actPath(r *http.Request, name string) string { return r.PathValue(name) }

func actForm(r *http.Request, name string) string {
	if r.Form == nil {
		return ""
	}
	return strings.TrimSpace(r.Form.Get(name))
}

// actJoin builds "verb thing" and drops empty parts so a missing form value doesn't leave double spaces
func actJoin(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

func quoted(s string) string {
	if s == "" {
		return ""
	}
	return "'" + s + "'"
}

func onOff(v string) string {
	switch strings.ToLower(v) {
	case "on", "true", "1", "yes", "enable", "enabled":
		return "on"
	case "off", "false", "0", "no", "disable", "disabled":
		return "off"
	}
	return v
}

func titleWord(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// accountActions describes the shared /administrators and /resellers form actions
func accountActions(r *http.Request, kind string) string {
	user := quoted(actForm(r, "username"))
	switch actForm(r, "action") {
	case "create":
		return actJoin("Created", kind, user)
	case "delete":
		return actJoin("Deleted", kind, user)
	case "suspend":
		return actJoin("Suspended", kind, user)
	case "unsuspend":
		return actJoin("Unsuspended", kind, user)
	case "reset_password":
		return actJoin("Changed password for", kind, user)
	case "rename_user":
		return actJoin("Renamed", kind, user, "to", quoted(actForm(r, "new_username")))
	case "disable_2fa":
		return actJoin("Disabled 2FA for", kind, user)
	case "disable_passkeys":
		return actJoin("Removed passkeys for", kind, user)
	case "update":
		return actJoin("Updated limits for", kind, user)
	case "update_branding":
		return actJoin("Updated branding for", kind, user)
	}
	return actJoin("Updated", kind, user)
}

var activityDescriptions = map[string]func(r *http.Request) string{
	// administrators and resellers
	"POST /administrators": func(r *http.Request) string { return accountActions(r, "administrator") },
	"POST /resellers":      func(r *http.Request) string { return accountActions(r, "reseller") },

	// own account
	"POST /security/2fa/enable":                 func(r *http.Request) string { return "Enabled 2FA for own account" },
	"POST /security/2fa/disable":                func(r *http.Request) string { return "Disabled 2FA for own account" },
	"POST /security/passkeys/register/complete": func(r *http.Request) string { return "Added a passkey" },
	"POST /security/passkeys/rename":            func(r *http.Request) string { return "Renamed a passkey" },
	"POST /security/passkeys/delete":            func(r *http.Request) string { return "Deleted a passkey" },

	// users
	"POST /user/new": func(r *http.Request) string {
		return actJoin("Created user", quoted(strings.ToLower(actForm(r, "admin_username"))), withPlan(actForm(r, "plan_name")))
	},
	"POST /user/{action}/{username}": func(r *http.Request) string {
		user := quoted(actPath(r, "username"))
		switch actPath(r, "action") {
		case "suspend":
			return actJoin("Suspended user", user)
		case "unsuspend":
			return actJoin("Unsuspended user", user)
		case "delete":
			return actJoin("Deleted user", user)
		case "edit":
			return actJoin("Edited user", user)
		case "permissions":
			return actJoin("Changed feature permissions for user", user)
		case "permissions_reset":
			return actJoin("Reset feature permissions for user", user)
		}
		return actJoin(titleWord(actPath(r, "action")), "user", user)
	},
	"POST /get_user_notes/{username}": func(r *http.Request) string { return actJoin("Edited notes for user", quoted(actPath(r, "username"))) },
	"POST /get_custom_message_for_user/{username}": func(r *http.Request) string {
		return actJoin("Edited custom message for user", quoted(actPath(r, "username")))
	},
	"POST /users/{username}/account-setting/{field}": func(r *http.Request) string {
		return actJoin("Changed", titleWord(actPath(r, "field")), "for user", quoted(actPath(r, "username")))
	},
	"POST /containers/{username}/{action}/{container_name}": func(r *http.Request) string {
		return actJoin(titleWord(actPath(r, "action")), "container", quoted(actPath(r, "container_name")), "of user", quoted(actPath(r, "username")))
	},
	"POST /json/php/default_version/{username}": func(r *http.Request) string {
		return actJoin("Changed default PHP version for user", quoted(actPath(r, "username")))
	},
	"POST /user/export/create/{username}": func(r *http.Request) string { return actJoin("Exported user", quoted(actPath(r, "username"))) },
	"POST /user/export/delete/{username}": func(r *http.Request) string {
		return actJoin("Deleted an export of user", quoted(actPath(r, "username")))
	},
	"POST /import/{panel_type}": func(r *http.Request) string { return actJoin("Imported an account from", actPath(r, "panel_type")) },
	"POST /import/transfer/":    func(r *http.Request) string { return "Started an account transfer" },

	// plans and features
	"POST /plans":     func(r *http.Request) string { return "Updated plans" },
	"POST /plans/new": func(r *http.Request) string { return actJoin("Created plan", quoted(actForm(r, "name"))) },
	"POST /plans/{plan_id}": func(r *http.Request) string {
		return actJoin("Edited plan", quoted(firstNonEmpty(actForm(r, "name"), actPath(r, "plan_id"))))
	},
	"POST /plan/delete/{plan_name}": func(r *http.Request) string { return actJoin("Deleted plan", quoted(actPath(r, "plan_name"))) },
	"POST /features":                func(r *http.Request) string { return "Updated feature sets" },
	"POST /features/":               func(r *http.Request) string { return "Updated feature sets" },
	"POST /features/{plan}":         func(r *http.Request) string { return actJoin("Updated feature set", quoted(actPath(r, "plan"))) },

	// domains
	"POST /domains/add": func(r *http.Request) string {
		return actJoin("Added domain", quoted(actForm(r, "domain")), "for user", quoted(actForm(r, "username")))
	},
	"POST /domains/vhost/{username}/{domain_name}": func(r *http.Request) string {
		return actJoin("Edited VirtualHost of domain", quoted(actPath(r, "domain_name")))
	},
	"POST /domains/{seg2}/{seg3}": func(r *http.Request) string {
		seg2, seg3 := actPath(r, "seg2"), actPath(r, "seg3")
		switch {
		case seg3 == "toggle":
			domain := quoted(actForm(r, "domain_name"))
			switch strings.ToLower(seg2) {
			case "waf":
				return actJoin("Turned WAF", onOff(actForm(r, "modsec_action")), "for domain", domain)
			case "hsts":
				return actJoin("Turned HSTS", onOff(actForm(r, "hsts_action")), "for domain", domain)
			case "dns":
				return actJoin("Changed DNS status for domain", domain)
			}
			return actJoin("Toggled", seg2, "for domain", domain)
		case seg2 == "dns":
			return actJoin("Edited DNS zone of domain", quoted(seg3))
		case seg2 == "caddy":
			return actJoin("Edited Caddyfile of domain", quoted(seg3))
		case seg2 == "config":
			return actJoin("Edited webserver config of user", quoted(seg3))
		case seg2 == "ssl":
			return actJoin("Changed SSL of domain", quoted(seg3))
		}
		return "POST " + r.URL.Path
	},
	"POST /domains/zone-templates": func(r *http.Request) string { return "Edited DNS zone templates" },
	"POST /domains/file-templates": func(r *http.Request) string { return "Edited domain file templates" },
	"POST /domains/dns-cluster":    func(r *http.Request) string { return "Changed DNS cluster settings" },

	// emails
	"POST /emails/settings":               func(r *http.Request) string { return "Changed email settings" },
	"POST /emails/api/update-password":    func(r *http.Request) string { return "Changed password of an email account" },
	"POST /emails/api/quota-set":          func(r *http.Request) string { return "Set quota of an email account" },
	"POST /emails/api/quota-del":          func(r *http.Request) string { return "Removed quota of an email account" },
	"POST /emails/api/restrict":           func(r *http.Request) string { return "Changed sending/receiving restrictions of an email account" },
	"POST /emails/api/delete":             func(r *http.Request) string { return "Deleted an email account" },
	"POST /emails/accounts":               func(r *http.Request) string { return "Changed email accounts" },
	"POST /emails/queue":                  func(r *http.Request) string { return "Changed the mail queue" },
	"POST /emails/queue/action":           func(r *http.Request) string { return actJoin("Mail queue action", actForm(r, "action")) },
	"POST /emails/domain-limits/save-raw": func(r *http.Request) string { return "Edited email rate limits" },
	"POST /emails/domain-limits/api":      func(r *http.Request) string { return "Changed email rate limits" },

	// services
	"POST /services":      func(r *http.Request) string { return "Changed services" },
	"POST /services/edit": func(r *http.Request) string { return "Edited a service" },
	"POST /service/{action}/{service_name}": func(r *http.Request) string {
		return actJoin(titleWord(actPath(r, "action")), "service", quoted(actPath(r, "service_name")))
	},
	"POST /services/ftp/refresh":  func(r *http.Request) string { return "Refreshed FTP accounts" },
	"POST /services/ftp":          func(r *http.Request) string { return "Changed FTP accounts" },
	"POST /services/ftp/settings": func(r *http.Request) string { return "Changed FTP settings" },
	"POST /services/podman/images/bulk/{action}": func(r *http.Request) string {
		return actJoin("Container images:", strings.ReplaceAll(actPath(r, "action"), "-", " "))
	},
	"POST /services/podman/images/{action}/{id...}": func(r *http.Request) string {
		return actJoin(titleWord(actPath(r, "action")), "container image", quoted(actPath(r, "id")))
	},
	"POST /services/limits":              func(r *http.Request) string { return "Changed service resource limits" },
	"POST /services/logs/edit":           func(r *http.Request) string { return "Changed log settings" },
	"POST /services/logs/raw":            func(r *http.Request) string { return "Edited a log file" },
	"DELETE /services/logs/raw":          func(r *http.Request) string { return "Emptied a log file" },
	"POST /settings/updates/log/":        func(r *http.Request) string { return "Changed update log" },
	"POST /services/updates/log/raw":     func(r *http.Request) string { return "Edited update log" },
	"DELETE /services/updates/log/raw":   func(r *http.Request) string { return "Emptied update log" },
	"POST /services/crashlogs/log/":      func(r *http.Request) string { return "Changed crash logs" },
	"POST /services/crashlogs/log/raw":   func(r *http.Request) string { return "Edited a crash log" },
	"DELETE /services/crashlogs/log/raw": func(r *http.Request) string { return "Emptied a crash log" },

	// backups
	"POST /backups/user/settings":      func(r *http.Request) string { return "Changed user backup settings" },
	"POST /backups/user/configuration": func(r *http.Request) string { return "Changed user backup configuration" },
	"POST /backups/user/run":           func(r *http.Request) string { return "Started a user backup" },
	"POST /backups/system":             func(r *http.Request) string { return "Changed system backup settings" },
	"POST /backups/system/run":         func(r *http.Request) string { return "Started a system backup" },
	"POST /backups/system/restore/{filename...}": func(r *http.Request) string {
		return actJoin("Restored system backup", quoted(actPath(r, "filename")))
	},
	"POST /backups/system/delete/{filename...}": func(r *http.Request) string {
		return actJoin("Deleted system backup", quoted(actPath(r, "filename")))
	},

	// server
	"POST /server/timezone":               func(r *http.Request) string { return "Changed server timezone" },
	"POST /server/root-password":          func(r *http.Request) string { return "Changed root password" },
	"POST /server/memory_usage/drop":      func(r *http.Request) string { return "Dropped memory caches" },
	"POST /server/memory_usage/drop-swap": func(r *http.Request) string { return "Cleared swap" },
	"POST /server/crons":                  func(r *http.Request) string { return "Changed scheduled actions" },
	"POST /server/reboot":                 func(r *http.Request) string { return "Rebooted the server" },
	"POST /server/swap/action/{action}":   func(r *http.Request) string { return actJoin("Swap:", actPath(r, "action")) },
	"POST /server/migrate":                func(r *http.Request) string { return "Started a server migration" },
	"POST /server/processes/{pid}/{action}": func(r *http.Request) string {
		return actJoin(titleWord(actPath(r, "action")), "process", actPath(r, "pid"))
	},
	"POST /server/node":       func(r *http.Request) string { return "Changed cluster nodes" },
	"POST /server/ssh":        func(r *http.Request) string { return "Changed SSH access" },
	"POST /server/ssh/config": func(r *http.Request) string { return "Edited SSH configuration" },
	"POST /server/demo-mode":  func(r *http.Request) string { return "Changed demo mode" },

	// security
	"POST /security/disable-admin":        func(r *http.Request) string { return "Disabled OpenAdmin" },
	"POST /security/basic_auth":           func(r *http.Request) string { return "Changed Basic Authentication" },
	"POST /security/blacklist-useragents": func(r *http.Request) string { return "Edited blocked user agents" },
	"POST /security/waf":                  func(r *http.Request) string { return "Changed WAF settings" },
	"POST /security/waf/rules":            func(r *http.Request) string { return "Changed WAF rules" },
	"POST /configservercsf/iframe/":       func(r *http.Request) string { return "Changed ConfigServer Firewall" },
	"POST /imav/{path...}":                func(r *http.Request) string { return "Changed ImunifyAV" },

	// notifications
	"POST /notifications/delete/{line_number}":       func(r *http.Request) string { return "Deleted a notification" },
	"POST /notifications/mark_as_read/{line_number}": func(r *http.Request) string { return "Marked a notification as read" },
	"POST /notifications/pause":                      func(r *http.Request) string { return "Paused notifications" },
	"POST /notifications/resume":                     func(r *http.Request) string { return "Resumed notifications" },
	"POST /notifications/snooze/{line_number}":       func(r *http.Request) string { return "Snoozed a notification" },
	"POST /notifications/unsnooze/{line_number}":     func(r *http.Request) string { return "Unsnoozed a notification" },
	"POST /settings/notifications":                   func(r *http.Request) string { return "Changed notification settings" },

	// settings
	"POST /settings/caddy":              func(r *http.Request) string { return "Changed Caddy settings" },
	"POST /settings/php":                func(r *http.Request) string { return "Changed PHP settings" },
	"POST /php/{value}":                 func(r *http.Request) string { return actJoin("Changed PHP", actPath(r, "value")) },
	"POST /settings/custom-code":        func(r *http.Request) string { return "Edited custom code" },
	"POST /settings/general":            func(r *http.Request) string { return "Changed general settings" },
	"POST /settings/locales":            func(r *http.Request) string { return "Changed locales" },
	"POST /settings/modules":            func(r *http.Request) string { return "Changed enabled modules" },
	"POST /settings/updates/update_now": func(r *http.Request) string { return "Started an OpenPanel update" },
	"POST /settings/updates":            func(r *http.Request) string { return "Changed update settings" },
	"POST /settings/open-panel":         func(r *http.Request) string { return "Changed OpenPanel settings" },
	"POST /settings/defaults":           func(r *http.Request) string { return "Changed default settings for new users" },
	"POST /settings/defaults/files":     func(r *http.Request) string { return "Added a default file for new users" },
	"PUT /settings/defaults/files":      func(r *http.Request) string { return "Edited a default file for new users" },
	"DELETE /settings/defaults/files":   func(r *http.Request) string { return "Deleted a default file for new users" },
	"POST /settings/defaults/files/{username}": func(r *http.Request) string {
		return actJoin("Copied default files to user", quoted(actPath(r, "username")))
	},
	"POST /settings/api":     func(r *http.Request) string { return "Changed API access" },
	"POST /settings/api/":    func(r *http.Request) string { return "Changed API access" },
	"POST /license/key":      func(r *http.Request) string { return "Changed license key" },
	"POST /license/verify":   func(r *http.Request) string { return "Verified license" },
	"DELETE /license/delete": func(r *http.Request) string { return "Removed license key" },
}

func withPlan(plan string) string {
	if plan == "" {
		return ""
	}
	return "on plan " + quoted(plan)
}

// describeActivity turns the matched route into a readable line, falling back to the method and path
func describeActivity(r *http.Request) string {
	if fn, ok := activityDescriptions[r.Pattern]; ok {
		if s := fn(r); s != "" {
			return s
		}
	}
	return r.Method + " " + r.URL.Path
}
