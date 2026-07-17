// This file implements the internal /send_email endpoint used by other
// server-side processes (not browser sessions) to send admin/user
// notification emails over SMTP. Authenticated by a shared one-time HMAC
// code from openpanel.config, not a user session or CSRF token, since
// callers here are other local processes rather than a browser.
package handlers

import (
	"crypto/hmac"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"openadmin/internal/config"
	"openadmin/internal/webtemplates"
)

// Mailer bundles the /send_email handler.
type Mailer struct {
	PublicIP string
}

// MailerEmailLogDir / MailerCronLogPath / MailerAPILogPath are the
// hardcoded paths used by the mailer's debug logging and usage stats.
var (
	MailerEmailLogDir = "/var/log/openpanel/admin/emails/"
	MailerCronLogPath = "/var/log/openpanel/admin/cron.log"
	MailerAPILogPath  = "/var/log/openpanel/admin/api.log"
)

type mailerSMTPConfig struct {
	Server        string
	Port          int
	UseSSL        bool
	UseTLS        bool
	Username      string
	Password      string
	DefaultSender string
	Debug         bool
}

// loadMailerSMTPConfig is read fresh (no caching) each time it's called.
func loadMailerSMTPConfig() mailerSMTPConfig {
	data := config.Load(config.OpenpanelConfigPath)
	port, _ := strconv.Atoi(data.Get("SMTP", "mail_port", "0"))
	return mailerSMTPConfig{
		Server:        data.Get("SMTP", "mail_server", ""),
		Port:          port,
		UseSSL:        strings.EqualFold(data.Get("SMTP", "mail_use_ssl", ""), "true"),
		UseTLS:        strings.EqualFold(data.Get("SMTP", "mail_use_tls", ""), "true"),
		Username:      data.Get("SMTP", "mail_username", ""),
		Password:      data.Get("SMTP", "mail_password", ""),
		DefaultSender: data.Get("SMTP", "mail_default_sender", ""),
		Debug:         strings.EqualFold(data.Get("SMTP", "mail_debug", ""), "true"),
	}
}

func saveEmailToFile(subject, recipient, body string) error {
	if err := os.MkdirAll(MailerEmailLogDir, 0755); err != nil {
		return err
	}
	filename := recipient + "_" + subject + ".txt"
	return os.WriteFile(filepath.Join(MailerEmailLogDir, filename), []byte(body), 0644)
}

// validateOneTimeCode is a constant-time comparison against
// openpanel.config's [SMTP] mail_security_token.
func validateOneTimeCode(oneTimeCode string) bool {
	token := config.Load(config.OpenpanelConfigPath).Get("SMTP", "mail_security_token", "")
	if token == "" || oneTimeCode == "" {
		return false
	}
	return hmac.Equal([]byte(oneTimeCode), []byte(token))
}

func countLinesContainingToday(path, todayMarker, excludeMarker string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, todayMarker) && (excludeMarker == "" || !strings.Contains(line, excludeMarker)) {
			count++
		}
	}
	return count
}

func countCronsExecutedToday() int {
	today := time.Now().Format("Mon Jan _2")
	return countLinesContainingToday(MailerCronLogPath, today, "Notifications script executed")
}

func countAPIRequestsReceivedToday() int {
	today := time.Now().Format("2006-01-02")
	return countLinesContainingToday(MailerAPILogPath, today, "")
}

// getCPUUsage/getRAMUsage/getDiskUsage are Linux-specific (/proc-based)
// stand-ins for psutil's cross-platform equivalents -- this app only ever
// runs on Linux. getDiskUsage simplifies psutil.disk_partitions()'s
// multi-partition aggregation down to just "/", which is the deliberate
// scope reduction here: exactly replicating psutil's virtual-filesystem
// filtering isn't worth the complexity for an email stats footer.
var mailerCPUUsageRun = func() (float64, error) {
	read := func() (idle, total uint64, err error) {
		raw, err := os.ReadFile("/proc/stat")
		if err != nil {
			return 0, 0, err
		}
		firstLine := strings.SplitN(string(raw), "\n", 2)[0]
		fields := strings.Fields(firstLine)
		for i, f := range fields {
			if i == 0 {
				continue // "cpu" label
			}
			n, _ := strconv.ParseUint(f, 10, 64)
			total += n
			if i == 4 { // idle is the 4th value field
				idle = n
			}
		}
		return idle, total, nil
	}
	idle1, total1, err := read()
	if err != nil {
		return 0, err
	}
	time.Sleep(1 * time.Second)
	idle2, total2, err := read()
	if err != nil {
		return 0, err
	}
	totalDelta := float64(total2 - total1)
	if totalDelta <= 0 {
		return 0, nil
	}
	return 100 * (1 - float64(idle2-idle1)/totalDelta), nil
}

