package handlers

import (
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
		lines[i] = "2026-01-01 UNREAD notification " + string(rune('A'+i))
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
		"2026-01-01 UNREAD one",
		"2026-01-01 READ two",
		"2026-01-01 UNREAD three",
	)

	r := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	chrome := buildChrome(r, "Dashboard")

	if chrome.UnreadNotifications != 2 {
		t.Fatalf("expected 2 unread, got %d", chrome.UnreadNotifications)
	}
}
