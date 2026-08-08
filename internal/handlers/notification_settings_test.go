package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/auth"
	"openadmin/internal/config"
)

func withScratchNotificationConfig(t *testing.T) (mainConfigPath, notifConfigPath, whitelistPath string) {
	t.Helper()
	dir := t.TempDir()

	origMain := config.OpenpanelConfigPath
	mainConfigPath = filepath.Join(dir, "openpanel.config")
	config.OpenpanelConfigPath = mainConfigPath
	t.Cleanup(func() { config.OpenpanelConfigPath = origMain })

	origNotif := NotificationsConfigPath
	notifConfigPath = filepath.Join(dir, "notifications.ini")
	NotificationsConfigPath = notifConfigPath
	t.Cleanup(func() { NotificationsConfigPath = origNotif })

	origWhitelist := SSHWhitelistPath
	whitelistPath = filepath.Join(dir, "ssh_whitelist.conf")
	SSHWhitelistPath = whitelistPath
	t.Cleanup(func() { SSHWhitelistPath = origWhitelist })

	return mainConfigPath, notifConfigPath, whitelistPath
}

func newNotificationSettingsMux(ns *NotificationSettings) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /settings/notifications", ns.ServeSettings)
	mux.HandleFunc("POST /settings/notifications", ns.HandleUpdate)
	return mux
}

func TestNotificationSettingsServeReflectsConfig(t *testing.T) {
	mainPath, _, _ := withScratchNotificationConfig(t)
	os.WriteFile(mainPath, []byte("[DEFAULT]\nemail=admin@example.com\n[SMTP]\nmail_server=smtp.example.com\n"), 0644)

	ns := &NotificationSettings{Sessions: auth.NewManager("test-secret", false)}
	srv := httptest.NewServer(newNotificationSettingsMux(ns))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/settings/notifications?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "admin@example.com") || !strings.Contains(string(body), "smtp.example.com") {
		t.Fatalf("expected config values in JSON output, got %s", truncate(string(body)))
	}
}

func TestNotificationSettingsServeRendersHTML(t *testing.T) {
	mainPath, notifPath, _ := withScratchNotificationConfig(t)
	os.WriteFile(mainPath, []byte("[DEFAULT]\nemail=admin@example.com\n[SMTP]\nmail_server=smtp.example.com\n"), 0644)
	os.WriteFile(notifPath, []byte("[DEFAULT]\nservices=panel,mysql\nattack=yes\nreboot=yes\n[ACTIONS]\nuser_create=yes\n"), 0644)

	ns := &NotificationSettings{Sessions: auth.NewManager("test-secret", false)}
	srv := httptest.NewServer(newNotificationSettingsMux(ns))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/settings/notifications")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	for _, want := range []string{
		"Notification settings", "admin@example.com", "smtp.example.com",
		"OpenPanel", "MySQL", "Server reboot", "User added",
		"SSH Allowlist", "</html>",
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}
}

func TestNotificationSettingsSSHWhitelistRejectsInvalidCIDR(t *testing.T) {
	withScratchNotificationConfig(t)

	ns := &NotificationSettings{Sessions: auth.NewManager("test-secret", false)}
	srv := httptest.NewServer(newNotificationSettingsMux(ns))
	defer srv.Close()

	resp, err := http.PostForm(srv.URL+"/settings/notifications", url.Values{
		"ssh_whitelist": {"not-an-ip"},
	})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	if _, err := os.Stat(SSHWhitelistPath); err == nil {
		t.Fatal("expected the whitelist file to not be written when validation fails")
	}
}

