package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newAPINotificationsMux(n *APINotifications) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/notifications", n.ServeNotifications)
	mux.HandleFunc("POST /api/notifications/{line_number}/read", n.HandleMarkRead)
	mux.HandleFunc("DELETE /api/notifications/{line_number}", n.HandleDelete)
	mux.HandleFunc("GET /api/usage/disk", n.ServeDiskUsage)
	return mux
}

func TestAPIServeNotificationsCreatesLogIfMissing(t *testing.T) {
	path := withScratchNotificationsLog(t)
	n := &APINotifications{}
	srv := httptest.NewServer(newAPINotificationsMux(n))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/notifications")
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
	var body struct {
		Success       bool     `json:"success"`
		Notifications []string `json:"notifications"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if !body.Success || len(body.Notifications) != 0 {
		t.Fatalf("expected success with an empty list, got %+v", body)
	}
}

func TestAPIServeNotificationsSortsNewestFirstAndSkipsBlank(t *testing.T) {
	path := withScratchNotificationsLog(t)
	os.WriteFile(path, []byte("2026-01-01 10:00:00 UNREAD first\n\n2026-01-03 10:00:00 UNREAD third\n2026-01-02 10:00:00 UNREAD second\n"), 0644)

	n := &APINotifications{}
	srv := httptest.NewServer(newAPINotificationsMux(n))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/notifications")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Notifications []string `json:"notifications"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Notifications) != 3 {
		t.Fatalf("expected 3 non-blank lines, got %+v", body.Notifications)
	}
	if !strings.HasPrefix(body.Notifications[0], "2026-01-03") {
		t.Fatalf("expected newest-first order, got %+v", body.Notifications)
	}
}

func TestAPIHandleMarkReadSpecificLine(t *testing.T) {
	path := withScratchNotificationsLog(t)
	os.WriteFile(path, []byte("A UNREAD one\nB UNREAD two\nC UNREAD three\n"), 0644)

	n := &APINotifications{}
	srv := httptest.NewServer(newAPINotificationsMux(n))
	defer srv.Close()

	// line_number=1 addresses the newest (last) raw line.
	resp, err := http.Post(srv.URL+"/api/notifications/1/read", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	written, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(written), "\n"), "\n")
	if lines[2] != "C READ three" {
		t.Fatalf("expected the last line marked READ, got %+v", lines)
	}
	if lines[0] != "A UNREAD one" || lines[1] != "B UNREAD two" {
		t.Fatalf("expected other lines untouched, got %+v", lines)
	}
}

func TestAPIHandleMarkReadAllViaQueryCommand(t *testing.T) {
	path := withScratchNotificationsLog(t)
	os.WriteFile(path, []byte("A UNREAD one\nB UNREAD two\n"), 0644)

	n := &APINotifications{}
	srv := httptest.NewServer(newAPINotificationsMux(n))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/notifications/1/read?command=mark_all_as_read", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	written, _ := os.ReadFile(path)
	if strings.Contains(string(written), "UNREAD") {
		t.Fatalf("expected every line marked READ, got %s", written)
	}
}

func TestAPIHandleMarkReadInvalidLineNumberRejected(t *testing.T) {
	path := withScratchNotificationsLog(t)
	os.WriteFile(path, []byte("A UNREAD one\n"), 0644)

	n := &APINotifications{}
	srv := httptest.NewServer(newAPINotificationsMux(n))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/notifications/99/read", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAPIHandleMarkReadNonNumericLineNumberIs404(t *testing.T) {
	withScratchNotificationsLog(t)

	n := &APINotifications{}
	srv := httptest.NewServer(newAPINotificationsMux(n))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/notifications/abc/read", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for a non-numeric line number, got %d", resp.StatusCode)
	}
}

func TestAPIHandleMarkReadMissingLogFileIs404(t *testing.T) {
	dir := t.TempDir()
	orig := NotificationsLogPath
	NotificationsLogPath = filepath.Join(dir, "does-not-exist.log")
	t.Cleanup(func() { NotificationsLogPath = orig })

	n := &APINotifications{}
	srv := httptest.NewServer(newAPINotificationsMux(n))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/notifications/1/read", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestAPIHandleDeleteSpecificLine(t *testing.T) {
	path := withScratchNotificationsLog(t)
	os.WriteFile(path, []byte("A one\nB two\nC three\n"), 0644)

	n := &APINotifications{}
	srv := httptest.NewServer(newAPINotificationsMux(n))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/notifications/1", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	written, _ := os.ReadFile(path)
	if strings.TrimRight(string(written), "\n") != "A one\nB two" {
		t.Fatalf("expected the last line deleted, got %q", written)
	}
}

