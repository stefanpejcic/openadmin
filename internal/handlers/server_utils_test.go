package handlers

import (
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

// stubTimedatectl/stubPasswd/stubDropCache/stubSwapCycle below all replace
// the package-level command-runner vars in server_utils.go for the duration
// of a test, so no test here ever actually invokes timedatectl, passwd,
// drop_caches, or swapoff/swapon against the real host.

func withStubbedTimedatectl(t *testing.T, fn func(args ...string) (string, string, error)) {
	t.Helper()
	orig := timedatectlRun
	timedatectlRun = fn
	t.Cleanup(func() { timedatectlRun = orig })
}

func withStubbedPasswd(t *testing.T, fn func(stdin string, args ...string) (string, string, error)) {
	t.Helper()
	orig := passwdRun
	passwdRun = fn
	t.Cleanup(func() { passwdRun = orig })
}

func withStubbedDropCache(t *testing.T, fn func() error) {
	t.Helper()
	orig := dropCacheRun
	dropCacheRun = fn
	t.Cleanup(func() { dropCacheRun = orig })
}

func withStubbedSwapCycle(t *testing.T, fn func() error) {
	t.Helper()
	orig := swapCycleRun
	swapCycleRun = fn
	t.Cleanup(func() { swapCycleRun = orig })
}

func withFixtureTimezones(t *testing.T, zones []string) {
	t.Helper()
	orig := AllTimezones
	AllTimezones = func() []string { return zones }
	t.Cleanup(func() { AllTimezones = orig })
}

func newServerUtilsTestServer(t *testing.T, su *ServerUtils, role string) (*httptest.Server, *http.Client) {
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
	db.CreateUser("caller", hash, role)
	caller, err := db.UserByUsername("caller")
	if err != nil {
		t.Fatal(err)
	}

	sessions := auth.NewManager("test-secret", false)
	su.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /server/timezone", su.ServeTimezone)
	mux.HandleFunc("POST /server/timezone", su.ServeTimezone)
	mux.HandleFunc("GET /server/root-password", su.ServeRootPassword)
	mux.HandleFunc("POST /server/root-password", su.ServeRootPassword)
	mux.HandleFunc("POST /server/memory_usage/drop", su.HandleDropMemoryCache)
	mux.HandleFunc("POST /server/memory_usage/drop-swap", su.HandleDropSwapCache)
	mux.HandleFunc("GET /server/demo-mode", su.ServeDemoMode)
	mux.HandleFunc("POST /server/demo-mode", su.ServeDemoMode)
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

func TestTimezoneServeShowsCurrentAndAvailable(t *testing.T) {
	withFixtureTimezones(t, []string{"Europe/Belgrade", "America/New_York", "UTC"})
	withStubbedTimedatectl(t, func(args ...string) (string, string, error) {
		return "Europe/Belgrade", "", nil
	})

	su := &ServerUtils{}
	srv, client := newServerUtilsTestServer(t, su, "admin")

	resp, err := client.Get(srv.URL + "/server/timezone")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	for _, want := range []string{"Europe/Belgrade", "America/New_York", "Change TimeZone", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q (page may have been truncated by a template execution error), got %s", want, truncate(got))
		}
	}
}

func TestTimezoneSetRejectsInvalidZone(t *testing.T) {
	withFixtureTimezones(t, []string{"Europe/Belgrade", "UTC"})
	withStubbedTimedatectl(t, func(args ...string) (string, string, error) {
		return "UTC", "", nil
	})

	called := false
	withStubbedTimedatectl(t, func(args ...string) (string, string, error) {
		if len(args) > 0 && args[0] == "set-timezone" {
			called = true
		}
		return "UTC", "", nil
	})

	su := &ServerUtils{}
	srv, client := newServerUtilsTestServer(t, su, "admin")

	resp, err := client.PostForm(srv.URL+"/server/timezone", url.Values{"timezone": {"Not/AZone"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for an invalid timezone, got %d: %s", resp.StatusCode, body)
	}
	if called {
		t.Fatal("expected set-timezone to never be invoked for an invalid zone")
	}
}

func TestTimezoneSetAcceptsValidZone(t *testing.T) {
	withFixtureTimezones(t, []string{"Europe/Belgrade", "UTC"})
	var setTo string
	withStubbedTimedatectl(t, func(args ...string) (string, string, error) {
		if len(args) > 0 && args[0] == "set-timezone" {
			setTo = args[1]
			return "", "", nil
		}
		return "UTC", "", nil
	})

	su := &ServerUtils{}
	srv, client := newServerUtilsTestServer(t, su, "admin")

	resp, err := client.PostForm(srv.URL+"/server/timezone", url.Values{"timezone": {"Europe/Belgrade"}})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if setTo != "Europe/Belgrade" {
		t.Fatalf("expected set-timezone to be called with Europe/Belgrade, got %q", setTo)
	}
}

func TestRootPasswordRequiresAdminRole(t *testing.T) {
	su := &ServerUtils{}
	srv, client := newServerUtilsTestServer(t, su, "user") // plain "user" role, not "admin"

	resp, err := client.Get(srv.URL + "/server/root-password")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-admin role, got %d", resp.StatusCode)
	}
}

func TestDemoModeRendersHTML(t *testing.T) {
	su := &ServerUtils{}
	srv, client := newServerUtilsTestServer(t, su, "admin")

	resp, err := client.Get(srv.URL + "/server/demo-mode")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"Demo Mode", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q (page may have been truncated by a template execution error), got %s", want, truncate(got))
		}
	}
}

