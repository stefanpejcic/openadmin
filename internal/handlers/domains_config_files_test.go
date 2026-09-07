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

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func newConfigFileEditorTestServer(t *testing.T, h *ConfigFileEditor) (*httptest.Server, *http.Client) {
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
	mux.HandleFunc("GET /domains/config", h.ServeEditConfigFile)
	mux.HandleFunc("GET /domains/config/{username}", h.ServeEditConfigFile)
	mux.HandleFunc("POST /domains/config/{username}", h.ServeEditConfigFile)
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

func TestMainConfPathForKnownWebservers(t *testing.T) {
	cases := map[string]string{
		"apache":        "httpd.conf",
		"nginx":         "nginx.conf",
		"openresty":     "openresty.conf",
		"openlitespeed": "openlitespeed.conf",
		"litespeed":     "openlitespeed.conf",
	}
	for webserver, filename := range cases {
		want := filepath.Join("/home", "alice-ctx", filename)
		got := mainConfPathFor("alice-ctx", webserver)
		if got != want {
			t.Fatalf("webserver %q: expected %q, got %q", webserver, want, got)
		}
	}
}

func TestMainConfPathForUnknownWebserverReturnsEmpty(t *testing.T) {
	if got := mainConfPathFor("alice-ctx", "bogus-webserver"); got != "" {
		t.Fatalf("expected empty path for unrecognized webserver, got %q", got)
	}
	if got := mainConfPathFor("alice-ctx", ""); got != "" {
		t.Fatalf("expected empty path when webserver is unknown, got %q", got)
	}
}

func TestConfigEditorListsDomainsWhenNoneGiven(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows(
		[]string{"domain_id", "docroot", "domain_url", "php_version", "username"}).
		AddRow(1, "/var/www/html", "example.com", "8.2", "alice"))

	h := &ConfigFileEditor{MySQL: mysqlDB}
	srv, client := newConfigFileEditorTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/config")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"Select a user's domain", "example.com", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestConfigEditorUnknownUserFlashesReadError(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs("nobody").
		WillReturnError(sqlErrConnRefused{})

	h := &ConfigFileEditor{MySQL: mysqlDB}
	srv, client := newConfigFileEditorTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/config/nobody")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "Error reading main configuration file for user nobody.") {
		t.Fatalf("expected missing-file flash, got %s", truncate(string(body)))
	}
}

func TestConfigEditorPostUnrecognizedWebserverFlashesError(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs("nobody").
		WillReturnError(sqlErrConnRefused{})

	h := &ConfigFileEditor{MySQL: mysqlDB}
	srv, client := newConfigFileEditorTestServer(t, h)

	resp, err := client.PostForm(srv.URL+"/domains/config/nobody", url.Values{
		"conf_content": {"# example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Error saving main configuration file for nobody.") {
		t.Fatalf("expected save-error flash, got %s", truncate(string(body)))
	}
}
