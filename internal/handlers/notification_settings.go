package handlers

import (
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/config"
	"openadmin/internal/webtemplates"
)

// NotificationSettings bundles the /settings/notifications handlers.
type NotificationSettings struct {
	Sessions *auth.Manager
}

var (
	NotificationsConfigPath = "/etc/openpanel/openadmin/config/notifications.ini"
	SSHWhitelistPath        = "/etc/openpanel/openadmin/ssh_whitelist.conf"
)

var (
	notifNumericKeys = []string{"load", "cpu", "ram", "du", "swap"}
	httpNumericKeys  = []string{"max_total_conn", "max_conn_per_ip"}
	defaultToggles   = []string{"update", "attack", "limit", "login", "ssh", "reboot", "dns"}
	actionToggles    = []string{
		"admin_status", "admin_api", "admin_create", "reseller_create",
		"admin_password", "admin_rename", "admin_suspend", "admin_unsuspend",
		"waf_domain", "waf_status",
		"user_status", "user_create", "user_delete", "user_email",
		"user_ip", "user_password", "user_rename",
		"ftp_create", "ftp_delete", "ftp_password",
		"domains_add", "domains_delete", "domains_status", "domains_ssl", "domains_hsts",
	}
)

type notificationSettingsPageData struct {
	webtemplates.Chrome
	MailServer        string
	MailPort          string
	MailUseSSL        string
	MailUseTLS        string
	MailDebug         string
	MailUsername      string
	MailPassword      string
	MailDefaultSender string
	EmailAddress      string
	SSHWhitelist      string
	ConfigData        config.Data
	Services          []notifServiceView
	Thresholds        []notifThresholdView
	ServerActions     []notifToggleView
	UserActions       []notifToggleView
	AttackEnabled     bool
	MaxTotalConn      string
	MaxConnPerIP      string
	CSRFToken         string
	Flashes           []auth.Flash
}

type notifServiceView struct {
	Key, Name, Desc string
	Enabled         bool
}

type notifThresholdView struct {
	Key, Label, Value string
	ShowPercent       bool
}

type notifToggleView struct {
	Key, Label, Tooltip string
	Checked             bool
	IsAttack            bool
}

var notifServiceOrder = []struct{ Key, Name, Desc string }{
	{"panel", "OpenPanel", "Receive a notification when OpenPanel UI fails."},
	{"admin", "OpenAdmin", "Receive a notification when OpenAdmin UI fails."},
	{"caddy", "Caddy", "Receive a notification when webserver is not responding."},
	{"mysql", "MySQL", "Receive a notification when database is unreachable."},
	{"docker", "Docker", "Receive a notification when Docker service is down."},
	{"named", "BIND9", "Receive a notification when DNS service is down or not responding to requests."},
	{"csf", "Sentinel Firewall", "Receive a notification when CSF is disabled."},
}

var notifThresholdOrder = []struct{ Key, Label string }{
	{"load", "Load Avg."},
	{"cpu", "CPU"},
	{"ram", "Memory"},
	{"du", "Disk Usage"},
	{"swap", "SWAP"},
}

var notifServerActionOrder = []struct{ Key, Label, Tooltip string }{
	{"reboot", "Server reboot", "Triggered when the server is restarted."},
	{"attack", "Unusual traffic or SYN flood", "Fires when suspicious traffic or DDoS attacks are detected."},
	{"limit", "Out of Memory (OOM) errors", "Check journal logs for system services and user processes killed by OOM in the last 24 hours."},
	{"dns", "DNS issue detected", "Triggered when the panel domain or nameservers are misconfigured or not resolving to this server. Disable if using external nameservers or Cloudflare proxy."},
	{"login", "OpenAdmin login from new IP", "Triggered when the OpenAdmin panel is accessed from an unrecognized IP address."},
	{"ssh", "SSH login from new IP", `Triggered when root SSH access is detected from an unknown IP address. IP can be whitelisted in "SSH Allowlist" below.`},
	{"update", "New update available", "Triggered when a new version of OpenPanel is available for update."},
}