func TestRootPasswordRendersHTML(t *testing.T) {
	su := &ServerUtils{}
	srv, client := newServerUtilsTestServer(t, su, "admin")

	resp, err := client.Get(srv.URL + "/server/root-password")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"Change Root Password", "name=\"password\"", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q (page may have been truncated by a template execution error), got %s", want, truncate(got))
		}
	}
}

func TestRootPasswordRejectsEmptyPassword(t *testing.T) {
	called := false
	withStubbedPasswd(t, func(stdin string, args ...string) (string, string, error) {
		called = true
		return "", "", nil
	})

	su := &ServerUtils{}
	srv, client := newServerUtilsTestServer(t, su, "admin")

	resp, err := client.PostForm(srv.URL+"/server/root-password", url.Values{"password": {""}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Password cannot be empty.") {
		t.Fatalf("expected empty-password flash, got %s", truncate(string(body)))
	}
	if called {
		t.Fatal("expected passwd to never be invoked for an empty password")
	}
}

func TestRootPasswordSuccessFlow(t *testing.T) {
	var gotStdin string
	withStubbedPasswd(t, func(stdin string, args ...string) (string, string, error) {
		if len(args) > 0 && args[0] == "root" {
			gotStdin = stdin
			return "", "", nil
		}
		return "P 2026-01-01", "", nil // passwd --status root
	})

	su := &ServerUtils{}
	srv, client := newServerUtilsTestServer(t, su, "admin")

	resp, err := client.PostForm(srv.URL+"/server/root-password", url.Values{"password": {"newpw123"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "SSH password changed successfully!") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}
	if gotStdin != "newpw123\nnewpw123\n" {
		t.Fatalf("expected password piped twice to passwd, got %q", gotStdin)
	}
}

func TestRootPasswordCommandFailureFlashesStderr(t *testing.T) {
	withStubbedPasswd(t, func(stdin string, args ...string) (string, string, error) {
		return "", "authentication token manipulation error", errors.New("exit 1")
	})

	su := &ServerUtils{}
	srv, client := newServerUtilsTestServer(t, su, "admin")

	resp, err := client.PostForm(srv.URL+"/server/root-password", url.Values{"password": {"newpw123"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "authentication token manipulation error") {
		t.Fatalf("expected stderr surfaced in flash, got %s", truncate(string(body)))
	}
}

func TestDropMemoryCache(t *testing.T) {
	withStubbedDropCache(t, func() error { return nil })

	su := &ServerUtils{}
	srv, client := newServerUtilsTestServer(t, su, "admin")

	resp, err := client.Post(srv.URL+"/server/memory_usage/drop", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "success") {
		t.Fatalf("expected success JSON, got %d: %s", resp.StatusCode, body)
	}
}

func TestDropMemoryCacheFailure(t *testing.T) {
	withStubbedDropCache(t, func() error { return errors.New("permission denied") })

	su := &ServerUtils{}
	srv, client := newServerUtilsTestServer(t, su, "admin")

	resp, err := client.Post(srv.URL+"/server/memory_usage/drop", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 on failure, got %d", resp.StatusCode)
	}
}

func TestDropSwapCache(t *testing.T) {
	withStubbedSwapCycle(t, func() error { return nil })

	su := &ServerUtils{}
	srv, client := newServerUtilsTestServer(t, su, "admin")

	resp, err := client.Post(srv.URL+"/server/memory_usage/drop-swap", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d", resp.StatusCode)
	}
}