func TestAPIHandleDeleteAllViaCommand(t *testing.T) {
	path := withScratchNotificationsLog(t)
	os.WriteFile(path, []byte("A one\nB two\n"), 0644)

	n := &APINotifications{}
	srv := httptest.NewServer(newAPINotificationsMux(n))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/notifications/1?command=delete_all", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	written, _ := os.ReadFile(path)
	if string(written) != "" {
		t.Fatalf("expected an emptied log file, got %q", written)
	}
}

func TestAPIHandleDeleteInvalidLineNumberRejected(t *testing.T) {
	path := withScratchNotificationsLog(t)
	os.WriteFile(path, []byte("A one\n"), 0644)

	n := &APINotifications{}
	srv := httptest.NewServer(newAPINotificationsMux(n))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/notifications/0", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func withScratchAPIQuotaReport(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	orig := QuotaReportPath
	QuotaReportPath = filepath.Join(dir, "quota_report.json")
	t.Cleanup(func() { QuotaReportPath = orig })
	if content != "" {
		os.WriteFile(QuotaReportPath, []byte(content), 0644)
	}
	return QuotaReportPath
}

func TestAPIServeDiskUsageReadsExistingReport(t *testing.T) {
	withScratchAPIQuotaReport(t, `{"users":[{"username":"alice","disk_used":100,"disk_hard":1000,"inodes_used":5}]}`)

	n := &APINotifications{}
	srv := httptest.NewServer(newAPINotificationsMux(n))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/usage/disk")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)

	alice, ok := body["alice"]
	if !ok {
		t.Fatalf("expected an entry for alice, got %+v", body)
	}
	if alice["diskusage"].(float64) != 100 || alice["disklimit"].(float64) != 1000 {
		t.Fatalf("unexpected disk fields: %+v", alice)
	}
	if alice["inodeslimit"].(float64) != 0 {
		t.Fatalf("expected a missing inodes_hard to default to 0, got %+v", alice["inodeslimit"])
	}
	if alice["bwusage"].(float64) != 0 || alice["bwlimit"].(float64) != 0 {
		t.Fatalf("expected bandwidth fields hardcoded to 0, got %+v", alice)
	}
}

func TestAPIServeDiskUsageGeneratesReportWhenMissing(t *testing.T) {
	path := withScratchAPIQuotaReport(t, "")

	orig := apiQuotaGenerateRun
	apiQuotaGenerateRun = func() error {
		return os.WriteFile(path, []byte(`{"users":[{"username":"bob","disk_used":1}]}`), 0644)
	}
	t.Cleanup(func() { apiQuotaGenerateRun = orig })

	n := &APINotifications{}
	srv := httptest.NewServer(newAPINotificationsMux(n))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/usage/disk")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "bob") {
		t.Fatalf("expected the generated report to be used, got %s", body)
	}
}

func TestAPIServeDiskUsageStillMissingAfterGenerationIs404(t *testing.T) {
	withScratchAPIQuotaReport(t, "")

	orig := apiQuotaGenerateRun
	apiQuotaGenerateRun = func() error { return nil } // "succeeds" but writes nothing
	t.Cleanup(func() { apiQuotaGenerateRun = orig })

	n := &APINotifications{}
	srv := httptest.NewServer(newAPINotificationsMux(n))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/usage/disk")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestAPIServeDiskUsageGenerationFailureIs500(t *testing.T) {
	withScratchAPIQuotaReport(t, "")

	orig := apiQuotaGenerateRun
	apiQuotaGenerateRun = func() error { return os.ErrInvalid }
	t.Cleanup(func() { apiQuotaGenerateRun = orig })

	n := &APINotifications{}
	srv := httptest.NewServer(newAPINotificationsMux(n))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/usage/disk")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}
