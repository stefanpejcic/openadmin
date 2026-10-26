package handlers

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"openadmin/internal/config"
)

func withScratchMailerConfig(t *testing.T, extraLines ...string) {
	t.Helper()
	dir := t.TempDir()
	origPath := config.OpenpanelConfigPath
	path := filepath.Join(dir, "openpanel.config")
	content := "[SMTP]\n" + strings.Join(extraLines, "\n") + "\n"
	os.WriteFile(path, []byte(content), 0644)
	config.OpenpanelConfigPath = path
	t.Cleanup(func() { config.OpenpanelConfigPath = origPath })
}

func withScratchMailerLogPaths(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	origLogDir, origCron, origAPI := MailerEmailLogDir, MailerCronLogPath, MailerAPILogPath
	MailerEmailLogDir = filepath.Join(dir, "emails") + "/"
	MailerCronLogPath = filepath.Join(dir, "cron.log")
	MailerAPILogPath = filepath.Join(dir, "api.log")
	t.Cleanup(func() {
		MailerEmailLogDir, MailerCronLogPath, MailerAPILogPath = origLogDir, origCron, origAPI
	})
}

func newMailerTestServer(t *testing.T, m *Mailer) (*httptest.Server, *http.Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /send_email", m.ServeSendEmail)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

func TestValidateOneTimeCode(t *testing.T) {
	withScratchMailerConfig(t, "mail_security_token=secret123")
	if !validateOneTimeCode("secret123") {
		t.Fatal("expected matching code to validate")
	}
	if validateOneTimeCode("wrong") {
		t.Fatal("expected mismatched code to fail")
	}
	if validateOneTimeCode("") {
		t.Fatal("expected empty code to fail")
	}
}

func TestValidateOneTimeCodeNoTokenConfigured(t *testing.T) {
	withScratchMailerConfig(t)
	if validateOneTimeCode("anything") {
		t.Fatal("expected no configured token to always fail")
	}
}

func TestCountCronsAndAPIRequestsToday(t *testing.T) {
	withScratchMailerLogPaths(t)
	today := time.Now().Format("Mon Jan _2")
	todayISO := time.Now().Format("2006-01-02")
	os.WriteFile(MailerCronLogPath, []byte(today+" job ran\n"+today+" Notifications script executed\nunrelated\n"), 0644)
	os.WriteFile(MailerAPILogPath, []byte(todayISO+" GET /foo\nunrelated\n"+todayISO+" POST /bar\n"), 0644)

	if got := countCronsExecutedToday(); got != 1 {
		t.Fatalf("expected 1 (excluding the notifications-script line), got %d", got)
	}
	if got := countAPIRequestsReceivedToday(); got != 2 {
		t.Fatalf("expected 2, got %d", got)
	}
}

func TestSaveEmailToFile(t *testing.T) {
	withScratchMailerLogPaths(t)
	if err := saveEmailToFile("Test Subject", "user@example.com", "body content"); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(filepath.Join(MailerEmailLogDir, "user@example.com_Test Subject.txt"))
	if err != nil {
		t.Fatalf("expected email log file written, err=%v", err)
	}
	if string(saved) != "body content" {
		t.Fatalf("expected body content saved, got %q", saved)
	}
}

func TestServeSendEmailFallsBackToLocalMTAWhenNotConfigured(t *testing.T) {
	withScratchMailerConfig(t, "mail_security_token=right") // no mail_server/mail_username

	var gotCfg mailerSMTPConfig
	origSend := mailerSendRun
	mailerSendRun = func(cfg mailerSMTPConfig, to, subject, htmlBody string) error {
		gotCfg = cfg
		return nil
	}
	t.Cleanup(func() { mailerSendRun = origSend })

	m := &Mailer{}
	srv, client := newMailerTestServer(t, m)

	resp, err := client.PostForm(srv.URL+"/send_email", url.Values{
		"recipient": {"a@b.com"}, "subject": {"Alert"}, "body": {"something"}, "transient": {"right"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (sending via the local-MTA fallback, not a 503), got %d: %s", resp.StatusCode, body)
	}
	if gotCfg.Server != "localhost" || gotCfg.Port != 25 {
		t.Fatalf("expected the localhost:25 fallback, got server=%q port=%d", gotCfg.Server, gotCfg.Port)
	}
	hostname := generalHostname()
	if gotCfg.DefaultSender != "root@"+hostname {
		t.Fatalf("expected root@%s as the fallback sender, got %q", hostname, gotCfg.DefaultSender)
	}
}

func TestServeSendEmailUsesRootSenderWhenOnlyUsernameMissing(t *testing.T) {
	// mail_server is configured (e.g. an open relay/local Postfix with no
	// auth) but no mail_username -- should send unauthenticated with
	// root@<hostname> as sender rather than refusing.
	withScratchMailerConfig(t, "mail_server=relay.example.com", "mail_security_token=right")

	var gotCfg mailerSMTPConfig
	origSend := mailerSendRun
	mailerSendRun = func(cfg mailerSMTPConfig, to, subject, htmlBody string) error {
		gotCfg = cfg
		return nil
	}
	t.Cleanup(func() { mailerSendRun = origSend })

	m := &Mailer{}
	srv, client := newMailerTestServer(t, m)

	resp, err := client.PostForm(srv.URL+"/send_email", url.Values{
		"recipient": {"a@b.com"}, "subject": {"Alert"}, "body": {"something"}, "transient": {"right"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if gotCfg.Server != "relay.example.com" {
		t.Fatalf("expected the configured mail_server preserved, got %q", gotCfg.Server)
	}
	hostname := generalHostname()
	if gotCfg.DefaultSender != "root@"+hostname {
		t.Fatalf("expected root@%s as the fallback sender, got %q", hostname, gotCfg.DefaultSender)
	}
}

func TestServeSendEmailInvalidRecipient(t *testing.T) {
	withScratchMailerConfig(t, "mail_server=smtp.example.com", "mail_username=user")

	m := &Mailer{}
	srv, client := newMailerTestServer(t, m)

	resp, err := client.PostForm(srv.URL+"/send_email", url.Values{"recipient": {"not-an-email"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
}

func TestServeSendEmailInvalidCode(t *testing.T) {
	withScratchMailerConfig(t, "mail_server=smtp.example.com", "mail_username=user", "mail_security_token=right")

	m := &Mailer{}
	srv, client := newMailerTestServer(t, m)

	resp, err := client.PostForm(srv.URL+"/send_email", url.Values{
		"recipient": {"a@b.com"}, "transient": {"wrong"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", resp.StatusCode, body)
	}
}

func TestServeSendEmailSuccessNewUserPlainText(t *testing.T) {
	withScratchMailerConfig(t, "mail_server=smtp.example.com", "mail_username=user", "mail_security_token=right")

	var gotTo, gotSubject, gotBody string
	origSend := mailerSendRun
	mailerSendRun = func(cfg mailerSMTPConfig, to, subject, htmlBody string) error {
		gotTo, gotSubject, gotBody = to, subject, htmlBody
		return nil
	}
	t.Cleanup(func() { mailerSendRun = origSend })

	m := &Mailer{PublicIP: "198.51.100.5"}
	srv, client := newMailerTestServer(t, m)

	resp, err := client.PostForm(srv.URL+"/send_email", url.Values{
		"recipient": {"newuser@example.com"}, "subject": {"Welcome"},
		"body": {"OpenPanel URL: http://x|username: bob|password: secret"}, "transient": {"right"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if string(body) != "Email sent successfully" {
		t.Fatalf("expected plain-text success for new-user emails, got %q", body)
	}
	if gotTo != "newuser@example.com" {
		t.Fatalf("expected recipient passed through, got %q", gotTo)
	}
	if !strings.Contains(gotSubject, "Welcome") {
		t.Fatalf("expected subject embedded in message title, got %q", gotSubject)
	}
	if !strings.Contains(gotBody, "OpenPanel URL") {
		t.Fatalf("expected rendered template to include the message, got %s", truncate(gotBody))
	}
}

func TestServeSendEmailSuccessOtherReturnsJSON(t *testing.T) {
	withScratchMailerConfig(t, "mail_server=smtp.example.com", "mail_username=user", "mail_security_token=right")

	origSend := mailerSendRun
	mailerSendRun = func(cfg mailerSMTPConfig, to, subject, htmlBody string) error { return nil }
	t.Cleanup(func() { mailerSendRun = origSend })

	m := &Mailer{}
	srv, client := newMailerTestServer(t, m)

	resp, err := client.PostForm(srv.URL+"/send_email", url.Values{
		"recipient": {"admin@example.com"}, "subject": {"Alert"}, "body": {"disk space low"}, "transient": {"right"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"message":"Email sent successfully"`) {
		t.Fatalf("expected JSON success message, got %s", body)
	}
}

func TestServeSendEmailFailureProdModeGenericError(t *testing.T) {
	withScratchMailerConfig(t, "mail_server=smtp.example.com", "mail_username=user", "mail_security_token=right", "mail_password=secret")

	origSend := mailerSendRun
	mailerSendRun = func(cfg mailerSMTPConfig, to, subject, htmlBody string) error {
		return &ftpStubError{"connection refused"}
	}
	t.Cleanup(func() { mailerSendRun = origSend })

	m := &Mailer{}
	srv, client := newMailerTestServer(t, m)

	resp, err := client.PostForm(srv.URL+"/send_email", url.Values{
		"recipient": {"admin@example.com"}, "subject": {"Alert"}, "body": {"something"}, "transient": {"right"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
	if strings.Contains(string(body), "secret") {
		t.Fatalf("expected mail_password to never leak, got %s", body)
	}
	if !strings.Contains(string(body), "connection refused") {
		t.Fatalf("expected error detail, got %s", body)
	}
}

func TestServeSendEmailFailureDevModeIncludesDebugFields(t *testing.T) {
	withScratchMailerConfig(t, "mail_server=smtp.example.com", "mail_username=user",
		"mail_security_token=right", "mail_password=secret", "\n[PANEL]\ndev_mode=on")

	origSend := mailerSendRun
	mailerSendRun = func(cfg mailerSMTPConfig, to, subject, htmlBody string) error {
		return &ftpStubError{"connection refused"}
	}
	t.Cleanup(func() { mailerSendRun = origSend })

	m := &Mailer{}
	srv, client := newMailerTestServer(t, m)

	resp, err := client.PostForm(srv.URL+"/send_email", url.Values{
		"recipient": {"admin@example.com"}, "subject": {"Alert"}, "body": {"something"}, "transient": {"right"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"mail_server":"smtp.example.com"`) {
		t.Fatalf("expected dev-mode debug fields, got %s", body)
	}
	if strings.Contains(string(body), "\"mail_password\"") {
		t.Fatalf("expected mail_password to never be included, even in dev mode, got %s", body)
	}
}

func TestServeSendEmailRendersNotificationItems(t *testing.T) {
	withScratchMailerConfig(t, "mail_security_token=right")

	var gotBody string
	origSend := mailerSendRun
	mailerSendRun = func(cfg mailerSMTPConfig, to, subject, htmlBody string) error {
		gotBody = htmlBody
		return nil
	}
	t.Cleanup(func() { mailerSendRun = origSend })

	m := &Mailer{}
	srv, client := newMailerTestServer(t, m)
	items := `[{"severity":"critical","title":"OpenPanel container not running!","message":"Container openpanel was not running."},` +
		`{"severity":"resolved","title":"Resolved: High CPU Usage!","message":"Sentinel no longer detects this issue."}]`
	resp, err := client.PostForm(srv.URL+"/send_email", url.Values{
		"recipient": {"a@b.com"}, "subject": {"2 notifications from Sentinel"}, "body": {"text"}, "transient": {"right"}, "notifications": {items},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, want := range []string{"OpenPanel container not running!", "Critical", "#dc2626", "Resolved: High CPU Usage!", "#059669", "2 notifications from Sentinel"} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("expected email to contain %q, got %s", want, gotBody)
		}
	}
}

func TestServeSendEmailUserTypeUsesUserTemplate(t *testing.T) {
	withScratchMailerConfig(t, "mail_server=smtp.example.com", "mail_username=user", "mail_security_token=right")

	var gotBody string
	origSend := mailerSendRun
	mailerSendRun = func(cfg mailerSMTPConfig, to, subject, htmlBody string) error {
		gotBody = htmlBody
		return nil
	}
	t.Cleanup(func() { mailerSendRun = origSend })

	m := &Mailer{PublicIP: "198.51.100.5"}
	srv, client := newMailerTestServer(t, m)

	resp, err := client.PostForm(srv.URL+"/send_email", url.Values{
		"recipient": {"john@example.com"}, "subject": {"Account john is almost out of disk space"},
		"body": {"Account john is close to its hosting plan limit"}, "transient": {"right"}, "type": {"user"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(gotBody, "Edit notification preferences") || strings.Contains(gotBody, "Sentinel") {
		t.Fatalf("expected the user template for type=user, got %s", truncate(gotBody))
	}
}

func TestParseEmailUsageItems(t *testing.T) {
	items := parseEmailUsageItems(`[{"title":"Disk space","used":"8.70 GB","total":"10.00 GB","percent":87,"limit":85},{"title":"Inodes","percent":42,"limit":95},{"title":"info@example.com","percent":130,"limit":90}]`)
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[0].Label != "Over 85%" || items[0].Color != "#d97706" || items[0].Width != 87 {
		t.Errorf("over limit item wrong: %+v", items[0])
	}
	if items[1].Label != "OK" || items[1].Color != "#059669" {
		t.Errorf("ok item wrong: %+v", items[1])
	}
	if items[2].Label != "Full" || items[2].Width != 100 {
		t.Errorf("full item wrong: %+v", items[2])
	}
	if parseEmailUsageItems("not json") != nil {
		t.Error("expected nil for invalid JSON")
	}
}

func TestServeSendEmailUpgradeSection(t *testing.T) {
	withScratchMailerConfig(t, "mail_server=smtp.example.com", "mail_username=user", "mail_security_token=right")

	var gotBody string
	origSend := mailerSendRun
	mailerSendRun = func(cfg mailerSMTPConfig, to, subject, htmlBody string) error {
		gotBody = htmlBody
		return nil
	}
	t.Cleanup(func() { mailerSendRun = origSend })

	m := &Mailer{PublicIP: "198.51.100.5"}
	srv, client := newMailerTestServer(t, m)
	send := func(extra url.Values) {
		form := url.Values{"recipient": {"john@example.com"}, "subject": {"Account john is almost out of disk space"},
			"body": {"Account john is close to its hosting plan limit."}, "transient": {"right"}, "type": {"user"}}
		for k, v := range extra {
			form[k] = v
		}
		resp, err := client.PostForm(srv.URL+"/send_email", form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	send(url.Values{"upgrade_plan": {"Business"}, "upgrade_text": {"The Business plan gives you 50 GB of disk space instead of 10 GB."}, "tips": {"To free up space, delete old files."}})
	if !strings.Contains(gotBody, "Upgrade to Business") || !strings.Contains(gotBody, "/dashboard/upgrade") || !strings.Contains(gotBody, "50 GB of disk space") {
		t.Fatalf("expected the upgrade section, got %s", truncate(gotBody))
	}
	if strings.Index(gotBody, "To free up space") < strings.Index(gotBody, "Upgrade to Business") {
		t.Fatalf("expected tips below the upgrade section, got %s", truncate(gotBody))
	}

	send(nil)
	if strings.Contains(gotBody, "Need more room") {
		t.Fatalf("expected no upgrade section without upgrade_plan, got %s", truncate(gotBody))
	}
}

func TestServeSendEmailDetailsAndFooter(t *testing.T) {
	withScratchMailerConfig(t, "mail_server=smtp.example.com", "mail_username=user", "mail_security_token=right")

	var gotBody string
	origSend := mailerSendRun
	mailerSendRun = func(cfg mailerSMTPConfig, to, subject, htmlBody string) error {
		gotBody = htmlBody
		return nil
	}
	t.Cleanup(func() { mailerSendRun = origSend })

	m := &Mailer{PublicIP: "198.51.100.5"}
	srv, client := newMailerTestServer(t, m)
	resp, err := client.PostForm(srv.URL+"/send_email", url.Values{
		"recipient": {"john@example.com"}, "subject": {"New login to OpenPanel"}, "body": {"New password login from IP 203.0.113.10."},
		"transient": {"right"}, "type": {"user"}, "tips": {"If this wasn't you, change your password."},
		"details": {`[{"label":"IP address","value":"203.0.113.10","url":"https://www.abuseipdb.com/check/203.0.113.10"},{"label":"Country","value":"DE","url":"javascript:alert(1)","flag":"de"},{"label":"Other","value":"x","flag":"../evil"}]`},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(gotBody, `href="https://www.abuseipdb.com/check/203.0.113.10"`) || !strings.Contains(gotBody, "font-weight:600;\">DE") && !strings.Contains(gotBody, ">DE<") {
		t.Fatalf("expected details table with a link, got %s", truncate(gotBody))
	}
	if !strings.Contains(gotBody, `/static/flags/de.png"`) || strings.Contains(gotBody, "evil") {
		t.Fatalf("expected only the valid flag image, got %s", truncate(gotBody))
	}
	if strings.Contains(gotBody, "javascript:") {
		t.Fatalf("expected non-http links to be dropped, got %s", truncate(gotBody))
	}
	if !strings.Contains(gotBody, "Edit notification preferences") || strings.Contains(gotBody, "Edit Notification Preferences") {
		t.Fatalf("expected the preferences link in the footer, got %s", truncate(gotBody))
	}
}

func TestServeSendEmailAcceptsMultipleRecipients(t *testing.T) {
	withScratchMailerConfig(t, "mail_server=smtp.example.com", "mail_username=user", "mail_security_token=right")
	origSend := mailerSendRun
	var gotTo string
	mailerSendRun = func(cfg mailerSMTPConfig, to, subject, htmlBody string) error {
		gotTo = to
		return nil
	}
	t.Cleanup(func() { mailerSendRun = origSend })

	srv, client := newMailerTestServer(t, &Mailer{})
	resp, err := client.PostForm(srv.URL+"/send_email", url.Values{
		"recipient": {"a@b.com, c@d.org"}, "subject": {"Alert"}, "body": {"x"}, "transient": {"right"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || gotTo != "a@b.com, c@d.org" {
		t.Fatalf("expected 200 with list passed through, got %d to=%q", resp.StatusCode, gotTo)
	}

	resp, err = client.PostForm(srv.URL+"/send_email", url.Values{
		"recipient": {"a@b.com,nope"}, "subject": {"Alert"}, "body": {"x"}, "transient": {"right"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 when one address is invalid, got %d", resp.StatusCode)
	}
}

type fakeSMTPResult struct {
	rcpts []string
	data  string
}

// tiny fake SMTP server that rejects RCPTs listed in reject and answers the end of DATA with dataReply
func startFakeSMTP(t *testing.T, reject map[string]bool, dataReply string) (int, func() fakeSMTPResult) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	var res fakeSMTPResult
	var data strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		reply := func(s string) { conn.Write([]byte(s + "\r\n")) }
		reply("220 test")
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if inData {
				if line == "." {
					inData = false
					reply(dataReply)
					continue
				}
				data.WriteString(line + "\n")
				continue
			}
			switch cmd := strings.ToUpper(line); {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				reply("250 test")
			case strings.HasPrefix(cmd, "RCPT TO:"):
				addr := strings.Trim(line[len("RCPT TO:"):], "<>")
				if reject[addr] {
					reply("550 no such user")
					continue
				}
				res.rcpts = append(res.rcpts, addr)
				reply("250 ok")
			case cmd == "DATA":
				inData = true
				reply("354 go")
			case cmd == "QUIT":
				reply("221 bye")
				return
			default:
				reply("250 ok")
			}
		}
	}()

	port, _ := strconv.Atoi(strings.Split(ln.Addr().String(), ":")[1])
	return port, func() fakeSMTPResult {
		ln.Close()
		<-done
		res.data = data.String()
		return res
	}
}

func TestMailerSendRunSendsRcptPerAddress(t *testing.T) {
	port, result := startFakeSMTP(t, nil, "250 ok")
	cfg := mailerSMTPConfig{Server: "127.0.0.1", Port: port, DefaultSender: "root@host"}
	if err := mailerSendRun(cfg, "a@b.com, c@d.org", "Alert", "<p>hi</p>"); err != nil {
		t.Fatal(err)
	}
	res := result()
	if len(res.rcpts) != 2 || res.rcpts[0] != "a@b.com" || res.rcpts[1] != "c@d.org" {
		t.Fatalf("expected one RCPT per address, got %q", res.rcpts)
	}
	if !strings.Contains(res.data, "To: a@b.com, c@d.org") {
		t.Fatalf("expected joined To header, got %q", res.data)
	}
}

func TestMailerSendRunSkipsRejectedRecipient(t *testing.T) {
	port, result := startFakeSMTP(t, map[string]bool{"bad@b.com": true}, "250 ok")
	cfg := mailerSMTPConfig{Server: "127.0.0.1", Port: port, DefaultSender: "root@host"}
	err := mailerSendRun(cfg, "bad@b.com, c@d.org", "Alert", "<p>hi</p>")
	if !isPartialSend(err) || !strings.Contains(err.Error(), "bad@b.com") {
		t.Fatalf("expected partial send error naming bad@b.com, got %v", err)
	}
	res := result()
	if len(res.rcpts) != 1 || res.rcpts[0] != "c@d.org" {
		t.Fatalf("expected only the good address accepted, got %q", res.rcpts)
	}
	if !strings.Contains(res.data, "To: c@d.org\n") {
		t.Fatalf("expected To header without the rejected address, got %q", res.data)
	}
}

func TestMailerSendRunFailsWhenAllRecipientsRejected(t *testing.T) {
	port, result := startFakeSMTP(t, map[string]bool{"a@b.com": true, "c@d.org": true}, "250 ok")
	cfg := mailerSMTPConfig{Server: "127.0.0.1", Port: port, DefaultSender: "root@host"}
	err := mailerSendRun(cfg, "a@b.com,c@d.org", "Alert", "<p>hi</p>")
	if err == nil || isPartialSend(err) {
		t.Fatalf("expected a hard failure, got %v", err)
	}
	if res := result(); res.data != "" {
		t.Fatalf("expected no DATA sent, got %q", res.data)
	}
}

func TestMailerSendRunReportsRejectionAfterData(t *testing.T) {
	port, result := startFakeSMTP(t, nil, "554 message rejected as spam")
	cfg := mailerSMTPConfig{Server: "127.0.0.1", Port: port, DefaultSender: "root@host"}
	err := mailerSendRun(cfg, "a@b.com", "Alert", "<p>hi</p>")
	if err == nil || !strings.Contains(err.Error(), "spam") {
		t.Fatalf("expected the end-of-DATA rejection to be returned, got %v", err)
	}
	result()
}

func TestServeSendEmailPartialSendStillSucceeds(t *testing.T) {
	withScratchMailerConfig(t, "mail_server=smtp.example.com", "mail_username=user", "mail_security_token=right")
	origSend := mailerSendRun
	mailerSendRun = func(cfg mailerSMTPConfig, to, subject, htmlBody string) error {
		return &rejectedRecipientsError{rejected: map[string]error{"bad@b.com": io.EOF}}
	}
	t.Cleanup(func() { mailerSendRun = origSend })

	srv, client := newMailerTestServer(t, &Mailer{})
	resp, err := client.PostForm(srv.URL+"/send_email", url.Values{
		"recipient": {"bad@b.com,c@d.org"}, "subject": {"Alert"}, "body": {"x"}, "transient": {"right"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "sent successfully") {
		t.Fatalf("expected success when some recipients got it, got %d: %s", resp.StatusCode, body)
	}
}
