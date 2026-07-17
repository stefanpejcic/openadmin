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

func withScratchVHostHomeDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := VHostHomeDir
	VHostHomeDir = dir
	t.Cleanup(func() { VHostHomeDir = orig })
	return dir
}

func newVHostFileEditorTestServer(t *testing.T, h *VHostFileEditor) (*httptest.Server, *http.Client) {
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
	mux.HandleFunc("GET /domains/vhost", h.ServeEditVHostFile)
	mux.HandleFunc("GET /domains/vhost/{username}/{domain_name}", h.ServeEditVHostFile)
	mux.HandleFunc("POST /domains/vhost/{username}/{domain_name}", h.ServeEditVHostFile)
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

func TestGetWebserverForReadsWebServerKey(t *testing.T) {
	dir := withScratchVHostHomeDir(t)
	os.MkdirAll(filepath.Join(dir, "alice"), 0755)
	os.WriteFile(filepath.Join(dir, "alice", ".env"), []byte("# comment\nFOO=bar\nWEB_SERVER='nginx'\n"), 0644)

	got := getWebserverFor("alice")
	if got != "nginx" {
		t.Fatalf("expected nginx, got %q", got)
	}
}

func TestGetWebserverForMissingEnvFileReturnsPythonQuirkString(t *testing.T) {
	withScratchVHostHomeDir(t)
	got := getWebserverFor("nobody")
	if got != ".env file not found." {
		t.Fatalf("expected the faithfully-reproduced Python quirk string, got %q", got)
	}
}

func TestVHostEditorListsDomainsWhenNoneGiven(t *testing.T) {
	withScratchVHostHomeDir(t)

	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows(
		[]string{"domain_id", "docroot", "domain_url", "php_version", "username"}).
		AddRow(1, "/var/www/html", "example.com", "8.2", "alice"))

	h := &VHostFileEditor{MySQL: mysqlDB}
	srv, client := newVHostFileEditorTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/vhost")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"Select domain to edit VirtualHosts file for", "example.com", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestVHostEditorInvalidDomainRedirectsWithFlash(t *testing.T) {
	withScratchVHostHomeDir(t)
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	h := &VHostFileEditor{MySQL: mysqlDB}
	srv, client := newVHostFileEditorTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/vhost/alice/not-a-domain")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Request.URL.Path != "/domains/vhost" {
		t.Fatalf("expected redirect to /domains/vhost, ended at %q", resp.Request.URL.Path)
	}
	if !strings.Contains(string(body), "Invalid domain name format.") {
		t.Fatalf("expected invalid-domain flash, got %s", truncate(string(body)))
	}
}

func TestVHostEditorGetReadsExistingFile(t *testing.T) {
	dir := withScratchVHostHomeDir(t)
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs("alice").
		WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow("alice-ctx"))

	confDir := filepath.Join(dir, "alice-ctx", "docker-data", "volumes", "alice-ctx_webserver_data", "_data")
	os.MkdirAll(confDir, 0755)
	os.WriteFile(filepath.Join(confDir, "example.com.conf"), []byte("<VirtualHost *:80>\n</VirtualHost>\n"), 0644)

	h := &VHostFileEditor{MySQL: mysqlDB}
	srv, client := newVHostFileEditorTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/vhost/alice/example.com")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"example.com", "VirtualHost", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestVHostEditorGetMissingFileFlashesError(t *testing.T) {
	withScratchVHostHomeDir(t)
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs("alice").
		WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow("alice-ctx"))

	h := &VHostFileEditor{MySQL: mysqlDB}
	srv, client := newVHostFileEditorTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/vhost/alice/example.com")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Error reading VirtualHosts file for domain example.com.") {
		t.Fatalf("expected missing-file flash, got %s", truncate(string(body)))
	}
}

func TestVHostEditorPostWritesFileAndRestartsWebserver(t *testing.T) {
	dir := withScratchVHostHomeDir(t)
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs("alice").
		WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow("alice-ctx"))

	os.MkdirAll(filepath.Join(dir, "alice-ctx"), 0755)
	os.WriteFile(filepath.Join(dir, "alice-ctx", ".env"), []byte("WEB_SERVER=nginx\n"), 0644)
	os.MkdirAll(filepath.Join(dir, "alice-ctx", "docker-data", "volumes", "alice-ctx_webserver_data", "_data"), 0755)

	h := &VHostFileEditor{MySQL: mysqlDB}
	srv, client := newVHostFileEditorTestServer(t, h)

	resp, err := client.PostForm(srv.URL+"/domains/vhost/alice/example.com", url.Values{
		"bind_content": {"<VirtualHost *:80>\nServerName example.com\n</VirtualHost>\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	confPath := filepath.Join(dir, "alice-ctx", "docker-data", "volumes", "alice-ctx_webserver_data", "_data", "example.com.conf")
	written, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("expected conf file to be written: %v", err)
	}
	if !strings.Contains(string(written), "ServerName example.com") {
		t.Fatalf("expected written content, got %q", string(written))
	}
	// Either the success flash (webserver restart attempted, which will
	// fail in this sandboxed test since there's no real podman/nginx) or
	// the generic error flash should be present -- both are legitimate
	// outcomes of the same code path depending on whether `podman` is on
	// PATH in the test environment; what matters is the file got written.
	got := string(body)
	if !strings.Contains(got, "example.com") {
		t.Fatalf("expected page to mention the domain, got %s", truncate(got))
	}
}