func TestNotificationSettingsSSHWhitelistWritesValidEntries(t *testing.T) {
	_, _, whitelistPath := withScratchNotificationConfig(t)

	ns := &NotificationSettings{Sessions: auth.NewManager("test-secret", false)}
	srv := httptest.NewServer(newNotificationSettingsMux(ns))
	defer srv.Close()

	resp, err := http.PostForm(srv.URL+"/settings/notifications", url.Values{
		"ssh_whitelist": {"203.0.113.4\n198.51.100.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	written, err := os.ReadFile(whitelistPath)
	if err != nil {
		t.Fatalf("expected the whitelist file to be written: %v", err)
	}
	if !strings.Contains(string(written), "203.0.113.4") || !strings.Contains(string(written), "198.51.100.0/24") {
		t.Fatalf("expected both entries in the written file, got %q", written)
	}
}

func TestNotificationSettingsValidators(t *testing.T) {
	if !isValidPort("443") || isValidPort("70000") || isValidPort("abc") {
		t.Fatal("isValidPort behaved unexpectedly")
	}
	if !isValidBool("True") || !isValidBool("False") || isValidBool("yes") {
		t.Fatal("isValidBool behaved unexpectedly")
	}
	if !isValidEmail("a@b.com") || isValidEmail("not-an-email") {
		t.Fatal("isValidEmail behaved unexpectedly")
	}
}

func TestFormValueOrNilDistinguishesAbsentFromEmpty(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("present="))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if v := formValueOrNil(req, "absent"); v != nil {
		t.Fatalf("expected nil for an absent field, got %v", *v)
	}
	if v := formValueOrNil(req, "present"); v == nil || *v != "" {
		t.Fatalf("expected a non-nil empty string for a present-but-empty field, got %v", v)
	}
}

func postTestSMTP(t *testing.T, ns *NotificationSettings, form url.Values) map[string]interface{} {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/settings/notifications/test-smtp", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	ns.TestSMTP(rec, req)

	var payload map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	payload["__status"] = float64(rec.Code)
	return payload
}

func TestTestSMTPRequiresServerAndPort(t *testing.T) {
	withScratchNotificationConfig(t)
	ns := &NotificationSettings{Sessions: auth.NewManager("test-secret", false)}

	payload := postTestSMTP(t, ns, url.Values{"mail_default_sender": {"a@b.com"}})
	if payload["__status"] != float64(http.StatusBadRequest) || !strings.Contains(fmt.Sprint(payload["error"]), "server") {
		t.Fatalf("expected a missing-server error, got %v", payload)
	}

	payload = postTestSMTP(t, ns, url.Values{"mail_server": {"smtp.example.com"}, "mail_port": {"nope"}, "mail_default_sender": {"a@b.com"}})
	if payload["__status"] != float64(http.StatusBadRequest) || !strings.Contains(fmt.Sprint(payload["error"]), "port") {
		t.Fatalf("expected a missing-port error, got %v", payload)
	}
}

func TestTestSMTPRequiresARecipient(t *testing.T) {
	withScratchNotificationConfig(t)
	ns := &NotificationSettings{Sessions: auth.NewManager("test-secret", false)}

	payload := postTestSMTP(t, ns, url.Values{"mail_server": {"smtp.example.com"}, "mail_port": {"587"}})
	if payload["__status"] != float64(http.StatusBadRequest) {
		t.Fatalf("expected a bad request when no recipient can be derived, got %v", payload)
	}
}

func TestTestSMTPSuccessReportsRecipient(t *testing.T) {
	withScratchNotificationConfig(t)
	ns := &NotificationSettings{Sessions: auth.NewManager("test-secret", false)}

	origSend := mailerSendRun
	var gotTo, gotSubject string
	mailerSendRun = func(cfg mailerSMTPConfig, to, subject, htmlBody string) error {
		gotTo, gotSubject = to, subject
		if cfg.Server != "smtp.example.com" || cfg.Port != 587 {
			t.Fatalf("unexpected cfg passed to mailerSendRun: %+v", cfg)
		}
		return nil
	}
	t.Cleanup(func() { mailerSendRun = origSend })

	payload := postTestSMTP(t, ns, url.Values{
		"mail_server":         {"smtp.example.com"},
		"mail_port":           {"587"},
		"mail_default_sender": {"admin@example.com"},
	})
	if payload["success"] != true {
		t.Fatalf("expected success, got %v", payload)
	}
	if gotTo != "admin@example.com" {
		t.Fatalf("expected test email sent to admin@example.com, got %q", gotTo)
	}
	if gotSubject == "" {
		t.Fatalf("expected a non-empty subject")
	}
}

func TestTestSMTPFailureIsReportedNotErrored(t *testing.T) {
	withScratchNotificationConfig(t)
	ns := &NotificationSettings{Sessions: auth.NewManager("test-secret", false)}

	origSend := mailerSendRun
	mailerSendRun = func(cfg mailerSMTPConfig, to, subject, htmlBody string) error {
		return fmt.Errorf("connection refused")
	}
	t.Cleanup(func() { mailerSendRun = origSend })

	payload := postTestSMTP(t, ns, url.Values{
		"mail_server":         {"smtp.example.com"},
		"mail_port":           {"587"},
		"mail_default_sender": {"admin@example.com"},
	})
	if payload["__status"] != float64(http.StatusOK) {
		t.Fatalf("SMTP send failures should still be a 200 with success:false, got status %v", payload["__status"])
	}
	if payload["success"] != false || !strings.Contains(fmt.Sprint(payload["error"]), "connection refused") {
		t.Fatalf("expected a reported failure with the underlying error, got %v", payload)
	}
}
