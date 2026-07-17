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

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func withScratchGoAccessStatsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := GoAccessStatsDir
	GoAccessStatsDir = dir
	t.Cleanup(func() { GoAccessStatsDir = orig })
	return dir
}

func newGoAccessStatsTestServer(t *testing.T, h *GoAccessStats) (*httptest.Server, *http.Client) {
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
	mux.HandleFunc("GET /domains/stats/{current_username}/{domain_name}", h.ServeStats)
	// Stub for the redirect target of the missing-stats-file flash
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

func TestGoAccessStatsInvalidDomainReturnsBadRequest(t *testing.T) {
	withScratchGoAccessStatsDir(t)
	h := &GoAccessStats{}
	srv, client := newGoAccessStatsTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/stats/alice/not-a-domain")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestGoAccessStatsMissingFileFlashesAndRedirects(t *testing.T) {
	withScratchGoAccessStatsDir(t)
	h := &GoAccessStats{}
	srv, client := newGoAccessStatsTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/stats/alice/example.com")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Request.URL.Path != "/domains" {
		t.Fatalf("expected redirect to /domains, ended at %q", resp.Request.URL.Path)
	}
	if !strings.Contains(string(body), "Stats file for domain example.com not found. Data is generated every 24h.") {
		t.Fatalf("expected missing-stats flash, got %s", truncate(string(body)))
	}
}

// TestGoAccessStatsServesRawFileVerbatim locks in that this route is a raw
// passthrough of the pre-generated GoAccess HTML report -- not wrapped in
// the usual chrome layer -- matching goaccess_single.html's
// `{{ html_content|safe }}` body with no {% extends %}.
func TestGoAccessStatsServesRawFileVerbatim(t *testing.T) {
	dir := withScratchGoAccessStatsDir(t)
	os.MkdirAll(filepath.Join(dir, "alice"), 0755)
	report := "<!doctype html><html><head><title>GoAccess</title></head><body>Report for example.com</body></html>"
	os.WriteFile(filepath.Join(dir, "alice", "example.com.html"), []byte(report), 0644)

	h := &GoAccessStats{}
	srv, client := newGoAccessStatsTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/stats/alice/example.com")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if string(body) != report {
		t.Fatalf("expected the raw report bytes verbatim (no chrome wrapper), got %s", truncate(string(body)))
	}
}
