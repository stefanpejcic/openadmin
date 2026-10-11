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
		"Login Whitelist", "</html>",
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
	if !isValidEmailList("a@b.com, c@d.org") || isValidEmailList("a@b.com,nope") || isValidEmailList(" , ") {
		t.Fatal("isValidEmailList behaved unexpectedly")
	}
	if !isRecipientList("root@localhost,a@b.com") || isRecipientList("a@b.com,nope") || isRecipientList("") {
		t.Fatal("isRecipientList behaved unexpectedly")
	}
	if got := splitEmailList(" a@b.com ,, c@d.org,"); len(got) != 2 || got[0] != "a@b.com" || got[1] != "c@d.org" {
		t.Fatalf("splitEmailList = %q", got)
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

func TestJoinWebhookURLs(t *testing.T) {
	got, err := joinWebhookURLs([]string{" https://a.example/x ", "", "http://b.example/y https://c.example"})
	if err != nil || got != "https://a.example/x http://b.example/y https://c.example" {
		t.Fatalf("joinWebhookURLs = %q, %v", got, err)
	}
	if got, err := joinWebhookURLs([]string{""}); err != nil || got != "" {
		t.Fatalf("expected empty input to clear webhooks, got %q, %v", got, err)
	}
	if _, err := joinWebhookURLs([]string{"https://ok.example", "ftp://nope"}); err == nil {
		t.Fatal("expected a non-http URL to be rejected")
	}
}

func TestNotificationSettingsRendersWebhookList(t *testing.T) {
	_, notifPath, _ := withScratchNotificationConfig(t)
	os.WriteFile(notifPath, []byte("[DEFAULT]\nwebhook_url=https://a.example/x https://b.example/y\n"), 0644)

	ns := &NotificationSettings{Sessions: auth.NewManager("test-secret", false)}
	srv := httptest.NewServer(newNotificationSettingsMux(ns))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/settings/notifications")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `data-items="[&#34;https://a.example/x&#34;,&#34;https://b.example/y&#34;]"`) {
		t.Fatal("expected both webhook URLs passed to the webhook list")
	}
}

func TestTestWebhookPostsSentinelPayload(t *testing.T) {
	var got map[string]string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer hook.Close()

	ns := &NotificationSettings{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/settings/notifications/test-webhook", strings.NewReader(url.Values{"url": {hook.URL}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ns.TestWebhook(rec, req)

	var payload map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &payload)
	if payload["success"] != true {
		t.Fatalf("expected success, got %s", rec.Body.String())
	}
	if !strings.Contains(got["text"], "test notification") || !strings.Contains(got["content"], "test notification") || !strings.HasPrefix(got["username"], "OpenAdmin-") {
		t.Fatalf("unexpected webhook payload %v", got)
	}
}

func TestTestWebhookReportsBadStatus(t *testing.T) {
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Invalid Webhook Token", http.StatusUnauthorized)
	}))
	defer hook.Close()

	ns := &NotificationSettings{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/settings/notifications/test-webhook", strings.NewReader(url.Values{"url": {hook.URL}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ns.TestWebhook(rec, req)

	var payload map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &payload)
	if payload["success"] != false || !strings.Contains(fmt.Sprint(payload["error"]), "401") || !strings.Contains(fmt.Sprint(payload["error"]), "Invalid Webhook Token") {
		t.Fatalf("expected the 401 and body in the error, got %s", rec.Body.String())
	}
}

func TestTestWebhookRejectsInvalidURL(t *testing.T) {
	ns := &NotificationSettings{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/settings/notifications/test-webhook", strings.NewReader("url=javascript:alert(1)"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ns.TestWebhook(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestJoinEmailList(t *testing.T) {
	got, err := joinEmailList([]string{" a@b.com ", "", "c@d.org"})
	if err != nil || got != "a@b.com,c@d.org" {
		t.Fatalf("joinEmailList = %q, %v", got, err)
	}
	if got, err := joinEmailList([]string{""}); err != nil || got != "" {
		t.Fatalf("expected empty input to clear the email, got %q, %v", got, err)
	}
	if _, err := joinEmailList([]string{"a@b.com", "nope"}); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected the bad address named in the error, got %v", err)
	}
}

func TestNotificationSettingsRendersEmailList(t *testing.T) {
	mainPath, _, _ := withScratchNotificationConfig(t)
	os.WriteFile(mainPath, []byte("[DEFAULT]\nemail=a@b.com, c@d.org\n"), 0644)

	ns := &NotificationSettings{Sessions: auth.NewManager("test-secret", false)}
	srv := httptest.NewServer(newNotificationSettingsMux(ns))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/settings/notifications")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `data-items="[&#34;a@b.com&#34;,&#34;c@d.org&#34;]"`) {
		t.Fatal("expected one email row per address")
	}
}

func TestThresholdAdvice(t *testing.T) {
	h := thresholdHost{Cores: 4, RAMGB: 8, SwapGB: 2, DiskGB: 200}
	adv := func(key string, h thresholdHost) notifThresholdView {
		v := notifThresholdView{Key: key}
		thresholdAdvice(&v, h)
		return v
	}

	if v := adv("load", h); v.WarnBelow != 8 || !strings.Contains(v.Hint, "at least 8") || !strings.Contains(v.Hint, "4 CPU cores") {
		t.Fatalf("load advice = %+v", v)
	}
	if v := adv("load", thresholdHost{Cores: 64}); v.WarnBelow != 100 || !strings.Contains(v.Hint, "Recommended: 100") {
		t.Fatalf("expected load capped at sentinel's max of 100, got %+v", v)
	}
	if v := adv("du", h); v.WarnAbove != 97 || v.WarnBelow != 70 {
		t.Fatalf("expected 5 GB reserve on a 200 GB disk, got %+v", v)
	}
	if v := adv("du", thresholdHost{DiskGB: 20}); v.WarnAbove != 90 {
		t.Fatalf("expected 10%% reserve on a small disk, got %+v", v)
	}
	if v := adv("swap", thresholdHost{}); v.WarnBelow != 0 || !strings.Contains(v.Hint, "no swap") {
		t.Fatalf("expected no-swap hint without warnings, got %+v", v)
	}
	if v := adv("cpu", h); v.WarnBelow != 80 || v.Hint == "" {
		t.Fatalf("cpu advice = %+v", v)
	}
}

func TestNotificationSettingsRendersThresholdAdvice(t *testing.T) {
	_, notifPath, _ := withScratchNotificationConfig(t)
	os.WriteFile(notifPath, []byte("[DEFAULT]\nload=4\n"), 0644)
	orig := thresholdHostRun
	thresholdHostRun = func() thresholdHost { return thresholdHost{Cores: 6, RAMGB: 16, SwapGB: 4, DiskGB: 100} }
	t.Cleanup(func() { thresholdHostRun = orig })

	ns := &NotificationSettings{Sessions: auth.NewManager("test-secret", false)}
	srv := httptest.NewServer(newNotificationSettingsMux(ns))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/settings/notifications")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{"at least 12, 2× the 6 CPU cores", `data-value="4"`, "Number(v) < 12", "Below 12, normal load on 6 cores"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected %q in the page", want)
		}
	}
}
