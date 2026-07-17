package handlers

import (
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

// stubOpencliSSL installs an opencliSSLRun stub keyed by the joined args
// string, so tests never shell out to a real opencli binary.
func stubOpencliSSL(t *testing.T, responses map[string]struct {
	stdout, stderr string
	exitCode       int
	err            error
}) {
	t.Helper()
	orig := opencliSSLRun
	opencliSSLRun = func(args ...string) (string, string, int, error) {
		key := strings.Join(args, " ")
		if resp, ok := responses[key]; ok {
			return resp.stdout, resp.stderr, resp.exitCode, resp.err
		}
		return "", "not stubbed: " + key, 1, nil
	}
	t.Cleanup(func() { opencliSSLRun = orig })
}

func newSSLPageTestServer(t *testing.T, h *SSLPage) (*httptest.Server, *http.Client) {
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
	mux.HandleFunc("GET /domains/ssl/{domain_name}", h.ServeSSL)
	mux.HandleFunc("POST /domains/ssl/{domain_name}", h.ServeSSL)
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

	// RecoverMiddleware is what turns the deliberate panic() calls in
	// domains_ssl.go into the same 500 response production would return --
	// exercised below.
	handler := auth.WithUserLoader(sessions, db)(RecoverMiddleware(mux))
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

func TestSSLPageInvalidDomainRedirects(t *testing.T) {
	h := &SSLPage{}
	srv, client := newSSLPageTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/ssl/not-a-domain")
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

func TestSSLPageGetRendersWhenStatusSucceeds(t *testing.T) {
	stubOpencliSSL(t, map[string]struct {
		stdout, stderr string
		exitCode       int
		err            error
	}{
		"example.com status": {stdout: "AutoSSL\n", exitCode: 0},
		"example.com info":   {stdout: "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----\n", exitCode: 0},
	})

	h := &SSLPage{}
	srv, client := newSSLPageTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/ssl/example.com")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"Auto SSL", "BEGIN CERTIFICATE", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q, got %s", want, truncate(got))
		}
	}
}

// TestSSLPageGetCrashesWhenStatusCheckFails locks in a genuine bug (see
// domains_ssl.go's file header): `keys` is only ever assigned when the
// status check succeeds, so rendering the page whenever it doesn't hits a
// referenced-before-assignment crash -- an unhandled exception. This is
// reproduced deliberately via panic()+RecoverMiddleware rather than
// silently rendering a graceful empty state.
func TestSSLPageGetCrashesWhenStatusCheckFails(t *testing.T) {
	stubOpencliSSL(t, map[string]struct {
		stdout, stderr string
		exitCode       int
		err            error
	}{
		"example.com status": {stderr: "no certificate configured", exitCode: 1},
	})

	h := &SSLPage{}
	srv, client := newSSLPageTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/ssl/example.com")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 (reproducing the referenced-before-assignment crash), got %d: %s", resp.StatusCode, truncate(string(body)))
	}
}

func TestSSLPagePostLogsActionReturnsJSON(t *testing.T) {
	stubOpencliSSL(t, map[string]struct {
		stdout, stderr string
		exitCode       int
		err            error
	}{
		"example.com logs 1000": {stdout: `{"msg":"obtained certificate"}` + "\n", exitCode: 0},
	})

	h := &SSLPage{}
	srv, client := newSSLPageTestServer(t, h)

	resp, err := client.PostForm(srv.URL+"/domains/ssl/example.com", url.Values{"action": {"logs"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "obtained certificate") {
		t.Fatalf("expected logs JSON body, got %s", truncate(string(body)))
	}
}

// TestSSLPagePostAutosslCrashes locks in the other genuine bug: every POST
// with action=custom or action=autossl ends by trying to redirect to a
// route name that doesn't exist anywhere in this app, so both actions
// always 500.
func TestSSLPagePostAutosslCrashes(t *testing.T) {
	stubOpencliSSL(t, map[string]struct {
		stdout, stderr string
		exitCode       int
		err            error
	}{
		"example.com auto": {stdout: "AutoSSL enabled", exitCode: 0},
	})

	h := &SSLPage{}
	srv, client := newSSLPageTestServer(t, h)

	resp, err := client.PostForm(srv.URL+"/domains/ssl/example.com", url.Values{"action": {"autossl"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 (reproducing the url-build crash), got %d: %s", resp.StatusCode, truncate(string(body)))
	}
}

func TestResolveSSLKeyPathAndContainmentCheck(t *testing.T) {
	inside := resolveSSLKeyPath("/var/www/html/site/cert.crt")
	if !isRelativeToSSLHomeDir(inside) {
		t.Fatalf("expected %q to be considered inside /var/www/html/", inside)
	}
	outsideLookalike := resolveSSLKeyPath("/var/www/html-evil/cert.crt")
	if isRelativeToSSLHomeDir(outsideLookalike) {
		t.Fatalf("expected %q to NOT be considered inside /var/www/html/ (prefix lookalike)", outsideLookalike)
	}
	traversal := resolveSSLKeyPath("/var/www/html/../../etc/passwd")
	if isRelativeToSSLHomeDir(traversal) {
		t.Fatalf("expected traversal path %q to be rejected", traversal)
	}
}
