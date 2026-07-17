package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

var errPHPTestSetFailed = errors.New("simulated opencli failure")

func withScratchPHPPaths(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	origOptions := phpOptionsPath
	origIni := phpIniPaths
	phpOptionsPath = filepath.Join(dir, "options.txt")
	newIni := make(map[string]string, len(origIni))
	for k := range origIni {
		newIni[k] = filepath.Join(dir, k+".ini")
	}
	phpIniPaths = newIni
	t.Cleanup(func() {
		phpOptionsPath = origOptions
		phpIniPaths = origIni
	})
}

func newPHPTestServer(t *testing.T, p *PHP) (*httptest.Server, *http.Client) {
	return newPHPTestServerWithRole(t, p, "admin")
}

func newPHPTestServerWithRole(t *testing.T, p *PHP, role string) (*httptest.Server, *http.Client) {
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
	p.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /settings/php", p.ServePHP)
	mux.HandleFunc("POST /settings/php", p.ServePHP)
	mux.HandleFunc("GET /json/php/default_version/{username}", p.ServePHPDefaultVersion)
	mux.HandleFunc("POST /json/php/default_version/{username}", p.ServePHPDefaultVersion)
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

func TestServePHPGetRendersAllVersions(t *testing.T) {
	withScratchPHPPaths(t)
	os.WriteFile(phpIniPaths["php72"], []byte("memory_limit=256M"), 0644)

	p := &PHP{}
	srv, client := newPHPTestServer(t, p)

	resp, err := client.Get(srv.URL + "/settings/php")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	if !strings.Contains(got, `name="php72"`) {
		t.Fatalf("expected php72 textarea to be rendered, got %s", truncate(got))
	}
	if !strings.Contains(got, "memory_limit=256M") {
		t.Fatalf("expected php72 content in rendered page, got %s", truncate(got))
	}
	if !strings.Contains(got, "PHP 5.6 INI") {
		t.Fatalf("expected version label, got %s", truncate(got))
	}
	for _, want := range []string{"PHP Settings", "Available Options", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestServePHPGetJSON(t *testing.T) {
	withScratchPHPPaths(t)
	os.WriteFile(phpOptionsPath, []byte("upload_max_filesize=64M"), 0644)

	p := &PHP{}
	srv, client := newPHPTestServer(t, p)

	resp, err := client.Get(srv.URL + "/settings/php?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var parsed map[string]string
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("expected valid JSON, got %s: %v", body, err)
	}
	if parsed["options"] != "upload_max_filesize=64M" {
		t.Fatalf("expected options content in JSON, got %+v", parsed)
	}
	if _, ok := parsed["php72"]; !ok {
		t.Fatalf("expected php72 key present in JSON output even though it can't be saved, got %+v", parsed)
	}
}

func TestServePHPPostSavesOptions(t *testing.T) {
	withScratchPHPPaths(t)

	p := &PHP{}
	srv, client := newPHPTestServer(t, p)

	resp, err := client.PostForm(srv.URL+"/settings/php", url.Values{"options": {"memory_limit=512M"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "PHP options saved successfully!") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	saved, err := os.ReadFile(phpOptionsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != "memory_limit=512M" {
		t.Fatalf("expected options file saved, got %q", saved)
	}
}

func TestServePHPPostSavesVersionIni(t *testing.T) {
	withScratchPHPPaths(t)

	p := &PHP{}
	srv, client := newPHPTestServer(t, p)

	resp, err := client.PostForm(srv.URL+"/settings/php", url.Values{"php84": {"opcache.enable=1"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "php84 INI file saved successfully!") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	saved, err := os.ReadFile(phpIniPaths["php84"])
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != "opcache.enable=1" {
		t.Fatalf("expected php84.ini saved, got %q", saved)
	}
}

func TestServePHPPostPhp72NowSaves(t *testing.T) {
	withScratchPHPPaths(t)
	os.WriteFile(phpIniPaths["php72"], []byte("original content"), 0644)

	p := &PHP{}
	srv, client := newPHPTestServer(t, p)

	resp, err := client.PostForm(srv.URL+"/settings/php", url.Values{"php72": {"new content"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "php72 INI file saved successfully!") {
		t.Fatalf("expected php72 save to succeed, got %s", truncate(string(body)))
	}

	saved, err := os.ReadFile(phpIniPaths["php72"])
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != "new content" {
		t.Fatalf("expected php72.ini to be updated, got %q", saved)
	}
}

func TestServePHPPostOptionsTakesPriorityOverVersions(t *testing.T) {
	withScratchPHPPaths(t)

	p := &PHP{}
	srv, client := newPHPTestServer(t, p)

	resp, err := client.PostForm(srv.URL+"/settings/php", url.Values{
		"options": {"some_option=1"},
		"php84":   {"should not be saved"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "php84 INI file saved successfully!") {
		t.Fatal("expected version save to be skipped when options is non-empty")
	}

	if _, err := os.Stat(phpIniPaths["php84"]); !os.IsNotExist(err) {
		t.Fatalf("expected php84.ini to not be created, err=%v", err)
	}
}

func TestServePHPDefaultVersionResellerNotOwnerDenied(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT 1 FROM users WHERE username = ? AND owner = ? LIMIT 1`)).
		WithArgs("bob", "caller").
		WillReturnError(sql.ErrNoRows)

	p := &PHP{MySQL: db}
	srv, client := newPHPTestServerWithRole(t, p, "reseller")

	resp, err := client.Get(srv.URL + "/json/php/default_version/bob")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for a reseller who doesn't own the account, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
}

func TestServePHPDefaultVersionGETSuccess(t *testing.T) {
	origGet := phpDefaultVersionGetRun
	phpDefaultVersionGetRun = func(username string) (string, error) {
		return "Default PHP version for user '" + username + "' is: 8.2\n", nil
	}
	t.Cleanup(func() { phpDefaultVersionGetRun = origGet })

	p := &PHP{}
	srv, client := newPHPTestServer(t, p)

	resp, err := client.Get(srv.URL + "/json/php/default_version/bob")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), `"default_version":"8.2"`) {
		t.Fatalf("expected the parsed version, got %s", truncate(string(body)))
	}
}

func TestServePHPDefaultVersionGETUnexpectedOutput(t *testing.T) {
	origGet := phpDefaultVersionGetRun
	phpDefaultVersionGetRun = func(username string) (string, error) { return "garbage output", nil }
	t.Cleanup(func() { phpDefaultVersionGetRun = origGet })

	p := &PHP{}
	srv, client := newPHPTestServer(t, p)

	resp, err := client.Get(srv.URL + "/json/php/default_version/bob")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "Unexpected output format") {
		t.Fatalf("expected the unexpected-format message, got %s", truncate(string(body)))
	}
}

func TestServePHPDefaultVersionGETNonzeroExitIsNotAnError(t *testing.T) {
	// A nonzero exit code alone must not be treated as a failure on the
	// GET path.
	origGet := phpDefaultVersionGetRun
	phpDefaultVersionGetRun = func(username string) (string, error) {
		return "Default PHP version for user '" + username + "' is: 7.4\n", nil
	}
	t.Cleanup(func() { phpDefaultVersionGetRun = origGet })

	p := &PHP{}
	srv, client := newPHPTestServer(t, p)

	resp, err := client.Get(srv.URL + "/json/php/default_version/bob")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
}

func TestServePHPDefaultVersionPOSTMissingVersion(t *testing.T) {
	p := &PHP{}
	srv, client := newPHPTestServer(t, p)

	resp, err := client.Post(srv.URL+"/json/php/default_version/bob", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "Version must be provided") {
		t.Fatalf("expected the missing-version message, got %s", truncate(string(body)))
	}
}

func TestServePHPDefaultVersionPOSTSuccess(t *testing.T) {
	origSet := phpDefaultVersionSetRun
	var capturedUsername, capturedVersion string
	phpDefaultVersionSetRun = func(username, version string) error {
		capturedUsername, capturedVersion = username, version
		return nil
	}
	t.Cleanup(func() { phpDefaultVersionSetRun = origSet })

	p := &PHP{}
	srv, client := newPHPTestServer(t, p)

	resp, err := client.Post(srv.URL+"/json/php/default_version/bob", "application/json", strings.NewReader(`{"version":"8.3"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "updated to: 8.3") {
		t.Fatalf("expected the success message, got %s", truncate(string(body)))
	}
	if capturedUsername != "bob" || capturedVersion != "8.3" {
		t.Fatalf("expected opencli called with bob/8.3, got %q/%q", capturedUsername, capturedVersion)
	}
}

func TestServePHPDefaultVersionPOSTFailure(t *testing.T) {
	origSet := phpDefaultVersionSetRun
	phpDefaultVersionSetRun = func(username, version string) error { return errPHPTestSetFailed }
	t.Cleanup(func() { phpDefaultVersionSetRun = origSet })

	p := &PHP{}
	srv, client := newPHPTestServer(t, p)

	resp, err := client.Post(srv.URL+"/json/php/default_version/bob", "application/json", strings.NewReader(`{"version":"8.3"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "Failed to retrieve or update default PHP version") {
		t.Fatalf("expected the failure message, got %s", truncate(string(body)))
	}
}