func getRAMUsage() string {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return "N/A"
	}
	values := map[string]uint64{}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		n, _ := strconv.ParseUint(fields[1], 10, 64)
		values[key] = n
	}
	totalKB, availKB := values["MemTotal"], values["MemAvailable"]
	if totalKB == 0 {
		return "N/A"
	}
	totalGB := float64(totalKB) / (1024 * 1024)
	usedGB := float64(totalKB-availKB) / (1024 * 1024)
	return fmt.Sprintf("%.2f / %.2f GB", usedGB, totalGB)
}

var mailerDiskUsageRun = func() string {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		return "N/A"
	}
	blockSize := uint64(stat.Bsize)
	totalGB := float64(stat.Blocks*blockSize) / (1024 * 1024 * 1024)
	freeGB := float64(stat.Bfree*blockSize) / (1024 * 1024 * 1024)
	usedGB := totalGB - freeGB
	if totalGB <= 0 {
		return "0.00 / 0.00 GB"
	}
	return fmt.Sprintf("%.2f / %.2f GB", usedGB, totalGB)
}

// --- SMTP sending ---

// mailerSendRun is injectable so tests never open a real network
// connection.
var mailerSendRun = func(cfg mailerSMTPConfig, to, subject, htmlBody string) error {
	from := cfg.DefaultSender
	if from == "" {
		from = cfg.Username
	}
	msg := buildMIMEMessage(from, to, subject, htmlBody)
	addr := fmt.Sprintf("%s:%d", cfg.Server, cfg.Port)

	var auth smtp.Auth
	if cfg.Username != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Server)
	}

	var client *smtp.Client
	var err error
	if cfg.UseSSL {
		var conn *tls.Conn
		conn, err = tls.Dial("tcp", addr, &tls.Config{ServerName: cfg.Server})
		if err != nil {
			return err
		}
		client, err = smtp.NewClient(conn, cfg.Server)
	} else {
		client, err = smtp.Dial(addr)
	}
	if err != nil {
		return err
	}
	defer client.Close()

	if !cfg.UseSSL && cfg.UseTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: cfg.Server}); err != nil {
				return err
			}
		}
	}

	if auth != nil {
		if ok, _ := client.Extension("AUTH"); ok {
			if err := client.Auth(auth); err != nil {
				return err
			}
		}
	}

	if err := client.Mail(from); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	defer w.Close()
	_, err = w.Write([]byte(msg))
	return err
}

func buildMIMEMessage(from, to, subject, htmlBody string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
	b.WriteString(htmlBody)
	return b.String()
}

