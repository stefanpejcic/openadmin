package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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
