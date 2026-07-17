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
