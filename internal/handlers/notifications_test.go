package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/auth"
)

func withScratchNotificationsLog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := NotificationsLogPath
	NotificationsLogPath = filepath.Join(dir, "notifications.log")
	t.Cleanup(func() { NotificationsLogPath = orig })
	return NotificationsLogPath
}

func newNotificationsMux(n *Notifications) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /notifications", n.ServeView)
	mux.HandleFunc("POST /notifications/delete/{line_number}", n.HandleDelete)
	mux.HandleFunc("POST /notifications/mark_as_read/{line_number}", n.HandleMarkAsRead)
	return mux
}

func TestNotificationsViewCreatesLogIfMissing(t *testing.T) {
	path := withScratchNotificationsLog(t)
	n := &Notifications{}
	srv := httptest.NewServer(newNotificationsMux(n))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/notifications?output=json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected the log file to be created: %v", err)
	}
}

func TestNotificationsViewSortsNewestFirst(t *testing.T) {
	path := withScratchNotificationsLog(t)
	os.WriteFile(path, []byte("2026-01-01 10:00:00 UNREAD first\n2026-01-03 10:00:00 UNREAD third\n2026-01-02 10:00:00 UNREAD second\n"), 0644)

	n := &Notifications{}
	srv := httptest.NewServer(newNotificationsMux(n))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/notifications?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.HasPrefix(string(body), `["2026-01-03`) {
		t.Fatalf("expected newest-first order, got %s", truncate(string(body)))
	}
}

func TestNotificationsViewRendersHTMLForEachMessageKind(t *testing.T) {
	path := withScratchNotificationsLog(t)
	os.WriteFile(path, []byte(strings.Join([]string{
		"2026-01-01 10:00:00 UNREAD High memory usage MESSAGE: Used RAM: 4GB/8GB (50%) | proc1\nproc2",
		"2026-01-01 11:00:00 UNREAD High CPU usage MESSAGE: CPU: 80% | proc1\nproc2",
		"2026-01-01 12:00:00 READ OOM event MESSAGE: 2026-01-01 12:00:00 killed by OOM | alice: proc1 | 20 more info",
		"2026-01-01 13:00:00 UNREAD Disk usage MESSAGE: Disk usage: 90% | Partitions: /dev/sda1 90%",
		"2026-01-01 14:00:00 UNREAD Update finished MESSAGE: Update completed. Log file: /var/log/openpanel/admin/updates/2026-01-01.log",
		"2026-01-01 15:00:00 UNREAD Crash detected MESSAGE: Service crashed, see detailed report: /var/log/openpanel/admin/crashes/2026-01-01.log",
		"2026-01-01 16:00:00 READ Plain notice MESSAGE: Just a plain message",
	}, "\n")+"\n"), 0644)

	n := &Notifications{Sessions: auth.NewManager("test-secret", false)}
	srv := httptest.NewServer(newNotificationsMux(n))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/notifications")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{
		"High memory usage", "50%", "80%", "OOM kills detected", "alice",
		"Disk usage", "90%", "Log file:", "detailed report:", "Just a plain message",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestNotificationsDeleteSpecificLine(t *testing.T) {
	path := withScratchNotificationsLog(t)
	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0644)

	n := &Notifications{Sessions: auth.NewManager("test-secret", false)}
	srv := httptest.NewServer(newNotificationsMux(n))
	defer srv.Close()

	// line_number=1 is the newest / last physical line in the file ("line3")
	resp, err := http.Post(srv.URL+"/notifications/delete/1", "application/x-www-form-urlencoded", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	remaining, _ := os.ReadFile(path)
	if strings.Contains(string(remaining), "line3") {
		t.Fatalf("expected line3 to be deleted, got %q", remaining)
	}
	if !strings.Contains(string(remaining), "line1") || !strings.Contains(string(remaining), "line2") {
		t.Fatalf("expected line1 and line2 to survive, got %q", remaining)
	}
}

func TestNotificationsDeleteAll(t *testing.T) {
	path := withScratchNotificationsLog(t)
	os.WriteFile(path, []byte("line1\nline2\n"), 0644)

	n := &Notifications{Sessions: auth.NewManager("test-secret", false)}
	srv := httptest.NewServer(newNotificationsMux(n))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/notifications/delete/0", "application/x-www-form-urlencoded", strings.NewReader("command=delete_all"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	remaining, _ := os.ReadFile(path)
	if strings.TrimSpace(string(remaining)) != "" {
		t.Fatalf("expected an empty log after delete_all, got %q", remaining)
	}
}

func TestNotificationsDeleteInvalidLineNumberRejected(t *testing.T) {
	path := withScratchNotificationsLog(t)
	os.WriteFile(path, []byte("line1\n"), 0644)

	n := &Notifications{Sessions: auth.NewManager("test-secret", false)}
	srv := httptest.NewServer(newNotificationsMux(n))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/notifications/delete/99", "application/x-www-form-urlencoded", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for an out-of-range line number, got %d", resp.StatusCode)
	}

	remaining, _ := os.ReadFile(path)
	if !strings.Contains(string(remaining), "line1") {
		t.Fatalf("expected the log to be untouched, got %q", remaining)
	}
}

func TestNotificationsMarkAsReadSpecificLine(t *testing.T) {
	path := withScratchNotificationsLog(t)
	os.WriteFile(path, []byte("2026-01-01 UNREAD a\n2026-01-02 UNREAD b\n"), 0644)

	n := &Notifications{Sessions: auth.NewManager("test-secret", false)}
	srv := httptest.NewServer(newNotificationsMux(n))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/notifications/mark_as_read/1", "application/x-www-form-urlencoded", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	remaining, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(remaining), "\n"), "\n")
	if !strings.Contains(lines[1], "READ b") || strings.Contains(lines[1], "UNREAD") {
		t.Fatalf("expected the last line to be marked READ, got %q", remaining)
	}
	if !strings.Contains(lines[0], "UNREAD") {
		t.Fatalf("expected the first line to remain UNREAD, got %q", remaining)
	}
}

func TestNotificationsMarkAllAsRead(t *testing.T) {
	path := withScratchNotificationsLog(t)
	os.WriteFile(path, []byte("UNREAD a\nUNREAD b\n"), 0644)

	n := &Notifications{Sessions: auth.NewManager("test-secret", false)}
	srv := httptest.NewServer(newNotificationsMux(n))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/notifications/mark_as_read/0", "application/x-www-form-urlencoded", strings.NewReader("command=mark_all_as_read"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	remaining, _ := os.ReadFile(path)
	if strings.Contains(string(remaining), "UNREAD") {
		t.Fatalf("expected no UNREAD entries left, got %q", remaining)
	}
}