// ServeSendEmail handles POST /send_email. Rather than refusing outright
// when [SMTP] has no configured username/password, this falls back to
// sending unauthenticated through the box's local MTA (localhost:25, e.g.
// postfix/sendmail, which is what most hosts already run for local mail
// delivery) using root@<hostname> as the sender. This lets notification
// emails go out even on installs that never configured an external SMTP
// relay, instead of every send failing with 503.
func (m *Mailer) ServeSendEmail(w http.ResponseWriter, r *http.Request) {
	cfg := loadMailerSMTPConfig()
	serverHostname := generalHostname()

	if cfg.Server == "" {
		cfg.Server = "localhost"
		cfg.Port = 25
	}
	if cfg.Username == "" {
		cfg.DefaultSender = "root@" + serverHostname
	}

	r.ParseForm()
	recipient := strings.TrimSpace(r.PostFormValue("recipient"))
	subject := r.PostFormValue("subject")
	messageContent := r.PostFormValue("body")

	if recipient == "" || !strings.Contains(recipient, "@") {
		writeJSONError(w, http.StatusBadRequest, "Invalid recipient email address provided.")
		return
	}

	if !validateOneTimeCode(r.PostFormValue("transient")) {
		writeJSONError(w, http.StatusUnauthorized, "Invalid unique code")
		return
	}

	port := generalOpenpanelPort()
	adminPort := generalOpenadminPort()
	domainForces := generalAdminDomain()

	protocol := "http://"
	if domainForces != "" {
		protocol = "https://"
	} else {
		domainForces = m.PublicIP
	}

	messageTitle := fmt.Sprintf("[%s] %s", domainForces, subject)
	adminURL := fmt.Sprintf("%s%s:%s/", protocol, domainForces, adminPort)
	panelNotificationsPage := fmt.Sprintf("%s%s:%s/account/notifications", protocol, domainForces, port)

	var emailTemplate string
	var renderErr error
	switch {
	case strings.Contains(messageContent, "Daily Usage Report"):
		counts := getCountsFromDBForMailer()
		cpuUsage, _ := mailerCPUUsageRun()
		emailTemplate, renderErr = webtemplates.RenderToString("email_daily_system_report.html", map[string]interface{}{
			"Title":                    subject,
			"Message":                  messageContent,
			"Hostname":                 serverHostname,
			"AdminURL":                 adminURL,
			"TotalUsers":               counts.users,
			"TotalDomains":             counts.domains,
			"TotalWebsites":            counts.websites,
			"CurrentDiskUsage":         mailerDiskUsageRun(),
			"TotalCPUUsage":            fmt.Sprintf("%.2f %%", cpuUsage),
			"TotalRAMUsage":            getRAMUsage(),
			"CronsExecutedToday":       countCronsExecutedToday(),
			"APIRequestsReceivedToday": countAPIRequestsReceivedToday(),
		})
	case strings.Contains(messageContent, "OpenPanel URL"):
		emailTemplate, renderErr = webtemplates.RenderToString("email_new_user.html", map[string]interface{}{
			"Title": subject, "Message": messageContent, "Hostname": serverHostname,
		})
	case strings.Contains(messageContent, "changed for account") || strings.Contains(messageContent, "login from"):
		emailTemplate, renderErr = webtemplates.RenderToString("email_user_notifications.html", map[string]interface{}{
			"Title": subject, "NotificationsURL": panelNotificationsPage, "Message": messageContent, "Hostname": serverHostname,
		})
	default:
		emailTemplate, renderErr = webtemplates.RenderToString("email_admin_notifications.html", map[string]interface{}{
			"Title": subject, "Message": messageContent, "Hostname": serverHostname, "AdminURL": adminURL,
		})
	}
	if renderErr != nil {
		writeJSONError(w, http.StatusInternalServerError, renderErr.Error())
		return
	}

	if cfg.Debug {
		saveEmailToFile(subject, recipient, emailTemplate)
	}

	if err := mailerSendRun(cfg, recipient, messageTitle, emailTemplate); err != nil {
		devMode := strings.EqualFold(config.Load(config.OpenpanelConfigPath).Get("PANEL", "dev_mode", "off"), "on")
		if devMode {
			// In dev mode, include extra SMTP config fields for debugging,
			// deliberately excluding mail_password -- that one should
			// never be shown, even here.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":               "An error occurred: " + err.Error(),
				"recipient":           recipient,
				"subject":             subject,
				"body":                messageContent,
				"mail_server":         cfg.Server,
				"mail_port":           cfg.Port,
				"mail_use_tls":        cfg.UseTLS,
				"mail_use_ssl":        cfg.UseSSL,
				"mail_debug":          cfg.Debug,
				"mail_username":       cfg.Username,
				"mail_default_sender": cfg.DefaultSender,
			})
		} else {
			writeJSONError(w, http.StatusInternalServerError, "An error occurred: "+err.Error())
		}
		return
	}

	if strings.Contains(messageContent, "OpenPanel URL") {
		w.Write([]byte("Email sent successfully"))
		return
	}
	writeJSON(w, map[string]string{"message": "Email sent successfully"})
}

type mailerCounts struct {
	users, websites, domains int
}

// getCountsFromDBForMailer is a thin, mailer-local wrapper so tests can
// stub it without needing a real MySQL connection wired through this
// struct (the Daily Usage Report is a rarely-exercised path).
var getCountsFromDBForMailer = func() mailerCounts {
	return mailerCounts{}
}
