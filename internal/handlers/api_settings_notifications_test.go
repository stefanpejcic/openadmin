package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Note: unlike caddyFetchMetrics/localesInstallRun-style dependencies, the
// opencli invocations this handler makes (via runOpenCLI/
// runOpenCLINotificationUpdate) are not behind an injectable var anywhere
// in this package -- notification_settings.go's own HTML-page handler
// shells out the same way and its tests don't mock it either. Since every
// caller ignores runOpenCLI's return value, a missing "opencli" binary in
// the test environment fails fast without affecting the HTTP response, so
// these tests exercise the field-selection/validation logic (which is the
// actual behavior under test) plus the real on-disk SSH-whitelist side
// effect, rather than asserting on the exact opencli argv.

func newAPISettingsNotificationsTestServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	a := &APISettingsNotifications{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/settings/notifications", a.Serve)
	mux.HandleFunc("POST /api/settings/notifications", a.Serve)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

func TestAPISettingsNotificationsGetReflectsConfig(t *testing.T) {
	mainPath, notifPath, _ := withScratchNotificationConfig(t)
	os.WriteFile(mainPath, []byte("[DEFAULT]\nemail=admin@example.com\n[SMTP]\nmail_server=smtp.example.com\n"), 0644)
	os.WriteFile(notifPath, []byte("[DEFAULT]\nservices=panel,mysql\nattack=yes\n[ACTIONS]\nuser_create=yes\n"), 0644)

	srv, client := newAPISettingsNotificationsTestServer(t)
	resp, err := client.Get(srv.URL + "/api/settings/notifications")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var out struct {
		EmailAddress string                       `json:"email_address"`
		MailServer   string                       `json:"mail_server"`
		MailPort     string                       `json:"mail_port"`
		Settings     map[string]map[string]string `json:"settings"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("expected valid JSON: %v (%s)", err, body)
	}
	if out.EmailAddress != "admin@example.com" || out.MailServer != "smtp.example.com" {
		t.Fatalf("expected config values reflected, got %+v", out)
	}
	if out.MailPort != "465" {
		t.Fatalf("expected default mail_port 465, got %q", out.MailPort)
	}
	if out.Settings["DEFAULT"]["services"] != "panel,mysql" {
		t.Fatalf("expected full settings dict exposed, got %+v", out.Settings)
	}
}

func TestAPISettingsNotificationsPostRequiresJSON(t *testing.T) {
	withScratchNotificationConfig(t)
	srv, client := newAPISettingsNotificationsTestServer(t)

	resp, err := client.Post(srv.URL+"/api/settings/notifications", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
}

func TestAPISettingsNotificationsPostEmptyBodySucceeds(t *testing.T) {
	withScratchNotificationConfig(t)
	srv, client := newAPISettingsNotificationsTestServer(t)

	resp := postJSON(t, client, srv.URL+"/api/settings/notifications", `{}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Notification settings updated.") {
		t.Fatalf("expected success message, got %s", body)
	}
}

func TestAPISettingsNotificationsPostNumericUnchangedIsNoOp(t *testing.T) {
	_, notifPath, _ := withScratchNotificationConfig(t)
	os.WriteFile(notifPath, []byte("[DEFAULT]\ncpu=90\n"), 0644)

	srv, client := newAPISettingsNotificationsTestServer(t)
	resp := postJSON(t, client, srv.URL+"/api/settings/notifications", `{"cpu": 90}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (submitting the current value is a no-op, not an error), got %d: %s", resp.StatusCode, body)
	}
}

func TestAPISettingsNotificationsPostNonNumericValueSkippedWithoutCrash(t *testing.T) {
	withScratchNotificationConfig(t)
	srv, client := newAPISettingsNotificationsTestServer(t)

	resp := postJSON(t, client, srv.URL+"/api/settings/notifications", `{"cpu": "not-a-number"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (an unparseable numeric field is silently skipped, matching a caught ValueError), got %d: %s", resp.StatusCode, body)
	}
}

func TestAPISettingsNotificationsPostSSHWhitelistRejectsInvalidCIDR(t *testing.T) {
	_, _, whitelistPath := withScratchNotificationConfig(t)
	srv, client := newAPISettingsNotificationsTestServer(t)

	resp := postJSON(t, client, srv.URL+"/api/settings/notifications", `{"ssh_whitelist": "not-an-ip"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Invalid IP/CIDR: not-an-ip") {
		t.Fatalf("expected invalid-CIDR message, got %s", body)
	}
	if _, err := os.Stat(whitelistPath); err == nil {
		t.Fatal("expected the whitelist file to not be written when validation fails")
	}
}

func TestAPISettingsNotificationsPostSSHWhitelistWritesValidEntries(t *testing.T) {
	_, _, whitelistPath := withScratchNotificationConfig(t)
	srv, client := newAPISettingsNotificationsTestServer(t)

	resp := postJSON(t, client, srv.URL+"/api/settings/notifications", `{"ssh_whitelist": "203.0.113.4\n198.51.100.0/24"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	written, err := os.ReadFile(whitelistPath)
	if err != nil {
		t.Fatalf("expected the whitelist file to be written: %v", err)
	}
	if !strings.Contains(string(written), "203.0.113.4") || !strings.Contains(string(written), "198.51.100.0/24") {
		t.Fatalf("expected both entries in the written file, got %q", written)
	}
}

func TestAPISettingsNotificationsPostSSHWhitelistUnchangedLeavesFileAlone(t *testing.T) {
	_, _, whitelistPath := withScratchNotificationConfig(t)
	os.WriteFile(whitelistPath, []byte("# SSH Whitelist\n203.0.113.9\n"), 0644)
	info, _ := os.Stat(whitelistPath)
	origModTime := info.ModTime()

	srv, client := newAPISettingsNotificationsTestServer(t)
	resp := postJSON(t, client, srv.URL+"/api/settings/notifications", `{"ssh_whitelist": "203.0.113.9"}`)
	io.ReadAll(resp.Body)
	resp.Body.Close()

	newInfo, _ := os.Stat(whitelistPath)
	if !newInfo.ModTime().Equal(origModTime) {
		t.Fatal("expected the whitelist file to not be rewritten when the submitted list matches the current one")
	}
}

func TestAPISettingsNotificationsPostSkipsAbsentSSHWhitelist(t *testing.T) {
	_, _, whitelistPath := withScratchNotificationConfig(t)
	os.WriteFile(whitelistPath, []byte("# SSH Whitelist\n203.0.113.9\n"), 0644)

	srv, client := newAPISettingsNotificationsTestServer(t)
	resp := postJSON(t, client, srv.URL+"/api/settings/notifications", `{}`)
	io.ReadAll(resp.Body)
	resp.Body.Close()

	saved, _ := os.ReadFile(whitelistPath)
	if !strings.Contains(string(saved), "203.0.113.9") {
		t.Fatalf("expected whitelist left untouched when key absent from body, got %q", saved)
	}
}

func TestAPISettingsNotificationsToggleTruthyHelper(t *testing.T) {
	cases := []struct {
		v    interface{}
		want bool
	}{
		{"on", true}, {"yes", true}, {true, true},
		{"off", false}, {false, false}, {float64(1), false}, {nil, false},
	}
	for _, tc := range cases {
		if got := apiNotifToggleTruthy(tc.v); got != tc.want {
			t.Errorf("apiNotifToggleTruthy(%v) = %v, want %v", tc.v, got, tc.want)
		}
	}
}

func TestAPISettingsNotificationsToIntHelper(t *testing.T) {
	if n, ok := apiNotifToInt(float64(5.9)); !ok || n != 5 {
		t.Fatalf("expected float truncation to 5, got %d %v", n, ok)
	}
	if n, ok := apiNotifToInt("42"); !ok || n != 42 {
		t.Fatalf("expected string parse to 42, got %d %v", n, ok)
	}
	if _, ok := apiNotifToInt("not-a-number"); ok {
		t.Fatal("expected non-numeric string to fail")
	}
}

func TestAPISettingsNotificationsFieldValueHelper(t *testing.T) {
	data := map[string]interface{}{"a": "x", "b": nil, "c": float64(5)}
	if v, ok := apiNotifFieldValue(data, "a"); !ok || v != "x" {
		t.Fatalf("expected present string field, got %q %v", v, ok)
	}
	if _, ok := apiNotifFieldValue(data, "b"); ok {
		t.Fatal("expected a null value to report absent")
	}
	if _, ok := apiNotifFieldValue(data, "missing"); ok {
		t.Fatal("expected a missing key to report absent")
	}
	if v, ok := apiNotifFieldValue(data, "c"); !ok || v != "5" {
		t.Fatalf("expected numeric field stringified, got %q %v", v, ok)
	}
}

func TestAPISettingsNotificationsChangedHelper(t *testing.T) {
	if apiNotifChanged(" x ", "x") {
		t.Fatal("expected whitespace-only difference to not count as changed")
	}
	if !apiNotifChanged("x", "y") {
		t.Fatal("expected different values to count as changed")
	}
}