var notifUserActionOrder = []struct{ Key, Label, Tooltip string }{
	{"admin_status", "OpenAdmin enabled/disabled", `Triggered when OpenAdmin is toggled on or off from "OpenAdmin > Settings > Disable OpenAdmin" page.`},
	{"admin_api", "API access enabled/disabled", `Fires when API status changes on "OpenAdmin > Settings > API Access" page.`},
	{"admin_create", "Admin account created", "Triggered when a new OpenAdmin account is created."},
	{"reseller_create", "Reseller account created", "Triggered when a new reseller account is created."},
	{"admin_password", "Admin password changed", "Fires when password is changed for an Administrator or Reseller accounts."},
	{"admin_rename", "Admin/Reseller renamed", "Fires when an Administrator or Reseller account is renamed."},
	{"admin_suspend", "Admin/Reseller suspended", "Fires when an Administrator or Reseller account is suspended."},
	{"admin_unsuspend", "Admin/Reseller unsuspended", "Fires when an Administrator or Reseller account is unsuspended."},
	{"waf_domain", "WAF enabled/disabled for a domain", "Triggered when WAF is toggled for a domain."},
	{"waf_status", "WAF enabled/disabled on the server", "Fires when WAF status changes on the server."},
	{"user_create", "User added", "Fires when a new OpenPanel user is added or imported from cPanel backup."},
	{"user_delete", "User deleted", "Fires when a OpenPanel user is deleted."},
	{"user_status", "User suspended/unsuspended", "Fires when a user is suspended or unsuspended."},
	{"user_email", "User email changed", "Triggered when email is updated for an OpenPanel user."},
	{"user_ip", "User IP changed", "Fires when IP is changed for OpenPanel user."},
	{"user_password", "User password changed", "Fires when password is changed for an OpenPanel user."},
	{"user_rename", "User renamed", "Fires when the username is changed for an OpenPanel account."},
	{"ftp_create", "FTP account created", "Fires when a new FTP account is added."},
	{"ftp_delete", "FTP account deleted", "Fires when an FTP account is deleted."},
	{"ftp_password", "FTP account password change", "Triggered when an FTP password changes."},
	{"domains_add", "Domain added", "Fires when a new domain is added."},
	{"domains_delete", "Domain deleted", "Fires when a domain is deleted."},
	{"domains_status", "Domain suspended/unsuspended", "Triggered when a domain status changes."},
	{"domains_ssl", "SSL type changed", "Fires when SSL type is changed to AutoSSL or Custom SSL is configured."},
	{"domains_hsts", "HSTS enabled/disabled", "Triggered when HSTS is toggled for a domain name."},
}

