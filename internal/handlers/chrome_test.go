package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withScratchNotificationsLogForChrome(t *testing.T, lines ...string) {
	t.Helper()
	dir := t.TempDir()
	origPath := NotificationsLogPath
	NotificationsLogPath = filepath.Join(dir, "notifications.log")
	t.Cleanup(func() { NotificationsLogPath = origPath })
	if len(lines) > 0 {
		os.WriteFile(NotificationsLogPath, []byte(strings.Join(lines, "\n")+"\n"), 0644)
	}
}

// TestBuildChromeUnreadNotificationsCountsAllNotJustLast5 guards against a
// real bug: the unread badge used to only scan the last 5 log lines, so it
// could never report more than 5 even with many more unread notifications,
// and dismissing one only nudged the count within that narrow window
// instead of reflecting the real total.
func TestBuildChromeUnreadNotificationsCountsAllNotJustLast5(t *testing.T) {
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = `{"status":"unread","severity":"info","title":"notification ` + string(rune('A'+i)) + `"}`
	}
	withScratchNotificationsLogForChrome(t, lines...)

	r := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	chrome := buildChrome(r, "Dashboard")

	if chrome.UnreadNotifications != 12 {
		t.Fatalf("expected all 12 unread notifications counted, got %d", chrome.UnreadNotifications)
	}
}

func TestBuildChromeUnreadNotificationsMixedReadAndUnread(t *testing.T) {
	withScratchNotificationsLogForChrome(t,
		`{"status":"unread","title":"one"}`,
		`{"status":"read","title":"two"}`,
		"2026-01-01 10:00:00 UNREAD three MESSAGE: old format",
	)

	r := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	chrome := buildChrome(r, "Dashboard")

	if chrome.UnreadNotifications != 2 {
		t.Fatalf("expected 2 unread, got %d", chrome.UnreadNotifications)
	}
}

func TestInitChromeSiteInfoDetectsCustomCSS(t *testing.T) {
	origPath := ChromeCustomCSSPath
	t.Cleanup(func() { ChromeCustomCSSPath = origPath })

	dir := t.TempDir()
	ChromeCustomCSSPath = filepath.Join(dir, "custom.css")
	InitChromeSiteInfo("", "", "", "", "", false, "")
	if chromeSite.CustomCSSEnabled {
		t.Fatalf("expected CustomCSSEnabled false when %s is absent", ChromeCustomCSSPath)
	}

	os.WriteFile(ChromeCustomCSSPath, []byte("body{}"), 0644)
	InitChromeSiteInfo("", "", "", "", "", false, "")
	if !chromeSite.CustomCSSEnabled {
		t.Fatalf("expected CustomCSSEnabled true once %s exists", ChromeCustomCSSPath)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	c := buildChrome(req, "Title")
	if !c.CustomCSSEnabled {
		t.Fatalf("expected buildChrome to carry CustomCSSEnabled through to Chrome")
	}
}

func TestQuickStartDismissedTracksSkipFile(t *testing.T) {
	dir := t.TempDir()
	origPath := ChromeQuickStartSkipFilePath
	ChromeQuickStartSkipFilePath = filepath.Join(dir, "quick_start.dismissed")
	t.Cleanup(func() { ChromeQuickStartSkipFilePath = origPath })

	if quickStartDismissed() {
		t.Fatalf("expected quickStartDismissed false before the skip file exists")
	}

	os.WriteFile(ChromeQuickStartSkipFilePath, nil, 0644)
	if !quickStartDismissed() {
		t.Fatalf("expected quickStartDismissed true once the skip file exists")
	}
}

func TestReadMenuStyle(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"menu_style=modern\n":           "modern",
		"foo=bar\nmenu_style=classic\n": "classic",
		"menu_style=\"modern\"\n":       "modern",
		"menu_style=weird\n":            "classic",
		"enabled_modules=dns\n":         "classic",
	}
	i := 0
	for content, want := range cases {
		i++
		p := filepath.Join(dir, fmt.Sprintf("openpanel%d.config", i))
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		if got := readMenuStyle(p); got != want {
			t.Errorf("readMenuStyle(%q) = %q, want %q", content, got, want)
		}
	}
	if got := readMenuStyle(filepath.Join(dir, "missing.config")); got != "classic" {
		t.Errorf("expected classic for a missing config, got %q", got)
	}
}

func TestUnreadNotificationSummary(t *testing.T) {
	entries := []Notification{
		parseNotification(`{"status":"unread","severity":"warning","category":"resources","title":"High SWAP usage!"}`),
		parseNotification(`{"status":"unread","severity":"critical","category":"service","title":"OpenPanel container not running!"}`),
		parseNotification(`{"status":"unread","severity":"critical","category":"service","title":"Caddy not active - websites down!"}`),
		parseNotification(`{"status":"read","severity":"critical","category":"service","title":"MariaDB service not active!"}`),
		parseNotification(`2026-09-23 18:02:51 UNREAD Old format entry MESSAGE: x`),
	}
	unread, down := unreadNotificationSummary(entries)
	if unread != 4 {
		t.Errorf("expected 4 unread, got %d", unread)
	}
	if down != "OpenPanel container not running!" {
		t.Errorf("expected the newest unread critical service alert, got %q", down)
	}
}
