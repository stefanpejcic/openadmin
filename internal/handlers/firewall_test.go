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

func newFirewallTestServer(t *testing.T, f *Firewall) (*httptest.Server, *http.Client) {
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
	f.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /configservercsf/iframe/", f.ServeCSFIframe)
	mux.HandleFunc("POST /configservercsf/iframe/", f.ServeCSFIframe)
	mux.HandleFunc("GET /security/firewall", f.ServeFirewallSettings)
	mux.HandleFunc("GET /static/configservercsf/{filename...}", f.ServeCSFImages)
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

func TestServeCSFIframeInjectsCSRFToken(t *testing.T) {
	var gotTmpFileContent string
	origRun := firewallCSFRun
	firewallCSFRun = func(tmpFile string) (string, error) {
		raw, _ := readFileOrEmpty(tmpFile)
		gotTmpFileContent = raw
		return `<html><body><form method="post" action="x">stuff</form></body></html>`, nil
	}
	t.Cleanup(func() { firewallCSFRun = origRun })

	f := &Firewall{}
	srv, client := newFirewallTestServer(t, f)

	resp, err := client.Get(srv.URL + "/configservercsf/iframe/?action=list&csrf_token=ignoreme")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body), `<input type="hidden" name="csrf_token"`) {
		t.Fatalf("expected a CSRF token injected into the form, got %s", truncate(string(body)))
	}
	if !strings.Contains(gotTmpFileContent, "action=list") {
		t.Fatalf("expected query string written to temp file, got %q", gotTmpFileContent)
	}
	if strings.Contains(gotTmpFileContent, "csrf_token") {
		t.Fatalf("expected csrf_token excluded from the reconstructed query string, got %q", gotTmpFileContent)
	}
}

func TestServeCSFIframeInjectsCSRFTokenIntoEveryForm(t *testing.T) {
	origRun := firewallCSFRun
	firewallCSFRun = func(tmpFile string) (string, error) {
		return `<html><body>` +
			`<form method="post" action="x">deny</form>` +
			`<form method="post" action="y">allow</form>` +
			`<form method="post" action="z">ignore</form>` +
			`</body></html>`, nil
	}
	t.Cleanup(func() { firewallCSFRun = origRun })

	f := &Firewall{}
	srv, client := newFirewallTestServer(t, f)

	resp, err := client.Get(srv.URL + "/configservercsf/iframe/?action=list")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	got := strings.Count(string(body), `<input type="hidden" name="csrf_token"`)
	if got != 3 {
		t.Fatalf("expected a CSRF token injected into all 3 forms, got %d in %s", got, truncate(string(body)))
	}
}

func TestServeCSFIframePostUsesFormValues(t *testing.T) {
	var gotTmpFileContent string
	origRun := firewallCSFRun
	firewallCSFRun = func(tmpFile string) (string, error) {
		raw, _ := readFileOrEmpty(tmpFile)
		gotTmpFileContent = raw
		return "no form here", nil
	}
	t.Cleanup(func() { firewallCSFRun = origRun })

	f := &Firewall{}
	srv, client := newFirewallTestServer(t, f)

	resp, err := client.PostForm(srv.URL+"/configservercsf/iframe/", url.Values{"action": {"add"}, "ip": {"1.2.3.4"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "no form here" {
		t.Fatalf("expected output passed through unmodified when there's no <form>, got %q", body)
	}
	if !strings.Contains(gotTmpFileContent, "action=add") || !strings.Contains(gotTmpFileContent, "ip=1.2.3.4") {
		t.Fatalf("expected POST form values in temp file, got %q", gotTmpFileContent)
	}
}

func TestServeCSFIframeRunFailureFallsBackToGenericMessage(t *testing.T) {
	origRun := firewallCSFRun
	firewallCSFRun = func(tmpFile string) (string, error) { return "", &ftpStubError{"exec: csf.pl: not found"} }
	t.Cleanup(func() { firewallCSFRun = origRun })

	f := &Firewall{}
	srv, client := newFirewallTestServer(t, f)

	resp, err := client.Get(srv.URL + "/configservercsf/iframe/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Unable to create csf UI temp file") {
		t.Fatalf("expected the (mislabeled, matching the original) generic error, got %s", truncate(string(body)))
	}
}

func TestServeFirewallSettingsAvailable(t *testing.T) {
	origRun := firewallCommandAvailableRun
	firewallCommandAvailableRun = func(command string) bool { return command == "csf" }
	t.Cleanup(func() { firewallCommandAvailableRun = origRun })

	f := &Firewall{}
	srv, client := newFirewallTestServer(t, f)

	resp, err := client.Get(srv.URL + "/security/firewall")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	got := string(body)
	for _, want := range []string{"/configservercsf/iframe/", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q (page may have been truncated by a template execution error), got %s", want, truncate(got))
		}
	}
}

func TestServeCSFImagesServesAllowedFile(t *testing.T) {
	dir := t.TempDir()
	origDir := csfImagesDir
	csfImagesDir = dir
	t.Cleanup(func() { csfImagesDir = origDir })

	if err := os.WriteFile(filepath.Join(dir, "allow.gif"), []byte("gif89a"), 0644); err != nil {
		t.Fatal(err)
	}

	f := &Firewall{}
	srv, client := newFirewallTestServer(t, f)

	resp, err := client.Get(srv.URL + "/static/configservercsf/allow.gif")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if string(body) != "gif89a" {
		t.Fatalf("expected file contents served, got %s", truncate(string(body)))
	}
}

func TestServeCSFImagesBlocksTraversal(t *testing.T) {
	dir := t.TempDir()
	origDir := csfImagesDir
	csfImagesDir = dir
	t.Cleanup(func() { csfImagesDir = origDir })

	f := &Firewall{}
	srv, client := newFirewallTestServer(t, f)

	resp, err := client.Get(srv.URL + "/static/configservercsf/../../../../etc/passwd")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("expected traversal attempt to be rejected, got 200")
	}
}

func TestServeFirewallSettingsUnavailable(t *testing.T) {
	origRun := firewallCommandAvailableRun
	firewallCommandAvailableRun = func(command string) bool { return false }
	t.Cleanup(func() { firewallCommandAvailableRun = origRun })

	f := &Firewall{}
	srv, client := newFirewallTestServer(t, f)

	resp, err := client.Get(srv.URL + "/security/firewall")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 when csf isn't available, got %d", resp.StatusCode)
	}
	got := string(body)
	for _, want := range []string{"ConfigServer Firewall (CSF) is not available on this system.", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected chrome-wrapped error page to contain %q, got %s", want, truncate(got))
		}
	}
}