// ServeSettings handles GET /settings/notifications.
func (n *NotificationSettings) ServeSettings(w http.ResponseWriter, r *http.Request) {
	mainConfig := config.Load(config.OpenpanelConfigPath)
	notifConfig := config.Load(NotificationsConfigPath)

	enabledServices := map[string]bool{}
	for _, s := range strings.Split(notifConfig.Get("DEFAULT", "services", ""), ",") {
		enabledServices[strings.TrimSpace(s)] = true
	}
	services := make([]notifServiceView, 0, len(notifServiceOrder))
	for _, s := range notifServiceOrder {
		services = append(services, notifServiceView{Key: s.Key, Name: s.Name, Desc: s.Desc, Enabled: enabledServices[s.Key]})
	}

	thresholds := make([]notifThresholdView, 0, len(notifThresholdOrder))
	for _, t := range notifThresholdOrder {
		thresholds = append(thresholds, notifThresholdView{
			Key: t.Key, Label: t.Label, Value: notifConfig.Get("DEFAULT", t.Key, ""),
			ShowPercent: t.Key != "load",
		})
	}

	serverActions := make([]notifToggleView, 0, len(notifServerActionOrder))
	for _, a := range notifServerActionOrder {
		serverActions = append(serverActions, notifToggleView{
			Key: a.Key, Label: a.Label, Tooltip: a.Tooltip,
			Checked: notifConfig.Get("DEFAULT", a.Key, "") == "yes", IsAttack: a.Key == "attack",
		})
	}

	userActions := make([]notifToggleView, 0, len(notifUserActionOrder))
	for _, a := range notifUserActionOrder {
		userActions = append(userActions, notifToggleView{
			Key: a.Key, Label: a.Label, Tooltip: a.Tooltip,
			Checked: notifConfig.Get("ACTIONS", a.Key, "") == "yes",
		})
	}

	maxTotalConn := notifConfig.Get("HTTP", "max_total_conn", "")
	if maxTotalConn == "" {
		maxTotalConn = "5000"
	}
	maxConnPerIP := notifConfig.Get("HTTP", "max_conn_per_ip", "")
	if maxConnPerIP == "" {
		maxConnPerIP = "200"
	}

	data := notificationSettingsPageData{
		Chrome:            buildChrome(r, "Notification settings"),
		MailServer:        mainConfig.Get("SMTP", "mail_server", ""),
		MailPort:          mainConfig.Get("SMTP", "mail_port", "465"),
		MailUseSSL:        mainConfig.Get("SMTP", "mail_use_ssl", ""),
		MailUseTLS:        mainConfig.Get("SMTP", "mail_use_tls", ""),
		MailDebug:         mainConfig.Get("SMTP", "mail_debug", ""),
		MailUsername:      mainConfig.Get("SMTP", "mail_username", ""),
		MailPassword:      mainConfig.Get("SMTP", "mail_password", ""),
		MailDefaultSender: mainConfig.Get("SMTP", "mail_default_sender", ""),
		EmailAddress:      mainConfig.Get("DEFAULT", "email", ""),
		SSHWhitelist:      readSSHWhitelist(),
		ConfigData:        notifConfig,
		Services:          services,
		Thresholds:        thresholds,
		ServerActions:     serverActions,
		UserActions:       userActions,
		AttackEnabled:     notifConfig.Get("DEFAULT", "attack", "") == "yes",
		MaxTotalConn:      maxTotalConn,
		MaxConnPerIP:      maxConnPerIP,
		CSRFToken:         csrf.Token(r),
		Flashes:           auth.PopFlashes(w, r, n.Sessions),
	}

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, map[string]interface{}{
			"email_address":       data.EmailAddress,
			"mail_server":         data.MailServer,
			"mail_port":           data.MailPort,
			"mail_use_tls":        data.MailUseTLS,
			"mail_debug":          data.MailDebug,
			"mail_use_ssl":        data.MailUseSSL,
			"mail_username":       data.MailUsername,
			"mail_password":       data.MailPassword,
			"mail_default_sender": data.MailDefaultSender,
			"ssh_whitelist":       data.SSHWhitelist,
			"settings":            data.ConfigData,
		})
		return
	}

	webtemplates.Render(w, "notification_settings.html", data)
}

func readSSHWhitelist() string {
	raw, err := os.ReadFile(SSHWhitelistPath)
	if err != nil {
		return ""
	}
	var lines []string
	for _, l := range strings.Split(string(raw), "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

// HandleUpdate handles POST /settings/notifications.
func (n *NotificationSettings) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	mainConfig := config.Load(config.OpenpanelConfigPath)
	notifConfig := config.Load(NotificationsConfigPath)

	for _, key := range notifNumericKeys {
		updateNumericIfChanged(r, key, notifConfig.Get("DEFAULT", key, ""))
	}
	for _, key := range httpNumericKeys {
		updateNumericIfChanged(r, key, notifConfig.Get("HTTP", key, ""))
	}

	if newWebhook := formValueOrNil(r, "webhook_url"); newWebhook != nil && *newWebhook != notifConfig.Get("DEFAULT", "webhook_url", "") {
		runOpenCLINotificationUpdate("webhook_url", *newWebhook)
	}

	if newEmail := formValueOrNil(r, "email"); newEmail != nil && *newEmail != mainConfig.Get("DEFAULT", "email", "") {
		runOpenCLI("", "opencli", "config", "update", "email", *newEmail)
	}

	updateSMTPFieldIfChanged(r, mainConfig, "mail_server", nil)
	updateSMTPFieldIfChanged(r, mainConfig, "mail_port", isValidPort)
	updateSMTPFieldIfChanged(r, mainConfig, "mail_use_tls", isValidBool)
	updateSMTPFieldIfChanged(r, mainConfig, "mail_use_ssl", isValidBool)
	updateSMTPFieldIfChanged(r, mainConfig, "mail_debug", isValidBool)
	updateSMTPFieldIfChanged(r, mainConfig, "mail_username", isValidEmail)
	updateSMTPFieldIfChanged(r, mainConfig, "mail_password", nil)
	updateSMTPFieldIfChanged(r, mainConfig, "mail_default_sender", isValidEmail)

	if newServices := formValueOrNil(r, "services"); newServices != nil && *newServices != notifConfig.Get("DEFAULT", "services", "") {
		runOpenCLINotificationUpdate("services", *newServices)
	}

	for _, toggle := range defaultToggles {
		newVal := onOffToYesNo(r.FormValue(toggle))
		if newVal != notifConfig.Get("DEFAULT", toggle, "") {
			runOpenCLINotificationUpdate(toggle, newVal)
		}
	}
	for _, toggle := range actionToggles {
		newVal := onOffToYesNo(r.FormValue(toggle))
		if newVal != notifConfig.Get("ACTIONS", toggle, "") {
			runOpenCLINotificationUpdate(toggle, newVal)
		}
	}

	if err := n.updateSSHWhitelist(w, r); err != nil {
		auth.AddFlash(w, r, n.Sessions, err.Error(), "error")
	}

	http.Redirect(w, r, "/settings/notifications", http.StatusSeeOther)
}

func (n *NotificationSettings) updateSSHWhitelist(w http.ResponseWriter, r *http.Request) error {
	raw := r.FormValue("ssh_whitelist")
	var validIPs []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(withCIDRSuffix(line)); err != nil {
			return &invalidCIDRError{ip: line}
		}
		validIPs = append(validIPs, line)
	}

	current := strings.Split(readSSHWhitelist(), "\n")
	if len(current) == 1 && current[0] == "" {
		current = nil
	}
	if slicesEqual(validIPs, current) {
		return nil
	}

	var b strings.Builder
	b.WriteString("# SSH Whitelist\n")
	for _, ip := range validIPs {
		b.WriteString(ip + "\n")
	}
	return os.WriteFile(SSHWhitelistPath, []byte(b.String()), 0644)
}

