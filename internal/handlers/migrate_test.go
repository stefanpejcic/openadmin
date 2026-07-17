package handlers

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func withScratchMigratePaths(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	origLog, origPID := MigrateLogPath, MigrateProcessPIDFile
	MigrateLogPath = filepath.Join(dir, "migrate.log")
	MigrateProcessPIDFile = filepath.Join(dir, "migrate.pid")
	t.Cleanup(func() {
		MigrateLogPath = origLog
		MigrateProcessPIDFile = origPID
	})
}

func newMigrateTestServer(t *testing.T, m *Migrate) (*httptest.Server, *http.Client) {
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
	m.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /server/migrate", m.ServeMigrate)
	mux.HandleFunc("POST /server/migrate", m.ServeMigrate)
	mux.HandleFunc("GET /server/migrate/status", m.ServeMigrateStatus)
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

func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("expected the current process to report as alive")
	}
	if processAlive(0) {
		t.Fatal("expected pid 0 to report as not alive")
	}
	// A pid extremely unlikely to be in use.
	if processAlive(1 << 30) {
		t.Fatal("expected a bogus high pid to report as not alive")
	}
}

func TestServeMigrateGetShowsForm(t *testing.T) {
	withScratchMigratePaths(t)

	m := &Migrate{}
	srv, client := newMigrateTestServer(t, m)

	resp, err := client.Get(srv.URL + "/server/migrate")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	for _, want := range []string{`name="host"`, "Server Migration", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q (page may have been truncated by a template execution error), got %s", want, truncate(got))
		}
	}
}

func TestServeMigratePostMissingFieldRedirects(t *testing.T) {
	withScratchMigratePaths(t)

	called := false
	orig := migrateStartRun
	migrateStartRun = func(host, root, password string) error { called = true; return nil }
	t.Cleanup(func() { migrateStartRun = orig })

	m := &Migrate{}
	srv, client := newMigrateTestServer(t, m)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := client.PostForm(srv.URL+"/server/migrate", url.Values{"host": {"1.2.3.4"}, "root": {"root"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected a redirect for missing password, got %d", resp.StatusCode)
	}
	if called {
		t.Fatal("expected migrateStartRun NOT to be called when a field is missing")
	}
}

func TestServeMigratePostStartsAndRenders(t *testing.T) {
	withScratchMigratePaths(t)

	var gotHost, gotRoot, gotPassword string
	orig := migrateStartRun
	migrateStartRun = func(host, root, password string) error {
		gotHost, gotRoot, gotPassword = host, root, password
		return nil
	}
	t.Cleanup(func() { migrateStartRun = orig })

	m := &Migrate{}
	srv, client := newMigrateTestServer(t, m)

	resp, err := client.PostForm(srv.URL+"/server/migrate", url.Values{
		"host": {"1.2.3.4"}, "root": {"root"}, "password": {"secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if gotHost != "1.2.3.4" || gotRoot != "root" || gotPassword != "secret" {
		t.Fatalf("unexpected args passed to migrateStartRun: %q %q %q", gotHost, gotRoot, gotPassword)
	}
	got := string(body)
	for _, want := range []string{"Migration process started.", "migration is in progress", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q (page may have been truncated by a template execution error), got %s", want, truncate(got))
		}
	}
}

func TestServeMigrateStatusUnknownWithoutPidFile(t *testing.T) {
	withScratchMigratePaths(t)

	m := &Migrate{}
	srv, client := newMigrateTestServer(t, m)

	resp, err := client.Get(srv.URL + "/server/migrate/status")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"status":"unknown"`) {
		t.Fatalf("expected unknown status without a pid file, got %s", body)
	}
}

func TestServeMigrateStatusRunningWhilePidAlive(t *testing.T) {
	withScratchMigratePaths(t)
	os.WriteFile(MigrateLogPath, []byte("migrating..."), 0644)
	os.WriteFile(MigrateProcessPIDFile, []byte(strconv.Itoa(os.Getpid())), 0644)

	m := &Migrate{}
	srv, client := newMigrateTestServer(t, m)

	resp, err := client.Get(srv.URL + "/server/migrate/status")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"status":"running"`) {
		t.Fatalf("expected running status while pid is alive, got %s", body)
	}
	if !strings.Contains(string(body), "migrating...") {
		t.Fatalf("expected log output included, got %s", body)
	}
}

func TestServeMigrateStatusFinishedWhenPidDead(t *testing.T) {
	withScratchMigratePaths(t)
	os.WriteFile(MigrateProcessPIDFile, []byte(strconv.Itoa(1<<30)), 0644)

	m := &Migrate{}
	srv, client := newMigrateTestServer(t, m)

	resp, err := client.Get(srv.URL + "/server/migrate/status")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"status":"finished"`) {
		t.Fatalf("expected finished status when the pid is dead, got %s", body)
	}
}

func TestServeMigrateStatusCorruptPidFileReturns500(t *testing.T) {
	withScratchMigratePaths(t)
	os.WriteFile(MigrateProcessPIDFile, []byte("not-a-pid"), 0644)

	m := &Migrate{}
	srv, client := newMigrateTestServer(t, m)

	resp, err := client.Get(srv.URL + "/server/migrate/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 for a corrupt pid file, got %d", resp.StatusCode)
	}
}
