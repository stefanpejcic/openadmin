package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	os.WriteFile(path, []byte(`{"time":"2026-01-01 10:00:00","status":"unread","title":"first"}`+"\n"+
		`{"time":"2026-01-02 10:00:00","status":"unread","title":"second"}`+"\n"+
		`{"time":"2026-01-03 10:00:00","status":"unread","title":"third"}`+"\n"), 0644)

	n := &Notifications{}
	srv := httptest.NewServer(newNotificationsMux(n))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/notifications?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.HasPrefix(string(body), `[{"id":"","time":"2026-01-03`) {
		t.Fatalf("expected newest-first order, got %s", truncate(string(body)))
	}
}

func TestNotificationsViewRendersHTMLForEachMessageKind(t *testing.T) {
	path := withScratchNotificationsLog(t)
	os.WriteFile(path, []byte(strings.Join([]string{
		`{"time":"2026-01-01 10:00:00","status":"unread","severity":"warning","category":"resources","title":"High Memory Usage!","message":"RAM usage is 50%.","details":{"kind":"ram","percent":50,"used_mb":4096,"total_mb":8192,"processes":"pid1 proc1\npid2 proc2"}}`,
		`{"time":"2026-01-01 11:00:00","status":"unread","severity":"warning","category":"resources","title":"High CPU Usage!","message":"CPU usage is 80%.","details":{"kind":"cpu","percent":80,"processes":"pid3 proc3"}}`,
		`{"time":"2026-01-01 12:00:00","status":"read","severity":"warning","category":"resources","title":"OOM Alert","message":"1 user process(es) killed by OOM today.","details":{"kind":"oom","system":["sys line"],"users":[{"username":"alice","entries":["alice line"]}]}}`,
		`{"time":"2026-01-01 13:00:00","status":"unread","severity":"warning","category":"resources","title":"Running out of Disk Space!","message":"Disk usage is 90%.","details":{"kind":"disk","percent":90,"partitions":"/dev/sda1 90%\n/dev/sdb1 10%"}}`,
		`{"time":"2026-01-01 14:00:00","status":"unread","severity":"info","category":"update","title":"OpenPanel updated successfully!","message":"OpenPanel updated to version 2.0.12.","details":{"log_file":"/var/log/openpanel/updates/2.0.12.log"}}`,
		`{"time":"2026-01-01 15:30:00","status":"unread","severity":"warning","category":"resources","title":"High System Load!","message":"Load average is 27.2.","details":{"kind":"load","load":"27.2","crashlog":"/var/log/openpanel/admin/crashlog/1788526911.txt"}}`,
		`{"time":"2026-01-01 16:00:00","last_seen":"2026-01-01 16:20:00","count":5,"status":"read","severity":"critical","category":"service","title":"OpenPanel container not running!","message":"Container openpanel was not running.\n\nLast log lines:\nboom","resolved_at":"2026-01-01 16:25:00"}`,
		"2026-01-01 17:00:00 UNREAD Old format MESSAGE: Just a plain message",
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
		"4096MB of 8192MB", "width: 50%;", "pid1 proc1\npid2 proc2",
		"CPU Usage", "width: 80%;",
		"User processes: alice", "alice line", "sys line",
		"width: 90%;", "/dev/sda1 90%\n/dev/sdb1 10%",
		"View update log", `/settings/updates/log/?log_name=2.0.12.log`,
		"View crashlog", `/services/crashlogs/log/?log_name=1788526911.txt`,
		"Resolved after 25m", "5× · last 2026-01-01 16:20:00", "Container openpanel was not running.", "Last log lines:\nboom",
		"Critical", "Warning", "Info", "Just a plain message",
		`<option value="resources">Resources</option>`, "Unread (6)", "Resolved (1)", "Critical (1)",
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
	os.WriteFile(path, []byte(`{"status":"unread","title":"a","extra":"kept"}`+"\n2026-01-01 10:00:00 UNREAD b MESSAGE: x\n"), 0644)

	n := &Notifications{Sessions: auth.NewManager("test-secret", false)}
	srv := httptest.NewServer(newNotificationsMux(n))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/notifications/mark_as_read/0", "application/x-www-form-urlencoded", strings.NewReader("command=mark_all_as_read"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	remaining, _ := os.ReadFile(path)
	if strings.Contains(string(remaining), "unread") || strings.Contains(string(remaining), "UNREAD") {
		t.Fatalf("expected no unread entries left, got %q", remaining)
	}
	if !strings.Contains(string(remaining), `"extra":"kept"`) {
		t.Fatalf("expected fields the page doesn't know about to be kept, got %q", remaining)
	}
}

func withScratchSentinelSnapshots(t *testing.T) string {
	t.Helper()
	orig := SentinelSnapshotsPath
	SentinelSnapshotsPath = filepath.Join(t.TempDir(), "sentinel_snapshots.jsonl")
	t.Cleanup(func() { SentinelSnapshotsPath = orig })
	return SentinelSnapshotsPath
}

func TestLastSentinelCheckMissingFile(t *testing.T) {
	withScratchSentinelSnapshots(t)
	if got := lastSentinelCheck(time.Now()); got.Ran {
		t.Fatalf("expected no check without a snapshots file, got %+v", got)
	}
}

func TestLastSentinelCheckRecent(t *testing.T) {
	path := withScratchSentinelSnapshots(t)
	os.WriteFile(path, []byte(`{"ts":"2026-09-24 10:00:00","status":0,"pass":18,"warn":0,"fail":0}`+"\n"+`{"ts":"2026-09-24 10:05:00","status":2,"pass":16,"warn":1,"fail":2}`+"\n"), 0644)
	now := time.Now()
	os.Chtimes(path, now.Add(-3*time.Minute), now.Add(-3*time.Minute))

	got := lastSentinelCheck(now)
	if !got.Ran || got.Stale || got.Ago != "3m ago" {
		t.Fatalf("unexpected check info: %+v", got)
	}
}

func TestLastSentinelCheckStale(t *testing.T) {
	path := withScratchSentinelSnapshots(t)
	os.WriteFile(path, []byte(`{"pass":1,"warn":0,"fail":0}`+"\n"), 0644)
	now := time.Now()
	os.Chtimes(path, now.Add(-2*time.Hour), now.Add(-2*time.Hour))

	got := lastSentinelCheck(now)
	if !got.Stale || got.Ago != "2h 0m ago" {
		t.Fatalf("expected a stale check 2h ago, got %+v", got)
	}
}

func TestNotificationsViewShowsLastCheck(t *testing.T) {
	withScratchNotificationsLog(t)
	path := withScratchSentinelSnapshots(t)
	os.WriteFile(path, []byte(`{"pass":18,"warn":1,"fail":0}`+"\n"), 0644)

	rec := httptest.NewRecorder()
	(&Notifications{Sessions: auth.NewManager("test-secret", false)}).ServeView(rec, httptest.NewRequest("GET", "/notifications", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "Last check: just now") || strings.Contains(body, "18 pass") {
		t.Fatalf("expected the last check line in the page, got:\n%s", body)
	}
}