type invalidCIDRError struct{ ip string }

func (e *invalidCIDRError) Error() string { return "Invalid IP/CIDR: " + e.ip }

// withCIDRSuffix appends "/32" (or "/128" for IPv6) to a bare IP so
// net.ParseCIDR can validate both plain IPs and CIDR ranges uniformly.
func withCIDRSuffix(ip string) string {
	if strings.Contains(ip, "/") {
		return ip
	}
	if strings.Contains(ip, ":") {
		return ip + "/128"
	}
	return ip + "/32"
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// formValueOrNil returns nil when the field wasn't submitted at all, vs. a
// pointer to "" when it was submitted empty (unlike r.FormValue, which
// can't distinguish the two).
func formValueOrNil(r *http.Request, key string) *string {
	if r.PostForm == nil {
		r.ParseForm()
	}
	vals, ok := r.PostForm[key]
	if !ok || len(vals) == 0 {
		return nil
	}
	return &vals[0]
}

func onOffToYesNo(v string) string {
	if v == "on" {
		return "yes"
	}
	return "no"
}

func updateNumericIfChanged(r *http.Request, key, currentVal string) {
	newVal := formValueOrNil(r, key)
	if newVal == nil {
		return
	}
	n, err := strconv.Atoi(*newVal)
	if err != nil {
		return
	}
	if strconv.Itoa(n) != strings.TrimSpace(currentVal) {
		runOpenCLI("", "opencli", "admin", "notifications", "update", key, strconv.Itoa(n))
	}
}

func updateSMTPFieldIfChanged(r *http.Request, mainConfig config.Data, field string, validate func(string) bool) {
	newVal := formValueOrNil(r, field)
	if newVal == nil || *newVal == mainConfig.Get("SMTP", field, "") {
		return
	}
	if validate != nil && !validate(*newVal) {
		return
	}
	runOpenCLI("", "opencli", "config", "update", field, *newVal)
}

func runOpenCLINotificationUpdate(key, value string) {
	runOpenCLI("", "opencli", "admin", "notifications", "update", key, value)
}

var emailRe = regexp.MustCompile(`^[^@]+@[^@]+\.[^@]+$`)

func isValidPort(v string) bool {
	n, err := strconv.Atoi(v)
	return err == nil && n >= 1 && n <= 65535
}

func isValidBool(v string) bool { return v == "True" || v == "False" }

func isValidEmail(v string) bool { return emailRe.MatchString(v) }
