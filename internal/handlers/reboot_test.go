package handlers

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func withScratchRebootDisableFlag(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := RebootDisableFlagPath
	RebootDisableFlagPath = filepath.Join(dir, "disable_openadmin_reboot_ui")
	t.Cleanup(func() { RebootDisableFlagPath = orig })
	return RebootDisableFlagPath
}

func newRebootTestServer(t *testing.T, rb *Reboot) (*httptest.Server, *http.Client) {
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
	rb.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /server/reboot", rb.ServeReboot)
	mux.HandleFunc("POST /server/reboot", rb.ServeReboot)
	mux.HandleFunc("GET /server/reboot/status", rb.ServeRebootStatus)
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

func TestServeRebootDisabledFlagForbidden(t *testing.T) {
	flagPath := withScratchRebootDisableFlag(t)
	os.WriteFile(flagPath, []byte(""), 0644)

	rb := &Reboot{}
	srv, client := newRebootTestServer(t, rb)

	resp, err := client.Get(srv.URL + "/server/reboot")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 when disable flag is present, got %d: %s", resp.StatusCode, body)
	}
}

func TestServeRebootGetShowsForm(t *testing.T) {
	withScratchRebootDisableFlag(t) // not created -> reboot allowed

	rb := &Reboot{}
	srv, client := newRebootTestServer(t, rb)

	resp, err := client.Get(srv.URL + "/server/reboot")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	for _, want := range []string{`name="reboot_type"`, "Server Reboot", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q (page may have been truncated by a template execution error), got %s", want, truncate(got))
		}
	}
	if strings.Contains(got, "progress_reboot") {
		t.Fatalf("expected no in-progress markup before a POST, got %s", truncate(got))
	}
}

func TestServeRebootPostGracefulCallsRunner(t *testing.T) {
	withScratchRebootDisableFlag(t)

	called := ""
	origGraceful, origHard := rebootGracefulRun, rebootHardRun
	rebootGracefulRun = func() error { called = "graceful"; return nil }
	rebootHardRun = func() error { called = "hard"; return nil }
	t.Cleanup(func() { rebootGracefulRun, rebootHardRun = origGraceful, origHard })

	rb := &Reboot{}
	srv, client := newRebootTestServer(t, rb)

	resp, err := client.PostForm(srv.URL+"/server/reboot", url.Values{"reboot_type": {"graceful"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if called != "graceful" {
		t.Fatalf("expected rebootGracefulRun to be called, got %q", called)
	}
	if !strings.Contains(string(body), "reboot is now in progress") {
		t.Fatalf("expected in-progress markup after POST, got %s", truncate(string(body)))
	}
}

func TestServeRebootPostHardCallsRunner(t *testing.T) {
	withScratchRebootDisableFlag(t)

	called := ""
	origGraceful, origHard := rebootGracefulRun, rebootHardRun
	rebootGracefulRun = func() error { called = "graceful"; return nil }
	rebootHardRun = func() error { called = "hard"; return nil }
	t.Cleanup(func() { rebootGracefulRun, rebootHardRun = origGraceful, origHard })

	rb := &Reboot{}
	srv, client := newRebootTestServer(t, rb)

	resp, err := client.PostForm(srv.URL+"/server/reboot", url.Values{"reboot_type": {"hard"}})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if called != "hard" {
		t.Fatalf("expected rebootHardRun to be called, got %q", called)
	}
}

func TestServeRebootPostRunnerErrorReturns500(t *testing.T) {
	withScratchRebootDisableFlag(t)

	origGraceful := rebootGracefulRun
	rebootGracefulRun = func() error { return &ftpStubError{"boom"} }
	t.Cleanup(func() { rebootGracefulRun = origGraceful })

	rb := &Reboot{}
	srv, client := newRebootTestServer(t, rb)

	resp, err := client.PostForm(srv.URL+"/server/reboot", url.Values{"reboot_type": {"graceful"}})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 when the runner fails, got %d", resp.StatusCode)
	}
}

func TestServeRebootStatusReturnsUp(t *testing.T) {
	withScratchRebootDisableFlag(t)

	rb := &Reboot{}
	srv, client := newRebootTestServer(t, rb)

	resp, err := client.Get(srv.URL + "/server/reboot/status")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"status":"up"`) {
		t.Fatalf(`expected {"status":"up"}, got %s`, body)
	}
}
