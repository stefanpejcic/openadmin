package handlers

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func withScratchAccessLogsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := AccessLogsDir
	AccessLogsDir = dir
	t.Cleanup(func() { AccessLogsDir = orig })
	return dir
}

func newAccessLogsTestServer(t *testing.T, h *AccessLogs) (*httptest.Server, *http.Client) {
	t.Helper()
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = origPath })

	db, err := admindb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("caller", hash, "admin")
	caller, err := db.UserByUsername("caller")
	if err != nil {
		t.Fatal(err)
	}

	sessions := auth.NewManager("test-secret", false)
	h.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /domains/log", h.ServeAccessLog)
	mux.HandleFunc("GET /domains/log/", h.ServeAccessLog)
	mux.HandleFunc("GET /domains/log/{domain_name}", h.ServeAccessLog)
	// Stub for the redirect target of the invalid-domain flash
	// (url_for('domains')); not the real domains list page.
	mux.HandleFunc("GET /domains", func(w http.ResponseWriter, r *http.Request) {
		for _, f := range auth.PopFlashes(w, r, sessions) {
			w.Write([]byte(f.Message))
		}
	})
	mux.HandleFunc("/login-as", func(w http.ResponseWriter, r *http.Request) {
		auth.LoginUser(w, r, sessions, caller, "203.0.113.1")
	})

	handler := auth.WithUserLoader(sessions, db)(mux)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	if _, err := client.Get(srv.URL + "/login-as"); err != nil {
		t.Fatal(err)
	}
	return srv, client
}

func TestAccessLogsListsDomainsWhenNoneGiven(t *testing.T) {
	withScratchAccessLogsDir(t)
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows(
		[]string{"domain_id", "docroot", "domain_url", "php_version", "username"}).
		AddRow(1, "/var/www/html", "example.com", "8.2", "alice"))

	h := &AccessLogs{MySQL: mysqlDB}
	srv, client := newAccessLogsTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/log")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"Domain Logs", "example.com", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestAccessLogsInvalidDomainRedirects(t *testing.T) {
	withScratchAccessLogsDir(t)
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	h := &AccessLogs{MySQL: mysqlDB}
	srv, client := newAccessLogsTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/log/not-a-domain")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Request.URL.Path != "/domains" {
		t.Fatalf("expected redirect to /domains, ended at %q", resp.Request.URL.Path)
	}
	if !strings.Contains(string(body), "Invalid domain name format.") {
		t.Fatalf("expected invalid-domain flash, got %s", truncate(string(body)))
	}
}

func TestAccessLogsMissingFileRedirectsWithFlash(t *testing.T) {
	withScratchAccessLogsDir(t)
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	h := &AccessLogs{MySQL: mysqlDB}
	srv, client := newAccessLogsTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/log/example.com")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Request.URL.Path != "/domains/log" {
		t.Fatalf("expected redirect to /domains/log, ended at %q", resp.Request.URL.Path)
	}
	if !strings.Contains(string(body), "Log file not found for domain example.com.") {
		t.Fatalf("expected missing-file flash, got %s", truncate(string(body)))
	}
}

func TestAccessLogsEmptyFileRedirectsWithFlash(t *testing.T) {
	dir := withScratchAccessLogsDir(t)
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	os.MkdirAll(filepath.Join(dir, "example.com"), 0755)
	os.WriteFile(filepath.Join(dir, "example.com", "access.log"), []byte{}, 0644)

	h := &AccessLogs{MySQL: mysqlDB}
	srv, client := newAccessLogsTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/log/example.com")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Log file for domain example.com is empty.") {
		t.Fatalf("expected empty-file flash, got %s", truncate(string(body)))
	}
}

func TestAccessLogsRendersEntriesMostRecentFirst(t *testing.T) {
	dir := withScratchAccessLogsDir(t)
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	os.MkdirAll(filepath.Join(dir, "example.com"), 0755)
	lines := `{"ts":1.0,"status":200,"request":{"client_ip":"1.2.3.4","method":"GET","uri":"/first"}}
{"ts":2.0,"status":404,"request":{"client_ip":"1.2.3.5","method":"GET","uri":"/second"}}
`
	os.WriteFile(filepath.Join(dir, "example.com", "access.log"), []byte(lines), 0644)

	h := &AccessLogs{MySQL: mysqlDB}
	srv, client := newAccessLogsTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/log/example.com")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	// json_logs.reverse(): /second (ts=2.0) is written last in the file but
	// should render first.
	secondIdx := strings.Index(got, "/second")
	firstIdx := strings.Index(got, "/first")
	if secondIdx == -1 || firstIdx == -1 {
		t.Fatalf("expected both log entries to render, got %s", truncate(got))
	}
	if secondIdx > firstIdx {
		t.Fatalf("expected the most recent entry (/second) to render before /first (reversed order)")
	}
	if !strings.Contains(got, "out of 2 rows") {
		t.Fatalf("expected row count summary, got %s", truncate(got))
	}
}

func TestAccessLogsMalformedLineRedirectsWithFlash(t *testing.T) {
	dir := withScratchAccessLogsDir(t)
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	os.MkdirAll(filepath.Join(dir, "example.com"), 0755)
	os.WriteFile(filepath.Join(dir, "example.com", "access.log"), []byte("not json\n"), 0644)

	h := &AccessLogs{MySQL: mysqlDB}
	srv, client := newAccessLogsTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/log/example.com")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Request.URL.Path != "/domains/log" {
		t.Fatalf("expected redirect to /domains/log, ended at %q", resp.Request.URL.Path)
	}
	if !strings.Contains(string(body), "Error reading log file:") {
		t.Fatalf("expected error-reading flash, got %s", truncate(string(body)))
	}
}

func TestPythonSliceMatchesPythonSemantics(t *testing.T) {
	logs := make([]map[string]interface{}, 5)
	for i := range logs {
		logs[i] = map[string]interface{}{"i": i}
	}

	if got := clampSlice(logs, 0, 2); len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got := clampSlice(logs, -1000, 0); len(got) != 0 {
		t.Fatalf("expected an out-of-range negative start clamped to empty, got %d entries", len(got))
	}
	if got := clampSlice(logs, 10, 20); len(got) != 0 {
		t.Fatalf("expected an out-of-range start beyond len() to be empty, got %d entries", len(got))
	}
}
