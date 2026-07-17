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

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func withScratchCaddyBackupDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := CaddyFileBackupDir
	CaddyFileBackupDir = dir
	t.Cleanup(func() { CaddyFileBackupDir = orig })
	return dir
}

// stubCaddyValidate installs a caddyValidateRun/caddyReloadRun pair that
// never shells out to a real podman/caddy binary, and restores the
// originals on test cleanup.
func stubCaddyValidate(t *testing.T, exitCode int, stderr string, reloadErr error) {
	t.Helper()
	origValidate, origReload := caddyValidateRun, caddyReloadRun
	caddyValidateRun = func() (string, string, int, error) {
		return "", stderr, exitCode, nil
	}
	caddyReloadRun = func() error { return reloadErr }
	t.Cleanup(func() {
		caddyValidateRun = origValidate
		caddyReloadRun = origReload
	})
}

func newCaddyFileEditorTestServer(t *testing.T, h *CaddyFileEditor) (*httptest.Server, *http.Client) {
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
	mux.HandleFunc("GET /domains/caddy", h.ServeEditCaddyFile)
	mux.HandleFunc("GET /domains/caddy/{domain_name}", h.ServeEditCaddyFile)
	mux.HandleFunc("POST /domains/caddy/{domain_name}", h.ServeEditCaddyFile)
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

func TestCaddyFileEditorListsDomainsWhenNoDomainGiven(t *testing.T) {
	withScratchCaddyDomainsDir(t)
	withScratchCaddyBackupDir(t)

	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows(
		[]string{"domain_id", "docroot", "domain_url", "php_version", "username"}).
		AddRow(1, "/var/www/html", "example.com", "8.2", "alice"))

	h := &CaddyFileEditor{MySQL: mysqlDB}
	srv, client := newCaddyFileEditorTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/caddy")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"Select domain to edit Caddyfile for", "example.com", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestCaddyFileEditorInvalidDomainRedirectsWithFlash(t *testing.T) {
	withScratchCaddyDomainsDir(t)
	withScratchCaddyBackupDir(t)
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	h := &CaddyFileEditor{MySQL: mysqlDB}
	srv, client := newCaddyFileEditorTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/caddy/not-a-domain")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Request.URL.Path != "/domains/caddy" {
		t.Fatalf("expected redirect to /domains/caddy, ended at %q", resp.Request.URL.Path)
	}
	if !strings.Contains(string(body), "Invalid domain name format.") {
		t.Fatalf("expected invalid-domain flash, got %s", truncate(string(body)))
	}
}

func TestCaddyFileEditorGetReadsExistingConfFile(t *testing.T) {
	confDir := withScratchCaddyDomainsDir(t)
	withScratchCaddyBackupDir(t)
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	os.WriteFile(filepath.Join(confDir, "example.com.conf"), []byte("example.com {\n\treverse_proxy localhost:8080\n}\n"), 0644)

	h := &CaddyFileEditor{MySQL: mysqlDB}
	srv, client := newCaddyFileEditorTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/caddy/example.com")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"example.com", "reverse_proxy localhost:8080", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestCaddyFileEditorGetMissingConfFileFlashesError(t *testing.T) {
	withScratchCaddyDomainsDir(t)
	withScratchCaddyBackupDir(t)
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	h := &CaddyFileEditor{MySQL: mysqlDB}
	srv, client := newCaddyFileEditorTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/caddy/example.com")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Error reading Caddy file for domain example.com.") {
		t.Fatalf("expected missing-file flash, got %s", truncate(string(body)))
	}
}

func TestCaddyFileEditorPostSuccessWritesFileAndCleansBackup(t *testing.T) {
	confDir := withScratchCaddyDomainsDir(t)
	backupDir := withScratchCaddyBackupDir(t)
	stubCaddyValidate(t, 0, "", nil)
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	confPath := filepath.Join(confDir, "example.com.conf")
	os.WriteFile(confPath, []byte("old config\n"), 0644)

	h := &CaddyFileEditor{MySQL: mysqlDB}
	srv, client := newCaddyFileEditorTestServer(t, h)

	resp, err := client.PostForm(srv.URL+"/domains/caddy/example.com", url.Values{
		"bind_content": {"new config\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "saved successfully and reloaded.") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	written, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != "new config\n" {
		t.Fatalf("expected conf file to be updated, got %q", string(written))
	}

	leftoverBackups, _ := filepath.Glob(filepath.Join(backupDir, "example.com.conf.backup_*"))
	if len(leftoverBackups) != 0 {
		t.Fatalf("expected backup to be cleaned up after success, found %v", leftoverBackups)
	}
}

func TestCaddyFileEditorPostValidationFailureRevertsFile(t *testing.T) {
	confDir := withScratchCaddyDomainsDir(t)
	withScratchCaddyBackupDir(t)
	stubCaddyValidate(t, 1, "adapting config using caddyfile: parsing caddyfile tokens", nil)
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	confPath := filepath.Join(confDir, "example.com.conf")
	os.WriteFile(confPath, []byte("original config\n"), 0644)

	h := &CaddyFileEditor{MySQL: mysqlDB}
	srv, client := newCaddyFileEditorTestServer(t, h)

	resp, err := client.PostForm(srv.URL+"/domains/caddy/example.com", url.Values{
		"bind_content": {"broken config\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Caddyfile validation failed. Changes were reverted. Error:") {
		t.Fatalf("expected validation-failure flash, got %s", truncate(string(body)))
	}

	reverted, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(reverted) != "original config\n" {
		t.Fatalf("expected conf file reverted to original content, got %q", string(reverted))
	}
}
